package main

import (
	_ "embed"
	"flag"
	"fmt"
	"strings"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/node"
)

//go:embed GUIDE.md
var guideText string

// cmdGuide prints the mesh's usage guide with this node's live situation on
// top. An agent reading it learns both how the mesh works and who is actually
// on it right now, in one command and without leaving the terminal.
func cmdGuide(args []string) error {
	fs := flag.NewFlagSet("guide", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	fs.Parse(args)

	var head strings.Builder
	if m, err := config.LoadMesh(); err == nil {
		head.WriteString(fmt.Sprintf("Mesh: %s", m.Name))
		if m.Note != "" {
			head.WriteString(" -- " + m.Note)
		}
		head.WriteString("\n")
	}
	if nm, err := resolveName(*name); err == nil {
		if c, err := node.Dial(nm); err == nil {
			if r, err := c.Do(node.CtlReq{Op: "status"}, nil); err == nil {
				s := r.Status
				head.WriteString(fmt.Sprintf("You are: %s (you answer by %s)\n", s.Name, s.Adapter))
				if !s.HubUp {
					head.WriteString("Warning: the hub is not answering, so new peers cannot find you.\n")
				}
			}
			if r, err := c.Do(node.CtlReq{Op: "peers"}, nil); err == nil {
				if len(r.Peers) == 0 {
					head.WriteString("Peers: none yet\n")
				} else {
					head.WriteString("Peers:\n")
					for _, p := range r.Peers {
						head.WriteString(fmt.Sprintf("  %-14s %s\n", p.Name, describePeer(p)))
					}
				}
			}
		} else {
			head.WriteString("Your node is not running. Start it with: mesh join\n")
		}
	} else {
		head.WriteString("You have not joined yet. Run: mesh join\n")
	}

	fmt.Println(head.String())
	fmt.Println(strings.TrimSpace(guideText))
	return nil
}
