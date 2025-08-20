package utils

import (
	"log/slog"
	"os"
)

func ExitOnError(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
