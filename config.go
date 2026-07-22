package main

// Config holds the application configuration loaded from config.json
type Config struct {
	ApiId         int32   `mapstructure:"api_id"`
	ApiHash       string  `mapstructure:"api_hash"`
	SourceChatIds []int64 `mapstructure:"source_chat_ids"`
	TargetChatIds []int64 `mapstructure:"target_chat_ids"`
	AlertChatIds  []int64 `mapstructure:"alert_chat_ids"`
	// AlertSourceChatIds 是 source_chat_ids 的白名单子集:仅当来源命中这里
	// 列出的 id 时才触发 alert_chat_ids 的「1」催办。留空/不填 = 所有
	// source_chat_ids 均触发(保留旧行为)。转发本身不受此配置影响。
	AlertSourceChatIds   []int64 `mapstructure:"alert_source_chat_ids"`
	AlertIntervalSeconds int     `mapstructure:"alert_interval_seconds"`
	AlertMaxCount        int     `mapstructure:"alert_max_count"`
	ChannelPollSeconds   int     `mapstructure:"channel_poll_seconds"`
}
