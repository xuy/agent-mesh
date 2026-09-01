package adapter

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMailboxParksUntilAnswered(t *testing.T) {
	m := NewMailbox()
	done := make(chan string, 1)
	go func() {
		a, err := m.Handle(context.Background(), Request{ID: "q1", From: "opencode", Body: "which branch?"}, nil)
		if err != nil {
			t.Error(err)
		}
		done <- a
	}()

	waitFor(t, func() bool { return len(m.Waiting()) == 1 })
	if got := m.Waiting()[0].Body; got != "which branch?" {
		t.Fatalf("waiting question is wrong: %q", got)
	}
	if err := m.Reply("q1", "main"); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-done:
		if a != "main" {
			t.Fatalf("answer got mangled: %q", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Reply did not release the parked question")
	}
	if len(m.Waiting()) != 0 {
		t.Fatal("an answered question is still listed as waiting")
	}
}

func TestMailboxTimesOutWithAnActionableError(t *testing.T) {
	m := NewMailbox()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := m.Handle(ctx, Request{ID: "q2", From: "opencode", Body: "hi"}, nil)
	if err == nil {
		t.Fatal("a question nobody answered returned success")
	}
	// The asker sees this text; it has to say what to do about it.
	if !strings.Contains(err.Error(), "mesh reply q2") {
		t.Fatalf("timeout error does not name the fix: %v", err)
	}
}

func TestMailboxRejectsUnknownID(t *testing.T) {
	if err := NewMailbox().Reply("nope", "x"); err == nil {
		t.Fatal("replied to a question that was never asked")
	}
}

func TestExecPassesBodyOutOfBand(t *testing.T) {
	// The body must never be interpolated into the command line: a peer we
	// merely allow to talk to us must not be able to inject shell syntax.
	e := &Exec{Cmd: `printf '%s' "$MESH_BODY"`}
	got, err := e.Handle(context.Background(), Request{ID: "1", From: "peer", Body: `"; touch /tmp/pwned; echo "`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != `"; touch /tmp/pwned; echo "` {
		t.Fatalf("body was not passed through intact: %q", got)
	}
}

func TestExecStreamsLinesThenAnswers(t *testing.T) {
	e := &Exec{Cmd: `echo one; echo two`}
	var chunks []string
	got, err := e.Handle(context.Background(), Request{ID: "1"}, func(c string) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[0] != "one" || chunks[1] != "two" {
		t.Fatalf("progress was not streamed line by line: %v", chunks)
	}
	if got != "one\ntwo" {
		t.Fatalf("answer is wrong: %q", got)
	}
}

func TestExecReportsFailureWithStderr(t *testing.T) {
	e := &Exec{Cmd: `echo "the model is not configured" >&2; exit 3`}
	_, err := e.Handle(context.Background(), Request{ID: "1"}, nil)
	if err == nil {
		t.Fatal("a failing adapter reported success")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("the reason is missing from the error: %v", err)
	}
}

func TestExecSignalsThreadContinuation(t *testing.T) {
	e := &Exec{Cmd: `printf '%s' "${MESH_CONTINUE:-none}"`}
	first, err := e.Handle(context.Background(), Request{ID: "1", Thread: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != "none" {
		t.Fatalf("the first turn of a thread should not continue anything, got %q", first)
	}
	second, err := e.Handle(context.Background(), Request{ID: "2", Thread: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second != "--continue" {
		t.Fatalf("a second turn on the same thread should continue the peer's session, got %q", second)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}
