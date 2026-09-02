package policy

import (
	"testing"
	"time"
)

func TestBurstIsAllowedThenThrottled(t *testing.T) {
	// A legitimate flurry -- fanning a question out and reading answers back --
	// must not be mistaken for a runaway, but a loop must stop quickly.
	r := newRateKeeper(60) // 60/min, burst 20
	now := time.Now()

	allowed := 0
	for i := 0; i < 40; i++ {
		if ok, _ := r.allow("peer", now); ok {
			allowed++
		}
	}
	if allowed < 15 || allowed > 25 {
		t.Fatalf("burst allowance is %d, expected about 20", allowed)
	}
	ok, wait := r.allow("peer", now)
	if ok {
		t.Fatal("a peer past its burst was still allowed")
	}
	if wait <= 0 || wait > time.Minute {
		t.Fatalf("unhelpful wait hint: %v", wait)
	}
}

func TestTokensRefillOverTime(t *testing.T) {
	r := newRateKeeper(60)
	now := time.Now()
	for i := 0; i < 40; i++ {
		r.allow("peer", now)
	}
	if ok, _ := r.allow("peer", now); ok {
		t.Fatal("expected to be throttled")
	}
	// A minute later the peer is welcome again.
	if ok, _ := r.allow("peer", now.Add(time.Minute)); !ok {
		t.Fatal("a peer that waited was still refused")
	}
}

// One noisy peer must not spend another peer's allowance.
func TestPeersAreLimitedIndependently(t *testing.T) {
	r := newRateKeeper(60)
	now := time.Now()
	for i := 0; i < 40; i++ {
		r.allow("noisy", now)
	}
	if ok, _ := r.allow("quiet", now); !ok {
		t.Fatal("a quiet peer was throttled because another was noisy")
	}
}

func TestNegativeMeansUnlimited(t *testing.T) {
	r := newRateKeeper(-1)
	now := time.Now()
	for i := 0; i < 5000; i++ {
		if ok, _ := r.allow("peer", now); !ok {
			t.Fatal("a node configured for no limit throttled a peer")
		}
	}
}

func TestRateRefusalIsReportedThroughCheck(t *testing.T) {
	s, err := Load("", true, 60)
	if err != nil {
		t.Fatal(err)
	}
	var refused Decision
	for i := 0; i < 60; i++ {
		if d := s.Check("peer", "k", false); !d.Allowed {
			refused = d
			break
		}
	}
	if refused.Allowed || refused.Reason == "" {
		t.Fatal("a peer sending far past the limit was never refused")
	}
	if !contains(refused.Reason, "wait") {
		t.Fatalf("refusal does not say what to do: %q", refused.Reason)
	}
}

// A blocked peer should be told it is blocked, not told to slow down: the two
// call for completely different actions.
func TestBlockingIsReportedBeforeRate(t *testing.T) {
	s, _ := Load("", true, 1)
	s.Check("peer", "k", false)
	s.SetBlocked("peer", true)
	for i := 0; i < 10; i++ {
		d := s.Check("peer", "k", false)
		if d.Allowed {
			t.Fatal("a blocked peer got through")
		}
		if !contains(d.Reason, "blocked") {
			t.Fatalf("a blocked peer was told %q instead of that it is blocked", d.Reason)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
