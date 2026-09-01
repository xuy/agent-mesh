// Package ident holds a node's cryptographic identity and the addressing rules
// that let one node prove which peer is calling it.
package ident

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"tailscale.com/types/key"
)

// Identity is a node's permanent identity on the mesh: two WireGuard node keys.
//
// Two, not one, because a DERP relay keys its clients by node public key and
// admits one connection per key. A process that both serves (accepting peers)
// and dials (reaching peers) holds two DERP connections at once, so it needs
// two keys or the second connection evicts the first.
//
// Server is the node's address: its tailcat ConnBlob is derived from it, and
// peers dial it. Client is the node's caller identity: peers allowlist it and
// use it to attribute inbound connections. Both are published in the roster;
// both are permanent, generated once by `mesh init`.
type Identity struct {
	Server key.NodePrivate
	Client key.NodePrivate
}

type identityFile struct {
	Server string `json:"server"`
	Client string `json:"client"`
}

// New generates a fresh identity.
func New() *Identity {
	return &Identity{Server: key.NewNode(), Client: key.NewNode()}
}

// Save writes the identity to path with owner-only permissions.
func (id *Identity) Save(path string) error {
	sp, err := id.Server.MarshalText()
	if err != nil {
		return err
	}
	cp, err := id.Client.MarshalText()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(identityFile{Server: string(sp), Client: string(cp)}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Load reads an identity previously written by Save.
func Load(path string) (*Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f identityFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	id := &Identity{}
	if err := id.Server.UnmarshalText([]byte(f.Server)); err != nil {
		return nil, fmt.Errorf("%s: server key: %w", path, err)
	}
	if err := id.Client.UnmarshalText([]byte(f.Client)); err != nil {
		return nil, fmt.Errorf("%s: client key: %w", path, err)
	}
	return id, nil
}

// PubText returns the text form of a public key, as published in the roster.
func PubText(k key.NodePublic) string {
	b, err := k.MarshalText()
	if err != nil {
		return ""
	}
	return string(b)
}

// ParsePub parses the text form written by PubText.
func ParsePub(s string) (key.NodePublic, error) {
	var k key.NodePublic
	err := k.UnmarshalText([]byte(s))
	return k, err
}

// Addr returns the tunnel address a node key answers on.
//
// It reproduces tailcat's derivation: Tailscale's ULA range fd7a:115c:a1e0::/48
// with the low 80 bits taken from the node public key. This is what makes
// sender attribution free — the remote address of an accepted connection maps
// back to exactly one peer, with no in-band handshake.
//
// Only 80 bits of the key appear in the address, which is not enough to be a
// credential on its own. It does not need to be: WireGuard already refused the
// tunnel unless the peer's *full* public key was on the allowlist, so the
// address is only disambiguating among peers that are already authenticated.
func Addr(k key.NodePublic) netip.Addr {
	var a [16]byte
	r := k.Raw32()
	a[0], a[1], a[2], a[3], a[4], a[5] = 0xfd, 0x7a, 0x11, 0x5c, 0xa1, 0xe0
	copy(a[6:], r[:10])
	return netip.AddrFrom16(a)
}

// Info is a node's public entry in the mesh roster: the record that turns a
// name into something dialable.
type Info struct {
	Name string `json:"name"`
	Mesh string `json:"mesh,omitempty"`

	// Blob is the node's tailcat ConnBlob. It is regenerated every time the
	// daemon starts (it embeds the DERP region), which is exactly why the
	// control plane exists: names are stable, addresses are not.
	Blob string `json:"blob"`

	ServerPub string `json:"server_pub"`
	ClientPub string `json:"client_pub"`

	// Agent names the software behind the node ("claude-code", "opencode"),
	// and Kinds lists what it will answer to. Both are advisory: they let an
	// agent pick a peer to ask without a human explaining the mesh.
	Agent string   `json:"agent,omitempty"`
	Kinds []string `json:"kinds,omitempty"`
	Note  string   `json:"note,omitempty"`

	// Started is when this node's daemon came up. It is what tells a peer the
	// node restarted: addresses are deliberately stable across restarts, so a
	// cached tunnel would otherwise look valid while pointing at a dead
	// WireGuard session.
	Started time.Time `json:"started,omitzero"`

	Seen   time.Time `json:"seen,omitzero"`
	Online bool      `json:"online,omitempty"`
}

// ClientAddr returns the tunnel address this peer calls other nodes from.
func (i Info) ClientAddr() (netip.Addr, error) {
	k, err := ParsePub(i.ClientPub)
	if err != nil {
		return netip.Addr{}, err
	}
	return Addr(k), nil
}
