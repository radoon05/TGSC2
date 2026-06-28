package logger

import (
	"context"
	"log/slog"
	"os"
)

// Logger wraps slog.Logger for structured logging
type Logger struct {
	*slog.Logger
}

// New creates a new logger with the given level
func New(level string) *Logger {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
		// Add source for debugging if needed
		AddSource: logLevel == slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))
	return &Logger{slog.Default()}
}

// With returns a new logger with the given key-value pairs attached
func (l *Logger) With(args ...any) *Logger {
	return &Logger{l.Logger.With(args...)}
}

// WithContext returns a logger that includes context fields (e.g., trace_id)
func (l *Logger) WithContext(ctx context.Context) *Logger {
	if traceID := ctx.Value("trace_id"); traceID != nil {
		return l.With("trace_id", traceID)
	}
	return l
}

// Named adds a name to the logger (like component name)
func (l *Logger) Named(name string) *Logger {
	return l.With("component", name)
}