package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shinderuman/local-web-app-server/internal/config"
)

func TestTwoAppsAndBackendIsolation(t *testing.T) {
	appsDirectory := t.TempDir()
	alphaMarker := filepath.Join(t.TempDir(), "alpha-canceled")
	writeIntegrationApp(t, appsDirectory, "日本語 空白 #1", "alpha", "Alpha", "alpha page", alphaMarker)
	writeIntegrationApp(t, appsDirectory, "beta", "beta", "Beta", "beta page", filepath.Join(t.TempDir(), "beta-canceled"))
	apps, err := config.LoadApps(appsDirectory)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory, err := os.MkdirTemp("/private/tmp", "lwas-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	logDirectory := t.TempDir()
	server := New(apps, runtimeDirectory, logDirectory, logger)
	server.StartBackends(context.Background())
	t.Cleanup(func() {
		if err := server.StopBackends(); err != nil {
			t.Error(err)
		}
	})
	for _, status := range server.Statuses() {
		if status.Backend.State != "ready" {
			entries, _ := os.ReadDir(logDirectory)
			var logs strings.Builder
			for _, entry := range entries {
				data, _ := os.ReadFile(filepath.Join(logDirectory, entry.Name()))
				fmt.Fprintf(&logs, "%s:\n%s\n", entry.Name(), data)
			}
			t.Fatalf("%s state = %s: %s\n%s", status.ID, status.Backend.State, status.Backend.LastError, logs.String())
		}
	}

	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	assertBody(t, httpServer.URL+"/apps/alpha/", http.StatusOK, "alpha page")
	assertBody(t, httpServer.URL+"/apps/beta/", http.StatusOK, "beta page")
	assertBody(t, httpServer.URL+"/apps/alpha/api/echo?value=1", http.StatusOK, `"/api/echo"`)
	assertBody(t, httpServer.URL+"/apps/alpha/local-web-app.json", http.StatusNotFound, "")
	assertBody(t, httpServer.URL+"/_local/run", http.StatusNotFound, "")

	response, err := http.Get(httpServer.URL + "/apps/beta/api/events")
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if string(buffer) != "data: ready\n\n" {
		t.Fatalf("SSE = %q", buffer)
	}

	requestContext, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, httpServer.URL+"/apps/alpha/api/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- requestErr
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-result; err == nil {
		t.Fatal("canceled request unexpectedly succeeded")
	}
	waitFor(t, func() bool {
		_, err := os.Stat(alphaMarker)
		return err == nil
	})

	_, _ = http.Get(httpServer.URL + "/apps/alpha/api/exit")
	waitFor(t, func() bool {
		return server.backends["alpha"].Status().State == "failed"
	})
	assertBody(t, httpServer.URL+"/apps/beta/api/echo", http.StatusOK, `"/api/echo"`)
	assertBody(t, httpServer.URL+"/apps/alpha/api/echo", http.StatusServiceUnavailable, "backend unavailable")
}

func TestBackendExitsWhenHostLifetimeEndsAndCanBeReplaced(t *testing.T) {
	appsDirectory := t.TempDir()
	lifetimeMarker := filepath.Join(t.TempDir(), "lifetime-ended")
	writeIntegrationApp(t, appsDirectory, "alpha", "alpha", "Alpha", "alpha page", lifetimeMarker)
	apps, err := config.LoadApps(appsDirectory)
	if err != nil {
		t.Fatal(err)
	}
	runtimeDirectory, err := os.MkdirTemp("/private/tmp", "lwas-lifetime-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	first := New(apps, runtimeDirectory, t.TempDir(), logger)
	first.StartBackends(context.Background())
	current := first.backends["alpha"]
	if status := current.Status(); status.State != "ready" {
		t.Fatalf("first backend status = %#v", status)
	}
	current.mu.RLock()
	lifetime := current.lifetime
	current.mu.RUnlock()
	if lifetime == nil {
		t.Fatal("backend lifetime writer was not retained")
	}
	if err := lifetime.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_, markerErr := os.Stat(lifetimeMarker)
		return markerErr == nil && current.Status().State == "failed"
	})

	second := New(apps, runtimeDirectory, t.TempDir(), logger)
	second.StartBackends(context.Background())
	if status := second.backends["alpha"].Status(); status.State != "ready" {
		t.Fatalf("replacement backend status = %#v", status)
	}
	if err := second.StopBackends(); err != nil {
		t.Fatal(err)
	}
}

func TestStaticHandlerRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := staticHandler{root: root}
	for _, target := range []string{"/../secret", "/%2e%2e/secret", `/..\secret`} {
		request := httptest.NewRequest(http.MethodGet, "http://example.invalid"+target, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%q status = %d", target, recorder.Code)
		}
	}
}

func TestWaitForPreviousBackendDoesNotUnlinkLiveSocket(t *testing.T) {
	root, err := os.MkdirTemp("/private/tmp", "lwas-socket-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketPath := filepath.Join(root, "live.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := waitForPreviousBackend(ctx, socketPath); err == nil {
		t.Fatal("waitForPreviousBackend() accepted a live prior backend")
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
}

func TestWaitForPreviousBackendRemovesUnreachableSocket(t *testing.T) {
	root, err := os.MkdirTemp("/private/tmp", "lwas-socket-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketPath := filepath.Join(root, "stale.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitForPreviousBackend(context.Background(), socketPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket still exists: %v", err)
	}
}

func TestBackendEnvironmentReplacesReservedValues(t *testing.T) {
	got := backendEnvironment([]string{
		"PATH=/bin", "LOCAL_WEB_SOCKET=/wrong.sock", "LOCAL_WEB_LIFETIME_FD=99",
	}, "/runtime/app.sock", 3)
	want := []string{"PATH=/bin", "LOCAL_WEB_SOCKET=/runtime/app.sock", "LOCAL_WEB_LIFETIME_FD=3"}
	if !slices.Equal(got, want) {
		t.Fatalf("backendEnvironment() = %v, want %v", got, want)
	}
}

func TestHelperBackend(t *testing.T) {
	if os.Getenv("GO_WANT_LOCAL_WEB_HELPER") != "1" {
		return
	}
	marker := os.Args[len(os.Args)-1]
	if value := os.Getenv(lifetimeFDEnvironment); value != "" {
		fd, err := strconv.Atoi(value)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		lifetime := os.NewFile(uintptr(fd), "local-web-host-lifetime")
		go func() {
			_, _ = io.Copy(io.Discard, lifetime)
			_ = os.WriteFile(marker, []byte("lifetime ended"), 0o600)
			os.Exit(0)
		}()
	}
	socketPath := os.Getenv("LOCAL_WEB_SOCKET")
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/echo", func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"path": request.URL.Path})
	})
	mux.HandleFunc("/api/events", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: ready\n\n")
		writer.(http.Flusher).Flush()
	})
	mux.HandleFunc("/api/slow", func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
		_ = os.WriteFile(marker, []byte("canceled"), 0o600)
	})
	mux.HandleFunc("/api/exit", func(http.ResponseWriter, *http.Request) {
		os.Exit(3)
	})
	if err := http.Serve(listener, mux); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func writeIntegrationApp(t *testing.T, appsDirectory, directory, id, name, page, marker string) {
	t.Helper()
	root := filepath.Join(appsDirectory, directory)
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	currentExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(currentExecutable)
	if err != nil {
		t.Fatal(err)
	}
	backendPath := filepath.Join(root, "bin", "backend")
	if err := os.WriteFile(backendPath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(
		`{"schema_version":1,"id":%q,"name":%q,"web_root":"web","backend":{"executable":"bin/backend","args":["-test.run=^TestHelperBackend$","--",%q],"shutdown_timeout_seconds":2}}`,
		id, name, marker,
	)
	if err := os.WriteFile(filepath.Join(root, config.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_WANT_LOCAL_WEB_HELPER", "1")
}

func assertBody(t *testing.T, url string, status int, contains string) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s status = %d, body = %s", url, response.StatusCode, body)
	}
	if contains != "" && !strings.Contains(string(body), contains) {
		t.Fatalf("%s body = %q, want containing %q", url, body, contains)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied")
}
