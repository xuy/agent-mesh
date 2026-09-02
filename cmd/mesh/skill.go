package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xuy/agent-mesh/internal/connect"
	"github.com/xuy/agent-mesh/internal/service"
)

//go:embed SKILL.md
var skillText string

// cmdInstallSkill teaches an agent how to use the mesh before it has ever seen
// it. A skill file is the cheapest possible onboarding: the next session that
// starts on this machine can already reach its peers.
func cmdInstallSkill(args []string) error {
	fs := flag.NewFlagSet("install-skill", flag.ExitOnError)
	dir := fs.String("dir", "", "install into this skills directory instead of the defaults")
	print := fs.Bool("print", false, "write the skill to stdout and install nothing")
	fs.Parse(hoistFlags(fs, args))

	if *print {
		fmt.Print(skillText)
		return nil
	}
	if *dir != "" {
		return installSkillTo(*dir)
	}
	if err := installSkillFiles(); err != nil {
		return err
	}

	fmt.Println("\nA new agent session on this machine can now find its peers on its own.")
	fmt.Println("`mesh connect` goes further and registers the mesh as a tool server with")
	fmt.Println("the desktop and editor harnesses installed here.")

	exe, err := service.Executable()
	if err == nil {
		nm, nerr := resolveName("")
		if nerr != nil {
			nm = "your-node"
		}
		fmt.Println("\nTo register one by hand:")
		fmt.Println()
		fmt.Println(connect.Snippet(exe, nm))
	}
	return nil
}

// installSkillFiles writes the skill where the harnesses on this machine look.
func installSkillFiles() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := installSkillTo(claudeSkills); err != nil {
		return err
	}
	// opencode reads the same format, and machines that use both usually link
	// the directories rather than keeping two copies; follow that.
	oc := filepath.Join(home, ".config", "opencode", "skills")
	if st, err := os.Stat(oc); err == nil && st.IsDir() {
		link := filepath.Join(oc, "join-mesh")
		if _, err := os.Lstat(link); err != nil {
			os.Symlink(filepath.Join(claudeSkills, "join-mesh"), link)
		}
	}
	return nil
}

func installSkillTo(dir string) error {
	p := filepath.Join(dir, "join-mesh", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(skillText), 0o644); err != nil {
		return err
	}
	fmt.Println("installed", p)
	return nil
}
