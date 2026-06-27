package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/zelenin/go-tdlib/client"
)

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
