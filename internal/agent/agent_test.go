package agent

import "testing"

func TestDetect(t *testing.T) {
	m := Defaults()
	cases := []struct {
		name string
		text string
		want State
	}{
		{"working spinner", "✻ Brewing… (12s · esc to interrupt)", Working},
		{"plan mode", "⏸ plan mode on (shift+tab to cycle)\n? for shortcuts", Plan},
		{"approval prompt", "Do you want to proceed?\n❯ 1. Yes\n  2. No", Waiting},
		{"idle agent prompt", "│ >                              │\n? for shortcuts", Waiting},
		// Real capture: idle Claude in auto mode — the footer still says
		// "esc to interrupt", so it must NOT be flagged Working.
		{"idle auto-mode footer", "❯ \n  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← for agents", Waiting},
		{"opencode present", "opencode  v0.1.0\n> ", Waiting},
		{"bare shell", "user@host ~/code $ ", Idle},
		{"empty", "", Idle},
	}
	for _, c := range cases {
		if got := Detect(c.text, m); got != c.want {
			t.Errorf("%s: Detect()=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestDetectWorkingBeatsPlan(t *testing.T) {
	// Agent working while plan mode is on → Working wins (busy is the truth).
	got := Detect("plan mode on\n✻ thinking (esc to interrupt)", Defaults())
	if got != Working {
		t.Errorf("Detect()=%v, want Working", got)
	}
}
