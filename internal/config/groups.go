package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Groups are named sets of peers, kept per node.
//
// Local rather than shared on purpose: a group is one agent's view of who it
// works with, not a fact about the mesh. Keeping them local means creating one
// needs no coordination, no agreement, and no round trip -- and two agents can
// disagree about what "builders" means without either being wrong.
type Groups map[string][]string

// GroupsPath is where a node keeps its groups.
func GroupsPath(n string) string { return NodeDir(n) + "/groups.json" }

// AllGroup is the group every peer belongs to, without anyone declaring it.
const AllGroup = "all"

// LoadGroups reads a node's groups.
func LoadGroups(node string) (Groups, error) {
	g := Groups{}
	b, err := os.ReadFile(GroupsPath(node))
	if err != nil {
		if os.IsNotExist(err) {
			return g, nil
		}
		return g, err
	}
	return g, json.Unmarshal(b, &g)
}

// Save writes a node's groups.
func (g Groups) Save(node string) error { return WriteJSON(GroupsPath(node), g) }

// IsGroup reports whether an address names a group rather than a peer.
func IsGroup(addr string) bool { return strings.HasPrefix(addr, "@") }

// GroupName strips the marker from a group address.
func GroupName(addr string) string { return strings.TrimPrefix(addr, "@") }

// Members resolves a group address to peer names. everyone is the current
// roster, used for the built-in "all" group and to drop members that have
// since left the mesh.
func (g Groups) Members(addr string, everyone []string) ([]string, error) {
	name := GroupName(addr)
	if name == AllGroup {
		return append([]string{}, everyone...), nil
	}
	members, ok := g[name]
	if !ok {
		return nil, fmt.Errorf("no group called %q -- `mesh group` lists them, `mesh group add %s <peer>` creates one", name, name)
	}
	known := map[string]bool{}
	for _, p := range everyone {
		known[p] = true
	}
	var out, missing []string
	for _, m := range members {
		if known[m] {
			out = append(out, m)
		} else {
			missing = append(missing, m)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no member of %q is on the mesh right now (%s)", name, strings.Join(missing, ", "))
	}
	sort.Strings(out)
	return out, nil
}

// Add puts peers in a group, creating it if needed.
func (g Groups) Add(name string, peers ...string) {
	has := map[string]bool{}
	for _, p := range g[name] {
		has[p] = true
	}
	for _, p := range peers {
		if !has[p] {
			g[name] = append(g[name], p)
			has[p] = true
		}
	}
	sort.Strings(g[name])
}

// Remove takes peers out of a group, deleting the group if it empties. With no
// peers named, the whole group goes.
func (g Groups) Remove(name string, peers ...string) {
	if len(peers) == 0 {
		delete(g, name)
		return
	}
	drop := map[string]bool{}
	for _, p := range peers {
		drop[p] = true
	}
	var kept []string
	for _, m := range g[name] {
		if !drop[m] {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		delete(g, name)
		return
	}
	g[name] = kept
}

// Names lists the groups, sorted.
func (g Groups) Names() []string {
	out := make([]string, 0, len(g))
	for n := range g {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
