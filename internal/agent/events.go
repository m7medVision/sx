package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Event is the latest lifecycle state a supported agent explicitly reported.
type Event struct {
	Session      string    `json:"session"`
	State        State     `json:"state"`
	Summary      string    `json:"summary,omitempty"`
	At           time.Time `json:"at"`
	Acknowledged bool      `json:"acknowledged"`
}

// Store persists the latest reported event for every session.
type Store struct {
	Path string
}

// DefaultStore uses XDG_STATE_HOME (or its conventional fallback). SX_AGENT_EVENT_STORE
// is provided for isolated integrations and tests.
func DefaultStore() Store {
	if path := os.Getenv("SX_AGENT_EVENT_STORE"); path != "" {
		return Store{Path: path}
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Store{Path: filepath.Join(".local", "state", "sx", "agent-events.json")}
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return Store{Path: filepath.Join(stateHome, "sx", "agent-events.json")}
}

// Load returns no events when the store has not been created yet.
func (s Store) Load() (map[string]Event, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent event store: %w", err)
	}
	events := map[string]Event{}
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode agent event store: %w", err)
	}
	return events, nil
}

// Put validates and atomically records an event. A sidecar advisory lock
// serializes the complete read-modify-write operation across sx processes.
func (s Store) Put(event Event) error {
	event.Session = strings.TrimSpace(event.Session)
	event.Summary = strings.TrimSpace(event.Summary)
	if event.Session == "" {
		return errors.New("session is required")
	}
	if _, err := ParseState(event.State.String()); err != nil {
		return err
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	return s.withLock(func() error {
		events, err := s.Load()
		if err != nil {
			return err
		}
		// A new report is always unread, including when it updates a previously
		// acknowledged lifecycle record.
		event.Acknowledged = false
		events[event.Session] = event
		return s.write(events)
	})
}

// Acknowledge marks a lifecycle record read without discarding its summary or
// timestamp. It is intentionally separate from Put so only a visit can read it.
func (s Store) Acknowledge(session string) error {
	session = strings.TrimSpace(session)
	if session == "" {
		return errors.New("session is required")
	}
	return s.withLock(func() error {
		events, err := s.Load()
		if err != nil {
			return err
		}
		event, ok := events[session]
		if !ok || event.Acknowledged {
			return nil
		}
		event.Acknowledged = true
		events[session] = event
		return s.write(events)
	})
}

// Delete removes the lifecycle event for session so a reused session name is
// assessed from its panes again.
func (s Store) Delete(session string) error {
	session = strings.TrimSpace(session)
	if session == "" {
		return errors.New("session is required")
	}
	return s.withLock(func() error {
		events, err := s.Load()
		if err != nil {
			return err
		}
		if _, ok := events[session]; !ok {
			return nil
		}
		delete(events, session)
		return s.write(events)
	})
}

// withLock holds an advisory flock on a stable sidecar file. Locking the
// store itself would be ineffective because each atomic replacement changes
// the inode being locked.
func (s Store) withLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create agent event store: %w", err)
	}
	lock, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open agent event store lock: %w", err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock agent event store: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	return fn()
}

// write replaces the store atomically while the caller holds its lock.
func (s Store) write(events map[string]Event) error {
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent event store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".agent-events-*")
	if err != nil {
		return fmt.Errorf("create agent event store: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write agent event store: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure agent event store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close agent event store: %w", err)
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		return fmt.Errorf("replace agent event store: %w", err)
	}
	return nil
}
