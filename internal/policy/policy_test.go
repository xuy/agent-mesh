package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

func store(t *testing.T, openByDefault bool) *Store {
	t.Helper()
	s, err := Load(filepath.Join(t.TempDir(), "peers.json"), openByDefault)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFirstContactPinsTheKey(t *testing.T) {
	s := store(t, true)
	if d := s.Check("windows", "keyA", false); !d.Allowed {
		t.Fatalf("first contact refused: %s", d.Reason)
	}
	p := s.Get("windows")
	if p == nil || p.Key != "keyA" {
		t.Fatalf("key was not pinned: %+v", p)
	}
	if p.Verified {
		t.Error("a peer nobody checked should not be marked verified")
	}
}

// The attack this exists for: someone claims a name that is already taken,
// with a different key. Accepting silently is how a name gets stolen.
func TestADifferentKeyUnderAKnownNameIsRefused(t *testing.T) {
	s := store(t, true)
	s.Check("windows", "keyA", false)

	d := s.Check("windows", "keyB", false)
	if d.Allowed {
		t.Fatal("a different key was accepted under an established name")
	}
	if !strings.Contains(d.Reason, "mesh forget windows") {
		t.Errorf("refusal does not say how to resolve it: %s", d.Reason)
	}
	if got := s.Get("windows").Key; got != "keyA" {
		t.Errorf("the pinned key was overwritten by the impostor: %q", got)
	}
}

func TestForgetAcceptsARebuiltPeer(t *testing.T) {
	s := store(t, true)
	s.Check("windows", "keyA", false)
	if err := s.Forget("windows"); err != nil {
		t.Fatal(err)
	}
	if d := s.Check("windows", "keyB", false); !d.Allowed {
		t.Fatalf("a forgotten peer could not re-pin: %s", d.Reason)
	}
	if got := s.Get("windows").Key; got != "keyB" {
		t.Errorf("new key not pinned: %q", got)
	}
}

// Telling costs nothing and commits no one; asking spends the node's tokens or
// runs its commands. A node that executes should not do that for a stranger.
func TestExecutingNodesRefuseWorkFromUnknownPeers(t *testing.T) {
	s := store(t, false)
	if d := s.Check("stranger", "k", false); !d.Allowed {
		t.Fatal("a tell was refused; telling should always be allowed")
	}
	d := s.Check("stranger", "k", true)
	if d.Allowed {
		t.Fatal("an unknown peer was allowed to make an executing node work")
	}
	if !strings.Contains(d.Reason, "mesh allow stranger") {
		t.Errorf("refusal does not name the command that grants it: %s", d.Reason)
	}
}

func TestMailboxNodesAcceptWorkByDefault(t *testing.T) {
	// A mailbox node's "work" is showing a human a question, so the human is
	// the check and being open is right.
	s := store(t, true)
	if d := s.Check("stranger", "k", true); !d.Allowed {
		t.Fatalf("a mailbox node refused a question: %s", d.Reason)
	}
}

func TestAllowGrantsAndDenyWithdraws(t *testing.T) {
	s := store(t, false)
	s.Check("peer", "k", true)
	if err := s.SetMayAsk("peer", true); err != nil {
		t.Fatal(err)
	}
	if d := s.Check("peer", "k", true); !d.Allowed {
		t.Fatalf("allow did not take effect: %s", d.Reason)
	}
	if err := s.SetMayAsk("peer", false); err != nil {
		t.Fatal(err)
	}
	if d := s.Check("peer", "k", true); d.Allowed {
		t.Fatal("deny did not take effect")
	}
}

// Blocking has to bite on the next message, not the next restart: a peer that
// already has a tunnel open must not get one more request in.
func TestBlockingTakesEffectImmediately(t *testing.T) {
	s := store(t, true)
	s.Check("peer", "k", true)
	if err := s.SetBlocked("peer", true); err != nil {
		t.Fatal(err)
	}
	if d := s.Check("peer", "k", false); d.Allowed {
		t.Fatal("a blocked peer still got through, and not even with a question")
	}
	if err := s.SetBlocked("peer", false); err != nil {
		t.Fatal(err)
	}
	if d := s.Check("peer", "k", false); !d.Allowed {
		t.Fatal("unblock did not restore the peer")
	}
}

func TestDecisionsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	s1, _ := Load(path, true)
	s1.Check("peer", "keyA", false)
	s1.SetBlocked("peer", true)

	s2, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	p := s2.Get("peer")
	if p == nil || !p.Blocked || p.Key != "keyA" {
		t.Fatalf("decisions did not survive a restart: %+v", p)
	}
	if d := s2.Check("peer", "keyA", false); d.Allowed {
		t.Fatal("a peer blocked before the restart got through after it")
	}
}

func TestFingerprintIsReadable(t *testing.T) {
	got := Fingerprint("nodekey:abcdef0123456789aaaaaaaaaaaaaaaa")
	if strings.Contains(got, "nodekey:") {
		t.Errorf("fingerprint keeps the type prefix: %q", got)
	}
	if len(got) > 24 {
		t.Errorf("fingerprint is too long to read aloud: %q", got)
	}
	if !strings.Contains(got, "-") {
		t.Errorf("fingerprint is not grouped, so it is hard to compare: %q", got)
	}
	// Same key in, same fingerprint out -- otherwise comparing is meaningless.
	if got != Fingerprint("nodekey:abcdef0123456789aaaaaaaaaaaaaaaa") {
		t.Error("fingerprint is not stable")
	}
	if got == Fingerprint("nodekey:ffffffff123456789aaaaaaaaaaaaaaa") {
		t.Error("different keys share a fingerprint")
	}
}
