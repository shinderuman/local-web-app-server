package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"testing"
	"time"
)

func TestValidateListenAddress(t *testing.T) {
	if err := validateListenAddress("127.0.0.1:8765"); err != nil {
		t.Fatal(err)
	}
	if err := validateListenAddress("0.0.0.0:8765"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"localhost:8765", "[::1]:8765", "invalid"} {
		if err := validateListenAddress(address); err == nil {
			t.Errorf("%q unexpectedly accepted", address)
		}
	}
}

func TestDefaultListenAddressIsLAN(t *testing.T) {
	if defaultListenAddress != "0.0.0.0:8766" {
		t.Fatalf("default listen address = %q, want 0.0.0.0:8766", defaultListenAddress)
	}
}

func TestShutdownServicesStopsBackendsBeforeHTTP(t *testing.T) {
	var order []string
	backendFailure := errors.New("backend failure")
	httpFailure := errors.New("HTTP failure")
	err := shutdownServices(func() error {
		order = append(order, "backends")
		return backendFailure
	}, func(ctx context.Context) error {
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
	tests := []struct {
		address string
		want    string
	}{
		{"127.0.0.1:8765", "http://127.0.0.1:8765"},
		{"0.0.0.0:8765", "http://127.0.0.1:8765"},
	}
	for _, test := range tests {
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
	if err := flags.Parse([]string{"-a", "/tmp/apps", "-o"}); err != nil {
		t.Fatal(err)
	}
	if *text != "/tmp/apps" || !*enabled {
		t.Fatalf("text = %q, enabled = %v", *text, *enabled)
	}
}

func TestWaitForProcessExit(t *testing.T) {
	checks := 0
	pauses := 0
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
	err = waitForProcessExit(42, func(int) (bool, error) {
		return false, expected
	}, func(time.Duration) {
		t.Fatal("unexpected pause")
	})
	if !errors.Is(err, expected) {
		t.Fatalf("waitForProcessExit() error = %v", err)
	}
}
