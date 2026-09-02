package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/xuy/agent-mesh/internal/adapter"
	"github.com/xuy/agent-mesh/internal/ident"
	"github.com/xuy/agent-mesh/internal/policy"
	"github.com/xuy/agent-mesh/internal/wire"
)

// CtlReq is a command from the local CLI to the running daemon.
type CtlReq struct {
	Op         string   `json:"op"`
	To         string   `json:"to,omitempty"`
	Body       string   `json:"body,omitempty"`
	Thread     string   `json:"thread,omitempty"`
	ID         string   `json:"id,omitempty"`
	TimeoutSec int      `json:"timeout_sec,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Incoming   bool     `json:"incoming,omitempty"`
	Files      []string `json:"files,omitempty"`
}

// CtlResp is one frame of the daemon's answer. A streaming command sends any
// number of "chunk" frames and exactly one terminal "ok" or "error" frame.
type CtlResp struct {
	Kind    string             `json:"kind"`
	Error   string             `json:"error,omitempty"`
	Body    string             `json:"body,omitempty"`
	Peers   []ident.Info       `json:"peers,omitempty"`
	Msgs    []wire.Envelope    `json:"msgs,omitempty"`
	Status  *Status            `json:"status,omitempty"`
	Ping    *PingResult        `json:"ping,omitempty"`
	Waiting []adapter.Question `json:"waiting,omitempty"`
	Trust   []*policy.Peer     `json:"trust,omitempty"`
	Audit   []auditEntry       `json:"audit,omitempty"`
}

// Status is a snapshot of the daemon, for `mesh status` and `mesh doctor`.
type Status struct {
	Name    string `json:"name"`
	Mesh    string `json:"mesh"`
	Agent   string `json:"agent,omitempty"`
	Adapter string `json:"adapter"`
	Blob    string `json:"blob,omitempty"`
	HubUp   bool   `json:"hub_up"`
	Peers   int    `json:"peers"`
	Online  int    `json:"online"`
	Uptime  string `json:"uptime"`
	PID     int    `json:"pid"`

	// Key is this node's own public key, so `mesh id` can show a fingerprint
	// for someone to read out to whoever is adding them.
	Key string `json:"key,omitempty"`

	// Relay is the relay this node is reachable through. Empty means no peer
	// can open a connection to it, whatever else looks healthy.
	Relay string `json:"relay,omitempty"`
}

// ServeCtl accepts local CLI connections until the node closes.
func (n *Node) ServeCtl(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-n.closed:
				return
			default:
			}
			return
		}
		go n.serveCtlConn(c)
	}
}

func (n *Node) serveCtlConn(c net.Conn) {
	defer c.Close()
	dec := json.NewDecoder(c)
	enc := json.NewEncoder(c)
	var req CtlReq
	if err := dec.Decode(&req); err != nil {
		return
	}
	fail := func(err error) { enc.Encode(CtlResp{Kind: "error", Error: err.Error()}) }

	ctx := context.Background()
	if req.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second)
		defer cancel()
	}

	switch req.Op {
	case "status":
		enc.Encode(CtlResp{Kind: "ok", Status: n.status()})
	case "trust":
		enc.Encode(CtlResp{Kind: "ok", Trust: n.policy.All()})
	case "audit":
		enc.Encode(CtlResp{Kind: "ok", Audit: n.Audit(req.Limit)})
	case "allow", "deny", "block", "unblock", "verify", "forget":
		if req.To == "" {
			fail(fmt.Errorf("which peer?"))
			return
		}
		var err error
		switch req.Op {
		case "allow":
			err = n.policy.SetMayAsk(req.To, true)
		case "deny":
			err = n.policy.SetMayAsk(req.To, false)
		case "block":
			err = n.policy.SetBlocked(req.To, true)
		case "unblock":
			err = n.policy.SetBlocked(req.To, false)
		case "verify":
			err = n.policy.SetVerified(req.To, n.peerKey(req.To))
		case "forget":
			err = n.policy.Forget(req.To)
			n.ForgetPeer(req.To)
		}
		if err != nil {
			fail(err)
			return
		}
		enc.Encode(CtlResp{Kind: "ok", Body: req.Op + " " + req.To, Trust: n.policy.All()})
	case "peers":
		enc.Encode(CtlResp{Kind: "ok", Peers: n.Peers()})
	case "inbox":
		enc.Encode(CtlResp{Kind: "ok", Msgs: n.Inbox(req.Limit, req.Incoming)})
	case "wait":
		// Anything already parked counts as having arrived: a peer blocked on
		// us right now is more urgent than the next message.
		if n.mailbox != nil {
			if q := n.mailbox.Waiting(); len(q) > 0 && req.ID == "" {
				enc.Encode(CtlResp{Kind: "ok", Msgs: n.recent(q)})
				return
			}
		}
		// Anything that arrived while the agent was busy counts too. Waiting
		// only for what comes next is a race the agent always eventually
		// loses, and an agent that has to track an id between turns to avoid
		// that will forget to.
		missed := n.Unread()
		if req.ID != "" {
			missed = n.since(req.ID)
		}
		if len(missed) > 0 {
			n.setReadCursor(missed[len(missed)-1].ID)
			enc.Encode(CtlResp{Kind: "ok", Msgs: missed})
			return
		}
		ch, release := n.Subscribe()
		defer release()
		select {
		case e := <-ch:
			n.setReadCursor(e.ID)
			enc.Encode(CtlResp{Kind: "ok", Msgs: []wire.Envelope{e}})
		case <-ctx.Done():
			enc.Encode(CtlResp{Kind: "error", Error: "nothing arrived before the timeout"})
		}
	case "waiting":
		if n.mailbox == nil {
			fail(fmt.Errorf("%s answers with an exec adapter, so nothing waits for a human", n.cfg.Name))
			return
		}
		enc.Encode(CtlResp{Kind: "ok", Waiting: n.mailbox.Waiting()})
	case "reply":
		if n.mailbox == nil {
			fail(fmt.Errorf("%s answers with an exec adapter; there is nothing to reply to", n.cfg.Name))
			return
		}
		if err := n.mailbox.Reply(req.ID, req.Body); err != nil {
			fail(err)
			return
		}
		enc.Encode(CtlResp{Kind: "ok", Body: "answered " + req.ID})
	case "tell":
		if err := n.TellWithFiles(ctx, req.To, req.Body, req.Thread, req.Files); err != nil {
			fail(err)
			return
		}
		enc.Encode(CtlResp{Kind: "ok", Body: "delivered to " + req.To})
	case "ask":
		answer, err := n.AskWithFiles(ctx, req.To, req.Body, req.Thread, req.Files, func(chunk string) {
			enc.Encode(CtlResp{Kind: "chunk", Body: chunk})
		})
		if err != nil {
			fail(err)
			return
		}
		enc.Encode(CtlResp{Kind: "ok", Body: answer})
	case "ping":
		pr, err := n.Ping(ctx, req.To)
		if err != nil {
			fail(err)
			return
		}
		enc.Encode(CtlResp{Kind: "ok", Ping: &pr})
	case "stop":
		enc.Encode(CtlResp{Kind: "ok", Body: "stopping " + n.cfg.Name})
		go func() { time.Sleep(100 * time.Millisecond); n.Close(); os.Exit(0) }()
	default:
		// Almost always version skew: the CLI was rebuilt and the daemon is
		// still the process the service manager started from the old binary.
		fail(fmt.Errorf("this daemon does not know the command %q -- it is probably older than your `mesh` binary. Restart it with `mesh down --name %s && mesh up --name %s`", req.Op, n.cfg.Name, n.cfg.Name))
	}
}

func (n *Node) status() *Status {
	peers := n.Peers()
	online := 0
	for _, p := range peers {
		if p.Online {
			online++
		}
	}
	blob := ""
	if n.srv != nil {
		blob = string(n.srv.ConnBlob())
	}
	return &Status{
		Key:   ident.PubText(n.id.Server.Public()),
		Relay: n.RelayHome(),
		Name:  n.cfg.Name, Mesh: n.mesh.Name, Agent: n.cfg.Agent,
		Adapter: n.ad.Kind(), Blob: blob, HubUp: n.HubUp(),
		Peers: len(peers), Online: online,
		Uptime: time.Since(n.started).Round(time.Second).String(), PID: os.Getpid(),
	}
}

// ---------- client side ----------

// Ctl is a connection from the CLI to a running daemon.
type Ctl struct{ name string }

// Dial checks that a daemon for name is listening.
func Dial(name string) (*Ctl, error) {
	c, err := dialControl(name, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("no mesh daemon is running for %q -- start one with `mesh up --name %s`", name, name)
	}
	c.Close()
	return &Ctl{name: name}, nil
}

// Do sends one command and returns the terminal frame, passing any streamed
// progress to onChunk.
func (x *Ctl) Do(req CtlReq, onChunk func(string)) (CtlResp, error) {
	var last CtlResp
	c, err := dialControl(x.name, 5*time.Second)
	if err != nil {
		return last, fmt.Errorf("no mesh daemon is running for %q -- start one with `mesh up --name %s`", x.name, x.name)
	}
	defer c.Close()
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return last, err
	}
	dec := json.NewDecoder(c)
	for {
		var resp CtlResp
		if err := dec.Decode(&resp); err != nil {
			if last.Kind == "" {
				return last, fmt.Errorf("the daemon closed the connection without answering")
			}
			return last, nil
		}
		if resp.Kind == "chunk" {
			if onChunk != nil {
				onChunk(resp.Body)
			}
			continue
		}
		if resp.Kind == "error" {
			return resp, fmt.Errorf("%s", resp.Error)
		}
		return resp, nil
	}
}
