package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	lockName   = "server.lock"
	statusName = "server.json"
)

var ErrAlreadyRunning = errors.New("local web app server is already running")

type Status struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

type Lock struct {
	file       *os.File
	statusPath string
}

func Acquire(runtimeDirectory string) (*Lock, error) {
	if err := os.MkdirAll(runtimeDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create runtime directory: %w", err)
	}
	if err := os.Chmod(runtimeDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("secure runtime directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(runtimeDirectory, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock instance: %w", err)
	}
	return &Lock{file: file, statusPath: filepath.Join(runtimeDirectory, statusName)}, nil
}

func (lock *Lock) WriteStatus(status Status) error {
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("encode instance status: %w", err)
	}
	temporary := lock.statusPath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write instance status: %w", err)
	}
	if err := os.Rename(temporary, lock.statusPath); err != nil {
		return fmt.Errorf("publish instance status: %w", err)
	}
	return nil
}

func ReadStatus(runtimeDirectory string) (Status, error) {
	data, err := os.ReadFile(filepath.Join(runtimeDirectory, statusName))
	if err != nil {
		return Status{}, fmt.Errorf("read instance status: %w", err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, fmt.Errorf("decode instance status: %w", err)
	}
	if status.PID <= 0 || status.URL == "" {
		return Status{}, errors.New("invalid instance status")
	}
	return status, nil
}

func (lock *Lock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	_ = os.Remove(lock.statusPath)
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}
