package storetest_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// stepClock advances a millisecond on every reading.
func stepClock() func() time.Time {
	var mu sync.Mutex
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t = t.Add(time.Millisecond)
		return t
	}
}

func TestFake(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return storetest.NewFake(stepClock()) })
}

// Every reading the same instant: ordering must not rely on the clock.
func TestFakeFixedClock(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	storetest.Run(t, func(*testing.T) store.Store { return storetest.NewFake(func() time.Time { return at }) })
}

func TestFakeRealClock(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return storetest.NewFake(nil) })
}

func TestFakeClosed(t *testing.T) {
	f := storetest.NewFake(nil)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Ping(t.Context()); err == nil {
		t.Fatal("ping after close succeeded")
	}
	if _, err := f.CreateJob(t.Context(), "user1"); err == nil {
		t.Fatal("create after close succeeded")
	}
}

func TestFakeCancelledContext(t *testing.T) {
	f := storetest.NewFake(nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.CreateJob(ctx, "user1"); err == nil {
		t.Fatal("create with a cancelled context succeeded")
	}
}

func TestPath(t *testing.T) {
	for _, to := range storetest.States {
		p := storetest.Path(store.Uploading, to)
		if p == nil {
			t.Fatalf("no path from uploading to %s", to)
		}
		prev := store.Uploading
		for _, s := range p {
			if !store.CanTransition(prev, s) {
				t.Fatalf("path to %s: %s → %s is not allowed", to, prev, s)
			}
			prev = s
		}
		if to != store.Uploading && prev != to {
			t.Fatalf("path to %s ends in %s", to, prev)
		}
	}
	if storetest.Path(store.Done, store.Uploading) != nil {
		t.Fatal("found a path out of done")
	}
}
