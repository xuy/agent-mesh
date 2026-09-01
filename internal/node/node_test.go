package node

import (
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
