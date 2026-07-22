package main

import (
	"log"
	"time"

	"github.com/zelenin/go-tdlib/client"
)

// startListener subscribes to the TDLib update stream in a background goroutine.
// It returns immediately after spawning; the loop runs until the listener is
// closed (which happens when the TDLib client is closed during shutdown).
// alertSourceSet is the optional white­list controlling which source chat ids
// trigger alert_chat_ids. Empty set = every source triggers (legacy behavior).
func startListener(c *client.Client, cfg Config, sourceSet, alertSet, alertSourceSet map[int64]bool) {
	// Any message older than this is from before we booted — almost certainly
	// a historical message TDLib is replaying as part of the startup
	// openChat/getChannelDifference catch-up. Forwarding those would spam
	// targets with old content on every restart.
	startTime := int32(time.Now().Unix())

	listener := c.GetListener()
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
			if msg.Date < startTime {
				continue // pre-boot message replayed by startup catch-up
			}
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
			prefix := buildSourcePrefix(c, msg)

			// Album (media group): an N-image message arrives as N separate messages
			// sharing one MediaAlbumId. Buffer them and flush as one grouped album.
			if msg.MediaAlbumId != 0 {
				bufferAlbumMessage(c, cfg, int64(msg.MediaAlbumId), msg.Content, prefix)
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
					_, err := c.SendMessage(&client.SendMessageRequest{
						ChatId: targetId,
						InputMessageContent: &client.InputMessageText{
							Text: &client.FormattedText{Text: "【" + prefix + "】"},
						},
					})
					if err != nil {
						log.Printf("Failed to send source header to chat %d: %s", targetId, err)
					}
				}

				_, err := c.SendMessage(&client.SendMessageRequest{
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
			// alertSourceSet is a whitelist — empty means every source triggers.
			if len(alertSourceSet) == 0 || alertSourceSet[msg.ChatId] {
				for _, id := range cfg.AlertChatIds {
					startAlert(c, id, cfg.AlertIntervalSeconds, cfg.AlertMaxCount)
				}
			}
		}
	}()
}
