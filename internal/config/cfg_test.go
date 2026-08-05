package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIncludesConfiguredReviewCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "sx", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("review_command: code --reuse-window .\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load("").ReviewCommand; got != "code --reuse-window ." {
		t.Fatalf("review command = %q", got)
	}
}
