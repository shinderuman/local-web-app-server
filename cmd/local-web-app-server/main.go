package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shinderuman/local-web-app-server/internal/config"
	"github.com/shinderuman/local-web-app-server/internal/instance"
	"github.com/shinderuman/local-web-app-server/internal/logging"
	appserver "github.com/shinderuman/local-web-app-server/internal/server"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "local-web-app-server:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find home directory: %w", err)
	}
	defaultApps := filepath.Join(home, "Library", "Application Support", "LocalWebAppServer", "apps")
	defaultLogs := filepath.Join(home, "Library", "Logs", "LocalWebAppServer")
	defaultRuntime := filepath.Join("/private/tmp", "local-web-app-server-"+strconv.Itoa(os.Getuid()))

	flags := flag.NewFlagSet("local-web-app-server", flag.ContinueOnError)
	appsDirectory := stringFlag(flags, "apps", "a", defaultApps, "installed app directory")
	listenAddress := stringFlag(flags, "listen", "l", defaultListenAddress, "HTTP listen address")
	runtimeDirectory := stringFlag(flags, "runtime", "r", defaultRuntime, "runtime directory")
	logDirectory := stringFlag(flags, "log-directory", "g", defaultLogs, "log directory")
	openBrowser := boolFlag(flags, "open", "o", false, "open the app list in the default browser")
	stopServer := boolFlag(flags, "stop", "s", false, "gracefully stop the running server")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if err := validateListenAddress(*listenAddress); err != nil {
		return err
	}

	lock, err := instance.Acquire(*runtimeDirectory)
	if *stopServer {
		if err == nil {
			return lock.Close()
		}
		if !errors.Is(err, instance.ErrAlreadyRunning) {
			return err
		}
		status, statusErr := instance.ReadStatus(*runtimeDirectory)
		if statusErr != nil {
			return fmt.Errorf("read running server status: %w", statusErr)
		}
		if signalErr := syscall.Kill(status.PID, syscall.SIGTERM); signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) {
			return fmt.Errorf("stop running server: %w", signalErr)
		}
		return waitForProcessExit(status.PID, processExists, time.Sleep)
	}
	if errors.Is(err, instance.ErrAlreadyRunning) {
		status, statusErr := instance.ReadStatus(*runtimeDirectory)
		if statusErr != nil {
			return fmt.Errorf("%w, but its status cannot be read: %v", err, statusErr)
		}
		if healthErr := confirmRunning(status.URL); healthErr != nil {
			if stopErr := stopExistingServer(status.PID); stopErr != nil {
				return fmt.Errorf("%w, health check failed: %v, and stale process could not be stopped: %w", instance.ErrAlreadyRunning, healthErr, stopErr)
			}
			return run(arguments)
		}
		fmt.Println(status.URL)
		if *openBrowser {
			return openURL(status.URL)
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()

	logger, logCloser, err := logging.OpenServer(*logDirectory)
	if err != nil {
		return err
	}
	defer logCloser.Close()

	if err := os.MkdirAll(*appsDirectory, 0o755); err != nil {
		return fmt.Errorf("create apps directory: %w", err)
	}
	apps, err := config.LoadApps(*appsDirectory)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	url := localURL(listener.Addr())
	if err := lock.WriteStatus(instance.Status{PID: os.Getpid(), URL: url}); err != nil {
		return err
	}

	server := appserver.New(apps, *runtimeDirectory, *logDirectory, logger)
	server.StartBackends(context.Background())
	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()
	logger.Info("server ready", "url", url, "apps", len(apps))
	fmt.Println(url)
	if *openBrowser {
		if err := openURL(url); err != nil {
			logger.Warn("could not open browser", "error", err)
		}
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case received := <-signals:
		logger.Info("shutdown requested", "signal", received)
	case serveErr := <-serveErrors:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", serveErr)
		}
	}

	return shutdownServices(server.StopBackends, httpServer.Shutdown)
}

const staleProcessStopTimeout = 5 * time.Second

const defaultListenAddress = "0.0.0.0:8766"

func stopExistingServer(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("send termination signal: %w", err)
	}
	deadline := time.Now().Add(staleProcessStopTimeout)
	for time.Now().Before(deadline) {
		running, err := processExists(pid)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("force stop server: %w", err)
	}
	return waitForProcessExit(pid, processExists, time.Sleep)
}

func shutdownServices(stopBackends func() error, shutdownHTTP func(context.Context) error) error {
	// Stop backends first so long-lived proxied responses such as SSE close before
	// the host waits for HTTP handlers. The listener remains available during the
	// backend-specific graceful wait, but stopping backends reject new API work.
	backendErr := stopBackends()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpErr := shutdownHTTP(shutdownContext)
	return errors.Join(backendErr, httpErr)
}

func stringFlag(flags *flag.FlagSet, long, short, defaultValue, usage string) *string {
	value := flags.String(long, defaultValue, usage)
	flags.StringVar(value, short, defaultValue, usage+" (short)")
	return value
}

func boolFlag(flags *flag.FlagSet, long, short string, defaultValue bool, usage string) *bool {
	value := flags.Bool(long, defaultValue, usage)
	flags.BoolVar(value, short, defaultValue, usage+" (short)")
	return value
}

func validateListenAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if host != "127.0.0.1" && host != "0.0.0.0" {
		return errors.New("listen address must use 127.0.0.1 or 0.0.0.0")
	}
	return nil
}

func localURL(address net.Addr) string {
	host, port, err := net.SplitHostPort(address.String())
	if err == nil && (host == "0.0.0.0" || host == "::") {
		host = "127.0.0.1"
		return "http://" + net.JoinHostPort(host, port)
	}
	return "http://" + address.String()
}

func confirmRunning(baseURL string) error {
	client := http.Client{Timeout: time.Second}
	response, err := client.Get(baseURL + "/_local/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health status is %s", response.Status)
	}
	return nil
}

func openURL(url string) error {
	command := exec.Command("open", url)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func processExists(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}

func waitForProcessExit(pid int, exists func(int) (bool, error), pause func(time.Duration)) error {
	for {
		running, err := exists(pid)
		if err != nil {
			return fmt.Errorf("check running server: %w", err)
		}
		if !running {
			return nil
		}
		pause(100 * time.Millisecond)
	}
}
