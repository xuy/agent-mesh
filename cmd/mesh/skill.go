package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed SKILL.md
var skillText string

// cmdInstallSkill teaches an agent how to use the mesh before it has ever seen
// it. A skill file is the cheapest possible onboarding: the next Claude Code or
// opencode session that starts on this machine can already reach its peers.
func cmdInstallSkill(args []string) error {
	fs := flag.NewFlagSet("install-skill", flag.ExitOnError)
	dir := fs.String("dir", "", "install into this skills directory instead of the defaults")
	print := fs.Bool("print", false, "write the skill to stdout and install nothing")
	fs.Parse(args)

	if *print {
		fmt.Print(skillText)
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	targets := []string{filepath.Join(home, ".claude", "skills")}
	if *dir != "" {
		targets = []string{*dir}
	}

	primary := ""
	for _, t := range targets {
		p := filepath.Join(t, "join-mesh", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(skillText), 0o644); err != nil {
			return err
		}
		fmt.Println("installed", p)
		if primary == "" {
			primary = filepath.Dir(p)
		}
	}

	// opencode reads the same SKILL.md format and this machine already links
	// its skills across, so follow that convention rather than duplicating.
	if *dir == "" {
		oc := filepath.Join(home, ".config", "opencode", "skills")
		if st, err := os.Stat(oc); err == nil && st.IsDir() {
			link := filepath.Join(oc, "join-mesh")
			if _, err := os.Lstat(link); err != nil {
				if err := os.Symlink(primary, link); err == nil {
					fmt.Println("linked  ", link)
				}
			}
		}
	}

	fmt.Println("\nA new agent session on this machine can now find its peers on its own.")
	fmt.Println("To give an agent the mesh as native tools instead of shell commands,")
	fmt.Println("add this MCP server to its config:")
	self, _ := os.Executable()
	fmt.Printf("\n  \"agent-mesh\": { \"type\": \"local\", \"command\": [\"%s\", \"mcp\"] }\n", self)
	return nil
}
