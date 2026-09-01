// Package wire defines the message envelope agents exchange over the mesh
// data plane, plus the framing used on every mesh connection.
//
// The framing is newline-delimited JSON in both directions. A request opens a
// connection and the connection stays open for the reply, so a long-running
// agent can stream progress before it answers.
package wire

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// Version is the envelope version. Receivers ignore unknown fields, so the
// format can grow without a version bump; this exists to reject a future
// incompatible change.
const Version = 1

// Port is the TCP port a node serves the mesh protocol on, inside its tunnel.
const Port = 7020

// HubPort is the TCP port the control plane serves on, inside its tunnel.
const HubPort = 7010

// Kind is an envelope's message type.
type Kind string

const (
	// KindTell is fire-and-forget: it lands in the recipient's inbox and is
	// acknowledged, but no answer is produced.
	KindTell Kind = "tell"
	// KindAsk expects an answer from the recipient's adapter.
	KindAsk Kind = "ask"
	// KindAck confirms receipt of a tell.
	KindAck Kind = "ack"
	// KindChunk is partial progress toward an answer.
	KindChunk Kind = "chunk"
	// KindDone carries the final answer and ends the exchange.
	KindDone Kind = "done"
	// KindError ends the exchange without an answer.
	KindError Kind = "error"
)

// Envelope is one message on the mesh.
type Envelope struct {
	V    int    `json:"v"`
	ID   string `json:"id,omitempty"`   // unique per request
	Corr string `json:"corr,omitempty"` // the ID this frame answers
	From string `json:"from,omitempty"` // sender's mesh name; verified by the receiver
	To   string `json:"to,omitempty"`   // recipient's mesh name
	Kind Kind   `json:"kind"`

	// Thread groups an exchange so an adapter can keep model context across
	// turns. Empty means a one-shot with no memory.
	Thread string `json:"thread,omitempty"`

	// Deadline is when the sender stops waiting. Receivers should abandon
	// work past it rather than answer into a closed connection.
	Deadline time.Time `json:"deadline,omitzero"`

	TS   time.Time `json:"ts,omitzero"`
	Body string    `json:"body,omitempty"`
}

// NewID returns a sortable unique message ID: a millisecond timestamp followed
// by random bytes, so an inbox sorts chronologically by ID alone.
func NewID() string {
	var b [8]byte
	rand.Read(b[:])
	return fmt.Sprintf("%011x%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

// Conn is a framed mesh connection.
type Conn struct {
	c   net.Conn
	enc *json.Encoder
	dec *json.Decoder
}

// NewConn wraps c in the mesh framing.
func NewConn(c net.Conn) *Conn {
	return &Conn{c: c, enc: json.NewEncoder(c), dec: json.NewDecoder(c)}
}

// Send writes one envelope.
func (c *Conn) Send(e Envelope) error {
	if e.V == 0 {
		e.V = Version
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	return c.enc.Encode(e)
}

// Recv reads one envelope. It returns io.EOF when the peer has finished.
func (c *Conn) Recv() (Envelope, error) {
	var e Envelope
	if err := c.dec.Decode(&e); err != nil {
		return e, err
	}
	if e.V != 0 && e.V != Version {
		return e, fmt.Errorf("unsupported envelope version %d", e.V)
	}
	return e, nil
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.c.Close() }

// RemoteAddr reports the peer's address, which on a tunnel connection is
// derived from the peer's node public key and is therefore an identity.
func (c *Conn) RemoteAddr() net.Addr { return c.c.RemoteAddr() }

// SetDeadline sets the read/write deadline on the underlying connection.
func (c *Conn) SetDeadline(t time.Time) error { return c.c.SetDeadline(t) }

// IsEOF reports whether err means the peer closed cleanly.
func IsEOF(err error) bool { return err == io.EOF || err == io.ErrUnexpectedEOF }
