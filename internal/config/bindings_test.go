package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBindingsOverlaysDefaults(t *testing.T) {
	got, err := ResolveBindings(map[string]string{"command-palette": "ctrl-p", "new-session": "ctrl-o"})
	if err != nil {
		t.Fatal(err)
	}
	if got["command-palette"] != "ctrl-p" || got["new-session"] != "ctrl-o" {
		t.Fatalf("resolved bindings = %#v", got)
	}
	if got["kill-session"] != "ctrl-x" { // unspecified defaults remain stable
		t.Fatalf("kill binding = %q", got["kill-session"])
	}
}

func TestLoadBindingsReportsMalformedConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "sx", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("bindings: [not-a-map]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBindings(""); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want named config failure", err)
	}
}

func TestResolveBindingsRejectsInvalidConfiguration(t *testing.T) {
	for name, bindings := range map[string]map[string]string{
		"unknown action": {"launch-missiles": "x"},
		"invalid key":    {"new-session": "not-a-key"},
		"collision":      {"new-session": "ctrl-x"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveBindings(bindings)
			if err == nil || !strings.Contains(err.Error(), "action") && !strings.Contains(err.Error(), "binding") {
				t.Fatalf("error = %v, want clear configuration failure", err)
			}
		})
	}
}
