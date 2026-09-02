package main

import (
	"flag"
	"fmt"

	"github.com/xuy/agent-mesh/internal/node"
	"github.com/xuy/agent-mesh/internal/policy"
)

// cmdID prints this node's fingerprint, for reading out to whoever is adding it.
func cmdID(args []string) error {
	fs := flag.NewFlagSet("id", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(hoistFlags(fs, args))

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "status"}, nil)
	if err != nil {
		return err
	}
	fp := policy.Fingerprint(r.Status.Key)
	if *asJSON {
		return printJSON(map[string]string{"name": r.Status.Name, "mesh": r.Status.Mesh, "fingerprint": fp, "key": r.Status.Key})
	}
	fmt.Printf("%s on mesh %s\n", r.Status.Name, r.Status.Mesh)
	fmt.Printf("fingerprint  %s\n", fp)
	fmt.Println("\nRead that to whoever is adding you. They confirm it with `mesh verify " + r.Status.Name + "`.")
	return nil
}

// cmdTrust shows and changes what peers are allowed to do.
func cmdTrust(args []string) error {
	fs := flag.NewFlagSet("trust", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(hoistFlags(fs, args))

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "trust"}, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(r.Trust)
	}
	if len(r.Trust) == 0 {
		fmt.Println("no peers have contacted this node yet")
		return nil
	}
	for _, p := range r.Trust {
		fmt.Printf("%-14s %s\n", p.Name, describeTrust(p))
	}
	fmt.Println("\nmesh allow <peer>    let it ask this node to do work")
	fmt.Println("mesh deny <peer>     take that back")
	fmt.Println("mesh block <peer>    refuse it entirely")
	fmt.Println("mesh verify <peer>   record that you checked its fingerprint")
	return nil
}

func describeTrust(p *policy.Peer) string {
	s := "may tell"
	if p.MayAsk {
		s = "may tell and ask"
	}
	if p.Blocked {
		s = "BLOCKED"
	}
	s += ", key " + policy.Fingerprint(p.Key)
	if p.Verified {
		s += " (verified)"
	} else {
		s += " (pinned on first contact, not verified)"
	}
	return s
}

// cmdTrustAction applies one decision to one peer.
func cmdTrustAction(op string) func([]string) error {
	return func(args []string) error {
		fs := flag.NewFlagSet(op, flag.ExitOnError)
		name := fs.String("name", "", "act as this node")
		fs.Parse(hoistFlags(fs, args))
		rest := fs.Args()
		if len(rest) < 1 {
			return fmt.Errorf("usage: mesh %s <peer>", op)
		}
		c, _, err := ctlFor(*name)
		if err != nil {
			return err
		}
		r, err := c.Do(node.CtlReq{Op: op, To: rest[0]}, nil)
		if err != nil {
			return err
		}
		for _, p := range r.Trust {
			if p.Name == rest[0] {
				fmt.Printf("%-14s %s\n", p.Name, describeTrust(p))
				return nil
			}
		}
		fmt.Println(r.Body)
		return nil
	}
}

// cmdLog shows what peers have asked of this node, including what was refused.
func cmdLog(args []string) error {
	fs := flag.NewFlagSet("log", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	limit := fs.Int("n", 30, "how many entries to show (0 for all)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(hoistFlags(fs, args))

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "audit", Limit: *limit}, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(r.Audit)
	}
	if len(r.Audit) == 0 {
		fmt.Println("nothing has been asked of this node yet")
		return nil
	}
	for _, a := range r.Audit {
		when := a.TS.Local().Format("15:04:05")
		if a.Outcome == "refused" {
			fmt.Printf("%s  %-10s %-5s REFUSED  %s\n", when, a.From, a.Kind, a.Reason)
			continue
		}
		fmt.Printf("%s  %-10s %-5s ok       %s\n", when, a.From, a.Kind, oneLine(a.Body))
	}
	return nil
}
