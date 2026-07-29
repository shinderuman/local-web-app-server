package main

import (
	"flag"
	"testing"
)

func TestValidateListenAddress(t *testing.T) {
	if err := validateListenAddress("127.0.0.1:8765"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"0.0.0.0:8765", "localhost:8765", "[::1]:8765", "invalid"} {
		if err := validateListenAddress(address); err == nil {
			t.Errorf("%q unexpectedly accepted", address)
		}
	}
}

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
