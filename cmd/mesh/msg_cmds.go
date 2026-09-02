package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/node"
)

// ctlFor connects the CLI to the local daemon it should act as.
func ctlFor(flagName string) (*node.Ctl, string, error) {
	nm, err := resolveName(flagName)
	if err != nil {
		return nil, "", err
	}
	c, err := node.Dial(nm)
	return c, nm, err
}

func cmdPeers(args []string) error {
	fs := flag.NewFlagSet("peers", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(args)

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "peers"}, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(r.Peers)
	}
	if len(r.Peers) == 0 {
		fmt.Println("no other agents on the mesh yet")
		return nil
	}
	for _, p := range r.Peers {
		fmt.Printf("%-14s %s\n", p.Name, describePeer(p))
	}
	return nil
}

func cmdAsk(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	thread := fs.String("thread", "", "group this with earlier messages so the peer keeps context")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for an answer")
	asJSON := fs.Bool("json", false, "machine-readable output")
	stream := fs.Bool("stream", false, "show the peer's progress on stderr while it works")
	var files fileList
	fs.Var(&files, "file", "attach a file (repeatable)")
	fs.Parse(hoistFlags(fs, args))

	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: mesh ask <peer> <question>   (see `mesh peers` for who is here)")
	}
	peer, question := rest[0], joinArgs(rest[1:])

	c, nm, err := ctlFor(*name)
	if err != nil {
		return err
	}
	if config.IsGroup(peer) {
		peers, err := expandGroup(nm, peer, c)
		if err != nil {
			return err
		}
		return fanOut(c, peers, question, *thread, *timeout, *asJSON)
	}
	// Progress is off by default: a peer that streams its answer line by line
	// would otherwise print it twice, once as progress and once as the answer.
	var onChunk func(string)
	if *stream && !*asJSON {
		onChunk = func(s string) { fmt.Fprintln(os.Stderr, "  "+s) }
	}
	r, err := c.Do(node.CtlReq{
		Op: "ask", To: peer, Body: question, Thread: *thread,
		TimeoutSec: int(timeout.Seconds()), Files: files,
	}, onChunk)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(map[string]string{"peer": peer, "question": question, "answer": r.Body})
	}
	fmt.Println(r.Body)
	return nil
}

func cmdSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	thread := fs.String("thread", "", "group this with earlier messages")
	timeout := fs.Duration("timeout", 60*time.Second, "how long to wait for delivery")
	var files fileList
	fs.Var(&files, "file", "attach a file (repeatable)")
	fs.Parse(hoistFlags(fs, args))

	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: mesh send <peer> <message>")
	}
	c, nm, err := ctlFor(*name)
	if err != nil {
		return err
	}
	body := joinArgs(rest[1:])
	targets := []string{rest[0]}
	if config.IsGroup(rest[0]) {
		targets, err = expandGroup(nm, rest[0], c)
		if err != nil {
			return err
		}
	}
	var failed int
	for _, to := range targets {
		r, err := c.Do(node.CtlReq{
			Op: "tell", To: to, Body: body, Thread: *thread,
			TimeoutSec: int(timeout.Seconds()), Files: files,
		}, nil)
		if err != nil {
			failed++
			fmt.Printf("%s: %v\n", to, err)
			continue
		}
		fmt.Println(r.Body)
	}
	if failed == len(targets) {
		return fmt.Errorf("none of the %d peer(s) took the message", len(targets))
	}
	return nil
}

func cmdInbox(args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	limit := fs.Int("n", 20, "how many messages to show (0 for all)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	all := fs.Bool("all", false, "include messages you sent, not just ones you received")
	fs.Parse(args)

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "inbox", Limit: *limit, Incoming: !*all}, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(r.Msgs)
	}
	if len(r.Msgs) == 0 {
		fmt.Println("nothing yet")
		return nil
	}
	for _, m := range r.Msgs {
		who := m.From
		if who == "" {
			who = "?"
		}
		when := "--:--:--"
		if !m.TS.IsZero() {
			when = m.TS.Local().Format("15:04:05")
		}
		fmt.Printf("%s  %-10s %-5s %s\n", when, who, m.Kind, oneLine(m.Body))
	}
	return nil
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " / ")
	if len(s) > 100 {
		s = s[:100] + "..."
	}
	return s
}

func cmdWaiting(args []string) error {
	fs := flag.NewFlagSet("waiting", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(args)

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "waiting"}, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(r.Waiting)
	}
	if len(r.Waiting) == 0 {
		fmt.Println("no one is waiting on you")
		return nil
	}
	for _, q := range r.Waiting {
		fmt.Printf("%s  from %s\n  %s\n  answer with: mesh reply %s \"...\"\n", q.ID, q.From, oneLine(q.Body), q.ID)
	}
	return nil
}

func cmdReply(args []string) error {
	fs := flag.NewFlagSet("reply", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	fs.Parse(hoistFlags(fs, args))
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: mesh reply <id> <answer>   (`mesh waiting` lists the ids)")
	}
	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "reply", ID: rest[0], Body: joinArgs(rest[1:])}, nil)
	if err != nil {
		return err
	}
	fmt.Println(r.Body)
	return nil
}

func cmdPing(args []string) error {
	fs := flag.NewFlagSet("ping", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	asJSON := fs.Bool("json", false, "machine-readable output")
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait")
	fs.Parse(hoistFlags(fs, args))
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: mesh ping <peer>")
	}
	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "ping", To: rest[0], TimeoutSec: int(timeout.Seconds())}, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(r.Ping)
	}
	fmt.Printf("%s reachable in %s, %s\n", r.Ping.Peer, r.Ping.Latency.Round(time.Millisecond), r.Ping.Path)
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	name := fs.String("name", "", "act as this node")
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(args)

	c, _, err := ctlFor(*name)
	if err != nil {
		return err
	}
	r, err := c.Do(node.CtlReq{Op: "status"}, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(r.Status)
	}
	s := r.Status
	hub := "up"
	if !s.HubUp {
		hub = "DOWN (discovery stalled; existing peers still reachable)"
	}
	reach := s.Relay
	if reach == "" {
		reach = "NONE -- no peer can reach this node right now"
	}
	fmt.Printf("name     %s\nmesh     %s\nagent    %s\nanswers  %s\nreachable via %s\nhub      %s\npeers    %d (%d online)\nuptime   %s\npid      %d\n",
		s.Name, s.Mesh, s.Agent, s.Adapter, reach, hub, s.Peers, s.Online, s.Uptime, s.PID)
	return nil
}
