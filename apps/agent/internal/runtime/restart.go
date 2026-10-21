package runtime

import (
	"fmt"
	"time"
)

// RestartPolicy bounds how often the agent restarts a failing service and how
// long it waits between attempts.
//
// Without a limit a container that keeps crashing (or keeps failing its
// health checks) is restarted forever: the agent hides the failure behind an
// endless restart loop. The policy turns that loop into a bounded number of
// attempts, spaced out by a backoff, after which the service is marked failed
// and left alone until an operator redeploys it.
type RestartPolicy struct {
	// MaxRestarts is the number of restart attempts allowed inside Window.
	// Values <= 0 disable the limit (restarts are never capped).
	MaxRestarts int

	// Window is the sliding period over which restart attempts are counted.
	// Attempts older than the window no longer count towards MaxRestarts, so
	// a service that stays healthy long enough gets a fresh budget.
	Window time.Duration

	// Backoff is the delay applied before the next attempt. The delay doubles
	// with every consecutive attempt, capped by MaxBackoff.
	Backoff time.Duration

	// MaxBackoff caps the exponential growth of Backoff. Zero means no cap.
	MaxBackoff time.Duration
}

// Agent-wide restart defaults. They are used when nothing is configured and
// are overridable per service through the service spec.
const (
	// DefaultRestartMaxRestarts is the number of restart attempts allowed per
	// window before a service is marked failed.
	DefaultRestartMaxRestarts = 3
	// DefaultRestartWindow is the sliding window over which attempts count.
	DefaultRestartWindow = 10 * time.Minute
	// DefaultRestartBackoff is the base delay between restart attempts.
	DefaultRestartBackoff = 30 * time.Second
	// DefaultRestartMaxBackoff caps the exponential backoff growth.
	DefaultRestartMaxBackoff = 5 * time.Minute
)

// DefaultRestartPolicy returns the restart limits used when nothing is
// configured.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{
		MaxRestarts: DefaultRestartMaxRestarts,
		Window:      DefaultRestartWindow,
		Backoff:     DefaultRestartBackoff,
		MaxBackoff:  DefaultRestartMaxBackoff,
	}
}

// restartDecision is the outcome of asking the breaker whether another restart
// attempt may be made.
type restartDecision struct {
	// Allowed reports whether a restart attempt may be made at all.
	Allowed bool

	// Deferred reports that the attempt is allowed but not yet: the backoff
	// has not elapsed, so the attempt must wait until NextAttemptAt.
	Deferred bool

	// NextAttemptAt is the earliest time a deferred attempt may run.
	NextAttemptAt time.Time

	// Attempts is the number of attempts counted inside the current window.
	Attempts int

	// Reason explains a refusal (empty when the attempt is allowed).
	Reason string
}

// restartBreaker tracks the restart attempts of a single service. It is not
// safe for concurrent use; the runtime manager serialises access to it.
type restartBreaker struct {
	policy   RestartPolicy
	attempts []time.Time
	last     time.Time
}

// newRestartBreaker creates a breaker for a service with the given policy.
func newRestartBreaker(policy RestartPolicy) *restartBreaker {
	return &restartBreaker{policy: policy}
}

// decide reports whether a restart attempt may be made at now.
func (b *restartBreaker) decide(now time.Time) restartDecision {
	b.prune(now)
	attempts := len(b.attempts)

	if b.policy.MaxRestarts > 0 && attempts >= b.policy.MaxRestarts {
		return restartDecision{
			Allowed:  false,
			Attempts: attempts,
			Reason:   fmt.Sprintf("%d restarts within %s", attempts, b.policy.Window),
		}
	}

	if attempts == 0 {
		return restartDecision{Allowed: true}
	}

	// The attempt must wait for the backoff that follows the previous one.
	next := b.attempts[attempts-1].Add(b.backoff(attempts))
	if now.Before(next) {
		return restartDecision{
			Allowed:       true,
			Deferred:      true,
			NextAttemptAt: next,
			Attempts:      attempts,
		}
	}

	return restartDecision{Allowed: true, Attempts: attempts}
}

// record registers a restart attempt.
func (b *restartBreaker) record(now time.Time) {
	b.attempts = append(b.attempts, now)
	b.last = now
	b.prune(now)
}

// reset clears the recorded attempts.
func (b *restartBreaker) reset() {
	b.attempts = nil
	b.last = time.Time{}
}

// count returns the number of attempts inside the current window.
func (b *restartBreaker) count(now time.Time) int {
	b.prune(now)
	return len(b.attempts)
}

// prune drops attempts that fell out of the sliding window.
func (b *restartBreaker) prune(now time.Time) {
	if b.policy.Window <= 0 {
		return
	}
	cutoff := now.Add(-b.policy.Window)
	drop := 0
	for drop < len(b.attempts) && !b.attempts[drop].After(cutoff) {
		drop++
	}
	if drop == 0 {
		return
	}
	b.attempts = append(b.attempts[:0], b.attempts[drop:]...)
}

// backoff returns the delay to apply after the given number of consecutive
// attempts. The delay doubles with every attempt and is capped by MaxBackoff.
func (b *restartBreaker) backoff(attempts int) time.Duration {
	base := b.policy.Backoff
	if base <= 0 || attempts <= 0 {
		return 0
	}

	delay := base
	for i := 1; i < attempts; i++ {
		if b.policy.MaxBackoff > 0 && delay >= b.policy.MaxBackoff {
			return b.policy.MaxBackoff
		}
		delay *= 2
	}
	if b.policy.MaxBackoff > 0 && delay > b.policy.MaxBackoff {
		return b.policy.MaxBackoff
	}
	return delay
}
