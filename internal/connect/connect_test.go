package connect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sandbox(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("APPDATA", filepath.Join(h, "AppData", "Roaming"))
	return h
}

func TestRegisteringInAJSONConfigLeavesTheRestAlone(t *testing.T) {
	h := sandbox(t)
	p := filepath.Join(h, ".cursor", "mcp.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	// Something the person configured that we must not disturb.
	os.WriteFile(p, []byte(`{"mcpServers":{"notion":{"command":"notion-mcp"}},"theme":"dark"}`), 0o644)

	if _, _, err := mergeJSONServer(p, "mcpServers", "/usr/local/bin/mesh", "master"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" {
		t.Error("an unrelated setting was lost")
	}
	servers := got["mcpServers"].(map[string]any)
	if _, ok := servers["notion"]; !ok {
		t.Error("another tool server was removed")
	}
	mesh, ok := servers["agent-mesh"].(map[string]any)
	if !ok {
		t.Fatal("the mesh was not registered")
	}
	if mesh["command"] != "/usr/local/bin/mesh" {
		t.Errorf("wrong command: %v", mesh["command"])
	}
}

// Running it twice must not add a second entry or report a change it did not
// make, because people re-run setup commands constantly.
func TestRegisteringTwiceIsIdempotent(t *testing.T) {
	h := sandbox(t)
	p := filepath.Join(h, ".gemini", "settings.json")

	first, _, err := mergeJSONServer(p, "mcpServers", "/bin/mesh", "master")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Error("the first registration reported no change")
	}
	second, _, err := mergeJSONServer(p, "mcpServers", "/bin/mesh", "master")
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Error("re-running reported a change it did not make")
	}
}

// The rule that matters most: a config we cannot parse is never rewritten.
// Corrupting someone's editor config to save them a paste is not a trade worth
// making.
func TestAConfigWeCannotParseIsLeftUntouched(t *testing.T) {
	h := sandbox(t)
	p := filepath.Join(h, ".cursor", "mcp.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	original := "{\n  // a comment, which is not plain JSON\n  \"mcpServers\": {}\n}\n"
	os.WriteFile(p, []byte(original), 0o644)

	if _, _, err := mergeJSONServer(p, "mcpServers", "/bin/mesh", "master"); err == nil {
		t.Fatal("a file we cannot parse was rewritten anyway")
	}
	after, _ := os.ReadFile(p)
	if string(after) != original {
		t.Fatalf("the file was modified despite the failure:\n%s", after)
	}
}

func TestABackupIsKeptBeforeEditing(t *testing.T) {
	h := sandbox(t)
	p := filepath.Join(h, ".gemini", "settings.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"existing":true}`), 0o644)

	if _, _, err := mergeJSONServer(p, "mcpServers", "/bin/mesh", "master"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p + ".mesh-backup")
	if err != nil {
		t.Fatal("no backup was kept before editing a config someone wrote by hand")
	}
	if !strings.Contains(string(b), `"existing":true`) {
		t.Errorf("the backup does not hold the original: %s", b)
	}
}

func TestCodexAppendsAndDoesNotDuplicate(t *testing.T) {
	h := sandbox(t)
	p := filepath.Join(h, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("model = \"gpt-5\"\n"), 0o644)

	tgt := codexTarget()
	if r := tgt.Connect("/bin/mesh", "master"); !r.Done {
		t.Fatalf("first registration failed: %+v", r)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `model = "gpt-5"`) {
		t.Error("an existing setting was lost from a TOML config")
	}
	if !strings.Contains(string(b), "[mcp_servers.agent-mesh]") {
		t.Error("the mesh was not registered")
	}
	if r := tgt.Connect("/bin/mesh", "master"); !r.Done {
		t.Fatalf("re-running failed: %+v", r)
	}
	b2, _ := os.ReadFile(p)
	if strings.Count(string(b2), "[mcp_servers.agent-mesh]") != 1 {
		t.Errorf("re-running duplicated the entry:\n%s", b2)
	}
}

func TestEveryTargetIsAnsweredEvenWhenAbsent(t *testing.T) {
	sandbox(t)
	// A machine with nothing installed must still get a usable answer rather
	// than silence.
	for _, tgt := range Targets() {
		if tgt.Name == "" || tgt.Label == "" {
			t.Errorf("a target has no name or label: %+v", tgt)
		}
		if tgt.Present == nil || tgt.Connect == nil {
			t.Errorf("%s is not fully defined", tgt.Name)
		}
	}
	if s := Snippet("/bin/mesh", "master"); !strings.Contains(s, "agent-mesh") || !strings.Contains(s, "/bin/mesh") {
		t.Errorf("the hand-editing snippet is not usable: %s", s)
	}
}
