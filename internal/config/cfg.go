// Package config loads sx config (global + per-project) and applies the
// configured file copy/symlink operations into freshly created worktrees.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/m7medVision/sx/internal/agent"

	"gopkg.in/yaml.v3"
)

// Files describes which gitignored files to bring into a new worktree.
type Files struct {
	Copy    []string `yaml:"copy"`
	Symlink []string `yaml:"symlink"`
}

// Config is the merged sx configuration.
type Config struct {
	WorktreeDir   string            `yaml:"worktree_dir"`
	Files         Files             `yaml:"files"`
	Agents        agent.Markers     `yaml:"agents"`         // extra agent-detection markers
	ReviewCommand string            `yaml:"review_command"` // explicit external review command
	Bindings      map[string]string `yaml:"bindings"`       // action name -> key
}

// Scope identifies which config file is being edited.
type Scope int

const (
	GlobalScope Scope = iota
	ProjectScope
)

// Values is the user-editable subset of Config used by the TUI config view
// (worktree_dir + file globs). Agent markers are shown but not edited there.
type Values struct {
	WorktreeDir string   `yaml:"worktree_dir"`
	Copy        []string `yaml:"copy"`
	Symlink     []string `yaml:"symlink"`
}

// FromConfig projects the editable fields out of a Config.
func FromConfig(c Config) Values {
	return Values{
		WorktreeDir: c.WorktreeDir,
		Copy:        append([]string(nil), c.Files.Copy...),
		Symlink:     append([]string(nil), c.Files.Symlink...),
	}
}

// ToConfig merges the editable values back into a base Config (preserving
// non-editable fields like agent markers).
func (v Values) ToConfig(base Config) Config {
	base.WorktreeDir = v.WorktreeDir
	base.Files.Copy = append([]string(nil), v.Copy...)
	base.Files.Symlink = append([]string(nil), v.Symlink...)
	return base
}

// Markers returns the built-in agent-detection markers with any configured
// markers appended (config extends, never replaces, the defaults).
func (c Config) Markers() agent.Markers {
	m := agent.Defaults()
	m.NeedsInput = append(m.NeedsInput, c.Agents.NeedsInput...)
	m.Working = append(m.Working, c.Agents.Working...)
	m.Plan = append(m.Plan, c.Agents.Plan...)
	m.Present = append(m.Present, c.Agents.Present...)
	return m
}

// Default config used when nothing is on disk.
func defaults() Config {
	return Config{WorktreeDir: ".worktrees"}
}

// DefaultBindings are the primary familiar controls for actions exposed by the
// command palette. Additional legacy aliases (such as j/k) remain UI details.
func DefaultBindings() map[string]string {
	return map[string]string{
		"switch-session": "enter", "new-session": "ctrl-n", "new-worktree": "ctrl-w",
		"kill-session": "ctrl-x", "review-worktree": "r", "move-up": "up",
		"move-down": "down", "toggle-preview": "p", "toggle-attention": "a",
		"next-attention": "n", "help": "?", "config": "c", "command-palette": ":",
		"quit": "q",
	}
}

// LoadBindings loads and resolves bindings from the global then project scope.
// Unlike Load, it returns malformed configuration to the interactive caller.
func LoadBindings(repoRoot string) (map[string]string, error) {
	overrides := make(map[string]string)
	global, err := GlobalPath()
	if err != nil {
		return nil, fmt.Errorf("find global config: %w", err)
	}
	paths := []string{global}
	if repoRoot != "" {
		paths = append(paths, ProjectPath(repoRoot))
	}
	for _, path := range paths {
		cfg, err := LoadScope(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		for action, key := range cfg.Bindings {
			overrides[action] = key
		}
	}
	return ResolveBindings(overrides)
}

// ResolveBindings overlays developer bindings on the defaults. Each action may
// have one binding and each binding may invoke one action; violations are
// rejected rather than silently choosing an arbitrary winner.
func ResolveBindings(overrides map[string]string) (map[string]string, error) {
	resolved := DefaultBindings()
	valid := DefaultBindings()
	actions := make([]string, 0, len(overrides))
	for action := range overrides {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		key := overrides[action]
		if _, ok := valid[action]; !ok {
			return nil, fmt.Errorf("unknown action %q", action)
		}
		if !validBindingKey(key) {
			return nil, fmt.Errorf("invalid binding %q for action %q", key, action)
		}
		resolved[action] = normalizeBindingKey(key)
	}
	used := make(map[string]string, len(resolved))
	actions = actions[:0]
	for action := range resolved {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		key := resolved[action]
		if other, occupied := used[key]; occupied {
			return nil, fmt.Errorf("binding %q is assigned to both %q and %q", key, other, action)
		}
		used[key] = action
	}
	return resolved, nil
}

func normalizeBindingKey(key string) string { return strings.ToLower(strings.TrimSpace(key)) }

func validBindingKey(key string) bool {
	key = normalizeBindingKey(key)
	if len([]rune(key)) == 1 {
		return true
	}
	switch key {
	case "enter", "up", "down", "esc", "tab":
		return true
	}
	return len(key) == len("ctrl-x") && strings.HasPrefix(key, "ctrl-") && key[len("ctrl-")] >= 'a' && key[len("ctrl-")] <= 'z'
}

// GlobalPath is the path to the global config file.
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "sx", "config.yaml"), nil
}

// ProjectPath is the path to the per-project config (.sx.yaml) at repoRoot.
func ProjectPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".sx.yaml")
}

// LoadScope reads a single config file. A missing file yields defaults rather
// than an error so callers can edit and save a fresh config.
func LoadScope(path string) (Config, error) {
	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.WorktreeDir == "" {
		cfg.WorktreeDir = ".worktrees"
	}
	return cfg, nil
}

// SaveScope writes cfg to path as YAML, creating parent dirs as needed.
func SaveScope(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Load reads the global config (~/.config/sx/config.yaml) then overlays the
// per-project config (.sx.yaml at repoRoot). Both are optional.
func Load(repoRoot string) Config {
	cfg := defaults()

	if home, err := os.UserHomeDir(); err == nil {
		merge(&cfg, filepath.Join(home, ".config", "sx", "config.yaml"))
	}
	if repoRoot != "" {
		merge(&cfg, filepath.Join(repoRoot, ".sx.yaml"))
	}
	if cfg.WorktreeDir == "" {
		cfg.WorktreeDir = ".worktrees"
	}
	return cfg
}

// merge overlays the YAML at path onto cfg (fields present in the file win).
func merge(cfg *Config, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var in Config
	if yaml.Unmarshal(data, &in) != nil {
		return
	}
	if in.WorktreeDir != "" {
		cfg.WorktreeDir = in.WorktreeDir
	}
	if len(in.Files.Copy) > 0 {
		cfg.Files.Copy = in.Files.Copy
	}
	if len(in.Files.Symlink) > 0 {
		cfg.Files.Symlink = in.Files.Symlink
	}
	if in.ReviewCommand != "" {
		cfg.ReviewCommand = in.ReviewCommand
	}
	if len(in.Bindings) > 0 {
		if cfg.Bindings == nil {
			cfg.Bindings = make(map[string]string)
		}
		for action, key := range in.Bindings {
			cfg.Bindings[action] = key
		}
	}
	// Agent markers accumulate across config layers (global + per-project).
	cfg.Agents.NeedsInput = append(cfg.Agents.NeedsInput, in.Agents.NeedsInput...)
	cfg.Agents.Working = append(cfg.Agents.Working, in.Agents.Working...)
	cfg.Agents.Plan = append(cfg.Agents.Plan, in.Agents.Plan...)
	cfg.Agents.Present = append(cfg.Agents.Present, in.Agents.Present...)
}

// ApplyFiles copies and symlinks the configured globs from repoRoot into
// wtPath, preserving each match's path relative to repoRoot. Worktrees are
// clean checkouts, so this is how .env, node_modules, etc. get in.
func (c Config) ApplyFiles(repoRoot, wtPath string) error {
	for _, pattern := range c.Files.Copy {
		if err := each(repoRoot, pattern, func(rel, src string) error {
			return copyPath(src, filepath.Join(wtPath, rel))
		}); err != nil {
			return err
		}
	}
	for _, pattern := range c.Files.Symlink {
		if err := each(repoRoot, pattern, func(rel, src string) error {
			return symlink(src, filepath.Join(wtPath, rel))
		}); err != nil {
			return err
		}
	}
	return nil
}

// each expands a glob relative to repoRoot and runs fn(relPath, absSrc) for
// every match.
func each(repoRoot, pattern string, fn func(rel, src string) error) error {
	matches, err := filepath.Glob(filepath.Join(repoRoot, pattern))
	if err != nil {
		return err
	}
	for _, src := range matches {
		rel, err := filepath.Rel(repoRoot, src)
		if err != nil {
			return err
		}
		if err := fn(rel, src); err != nil {
			return err
		}
	}
	return nil
}

// symlink creates dst as a symlink to src, replacing anything already there.
func symlink(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(dst)
	return os.Symlink(src, dst)
}

// copyPath copies a file or directory tree from src to dst.
func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
