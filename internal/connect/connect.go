// Package connect registers the mesh with the agent harnesses installed on
// this machine.
//
// This is where the promise of "any agent" is either kept or not. A mesh that
// works beautifully once you have hand-edited four JSON files in three formats
// is a mesh nobody joins. Every harness here is one someone actually runs, and
// each gets the best integration it can accept rather than the same one
// everywhere -- a skill where skills exist, a tool server where that is all
// there is.
//
// The rule throughout: never damage a config we do not fully understand. A
// file we can parse is updated in place with a backup beside it; a file we
// cannot is left alone and the exact snippet is printed instead. Corrupting
// someone's editor config to save them a copy and paste is not a trade worth
// making.
package connect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Result is what happened for one harness.
type Result struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Present bool   `json:"present"`
	Done    bool   `json:"done"`
	Detail  string `json:"detail"`
	// Manual is a snippet for the person to paste, when we would not edit the
	// file safely ourselves.
	Manual string `json:"manual,omitempty"`
}

// Target is one harness we know how to register with.
type Target struct {
	Name  string
	Label string
	// Present reports whether this harness looks installed.
	Present func() bool
	// Connect registers the mesh. exe is the absolute path to this binary and
	// node is the mesh node the harness should act as.
	Connect func(exe, node string) Result
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// serverEntry is the MCP server definition nearly every harness shares.
func serverEntry(exe, node string) map[string]any {
	return map[string]any{
		"command": exe,
		"args":    []any{"mcp", "--name", node},
	}
}

// Snippet renders the config a person would paste by hand.
func Snippet(exe, node string) string {
	b, _ := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{"agent-mesh": serverEntry(exe, node)},
	}, "", "  ")
	return string(b)
}

// mergeJSONServer adds the mesh to a JSON config under the given key path,
// leaving everything else in the file untouched.
//
// It refuses rather than guesses: a file that does not parse is not rewritten,
// because the alternative is destroying a config someone spent an afternoon on
// to save them one paste.
func mergeJSONServer(path string, section string, exe, node string) (bool, string, error) {
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(b))) > 0 {
			if err := json.Unmarshal(b, &cfg); err != nil {
				return false, "", fmt.Errorf("%s is not plain JSON this can edit safely", filepath.Base(path))
			}
		}
		// Keep a copy before touching anything a person configured by hand.
		os.WriteFile(path+".mesh-backup", b, 0o600)
	} else if !os.IsNotExist(err) {
		return false, "", err
	}

	servers, _ := cfg[section].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	before, _ := json.Marshal(servers["agent-mesh"])
	servers["agent-mesh"] = serverEntry(exe, node)
	after, _ := json.Marshal(servers["agent-mesh"])
	cfg[section] = servers

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, "", err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, "", err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, "", err
	}
	changed := string(before) != string(after)
	return changed, path, nil
}

func jsonTarget(name, label, section string, paths func() string) Target {
	return Target{
		Name: name, Label: label,
		Present: func() bool { return exists(paths()) || exists(filepath.Dir(paths())) },
		Connect: func(exe, node string) Result {
			p := paths()
			changed, where, err := mergeJSONServer(p, section, exe, node)
			if err != nil {
				return Result{Name: name, Label: label, Present: true,
					Detail: err.Error(), Manual: Snippet(exe, node)}
			}
			detail := "already registered in " + where
			if changed {
				detail = "registered in " + where
			}
			return Result{Name: name, Label: label, Present: true, Done: true, Detail: detail}
		},
	}
}

// Targets are the harnesses this machine might have, in the order a person
// would most likely care about them.
func Targets() []Target {
	return []Target{
		claudeCode(),
		jsonTarget("claude-desktop", "Claude Desktop", "mcpServers", claudeDesktopConfig),
		codexTarget(),
		jsonTarget("cursor", "Cursor", "mcpServers", func() string {
			return filepath.Join(home(), ".cursor", "mcp.json")
		}),
		jsonTarget("gemini-cli", "Gemini CLI", "mcpServers", func() string {
			return filepath.Join(home(), ".gemini", "settings.json")
		}),
		jsonTarget("zed", "Zed", "context_servers", func() string {
			return filepath.Join(home(), ".config", "zed", "settings.json")
		}),
		opencodeTarget(),
		openclawTarget(),
	}
}

func claudeDesktopConfig() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home(), "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "Claude", "claude_desktop_config.json")
		}
	}
	return filepath.Join(home(), ".config", "Claude", "claude_desktop_config.json")
}
