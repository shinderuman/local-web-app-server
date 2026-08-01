package logging

import (
	"os"
	"testing"
)

func TestOpenLogs(t *testing.T) {
	directory := t.TempDir()
	logger, closer, err := OpenServer(directory)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("test")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory + "/server.log"); err != nil {
		t.Fatal(err)
	}
	backend, err := OpenBackend(directory, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	name := backend.Name()
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatal(err)
	}
}
