package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/spf13/viper"
	"github.com/zelenin/go-tdlib/client"
)

// Config holds the application configuration loaded from config.json
type Config struct {
	ApiId                int32   `mapstructure:"api_id"`
	ApiHash              string  `mapstructure:"api_hash"`
	SourceChatIds        []int64 `mapstructure:"source_chat_ids"`
	TargetChatIds        []int64 `mapstructure:"target_chat_ids"`
	AlertChatIds         []int64 `mapstructure:"alert_chat_ids"`
	AlertIntervalSeconds int     `mapstructure:"alert_interval_seconds"`
	AlertMaxCount        int     `mapstructure:"alert_max_count"`
}

func main() {
	// Load config
	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Failed to read config: %s", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		log.Fatalf("Failed to unmarshal config: %s", err)
	}

	if cfg.AlertIntervalSeconds <= 0 {
		cfg.AlertIntervalSeconds = 60
	}
	if cfg.AlertMaxCount <= 0 {
		cfg.AlertMaxCount = 10
	}
	log.Printf("Alert interval: %ds, max count: %d", cfg.AlertIntervalSeconds, cfg.AlertMaxCount)

	// Build source chat ID set for quick lookup
	sourceSet := make(map[int64]bool)
	for _, id := range cfg.SourceChatIds {
		sourceSet[id] = true
	}

	// Build alert chat ID set for quick reply detection
	alertSet := make(map[int64]bool)
	for _, id := range cfg.AlertChatIds {
		alertSet[id] = true
	}

	log.Printf("Source chat IDs: %v", cfg.SourceChatIds)
	log.Printf("Target chat IDs: %v", cfg.TargetChatIds)
	log.Printf("Alert chat IDs: %v", cfg.AlertChatIds)

	tdlibParameters := &client.SetTdlibParametersRequest{
		UseTestDc:           false,
		DatabaseDirectory:   filepath.Join(".tdlib", "database"),
		FilesDirectory:      filepath.Join(".tdlib", "files"),
		UseFileDatabase:     true,
		UseChatInfoDatabase: true,
		UseMessageDatabase:  true,
		UseSecretChats:      false,
		ApiId:               cfg.ApiId,
		ApiHash:             cfg.ApiHash,
		SystemLanguageCode:  "en",
		DeviceModel:         "Server",
		SystemVersion:       "1.0.0",
		ApplicationVersion:  "1.0.0",
	}

	authorizer := client.ClientAuthorizer(tdlibParameters)
	go client.CliInteractor(authorizer)

	_, err := client.SetLogVerbosityLevel(&client.SetLogVerbosityLevelRequest{
		NewVerbosityLevel: 1,
	})
	if err != nil {
		log.Fatalf("SetLogVerbosityLevel error: %s", err)
	}

	tdlibClient, err := client.NewClient(authorizer)
	if err != nil {
		log.Fatalf("NewClient error: %s", err)
	}

	// Keep this session marked as an active foreground client. Without this,
	// TDLib defaults to "offline" and the Telegram server treats the session
	// as a background secondary client — updates can be deferred until the
	// account's primary device (e.g. the phone) comes online, which manifests
	// as forwards/alerts only firing after the user unlocks their phone.
	if _, err := tdlibClient.SetOption(&client.SetOptionRequest{
		Name:  "online",
		Value: &client.OptionValueBoolean{Value: true},
	}); err != nil {
		log.Printf("SetOption online=true failed: %s", err)
	}
	// Process every update even when the client isn't considered "in use".
	if _, err := tdlibClient.SetOption(&client.SetOptionRequest{
		Name:  "ignore_background_updates",
		Value: &client.OptionValueBoolean{Value: false},
	}); err != nil {
		log.Printf("SetOption ignore_background_updates=false failed: %s", err)
	}

	listener := tdlibClient.GetListener()
	go func() {
		defer listener.Close()
		for update := range listener.Updates {
			// Read-receipt path: the recipient read up to last_read_outbox_message_id.
			// If that's >= our session's first "1", consider the alert acknowledged.
			if u, ok := update.(*client.UpdateChatReadOutbox); ok {
				if alertSet[u.ChatId] {
					alertMu.Lock()
					s, exists := alertSessions[u.ChatId]
					shouldStop := exists && s.watchMsgId != 0 && u.LastReadOutboxMessageId >= s.watchMsgId
					alertMu.Unlock()
					if shouldStop {
						stopAlert(u.ChatId, "read receipt")
					}
				}
				continue
			}

			// When the first "1" is confirmed by the server, swap its temporary
			// id for the real one. Until this fires, watchMsgId stays 0 and the
			// read-receipt branch above no-ops, so stale pre-session reads can't
			// kill a fresh alert.
			if u, ok := update.(*client.UpdateMessageSendSucceeded); ok {
				if u.Message != nil && alertSet[u.Message.ChatId] {
					alertMu.Lock()
					if s, exists := alertSessions[u.Message.ChatId]; exists && s.pendingTmpId == u.OldMessageId {
						s.watchMsgId = u.Message.Id
						s.pendingTmpId = 0
					}
					alertMu.Unlock()
				}
				continue
			}

			newMsg, ok := update.(*client.UpdateNewMessage)
			if !ok {
				continue
			}

			msg := newMsg.Message
			log.Printf("New message from source chat %d, type: %s", msg.ChatId, msg.Content.MessageContentType())

			// An alert recipient replied (any incoming message) -> stop its alert loop.
			// Skip outgoing messages, otherwise our own "1" sends would self-cancel.
			// This is the fallback path; the primary trigger is the read-receipt
			// handler below (UpdateChatReadOutbox).
			if !msg.IsOutgoing && alertSet[msg.ChatId] {
				stopAlert(msg.ChatId, "got reply")
			}

			if !sourceSet[msg.ChatId] {
				continue
			}

			// Build the source label "群名 · 发送者 · id" once, reused for all targets.
			prefix := buildSourcePrefix(tdlibClient, msg)

			// Album (media group): an N-image message arrives as N separate messages
			// sharing one MediaAlbumId. Buffer them and flush as one grouped album.
			if msg.MediaAlbumId != 0 {
				bufferAlbumMessage(tdlibClient, cfg, int64(msg.MediaAlbumId), msg.Content, prefix)
				continue
			}

			inputContent := convertMessageContent(msg.Content, prefix)
			if inputContent == nil {
				log.Printf("Unsupported message content type: %s, skipping", msg.Content.MessageContentType())
				continue
			}

			// Stickers carry no caption, so send the source label as a separate header first.
			_, isSticker := msg.Content.(*client.MessageSticker)

			for _, targetId := range cfg.TargetChatIds {
				if isSticker {
					_, err := tdlibClient.SendMessage(&client.SendMessageRequest{
						ChatId: targetId,
						InputMessageContent: &client.InputMessageText{
							Text: &client.FormattedText{Text: "【" + prefix + "】"},
						},
					})
					if err != nil {
						log.Printf("Failed to send source header to chat %d: %s", targetId, err)
					}
				}

				_, err := tdlibClient.SendMessage(&client.SendMessageRequest{
					ChatId:              targetId,
					InputMessageContent: inputContent,
				})
				if err != nil {
					log.Printf("Failed to send message to chat %d: %s", targetId, err)
				} else {
					log.Printf("Message forwarded to chat %d", targetId)
				}
			}

			// Forward triggered: start (or reset) the "1" alert loop for each alert id.
			for _, id := range cfg.AlertChatIds {
				startAlert(tdlibClient, id, cfg.AlertIntervalSeconds, cfg.AlertMaxCount)
			}
		}
	}()

	me, err := tdlibClient.GetMe()
	if err != nil {
		log.Fatalf("GetMe error: %s", err)
	}

	log.Printf("Logged in as: %s %s", me.FirstName, me.LastName)

	printAllChats(tdlibClient)

	log.Printf("Listening for messages from source chats...")

	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	log.Println("Shutting down...")
	tdlibClient.Close()
	os.Exit(0)
}

// alertSession identifies one running alert loop. The pointer is used as an
// identity token so a finishing goroutine only cleans up its own map entry
// (function values are not comparable, so a pointer is used instead).
//
// watchMsgId is the server-assigned id of this session's first "1" — set by
// the UpdateMessageSendSucceeded handler. When the recipient's read-outbox
// pointer reaches it, the loop is cancelled. pendingTmpId holds the local
// temporary id while the send is in flight.
type alertSession struct {
	cancel       context.CancelFunc
	pendingTmpId int64
	watchMsgId   int64
}

var (
	alertMu       sync.Mutex
	alertSessions = map[int64]*alertSession{}
)

// startAlert starts (or resets) a "1" alert loop for a chat id. intervalSeconds
// and maxCount come from config; together they bound the alert window.
func startAlert(c *client.Client, chatId int64, intervalSeconds, maxCount int) {
	alertMu.Lock()
	if old, ok := alertSessions[chatId]; ok {
		old.cancel() // re-trigger: cancel the old loop and restart the timer
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &alertSession{cancel: cancel}
	alertSessions[chatId] = s
	alertMu.Unlock()

	go runAlertLoop(ctx, c, chatId, s, intervalSeconds, maxCount)
}

// runAlertLoop sends "1" immediately, then once every intervalSeconds, maxCount
// times total, unless cancelled by a reply or a reset.
func runAlertLoop(ctx context.Context, c *client.Client, chatId int64, s *alertSession, intervalSeconds, maxCount int) {
	defer func() {
		alertMu.Lock()
		if alertSessions[chatId] == s { // only clean up our own entry
			delete(alertSessions, chatId)
		}
		alertMu.Unlock()
	}()

	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()
	for i := 0; i < maxCount; i++ {
		sent, err := c.SendMessage(&client.SendMessageRequest{
			ChatId: chatId,
			InputMessageContent: &client.InputMessageText{
				Text: &client.FormattedText{Text: "1"},
			},
		})
		if err != nil {
			log.Printf("Failed to send alert '1' to %d: %s", chatId, err)
		} else {
			log.Printf("Alert '1' sent to %d (%d/%d)", chatId, i+1, maxCount)
			// Record the first "1"'s temporary id so the SendSucceeded handler
			// can swap it for the server id we compare against read receipts.
			if i == 0 && sent != nil {
				alertMu.Lock()
				if alertSessions[chatId] == s {
					s.pendingTmpId = sent.Id
				}
				alertMu.Unlock()
			}
		}

		select {
		case <-ctx.Done():
			return // got a reply or was reset
		case <-ticker.C:
		}
	}
}

// stopAlert stops an alert loop for a chat id. reason is logged so the two
// trigger paths (read receipt / any reply) are distinguishable.
func stopAlert(chatId int64, reason string) {
	alertMu.Lock()
	if s, ok := alertSessions[chatId]; ok {
		s.cancel()
		delete(alertSessions, chatId)
		log.Printf("Alert for %d stopped (%s)", chatId, reason)
	}
	alertMu.Unlock()
}

// albumFlushDelay is how long to wait after the last message of an album before
// flushing it. Album messages arrive back-to-back, so a short window collects them.
const albumFlushDelay = time.Second

// albumBuffer collects the converted contents of one media group (album).
type albumBuffer struct {
	timer    *time.Timer
	contents []client.InputMessageContent
}

var (
	albumMu       sync.Mutex
	pendingAlbums = map[int64]*albumBuffer{}
)

// bufferAlbumMessage appends one album item to its buffer and (re)arms the flush
// timer. The source label is applied to the first item only.
func bufferAlbumMessage(c *client.Client, cfg Config, albumId int64, content client.MessageContent, prefix string) {
	albumMu.Lock()
	defer albumMu.Unlock()

	buf, ok := pendingAlbums[albumId]
	itemPrefix := "" // only the first item carries the "【来源】" label
	if !ok {
		buf = &albumBuffer{}
		pendingAlbums[albumId] = buf
		itemPrefix = prefix
	}

	if input := convertMessageContent(content, itemPrefix); input != nil {
		buf.contents = append(buf.contents, input)
	}

	if buf.timer != nil {
		buf.timer.Stop()
	}
	buf.timer = time.AfterFunc(albumFlushDelay, func() {
		flushAlbum(c, cfg, albumId)
	})
}

// flushAlbum sends a buffered album to all targets as one grouped message,
// then triggers the alert loop once for the whole album.
func flushAlbum(c *client.Client, cfg Config, albumId int64) {
	albumMu.Lock()
	buf, ok := pendingAlbums[albumId]
	delete(pendingAlbums, albumId)
	albumMu.Unlock()

	if !ok || len(buf.contents) == 0 {
		return
	}

	for _, targetId := range cfg.TargetChatIds {
		var err error
		if len(buf.contents) == 1 {
			// Only one deliverable item survived: a normal message, not an album.
			_, err = c.SendMessage(&client.SendMessageRequest{
				ChatId:              targetId,
				InputMessageContent: buf.contents[0],
			})
		} else {
			_, err = c.SendMessageAlbum(&client.SendMessageAlbumRequest{
				ChatId:               targetId,
				InputMessageContents: buf.contents,
			})
		}
		if err != nil {
			log.Printf("Failed to send album to chat %d: %s", targetId, err)
		} else {
			log.Printf("Album (%d items) forwarded to chat %d", len(buf.contents), targetId)
		}
	}

	for _, id := range cfg.AlertChatIds {
		startAlert(c, id, cfg.AlertIntervalSeconds, cfg.AlertMaxCount)
	}
}

// convertMessageContent converts a received MessageContent into the
// corresponding InputMessageContent for re-sending (content copy, not forward).
func convertMessageContent(content client.MessageContent, prefix string) client.InputMessageContent {
	switch c := content.(type) {
	case *client.MessageText:
		return &client.InputMessageText{
			Text:       prependPrefix(c.Text, prefix),
			ClearDraft: true,
		}

	case *client.MessagePhoto:
		if c.Photo == nil || len(c.Photo.Sizes) == 0 {
			return nil
		}
		// Use the largest available photo size
		largest := c.Photo.Sizes[len(c.Photo.Sizes)-1]
		return &client.InputMessagePhoto{
			Photo:   &client.InputFileRemote{Id: largest.Photo.Remote.Id},
			Caption: prependPrefix(c.Caption, prefix),
		}

	case *client.MessageVideo:
		if c.Video == nil {
			return nil
		}
		return &client.InputMessageVideo{
			Video:   &client.InputFileRemote{Id: c.Video.Video.Remote.Id},
			Caption: prependPrefix(c.Caption, prefix),
		}

	case *client.MessageDocument:
		if c.Document == nil {
			return nil
		}
		return &client.InputMessageDocument{
			Document: &client.InputFileRemote{Id: c.Document.Document.Remote.Id},
			Caption:  prependPrefix(c.Caption, prefix),
		}

	case *client.MessageAnimation:
		if c.Animation == nil {
			return nil
		}
		return &client.InputMessageAnimation{
			Animation: &client.InputFileRemote{Id: c.Animation.Animation.Remote.Id},
			Caption:   prependPrefix(c.Caption, prefix),
		}

	case *client.MessageAudio:
		if c.Audio == nil {
			return nil
		}
		return &client.InputMessageAudio{
			Audio:   &client.InputFileRemote{Id: c.Audio.Audio.Remote.Id},
			Caption: prependPrefix(c.Caption, prefix),
		}

	case *client.MessageSticker:
		if c.Sticker == nil {
			return nil
		}
		// Stickers can't carry a caption; the source label is sent as a separate header.
		return &client.InputMessageSticker{
			Sticker: &client.InputFileRemote{Id: c.Sticker.Sticker.Remote.Id},
			Emoji:   c.Sticker.Emoji,
		}

	case *client.MessageVoiceNote:
		if c.VoiceNote == nil {
			return nil
		}
		return &client.InputMessageVoiceNote{
			VoiceNote: &client.InputFileRemote{Id: c.VoiceNote.Voice.Remote.Id},
			Caption:   prependPrefix(c.Caption, prefix),
		}

	default:
		return nil
	}
}

// printAllChats loads the account's main chat list and logs the title, id and
// type of every chat, so the user can copy the ids they need into config.json.
func printAllChats(c *client.Client) {
	// GetChats only returns already-loaded chats, so first pull the whole main
	// chat list via LoadChats. It returns a 404 error once everything is loaded.
	for {
		if _, err := c.LoadChats(&client.LoadChatsRequest{Limit: 100}); err != nil {
			break // reached the end of the list (or nothing to load)
		}
	}

	chats, err := c.GetChats(&client.GetChatsRequest{Limit: 1000})
	if err != nil {
		log.Printf("Failed to list chats: %s", err)
		return
	}

	log.Printf("===== 所有会话列表 (%d) =====", len(chats.ChatIds))
	for _, id := range chats.ChatIds {
		chat, err := c.GetChat(&client.GetChatRequest{ChatId: id})
		if err != nil {
			log.Printf("[未知] (id=%d) 获取失败: %s", id, err)
			continue
		}
		log.Printf("[%s] %s (id=%d)", chatTypeLabel(chat.Type), chat.Title, chat.Id)
	}
	log.Printf("===== 会话列表结束 =====")
}

// chatTypeLabel returns a human-readable Chinese label for a chat type.
func chatTypeLabel(t client.ChatType) string {
	switch ct := t.(type) {
	case *client.ChatTypePrivate:
		return "私聊"
	case *client.ChatTypeBasicGroup:
		return "群组"
	case *client.ChatTypeSupergroup:
		if ct.IsChannel {
			return "频道"
		}
		return "超级群"
	case *client.ChatTypeSecret:
		return "密聊"
	default:
		return "未知"
	}
}

// buildSourcePrefix builds a source label like "群名 · 张三 · 123456789".
// For a private chat it collapses to "对方名 · 123456789" (no repeated name).
func buildSourcePrefix(c *client.Client, msg *client.Message) string {
	title := chatTitle(c, msg.ChatId)
	name, sid := resolveSender(c, msg.SenderId)
	if sid == msg.ChatId || name == "" {
		return fmt.Sprintf("%s · %d", title, sid)
	}
	return fmt.Sprintf("%s · %s · %d", title, name, sid)
}

// chatTitle returns a chat's display name (group title or peer user name),
// falling back to the numeric id when it can't be fetched.
func chatTitle(c *client.Client, chatId int64) string {
	chat, err := c.GetChat(&client.GetChatRequest{ChatId: chatId})
	if err != nil {
		return fmt.Sprintf("%d", chatId)
	}
	return chat.Title
}

// resolveSender returns the message sender's display name and numeric id.
func resolveSender(c *client.Client, sender client.MessageSender) (string, int64) {
	switch s := sender.(type) {
	case *client.MessageSenderUser:
		if u, err := c.GetUser(&client.GetUserRequest{UserId: s.UserId}); err == nil {
			return strings.TrimSpace(u.FirstName + " " + u.LastName), s.UserId
		}
		return "", s.UserId
	case *client.MessageSenderChat:
		return chatTitle(c, s.ChatId), s.ChatId
	}
	return "", 0
}

// prependPrefix prepends "【prefix】\n" to a FormattedText, shifting all entity
// offsets by the prefix length in UTF-16 code units so formatting stays aligned.
func prependPrefix(ft *client.FormattedText, prefix string) *client.FormattedText {
	if prefix == "" {
		return ft // empty label (e.g. non-first album item): leave caption untouched
	}
	header := "【" + prefix + "】\n"
	shift := int32(len(utf16.Encode([]rune(header))))
	out := &client.FormattedText{Text: header}
	if ft != nil {
		out.Text += ft.Text
		for _, e := range ft.Entities {
			out.Entities = append(out.Entities, &client.TextEntity{
				Offset: e.Offset + shift,
				Length: e.Length,
				Type:   e.Type,
			})
		}
	}
	return out
}
