package logging

import (
	"io"
	"log/slog"
)

func New(output io.Writer, level slog.Level, format string) *slog.Logger {
	options := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(output, options)
	} else {
		handler = slog.NewJSONHandler(output, options)
	}

	return slog.New(handler)
}
