package ident

import (
	"path/filepath"
	"testing"

	"tailscale.com/types/key"
)

func TestIdentitySaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.key")
	want := New()
	if err := want.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Server.Equal(want.Server) || !got.Client.Equal(want.Client) {
		t.Fatal("identity did not survive a save/load round trip")
	}
}

func TestServerAndClientKeysDiffer(t *testing.T) {
	// A node holds two keys because a DERP relay admits one connection per
	// key; if these were ever the same, serving would evict dialing.
	id := New()
	if id.Server.Equal(id.Client) {
		t.Fatal("a node's serving and dialing keys must differ")
	}
}

// TestAddrMatchesTailcatDerivation pins the address derivation that sender
// attribution depends on: Tailscale's ULA prefix with the low 80 bits taken
// from the node key. If tailcat ever changes this, peers would still connect
// but every inbound message would be attributed to nobody.
func TestAddrMatchesTailcatDerivation(t *testing.T) {
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	pub := key.NodePublicFromRaw32(memRO(raw))
	got := Addr(pub)

	if !got.Is6() {
		t.Fatalf("address is not IPv6: %v", got)
	}
	b := got.As16()
	wantPrefix := []byte{0xfd, 0x7a, 0x11, 0x5c, 0xa1, 0xe0}
	for i, w := range wantPrefix {
		if b[i] != w {
			t.Fatalf("wrong ULA prefix at byte %d: got %#x want %#x (addr %v)", i, b[i], w, got)
		}
	}
	for i := 0; i < 10; i++ {
		if b[6+i] != raw[i] {
			t.Fatalf("byte %d of the address is not key byte %d: got %#x want %#x", 6+i, i, b[6+i], raw[i])
		}
	}
}

func TestAddrIsStableAndDistinct(t *testing.T) {
	a, b := New(), New()
	if Addr(a.Client.Public()) != Addr(a.Client.Public()) {
		t.Fatal("the same key produced two addresses")
	}
	if Addr(a.Client.Public()) == Addr(b.Client.Public()) {
		t.Fatal("two different keys produced the same address")
	}
}

func TestPubTextRoundTrip(t *testing.T) {
	id := New()
	got, err := ParsePub(PubText(id.Client.Public()))
	if err != nil {
		t.Fatal(err)
	}
	if got != id.Client.Public() {
		t.Fatal("public key did not survive the roster's text encoding")
	}
}
