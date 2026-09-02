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
	"errors"
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
	"github.com/xuy/agent-mesh/internal/policy"
	"github.com/xuy/agent-mesh/internal/spool"
	"github.com/xuy/agent-mesh/internal/wire"
	"tailscale.com/tailcfg"
)

// firstMessageTimeout is how long an accepted connection may stay silent
// before it is dropped.
const firstMessageTimeout = 30 * time.Second

const (
	// hubKeepalive is how often a node pings the coordinator. Each ping draws
	// a reply, so it doubles as the proof that the connection is still alive.
	hubKeepalive = 15 * time.Second

	// hubSilenceTimeout is how long a node waits to hear anything at all
	// before assuming the connection is dead and rebuilding it. It sets the
	// worst-case time to recover from a coordinator that vanished without
	// closing, so it is kept to a small multiple of the keepalive rather than
	// a comfortable margin: two missed replies is already conclusive.
	hubSilenceTimeout = 45 * time.Second
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

	// coord is set when this node also runs the mesh's control plane inside
	// its own daemon, which is the default for the first node in a mesh.
	coord        *hub.Hub
	releaseLocal func()

	// policy decides which peers this node deals with and what they may make
	// it do. Consulted per message, so blocking takes effect immediately
	// rather than at the next restart.
	policy *policy.Store

	mu      sync.Mutex
	roster  map[string]ident.Info  // by name, excluding self
	byAddr  map[netip.Addr]string  // caller tunnel address -> name
	clients map[string]*peerClient // dialed peers, kept warm
	hubUp   bool
	hubConn net.Conn // the live control-plane connection, closed on shutdown
	started time.Time

	// spool holds tells for peers that are not reachable, so a message survives
	// the restart that is the usual reason a peer is missing.
	spool    *spool.Spool
	flushMu  sync.Mutex
	flushing map[string]bool

	inboxMu sync.Mutex
	closed  chan struct{}

	// waiters are blocked `mesh wait` calls. An agent that is not polling has
	// no other way to learn a message arrived, and polling is exactly what
	// this whole project exists to stop people doing.
	waitersMu sync.Mutex
	waiters   map[chan wire.Envelope]struct{}
}

// Subscribe returns a channel that receives inbound messages until release is
// called. The channel is buffered and dropped on overflow: a slow watcher must
// not be able to block the node from answering its peers.
func (n *Node) Subscribe() (<-chan wire.Envelope, func()) {
	ch := make(chan wire.Envelope, 16)
	n.waitersMu.Lock()
	if n.waiters == nil {
		n.waiters = map[chan wire.Envelope]struct{}{}
	}
	n.waiters[ch] = struct{}{}
	n.waitersMu.Unlock()
	return ch, func() {
		n.waitersMu.Lock()
		delete(n.waiters, ch)
		n.waitersMu.Unlock()
	}
}

func (n *Node) notifyWaiters(e wire.Envelope) {
	n.waitersMu.Lock()
	defer n.waitersMu.Unlock()
	for ch := range n.waiters {
		select {
		case ch <- e:
		default:
		}
	}
}

type peerClient struct {
	blob    string
	started time.Time
	cl      *tailcat.Client
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
	switch {
	case cfg.Adapter == "exec" && cfg.Exec != "":
		n.ad = &adapter.Exec{Cmd: cfg.Exec}
	case cfg.Adapter == "webhook" && cfg.WebhookURL != "":
		n.ad = &adapter.Webhook{
			URL: cfg.WebhookURL, Header: cfg.WebhookHeader,
			Async: cfg.WebhookAsync, Box: adapter.NewMailbox(),
		}
	case cfg.Adapter == "notify":
		n.ad = &adapter.Notify{Cmd: cfg.Exec, Box: adapter.NewMailbox()}
	default:
		n.mailbox = adapter.NewMailbox()
		n.ad = n.mailbox
	}
	// Several modes deliver the question somewhere and still expect a human or
	// an agent to answer with `mesh reply`, so find the parking area wherever
	// it lives rather than special-casing each one.
	if p, ok := n.ad.(interface{ Mailbox() *adapter.Mailbox }); ok && n.mailbox == nil {
		n.mailbox = p.Mailbox()
	}

	// A node whose "work" is running a command or waking a live agent starts
	// closed: a peer that has never been vouched for should have to be let in
	// before it can execute anything. A node whose work is showing a human a
	// question starts open, because the human is the check.
	openByDefault := n.ad.Kind() == "mailbox" || n.ad.Kind() == "notify"
	pol, err := policy.Load(config.PeersPath(cfg.Name), openByDefault, cfg.RatePerMinute)
	if err != nil {
		logf("reading peer policy: %v (starting with none)", err)
		pol, _ = policy.Load("", openByDefault, cfg.RatePerMinute)
	}
	n.policy = pol

	// A node with nowhere to spool still works; it just fails a tell to an
	// absent peer the way it always did, which is a worse outcome than
	// queueing but a better one than refusing to start.
	if cfg.Name != "" {
		sp, err := spool.Open(config.SpoolDir(cfg.Name), 0)
		if err != nil {
			logf("opening the outbox: %v (messages to absent peers will fail rather than queue)", err)
		} else {
			n.spool = sp
		}
	}
	return n
}

// Outbox reports what is waiting for each peer, so `mesh outbox` can show it.
func (n *Node) Outbox() (map[string][]spool.Entry, error) {
	if n.spool == nil {
		return nil, nil
	}
	peers, err := n.spool.Peers()
	if err != nil {
		return nil, err
	}
	out := map[string][]spool.Entry{}
	for _, p := range peers {
		q, err := n.spool.Pending(p)
		if err != nil {
			return nil, err
		}
		if len(q) > 0 {
			out[p] = q
		}
	}
	return out, nil
}

// DropQueued removes queued messages for a peer: one of them if id is given,
// all of them otherwise.
func (n *Node) DropQueued(peer, id string) (int, error) {
	if n.spool == nil {
		return 0, nil
	}
	return n.spool.Drop(peer, id)
}

// retryQueued drains the outbox for peers that are up, on a slow timer.
//
// The roster is the fast path and covers a peer coming back. This covers the
// case it cannot: a message queued because the tunnel failed while the peer was
// already online has no transition coming, so without a timer it would wait for
// a roster change that may never happen. A minute is chosen to be slow enough
// to be free and fast enough that nobody watches an outbox wondering.
func (n *Node) retryQueued() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-n.closed:
			return
		case <-t.C:
			if n.spool == nil {
				continue
			}
			peers, err := n.spool.Peers()
			if err != nil {
				continue
			}
			for _, p := range peers {
				if i, ok := n.peer(p); ok && i.Online {
					n.flushSpool(p)
				}
			}
		}
	}
}

// flushSpool delivers what is queued for a peer that has just come back.
//
// It runs off the roster update rather than on a timer: the roster is the only
// thing that actually knows a peer returned, and a timer would either be slow
// or spend the mesh's time asking about peers that are still gone.
func (n *Node) flushSpool(peer string) {
	if n.spool == nil {
		return
	}
	// One flush per peer at a time. Roster updates and the retry loop both
	// trigger this, and two of them interleaving would deliver out of order.
	n.flushMu.Lock()
	if n.flushing == nil {
		n.flushing = map[string]bool{}
	}
	if n.flushing[peer] {
		n.flushMu.Unlock()
		return
	}
	n.flushing[peer] = true
	n.flushMu.Unlock()
	defer func() {
		n.flushMu.Lock()
		delete(n.flushing, peer)
		n.flushMu.Unlock()
	}()

	q, err := n.spool.Pending(peer)
	if err != nil || len(q) == 0 {
		return
	}
	// Do not dial a peer the roster says is down. Startup restores the cached
	// roster before the coordinator has said who is actually up, so without
	// this every offline peer with a queue costs a full dial timeout for
	// nothing -- measured at two minutes each.
	if i, ok := n.peer(peer); !ok || !i.Online {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sent, err := n.spool.Flush(peer, func(env wire.Envelope) error {
		// The envelope is delivered as it was written -- same ID, so a
		// redelivery after a crash is a duplicate the receiver can drop, and
		// same TS, so the receiver can see it was delayed rather than late.
		return n.deliverTell(ctx, peer, env, nil)
	})
	if sent > 0 {
		n.logf("delivered %d queued message(s) to %s", sent, peer)
	}
	if err != nil {
		n.logf("%d message(s) still waiting for %s: %v", len(q)-sent, peer, err)
	}
}

// Adapter reports how this node answers questions.
func (n *Node) Adapter() adapter.Adapter { return n.ad }

// Mailbox returns the mailbox adapter, or nil if this node answers by exec.
func (n *Node) Mailbox() *adapter.Mailbox { return n.mailbox }

// Start brings up the node's tunnel and begins serving peers.
func (n *Node) Start() error {
	n.started = time.Now()
	if n.mesh.Coordinator == n.cfg.Name && n.cfg.Name != "" {
		n.coord = hub.New(n.mesh, n.logf)
		n.coord.LoadClaims()
	}
	n.pinRegion()
	n.srv = &tailcat.Server{
		Key:      n.id.Server,
		RegionID: tailcfg.DERPRegionID(n.cfg.Region),
		Logf:     func(string, ...any) {},
		OnTCP: func(port uint16) func(net.Conn) {
			switch port {
			case wire.Port:
				return n.serveTunnel
			case wire.HubPort:
				if n.coord != nil {
					return n.coord.Handle
				}
			}
			return nil
		},
	}
	if err := n.srv.Start(); err != nil {
		return fmt.Errorf("starting tunnel: %w", err)
	}
	n.loadRoster()

	if n.coord != nil {
		// The coordinator's own address is the mesh's bootstrap address, so
		// publish it before anyone tries to join.
		blob := config.PublicAddr(string(n.srv.ConnBlob()), n.cfg.Region)
		if n.mesh.Hub != blob {
			n.mesh.Hub = blob
			if err := n.mesh.Save(); err != nil {
				return fmt.Errorf("publishing the mesh address: %w", err)
			}
		}
		release, err := n.coord.RegisterLocal(n.Info(), n.applyRoster)
		if err != nil {
			return fmt.Errorf("registering with our own control plane: %w", err)
		}
		n.releaseLocal = release
		n.mu.Lock()
		n.hubUp = true
		n.mu.Unlock()
		n.logf("%s is the coordinator for mesh %q", n.cfg.Name, n.mesh.Name)
		return nil
	}
	go n.registrar()
	go n.watchRelay()
	go n.retryQueued()
	return nil
}

// RelayHome reports which relay this node is reachable through, or "" if it
// currently has none -- meaning nothing on the mesh can open a connection to
// it, however healthy it looks from the machine it runs on.
func (n *Node) RelayHome() string {
	if n.srv == nil {
		return ""
	}
	st := n.srv.Status()
	if st == nil || st.Self == nil {
		return ""
	}
	return st.Self.Relay
}

// watchRelay notices when this node has fallen off the mesh.
//
// A daemon can be perfectly healthy from the outside -- answering its control
// socket, listing peers, reporting itself registered -- while its tunnel has no
// relay home, so no peer can reach it at all. That happened, and it was
// invisible from this side for an hour: `mesh ping` is answered by tailcat's
// own stack rather than by this program, so it kept succeeding, and doctor
// believed it.
//
// There is no supported way to re-home the server in place, so the honest
// recovery is to exit and let the service manager start a fresh one. Exiting
// deliberately is better than staying up pretending: a node that cannot be
// reached is not a node, and a restart is seconds.
func (n *Node) watchRelay() {
	const (
		checkEvery = 30 * time.Second
		giveUp     = 2 * time.Minute
	)
	lost := time.Time{}
	for {
		select {
		case <-n.closed:
			return
		case <-time.After(checkEvery):
		}
		if n.RelayHome() != "" {
			if !lost.IsZero() {
				n.logf("relay home is back")
				lost = time.Time{}
			}
			continue
		}
		if lost.IsZero() {
			lost = time.Now()
			n.logf("this node has no relay home: no peer can reach it right now")
			continue
		}
		if time.Since(lost) >= giveUp {
			n.logf("still no relay home after %s -- exiting so the service manager can start a fresh tunnel", giveUp)
			n.Close()
			os.Exit(1)
		}
	}
}

// waitUntilServing blocks until the tunnel has a relay home, meaning inbound
// connections can actually arrive. It reports the reason if it gives up; a node
// that is slow to home is still worth starting, it just cannot promise it is
// reachable.
func (n *Node) waitUntilServing(limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		if st := n.srv.Status(); st != nil && st.Self != nil && st.Self.Relay != "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no relay home after %s", limit)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// pinRegion chooses this node's relay once and remembers it, so the node's
// address is the same every time it starts.
//
// The measurement has to happen here rather than being read back out of the
// address afterwards: tailcat renumbers the region when it embeds it, so an
// address cannot tell you which public relay produced it.
func (n *Node) pinRegion() {
	if n.cfg.Region != 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	region, err := config.PickRegion(ctx)
	if err != nil || region == 0 {
		n.logf("could not choose a relay to pin (%v); this node's address may change when it restarts", err)
		return
	}
	n.cfg.Region = region
	if err := n.cfg.Save(); err != nil {
		n.logf("pinning relay region: %v", err)
	}
}

// Close shuts the node down.
func (n *Node) Close() error {
	select {
	case <-n.closed:
	default:
		close(n.closed)
	}
	if n.releaseLocal != nil {
		n.releaseLocal()
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
		Started:   n.started.UTC(),
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

	// A caller that connects and then says nothing must not hold a goroutine
	// open indefinitely. This matters most on a coordinator, which accepts
	// tunnels from nodes it has never met -- that is what joining is -- so
	// anyone holding its address could otherwise pin resources without ever
	// sending a byte. The deadline is extended once a real message arrives and
	// the work begins.
	wc.SetDeadline(time.Now().Add(firstMessageTimeout))

	// Attribution: the connection's remote address is derived from the
	// caller's node key, so it names the caller. WireGuard already refused
	// anyone not on the allowlist, so this only picks which allowed peer it is.
	caller, ok := n.peerAt(remoteAddr(c))
	if !ok {
		wc.Send(wire.Envelope{Kind: wire.KindError, Body: n.cfg.Name + " does not know you. If you are on this mesh, its coordinator may have just restarted; try again in a minute."})
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

	// Authority is decided before anything is spent on the message, and per
	// message rather than per connection: a peer blocked a moment ago must not
	// get one more request in on a tunnel it already had -- and must not be
	// able to make this node write a hundred megabytes to disk on the way to
	// being refused.
	d := n.policy.Check(caller, n.peerKey(caller), e.Kind == wire.KindAsk)
	if !d.Allowed {
		n.audit(e, "refused", d.Reason)
		n.logf("refused %s from %s: %s", e.Kind, caller, d.Reason)
		wc.Send(wire.Envelope{Corr: e.ID, From: n.cfg.Name, To: caller, Kind: wire.KindError, Body: d.Reason})
		return
	}

	// An allowed message may take as long as the sender asked for, so the
	// miserly first-message deadline gives way now.
	wc.SetDeadline(time.Time{})

	if len(e.Files) > 0 {
		got, err := n.receiveFiles(wc, e.ID, e.Files)
		if err != nil {
			n.logf("attachment from %s failed: %v", caller, err)
			wc.Send(wire.Envelope{Corr: e.ID, From: n.cfg.Name, To: caller, Kind: wire.KindError, Body: err.Error()})
			return
		}
		e.Files = got
	}
	n.audit(e, "accepted", "")

	n.appendInbox(e)
	n.notifyWaiters(e)

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
	if pc != nil && pc.blob == info.Blob && pc.started.Equal(info.Started) {
		return pc.cl, info, nil
	}
	if pc != nil {
		// Either the peer moved, or it restarted behind the same address.
		pc.cl.Close()
	}
	cl := &tailcat.Client{
		Server: tailcat.ConnBlob(info.Blob),
		Key:    n.id.Client,
		Logf:   func(string, ...any) {},
	}
	n.mu.Lock()
	n.clients[name] = &peerClient{blob: info.Blob, started: info.Started, cl: cl}
	n.mu.Unlock()
	return cl, info, nil
}

// dial opens a connection to a peer, retrying once with a fresh client.
//
// Bringing up a tunnel for the first time has its own ten-second ceiling inside
// tailcat, independent of our deadline, so a peer that started moments ago --
// or restarted while we held a cached client -- fails on the first attempt and
// succeeds on the second. Retrying here is cheaper than making every caller
// understand that.
func (n *Node) dial(ctx context.Context, to string, cl *tailcat.Client) (net.Conn, error) {
	c, err := cl.DialTCPPort(ctx, wire.Port)
	if err == nil {
		return c, nil
	}
	n.dropClient(to)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("cannot reach %s before the deadline: %w", to, err)
	}
	fresh, _, ferr := n.client(to)
	if ferr != nil {
		return nil, ferr
	}
	c, err = fresh.DialTCPPort(ctx, wire.Port)
	if err != nil {
		n.dropClient(to)
		return nil, fmt.Errorf("cannot reach %s (it may be offline or still starting; `mesh peers` shows who is up): %w", to, err)
	}
	return c, nil
}

// dropClient discards a cached tunnel so the next attempt builds a fresh one.
// A dial that failed has told us the cached path is dead, whatever the roster
// still says.
func (n *Node) dropClient(name string) {
	n.mu.Lock()
	pc := n.clients[name]
	delete(n.clients, name)
	n.mu.Unlock()
	if pc != nil {
		pc.cl.Close()
	}
}

// Ask sends a question and waits for the answer, streaming progress to onChunk.
func (n *Node) Ask(ctx context.Context, to, body, thread string, onChunk func(string)) (string, error) {
	return n.AskWithFiles(ctx, to, body, thread, nil, onChunk)
}

// AskWithFiles is Ask with attachments.
func (n *Node) AskWithFiles(ctx context.Context, to, body, thread string, files []string, onChunk func(string)) (string, error) {
	cl, _, err := n.client(to)
	if err != nil {
		return "", err
	}
	c, err := n.dial(ctx, to, cl)
	if err != nil {
		return "", err
	}
	defer c.Close()
	wc := wire.NewConn(c)

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(10 * time.Minute)
	}
	attached, err := describeFiles(files)
	if err != nil {
		return "", err
	}
	id := wire.NewID()
	req := wire.Envelope{ID: id, From: n.cfg.Name, To: to, Kind: wire.KindAsk, Thread: thread, Body: body, Deadline: deadline, TS: time.Now().UTC(), Files: stripPaths(attached)}
	if err := wc.Send(req); err != nil {
		return "", err
	}
	if err := sendFiles(wc, n.cfg.Name, to, id, attached); err != nil {
		return "", fmt.Errorf("sending attachments to %s: %w", to, err)
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
func (n *Node) Tell(ctx context.Context, to, body, thread string) (bool, error) {
	return n.TellWithFiles(ctx, to, body, thread, nil)
}

// refusedError is a refusal the peer produced deliberately -- blocked, not
// permitted, malformed. It is never queued: retrying it would mean waiting for
// a peer to change its mind, which is not what an outbox is for.
type refusedError struct{ err error }

func (e refusedError) Error() string { return e.err.Error() }
func (e refusedError) Unwrap() error { return e.err }

// TellWithFiles is Tell with attachments. It reports whether the message was
// queued rather than delivered.
//
// A tell to a peer that is not reachable is spooled instead of failing, because
// the usual reason a peer is missing is that it restarted a moment ago and
// losing the message is the worst available outcome. Two things are still
// refused rather than queued: a name that is not on the mesh at all, since
// queueing for it would mean waiting for a peer that was never coming, and a
// refusal the peer sent on purpose.
//
// Attachments are not spooled yet. Their bytes stream after the envelope rather
// than living in it, so queueing one means keeping a copy of the file and
// deciding what happens when it changes underneath -- worth doing, not worth
// guessing at. Until then a tell carrying files fails fast and says why.
func (n *Node) TellWithFiles(ctx context.Context, to, body, thread string, files []string) (bool, error) {
	attached, err := describeFiles(files)
	if err != nil {
		return false, err
	}
	req := wire.Envelope{ID: wire.NewID(), From: n.cfg.Name, To: to, Kind: wire.KindTell, Thread: thread, Body: body, TS: time.Now().UTC(), Files: stripPaths(attached)}

	info, known := n.peer(to)
	offline := known && !info.Online

	// A peer the roster shows as down is queued without dialling it: the dial
	// would spend the caller's whole deadline discovering what we already know.
	if offline && n.canQueue(to, attached) {
		return true, n.queue(to, req, spool.ReasonOffline)
	}

	if err := n.deliverTell(ctx, to, req, attached); err != nil {
		var refused refusedError
		if errors.As(err, &refused) || !known || !n.canQueue(to, attached) {
			return false, err
		}
		reason := spool.ReasonUnreachable
		if offline {
			reason = spool.ReasonOffline
		}
		if qerr := n.queue(to, req, reason); qerr != nil {
			// Report the original failure; the queue error explains why it
			// could not be softened.
			return false, fmt.Errorf("%w (and it could not be queued: %v)", err, qerr)
		}
		return true, nil
	}
	return false, nil
}

// canQueue reports whether a message for this peer may be spooled at all.
func (n *Node) canQueue(to string, attached []wire.File) bool {
	return n.spool != nil && len(attached) == 0 && to != n.cfg.Name
}

func (n *Node) queue(to string, req wire.Envelope, reason string) error {
	if err := n.spool.Add(to, req, reason); err != nil {
		return err
	}
	n.appendInbox(req)
	n.logf("queued a message for %s (%s); it goes when the peer is back", to, reason)
	return nil
}

// peer returns what the roster currently says about a name.
func (n *Node) peer(name string) (ident.Info, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	i, ok := n.roster[name]
	return i, ok
}

// deliverTell is the wire half of a tell, with no queueing in it.
func (n *Node) deliverTell(ctx context.Context, to string, req wire.Envelope, attached []wire.File) error {
	cl, _, err := n.client(to)
	if err != nil {
		return err
	}
	c, err := n.dial(ctx, to, cl)
	if err != nil {
		return err
	}
	defer c.Close()
	wc := wire.NewConn(c)
	if d, ok := ctx.Deadline(); ok {
		wc.SetDeadline(d)
	}
	if err := wc.Send(req); err != nil {
		return err
	}
	if err := sendFiles(wc, n.cfg.Name, to, req.ID, attached); err != nil {
		return fmt.Errorf("sending attachments to %s: %w", to, err)
	}
	n.appendInbox(req)
	e, err := wc.Recv()
	if err != nil {
		return fmt.Errorf("%s did not acknowledge: %w", to, err)
	}
	if e.Kind == wire.KindError {
		return refusedError{fmt.Errorf("%s: %s", to, e.Body)}
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
		n.dropClient(to)
		return res, fmt.Errorf("cannot reach %s: %w", to, err)
	}
	res.Latency = time.Since(start)
	// A purely numeric region code is tailcat's renumbering of an embedded
	// region, not a public relay -- it printed "relayed via DERP 1", which
	// names nothing a reader can look up. Only a real code (nyc, sfo, fra,
	// tok) is worth showing.
	if pr.Endpoint != "" {
		res.Path = "direct " + pr.Endpoint
	} else if code := pr.DERPRegionCode; code != "" && !isAllDigits(code) {
		res.Path = "relayed via " + code
	} else {
		res.Path = "relayed"
	}
	return res, nil
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// ---------- roster ----------

// isCoordinator reports whether a roster entry is the node whose address this
// mesh is joined through. No field says so -- an invite deliberately does not
// carry the coordinator's name (§14) -- but the mesh's address embeds its key,
// so comparing them answers it exactly.
func (n *Node) isCoordinator(i ident.Info) bool {
	if n.mesh.Hub == "" || i.ServerPub == "" {
		return false
	}
	ci, err := tailcat.ParseConnBlob(tailcat.ConnBlob(n.mesh.Hub))
	if err != nil {
		return false
	}
	return ident.PubText(ci.ServerPublic.NodePublic) == i.ServerPub
}

// peerKey returns the server key the roster currently advertises for a peer,
// which is what the policy store pins and compares against.
func (n *Node) peerKey(name string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.roster[name].ServerPub
}

// Policy exposes this node's peer decisions to the control socket.
func (n *Node) Policy() *policy.Store { return n.policy }

// ForgetPeer drops a peer from the roster as well as from the policy store.
//
// Keeping peers we have known is what stops a coordinator restart turning them
// into strangers, but it also means a node that is gone for good never leaves.
// Forgetting is the way to say it is gone: the roster entry goes, and the next
// message from that name pins whatever key it presents.
func (n *Node) ForgetPeer(name string) {
	n.mu.Lock()
	delete(n.roster, name)
	n.byAddr = map[netip.Addr]string{}
	for _, i := range n.roster {
		if a, err := i.ClientAddr(); err == nil {
			n.byAddr[a] = i.Name
		}
	}
	remaining := make([]ident.Info, 0, len(n.roster))
	for _, i := range n.roster {
		remaining = append(remaining, i)
	}
	pc := n.clients[name]
	delete(n.clients, name)
	n.mu.Unlock()

	if pc != nil {
		pc.cl.Close()
	}
	n.saveRoster(remaining)
}

func (n *Node) peerAt(a netip.Addr) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	name, ok := n.byAddr[a]
	return name, ok
}

// allowlisting reports whether this node restricts tunnels to known peers.
//
// A coordinator must not: accepting a tunnel from a node it has never seen is
// exactly what joining is, and tailcat's allowlist is per-tunnel rather than
// per-port, so switching it on would make the mesh unjoinable the moment the
// coordinator learned its first peer. Identity is still enforced a layer up --
// serveTunnel drops a caller that is not in the roster, and registering
// requires the mesh's join key.
func (n *Node) allowlisting() bool { return n.coord == nil }

// applyRoster replaces what this node knows about the mesh and widens the
// tunnel allowlist to match.
//
// Allowlisting is add-only for the life of the process: tailcat can grant a
// peer key but not revoke one. A peer removed from the mesh therefore stays
// dialable until the daemon restarts, which is the honest limit of this
// prototype rather than a property to rely on.
func (n *Node) applyRoster(rs []ident.Info) {
	n.mu.Lock()
	next := map[string]ident.Info{}
	var returned []string
	for _, i := range rs {
		if i.Name == n.cfg.Name {
			continue
		}
		// Every online peer is a candidate to drain, not only one that just
		// came back. A message queued as "unreachable" was queued while the
		// peer was already online, so a transition that never happens would
		// leave it waiting forever -- a queue that does not drain is the
		// silent loss this replaces, wearing a different hat.
		returned = append(returned, i.Name)
		i.Online = true
		next[i.Name] = i
	}
	// A peer missing from this roster is not a peer we have never met. The
	// coordinator builds the roster from its live sessions, so a restart
	// reports an empty mesh until everyone reconnects -- and forgetting them
	// in that window would make us reject their messages with "you are not in
	// the roster", which is both wrong and alarming. Keep what we knew, marked
	// offline, and let presence come from the roster while identity persists.
	for name, prev := range n.roster {
		if _, live := next[name]; !live {
			prev.Online = false
			next[name] = prev
		}
	}
	n.roster = next
	n.byAddr = map[netip.Addr]string{}
	for _, i := range n.roster {
		if a, err := i.ClientAddr(); err == nil {
			n.byAddr[a] = i.Name
		}
	}
	names := make([]ident.Info, 0, len(n.roster))
	for _, i := range n.roster {
		names = append(names, i)
	}
	n.mu.Unlock()

	// The roster can be applied before the tunnel exists -- a cached roster is
	// loaded at startup, and tests apply one directly -- so there may be no
	// server to widen yet. Start installs the allowlist again once it is up.
	// The mesh's coordinator starts trusted, because joining its mesh is how
	// this node said yes. Someone had to hand over the join key and someone
	// had to accept it, and requiring a second, per-pair approval afterwards
	// is friction that buys nothing: a stranger cannot become the coordinator
	// without already holding the address everyone dials.
	//
	// Every other peer still has to be allowed. Being on a mesh together is
	// not the same as being the node whose mesh it is.
	for _, i := range names {
		if n.isCoordinator(i) {
			n.policy.Seed(i.Name, i.ServerPub, true)
		}
	}

	if n.allowlisting() && n.srv != nil {
		for _, i := range names {
			if k, err := ident.ParsePub(i.ClientPub); err == nil {
				n.srv.AddAllowedClient(k)
			}
		}
	}
	n.saveRoster(names)

	// Off the caller's goroutine: this is the coordinator's roster update, and
	// a slow peer must not hold up applying the rest of the mesh.
	for _, name := range returned {
		go n.flushSpool(name)
	}
}

// saveRoster caches the roster so loadRoster can restore it on a start with no
// hub to ask.
//
// An empty roster is never cached. The coordinator builds the roster from its
// live sessions, so every coordinator restart pushes one that holds only the
// nodes that have reconnected so far -- briefly none of them. Writing that
// through would erase the cache at exactly the moment it is the only way left
// to reach anyone, which is the outage loadRoster exists for. A stale peer
// costs a failed dial; an erased cache costs the mesh.
func (n *Node) saveRoster(rs []ident.Info) {
	if len(rs) == 0 {
		return
	}
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
		t := time.NewTicker(hubKeepalive)
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
		// A coordinator that dies without closing cleanly leaves this read
		// blocked forever: the tunnel is gone but netstack has nothing to
		// report, so the node would sit holding a dead connection and never
		// re-register. The deadline is the only reliable signal. It is well
		// clear of the 30s keepalive, each of which draws a reply.
		c.SetReadDeadline(time.Now().Add(hubSilenceTimeout))
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
