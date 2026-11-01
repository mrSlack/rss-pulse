package main

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// newLogger builds the process logger from LOG_FORMAT (text or json, default
// text) and LOG_LEVEL (debug, info, warn or error, default info). Invalid values
// return an error so startup fails fast.
func newLogger(getenv func(string) string, output io.Writer) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "", "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", getenv("LOG_LEVEL"))
	}
	options := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(strings.TrimSpace(getenv("LOG_FORMAT"))) {
	case "", "text":
		return slog.New(slog.NewTextHandler(output, options)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(output, options)), nil
	default:
		return nil, fmt.Errorf("LOG_FORMAT must be text or json, got %q", getenv("LOG_FORMAT"))
	}
}
