package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/xuy/agent-mesh/internal/node"
)

// cmdWait blocks until a peer says something, then prints it and exits.
//
// It exists because an agent sitting at its prompt has no way to learn that a
// message arrived. Polling is what this project exists to stop people doing,
// and a command that blocks and then exits is something every agent harness
// already knows how to handle: run it in the background, and be told when it
// finishes. That turns "check your mesh inbox" from a habit an agent has to
// remember into an event it gets handed.
func cmdWait(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after this long")
	since := fs.String("since", "", "also report anything newer than this message id, in case it arrived while you were away")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(hoistFlags(fs, args))

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{
		Op: "wait", ID: *since, TimeoutSec: int(timeout.Seconds()) + 5,
	}, nil)
	if err != nil {
		// Nothing arriving is not a failure of the mesh, but it is a distinct
		// outcome a script needs to tell apart from a message.
		fmt.Fprintln(os.Stderr, "mesh: "+err.Error())
		os.Exit(3)
	}
	if *asJSON {
		return printJSON(r.Msgs)
	}
	for _, m := range r.Msgs {
		for _, f := range m.Files {
			fmt.Printf("%s sent a file: %s (%d bytes)\n  saved at %s\n", m.From, f.Name, f.Size, f.Path)
		}
		switch m.Kind {
		case "ask":
			fmt.Printf("%s asks: %s\n", m.From, m.Body)
			fmt.Printf("  they are waiting -- answer with: mesh reply %s \"...\"\n", m.ID)
		default:
			fmt.Printf("%s says: %s\n", m.From, m.Body)
		}
	}
	if len(r.Msgs) > 0 {
		fmt.Printf("\n(latest id %s -- pass it as --since next time so nothing is missed)\n", r.Msgs[len(r.Msgs)-1].ID)
	}
	return nil
}
