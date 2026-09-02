// Package policy decides which peers this node will deal with, and what they
// are allowed to make it do.
//
// A mesh of agents is a prompt-injection surface by construction: the whole
// point is that text written by another machine reaches a model that can run
// commands. Two defences live here. The first is identity -- a peer's key is
// pinned the first time it is seen, so a name cannot be quietly taken over by
// a different key later. The second is authority -- being on the mesh lets you
// say something, but not necessarily make this node *do* something.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Peer is what this node has decided about another one.
type Peer struct {
	Name string `json:"name"`

	// Key is the peer's server public key as first seen. A different key
	// arriving under the same name is refused: it is either an impersonation
	// attempt or a peer that was rebuilt, and the two are indistinguishable
	// from here, so a person has to say which.
	Key string `json:"key"`

	// Verified records that a human compared fingerprints out of band. Nothing
	// enforces it; it is there so `mesh peers` can show which peers are
	// trusted because someone checked, and which are trusted because they
	// turned up first.
	Verified bool `json:"verified,omitempty"`

	Blocked bool `json:"blocked,omitempty"`

	// MayAsk allows the peer to make this node work: run its adapter, spend
	// its tokens, execute its commands. Telling is always allowed -- a message
	// that lands in an inbox costs nothing and commits no one.
	MayAsk bool `json:"may_ask"`

	FirstSeen time.Time `json:"first_seen,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
}

// Decision is the outcome of checking an inbound message.
type Decision struct {
	Allowed bool
	// Reason is shown to the peer when it is refused, so it can act rather
	// than guess. It names the command that would grant what was refused.
	Reason string
}

// Store holds this node's decisions about its peers, on disk.
type Store struct {
	path string

	mu    sync.Mutex
	peers map[string]*Peer

	rate *rateKeeper

	// DefaultMayAsk is whether an unknown peer may make this node work.
	//
	// It is set from the node's delivery mode rather than fixed, because the
	// blast radius differs by an order of magnitude. A mailbox node's "work"
	// is showing a human a question, so being open is right. An exec or
	// webhook node's "work" is running a command or waking a live agent, so a
	// peer that has never been vouched for should have to be let in first.
	DefaultMayAsk bool
}

// Load reads the store at path. perMinute caps how fast one peer may send;
// zero means the default and a negative number means no limit.
func Load(path string, defaultMayAsk bool, perMinute int) (*Store, error) {
	s := &Store{
		path: path, peers: map[string]*Peer{},
		DefaultMayAsk: defaultMayAsk, rate: newRateKeeper(perMinute),
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.peers); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, p := range s.peers {
		p.Name = name
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.peers, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Check decides whether to accept a message from a peer.
//
// It also performs trust-on-first-use: a peer seen for the first time is
// recorded with the key it presented. That is not a substitute for
// verification, but it does mean a takeover has to happen before the peer is
// ever seen rather than at any later moment.
func (s *Store) Check(name, key string, isAsk bool) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, known := s.peers[name]
	if !known {
		p = &Peer{
			Name: name, Key: key, MayAsk: s.DefaultMayAsk,
			FirstSeen: time.Now().UTC(),
		}
		s.peers[name] = p
	}
	p.LastSeen = time.Now().UTC()
	defer s.saveLocked()

	if p.Blocked {
		return Decision{Reason: "you are blocked by this node"}
	}
	if key != "" && p.Key != "" && key != p.Key {
		// The name is the same and the key is not. Refuse and keep the old
		// key: accepting silently is how a name gets stolen.
		return Decision{Reason: fmt.Sprintf(
			"your key does not match the one this node pinned for %q. If that node was rebuilt, "+
				"its operator must run `mesh forget %s` to accept the new key", name, name)}
	}
	// Rate is checked after identity and blocking, so a blocked peer is told
	// it is blocked rather than told to slow down, and before authority, so a
	// peer hammering a node it is not even allowed to ask cannot make the node
	// write an audit line per attempt.
	if ok, wait := s.rate.allow(name, time.Now()); !ok {
		return Decision{Reason: rateReason(wait)}
	}
	if isAsk && !p.MayAsk {
		return Decision{Reason: fmt.Sprintf(
			"you may send messages to this node but not ask it to do work. "+
				"Its operator can allow that with `mesh allow %s`", name)}
	}
	return Decision{Allowed: true}
}

// Get returns what is known about a peer, or nil.
func (s *Store) Get(name string) *Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.peers[name]; ok {
		c := *p
		return &c
	}
	return nil
}

// All returns every known peer, by name.
func (s *Store) All() []*Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		c := *p
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) update(name string, fn func(*Peer)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[name]
	if !ok {
		p = &Peer{Name: name, MayAsk: s.DefaultMayAsk, FirstSeen: time.Now().UTC()}
		s.peers[name] = p
	}
	fn(p)
	return s.saveLocked()
}

// SetBlocked blocks or unblocks a peer. Blocking takes effect on the next
// message, with no restart: the check happens per message, not per connection.
func (s *Store) SetBlocked(name string, blocked bool) error {
	return s.update(name, func(p *Peer) { p.Blocked = blocked })
}

// SetMayAsk grants or withdraws a peer's authority to make this node work.
func (s *Store) SetMayAsk(name string, may bool) error {
	return s.update(name, func(p *Peer) { p.MayAsk = may })
}

// SetVerified records that a person compared fingerprints out of band.
func (s *Store) SetVerified(name, key string) error {
	return s.update(name, func(p *Peer) {
		if key != "" {
			p.Key = key
		}
		p.Verified = true
	})
}

// Forget drops what is known about a peer, so its next message pins whatever
// key it presents. It is the deliberate way to accept a peer that was rebuilt.
func (s *Store) Forget(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, name)
	return s.saveLocked()
}

// Fingerprint renders a key as something a person can compare across a desk:
// the leading bytes, grouped. Long enough to catch a substitution when read
// aloud, short enough that someone will actually read it.
func Fingerprint(key string) string {
	k := strings.TrimPrefix(key, "nodekey:")
	if len(k) > 16 {
		k = k[:16]
	}
	var parts []string
	for i := 0; i < len(k); i += 4 {
		end := i + 4
		if end > len(k) {
			end = len(k)
		}
		parts = append(parts, k[i:end])
	}
	return strings.Join(parts, "-")
}
