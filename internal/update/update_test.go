package update

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v1.2.0", "v1.2.0", false},
		{"1.2.0", "v1.2.0", false}, // missing "v" is fine
		{"v1.2.0", "v1.3.0", false},
		{"v2.0.0", "v1.9.9", true},
		{"v1.2.10", "v1.2.9", true},     // numeric, not lexical
		{"v1.2.0", "v1.2.0-rc1", false}, // suffix stripped → equal
		{"v1.2.1", "v1.2.0-rc1", true},  // suffix stripped → newer
		{"", "v1.2.0", false},           // no latest
		{"weird", "v1.2.0", true},       // unparseable → inequality fallback
		{"weird", "weird", false},       // unparseable but equal
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for _, v := range []string{"", "dev", "(devel)"} {
		if isRelease(v) {
			t.Errorf("isRelease(%q) = true, want false", v)
		}
	}
	for _, v := range []string{"v1.0.0", "1.2.3"} {
		if !isRelease(v) {
			t.Errorf("isRelease(%q) = false, want true", v)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v, ok := parseVersion("v1.2.3-rc1"); !ok || v != [3]int{1, 2, 3} {
		t.Errorf("parseVersion(v1.2.3-rc1) = %v, %v", v, ok)
	}
	if _, ok := parseVersion("1.2"); ok {
		t.Error("parseVersion(1.2) should fail")
	}
}
