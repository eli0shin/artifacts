package cliconfig

import (
	"path/filepath"
	"testing"
)

func TestPathUsesXDGConfigHome(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("ARTIFACT_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configHome, "artifact", "config.json")
	if path != want {
		t.Fatalf("Path = %q, want %q", path, want)
	}
}

func TestPathUsesOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "artifact.json")
	t.Setenv("ARTIFACT_CONFIG_PATH", want)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if path != want {
		t.Fatalf("Path = %q, want %q", path, want)
	}
}
