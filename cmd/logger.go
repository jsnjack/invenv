package cmd

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// LevelTrace is the slog level used for trace logging — below debug.
const LevelTrace = slog.Level(-8)

// initLogger configures the default slog logger. When tracePath is
// non-empty, all logs (at the configured level) are written to that file,
// truncated each run, and stderr is left untouched. Otherwise logs go to
// stderr. Returns a cleanup function that closes the trace file if one was
// opened.
func initLogger(tracePath, level string) func() {
	var w io.Writer = os.Stderr
	cleanup := func() {}

	if tracePath != "" {
		f, err := os.OpenFile(tracePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err == nil {
			w = f
			cleanup = func() { _ = f.Close() }
		}
		// On open failure we fall back to stderr — better than dropping logs.
	}

	lvl := slog.LevelWarn
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "trace":
		lvl = LevelTrace
	}

	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(h))
	return cleanup
}

// trace emits a trace-level log record using the default logger.
func trace(msg string, args ...any) {
	slog.Default().Log(context.Background(), LevelTrace, msg, args...)
}
