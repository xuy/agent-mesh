package main

import (
	"flag"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/node"
)

func cmdGroup(args []string) error {
	fs := flag.NewFlagSet("group", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(hoistFlags(fs, args))

	nm, err := resolveName(*name)
	if err != nil {
		return err
	}
	g, err := config.LoadGroups(nm)
	if err != nil {
		return err
	}
	rest := fs.Args()
	action := "list"
	if len(rest) > 0 {
		action = rest[0]
	}

	switch action {
	case "list":
		if *asJSON {
			return printJSON(g)
		}
		if len(g) == 0 {
			fmt.Println("no groups yet -- `mesh group add builders windows opencode` makes one")
		}
		for _, n := range g.Names() {
			fmt.Printf("@%-12s %s\n", n, strings.Join(g[n], ", "))
		}
		fmt.Printf("@%-12s every peer on the mesh (built in)\n", config.AllGroup)
		return nil

	case "add":
		if len(rest) < 3 {
			return fmt.Errorf("usage: mesh group add <group> <peer> [peer...]")
		}
		g.Add(rest[1], rest[2:]...)
		if err := g.Save(nm); err != nil {
			return err
		}
		fmt.Printf("@%s: %s\n", rest[1], strings.Join(g[rest[1]], ", "))
		return nil

	case "rm", "remove":
		if len(rest) < 2 {
			return fmt.Errorf("usage: mesh group rm <group> [peer...]   (no peers removes the group)")
		}
		g.Remove(rest[1], rest[2:]...)
		if err := g.Save(nm); err != nil {
			return err
		}
		if members, ok := g[rest[1]]; ok {
			fmt.Printf("@%s: %s\n", rest[1], strings.Join(members, ", "))
		} else {
			fmt.Printf("@%s is gone\n", rest[1])
		}
		return nil

	default:
		return fmt.Errorf("usage: mesh group [list|add|rm]")
	}
}

// expandGroup turns a group address into the peers currently on the mesh.
func expandGroup(nodeName, addr string, c *node.Ctl) ([]string, error) {
	g, err := config.LoadGroups(nodeName)
	if err != nil {
		return nil, err
	}
	r, err := c.Do(node.CtlReq{Op: "peers"}, nil)
	if err != nil {
		return nil, err
	}
	var online []string
	for _, p := range r.Peers {
		if p.Online {
			online = append(online, p.Name)
		}
	}
	return g.Members(addr, online)
}

// fanOut asks every member of a group at once and prints each answer as it
// arrives, labelled.
//
// Concurrently, because these are model calls: asking five agents in sequence
// takes five times as long for no reason. Labelled, because an unattributed
// wall of answers is worse than useless when they disagree.
func fanOut(c *node.Ctl, peers []string, question, thread string, timeout time.Duration, asJSON bool) error {
	type result struct {
		Peer   string `json:"peer"`
		Answer string `json:"answer,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	results := make([]result, len(peers))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, p := range peers {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			r, err := c.Do(node.CtlReq{
				Op: "ask", To: p, Body: question, Thread: thread,
				TimeoutSec: int(timeout.Seconds()),
			}, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[i] = result{Peer: p, Error: err.Error()}
				return
			}
			results[i] = result{Peer: p, Answer: r.Body}
		}(i, p)
	}
	wg.Wait()

	if asJSON {
		return printJSON(results)
	}
	failed := 0
	for _, r := range results {
		if r.Error != "" {
			failed++
			fmt.Printf("--- %s: could not answer\n%s\n\n", r.Peer, r.Error)
			continue
		}
		fmt.Printf("--- %s\n%s\n\n", r.Peer, r.Answer)
	}
	// One peer being down should not look like success, and should not look
	// like total failure either.
	if failed > 0 && failed == len(results) {
		return fmt.Errorf("none of the %d peer(s) answered", len(results))
	}
	return nil
}
