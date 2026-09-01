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
	d, err := os.UserHomeDir()
	if err != nil {
		return ".agent-mesh"
	}
	return filepath.Join(d, ".agent-mesh")
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
	return filepath.Join(os.TempDir(), fmt.Sprintf("mesh-%08x-%s.sock", h.Sum64()&0xffffffff, n))
}

// Mesh describes the mesh a node belongs to. It is written once per machine by
// `mesh hub` or `mesh join`, so a second agent on the same machine joins with
// nothing but a name.
type Mesh struct {
	Name string `json:"name"`           // the mesh's name, e.g. "noah"
	Hub  string `json:"hub"`            // the hub's tailcat ConnBlob
	Join string `json:"join"`           // shared secret authorizing registration
	Note string `json:"note,omitempty"` // free text shown to joining agents
}

const invitePrefix = "am1_"

// Invite encodes the mesh into one pasteable string. It is the only thing a
// remote agent needs to join, and it is a secret: it carries the join key.
func (m Mesh) Invite() string {
	b, _ := json.Marshal(m)
	return invitePrefix + base64.RawURLEncoding.EncodeToString(b)
}

// ParseInvite decodes a string produced by Invite.
func ParseInvite(s string) (Mesh, error) {
	var m Mesh
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, invitePrefix) {
		return m, fmt.Errorf("not a mesh invite (want %s...)", invitePrefix)
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, invitePrefix))
	if err != nil {
		return m, fmt.Errorf("corrupt invite: %w", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("corrupt invite: %w", err)
	}
	if m.Hub == "" || m.Join == "" {
		return m, fmt.Errorf("invite is missing the hub address or join key")
	}
	return m, nil
}

// NewJoinKey returns a fresh join secret.
func NewJoinKey() string {
	var b [24]byte
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

	// Adapter decides how an inbound ask is answered: "mailbox" parks it for
	// the local agent to answer by hand, "exec" runs Exec and streams stdout.
	Adapter string `json:"adapter,omitempty"`
	Exec    string `json:"exec,omitempty"`
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
