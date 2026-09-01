package hub

import (
	"encoding/json"
	"io"
	"net/netip"
	"strings"
	"testing"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/ident"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	t.Setenv("MESH_HOME", t.TempDir())
	return New(config.Mesh{Name: "test", Join: "k"}, func(string, ...any) {})
}

// nodeFor builds a registration for a fresh identity, along with the tunnel
// address the hub will see that node calling from.
func nodeFor(t *testing.T, name string) (ident.Info, netip.Addr, *ident.Identity) {
	t.Helper()
	id := ident.New()
	info := ident.Info{
		Name:      name,
		Blob:      "tcoBLOB-" + name,
		ServerPub: ident.PubText(id.Server.Public()),
		ClientPub: ident.PubText(id.Client.Public()),
	}
	return info, ident.Addr(id.Client.Public()), id
}

func discard() *json.Encoder { return json.NewEncoder(io.Discard) }

func TestRegisterClaimsAName(t *testing.T) {
	h := testHub(t)
	info, addr, _ := nodeFor(t, "master")
	if _, err := h.register(info, addr, discard()); err != nil {
		t.Fatal(err)
	}
	if got := h.roster(); len(got) != 1 || got[0].Name != "master" {
		t.Fatalf("roster wrong after registration: %+v", got)
	}
}

// A name, once claimed, must belong to one identity. Otherwise anyone who can
// reach the hub could impersonate a peer by taking its name.
func TestAnotherIdentityCannotStealAName(t *testing.T) {
	h := testHub(t)
	first, addr1, _ := nodeFor(t, "master")
	if _, err := h.register(first, addr1, discard()); err != nil {
		t.Fatal(err)
	}
	impostor, addr2, _ := nodeFor(t, "master")
	_, err := h.register(impostor, addr2, discard())
	if err == nil {
		t.Fatal("a different identity was allowed to claim a taken name")
	}
	if !strings.Contains(err.Error(), "already") {
		t.Fatalf("unhelpful rejection: %v", err)
	}
}

// A node that died without closing its tunnel leaves a session the hub cannot
// see is dead. When the same identity comes back it must reclaim its own name
// rather than be locked out of it -- this is the bug that made a restarted
// agent permanently unreachable.
func TestSameIdentityReclaimsItsNameAfterAGhostSession(t *testing.T) {
	h := testHub(t)
	info, addr, _ := nodeFor(t, "opencode")
	if _, err := h.register(info, addr, discard()); err != nil {
		t.Fatal(err)
	}
	info.Blob = "tcoBLOB-after-restart" // a restart changes the address
	if _, err := h.register(info, addr, discard()); err != nil {
		t.Fatalf("a restarted node could not reclaim its own name: %v", err)
	}
	got := h.roster()
	if len(got) != 1 {
		t.Fatalf("the ghost session was not replaced: %+v", got)
	}
	if got[0].Blob != "tcoBLOB-after-restart" {
		t.Fatalf("peers would still dial the dead address: %q", got[0].Blob)
	}
}

// The hub trusts the tunnel, not the payload: a registration must arrive on a
// connection whose address derives from the client key it claims.
func TestRegistrationMustMatchTheConnectionItArrivedOn(t *testing.T) {
	h := testHub(t)
	info, _, _ := nodeFor(t, "master")
	_, other, _ := nodeFor(t, "someone-else")
	if _, err := h.register(info, other, discard()); err == nil {
		t.Fatal("accepted a registration claiming a key the caller does not hold")
	}
}

func TestIncompleteRegistrationRejected(t *testing.T) {
	h := testHub(t)
	info, addr, _ := nodeFor(t, "master")
	info.Blob = ""
	if _, err := h.register(info, addr, discard()); err == nil {
		t.Fatal("accepted a node with no address")
	}
}

func TestClaimsSurviveAHubRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MESH_HOME", home)

	h1 := New(config.Mesh{Name: "test", Join: "k"}, func(string, ...any) {})
	info, addr, _ := nodeFor(t, "master")
	if _, err := h1.register(info, addr, discard()); err != nil {
		t.Fatal(err)
	}

	h2 := New(config.Mesh{Name: "test", Join: "k"}, func(string, ...any) {})
	h2.loadClaims()
	impostor, addr2, _ := nodeFor(t, "master")
	if _, err := h2.register(impostor, addr2, discard()); err == nil {
		t.Fatal("a name claim did not survive a hub restart, so names are stealable across one")
	}
}
