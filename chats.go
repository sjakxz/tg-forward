package main

import (
	"context"
	"log"
	"time"

	"github.com/zelenin/go-tdlib/client"
)

// openWatchedChats calls openChat on every source chat so this session is
// registered as a realtime subscriber for them. Alert chats are skipped on
// purpose: we only send to them and listen for replies, and replies on
// regular private/group chats are pushed without needing keepalive — the
// broadcast-channel pts dance only matters for source channels we read from.
// GetChat is called first to make sure TDLib has loaded the chat into memory;
// openChat on an unknown id fails. Returns the ids that were successfully
// opened so they can be closed on shutdown and toggled by pollWatchedChats.
func openWatchedChats(c *client.Client, cfg Config) []int64 {
	seen := make(map[int64]bool)
	var ids []int64
	for _, id := range cfg.SourceChatIds {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	var opened []int64
	for _, id := range ids {
		if _, err := c.GetChat(&client.GetChatRequest{ChatId: id}); err != nil {
			log.Printf("GetChat %d failed (can't open): %s", id, err)
			continue
		}
		if _, err := c.OpenChat(&client.OpenChatRequest{ChatId: id}); err != nil {
			log.Printf("OpenChat %d failed: %s", id, err)
			continue
		}
		opened = append(opened, id)
		log.Printf("Opened chat %d for realtime updates", id)
	}
	return opened
}

// pollWatchedChats toggles closeChat→openChat on each watched chat every
// interval, simulating the user re-opening the chat on a phone. openChat
// only triggers updates.getChannelDifference on a closed→open transition,
// so the close call is required — re-calling openChat alone is a no-op.
// The getChannelDifference call is what tells the Telegram server "this
// session is still actively subscribed", keeping it on the broadcast list
// for that channel. Failures are non-fatal; the next tick retries.
func pollWatchedChats(ctx context.Context, c *client.Client, chatIds []int64, interval time.Duration) {
	if len(chatIds) == 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, id := range chatIds {
				if _, err := c.CloseChat(&client.CloseChatRequest{ChatId: id}); err != nil {
					log.Printf("Channel keepalive close %d failed: %s", id, err)
					continue
				}
				if _, err := c.OpenChat(&client.OpenChatRequest{ChatId: id}); err != nil {
					log.Printf("Channel keepalive open %d failed: %s", id, err)
				}
			}
		}
	}
}
