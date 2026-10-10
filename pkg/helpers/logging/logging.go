// Package logging sets up structured logging and carries the request ID
// through contexts so every log line of a request can be correlated.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

type Format string

const (
	JSON Format = "json"
	Text Format = "text"
)

func (f *Format) UnmarshalText(text []byte) error {
	switch format := Format(text); format {
	case JSON, Text:
		*f = format
		return nil
	default:
		return fmt.Errorf("unknown log format %q, want json or text", text)
	}
}

// New returns a logger that adds the request ID found in the context to every
// record logged with a *Context method.
func New(out io.Writer, level slog.Level, format Format) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if format == Text {
		handler = slog.NewTextHandler(out, opts)
	} else {
		handler = slog.NewJSONHandler(out, opts)
	}
	return slog.New(requestIDHandler{handler})
}

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID stored in ctx, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

type requestIDHandler struct {
	slog.Handler
}

func (h requestIDHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := RequestID(ctx); id != "" {
		record.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, record)
}

func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
}
