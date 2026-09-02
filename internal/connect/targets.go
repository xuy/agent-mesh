package connect

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// claudeCode registers with Claude Code through its own CLI where that exists,
// and falls back to the project-scope config file.
//
// Its user config holds session state as well as settings, so this does not
// edit it: `claude mcp add` is the supported path and knows the file's shape
// better than we do. Reaching into a tool's state file to save a subprocess is
// how you corrupt someone's history.
func claudeCode() Target {
	return Target{
		Name: "claude-code", Label: "Claude Code",
		Present: func() bool {
			_, err := exec.LookPath("claude")
			return err == nil || exists(filepath.Join(home(), ".claude"))
		},
		Connect: func(exe, node string) Result {
			r := Result{Name: "claude-code", Label: "Claude Code", Present: true}
			if _, err := exec.LookPath("claude"); err != nil {
				r.Detail = "the `claude` command is not on PATH"
				r.Manual = "claude mcp add --scope user agent-mesh -- " + exe + " mcp --name " + node
				return r
			}
			// Replace any previous registration rather than failing on it.
			exec.Command("claude", "mcp", "remove", "--scope", "user", "agent-mesh").Run()
			out, err := exec.Command("claude", "mcp", "add", "--scope", "user",
				"agent-mesh", "--", exe, "mcp", "--name", node).CombinedOutput()
			if err != nil {
				r.Detail = strings.TrimSpace(string(out))
				r.Manual = "claude mcp add --scope user agent-mesh -- " + exe + " mcp --name " + node
				return r
			}
			r.Done = true
			r.Detail = "registered with `claude mcp add`, and the join-mesh skill is installed"
			return r
		},
	}
}

// codexTarget registers with the Codex CLI and the ChatGPT desktop app, which
// share a harness and a config file.
//
// The config is TOML, so this appends a table rather than reparsing and
// rewriting the file: appending is the one edit that cannot reorder or drop
// somebody else's settings.
func codexTarget() Target {
	path := func() string { return filepath.Join(home(), ".codex", "config.toml") }
	return Target{
		Name: "codex", Label: "Codex CLI / ChatGPT desktop",
		Present: func() bool { return exists(filepath.Dir(path())) || hasBin("codex") },
		Connect: func(exe, node string) Result {
			r := Result{Name: "codex", Label: "Codex CLI / ChatGPT desktop", Present: true}
			block := fmt.Sprintf("\n[mcp_servers.agent-mesh]\ncommand = %q\nargs = [\"mcp\", \"--name\", %q]\n",
				exe, node)
			p := path()
			existing, err := os.ReadFile(p)
			if err != nil && !os.IsNotExist(err) {
				r.Detail = err.Error()
				r.Manual = block
				return r
			}
			if strings.Contains(string(existing), "[mcp_servers.agent-mesh]") {
				r.Done = true
				r.Detail = "already registered in " + p
				return r
			}
			if len(existing) > 0 {
				os.WriteFile(p+".mesh-backup", existing, 0o600)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				r.Detail = err.Error()
				r.Manual = block
				return r
			}
			f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				r.Detail = err.Error()
				r.Manual = block
				return r
			}
			defer f.Close()
			if _, err := f.WriteString(block); err != nil {
				r.Detail = err.Error()
				r.Manual = block
				return r
			}
			r.Done = true
			r.Detail = "registered in " + p
			return r
		},
	}
}

// opencodeTarget installs the skill, which is opencode's native surface, and
// prints the tool-server snippet rather than editing a JSONC file.
//
// opencode.jsonc may contain comments, and rewriting it as plain JSON would
// silently delete them. A comment someone wrote to remind themselves why a
// setting exists is worth more than saving them a paste.
func opencodeTarget() Target {
	dir := func() string { return filepath.Join(home(), ".config", "opencode") }
	return Target{
		Name: "opencode", Label: "opencode",
		Present: func() bool { return exists(dir()) || hasBin("opencode") },
		Connect: func(exe, node string) Result {
			r := Result{Name: "opencode", Label: "opencode", Present: true, Done: true,
				Detail: "the join-mesh skill is installed, which is opencode's native surface"}
			cfg := filepath.Join(dir(), "opencode.jsonc")
			if exists(cfg) {
				r.Manual = fmt.Sprintf(`  "mcp": {
    "agent-mesh": { "type": "local", "command": [%q, "mcp", "--name", %q] }
  }

  (add to %s by hand -- it may contain comments, and rewriting it as plain
   JSON would delete them)`, exe, node, cfg)
			}
			return r
		},
	}
}

func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
