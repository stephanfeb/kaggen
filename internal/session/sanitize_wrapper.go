package session

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trpcsession "trpc.group/trpc-go/trpc-agent-go/session"
)

// SanitizeWrapper wraps a session.Service and strips binary data (images, files,
// audio) from ContentParts before persisting events. This prevents raw file bytes
// from accumulating in the session history and overflowing the context window on
// subsequent turns.
type SanitizeWrapper struct {
	trpcsession.Service
}

// NewSanitizeWrapper creates a wrapper around the given session service.
func NewSanitizeWrapper(inner trpcsession.Service) *SanitizeWrapper {
	return &SanitizeWrapper{Service: inner}
}

// AppendEvent strips binary content parts from the event before delegating to
// the inner service. Image/file/audio data is replaced with a text description
// referencing the original file path.
func (w *SanitizeWrapper) AppendEvent(ctx context.Context, sess *trpcsession.Session, evt *event.Event, opts ...trpcsession.Option) error {
	sanitizeEvent(evt)
	return w.Service.AppendEvent(ctx, sess, evt, opts...)
}

// ForkSession delegates to the inner service if it supports forking.
func (w *SanitizeWrapper) ForkSession(parentKey trpcsession.Key, upToEventID, threadName string) (trpcsession.Key, error) {
	type forker interface {
		ForkSession(trpcsession.Key, string, string) (trpcsession.Key, error)
	}
	if f, ok := w.Service.(forker); ok {
		return f.ForkSession(parentKey, upToEventID, threadName)
	}
	return trpcsession.Key{}, fmt.Errorf("inner session service does not support forking")
}

// sanitizeEvent replaces binary ContentParts with text references in-place.
func sanitizeEvent(evt *event.Event) {
	if evt == nil || evt.Response == nil {
		return
	}
	for i := range evt.Response.Choices {
		msg := &evt.Response.Choices[i].Message
		if len(msg.ContentParts) == 0 {
			continue
		}
		var sanitized []model.ContentPart
		for _, part := range msg.ContentParts {
			switch part.Type {
			case model.ContentTypeImage:
				if part.Image != nil && len(part.Image.Data) > 0 {
					desc := fmt.Sprintf("[Image: %s format, %d bytes]", part.Image.Format, len(part.Image.Data))
					sanitized = append(sanitized, model.ContentPart{
						Type: model.ContentTypeText,
						Text: &desc,
					})
					continue
				}
			case model.ContentTypeFile:
				if part.File != nil && len(part.File.Data) > 0 {
					desc := fmt.Sprintf("[File: %s, %s, %d bytes]", part.File.Name, part.File.MimeType, len(part.File.Data))
					sanitized = append(sanitized, model.ContentPart{
						Type: model.ContentTypeText,
						Text: &desc,
					})
					continue
				}
			case model.ContentTypeAudio:
				if part.Audio != nil && len(part.Audio.Data) > 0 {
					desc := fmt.Sprintf("[Audio: %s format, %d bytes]", part.Audio.Format, len(part.Audio.Data))
					sanitized = append(sanitized, model.ContentPart{
						Type: model.ContentTypeText,
						Text: &desc,
					})
					continue
				}
			}
			sanitized = append(sanitized, part)
		}
		msg.ContentParts = sanitized
	}
}
