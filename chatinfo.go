package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/zelenin/go-tdlib/client"
)

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
