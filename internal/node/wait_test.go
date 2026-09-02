package node

import (
	"testing"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/ident"
	"github.com/xuy/agent-mesh/internal/wire"
)

func testNode(t *testing.T) *Node {
	t.Helper()
	t.Setenv("MESH_HOME", t.TempDir())
	return New(config.Node{Name: "me"}, config.Mesh{Name: "t"}, ident.New(), nil)
}

func TestSubscribeDeliversInboundMessages(t *testing.T) {
	n := testNode(t)
	ch, release := n.Subscribe()
	defer release()

	n.notifyWaiters(wire.Envelope{ID: "1", From: "peer", Kind: wire.KindTell, Body: "hello"})
	select {
	case got := <-ch:
		if got.Body != "hello" || got.From != "peer" {
			t.Fatalf("wrong message delivered: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a waiting agent was never told a message arrived")
	}
}

func TestReleaseStopsDelivery(t *testing.T) {
	n := testNode(t)
	ch, release := n.Subscribe()
	release()
	n.notifyWaiters(wire.Envelope{ID: "1", From: "peer", Body: "hello"})
	select {
	case <-ch:
		t.Fatal("a released subscription still received a message")
	case <-time.After(100 * time.Millisecond):
	}
}

// A watcher that has stopped reading must not be able to wedge the node: the
// mesh answering its peers matters more than one slow observer.
func TestSlowWatcherCannotBlockTheNode(t *testing.T) {
	n := testNode(t)
	_, release := n.Subscribe()
	defer release()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			n.notifyWaiters(wire.Envelope{ID: "x", From: "peer"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a watcher that stopped reading blocked the node")
	}
}

func TestSinceReportsOnlyNewerInboundMessages(t *testing.T) {
	n := testNode(t)
	older := wire.NewID()
	time.Sleep(2 * time.Millisecond)
	newer := wire.NewID()

	n.appendInbox(wire.Envelope{ID: older, From: "peer", Kind: wire.KindTell, Body: "old", TS: time.Now()})
	n.appendInbox(wire.Envelope{ID: newer, From: "peer", Kind: wire.KindTell, Body: "new", TS: time.Now()})
	// Our own outbound messages are not news to us.
	n.appendInbox(wire.Envelope{ID: wire.NewID(), From: "me", Kind: wire.KindTell, Body: "mine", TS: time.Now()})

	got := n.since(older)
	if len(got) != 1 || got[0].Body != "new" {
		t.Fatalf("since(%q) returned %d messages, want just the newer one: %+v", older, len(got), got)
	}
}

func TestMultipleWatchersAllHear(t *testing.T) {
	n := testNode(t)
	a, ra := n.Subscribe()
	defer ra()
	b, rb := n.Subscribe()
	defer rb()

	n.notifyWaiters(wire.Envelope{ID: "1", From: "peer", Body: "hi"})
	for i, ch := range []<-chan wire.Envelope{a, b} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("watcher %d missed the message", i)
		}
	}
}

// A coordinator builds its roster from live sessions, so it reports an empty
// mesh until everyone reconnects after a restart. Forgetting peers in that
// window makes us reject their messages as strangers.
func TestKnownPeersSurviveAnEmptyRoster(t *testing.T) {
	n := testNode(t)
	peer := ident.New()
	info := ident.Info{
		Name:      "windows",
		Blob:      "tcoADDR",
		ClientPub: ident.PubText(peer.Client.Public()),
		ServerPub: ident.PubText(peer.Server.Public()),
	}
	n.applyRoster([]ident.Info{info})

	addr, err := info.ClientAddr()
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := n.peerAt(addr); !ok || name != "windows" {
		t.Fatal("peer was not recognised after joining")
	}

	n.applyRoster(nil) // the coordinator restarted

	if name, ok := n.peerAt(addr); !ok || name != "windows" {
		t.Fatal("a known peer became a stranger when the coordinator restarted")
	}
	peers := n.Peers()
	if len(peers) != 1 {
		t.Fatalf("peer vanished from the roster: %+v", peers)
	}
	if peers[0].Online {
		t.Error("a peer that is not in the live roster should show as offline")
	}
}

func TestRosterMarksReturningPeersOnline(t *testing.T) {
	n := testNode(t)
	info := ident.Info{Name: "windows", Blob: "tcoADDR", ClientPub: ident.PubText(ident.New().Client.Public())}
	n.applyRoster([]ident.Info{info})
	n.applyRoster(nil)
	n.applyRoster([]ident.Info{info})
	if p := n.Peers(); len(p) != 1 || !p[0].Online {
		t.Fatalf("a peer that came back is not shown as online: %+v", p)
	}
}
