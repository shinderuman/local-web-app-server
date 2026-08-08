package instance

import (
	"errors"
	"testing"
)

func TestLockAndStatus(t *testing.T) {
	runtimeDirectory := t.TempDir()
	lock, err := Acquire(runtimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })

	if _, err := Acquire(runtimeDirectory); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second acquire error = %v", err)
	}
	want := Status{PID: 123, URL: "http://127.0.0.1:8765", Managed: true}
	if err := lock.WriteStatus(want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus(runtimeDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("status = %#v, want %#v", got, want)
	}
}
