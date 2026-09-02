package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebhookAnswersFromTheResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"reply":"the build is green"}`))
	}))
	defer srv.Close()

	got, err := (&Webhook{URL: srv.URL}).Handle(context.Background(), Request{ID: "1", From: "master", Body: "how's the build?"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "the build is green" {
		t.Fatalf("answer wrong: %q", got)
	}
}

func TestWebhookAcceptsPlainTextAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("  green  \n"))
	}))
	defer srv.Close()
	got, err := (&Webhook{URL: srv.URL}).Handle(context.Background(), Request{ID: "1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "green" {
		t.Fatalf("answer wrong: %q", got)
	}
}

// A resident agent that merely acknowledges must not be treated as having
// answered: the question parks until the agent gets round to `mesh reply`.
func TestWebhookParksWhenTheAgentOnlyAcknowledges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"queued"}`))
	}))
	defer srv.Close()

	wh := &Webhook{URL: srv.URL, Box: NewMailbox()}
	done := make(chan string, 1)
	go func() {
		a, err := wh.Handle(context.Background(), Request{ID: "q9", From: "master", Body: "hi"}, nil)
		if err != nil {
			t.Error(err)
		}
		done <- a
	}()

	waitFor(t, func() bool { return len(wh.Mailbox().Waiting()) == 1 })
	if err := wh.Mailbox().Reply("q9", "answered later"); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-done:
		if a != "answered later" {
			t.Fatalf("answer wrong: %q", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the parked question was never released")
	}
}

func TestWebhookSendsAuthHeaderAndReplyInstructions(t *testing.T) {
	type got struct {
		auth string
		body delivery
	}
	seen := make(chan got, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var d delivery
		json.Unmarshal(b, &d)
		seen <- got{auth: r.Header.Get("Authorization"), body: d}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	wh := &Webhook{URL: srv.URL, Header: "Authorization: Bearer s3cret"}
	if _, err := wh.Handle(context.Background(), Request{ID: "abc", From: "master", Body: "hello"}, nil); err != nil {
		t.Fatal(err)
	}
	g := <-seen
	if g.auth != "Bearer s3cret" {
		t.Errorf("auth header not sent: %q", g.auth)
	}
	// The payload must carry its own reply instructions, so an agent with no
	// integration written for it can still answer.
	if !strings.Contains(g.body.ReplyWith, "mesh reply abc") {
		t.Errorf("payload does not say how to answer: %q", g.body.ReplyWith)
	}
	// And it must frame the message as peer input rather than a directive.
	if !strings.Contains(strings.ToLower(g.body.Note), "not as instructions") {
		t.Errorf("payload does not mark the message as peer data: %q", g.body.Note)
	}
	if g.body.From != "master" {
		t.Errorf("sender lost: %q", g.body.From)
	}
}

func TestWebhookReportsAnUnreachableAgent(t *testing.T) {
	_, err := (&Webhook{URL: "http://127.0.0.1:1/nope", Timeout: time.Second}).
		Handle(context.Background(), Request{ID: "1"}, nil)
	if err == nil {
		t.Fatal("a dead endpoint reported success")
	}
	if !strings.Contains(err.Error(), "could not reach") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestWebhookReportsRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := (&Webhook{URL: srv.URL}).Handle(context.Background(), Request{ID: "1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("rejection not surfaced: %v", err)
	}
}

func TestNotifyRunsTheCommandThenParks(t *testing.T) {
	dir := t.TempDir()
	who := filepath.Join(dir, "who")
	n := &Notify{Cmd: cmdWriteFrom(who), Box: NewMailbox()}
	go func() {
		waitFor(t, func() bool { return len(n.Mailbox().Waiting()) == 1 })
		n.Mailbox().Reply("n1", "ok")
	}()
	got, err := n.Handle(context.Background(), Request{ID: "n1", From: "master", Body: "ping"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("answer wrong: %q", got)
	}
	b, err := readFile(who)
	if err != nil || b != "master" {
		t.Fatalf("the notify command did not run with the message context: %q %v", b, err)
	}
}
