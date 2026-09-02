package node

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/xuy/agent-mesh/internal/adapter"
	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/wire"
)

// appendInbox records an envelope in the node's append-only log. Everything the
// node sends and receives lands here, so an agent that was not watching can
// always catch up on what it missed.
func (n *Node) appendInbox(e wire.Envelope) {
	n.inboxMu.Lock()
	defer n.inboxMu.Unlock()
	path := config.InboxPath(n.cfg.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		n.logf("inbox: %v", err)
		return
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
}

// Inbox returns the last n envelopes, oldest first. A limit of 0 means all.
func (n *Node) Inbox(limit int, incoming bool) []wire.Envelope {
	n.inboxMu.Lock()
	defer n.inboxMu.Unlock()
	f, err := os.Open(config.InboxPath(n.cfg.Name))
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []wire.Envelope
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e wire.Envelope
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if incoming && e.From == n.cfg.Name {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// since returns inbound messages newer than the given id. Message ids lead with
// a millisecond timestamp, so they sort chronologically and a plain comparison
// is enough.
func (n *Node) since(id string) []wire.Envelope {
	var out []wire.Envelope
	for _, e := range n.Inbox(0, true) {
		if e.ID > id {
			out = append(out, e)
		}
	}
	return out
}

// recent turns parked questions back into envelopes, so `mesh wait` reports
// them the same way it reports a message that just arrived.
func (n *Node) recent(qs []adapter.Question) []wire.Envelope {
	out := make([]wire.Envelope, 0, len(qs))
	for _, q := range qs {
		out = append(out, wire.Envelope{
			ID: q.ID, From: q.From, To: n.cfg.Name,
			Kind: wire.KindAsk, Thread: q.Thread, Body: q.Body,
		})
	}
	return out
}

// auditEntry is one line of the record of what peers asked of this node.
//
// It is separate from the inbox because it answers a different question. The
// inbox is what was said; the audit log is what was allowed, and it keeps
// refused attempts, which never reach the inbox at all.
type auditEntry struct {
	TS      time.Time `json:"ts"`
	From    string    `json:"from"`
	Kind    string    `json:"kind"`
	ID      string    `json:"id"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason,omitempty"`
	Body    string    `json:"body,omitempty"`
}

func (n *Node) audit(e wire.Envelope, outcome, reason string) {
	body := e.Body
	if len(body) > 400 {
		body = body[:400] + "..."
	}
	rec := auditEntry{
		TS: time.Now().UTC(), From: e.From, Kind: string(e.Kind),
		ID: e.ID, Outcome: outcome, Reason: reason, Body: body,
	}
	n.inboxMu.Lock()
	defer n.inboxMu.Unlock()
	path := config.AuditPath(n.cfg.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if b, err := json.Marshal(rec); err == nil {
		f.Write(append(b, '\n'))
	}
}

// Audit returns the last n entries of the record, oldest first.
func (n *Node) Audit(limit int) []auditEntry {
	n.inboxMu.Lock()
	defer n.inboxMu.Unlock()
	f, err := os.Open(config.AuditPath(n.cfg.Name))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []auditEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e auditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
