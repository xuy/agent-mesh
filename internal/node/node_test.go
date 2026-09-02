package node

import (
	"encoding/json"
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
