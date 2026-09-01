package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/hub"
	"github.com/xuy/agent-mesh/internal/node"
)

func cmdHub(args []string) error {
	fs := flag.NewFlagSet("hub", flag.ExitOnError)
	meshName := fs.String("mesh", "", "name of the mesh to serve (only needed the first time)")
	note := fs.String("note", "", "one line describing this mesh, shown to agents that join")
	fg := fs.Bool("foreground", false, "run in this process instead of detaching")
	stop := fs.Bool("stop", false, "stop the hub running on this machine")
	fs.Parse(args)

	if *stop {
		st, ok := hub.Running()
		if !ok {
			return fmt.Errorf("no hub is running on this machine")
		}
		p, err := os.FindProcess(st.PID)
		if err != nil {
			return err
		}
		if err := stopProcess(p); err != nil {
			return err
		}
		fmt.Printf("stopped the hub for mesh %q\n", st.Mesh)
		return nil
	}
	if st, ok := hub.Running(); ok && !*fg {
		fmt.Printf("the hub for mesh %q is already running (pid %d)\n", st.Mesh, st.PID)
		return printInvite()
	}

	m, err := config.LoadMesh()
	if err != nil {
		nm := *meshName
		if nm == "" {
			nm = "mesh"
		}
		m = config.Mesh{Name: nm, Join: config.NewJoinKey(), Note: *note}
	}
	if *meshName != "" && *meshName != m.Name {
		return fmt.Errorf("this machine already hosts mesh %q; delete %s to start a different one", m.Name, config.MeshPath())
	}
	if *note != "" {
		m.Note = *note
	}

	if !*fg {
		// Detach, then wait for the running hub to publish its state.
		before, _ := hub.Running()
		if _, err := detach(filepath.Join(config.HubDir(), "hub.log"), append([]string{"hub", "--foreground"}, args...)...); err != nil {
			return fmt.Errorf("starting the hub: %w", err)
		}
		fmt.Printf("starting the hub for mesh %q (measuring relay latency, ~10-30s)", m.Name)
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			if st, ok := hub.Running(); ok && st.Started.After(before.Started) {
				fmt.Println(" ok")
				return hubUpMessage()
			}
			fmt.Print(".")
			time.Sleep(time.Second)
		}
		fmt.Println()
		return fmt.Errorf("the hub did not come up -- see %s", filepath.Join(config.HubDir(), "hub.log"))
	}

	logf := func(f string, a ...any) { fmt.Printf(time.Now().Format("15:04:05 ")+f+"\n", a...) }
	h := hub.New(m, logf)
	fmt.Printf("starting the hub for mesh %q (measuring relay latency, ~10-30s)\n", m.Name)
	blob, err := h.Start()
	if err != nil {
		return err
	}
	defer h.Close()

	m.Hub = blob
	if err := m.Save(); err != nil {
		return err
	}

	if err := hubUpMessage(); err != nil {
		return err
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("\nhub stopping")
	return nil
}

// hubUpMessage tells whoever started the hub what to do next, in the two cases
// that actually occur: another agent on this machine, or one somewhere else.
func hubUpMessage() error {
	m, err := config.LoadMesh()
	if err != nil {
		return err
	}
	fmt.Printf("\nmesh %q is up.\n\n", m.Name)
	fmt.Println("Agents on THIS machine can now join with no arguments:")
	fmt.Println("    mesh join")
	fmt.Println("\nAn agent on ANOTHER machine joins with this invite (it is a secret --")
	fmt.Println("it carries the join key, so hand it over the way you would a password):")
	fmt.Printf("    mesh join --invite %s\n\n", m.Invite())
	fmt.Println("Peers talk to each other directly, so messages keep flowing if the hub")
	fmt.Println("stops -- only joining and address changes need it.")
	return nil
}

func printInvite() error {
	m, err := config.LoadMesh()
	if err != nil {
		return err
	}
	fmt.Printf("invite: mesh join --invite %s\n", m.Invite())
	return nil
}

func cmdInvite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(args)

	m, err := config.LoadMesh()
	if err != nil {
		return fmt.Errorf("no mesh on this machine -- run `mesh hub --mesh <name>` to start one")
	}
	if m.Hub == "" {
		return fmt.Errorf("mesh %q has no hub address yet -- run `mesh hub` at least once", m.Name)
	}
	if *asJSON {
		return printJSON(map[string]string{"mesh": m.Name, "invite": m.Invite()})
	}
	fmt.Println(m.Invite())
	return nil
}

// cmdDoctor answers "why isn't this working" in the order the answers matter,
// and ends every finding with the command that fixes it.
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	fs.Parse(args)

	ok := func(f string, a ...any) { fmt.Printf("  ok    "+f+"\n", a...) }
	bad := func(f string, a ...any) { fmt.Printf("  FIX   "+f+"\n", a...) }

	fmt.Println("mesh doctor")

	m, err := config.LoadMesh()
	if err != nil {
		bad("this machine belongs to no mesh.")
		bad("  start one:  mesh hub --mesh <name>")
		bad("  or join:    mesh join --invite <string>")
		return nil
	}
	ok("mesh %q, hub address on file", m.Name)

	nm, err := resolveName(*name)
	if err != nil {
		bad("no node here yet.  run: mesh join")
		return nil
	}
	if _, err := config.LoadNode(nm); err != nil {
		bad("node %q has no settings.  run: mesh join --name %s", nm, nm)
		return nil
	}
	ok("node %q is configured", nm)

	c, err := node.Dial(nm)
	if err != nil {
		bad("the daemon for %q is not running.  run: mesh up --name %s", nm, nm)
		bad("  its log: %s", config.LogPath(nm))
		return nil
	}
	r, err := c.Do(node.CtlReq{Op: "status"}, nil)
	if err != nil {
		bad("the daemon is not answering: %v", err)
		return nil
	}
	s := r.Status
	ok("daemon running, pid %d, up %s", s.PID, s.Uptime)

	if !s.HubUp {
		bad("not registered with the hub, so no one can find you.")
		bad("  is the hub running on its machine?  mesh hub")
		bad("  this node's log: %s", config.LogPath(nm))
	} else {
		ok("registered with the hub")
	}

	if s.Peers == 0 {
		bad("no peers known. another agent must run: mesh join")
	} else {
		ok("%d peer(s) known, %d online", s.Peers, s.Online)
	}

	if s.Adapter == "mailbox" {
		w, err := c.Do(node.CtlReq{Op: "waiting"}, nil)
		if err == nil && len(w.Waiting) > 0 {
			bad("%d question(s) are waiting on you.  run: mesh waiting", len(w.Waiting))
		} else {
			ok("nothing waiting on you")
		}
	}
	return nil
}
