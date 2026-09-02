package config

import (
	"context"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
)

// PickRegion measures which public DERP relay is nearest and returns its region
// id -- 301 for New York, 302 for San Francisco, and so on.
//
// A node has to know this number for two reasons, and neither can be recovered
// from an address after the fact. tailcat renumbers the region to 1 when it
// embeds the region's record in an address, so parsing one back tells you
// nothing about which public relay it was. Knowing the real number lets a node
// pin its relay across restarts, which keeps its address stable, and lets an
// invite name the relay by number instead of carrying its whole record.
func PickRegion(ctx context.Context) (int, error) {
	dm, err := tailcat.FetchDERPMap(ctx, tailcat.ExpandForServer)
	if err != nil {
		return 0, err
	}
	id, err := tailcat.PickBestRegion(ctx, dm)
	return int(id), err
}

// PublicAddr returns the shortest form of a node's address: its keys plus the
// number of the relay it bootstraps through, leaving the joining side to look
// that number up. It is roughly half the length of the embedded form, which
// matters only because a person carries this between two machines.
//
// Without a known region it returns the address unchanged. A long address that
// works beats a short one that does not, and "no such region" is a miserable
// error to debug.
func PublicAddr(blob string, region int) string {
	if region == 0 || blob == "" {
		return blob
	}
	ci, err := tailcat.ParseConnBlob(tailcat.ConnBlob(blob))
	if err != nil {
		return blob
	}
	short := tailcat.ConnInfo{
		ServerPublic:      ci.ServerPublic,
		ServerDiscoPublic: ci.ServerDiscoPublic,
		RegionID:          tailcfg.DERPRegionID(region),
	}
	return string(short.ConnBlob())
}
