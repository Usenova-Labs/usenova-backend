package runtime

import (
	"testing"
	"time"
)

func TestRestartBreaker_AllowsFirstAttempt(t *testing.T) {
	b := newRestartBreaker(RestartPolicy{MaxRestarts: 2, Window: time.Minute, Backoff: 30 * time.Second})

	d := b.decide(time.Now())
	if !d.Allowed {
		t.Fatalf("expected the first attempt to be allowed, got %+v", d)
	}
	if d.Deferred {
		t.Errorf("expected no deferral for the first attempt, got %+v", d)
	}
	if d.Attempts != 0 {
		t.Errorf("expected 0 attempts, got %d", d.Attempts)
	}
}

func TestRestartBreaker_DefersUntilBackoffElapsed(t *testing.T) {
	b := newRestartBreaker(RestartPolicy{MaxRestarts: 3, Window: time.Minute, Backoff: 30 * time.Second})
	now := time.Now()
	b.record(now)

	d := b.decide(now.Add(10 * time.Second))
	if !d.Allowed {
		t.Fatalf("expected the attempt to be allowed (deferred), got %+v", d)
	}
	if !d.Deferred {
		t.Fatalf("expected the attempt to be deferred, got %+v", d)
	}
	if want := now.Add(30 * time.Second); !d.NextAttemptAt.Equal(want) {
		t.Errorf("expected next attempt at %s, got %s", want, d.NextAttemptAt)
	}

	d = b.decide(now.Add(31 * time.Second))
	if !d.Allowed || d.Deferred {
		t.Errorf("expected the attempt to run once the backoff elapsed, got %+v", d)
	}
}

func TestRestartBreaker_RefusesAfterMaxRestarts(t *testing.T) {
	b := newRestartBreaker(RestartPolicy{MaxRestarts: 2, Window: time.Minute})
	now := time.Now()
	b.record(now)
	b.record(now)

	d := b.decide(now)
	if d.Allowed {
		t.Fatalf("expected the attempt to be refused, got %+v", d)
	}
	if d.Attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", d.Attempts)
	}
	if d.Reason == "" {
		t.Error("expected a reason for the refusal")
	}
}

func TestRestartBreaker_WindowRestoresBudget(t *testing.T) {
	b := newRestartBreaker(RestartPolicy{MaxRestarts: 2, Window: time.Minute})
	now := time.Now()
	b.record(now)
	b.record(now)

	if d := b.decide(now); d.Allowed {
		t.Fatalf("expected the attempt to be refused, got %+v", d)
	}

	// Once the attempts fall out of the window the budget is restored.
	if d := b.decide(now.Add(2 * time.Minute)); !d.Allowed {
		t.Fatalf("expected the attempt to be allowed after the window slid, got %+v", d)
	}
	if got := b.count(now.Add(2 * time.Minute)); got != 0 {
		t.Errorf("expected 0 attempts after the window slid, got %d", got)
	}
}

func TestRestartBreaker_UnlimitedWithoutLimit(t *testing.T) {
	b := newRestartBreaker(RestartPolicy{MaxRestarts: 0, Window: time.Minute})
	now := time.Now()
	for i := 0; i < 10; i++ {
		b.record(now)
	}

	if d := b.decide(now); !d.Allowed {
		t.Fatalf("expected restarts to stay unlimited, got %+v", d)
	}
}

func TestRestartBreaker_BackoffGrowsExponentially(t *testing.T) {
	b := newRestartBreaker(RestartPolicy{
		MaxRestarts: 10,
		Window:      time.Hour,
		Backoff:     time.Second,
		MaxBackoff:  8 * time.Second,
	})

	cases := map[int]time.Duration{
		1: time.Second,
		2: 2 * time.Second,
		3: 4 * time.Second,
		4: 8 * time.Second,
		5: 8 * time.Second, // capped
	}
	for attempts, want := range cases {
		if got := b.backoff(attempts); got != want {
			t.Errorf("backoff(%d) = %s, want %s", attempts, got, want)
		}
	}
}

func TestRestartBreaker_Reset(t *testing.T) {
	b := newRestartBreaker(RestartPolicy{MaxRestarts: 1, Window: time.Minute})
	now := time.Now()
	b.record(now)

	if d := b.decide(now); d.Allowed {
		t.Fatalf("expected the attempt to be refused, got %+v", d)
	}

	b.reset()
	if d := b.decide(now); !d.Allowed {
		t.Fatalf("expected the attempt to be allowed after a reset, got %+v", d)
	}
}

func TestDefaultRestartPolicy(t *testing.T) {
	p := DefaultRestartPolicy()
	if p.MaxRestarts != DefaultRestartMaxRestarts {
		t.Errorf("expected %d restarts, got %d", DefaultRestartMaxRestarts, p.MaxRestarts)
	}
	if p.Window != DefaultRestartWindow {
		t.Errorf("expected a %s window, got %s", DefaultRestartWindow, p.Window)
	}
	if p.Backoff != DefaultRestartBackoff {
		t.Errorf("expected a %s backoff, got %s", DefaultRestartBackoff, p.Backoff)
	}
	if p.MaxBackoff != DefaultRestartMaxBackoff {
		t.Errorf("expected a %s max backoff, got %s", DefaultRestartMaxBackoff, p.MaxBackoff)
	}
}
