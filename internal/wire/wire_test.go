package wire

import (
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
