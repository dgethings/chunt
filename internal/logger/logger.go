package logger

import (
	"log/slog"
	"os"
)

func SetLogger(lvl *slog.LevelVar) {
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})).With("lsp", "chunt")
	slog.SetDefault(l)
}
