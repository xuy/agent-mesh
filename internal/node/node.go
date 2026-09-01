// Package node implements the per-agent daemon: one long-lived process that
// owns an agent's tunnel, its roster, its inbox, and its local control socket.
//
// One process per agent, not one per message: bringing up a tailcat server
// costs a netcheck and a DERP handshake, so the daemon pays that once and the
// CLI reaches it over a unix socket in microseconds.
package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
	"github.com/xuy/agent-mesh/internal/adapter"
	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/hub"
	"github.com/xuy/agent-mesh/internal/ident"
	"github.com/xuy/agent-mesh/internal/wire"
)

// Node is a running agent on the mesh.
type Node struct {
	cfg  config.Node
	mesh config.Mesh
	id   *ident.Identity
	logf func(string, ...any)

	srv     *tailcat.Server
	ad      adapter.Adapter
	mailbox *adapter.Mailbox // set when the adapter is a mailbox

	mu      sync.Mutex
	roster  map[string]ident.Info  // by name, excluding self
	byAddr  map[netip.Addr]string  // caller tunnel address -> name
	clients map[string]*peerClient // dialed peers, kept warm
	hubUp   bool
	hubConn net.Conn // the live control-plane connection, closed on shutdown
	started time.Time

	inboxMu sync.Mutex
	closed  chan struct{}
}

type peerClient struct {
	blob string
	cl   *tailcat.Client
}

// New builds a node from its settings and identity.
func New(cfg config.Node, m config.Mesh, id *ident.Identity, logf func(string, ...any)) *Node {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	n := &Node{
		cfg: cfg, mesh: m, id: id, logf: logf,
		roster: map[string]ident.Info{}, byAddr: map[netip.Addr]string{},
		clients: map[string]*peerClient{}, closed: make(chan struct{}),
	}
	if cfg.Adapter == "exec" && cfg.Exec != "" {
		n.ad = &adapter.Exec{Cmd: cfg.Exec}
	} else {
		n.mailbox = adapter.NewMailbox()
		n.ad = n.mailbox
	}
	return n
}

// Adapter reports how this node answers questions.
func (n *Node) Adapter() adapter.Adapter { return n.ad }

// Mailbox returns the mailbox adapter, or nil if this node answers by exec.
func (n *Node) Mailbox() *adapter.Mailbox { return n.mailbox }

// Start brings up the node's tunnel and begins serving peers.
func (n *Node) Start() error {
	n.started = time.Now()
	n.srv = &tailcat.Server{
		Key:  n.id.Server,
		Logf: func(string, ...any) {},
		OnTCP: func(port uint16) func(net.Conn) {
			if port != wire.Port {
				return nil
			}
			return n.serveTunnel
		},
	}
	if err := n.srv.Start(); err != nil {
		return fmt.Errorf("starting tunnel: %w", err)
	}
	n.loadRoster()
	go n.registrar()
	return nil
}

// Close shuts the node down.
func (n *Node) Close() error {
	select {
	case <-n.closed:
	default:
		close(n.closed)
	}
	n.mu.Lock()
	for _, pc := range n.clients {
		pc.cl.Close()
	}
	n.clients = map[string]*peerClient{}
	if n.hubConn != nil {
		// Let the hub free our name immediately instead of waiting out its
		// liveness timeout.
		n.hubConn.Close()
	}
	n.mu.Unlock()
	if n.srv != nil {
		// The whole TCP stack lives in this process, so tearing it down with
		// segments still in flight loses them. This is the one place draining
		// belongs: per-answer it could never complete, because it waits for
		// every connection to close, including the one carrying the answer.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		n.srv.DrainTCP(ctx)
		cancel()
		return n.srv.Close()
	}
	return nil
}

// Info is this node's roster entry.
func (n *Node) Info() ident.Info {
	blob := ""
	if n.srv != nil {
		blob = string(n.srv.ConnBlob())
	}
	kinds := n.cfg.Kinds
	if len(kinds) == 0 {
		kinds = []string{"ask", "tell"}
	}
	return ident.Info{
		Name:      n.cfg.Name,
		Mesh:      n.mesh.Name,
		Blob:      blob,
		ServerPub: ident.PubText(n.id.Server.Public()),
		ClientPub: ident.PubText(n.id.Client.Public()),
		Agent:     n.cfg.Agent,
		Kinds:     append(append([]string{}, kinds...), n.ad.Kind()),
		Note:      n.cfg.Note,
		Seen:      time.Now().UTC(),
		Online:    true,
	}
}

// Peers returns the current roster, self excluded.
func (n *Node) Peers() []ident.Info {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]ident.Info, 0, len(n.roster))
	for _, i := range n.roster {
		out = append(out, i)
	}
	return out
}

// HubUp reports whether the control-plane connection is currently up. The mesh
// keeps working while it is down; only discovery stalls.
func (n *Node) HubUp() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hubUp
}

// ---------- serving ----------

// serveTunnel handles one inbound connection from a peer.
func (n *Node) serveTunnel(c net.Conn) {
	defer c.Close()
	wc := wire.NewConn(c)

	// Attribution: the connection's remote address is derived from the
	// caller's node key, so it names the caller. WireGuard already refused
	// anyone not on the allowlist, so this only picks which allowed peer it is.
	caller, ok := n.peerAt(remoteAddr(c))
	if !ok {
		wc.Send(wire.Envelope{Kind: wire.KindError, Body: "you are not in " + n.cfg.Name + "'s roster"})
		return
	}

	e, err := wc.Recv()
	if err != nil {
		return
	}
	if e.From != "" && e.From != caller {
		n.logf("rejecting message claiming to be from %q on %s's connection", e.From, caller)
		wc.Send(wire.Envelope{Corr: e.ID, Kind: wire.KindError, Body: "sender name does not match your key"})
		return
	}
	e.From = caller
	n.appendInbox(e)

	switch e.Kind {
	case wire.KindTell:
		wc.Send(wire.Envelope{Corr: e.ID, From: n.cfg.Name, To: caller, Kind: wire.KindAck})
	case wire.KindAsk:
		n.answer(wc, e)
	default:
		wc.Send(wire.Envelope{Corr: e.ID, Kind: wire.KindError, Body: "unsupported kind " + string(e.Kind)})
	}
}

func (n *Node) answer(wc *wire.Conn, e wire.Envelope) {
	deadline := e.Deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(10 * time.Minute)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	emit := func(chunk string) error {
		return wc.Send(wire.Envelope{Corr: e.ID, From: n.cfg.Name, To: e.From, Kind: wire.KindChunk, Body: chunk})
	}
	answer, err := n.ad.Handle(ctx, adapter.Request{ID: e.ID, From: e.From, Thread: e.Thread, Body: e.Body}, emit)
	out := wire.Envelope{Corr: e.ID, From: n.cfg.Name, To: e.From, Kind: wire.KindDone, Body: answer, TS: time.Now().UTC()}
	if err != nil {
		out.Kind = wire.KindError
		out.Body = err.Error()
	}
	wc.Send(out)
	n.appendInbox(out)
}

// ---------- dialing ----------

// client returns a warm tunnel to a peer, dialing one if needed. Only the
// first message to a peer pays the DERP handshake.
func (n *Node) client(name string) (*tailcat.Client, ident.Info, error) {
	n.mu.Lock()
	info, ok := n.roster[name]
	pc := n.clients[name]
	n.mu.Unlock()

	if !ok {
		return nil, info, fmt.Errorf("no peer named %q on mesh %s (run `mesh peers` to see who is here)", name, n.mesh.Name)
	}
	if pc != nil && pc.blob == info.Blob {
		return pc.cl, info, nil
	}
	if pc != nil {
		pc.cl.Close() // the peer restarted and has a new address
	}
	cl := &tailcat.Client{
		Server: tailcat.ConnBlob(info.Blob),
		Key:    n.id.Client,
		Logf:   func(string, ...any) {},
	}
	n.mu.Lock()
	n.clients[name] = &peerClient{blob: info.Blob, cl: cl}
	n.mu.Unlock()
	return cl, info, nil
}

// Ask sends a question and waits for the answer, streaming progress to onChunk.
func (n *Node) Ask(ctx context.Context, to, body, thread string, onChunk func(string)) (string, error) {
	cl, _, err := n.client(to)
	if err != nil {
		return "", err
	}
	c, err := cl.DialTCPPort(ctx, wire.Port)
	if err != nil {
		return "", fmt.Errorf("cannot reach %s (it may have restarted; check `mesh peers`): %w", to, err)
	}
	defer c.Close()
	wc := wire.NewConn(c)

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(10 * time.Minute)
	}
	id := wire.NewID()
	req := wire.Envelope{ID: id, From: n.cfg.Name, To: to, Kind: wire.KindAsk, Thread: thread, Body: body, Deadline: deadline, TS: time.Now().UTC()}
	if err := wc.Send(req); err != nil {
		return "", err
	}
	n.appendInbox(req)

	for {
		if d, ok := ctx.Deadline(); ok {
			wc.SetDeadline(d)
		}
		e, err := wc.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return "", fmt.Errorf("%s did not answer before the deadline", to)
			}
			return "", fmt.Errorf("connection to %s ended without an answer: %w", to, err)
		}
		switch e.Kind {
		case wire.KindChunk:
			if onChunk != nil {
				onChunk(e.Body)
			}
		case wire.KindDone:
			n.appendInbox(e)
			return e.Body, nil
		case wire.KindError:
			n.appendInbox(e)
			return "", fmt.Errorf("%s: %s", to, e.Body)
		}
	}
}

// Tell delivers a message without waiting for an answer.
func (n *Node) Tell(ctx context.Context, to, body, thread string) error {
	cl, _, err := n.client(to)
	if err != nil {
		return err
	}
	c, err := cl.DialTCPPort(ctx, wire.Port)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", to, err)
	}
	defer c.Close()
	wc := wire.NewConn(c)
	if d, ok := ctx.Deadline(); ok {
		wc.SetDeadline(d)
	}
	req := wire.Envelope{ID: wire.NewID(), From: n.cfg.Name, To: to, Kind: wire.KindTell, Thread: thread, Body: body, TS: time.Now().UTC()}
	if err := wc.Send(req); err != nil {
		return err
	}
	n.appendInbox(req)
	e, err := wc.Recv()
	if err != nil {
		return fmt.Errorf("%s did not acknowledge: %w", to, err)
	}
	if e.Kind == wire.KindError {
		return fmt.Errorf("%s: %s", to, e.Body)
	}
	return nil
}

// PingResult describes how a peer is currently reachable.
type PingResult struct {
	Peer    string        `json:"peer"`
	Latency time.Duration `json:"latency"`
	Path    string        `json:"path"` // "direct" or a DERP region
}

// Ping measures the path to a peer and reports whether it is direct or relayed.
func (n *Node) Ping(ctx context.Context, to string) (PingResult, error) {
	res := PingResult{Peer: to}
	cl, _, err := n.client(to)
	if err != nil {
		return res, err
	}
	start := time.Now()
	pr, err := cl.DiscoPing(ctx)
	if err != nil {
		return res, fmt.Errorf("cannot reach %s: %w", to, err)
	}
	res.Latency = time.Since(start)
	if pr.Endpoint != "" {
		res.Path = "direct " + pr.Endpoint
	} else if pr.DERPRegionCode != "" {
		res.Path = "relayed via DERP " + pr.DERPRegionCode
	} else {
		res.Path = "relayed"
	}
	return res, nil
}

// ---------- roster ----------

func (n *Node) peerAt(a netip.Addr) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	name, ok := n.byAddr[a]
	return name, ok
}

// applyRoster replaces what this node knows about the mesh and widens the
// tunnel allowlist to match.
//
// Allowlisting is add-only for the life of the process: tailcat can grant a
// peer key but not revoke one. A peer removed from the mesh therefore stays
// dialable until the daemon restarts, which is the honest limit of this
// prototype rather than a property to rely on.
func (n *Node) applyRoster(rs []ident.Info) {
	n.mu.Lock()
	n.roster = map[string]ident.Info{}
	n.byAddr = map[netip.Addr]string{}
	for _, i := range rs {
		if i.Name == n.cfg.Name {
			continue
		}
		n.roster[i.Name] = i
		if a, err := i.ClientAddr(); err == nil {
			n.byAddr[a] = i.Name
		}
	}
	names := make([]ident.Info, 0, len(n.roster))
	for _, i := range n.roster {
		names = append(names, i)
	}
	n.mu.Unlock()

	for _, i := range names {
		if k, err := ident.ParsePub(i.ClientPub); err == nil {
			n.srv.AddAllowedClient(k)
		}
	}
	n.saveRoster(names)
}

func (n *Node) saveRoster(rs []ident.Info) {
	if err := config.WriteJSON(config.RosterPath(n.cfg.Name), rs); err != nil {
		n.logf("caching roster: %v", err)
	}
}

// loadRoster restores the last known mesh from disk, so a node that starts
// while the hub is down can still reach the peers it already knew.
func (n *Node) loadRoster() {
	b, err := os.ReadFile(config.RosterPath(n.cfg.Name))
	if err != nil {
		return
	}
	var rs []ident.Info
	if json.Unmarshal(b, &rs) != nil {
		return
	}
	for i := range rs {
		rs[i].Online = false
	}
	n.applyRoster(rs)
	n.logf("restored %d peers from cache", len(rs))
}

// registrar keeps one long-lived connection to the hub. Registration, liveness
// and roster delivery all ride on it, so presence is just whether it is open.
func (n *Node) registrar() {
	backoff := time.Second
	for {
		select {
		case <-n.closed:
			return
		default:
		}
		if err := n.registerOnce(); err != nil {
			n.logf("hub: %v (retrying in %s)", err, backoff.Round(time.Second))
		}
		n.mu.Lock()
		n.hubUp = false
		n.mu.Unlock()

		select {
		case <-n.closed:
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (n *Node) registerOnce() error {
	if n.mesh.Hub == "" {
		return fmt.Errorf("this node has no hub configured")
	}
	cl := &tailcat.Client{
		Server: tailcat.ConnBlob(n.mesh.Hub),
		Key:    n.id.Client,
		Logf:   func(string, ...any) {},
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	c, err := cl.DialTCPPort(ctx, wire.HubPort)
	cancel()
	if err != nil {
		return fmt.Errorf("cannot reach the hub: %w", err)
	}
	defer c.Close()
	n.mu.Lock()
	n.hubConn = c
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		if n.hubConn == c {
			n.hubConn = nil
		}
		n.mu.Unlock()
	}()

	enc := json.NewEncoder(c)
	dec := json.NewDecoder(c)
	if err := enc.Encode(hub.Req{V: wire.Version, Op: "register", Join: n.mesh.Join, Node: n.Info()}); err != nil {
		return err
	}

	// Keepalive doubles as the hub's liveness signal for this node.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-n.closed:
				return
			case <-t.C:
				enc.Encode(hub.Req{V: wire.Version, Op: "ping", Join: n.mesh.Join})
			}
		}
	}()

	for {
		var resp hub.Resp
		if err := dec.Decode(&resp); err != nil {
			return fmt.Errorf("lost the hub connection: %w", err)
		}
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		if resp.Roster != nil {
			n.applyRoster(resp.Roster)
			n.mu.Lock()
			was := n.hubUp
			n.hubUp = true
			n.mu.Unlock()
			if !was {
				n.logf("registered with mesh %q; %d peer(s) online", resp.Mesh, len(resp.Roster)-1)
			}
		}
	}
}

func remoteAddr(c net.Conn) netip.Addr {
	ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr()
}
