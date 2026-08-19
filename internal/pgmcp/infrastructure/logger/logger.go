// Package logger builds pgmcp's structured logger. Tool handlers must never
// log arguments, SQL text, or result rows — only {tool, duration_ms, ok,
// user_id}.
package logger

import (
	"io"
	"log/slog"
)

// New returns a structured logger writing to w at the given level, in
// either "json" or "text" format. Any format other than "json" falls back
// to text. Source location is never included in log records.
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{
		AddSource: false,
		Level:     level,
	}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler)
}
