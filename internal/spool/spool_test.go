package spool

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuy/agent-mesh/internal/wire"
)

func open(t *testing.T, max int) *Spool {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "spool"), max)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func env(id string) wire.Envelope {
	return wire.Envelope{V: wire.Version, ID: id, Kind: wire.KindTell, Body: id}
}

func TestFlushDeliversInOrderAndEmpties(t *testing.T) {
	s := open(t, 0)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Add("master", env(id), ReasonOffline); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	n, err := s.Flush("master", func(e wire.Envelope) error {
		got = append(got, e.ID)
		return nil
	})
	if err != nil || n != 3 {
		t.Fatalf("flush: n=%d err=%v", n, err)
	}
	if fmt.Sprint(got) != "[a b c]" {
		t.Fatalf("out of order: %v", got)
	}
	q, _ := s.Pending("master")
	if len(q) != 0 {
		t.Fatalf("queue should be empty, has %d", len(q))
	}
	peers, _ := s.Peers()
	if len(peers) != 0 {
		t.Fatalf("a drained peer should not be listed: %v", peers)
	}
}

func TestFlushStopsAtTheFirstFailureAndKeepsTheRest(t *testing.T) {
	// Ordering is the whole reason a queue exists. If a flush skipped a failed
	// message and carried on, the peer would receive the rest out of order and
	// the failed one later still -- worse than not queueing at all.
	s := open(t, 0)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Add("master", env(id), ReasonOffline); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Flush("master", func(e wire.Envelope) error {
		if e.ID == "b" {
			return fmt.Errorf("peer went away mid-flush")
		}
		return nil
	})
	if n != 1 || err == nil {
		t.Fatalf("expected one delivered then a failure, got n=%d err=%v", n, err)
	}
	q, _ := s.Pending("master")
	if len(q) != 2 || q[0].Env.ID != "b" || q[1].Env.ID != "c" {
		t.Fatalf("the undelivered messages should remain, in order: %+v", q)
	}
}

func TestQueueSurvivesReopening(t *testing.T) {
	// The common reason a message is undelivered is that something restarted,
	// so a queue that only lived in memory would be empty exactly when needed.
	dir := filepath.Join(t.TempDir(), "spool")
	s, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("master", env("a"), ReasonOffline); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	q, err := again.Pending("master")
	if err != nil || len(q) != 1 || q[0].Env.ID != "a" {
		t.Fatalf("queue did not survive a reopen: %+v %v", q, err)
	}
}

func TestFullQueueRefusesRatherThanDropping(t *testing.T) {
	// A silent drop would make the spool a place messages go to disappear.
	s := open(t, 2)
	if err := s.Add("gone", env("a"), ReasonOffline); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("gone", env("b"), ReasonOffline); err != nil {
		t.Fatal(err)
	}
	err := s.Add("gone", env("c"), ReasonOffline)
	if err == nil {
		t.Fatal("expected the third message to be refused")
	}
	q, _ := s.Pending("gone")
	if len(q) != 2 || q[0].Env.ID != "a" {
		t.Fatalf("the cap must not evict what is already queued: %+v", q)
	}
}

func TestCapIsPerPeer(t *testing.T) {
	// One peer that is never coming back must not consume the budget of peers
	// that are merely asleep.
	s := open(t, 1)
	if err := s.Add("gone", env("a"), ReasonOffline); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("gone", env("b"), ReasonOffline); err == nil {
		t.Fatal("expected gone's queue to be full")
	}
	if err := s.Add("asleep", env("c"), ReasonOffline); err != nil {
		t.Fatalf("a different peer must still be queueable: %v", err)
	}
}

func TestATornLineDoesNotBlockTheRest(t *testing.T) {
	// A crash can leave a half-written last line. Everything behind it must
	// still be deliverable.
	s := open(t, 0)
	if err := s.Add("master", env("a"), ReasonOffline); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s.path("master"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"env":{"id":"trunc`)
	f.Close()

	q, err := s.Pending("master")
	if err != nil {
		t.Fatalf("a torn line should not fail the read: %v", err)
	}
	if len(q) != 1 || q[0].Env.ID != "a" {
		t.Fatalf("the intact message should survive: %+v", q)
	}
}

func TestPeerNameCannotEscapeTheSpoolDirectory(t *testing.T) {
	// The name comes from another machine's roster entry, so it is checked
	// rather than trusted.
	s := open(t, 0)
	for _, bad := range []string{"..", "../evil", `a\b`, "a/b", ""} {
		if err := s.Add(bad, env("x"), ReasonOffline); err == nil {
			t.Fatalf("peer name %q should have been refused", bad)
		}
	}
}

func TestDrop(t *testing.T) {
	s, err := Open(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Add("gone", env(id), ReasonOffline); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.Drop("gone", "b")
	if err != nil || n != 1 {
		t.Fatalf("drop one: %d, %v", n, err)
	}
	q, err := s.Pending("gone")
	if err != nil || len(q) != 2 || q[0].Env.ID != "a" || q[1].Env.ID != "c" {
		t.Fatalf("after dropping b: %+v, %v", q, err)
	}

	// An id that is not there is not an error; the caller's intent is already
	// satisfied, and reporting 0 says so.
	if n, err := s.Drop("gone", "zzz"); err != nil || n != 0 {
		t.Fatalf("drop missing: %d, %v", n, err)
	}

	n, err = s.Drop("gone", "")
	if err != nil || n != 2 {
		t.Fatalf("drop all: %d, %v", n, err)
	}
	peers, err := s.Peers()
	if err != nil || len(peers) != 0 {
		t.Fatalf("peers after dropping everything: %v, %v", peers, err)
	}

	// Dropping a queue that never existed is also not an error.
	if n, err := s.Drop("never", ""); err != nil || n != 0 {
		t.Fatalf("drop unknown peer: %d, %v", n, err)
	}
}
