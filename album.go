package main

import (
	"log"
	"sync"
	"time"

	"github.com/zelenin/go-tdlib/client"
)

// albumFlushDelay is how long to wait after the last message of an album before
// flushing it. Album messages arrive back-to-back, so a short window collects them.
const albumFlushDelay = time.Second

// albumBuffer collects the converted contents of one media group (album).
// alertAllowed is decided from the first item's source chat (all items of one
// media group come from the same chat) and gates the alert on flush.
type albumBuffer struct {
	timer        *time.Timer
	contents     []client.InputMessageContent
	alertAllowed bool
}

var (
	albumMu       sync.Mutex
	pendingAlbums = map[int64]*albumBuffer{}
)

// bufferAlbumMessage appends one album item to its buffer and (re)arms the flush
// timer. The source label is applied to the first item only.
func bufferAlbumMessage(c *client.Client, cfg Config, albumId int64, content client.MessageContent, prefix string, alertAllowed bool) {
	albumMu.Lock()
	defer albumMu.Unlock()

	buf, ok := pendingAlbums[albumId]
	itemPrefix := "" // only the first item carries the "【来源】" label
	if !ok {
		buf = &albumBuffer{alertAllowed: alertAllowed}
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

	// Same alert-source whitelist as the plain-message path: a source that isn't
	// whitelisted still gets forwarded, it just doesn't start the "1" loop.
	if !buf.alertAllowed {
		return
	}
	for _, id := range cfg.AlertChatIds {
		startAlert(c, id, cfg.AlertIntervalSeconds, cfg.AlertMaxCount)
	}
}
