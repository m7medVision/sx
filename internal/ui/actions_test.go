package ui

import (
	"strings"
	"testing"
)

func TestFilterActionsSearchesCatalog(t *testing.T) {
	got := filterActions("work")
	if len(got) != 2 || got[0] != "new-worktree" || got[1] != "review-worktree" {
		t.Fatalf("work search = %#v", got)
	}
}

func TestDispatchActionInvokesSupportedAction(t *testing.T) {
	app := &App{mode: modeList, previewOn: true}
	if err := app.DispatchAction("toggle-preview", nil, nil); err != nil {
		t.Fatal(err)
	}
	if app.previewOn {
		t.Fatal("toggle-preview action did not dispatch")
	}
	if err := app.DispatchAction("missing", nil, nil); err == nil || !strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("unknown action error = %v", err)
	}
}
