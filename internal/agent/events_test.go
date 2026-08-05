package agent

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestStoreKeepsLatestEventPerSession(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "events.json")}
	if err := store.Put(Event{Session: "api", State: Working, Summary: "starting"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(Event{Session: "api", State: Completed, Summary: "done"}); err != nil {
		t.Fatal(err)
	}

	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	event := events["api"]
	if len(events) != 1 || event.State != Completed || event.Summary != "done" || event.Session != "api" {
		t.Fatalf("events = %#v", events)
	}
}

func TestStoreRejectsInvalidEvent(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "events.json")}
	if err := store.Put(Event{Session: "", State: Working}); err == nil {
		t.Fatal("empty session accepted")
	}
	if err := store.Put(Event{Session: "api", State: State(99)}); err == nil {
		t.Fatal("invalid state accepted")
	}
}

func TestStorePutSerializesConcurrentWrites(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "events.json")}
	const writers = 64
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- store.Put(Event{Session: fmt.Sprintf("session-%d", i), State: Working})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != writers {
		t.Fatalf("stored %d events, want %d", len(events), writers)
	}
}

func TestStoreDeleteRemovesSessionEvent(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "events.json")}
	if err := store.Put(Event{Session: "api", State: Completed}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("api"); err != nil {
		t.Fatal(err)
	}
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := events["api"]; ok {
		t.Fatalf("deleted event remains: %#v", events["api"])
	}
}
