package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadApps(t *testing.T) {
	root := t.TempDir()
	writeTestApp(t, root, "second", `{"schema_version":1,"id":"beta","name":"Beta","web_root":"web","backend":{"executable":"bin/backend"}}`)
	writeTestApp(t, root, "first", `{"schema_version":1,"id":"alpha","name":"Alpha","web_root":"web","backend":{"executable":"bin/backend","shutdown_timeout_seconds":0}}`)

	apps, err := LoadApps(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || apps[0].Manifest.ID != "alpha" || apps[1].Manifest.ID != "beta" {
		t.Fatalf("unexpected apps: %#v", apps)
	}
	if got := ShutdownTimeoutSeconds(apps[0].Manifest); got != 0 {
		t.Fatalf("explicit zero timeout = %d", got)
	}
	if got := ShutdownTimeoutSeconds(apps[1].Manifest); got != 10 {
		t.Fatalf("default timeout = %d", got)
	}
}

func TestLoadAppRejectsUnsafeManifest(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{"unknown field", `{"schema_version":1,"id":"app","name":"App","web_root":"web","backend":{"executable":"bin/backend"},"extra":true}`, "unknown field"},
		{"bad id", `{"schema_version":1,"id":"App_1","name":"App","web_root":"web","backend":{"executable":"bin/backend"}}`, "invalid app id"},
		{"traversal", `{"schema_version":1,"id":"app","name":"App","web_root":"../web","backend":{"executable":"bin/backend"}}`, "stay within"},
		{"absolute", `{"schema_version":1,"id":"app","name":"App","web_root":"/tmp","backend":{"executable":"bin/backend"}}`, "absolute"},
		{"negative timeout", `{"schema_version":1,"id":"app","name":"App","web_root":"web","backend":{"executable":"bin/backend","shutdown_timeout_seconds":-1}}`, "zero or greater"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestApp(t, root, "app", test.manifest)
			_, err := LoadApp(filepath.Join(root, "app"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadAppRejectsWebSymlink(t *testing.T) {
	root := t.TempDir()
	appRoot := writeTestApp(t, root, "app", `{"schema_version":1,"id":"app","name":"App","web_root":"web","backend":{"executable":"bin/backend"}}`)
	if err := os.Symlink("/etc/passwd", filepath.Join(appRoot, "web", "leak")); err != nil {
		t.Fatal(err)
	}
	_, err := LoadApp(appRoot)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadAppRejectsExecutableThroughSymlink(t *testing.T) {
	root := t.TempDir()
	appRoot := writeTestApp(t, root, "app", `{"schema_version":1,"id":"app","name":"App","web_root":"web","backend":{"executable":"linked/backend"}}`)
	if err := os.Symlink(filepath.Join(appRoot, "bin"), filepath.Join(appRoot, "linked")); err != nil {
		t.Fatal(err)
	}
	_, err := LoadApp(appRoot)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v", err)
	}
}

func writeTestApp(t *testing.T, root, directory, manifest string) string {
	t.Helper()
	appRoot := filepath.Join(root, directory)
	if err := os.MkdirAll(filepath.Join(appRoot, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "web", "index.html"), []byte("app"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "bin", "backend"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return appRoot
}
