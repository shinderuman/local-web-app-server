package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shinderuman/local-web-app-server/internal/config"
	"github.com/shinderuman/local-web-app-server/internal/logging"
)

const (
	lifetimeFDEnvironment      = "LOCAL_WEB_LIFETIME_FD"
	previousBackendWaitTimeout = 5 * time.Second
)

type BackendStatus struct {
	State     string `json:"state"`
	PID       int    `json:"pid,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type backend struct {
	app          config.App
	socketPath   string
	logDirectory string
	logger       *slog.Logger
	mu           sync.RWMutex
	state        string
	pid          int
	lastError    string
	command      *exec.Cmd
	done         chan struct{}
	lifetime     *os.File
	proxy        *httputil.ReverseProxy
	stopping     bool
}

func newBackend(app config.App, runtimeDirectory, logDirectory string, logger *slog.Logger) *backend {
	socketPath := filepath.Join(runtimeDirectory, app.Manifest.ID+".sock")
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	target := &url.URL{Scheme: "http", Host: "unix"}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, err error) {
		logger.Error("backend proxy failed", "app", app.Manifest.ID, "path", request.URL.Path, "error", err)
		http.Error(writer, "backend unavailable", http.StatusBadGateway)
	}
	return &backend{
		app:          app,
		socketPath:   socketPath,
		logDirectory: logDirectory,
		logger:       logger,
		state:        "stopped",
		proxy:        proxy,
	}
}

func (backend *backend) Start(ctx context.Context) error {
	if len([]byte(backend.socketPath)) > 100 {
		return backend.fail(fmt.Errorf("unix socket path is too long: %s", backend.socketPath))
	}
	if err := waitForPreviousBackend(ctx, backend.socketPath); err != nil {
		return backend.fail(err)
	}
	logFile, err := logging.OpenBackend(backend.logDirectory, backend.app.Manifest.ID)
	if err != nil {
		return backend.fail(err)
	}
	lifetimeReader, lifetimeWriter, err := os.Pipe()
	if err != nil {
		_ = logFile.Close()
		return backend.fail(fmt.Errorf("create backend lifetime pipe: %w", err))
	}

	command := exec.Command(backend.app.Executable, backend.app.Manifest.Backend.Args...)
	command.Dir = backend.app.Root
	command.Env = backendEnvironment(os.Environ(), backend.socketPath, 3)
	command.ExtraFiles = []*os.File{lifetimeReader}
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = lifetimeReader.Close()
		_ = lifetimeWriter.Close()
		_ = logFile.Close()
		return backend.fail(fmt.Errorf("start backend: %w", err))
	}
	_ = lifetimeReader.Close()

	backend.mu.Lock()
	backend.command = command
	backend.done = make(chan struct{})
	backend.lifetime = lifetimeWriter
	backend.pid = command.Process.Pid
	backend.state = "starting"
	backend.lastError = ""
	backend.stopping = false
	done := backend.done
	backend.mu.Unlock()
	backend.logger.Info("backend started", "app", backend.app.Manifest.ID, "pid", command.Process.Pid)

	go func() {
		waitErr := command.Wait()
		_ = lifetimeWriter.Close()
		_ = logFile.Close()
		_ = os.Remove(backend.socketPath)
		backend.mu.Lock()
		if backend.stopping {
			backend.state = "stopped"
			backend.lastError = ""
		} else {
			backend.state = "failed"
			if waitErr == nil {
				backend.lastError = "backend exited unexpectedly"
			} else {
				backend.lastError = waitErr.Error()
			}
		}
		backend.pid = 0
		backend.lifetime = nil
		close(done)
		backend.mu.Unlock()
		if waitErr != nil {
			backend.logger.Error("backend exited", "app", backend.app.Manifest.ID, "error", waitErr)
		} else {
			backend.logger.Info("backend exited", "app", backend.app.Manifest.ID)
		}
	}()

	readinessContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		connection, dialErr := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(readinessContext, "unix", backend.socketPath)
		if dialErr == nil {
			_ = connection.Close()
			backend.mu.Lock()
			if backend.state == "starting" {
				backend.state = "ready"
			}
			backend.mu.Unlock()
			return nil
		}
		select {
		case <-done:
			status := backend.Status()
			return fmt.Errorf("backend exited before it became ready: %s", status.LastError)
		case <-readinessContext.Done():
			_ = backend.Stop()
			return backend.fail(fmt.Errorf("backend readiness timeout: %w", readinessContext.Err()))
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func backendEnvironment(environment []string, socketPath string, lifetimeFD int) []string {
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "LOCAL_WEB_SOCKET=") || strings.HasPrefix(entry, lifetimeFDEnvironment+"=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result,
		"LOCAL_WEB_SOCKET="+socketPath,
		lifetimeFDEnvironment+"="+strconv.Itoa(lifetimeFD),
	)
}

func waitForPreviousBackend(ctx context.Context, socketPath string) error {
	waitContext, cancel := context.WithTimeout(ctx, previousBackendWaitTimeout)
	defer cancel()
	for {
		if _, err := os.Lstat(socketPath); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("inspect backend socket: %w", err)
		}

		connection, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(waitContext, "unix", socketPath)
		if err == nil {
			_ = connection.Close()
		} else if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
			if removeErr := os.Remove(socketPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove stale backend socket: %w", removeErr)
			}
			return nil
		}

		select {
		case <-waitContext.Done():
			return fmt.Errorf("previous backend still owns socket %s: %w", socketPath, waitContext.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (backend *backend) Stop() error {
	backend.mu.Lock()
	if backend.command == nil || backend.done == nil {
		backend.state = "stopped"
		backend.mu.Unlock()
		return nil
	}
	select {
	case <-backend.done:
		backend.mu.Unlock()
		return nil
	default:
	}
	backend.stopping = true
	backend.state = "stopping"
	pid := backend.command.Process.Pid
	done := backend.done
	backend.mu.Unlock()

	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timeoutSeconds := config.ShutdownTimeoutSeconds(backend.app.Manifest)
	if timeoutSeconds == 0 {
		<-done
		return nil
	}
	timer := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		backend.logger.Warn("backend shutdown timed out; killing", "app", backend.app.Manifest.ID, "pid", pid)
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill backend %s: %w", backend.app.Manifest.ID, err)
		}
		<-done
		return nil
	}
}

func (backend *backend) Status() BackendStatus {
	backend.mu.RLock()
	defer backend.mu.RUnlock()
	return BackendStatus{State: backend.state, PID: backend.pid, LastError: backend.lastError}
}

func (backend *backend) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if backend.Status().State != "ready" {
		http.Error(writer, "backend unavailable", http.StatusServiceUnavailable)
		return
	}
	backend.proxy.ServeHTTP(writer, request)
}

func (backend *backend) fail(err error) error {
	backend.mu.Lock()
	backend.state = "failed"
	backend.lastError = err.Error()
	backend.mu.Unlock()
	backend.logger.Error("backend failed", "app", backend.app.Manifest.ID, "error", err)
	return err
}

func (backend *backend) String() string {
	return backend.app.Manifest.ID + ":" + strconv.Itoa(backend.Status().PID)
}
