package app

import (
	"context"
	"errors"
	"flag"
	"net"
	"testing"
	"time"
)

func TestValidateListenAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8765", "0.0.0.0:8765"} {
		if err := validateListenAddress(address); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"localhost:8765", "[::1]:8765", "invalid"} {
		if err := validateListenAddress(address); err == nil {
			t.Errorf("%q unexpectedly accepted", address)
		}
	}
}

func TestDefaultListenAddressIsLAN(t *testing.T) {
	if defaultListenAddress != "0.0.0.0:8766" {
		t.Fatalf("default listen address = %q", defaultListenAddress)
	}
}

func TestShutdownServicesStopsBackendsBeforeHTTP(t *testing.T) {
	var order []string
	backendFailure, httpFailure := errors.New("backend failure"), errors.New("HTTP failure")
	err := shutdownServices(func() error { order = append(order, "backends"); return backendFailure }, func(ctx context.Context) error {
		if ctx == nil {
			t.Fatal("shutdown context is nil")
		}
		order = append(order, "HTTP")
		return httpFailure
	})
	if len(order) != 2 || order[0] != "backends" || order[1] != "HTTP" {
		t.Fatalf("shutdown order = %v", order)
	}
	if !errors.Is(err, backendFailure) || !errors.Is(err, httpFailure) {
		t.Fatalf("shutdown error = %v", err)
	}
}

func TestLocalURL(t *testing.T) {
	for _, test := range []struct{ address, want string }{{"127.0.0.1:8765", "http://127.0.0.1:8765"}, {"0.0.0.0:8765", "http://127.0.0.1:8765"}} {
		if got := localURL(fakeAddress(test.address)); got != test.want {
			t.Errorf("localURL(%q) = %q, want %q", test.address, got, test.want)
		}
	}
}

type fakeAddress string

func (address fakeAddress) Network() string { return "tcp" }
func (address fakeAddress) String() string  { return string(address) }

var _ net.Addr = fakeAddress("")

func TestShortFlags(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	text := stringFlag(flags, "apps", "a", "default", "apps")
	enabled := boolFlag(flags, "open", "o", false, "open")
	restart := boolFlag(flags, "restart", "R", false, "restart")
	if err := flags.Parse([]string{"-a", "/tmp/apps", "-o", "-R"}); err != nil {
		t.Fatal(err)
	}
	if *text != "/tmp/apps" || !*enabled || !*restart {
		t.Fatalf("text = %q, enabled = %v, restart = %v", *text, *enabled, *restart)
	}
}

func TestRestartArgumentsRemovesRestartAndOpen(t *testing.T) {
	arguments := restartArguments([]string{"--apps", "/tmp/apps", "--restart", "--open", "--listen=0.0.0.0:9000"})
	want := []string{"--apps", "/tmp/apps", "--listen=0.0.0.0:9000"}
	if len(arguments) != len(want) {
		t.Fatalf("restart arguments = %v, want %v", arguments, want)
	}
	for index := range want {
		if arguments[index] != want[index] {
			t.Fatalf("restart arguments = %v, want %v", arguments, want)
		}
	}
}

func TestStartBackendsAsyncDoesNotBlockServerStartup(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	startBackendsAsync(context.Background(), func(context.Context) {
		close(started)
		<-finish
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend startup did not begin")
	}
	close(finish)
}

func TestWaitForProcessExit(t *testing.T) {
	checks, pauses := 0, 0
	err := waitForProcessExit(42, func(pid int) (bool, error) {
		if pid != 42 {
			t.Fatalf("pid = %d", pid)
		}
		checks++
		return checks < 3, nil
	}, func(duration time.Duration) {
		if duration != 100*time.Millisecond {
			t.Fatalf("pause = %s", duration)
		}
		pauses++
	})
	if err != nil {
		t.Fatalf("waitForProcessExit() error = %v", err)
	}
	if checks != 3 || pauses != 2 {
		t.Fatalf("checks = %d, pauses = %d", checks, pauses)
	}
	expected := errors.New("failure")
	err = waitForProcessExit(42, func(int) (bool, error) { return false, expected }, func(time.Duration) { t.Fatal("unexpected pause") })
	if !errors.Is(err, expected) {
		t.Fatalf("waitForProcessExit() error = %v", err)
	}
}
