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
	"github.com/xuy/agent-mesh/internal/pair"
	"github.com/xuy/agent-mesh/internal/service"
	"strings"
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

// youngDaemon reports whether a node has been up too briefly to expect a relay
// yet. Homing takes a few seconds, and warning about it during startup teaches
// people to ignore the warning that matters.
func youngDaemon(uptime string) bool {
	d, err := time.ParseDuration(uptime)
	return err == nil && d < time.Minute
}

func cmdInvite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	lan := fs.Bool("lan", false, "hand the invite over the local network, so the other machine only needs a short code")
	minutes := fs.Int("minutes", 10, "how long the code stays valid")
	fs.Parse(args)

	m, err := config.LoadMesh()
	if err != nil {
		return fmt.Errorf("no mesh on this machine -- run `mesh join` to start one")
	}
	if m.Hub == "" {
		return fmt.Errorf("mesh %q has no address yet -- run `mesh join` or `mesh up` at least once", m.Name)
	}
	if *lan {
		return offerOnLAN(m, time.Duration(*minutes)*time.Minute)
	}
	if *asJSON {
		return printJSON(map[string]string{"mesh": m.Name, "invite": m.Invite()})
	}
	fmt.Println(m.Invite())
	return nil
}

// offerOnLAN publishes the invite on the local network under a short code, so
// nobody has to carry a long string between two machines they are standing at.
func offerOnLAN(m config.Mesh, ttl time.Duration) error {
	code := pair.NewCode()
	o, err := pair.Offer(m, code, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
	if err != nil {
		return err
	}
	defer o.Close()

	fmt.Printf("Mesh %q is offering an invite on this network for %s.\n\n", m.Name, ttl)
	fmt.Println("On the other machine, run:")
	fmt.Printf("\n    mesh join --lan --code %s\n\n", code)
	fmt.Println("That is the whole thing to carry across. If the two machines cannot")
	fmt.Println("find each other, add --from <this machine's IP> on the other side.")
	fmt.Printf("\nWaiting...\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case host := <-o.Taken():
		fmt.Printf("\n%s took the invite. It should appear in `mesh peers` shortly.\n", host)
		// Someone else on the network may be joining too; keep the offer up
		// briefly rather than cutting a second machine off mid-handshake.
		time.Sleep(2 * time.Second)
		return nil
	case <-time.After(ttl):
		fmt.Println("\nThe code expired. Run `mesh invite --lan` again for a new one.")
		return nil
	case <-sig:
		fmt.Println("\nStopped offering.")
		return nil
	}
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

	// The check that would have caught a node alive on its own machine and
	// unreachable from every other one. `mesh ping` cannot: tailcat answers
	// that itself, so it succeeds while this program answers nothing.
	switch {
	case s.Relay != "":
		ok("reachable through relay %s", s.Relay)
	case youngDaemon(s.Uptime):
		ok("still choosing a relay (the node has only just started)")
	default:
		bad("this node has NO relay home, so no peer can reach it.")
		bad("  it restarts itself after two minutes of this; if it does not:")
		bad("  mesh down && mesh up")
	}

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

	if st, err := service.New().Status(nm); err == nil && strings.HasPrefix(st, "not installed") {
		bad("this node will not come back after a reboot or a crash.")
		bad("  fix it once with: mesh service install")
	} else if err == nil {
		ok("registered with %s, so it restarts on its own", service.New().Describe())
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
