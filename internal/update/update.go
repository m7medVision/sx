// Package update checks whether a newer sx release exists on GitHub.
//
// The check is best-effort and silent: any problem (no network, timeout,
// rate-limit, malformed response) results in "no update" rather than an error.
// Results are cached for 24h so the frequently-opened TUI popup doesn't hit the
// GitHub API on every launch.
package update

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	releaseURL  = "https://api.github.com/repos/m7medVision/sx/releases/latest"
	cacheTTL    = 24 * time.Hour
	httpTimeout = 3 * time.Second
)

// cacheEntry is the on-disk shape of the cached check result.
type cacheEntry struct {
	CheckedAt int64  `json:"checked_at"`
	Latest    string `json:"latest"`
}

// Check reports the latest release tag and whether it is newer than current.
//
// It returns ("", false) — meaning "show nothing" — when:
//   - current is a non-release build ("dev", "(devel)", "") ;
//   - the check is disabled via SX_NO_UPDATE_CHECK ;
//   - or no version could be determined (offline with no cache).
//
// On a warm cache (<24h) it never touches the network. When the network fetch
// fails but a previous result is cached, it falls back to that cached value so
// a known-pending update still shows while offline.
func Check(current string) (latest string, newer bool) {
	if os.Getenv("SX_NO_UPDATE_CHECK") != "" {
		return "", false
	}
	if !isRelease(current) {
		return "", false
	}

	cached, ok := readCache()
	if ok && time.Since(time.Unix(cached.CheckedAt, 0)) < cacheTTL {
		return cached.Latest, isNewer(cached.Latest, current)
	}

	fetched, err := fetchLatest()
	if err != nil {
		// Offline / rate-limited / bad response: fall back to a stale cache if
		// we have one, otherwise show nothing.
		if ok && cached.Latest != "" {
			return cached.Latest, isNewer(cached.Latest, current)
		}
		return "", false
	}

	writeCache(cacheEntry{CheckedAt: time.Now().Unix(), Latest: fetched})
	return fetched, isNewer(fetched, current)
}

// isRelease reports whether v looks like a real release version (not a local
// or untagged build).
func isRelease(v string) bool {
	switch v {
	case "", "dev", "(devel)":
		return false
	}
	return true
}

// fetchLatest queries the GitHub releases API for the latest tag name.
func fetchLatest() (string, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest(http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &httpError{resp.StatusCode}
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "github: status " + strconv.Itoa(e.code) }

// isNewer reports whether the latest version is strictly newer than current.
// Both are compared as semver-ish major.minor.patch (a leading "v" and any
// pre-release/build suffix are ignored). If either can't be parsed, it falls
// back to a plain inequality so we never crash on odd tags.
func isNewer(latest, current string) bool {
	if latest == "" {
		return false
	}
	lp, lok := parseVersion(latest)
	cp, cok := parseVersion(current)
	if !lok || !cok {
		return normalize(latest) != normalize(current)
	}
	for i := 0; i < 3; i++ {
		if lp[i] != cp[i] {
			return lp[i] > cp[i]
		}
	}
	return false
}

// normalize trims a leading "v" and surrounding whitespace.
func normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// parseVersion parses "v1.2.3" / "1.2.3-rc1" into [3]int{1,2,3}.
func parseVersion(v string) ([3]int, bool) {
	v = normalize(v)
	// Drop a pre-release / build suffix ("1.2.3-rc1", "1.2.3+meta").
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// cachePath returns ~/.cache/sx/update.json (or "" if it can't be determined).
func cachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sx", "update.json")
}

func readCache() (cacheEntry, bool) {
	path := cachePath()
	if path == "" {
		return cacheEntry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil {
		return cacheEntry{}, false
	}
	return e, true
}

func writeCache(e cacheEntry) {
	path := cachePath()
	if path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
