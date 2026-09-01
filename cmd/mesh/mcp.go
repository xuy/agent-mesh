package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/xuy/agent-mesh/internal/node"
)

// The MCP server exposes the mesh as tools rather than shell commands, so an
// agent can reach its peers without knowing a CLI exists. It speaks JSON-RPC
// 2.0 over stdio, which is all the protocol requires of a local tool server.

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func mcpTools() []toolDef {
	return []toolDef{
		{"mesh_peers", "List the other agents on the mesh: their names, what software they run, and what each is for. Call this before asking anyone anything.", obj(map[string]any{})},
		{"mesh_ask", "Ask another agent a question and wait for its answer. There is a model running on the other side, so this can take a while; raise timeout_seconds for real work. Pass the same thread across turns of one conversation so the peer keeps context.", obj(map[string]any{
			"peer":            str("the peer's name, from mesh_peers"),
			"question":        str("what to ask"),
			"thread":          str("optional: groups this with earlier messages so the peer keeps context"),
			"timeout_seconds": map[string]any{"type": "integer", "description": "how long to wait (default 300)"},
		}, "peer", "question")},
		{"mesh_send", "Tell another agent something without waiting for a reply.", obj(map[string]any{
			"peer":    str("the peer's name"),
			"message": str("what to say"),
		}, "peer", "message")},
		{"mesh_waiting", "List questions other agents have asked YOU that are still waiting for an answer. Check this when you finish a task -- a peer may be blocked on you.", obj(map[string]any{})},
		{"mesh_reply", "Answer a question another agent asked you. Get the id from mesh_waiting.", obj(map[string]any{
			"id":     str("the question's id"),
			"answer": str("your answer"),
		}, "id", "answer")},
		{"mesh_inbox", "Show recent messages sent to you, answered or not.", obj(map[string]any{
			"limit": map[string]any{"type": "integer", "description": "how many to show (default 20)"},
		})},
		{"mesh_status", "Report this node: its name, its mesh, how it answers questions, and whether discovery is working.", obj(map[string]any{})},
	}
}

func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	fs.Parse(args)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := json.NewEncoder(os.Stdout)

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp, send := mcpHandle(req, *name)
		if send {
			out.Encode(resp)
		}
	}
	return nil
}

func mcpHandle(req rpcReq, nodeName string) (rpcResp, bool) {
	r := rpcResp{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		r.Result = map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agent-mesh", "version": "0.1.0"},
			"instructions":    "Other agents are reachable by name. Call mesh_peers to see who is here, mesh_ask to ask one something, and mesh_waiting when you finish a task in case a peer is blocked on you.",
		}
	case "notifications/initialized", "notifications/cancelled":
		return r, false
	case "ping":
		r.Result = map[string]any{}
	case "tools/list":
		r.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var p struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		text, isErr := mcpCall(nodeName, p.Name, p.Args)
		r.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}
	default:
		r.Error = &rpcErr{Code: -32601, Message: "unknown method " + req.Method}
	}
	return r, true
}

func mcpCall(nodeName, tool string, raw json.RawMessage) (string, bool) {
	var a struct {
		Peer     string `json:"peer"`
		Question string `json:"question"`
		Message  string `json:"message"`
		Thread   string `json:"thread"`
		Timeout  int    `json:"timeout_seconds"`
		ID       string `json:"id"`
		Answer   string `json:"answer"`
		Limit    int    `json:"limit"`
	}
	json.Unmarshal(raw, &a)

	c, nm, err := ctlFor(nodeName)
	if err != nil {
		return err.Error(), true
	}
	_ = nm

	jsonOr := func(v any, err error) (string, bool) {
		if err != nil {
			return err.Error(), true
		}
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b), false
	}

	switch tool {
	case "mesh_peers":
		r, err := c.Do(node.CtlReq{Op: "peers"}, nil)
		if err != nil {
			return err.Error(), true
		}
		if len(r.Peers) == 0 {
			return "No other agents are on the mesh right now.", false
		}
		// Only what helps a model choose who to ask. The roster also carries
		// keys and a long relay address, which are the daemon's business.
		type peerView struct {
			Name   string   `json:"name"`
			Agent  string   `json:"agent,omitempty"`
			For    string   `json:"for,omitempty"`
			Kinds  []string `json:"kinds,omitempty"`
			Online bool     `json:"online"`
		}
		out := make([]peerView, 0, len(r.Peers))
		for _, p := range r.Peers {
			out = append(out, peerView{Name: p.Name, Agent: p.Agent, For: p.Note, Kinds: p.Kinds, Online: p.Online})
		}
		return jsonOr(out, nil)
	case "mesh_status":
		r, err := c.Do(node.CtlReq{Op: "status"}, nil)
		return jsonOr(r.Status, err)
	case "mesh_ask":
		if a.Timeout <= 0 {
			a.Timeout = 300
		}
		r, err := c.Do(node.CtlReq{Op: "ask", To: a.Peer, Body: a.Question, Thread: a.Thread, TimeoutSec: a.Timeout}, nil)
		if err != nil {
			return err.Error(), true
		}
		return r.Body, false
	case "mesh_send":
		r, err := c.Do(node.CtlReq{Op: "tell", To: a.Peer, Body: a.Message, Thread: a.Thread, TimeoutSec: 60}, nil)
		if err != nil {
			return err.Error(), true
		}
		return r.Body, false
	case "mesh_waiting":
		r, err := c.Do(node.CtlReq{Op: "waiting"}, nil)
		if err != nil {
			return err.Error(), true
		}
		if len(r.Waiting) == 0 {
			return "No one is waiting on you.", false
		}
		return jsonOr(r.Waiting, nil)
	case "mesh_reply":
		r, err := c.Do(node.CtlReq{Op: "reply", ID: a.ID, Body: a.Answer}, nil)
		if err != nil {
			return err.Error(), true
		}
		return r.Body, false
	case "mesh_inbox":
		if a.Limit <= 0 {
			a.Limit = 20
		}
		r, err := c.Do(node.CtlReq{Op: "inbox", Limit: a.Limit, Incoming: true}, nil)
		return jsonOr(r.Msgs, err)
	default:
		return fmt.Sprintf("unknown tool %q", tool), true
	}
}
