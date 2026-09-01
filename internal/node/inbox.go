package node

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

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
