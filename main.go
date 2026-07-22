package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/viper"
	"github.com/zelenin/go-tdlib/client"
)

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
	if cfg.ChannelPollSeconds <= 0 {
		cfg.ChannelPollSeconds = 30
	}
	log.Printf("Alert interval: %ds, max count: %d, channel poll: %ds",
		cfg.AlertIntervalSeconds, cfg.AlertMaxCount, cfg.ChannelPollSeconds)

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

	startListener(tdlibClient, cfg, sourceSet, alertSet)

	me, err := tdlibClient.GetMe()
	if err != nil {
		log.Fatalf("GetMe error: %s", err)
	}

	log.Printf("Logged in as: %s %s", me.FirstName, me.LastName)

	printAllChats(tdlibClient)

	// Mark every watched chat as opened so this session starts as a subscriber.
	// Required by TDLib for supergroup/channel realtime updates; openChat on a
	// previously-closed chat also triggers updates.getChannelDifference, which
	// is what flushes any pending pts gap on the server side.
	openedChats := openWatchedChats(tdlibClient, cfg)

	// Telegram only push-broadcasts a channel's updates to sessions it
	// considers actively subscribed. A one-shot openChat at startup is enough
	// for the first batch, but the server then downgrades us once we go quiet
	// — which is why messages used to arrive only when the user re-opened the
	// chat on their phone. Periodically toggling closeChat→openChat re-runs
	// updates.getChannelDifference and keeps this session on the broadcast list.
	// Missed messages flow back through the normal UpdateNewMessage path.
	// pollCtx, stopPoller := context.WithCancel(context.Background())
	// go pollWatchedChats(pollCtx, tdlibClient, openedChats, time.Duration(cfg.ChannelPollSeconds)*time.Second)

	log.Printf("Listening for messages from source chats...")

	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	log.Println("Shutting down...")
	// stopPoller()
	for _, id := range openedChats {
		if _, err := tdlibClient.CloseChat(&client.CloseChatRequest{ChatId: id}); err != nil {
			log.Printf("CloseChat %d failed: %s", id, err)
		}
	}
	tdlibClient.Close()
	os.Exit(0)
}
