// Package log wires the application to the shared paularlott/logger
// interface so output is uniform with the other tools. The backend is the
// slog implementation; swap Configure to change it app-wide.
package log

import (
	"io"
	"os"

	"github.com/paularlott/logger"
	logslog "github.com/paularlott/logger/slog"
)

var defaultLogger logger.Logger

func init() {
	defaultLogger = logslog.New(logslog.Config{
		Level:  "info",
		Format: "console",
		Writer: os.Stdout,
	})
}

// Configure sets up the logger with the given settings. Call this early in
// main(); a nil writer falls back to stdout.
func Configure(level, format string, writer io.Writer) {
	if writer == nil {
		writer = os.Stdout
	}
	defaultLogger = logslog.New(logslog.Config{
		Level:  level,
		Format: format,
		Writer: writer,
	})
}

// GetLogger returns the configured logger instance. Use this when passing
// the logger into components.
func GetLogger() logger.Logger {
	return defaultLogger
}

func Trace(msg string, keysAndValues ...any) { defaultLogger.Trace(msg, keysAndValues...) }
func Debug(msg string, keysAndValues ...any) { defaultLogger.Debug(msg, keysAndValues...) }
func Info(msg string, keysAndValues ...any)  { defaultLogger.Info(msg, keysAndValues...) }
func Warn(msg string, keysAndValues ...any)  { defaultLogger.Warn(msg, keysAndValues...) }
func Error(msg string, keysAndValues ...any) { defaultLogger.Error(msg, keysAndValues...) }
