package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xuy/agent-mesh/internal/connect"
	"github.com/xuy/agent-mesh/internal/service"
)

// cmdConnect registers the mesh with every agent harness on this machine.
//
// This is the command that decides whether "works with your agents" is true or
// marketing. It does what it can safely, says plainly what it could not, and
// prints the exact thing to paste for the rest.
func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	name := fs.String("name", "", "the node these harnesses should act as")
	only := fs.String("only", "", "just this harness (see `mesh connect --list`)")
	list := fs.Bool("list", false, "show what is installed here and do nothing")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(hoistFlags(fs, args))

	nm, err := resolveName(*name)
	if err != nil {
		return err
	}
	exe, err := service.Executable()
	if err != nil {
		return err
	}

	targets := connect.Targets()
	if *list {
		if *asJSON {
			var out []connect.Result
			for _, t := range targets {
				out = append(out, connect.Result{Name: t.Name, Label: t.Label, Present: t.Present()})
			}
			return printJSON(out)
		}
		for _, t := range targets {
			mark := "not installed"
			if t.Present() {
				mark = "installed"
			}
			fmt.Printf("%-14s %-30s %s\n", t.Name, t.Label, mark)
		}
		return nil
	}

	// The skill is how a harness with no tool-server support still learns the
	// mesh, and it costs nothing to install for the ones that do.
	if err := installSkillFiles(); err != nil {
		fmt.Fprintf(os.Stderr, "note: could not install the join-mesh skill: %v\n", err)
	}

	var results []connect.Result
	found := 0
	for _, t := range targets {
		if *only != "" && t.Name != *only {
			continue
		}
		if !t.Present() {
			results = append(results, connect.Result{Name: t.Name, Label: t.Label})
			continue
		}
		found++
		results = append(results, t.Connect(exe, nm))
	}
	if *asJSON {
		return printJSON(results)
	}

	if found == 0 {
		fmt.Println("No agent harness was found on this machine.")
		fmt.Println("If one is installed somewhere unusual, register it by hand:")
		fmt.Println()
		fmt.Println(connect.Snippet(exe, nm))
		return nil
	}

	for _, r := range results {
		switch {
		case !r.Present:
			continue
		case r.Done:
			fmt.Printf("  ok    %-28s %s\n", r.Label, r.Detail)
		default:
			fmt.Printf("  FIX   %-28s %s\n", r.Label, r.Detail)
		}
	}
	for _, r := range results {
		if r.Manual != "" {
			fmt.Printf("\n%s -- add this by hand:\n\n%s\n", r.Label, r.Manual)
		}
	}
	fmt.Printf("\nThese now act as %q on the mesh. Restart any app that was already running.\n", nm)
	fmt.Println("Ask one of them \"who is on my mesh?\" to check.")
	return nil
}
