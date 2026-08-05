package agent

import "testing"

func TestAssessReportsStateEvidenceAndCoverage(t *testing.T) {
	assessment := Assess([]string{"shell $", "Do you want to proceed?"}, Defaults(), true)
	if assessment.State != Blocked || assessment.Source != Heuristic || assessment.Confidence != High || assessment.Coverage != Complete || assessment.Evidence != "do you want to proceed" {
		t.Fatalf("assessment = %#v", assessment)
	}
}

func TestAssessDisclosesIncompletePaneCoverage(t *testing.T) {
	assessment := Assess([]string{"✻ thinking (esc to interrupt)"}, Defaults(), false)
	if assessment.State != Working || assessment.Coverage != Partial {
		t.Fatalf("assessment = %#v", assessment)
	}
}

func TestAssessBlockedBeatsCompletedAcrossPanes(t *testing.T) {
	assessment := Assess([]string{"task completed", "Do you want to proceed?"}, Defaults(), true)
	if assessment.State != Blocked {
		t.Fatalf("assessment = %#v", assessment)
	}
}

func TestDetect(t *testing.T) {
	m := Defaults()
	cases := []struct {
		name string
		text string
		want State
	}{
		{"working spinner", "✻ Brewing… (12s · esc to interrupt)", Working},
		{"plan mode", "⏸ plan mode on (shift+tab to cycle)\n? for shortcuts", Plan},
		{"approval prompt", "Do you want to proceed?\n❯ 1. Yes\n  2. No", Blocked},
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

func TestEventAssessmentTakesPrecedenceOverHeuristic(t *testing.T) {
	heuristic := Assess([]string{"✻ thinking (esc to interrupt)"}, Defaults(), true)
	event := Event{Session: "api", State: Completed, Summary: "deployed"}
	got := WithEvent(heuristic, event)
	if got.State != Completed || got.Source != EventSource || got.Confidence != High || got.Coverage != Complete || got.Summary != "deployed" {
		t.Fatalf("event assessment = %#v", got)
	}
}

func TestParseState(t *testing.T) {
	state, err := ParseState("working")
	if err != nil || state != Working {
		t.Fatalf("ParseState(working) = %v, %v", state, err)
	}
	if _, err := ParseState("unknown"); err == nil {
		t.Fatal("ParseState(unknown) succeeded")
	}
}

func TestDetectWorkingBeatsPlan(t *testing.T) {
	// Agent working while plan mode is on → Working wins (busy is the truth).
	got := Detect("plan mode on\n✻ thinking (esc to interrupt)", Defaults())
	if got != Working {
		t.Errorf("Detect()=%v, want Working", got)
	}
}
