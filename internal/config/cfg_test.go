package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectActionsMergesAndValidatesNamedActions(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	global := filepath.Join(home, ".config", "sx", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte("actions:\n  review:\n    source: origin/review\n    command: agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectPath(repo), []byte("actions:\n  review:\n    source: local/review\n  fix:\n    source: origin/fix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	actions, err := LoadProjectActions(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions["review"]; got.Source != "local/review" || got.Command != "" {
		t.Fatalf("review action = %#v", got)
	}
	if got := actions["fix"].Source; got != "origin/fix" {
		t.Fatalf("fix source = %q", got)
	}

	if err := os.WriteFile(ProjectPath(repo), []byte("actions:\n  broken:\n    command: agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectActions(repo); err == nil || !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "source") {
		t.Fatalf("invalid action error = %v", err)
	}
}

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
