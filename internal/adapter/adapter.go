// Package adapter turns an inbound question into an answer.
//
// This is the layer that makes the mesh a mesh of agents rather than a message
// pipe: an adapter is the seam where a message stops being bytes and becomes a
// prompt to whatever model or human is behind the node.
package adapter

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Request is an inbound ask, stripped to what an adapter needs.
type Request struct {
	ID     string
	From   string
	Thread string
	Body   string
}

// Emit streams partial progress back to the asker. Adapters should call it
// when they have something to show but not yet an answer; it is advisory and
// an error from it means the asker is gone.
type Emit func(chunk string) error

// Adapter answers questions on behalf of a node.
type Adapter interface {
	// Kind names the adapter in the roster, so a peer can see how it will be
	// answered before it asks.
	Kind() string
	Handle(ctx context.Context, r Request, emit Emit) (string, error)
}

// Mailbox parks an ask until the local agent answers it by hand with
// `mesh reply`. It requires no integration at all, which makes it the right
// default: any agent, or any human, can be a mesh node on day one.
type Mailbox struct {
	mu      sync.Mutex
	pending map[string]*parked
}

type parked struct {
	r  Request
	ch chan string
	at time.Time
}

// NewMailbox returns an empty mailbox.
func NewMailbox() *Mailbox { return &Mailbox{pending: map[string]*parked{}} }

func (m *Mailbox) Kind() string { return "mailbox" }

func (m *Mailbox) Handle(ctx context.Context, r Request, emit Emit) (string, error) {
	p := &parked{r: r, ch: make(chan string, 1), at: time.Now()}
	m.mu.Lock()
	m.pending[r.ID] = p
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pending, r.ID)
		m.mu.Unlock()
	}()

	select {
	case answer := <-p.ch:
		return answer, nil
	case <-ctx.Done():
		return "", fmt.Errorf("%s did not answer in time (it is a mailbox node: someone must run `mesh reply %s`)", r.From, r.ID)
	}
}

// Reply answers a parked ask.
func (m *Mailbox) Reply(id, answer string) error {
	m.mu.Lock()
	p, ok := m.pending[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no question is waiting with id %s (it may have timed out; `mesh inbox` shows what is open)", id)
	}
	select {
	case p.ch <- answer:
		return nil
	default:
		return fmt.Errorf("question %s was already answered", id)
	}
}

// Waiting lists the asks currently parked, oldest first.
func (m *Mailbox) Waiting() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, 0, len(m.pending))
	for _, p := range m.pending {
		out = append(out, p.r)
	}
	return out
}

// Exec answers by running a command and streaming its stdout back.
//
// The question is never interpolated into the command line. It arrives in the
// MESH_BODY environment variable and on the command's stdin, so a remote agent
// cannot inject shell syntax into a node it is merely allowed to talk to.
type Exec struct {
	// Cmd is a shell command, run with `sh -c`. It should reference the
	// question as "$MESH_BODY" (quoted) or read it from stdin.
	Cmd string
	// Dir is the working directory, if any.
	Dir string
	// Shell overrides the interpreter. Defaults to sh on Unix and PowerShell
	// on Windows; see ShellHint.
	Shell string

	mu      sync.Mutex
	threads map[string]bool
}

func (e *Exec) Kind() string { return "exec" }

func (e *Exec) Handle(ctx context.Context, r Request, emit Emit) (string, error) {
	// A thread the node has answered before is a continuing conversation, and
	// the far-side agent should keep its context. The command decides what to
	// do about it; we only report which case this is.
	cont := ""
	if r.Thread != "" {
		e.mu.Lock()
		if e.threads == nil {
			e.threads = map[string]bool{}
		}
		if e.threads[r.Thread] {
			cont = "--continue"
		}
		e.threads[r.Thread] = true
		e.mu.Unlock()
	}

	argv := shellArgv(e.Shell, e.Cmd)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = e.Dir
	cmd.Env = append(envOf(), []string{
		"MESH_BODY=" + r.Body,
		"MESH_FROM=" + r.From,
		"MESH_ID=" + r.ID,
		"MESH_THREAD=" + r.Thread,
		"MESH_CONTINUE=" + cont,
	}...)
	cmd.Stdin = strings.NewReader(r.Body)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var errbuf strings.Builder
	cmd.Stderr = &errbuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("adapter failed to start: %w", err)
	}

	var out strings.Builder
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		out.WriteString(line)
		out.WriteByte('\n')
		if emit != nil {
			emit(line)
		}
	}
	io.Copy(io.Discard, stdout)

	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(errbuf.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if len(msg) > 2000 {
			msg = msg[:2000] + "..."
		}
		return "", fmt.Errorf("adapter exited: %v: %s", err, msg)
	}
	answer := strings.TrimRight(out.String(), "\n")
	if answer == "" {
		answer = "(the adapter produced no output)"
	}
	return answer, nil
}

// Question is the public shape of a parked ask, as reported by `mesh waiting`.
type Question = Request
