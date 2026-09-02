package config

import (
	"testing"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
)

// makeAddr builds an address the way a running node would: keys plus a relay
// region embedded as a full record.
func makeAddr(t *testing.T, regionID tailcfg.DERPRegionID) (string, tailcat.ConnInfo) {
	t.Helper()
	k := tailcat.NewPrivateKey()
	ci := tailcat.ConnInfo{
		ServerPublic:      tailcat.NodePublic{NodePublic: k.Private.Public()},
		ServerDiscoPublic: tailcat.DiscoPublicForNode(k.Private),
		Region: []*tailcfg.DERPRegion{{
			RegionID:   regionID,
			RegionCode: "test",
			Nodes:      []*tailcfg.DERPNode{{Name: "n1", RegionID: regionID, HostName: "relay.example", IPv4: "203.0.113.9"}},
		}},
	}
	return string(ci.ConnBlob()), ci
}

// The bug this guards: an embedded region's id is renumbered by tailcat and has
// nothing to do with the public relay it came from. Shortening an address by
// copying that number out produced an address naming a relay that does not
// exist, and every node that used it failed to connect with "no such region".
func TestPublicAddrUsesTheGivenRegionNotTheEmbeddedOne(t *testing.T) {
	blob, orig := makeAddr(t, 1) // as embedded: renumbered to 1
	short := PublicAddr(blob, 301)

	got, err := tailcat.ParseConnBlob(tailcat.ConnBlob(short))
	if err != nil {
		t.Fatal(err)
	}
	if got.RegionID != 301 {
		t.Fatalf("short address names relay %d, want 301", got.RegionID)
	}
	if !got.ServerPublic.Equal(orig.ServerPublic) {
		t.Fatal("short address points at a different node")
	}
	if !got.ServerDiscoPublic.Equal(orig.ServerDiscoPublic) {
		t.Fatal("short address lost the path-discovery key")
	}
	if len(short) >= len(blob) {
		t.Errorf("short address is not shorter: %d vs %d", len(short), len(blob))
	}
}

// A long address that works beats a short one that does not.
func TestPublicAddrLeavesTheAddressAloneWithoutARegion(t *testing.T) {
	blob, _ := makeAddr(t, 1)
	if got := PublicAddr(blob, 0); got != blob {
		t.Fatal("address was rewritten without knowing which relay to name")
	}
	if got := PublicAddr("not an address", 301); got != "not an address" {
		t.Fatal("an unparseable address should be passed through untouched")
	}
	if got := PublicAddr("", 301); got != "" {
		t.Fatal("an empty address should stay empty")
	}
}
