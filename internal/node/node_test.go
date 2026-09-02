package node

import (
	"context"
	"encoding/json"
	"github.com/xuy/agent-mesh/internal/spool"
	"github.com/xuy/agent-mesh/internal/wire"
	"os"
	"testing"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/hub"
	"github.com/xuy/agent-mesh/internal/ident"
)

func TestCoordinatorDoesNotAllowlist(t *testing.T) {
	// Joining means dialing a node that has never heard of you. tailcat's
	// allowlist is per-tunnel, not per-port, so a coordinator that allowlisted
	// would drop every new node's first connection and the mesh would become
	// unjoinable as soon as it had one member.
	n := New(config.Node{Name: "master"}, config.Mesh{Name: "t", Coordinator: "master"}, ident.New(), nil)
	n.coord = hub.New(config.Mesh{Name: "t"}, nil)
	if n.allowlisting() {
		t.Fatal("a coordinator that allowlists makes its own mesh unjoinable")
	}
}

func TestPlainNodeAllowlists(t *testing.T) {
	n := New(config.Node{Name: "peer"}, config.Mesh{Name: "t", Coordinator: "master"}, ident.New(), nil)
	if !n.allowlisting() {
		t.Fatal("a plain node should restrict tunnels to peers it knows")
	}
}

func TestAdapterSelection(t *testing.T) {
	cases := []struct {
		cfg  config.Node
		want string
	}{
		{config.Node{Name: "a"}, "mailbox"},
		{config.Node{Name: "a", Adapter: "exec", Exec: "echo hi"}, "exec"},
		{config.Node{Name: "a", Adapter: "webhook", WebhookURL: "http://127.0.0.1:1"}, "webhook"},
		{config.Node{Name: "a", Adapter: "notify", Exec: "true"}, "notify"},
		// A mode with no configuration must fall back to something that works
		// rather than silently answering nothing.
		{config.Node{Name: "a", Adapter: "webhook"}, "mailbox"},
		{config.Node{Name: "a", Adapter: "exec"}, "mailbox"},
	}
	for _, c := range cases {
		n := New(c.cfg, config.Mesh{Name: "t"}, ident.New(), nil)
		if got := n.ad.Kind(); got != c.want {
			t.Errorf("adapter %q + url %q -> %q, want %q", c.cfg.Adapter, c.cfg.WebhookURL, got, c.want)
		}
		// Every mode that parks must expose its mailbox, or `mesh reply`
		// would have nothing to answer into.
		if c.want != "exec" && n.mailbox == nil {
			t.Errorf("adapter %q has no mailbox, so nobody could answer it", c.want)
		}
	}
}

func TestEmptyRosterDoesNotEraseTheCache(t *testing.T) {
	// The coordinator builds the roster from its live sessions, so restarting
	// it pushes a roster holding only the nodes that have reconnected so far --
	// briefly none. Caching that erases the peers this node needs precisely
	// when the hub is the thing that is down, which is the outage loadRoster
	// exists for. Observed on a real two-machine mesh: a coordinator restart
	// left `mesh peers` empty and roster.json overwritten with `[]`, and the
	// peer became unreachable even though its hub was still answering.
	t.Setenv("MESH_HOME", t.TempDir())
	n := New(config.Node{Name: "windows"}, config.Mesh{Name: "t", Coordinator: "master"}, ident.New(), nil)

	known := []ident.Info{{Name: "master"}}
	n.saveRoster(known)

	n.saveRoster(nil)

	b, err := os.ReadFile(config.RosterPath("windows"))
	if err != nil {
		t.Fatalf("the cache should still be on disk: %v", err)
	}
	var got []ident.Info
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal cache: %v", err)
	}
	if len(got) != 1 || got[0].Name != "master" {
		t.Fatalf("an empty roster erased the cache: got %v, want the known peer to survive", got)
	}
}

// offlinePeer builds a node that knows one peer and has seen it go away, which
// is the state the coordinator leaves behind when a peer restarts.
func offlinePeer(t *testing.T) *Node {
	t.Helper()
	t.Setenv("MESH_HOME", t.TempDir())
	n := New(config.Node{Name: "windows"}, config.Mesh{Name: "t", Coordinator: "master"}, ident.New(), nil)
	n.applyRoster([]ident.Info{{Name: "master"}})
	n.applyRoster(nil) // the peer is kept, marked offline
	if i, ok := n.peer("master"); !ok || i.Online {
		t.Fatal("expected master to be known and offline")
	}
	return n
}

func TestTellQueuesForAnOfflinePeer(t *testing.T) {
	// The point of the spool: losing the message is the worst outcome, and the
	// usual reason a peer is missing is that it restarted a moment ago.
	n := offlinePeer(t)
	queued, err := n.Tell(context.Background(), "master", "hello", "")
	if err != nil {
		t.Fatalf("a tell to an offline peer should queue, not fail: %v", err)
	}
	if !queued {
		t.Fatal("expected the message to be reported as queued")
	}
	box, err := n.Outbox()
	if err != nil {
		t.Fatal(err)
	}
	q := box["master"]
	if len(q) != 1 || q[0].Env.Body != "hello" {
		t.Fatalf("expected one queued message, got %+v", q)
	}
	if q[0].Reason != spool.ReasonOffline {
		t.Fatalf("the reason should say the peer was offline, got %q", q[0].Reason)
	}
	if q[0].Env.TS.IsZero() {
		t.Fatal("a queued message must keep the time it was written, not the time it is sent")
	}
}

func TestTellRefusesAnUnknownPeerRatherThanQueueing(t *testing.T) {
	// Queueing for a name that is not on the mesh means waiting for a peer that
	// was never coming, and hiding a typo until someone reads the outbox.
	n := offlinePeer(t)
	queued, err := n.Tell(context.Background(), "nosuchpeer", "hello", "")
	if err == nil {
		t.Fatal("expected an error for a peer that is not on the mesh")
	}
	if queued {
		t.Fatal("an unknown peer must not be queued for")
	}
	box, _ := n.Outbox()
	if len(box["nosuchpeer"]) != 0 {
		t.Fatalf("nothing should have been queued: %+v", box)
	}
}

func TestQueuedMessagesFlushWhenThePeerComesBack(t *testing.T) {
	// The flush runs off the roster update, because the roster is the only
	// thing that actually knows a peer returned.
	n := offlinePeer(t)
	if _, err := n.Tell(context.Background(), "master", "first", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Tell(context.Background(), "master", "second", ""); err != nil {
		t.Fatal(err)
	}
	q, _ := n.spool.Pending("master")
	if len(q) != 2 || q[0].Env.Body != "first" {
		t.Fatalf("expected two queued in order, got %+v", q)
	}

	// Flush with a delivery func that succeeds, standing in for a reachable
	// peer: the wire half needs a tunnel, and what is under test here is that
	// the queue drains in order and empties.
	var got []string
	sent, err := n.spool.Flush("master", func(e wire.Envelope) error {
		got = append(got, e.Body)
		return nil
	})
	if err != nil || sent != 2 {
		t.Fatalf("flush: sent=%d err=%v", sent, err)
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("a conversation must arrive in the order it was written: %v", got)
	}
	box, _ := n.Outbox()
	if len(box) != 0 {
		t.Fatalf("the outbox should be empty after a full flush: %+v", box)
	}
}
