package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/xuy/agent-mesh/internal/adapter"
	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/ident"
	"github.com/xuy/agent-mesh/internal/wire"
)

// CtlReq is a command from the local CLI to the running daemon.
type CtlReq struct {
	Op         string `json:"op"`
	To         string `json:"to,omitempty"`
	Body       string `json:"body,omitempty"`
	Thread     string `json:"thread,omitempty"`
	ID         string `json:"id,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Incoming   bool   `json:"incoming,omitempty"`
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
	case "peers":
		enc.Encode(CtlResp{Kind: "ok", Peers: n.Peers()})
	case "inbox":
		enc.Encode(CtlResp{Kind: "ok", Msgs: n.Inbox(req.Limit, req.Incoming)})
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
		if err := n.Tell(ctx, req.To, req.Body, req.Thread); err != nil {
			fail(err)
			return
		}
		enc.Encode(CtlResp{Kind: "ok", Body: "delivered to " + req.To})
	case "ask":
		answer, err := n.Ask(ctx, req.To, req.Body, req.Thread, func(chunk string) {
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
		fail(fmt.Errorf("unknown command %q", req.Op))
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
		Name: n.cfg.Name, Mesh: n.mesh.Name, Agent: n.cfg.Agent,
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
	c, err := net.DialTimeout("unix", config.SockPath(name), 2*time.Second)
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
	c, err := net.DialTimeout("unix", config.SockPath(x.name), 5*time.Second)
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
