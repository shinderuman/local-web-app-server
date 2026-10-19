package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shinderuman/local-web-app-server/internal/config"
)

type readinessLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *readinessLogBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *readinessLogBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func readinessTestBackend(t *testing.T, logger *slog.Logger) *backend {
	t.Helper()
	appsDirectory := t.TempDir()
	writeIntegrationApp(t, appsDirectory, "alpha", "alpha", "Alpha", "page", filepath.Join(t.TempDir(), "marker"))
	apps, err := config.LoadApps(appsDirectory)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory, err := os.MkdirTemp("/private/tmp", "lwas-ready-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })
	current := newBackend(apps[0], runtimeDirectory, t.TempDir(), logger)
	t.Cleanup(func() { _ = current.stop(time.Second) })
	return current
}

func TestBackendReadinessWaitsPastFiveSeconds(t *testing.T) {
	var logs readinessLogBuffer
	current := readinessTestBackend(t, slog.New(slog.NewTextHandler(&logs, nil)))
	t.Setenv("GO_LOCAL_WEB_START_DELAY", "6s")
	if err := current.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := current.Status(); status.State != "ready" {
		t.Fatalf("status = %#v", status)
	}
	if err := current.Stop(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "waiting for backend readiness") {
		t.Fatal("起動待機の進捗ログがない")
	}
}

func TestBackendStartupDeadlineStopsUnresponsiveProcess(t *testing.T) {
	current := readinessTestBackend(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Setenv("GO_LOCAL_WEB_START_DELAY", "1m")
	t.Setenv("GO_LOCAL_WEB_IGNORE_TERM", "1")
	// 通常停止は無期限でも、起動失敗時は専用の停止期限を使う。
	zero := 0
	current.app.Manifest.Backend.ShutdownTimeoutSeconds = &zero
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	err := current.Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") || !strings.Contains(err.Error(), "log:") {
		t.Fatalf("起動エラー = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("起動失敗の停止に時間がかかりすぎる: %s", elapsed)
	}
	if status := current.Status(); status.State != "failed" || status.PID != 0 {
		t.Fatalf("status = %#v", status)
	}
}
