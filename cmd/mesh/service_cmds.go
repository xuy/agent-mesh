package main

import (
	"flag"
	"fmt"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/node"
	"github.com/xuy/agent-mesh/internal/service"
)

// cmdService keeps a node running without anyone tending it: across reboots,
// across crashes, and across the end of the shell that started it.
func cmdService(args []string) error {
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	fs.Parse(hoistFlags(fs, args))

	rest := fs.Args()
	action := "status"
	if len(rest) > 0 {
		action = rest[0]
	}
	nm, err := resolveName(*name)
	if err != nil {
		return err
	}
	m := service.New()

	switch action {
	case "install":
		if _, err := config.LoadNode(nm); err != nil {
			return fmt.Errorf("no node named %q here -- run `mesh join --name %s` first", nm, nm)
		}
		exe, err := service.Executable()
		if err != nil {
			return err
		}
		// The service starts its own daemon; leaving the hand-started one
		// running would fight it for the control socket.
		if c, err := node.Dial(nm); err == nil {
			c.Do(node.CtlReq{Op: "stop"}, nil)
		}
		if err := m.Install(nm, exe); err != nil {
			return err
		}
		fmt.Printf("%s now starts with %s and comes back if it stops.\n", nm, m.Describe())
		fmt.Println("Nothing to restart by hand after a reboot.")
		return nil

	case "uninstall":
		if err := m.Uninstall(nm); err != nil {
			return err
		}
		fmt.Printf("%s no longer starts on its own. `mesh up --name %s` still works.\n", nm, nm)
		return nil

	case "status":
		st, err := m.Status(nm)
		if err != nil {
			return err
		}
		fmt.Printf("%-10s %s (%s)\n", nm, st, m.Describe())
		if _, err := node.Dial(nm); err == nil {
			fmt.Printf("%-10s daemon is answering right now\n", "")
		} else {
			fmt.Printf("%-10s daemon is not answering\n", "")
		}
		return nil

	default:
		return fmt.Errorf("usage: mesh service [install|uninstall|status]")
	}
}
