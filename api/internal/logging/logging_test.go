package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRequestIDIsLogged(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out, slog.LevelInfo, JSON).With("service", "api")

	ctx := WithRequestID(t.Context(), "req-42")
	logger.InfoContext(ctx, "hello")
	logger.Info("no context")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"request_id":"req-42"`) || !strings.Contains(lines[0], `"service":"api"`) {
		t.Errorf("line with context = %s", lines[0])
	}
	if strings.Contains(lines[1], "request_id") {
		t.Errorf("line without context = %s", lines[1])
	}
}

func TestFormat(t *testing.T) {
	var format Format
	if err := format.UnmarshalText([]byte("text")); err != nil || format != Text {
		t.Errorf("UnmarshalText(text) = %v, %q", err, format)
	}
	if err := format.UnmarshalText([]byte("xml")); err == nil {
		t.Error("UnmarshalText(xml) succeeded")
	}
}
