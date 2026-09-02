// Package config owns everything on disk: where a node's identity, settings,
// roster cache and inbox live, and how a mesh is described in one pasteable
// string.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
)

// Home is the root of all mesh state. Override with MESH_HOME.
func Home() string {
	if h := os.Getenv("MESH_HOME"); h != "" {
		return h
	}
	return defaultHome()
}

// Layout. Several agents share one machine, so every node gets its own
// directory and they share one description of the mesh they belong to.
func MeshPath() string        { return filepath.Join(Home(), "mesh.json") }
func CurrentPath() string     { return filepath.Join(Home(), "current") }
func HubDir() string          { return filepath.Join(Home(), "hub") }
func NodesDir() string        { return filepath.Join(Home(), "nodes") }
func NodeDir(n string) string { return filepath.Join(NodesDir(), n) }

func IdentityPath(n string) string { return filepath.Join(NodeDir(n), "node.key") }
func NodePath(n string) string     { return filepath.Join(NodeDir(n), "node.json") }
func RosterPath(n string) string   { return filepath.Join(NodeDir(n), "roster.json") }
func InboxPath(n string) string    { return filepath.Join(NodeDir(n), "inbox.jsonl") }
func LogPath(n string) string      { return filepath.Join(NodeDir(n), "daemon.log") }
func PeersPath(n string) string    { return filepath.Join(NodeDir(n), "peers.json") }
func AuditPath(n string) string    { return filepath.Join(NodeDir(n), "audit.jsonl") }
func CursorPath(n string) string   { return filepath.Join(NodeDir(n), "read-cursor") }
func PIDPath(n string) string      { return filepath.Join(NodeDir(n), "daemon.pid") }

// SockPath is the node daemon's local control socket: how the CLI reaches the
// running daemon without paying a tunnel setup of its own.
//
// It lives under the OS temp dir rather than in the node directory because a
// unix socket path has a hard ~104-byte limit on macOS and MESH_HOME may be
// deep. The name includes the home path's hash so two meshes on one machine
// never collide.
func SockPath(n string) string {
	h := fnv.New64a()
	h.Write([]byte(Home()))
	return filepath.Join(socketDir(), fmt.Sprintf("mesh-%08x-%s.sock", h.Sum64()&0xffffffff, n))
}

// Mesh describes the mesh a node belongs to. It is written once per machine by
// `mesh hub` or `mesh join`, so a second agent on the same machine joins with
// nothing but a name.
type Mesh struct {
	Name string `json:"name"`           // the mesh's name, e.g. "noah"
	Hub  string `json:"hub"`            // the hub's tailcat ConnBlob
	Join string `json:"join"`           // shared secret authorizing registration
	Note string `json:"note,omitempty"` // free text shown to joining agents

	// Coordinator names the local node that runs the control plane inside its
	// own daemon. Empty means the mesh uses a standalone `mesh hub`.
	Coordinator string `json:"coordinator,omitempty"`
}

const invitePrefix = "am1."

// Invite encodes the mesh into one string a person can carry to another
// machine. It is a secret: it carries the join key.
//
// The format is "am1.<mesh>.<address>.<joinkey>" rather than base64-of-JSON,
// which inflates by a third and hides what the thing is. Two fields are
// deliberately absent:
//
//   - Note, because it is decoration and the coordinator can say it later.
//   - Coordinator, because it names a node on *this* machine; carrying it
//     across would make a node on the far machine that happens to share the
//     name believe it coordinates the mesh, and publish its own address as the
//     one everyone should join.
//
// The address is already in its short form: a coordinator publishes it that
// way, naming its relay by number rather than embedding the relay's record.
func (m Mesh) Invite() string {
	return invitePrefix + SanitizeMeshName(m.Name) + "." + m.Hub + "." + m.Join
}

// ParseInvite decodes a string produced by Invite.
func ParseInvite(s string) (Mesh, error) {
	var m Mesh
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, invitePrefix) {
		return m, fmt.Errorf("that is not a mesh invite (it should start with %q)", invitePrefix)
	}
	parts := strings.Split(strings.TrimPrefix(s, invitePrefix), ".")
	if len(parts) != 3 {
		return m, fmt.Errorf("that invite is truncated or has extra text around it")
	}
	m.Name, m.Hub, m.Join = parts[0], parts[1], parts[2]
	if m.Hub == "" || m.Join == "" {
		return m, fmt.Errorf("that invite is missing the mesh address or its join key")
	}
	if !strings.HasPrefix(m.Hub, "tc") {
		return m, fmt.Errorf("that invite does not contain a mesh address")
	}
	return m, nil
}

// SanitizeMeshName reduces a mesh name to what can travel in an invite.
func SanitizeMeshName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "mesh"
	}
	return out
}

// NewJoinKey returns a fresh join secret.
//
// Twelve bytes, not more: it is carried by hand, and it is the second factor
// behind an address that is already unguessable, so length past this buys
// nothing and costs the person typing it.
func NewJoinKey() string {
	var b [12]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// LoadMesh reads the machine's mesh description.
func LoadMesh() (Mesh, error) {
	var m Mesh
	b, err := os.ReadFile(MeshPath())
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// SaveMesh writes the machine's mesh description.
func (m Mesh) Save() error { return writeJSON(MeshPath(), m, 0o600) }

// Node is one agent's settings.
type Node struct {
	Name  string   `json:"name"`
	Mesh  string   `json:"mesh,omitempty"`
	Agent string   `json:"agent,omitempty"` // "claude-code", "opencode", ...
	Kinds []string `json:"kinds,omitempty"`
	Note  string   `json:"note,omitempty"`

	// Adapter is this node's delivery mode -- how a question reaches whoever
	// answers it:
	//
	//   mailbox  park it for `mesh reply` (the default; works for anything)
	//   exec     run Exec and stream its stdout back (a fresh agent session)
	//   webhook  POST to a resident agent's local API (a live session)
	//   notify   park it, but run Exec first so someone notices
	Adapter string `json:"adapter,omitempty"`
	Exec    string `json:"exec,omitempty"`

	// RatePerMinute caps how fast one peer may send to this node. Zero means
	// the default; a negative number means no limit.
	RatePerMinute int `json:"rate_per_minute,omitempty"`

	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookHeader string `json:"webhook_header,omitempty"`
	WebhookAsync  bool   `json:"webhook_async,omitempty"`

	// Region pins the DERP relay this node bootstraps through, chosen by the
	// first start's latency check. A node's address encodes its relay, so
	// without pinning the address would change on every restart -- which would
	// invalidate every invite handed out by a coordinator, and stale every
	// peer's cached roster entry.
	Region int `json:"region,omitempty"`
}

// LoadNode reads a node's settings.
func LoadNode(name string) (Node, error) {
	var n Node
	b, err := os.ReadFile(NodePath(name))
	if err != nil {
		return n, err
	}
	return n, json.Unmarshal(b, &n)
}

// Save writes a node's settings.
func (n Node) Save() error { return writeJSON(NodePath(n.Name), n, 0o600) }

// Current returns the default node name for CLI commands: $MESH_NAME, else the
// last node created or joined on this machine.
func Current() string {
	if n := os.Getenv("MESH_NAME"); n != "" {
		return n
	}
	b, err := os.ReadFile(CurrentPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetCurrent records the default node name.
func SetCurrent(name string) error {
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(CurrentPath(), []byte(name+"\n"), 0o600)
}

func writeJSON(path string, v any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteJSON writes v to path atomically with owner-only permissions.
func WriteJSON(path string, v any) error { return writeJSON(path, v, 0o600) }
