package wire

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestConnRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	want := Envelope{ID: "abc", From: "master", To: "opencode", Kind: KindAsk, Thread: "t1", Body: "hello\nworld"}
	go func() { NewConn(a).Send(want) }()

	got, err := NewConn(b).Recv()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.From != want.From || got.Kind != want.Kind || got.Body != want.Body || got.Thread != want.Thread {
		t.Fatalf("round trip changed the envelope:\n got %+v\nwant %+v", got, want)
	}
	if got.V != Version {
		t.Errorf("version not stamped: %d", got.V)
	}
	if got.TS.IsZero() {
		t.Error("timestamp not stamped; the inbox would show a zero time")
	}
}

func TestRecvRejectsFutureVersion(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { NewConn(a).Send(Envelope{V: 99, Kind: KindTell}) }()
	if _, err := NewConn(b).Recv(); err == nil {
		t.Fatal("accepted an envelope from an incompatible future version")
	}
}

func TestNewIDIsSortableAndUnique(t *testing.T) {
	first := NewID()
	time.Sleep(2 * time.Millisecond)
	second := NewID()
	if first == second {
		t.Fatal("ids collided")
	}
	if !(first < second) {
		t.Fatalf("ids do not sort chronologically: %q then %q", first, second)
	}
}

// The mesh carries an application's own vocabulary and payload without
// interpreting either. This is the platform contract: if it did not survive a
// round trip untouched, anything built on the mesh would have to encode its
// protocol inside the body string and hope.
func TestApplicationTypeAndDataArePassedThroughUntouched(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	payload := `{"task":"t-9","state":"input-required","question":"which branch?"}`
	want := Envelope{
		ID: "1", From: "coder", To: "desk", Kind: KindTell,
		Thread: "t-9", Type: "example.com/task/progress", Data: []byte(payload),
	}
	go func() { NewConn(a).Send(want) }()

	got, err := NewConn(b).Recv()
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type {
		t.Errorf("application type changed: %q", got.Type)
	}
	if string(got.Data) != payload {
		t.Errorf("application payload changed:\n got %s\nwant %s", got.Data, payload)
	}
}

// An envelope carrying no application payload must not grow one, or every
// message on the wire pays for a feature most of them do not use.
func TestPlainMessagesCarryNoApplicationFields(t *testing.T) {
	b, err := json.Marshal(Envelope{ID: "1", Kind: KindTell, Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"type", "data"} {
		if bytes.Contains(b, []byte(`"`+field+`"`)) {
			t.Errorf("an empty %q field was serialised: %s", field, b)
		}
	}
}
