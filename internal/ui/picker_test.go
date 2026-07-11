package ui

import "testing"

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		query, path string
		want        bool
	}{
		{"", "anything", true},
		{".env", ".env", true},
		{".env", "src/.env.local", true},
		{"en", "src/.env.local", true},
		{"zzz", "src/main.go", false},
		{"mn", "main.go", true},
		{".e", ".example.txt", true},
	}
	for _, c := range cases {
		_, ok := fuzzyMatch(c.query, c.path)
		if ok != c.want {
			t.Errorf("fuzzyMatch(%q, %q) ok=%v want %v", c.query, c.path, ok, c.want)
		}
	}
}

func TestPickFilterOrdering(t *testing.T) {
	files := []string{
		"src/main.go",
		"main.go",
		"Makefile",
		"README.md",
	}
	// "main" should rank main.go higher than src/main.go (shorter).
	got := pickFilter(files, "main")
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0] != 1 {
		t.Errorf("expected main.go first, got index %d", got[0])
	}
}

func TestAppendUniqueGlob(t *testing.T) {
	s := []string{"a", "b"}
	s = appendUniqueGlob(s, "c")
	if len(s) != 3 {
		t.Errorf("expected 3, got %d", len(s))
	}
	s = appendUniqueGlob(s, "a")
	if len(s) != 3 {
		t.Errorf("expected 3 (no dup), got %d", len(s))
	}
}
