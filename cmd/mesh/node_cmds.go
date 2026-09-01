package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
	"github.com/xuy/agent-mesh/internal/ident"
	"github.com/xuy/agent-mesh/internal/node"
)

// resolveName picks which local node a command acts as: an explicit flag, then
// $MESH_NAME, then the last node joined on this machine.
func resolveName(flagName string) (string, error) {
	if flagName != "" {
		return flagName, nil
	}
	if n := config.Current(); n != "" {
		return n, nil
	}
	return "", fmt.Errorf("no node on this machine yet -- run `mesh join` to create one")
}

// detectAgent guesses what software is running this command, so a joining agent
// does not have to be told what it is.
func detectAgent() string {
	switch {
	case os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "":
		return "claude-code"
	case os.Getenv("OPENCODE") != "" || os.Getenv("OPENCODE_BIN_PATH") != "" || os.Getenv("OPENCODE_APP_INFO") != "":
		return "opencode"
	default:
		return "agent"
	}
}

// defaultMeshName names a brand-new mesh after the machine that started it,
// which is almost always what a single person wants and is easy to change.
func defaultMeshName() string {
	h, _ := os.Hostname()
	h = strings.TrimSuffix(strings.ToLower(h), ".local")
	if h == "" {
		return "mesh"
	}
	return h
}

func defaultName(agent string) string {
	if agent != "agent" {
		return agent
	}
	h, _ := os.Hostname()
	h = strings.TrimSuffix(strings.ToLower(h), ".local")
	if h == "" {
		h = "agent"
	}
	return h
}

func cmdJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	name := fs.String("name", "", "this node's name on the mesh (must be unique)")
	invite := fs.String("invite", "", "invite string from `mesh invite` on a machine already in the mesh")
	meshName := fs.String("mesh", "", "name for a new mesh, if this is the first node")
	agent := fs.String("agent", "", "what software is behind this node (default: detected)")
	execCmd := fs.String("exec", "", "answer questions by running this shell command instead of parking them for a human")
	webhook := fs.String("webhook", "", "deliver questions to a resident agent's local API, e.g. http://127.0.0.1:8080/api/sessions/main/messages")
	webhookHdr := fs.String("webhook-header", "", "header the local API needs, e.g. \"Authorization: Bearer <token>\"")
	webhookAsync := fs.Bool("webhook-async", false, "the local API only acknowledges; the answer comes back later via `mesh reply`")
	notify := fs.String("notify", "", "park questions for `mesh reply`, but run this command first so someone notices")
	note := fs.String("note", "", "one line telling other agents what you are for")
	wait := fs.Duration("wait", 90*time.Second, "how long to wait for the tunnel to come up")
	fs.Parse(args)

	if *invite != "" {
		m, err := config.ParseInvite(*invite)
		if err != nil {
			return err
		}
		if err := m.Save(); err != nil {
			return err
		}
		fmt.Printf("joined mesh %q\n", m.Name)
	}
	// No mesh here and no invite means this is the first agent: create the
	// mesh and let this node coordinate it, so nobody has to run a server.
	m, err := config.LoadMesh()
	founding := false
	if err != nil {
		nm := *meshName
		if nm == "" {
			nm = defaultMeshName()
		}
		m = config.Mesh{Name: nm, Join: config.NewJoinKey(), Note: *note}
		founding = true
	}

	ag := *agent
	if ag == "" {
		ag = detectAgent()
	}
	nm := *name
	if nm == "" {
		nm = defaultName(ag)
	}

	cfg, err := config.LoadNode(nm)
	if err != nil {
		cfg = config.Node{Name: nm}
	}
	cfg.Name, cfg.Mesh = nm, m.Name
	if ag != "" {
		cfg.Agent = ag
	}
	if *note != "" {
		cfg.Note = *note
	}
	switch {
	case *webhook != "":
		cfg.Adapter, cfg.WebhookURL = "webhook", *webhook
		cfg.WebhookHeader, cfg.WebhookAsync = *webhookHdr, *webhookAsync
	case *notify != "":
		cfg.Adapter, cfg.Exec = "notify", *notify
	case *execCmd != "":
		cfg.Adapter, cfg.Exec = "exec", *execCmd
	case cfg.Adapter == "":
		cfg.Adapter = "mailbox"
	}
	if founding {
		m.Coordinator = nm
		if err := m.Save(); err != nil {
			return err
		}
		fmt.Printf("created mesh %q with %s as its coordinator\n", m.Name, nm)
	}
	if err := ensureIdentity(nm); err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	if err := config.SetCurrent(nm); err != nil {
		return err
	}

	if _, err := node.Dial(nm); err == nil {
		fmt.Printf("%s is already on mesh %q\n", nm, m.Name)
	} else {
		if err := spawnDaemon(nm, *wait); err != nil {
			return err
		}
	}
	return joinSummary(nm, m.Name)
}

func joinSummary(nm, mesh string) error {
	c, err := node.Dial(nm)
	if err != nil {
		return err
	}
	// Registration is a round trip to the hub; give it a moment before
	// reporting an empty mesh that is really just a slow handshake.
	var st *node.Status
	deadline := time.Now().Add(20 * time.Second)
	for {
		r, err := c.Do(node.CtlReq{Op: "status"}, nil)
		if err != nil {
			return err
		}
		st = r.Status
		if st.HubUp || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("\nYou are %q on mesh %q.\n", st.Name, mesh)
	if !st.HubUp {
		fmt.Printf("The hub is not answering yet, so no one can find you. Run `mesh doctor`.\n")
	}
	r, err := c.Do(node.CtlReq{Op: "peers"}, nil)
	if err == nil {
		if len(r.Peers) == 0 {
			fmt.Println("No other agents are here yet.")
		} else {
			fmt.Printf("%d other agent(s) here:\n", len(r.Peers))
			for _, p := range r.Peers {
				fmt.Printf("  %-14s %s\n", p.Name, describePeer(p))
			}
		}
	}
	if m, err := config.LoadMesh(); err == nil && m.Coordinator == st.Name && m.Hub != "" {
		fmt.Println("\nThis node coordinates the mesh. To add an agent on another machine,")
		fmt.Println("give it this invite (a secret -- it carries the join key):")
		fmt.Printf("    mesh join --invite %s\n", m.Invite())
	}
	switch st.Adapter {
	case "mailbox":
		fmt.Println("\nYou answer by hand: `mesh waiting` shows questions, `mesh reply <id> <answer>` answers one.")
	case "exec":
		fmt.Println("\nYou answer automatically by running your configured command.")
	case "webhook":
		fmt.Println("\nQuestions are delivered to your resident agent's local API.")
		fmt.Println("If it only acknowledges them, answer with `mesh reply <id> <answer>`.")
	case "notify":
		fmt.Println("\nQuestions park for you and run your notify command; answer with `mesh reply <id> <answer>`.")
	}
	fmt.Println("Run `mesh guide` for everything else.")
	return nil
}

func describePeer(p ident.Info) string {
	bits := []string{}
	if p.Agent != "" {
		bits = append(bits, p.Agent)
	}
	if len(p.Kinds) > 0 {
		bits = append(bits, strings.Join(p.Kinds, ","))
	}
	s := strings.Join(bits, " ")
	if p.Note != "" {
		s += " -- " + p.Note
	}
	if !p.Online {
		s += " (offline)"
	}
	return s
}

func ensureIdentity(name string) error {
	p := config.IdentityPath(name)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return ident.New().Save(p)
}

// detach starts this binary again, in its own session, logging to logPath.
//
// Its own session matters: a daemon must outlive the shell that started it,
// including a shell that is killed rather than exited, which is the normal case
// when an agent starts one from a tool call.
func detach(logPath string, args ...string) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return 0, err
	}
	lf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer lf.Close()

	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = detachAttrs()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

// spawnDaemon starts the node daemon detached and waits for it to answer.
func spawnDaemon(name string, wait time.Duration) error {
	pid, err := detach(config.LogPath(name), "up", "--name", name, "--foreground")
	if err != nil {
		return fmt.Errorf("starting the daemon: %w", err)
	}
	os.WriteFile(config.PIDPath(name), []byte(strconv.Itoa(pid)+"\n"), 0o600)

	fmt.Printf("bringing up %s's tunnel (first start measures relay latency, ~10-30s)", name)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if _, err := node.Dial(name); err == nil {
			fmt.Println(" ok")
			return nil
		}
		fmt.Print(".")
		time.Sleep(time.Second)
	}
	fmt.Println()
	return fmt.Errorf("the daemon did not come up within %s -- see %s", wait, config.LogPath(name))
}

func cmdUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	name := fs.String("name", "", "node to run")
	fg := fs.Bool("foreground", false, "run in this process instead of detaching")
	fs.Parse(args)

	nm, err := resolveName(*name)
	if err != nil {
		return err
	}
	if !*fg {
		if _, err := node.Dial(nm); err == nil {
			fmt.Printf("%s is already running\n", nm)
			return nil
		}
		return spawnDaemon(nm, 90*time.Second)
	}

	m, err := config.LoadMesh()
	if err != nil {
		return fmt.Errorf("no mesh configured -- run `mesh hub` or `mesh join --invite <string>`")
	}
	cfg, err := config.LoadNode(nm)
	if err != nil {
		return fmt.Errorf("no node named %q -- run `mesh join --name %s`", nm, nm)
	}
	id, err := ident.Load(config.IdentityPath(nm))
	if err != nil {
		return fmt.Errorf("no identity for %q -- run `mesh join --name %s`", nm, nm)
	}

	// A foreground daemon's stderr is its log file (spawnDaemon redirects it),
	// so there is nothing to suppress.
	logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05 ")+f+"\n", a...) }

	n := node.New(cfg, m, id, logf)
	if err := n.Start(); err != nil {
		return err
	}
	defer n.Close()

	ln, err := node.ListenControl(nm)
	if err != nil {
		return err
	}
	defer func() { ln.Close(); node.RemoveControl(nm) }()

	logf("%s is up on mesh %q", nm, m.Name)
	go n.ServeCtl(ln)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	logf("%s shutting down", nm)
	return nil
}

func cmdDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	name := fs.String("name", "", "node to stop")
	fs.Parse(args)
	nm, err := resolveName(*name)
	if err != nil {
		return err
	}
	c, err := node.Dial(nm)
	if err != nil {
		return fmt.Errorf("%s is not running", nm)
	}
	r, err := c.Do(node.CtlReq{Op: "stop"}, nil)
	if err != nil {
		return err
	}
	fmt.Println(r.Body)
	return nil
}
