package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/m7medVision/sx/internal/agent"
)

func TestAgentNotifyStoresExplicitEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	t.Setenv("SX_AGENT_EVENT_STORE", path)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"agent", "notify", "api", "completed", "released to production"}, &stdout, &stderr); code != 0 {
		t.Fatalf("notify exit = %d, stderr = %s", code, stderr.String())
	}
	events, err := (agent.Store{Path: path}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if event := events["api"]; event.State != agent.Completed || event.Summary != "released to production" {
		t.Fatalf("event = %#v", event)
	}
}

func TestAgentNotifyRejectsMissingOrInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"agent", "notify", "api"}, {"agent", "notify", "api", "later"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "sx agent notify") {
			t.Fatalf("run(%q) = %d, stderr = %q", args, code, stderr.String())
		}
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("one\ntwo"); got != "one" {
		t.Fatalf("firstLine = %q", got)
	}
}
