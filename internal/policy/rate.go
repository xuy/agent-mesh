package policy

import (
	"fmt"
	"sync"
	"time"
)

// DefaultRatePerMinute is what a peer may send before being told to slow down.
//
// Generous for anything a person drives and for normal agent traffic, and low
// enough that an agent stuck in a loop is stopped in seconds rather than after
// it has spent someone else's tokens for an hour. The failure this guards
// against is not malice, it is a retry loop with no backoff, which is the most
// ordinary bug there is.
const DefaultRatePerMinute = 60

// burstFraction is how much of a minute's allowance can arrive at once, so a
// legitimate flurry -- an agent fanning a question out and reading answers back
// -- is not mistaken for a runaway.
const burstFraction = 3

// limiter is a token bucket per peer.
type limiter struct {
	tokens float64
	last   time.Time
}

type rateKeeper struct {
	mu        sync.Mutex
	buckets   map[string]*limiter
	perMinute float64
}

func newRateKeeper(perMinute int) *rateKeeper {
	if perMinute == 0 {
		perMinute = DefaultRatePerMinute
	}
	return &rateKeeper{buckets: map[string]*limiter{}, perMinute: float64(perMinute)}
}

// allow reports whether a peer may send now, and how long to wait if not.
func (r *rateKeeper) allow(name string, now time.Time) (bool, time.Duration) {
	if r.perMinute < 0 {
		return true, 0 // explicitly unlimited
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	burst := r.perMinute / burstFraction
	if burst < 1 {
		burst = 1
	}
	b, ok := r.buckets[name]
	if !ok {
		b = &limiter{tokens: burst, last: now}
		r.buckets[name] = b
	}
	b.tokens += now.Sub(b.last).Minutes() * r.perMinute
	b.last = now
	if b.tokens > burst {
		b.tokens = burst
	}
	if b.tokens < 1 {
		// Time until one whole token exists again.
		wait := time.Duration((1 - b.tokens) / r.perMinute * float64(time.Minute))
		return false, wait
	}
	b.tokens--
	return true, 0
}

func rateReason(wait time.Duration) string {
	return fmt.Sprintf("you are sending faster than this node accepts; wait %s and try again",
		wait.Round(time.Second))
}
