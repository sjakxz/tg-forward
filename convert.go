package main

import (
	"unicode/utf16"

	"github.com/zelenin/go-tdlib/client"
)

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
