// Package spool holds messages for peers that are not reachable yet.
//
// Without it, `mesh send` to an offline peer fails and the message is gone --
// which is worst at exactly the moment it is most likely, since the ordinary
// reason a peer is unreachable is that it restarted a second ago. The queue is
// the sender's: no coordinator sees it, nothing is stored in the middle, and a
// mesh with no server keeps having no server.
//
// It is deliberately not a mail system. Only `tell` is queued -- an `ask` has
// someone blocked on the answer, and a reply that arrives after the asker gave
// up is worse than a refusal.
package spool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xuy/agent-mesh/internal/wire"
)

// DefaultMax is how many messages may wait for one peer.
//
// Per peer rather than overall, so one agent that never comes back cannot
// starve delivery to everyone else -- the failure it is guarding against is a
// peer that is gone for good, and that peer should not be able to consume the
// budget of peers that are merely asleep.
const DefaultMax = 500

// Why a message was queued. Kept per entry rather than derived later, because
// by the time anyone looks the peer's state has usually changed -- and "waiting
// because it was offline" and "waiting because the tunnel failed while it
// looked online" call for different reactions from whoever is reading.
const (
	// ReasonOffline: the roster said the peer was not up.
	ReasonOffline = "peer offline"
	// ReasonUnreachable: the roster said it was up and the tunnel failed anyway.
	ReasonUnreachable = "unreachable"
)

// Entry is one message waiting to be delivered, with the time it was queued and
// why. The envelope keeps its original TS, so a receiver can tell that a
// message was delayed rather than merely sent late.
type Entry struct {
	Env    wire.Envelope `json:"env"`
	Queued time.Time     `json:"queued"`
	Reason string        `json:"reason,omitempty"`
}

// Spool is a set of per-peer queues on disk.
type Spool struct {
	dir string
	max int
	mu  sync.Mutex
}

// Open prepares the spool directory.
func Open(dir string, max int) (*Spool, error) {
	if max <= 0 {
		max = DefaultMax
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("preparing the spool: %w", err)
	}
	return &Spool{dir: dir, max: max}, nil
}

// safeName rejects a peer name that cannot be a filename. Mesh names are
// claimed through the coordinator and are already constrained, but this file
// path is derived from something another machine chose, so it is checked here
// rather than assumed.
func safeName(peer string) error {
	if peer == "" {
		return fmt.Errorf("empty peer name")
	}
	if strings.ContainsAny(peer, `/\:*?"<>|`) || peer == "." || peer == ".." {
		return fmt.Errorf("peer name %q cannot be used as a file name", peer)
	}
	return nil
}

func (s *Spool) path(peer string) string { return filepath.Join(s.dir, peer+".jsonl") }

// Add queues a message for a peer.
//
// A full queue is an error rather than a silent drop of the oldest entry. The
// cap exists for a peer that is never coming back, and in that case the honest
// thing is to tell the sender now -- quietly discarding what it asked to send
// would make the spool a place messages go to disappear.
func (s *Spool) Add(peer string, env wire.Envelope, reason string) error {
	if err := safeName(peer); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	have, err := s.readLocked(peer)
	if err != nil {
		return err
	}
	if len(have) >= s.max {
		return fmt.Errorf("%d messages are already waiting for %s; it has not come back since %s",
			len(have), peer, have[0].Queued.Format(time.RFC3339))
	}
	b, err := json.Marshal(Entry{Env: env, Queued: time.Now().UTC(), Reason: reason})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(peer), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("queueing for %s: %w", peer, err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("queueing for %s: %w", peer, err)
	}
	return f.Close()
}

func (s *Spool) readLocked(peer string) ([]Entry, error) {
	b, err := os.ReadFile(s.path(peer))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the queue for %s: %w", peer, err)
	}
	var out []Entry
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Entry
		// A line that will not parse is skipped rather than failing the whole
		// queue: one torn write at the end of a file killed by a crash must not
		// make every message behind it undeliverable.
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Pending returns what is waiting for a peer, oldest first.
func (s *Spool) Pending(peer string) ([]Entry, error) {
	if err := safeName(peer); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(peer)
}

// Peers names every peer with something waiting, in a stable order.
func (s *Spool) Peers() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		peer := strings.TrimSuffix(name, ".jsonl")
		q, err := s.readLocked(peer)
		if err != nil || len(q) == 0 {
			continue
		}
		out = append(out, peer)
	}
	sort.Strings(out)
	return out, nil
}

// Drop removes queued messages for a peer: one of them if id is given, all of
// them otherwise. It returns how many it removed.
//
// A queue with no way out is a trap. A test run, or a peer that is never coming
// back, leaves messages that will be delivered weeks later to someone who has
// no idea what they refer to -- and until this existed the only remedy was to
// find the file and delete it by hand.
func (s *Spool) Drop(peer, id string) (int, error) {
	if err := safeName(peer); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	q, err := s.readLocked(peer)
	if err != nil {
		return 0, err
	}
	if id == "" {
		if err := s.writeLocked(peer, nil); err != nil {
			return 0, err
		}
		return len(q), nil
	}
	kept := make([]Entry, 0, len(q))
	for _, e := range q {
		if e.Env.ID == id {
			continue
		}
		kept = append(kept, e)
	}
	n := len(q) - len(kept)
	if n == 0 {
		return 0, nil
	}
	if err := s.writeLocked(peer, kept); err != nil {
		return 0, err
	}
	return n, nil
}

// Flush delivers what is waiting for a peer, oldest first, and stops at the
// first failure so ordering is preserved.
//
// An entry is removed only after deliver returns nil, and the file is rewritten
// after each one. A crash between a successful delivery and that rewrite
// redelivers the message, which is why the envelope keeps its original ID: the
// receiver drops a duplicate it has already recorded. Losing a message is not
// recoverable; delivering one twice is, so the ordering of those two operations
// is chosen deliberately.
func (s *Spool) Flush(peer string, deliver func(wire.Envelope) error) (int, error) {
	if err := safeName(peer); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	q, err := s.readLocked(peer)
	if err != nil {
		return 0, err
	}
	sent := 0
	for i, e := range q {
		if err := deliver(e.Env); err != nil {
			if werr := s.writeLocked(peer, q[i:]); werr != nil {
				return sent, werr
			}
			return sent, err
		}
		sent++
		if werr := s.writeLocked(peer, q[i+1:]); werr != nil {
			return sent, werr
		}
	}
	return sent, nil
}

// writeLocked replaces a peer's queue, and removes the file once it is empty so
// Peers does not report a peer with nothing waiting.
func (s *Spool) writeLocked(peer string, q []Entry) error {
	if len(q) == 0 {
		if err := os.Remove(s.path(peer)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var b strings.Builder
	for _, e := range q {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := s.path(peer) + ".new"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(peer))
}
