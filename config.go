package main

// Config holds the application configuration loaded from config.json
type Config struct {
	ApiId                int32   `mapstructure:"api_id"`
	ApiHash              string  `mapstructure:"api_hash"`
	SourceChatIds        []int64 `mapstructure:"source_chat_ids"`
	TargetChatIds        []int64 `mapstructure:"target_chat_ids"`
	AlertChatIds         []int64 `mapstructure:"alert_chat_ids"`
	AlertIntervalSeconds int     `mapstructure:"alert_interval_seconds"`
	AlertMaxCount        int     `mapstructure:"alert_max_count"`
	ChannelPollSeconds   int     `mapstructure:"channel_poll_seconds"`
}
