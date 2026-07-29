package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const ManifestName = "local-web-app.json"

var appIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,46}[a-z0-9])?$`)

type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	WebRoot       string          `json:"web_root"`
	Backend       BackendManifest `json:"backend"`
}

type BackendManifest struct {
	Executable             string   `json:"executable"`
	Args                   []string `json:"args,omitempty"`
	ShutdownTimeoutSeconds *int     `json:"shutdown_timeout_seconds,omitempty"`
}

type App struct {
	Root       string
	WebRoot    string
	Executable string
	Manifest   Manifest
}

func LoadApps(appsDirectory string) ([]App, error) {
	entries, err := os.ReadDir(appsDirectory)
	if err != nil {
		return nil, fmt.Errorf("read apps directory: %w", err)
	}

	apps := make([]App, 0, len(entries))
	ids := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		root := filepath.Join(appsDirectory, entry.Name())
		manifestPath := filepath.Join(root, ManifestName)
		if _, err := os.Stat(manifestPath); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("stat manifest %q: %w", manifestPath, err)
		}
		app, err := LoadApp(root)
		if err != nil {
			return nil, fmt.Errorf("load app %q: %w", entry.Name(), err)
		}
		if prior, exists := ids[app.Manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate app id %q in %q and %q", app.Manifest.ID, prior, entry.Name())
		}
		ids[app.Manifest.ID] = entry.Name()
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Manifest.ID < apps[j].Manifest.ID
	})
	return apps, nil
}

func LoadApp(root string) (App, error) {
	manifestPath := filepath.Join(root, ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return App{}, fmt.Errorf("read manifest: %w", err)
	}

	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return App{}, fmt.Errorf("decode manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return App{}, errors.New("decode manifest: multiple JSON values")
		}
		return App{}, fmt.Errorf("decode manifest trailing data: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return App{}, err
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return App{}, fmt.Errorf("resolve app root: %w", err)
	}
	webRoot, err := resolveContained(absoluteRoot, manifest.WebRoot)
	if err != nil {
		return App{}, fmt.Errorf("web_root: %w", err)
	}
	executable, err := resolveContained(absoluteRoot, manifest.Backend.Executable)
	if err != nil {
		return App{}, fmt.Errorf("backend executable: %w", err)
	}
	if err := validateDirectoryWithoutSymlinks(absoluteRoot, webRoot); err != nil {
		return App{}, fmt.Errorf("web_root: %w", err)
	}
	if err := validatePathWithoutSymlinks(absoluteRoot, executable); err != nil {
		return App{}, fmt.Errorf("backend executable: %w", err)
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return App{}, fmt.Errorf("backend executable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return App{}, errors.New("backend executable must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return App{}, errors.New("backend executable is not executable")
	}

	return App{
		Root:       absoluteRoot,
		WebRoot:    webRoot,
		Executable: executable,
		Manifest:   manifest,
	}, nil
}

func validatePathWithoutSymlinks(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed: %s", relative)
		}
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1, got %d", manifest.SchemaVersion)
	}
	if !appIDPattern.MatchString(manifest.ID) {
		return fmt.Errorf("invalid app id %q", manifest.ID)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("name is required")
	}
	if err := validateRelativePath(manifest.WebRoot); err != nil {
		return fmt.Errorf("invalid web_root: %w", err)
	}
	if err := validateRelativePath(manifest.Backend.Executable); err != nil {
		return fmt.Errorf("invalid backend executable: %w", err)
	}
	if manifest.Backend.ShutdownTimeoutSeconds != nil && *manifest.Backend.ShutdownTimeoutSeconds < 0 {
		return errors.New("shutdown_timeout_seconds must be zero or greater")
	}
	for _, arg := range manifest.Backend.Args {
		if strings.ContainsRune(arg, 0) {
			return errors.New("backend argument contains NUL")
		}
	}
	return nil
}

func validateRelativePath(path string) error {
	if path == "" {
		return errors.New("path is required")
	}
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return errors.New("absolute paths are not allowed")
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return errors.New("path must stay within the app directory")
	}
	return nil
}

func resolveContained(root, relative string) (string, error) {
	resolved := filepath.Join(root, filepath.Clean(relative))
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes app directory")
	}
	return resolved, nil
}

func validateDirectoryWithoutSymlinks(root, directory string) error {
	info, err := os.Stat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("must be a directory")
	}
	return filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			rel, _ := filepath.Rel(root, path)
			return fmt.Errorf("symlink is not allowed: %s", rel)
		}
		return nil
	})
}

func ShutdownTimeoutSeconds(manifest Manifest) int {
	if manifest.Backend.ShutdownTimeoutSeconds == nil {
		return 10
	}
	return *manifest.Backend.ShutdownTimeoutSeconds
}
