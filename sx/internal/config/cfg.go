// Package config loads sx config (global + per-project) and applies the
// configured file copy/symlink operations into freshly created worktrees.
package config

import (
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Files describes which gitignored files to bring into a new worktree.
type Files struct {
	Copy    []string `yaml:"copy"`
	Symlink []string `yaml:"symlink"`
}

// Config is the merged sx configuration.
type Config struct {
	WorktreeDir string `yaml:"worktree_dir"`
	Files       Files  `yaml:"files"`
}

// Default config used when nothing is on disk.
func defaults() Config {
	return Config{WorktreeDir: ".worktrees"}
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
