// Package hub implements the mesh control plane.
//
// The hub is a phone book, not a switchboard: it resolves a name into a
// dialable address and tells every node who else exists, then gets out of the
// way. Agents talk to each other directly, peer to peer, and a mesh whose hub
// has died keeps working off each node's cached roster.
package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/tailscale/tailcat"
	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/ident"
	"github.com/xuy/agent-mesh/internal/wire"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// Req is a control-plane request.
type Req struct {
	V    int        `json:"v"`
	Op   string     `json:"op"` // "register", "roster", "ping"
	Join string     `json:"join,omitempty"`
	Node ident.Info `json:"node,omitzero"`
}

// Resp is a control-plane response. The hub also pushes an unsolicited Resp
// carrying a fresh roster whenever membership changes.
type Resp struct {
	V      int          `json:"v"`
	OK     bool         `json:"ok"`
	Error  string       `json:"error,omitempty"`
	Mesh   string       `json:"mesh,omitempty"`
	Roster []ident.Info `json:"roster,omitempty"`
}

// sessionTimeout is how long the hub waits to hear from a node before treating
// it as gone. Nodes ping every 15s, so this tolerates two missed pings.
const sessionTimeout = 45 * time.Second

// Hub is the running control plane.
type Hub struct {
	mesh config.Mesh
	logf func(string, ...any)

	mu       sync.Mutex
	claims   map[string]string // name -> server public key, persisted
	sessions map[string]*session
	srv      *tailcat.Server
}

// session is one registered node. Its send func hides how the roster reaches
// that node: over a tunnel for a remote one, by direct call for the node that
// is itself running this hub.
type session struct {
	info ident.Info
	send func(Resp)
	mu   sync.Mutex // serializes pushes, so two rosters never interleave
}

func (s *session) push(r Resp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.send(r)
}

// New returns a hub for the given mesh.
func New(m config.Mesh, logf func(string, ...any)) *Hub {
	if logf == nil {
		logf = log.Printf
	}
	return &Hub{mesh: m, logf: logf, claims: map[string]string{}, sessions: map[string]*session{}}
}

func claimsPath() string { return filepath.Join(config.HubDir(), "claims.json") }

// hubKey is the hub's persisted identity and relay choice.
//
// Both must survive a restart. A ConnBlob encodes the key *and* the DERP
// region, so a hub that regenerated either would invalidate every invite it
// had ever handed out -- the mesh's one bootstrap address would silently rot.
type hubKey struct {
	Key    string               `json:"key"`
	Region tailcfg.DERPRegionID `json:"region,omitempty"`
}

func keyPath() string { return filepath.Join(config.HubDir(), "hub.key") }

// StatePath is where a running hub records that it is up. A detached hub is
// otherwise silent, and its address is stable across restarts, so there is
// nothing else for a caller to poll.
func StatePath() string { return filepath.Join(config.HubDir(), "running.json") }

// State is what a running hub publishes about itself.
type State struct {
	Mesh    string    `json:"mesh"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// Running reports the state of a hub running on this machine, if any.
func Running() (State, bool) {
	var st State
	b, err := os.ReadFile(StatePath())
	if err != nil || json.Unmarshal(b, &st) != nil || st.PID == 0 {
		return st, false
	}
	p, err := os.FindProcess(st.PID)
	if err != nil {
		return st, false
	}
	return st, p.Signal(syscall.Signal(0)) == nil
}

func loadHubKey() (key.NodePrivate, tailcfg.DERPRegionID) {
	var k key.NodePrivate
	b, err := os.ReadFile(keyPath())
	if err != nil {
		return key.NewNode(), 0
	}
	var hk hubKey
	if json.Unmarshal(b, &hk) != nil || k.UnmarshalText([]byte(hk.Key)) != nil {
		return key.NewNode(), 0
	}
	return k, hk.Region
}

func saveHubKey(k key.NodePrivate, region tailcfg.DERPRegionID) error {
	t, err := k.MarshalText()
	if err != nil {
		return err
	}
	return config.WriteJSON(keyPath(), hubKey{Key: string(t), Region: region})
}

// Start brings up the hub's tunnel and begins accepting nodes. It returns the
// hub's ConnBlob, which is the mesh's bootstrap address.
func (h *Hub) Start() (string, error) {
	h.loadClaims()
	k, region := loadHubKey()
	if region == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if r, err := config.PickRegion(ctx); err == nil && r != 0 {
			region = tailcfg.DERPRegionID(r)
		} else {
			h.logf("hub: could not pin a relay (%v); this mesh's address may change when the hub restarts", err)
		}
		cancel()
	}
	h.srv = &tailcat.Server{
		Key:      k,
		RegionID: region,
		Logf:     func(string, ...any) {},
		OnTCP: func(port uint16) func(net.Conn) {
			if port != wire.HubPort {
				return nil
			}
			return h.serve
		},
	}
	if err := h.srv.Start(); err != nil {
		return "", err
	}
	blob := h.srv.ConnBlob()
	if region == 0 {
		// First start: pin whichever relay the netcheck chose, so the
		// address we are about to publish keeps working.
		if ci, err := tailcat.ParseConnBlob(blob); err == nil {
			region = ci.RegionID
		}
	}
	if err := saveHubKey(k, region); err != nil {
		h.logf("hub: saving key: %v", err)
	}
	if err := config.WriteJSON(StatePath(), State{Mesh: h.mesh.Name, PID: os.Getpid(), Started: time.Now()}); err != nil {
		h.logf("hub: publishing state: %v", err)
	}
	return string(blob), nil
}

// Close shuts the hub down.
func (h *Hub) Close() error {
	os.Remove(StatePath())
	if h.srv == nil {
		return nil
	}
	return h.srv.Close()
}

func (h *Hub) loadClaims() {
	b, err := os.ReadFile(claimsPath())
	if err != nil {
		return
	}
	json.Unmarshal(b, &h.claims)
}

func (h *Hub) saveClaimsLocked() {
	if err := config.WriteJSON(claimsPath(), h.claims); err != nil {
		h.logf("hub: saving claims: %v", err)
	}
}

// Handle serves one inbound control connection. A node that is also the
// coordinator wires this into its own tailcat server, so the mesh needs no
// separate process.
func (h *Hub) Handle(c net.Conn) { h.serve(c) }

// LoadClaims reads the persisted name claims. Start does this itself; a hub
// embedded in a node calls it directly.
func (h *Hub) LoadClaims() { h.loadClaims() }

// RegisterLocal registers the node that is running this hub, without a
// connection. onRoster receives the roster now and on every later change.
// The returned func unregisters.
func (h *Hub) RegisterLocal(info ident.Info, onRoster func([]ident.Info)) (func(), error) {
	sess, err := h.register(info, netip.Addr{}, func(r Resp) {
		if r.Roster != nil {
			onRoster(r.Roster)
		}
	})
	if err != nil {
		return nil, err
	}
	onRoster(h.roster())
	h.broadcast()
	return func() {
		h.mu.Lock()
		if h.sessions[info.Name] == sess {
			delete(h.sessions, info.Name)
		}
		h.mu.Unlock()
		h.broadcast()
	}, nil
}

// serve handles one node's long-lived control connection. Registration,
// liveness and roster delivery all ride on it, so presence is simply whether
// the connection is open.
func (h *Hub) serve(c net.Conn) {
	defer c.Close()
	remote := remoteAddr(c)
	var registered string
	var mine *session

	dec := json.NewDecoder(c)
	enc := json.NewEncoder(c)
	// Once registered, the hub may push a roster from another goroutine at any
	// time. Every write to this connection goes through one lock or two JSON
	// documents would interleave on the wire.
	reply := func(r Resp) {
		if mine != nil {
			mine.push(r)
			return
		}
		enc.Encode(r)
	}

	defer func() {
		if registered == "" {
			return
		}
		h.mu.Lock()
		// Only clean up if this connection still owns the name. A node that
		// died and came back has already replaced us; deleting the entry here
		// would silently unregister the live node.
		still := h.sessions[registered] == mine
		if still {
			delete(h.sessions, registered)
		}
		h.mu.Unlock()
		if still {
			h.logf("hub: %s left", registered)
			h.broadcast()
		}
	}()

	for {
		// A node that dies takes its tunnel with it, and netstack may never
		// deliver a FIN, so a blocked read would hold the name forever. The
		// deadline is the real liveness signal; nodes ping well inside it.
		c.SetReadDeadline(time.Now().Add(sessionTimeout))
		var req Req
		if err := dec.Decode(&req); err != nil {
			return
		}
		if subtle.ConstantTimeCompare([]byte(req.Join), []byte(h.mesh.Join)) != 1 {
			reply(Resp{V: wire.Version, Error: "bad join key for mesh " + h.mesh.Name})
			return
		}
		switch req.Op {
		case "roster":
			reply(Resp{V: wire.Version, OK: true, Mesh: h.mesh.Name, Roster: h.roster()})
		case "ping":
			h.mu.Lock()
			if mine != nil {
				mine.info.Seen = time.Now().UTC()
			}
			h.mu.Unlock()
			reply(Resp{V: wire.Version, OK: true})
		case "register":
			sess, err := h.register(req.Node, remote, func(r Resp) { enc.Encode(r) })
			if err != nil {
				reply(Resp{V: wire.Version, Error: err.Error()})
				return
			}
			registered, mine = sess.info.Name, sess
			reply(Resp{V: wire.Version, OK: true, Mesh: h.mesh.Name, Roster: h.roster()})
			h.broadcast()
		default:
			reply(Resp{V: wire.Version, Error: "unknown op " + req.Op})
			return
		}
	}
}

// register admits a node, enforcing the two rules that make a name mean
// something: the caller must actually hold the client key it claims, and a
// name, once claimed, belongs to one server key forever.
func (h *Hub) register(info ident.Info, remote netip.Addr, send func(Resp)) (*session, error) {
	if info.Name == "" {
		return nil, fmt.Errorf("a node must register a name")
	}
	if info.Blob == "" || info.ServerPub == "" || info.ClientPub == "" {
		return nil, fmt.Errorf("incomplete registration for %q", info.Name)
	}
	want, err := info.ClientAddr()
	if err != nil {
		return nil, fmt.Errorf("unparseable client key: %w", err)
	}
	if remote.IsValid() && want != remote {
		return nil, fmt.Errorf("client key does not match the connection it arrived on")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if prior, ok := h.claims[info.Name]; ok && prior != info.ServerPub {
		return nil, fmt.Errorf("the name %q is already claimed by another node", info.Name)
	}
	if live, ok := h.sessions[info.Name]; ok {
		if live.info.ServerPub != info.ServerPub {
			return nil, fmt.Errorf("%q is already connected from another node", info.Name)
		}
		// Same node, restarted. Its old session is a ghost: take the name back
		// rather than locking the node out of its own identity.
		h.logf("hub: %s reconnected, replacing its previous session", info.Name)
		delete(h.sessions, info.Name)
	}
	h.claims[info.Name] = info.ServerPub
	h.saveClaimsLocked()

	info.Mesh = h.mesh.Name
	info.Seen = time.Now().UTC()
	info.Online = true
	sess := &session{info: info, send: send}
	h.sessions[info.Name] = sess
	h.logf("hub: %s joined (%s)", info.Name, info.Agent)
	return sess, nil
}

func (h *Hub) roster() []ident.Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ident.Info, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, s.info)
	}
	return out
}

// broadcast pushes the current roster to every connected node, so a new peer
// is dialable everywhere within milliseconds of joining.
func (h *Hub) broadcast() {
	r := h.roster()
	h.mu.Lock()
	ss := make([]*session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	for _, s := range ss {
		s.push(Resp{V: wire.Version, OK: true, Mesh: h.mesh.Name, Roster: r})
	}
}

func remoteAddr(c net.Conn) netip.Addr {
	ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr()
}
