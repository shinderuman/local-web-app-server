package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

func OpenServer(directory string) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(directory, "server.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open server log: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, file), nil))
	return logger, file, nil
}

func OpenBackend(directory, appID string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	name := fmt.Sprintf("%s-%s.log", appID, time.Now().Format("20060102T150405.000"))
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open backend log: %w", err)
	}
	return file, nil
}
