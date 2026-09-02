// Package pair hands a mesh invite from one machine to another over the local
// network, so a person carries a short code instead of a long string.
//
// An invite has to reach the joining machine somehow. Across the internet
// something must be carried by hand -- two machines that have never met, with
// no server vouching for either, have no other way to establish that they mean
// each other. But two machines on the same network can find each other, and
// then the only thing a person needs to carry is proof that they are standing
// at both: a short code.
//
// The code is the whole security of the exchange, so the invite is encrypted
// under a key derived from it with argon2id. Someone who captures the exchange
// must brute-force the code at roughly a tenth of a second per guess against a
// 40-bit space, and the offer is open for minutes, not forever.
package pair

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/nacl/secretbox"
	"golang.org/x/net/ipv4"
)

const (
	// DiscoveryAddr is the multicast group offers are announced on. Multicast
	// rather than broadcast because it needs no socket options and behaves the
	// same on macOS, Linux and Windows.
	DiscoveryAddr = "239.255.90.98:7098"

	// HandoffPort is where an offering machine serves the encrypted invite.
	HandoffPort = 7099

	probeKind = "agent-mesh/probe/1"
	offerKind = "agent-mesh/offer/1"
)

// codeAlphabet never contains both halves of a pair people misread when copying
// between two screens: 0/O, 1/I/L, 8/B, 5/S, 2/Z, 6/G and U/V. Keeping one of
// each is what matters, not avoiding the characters themselves.
const codeAlphabet = "23456789ACDEFHJKMNPQRTUWXY"

// CodeLength is 8 characters over a 26-symbol alphabet, about 37 bits. Short
// enough to read across a desk, long enough that argon2id makes guessing it
// impractical within the minutes an offer stays open.
const CodeLength = 8

// NewCode returns a fresh pairing code.
func NewCode() string {
	b := make([]byte, CodeLength)
	rand.Read(b)
	out := make([]byte, CodeLength)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out)
}

// NormalizeCode makes a typed code comparable: case and separators do not
// matter, so "k7m2-9qpx" and "K7M29QPX" are the same code.
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if strings.ContainsRune(codeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// key derives the exchange key from the code and the joiner's nonce.
func key(code string, salt []byte) *[32]byte {
	k := argon2.IDKey([]byte(NormalizeCode(code)), salt, 1, 64*1024, 4, 32)
	var out [32]byte
	copy(out[:], k)
	return &out
}

type probe struct {
	Kind string `json:"kind"`
}

type offer struct {
	Kind string `json:"kind"`
	Mesh string `json:"mesh"`
	Port int    `json:"port"`
}

// Offering is an open invitation on the local network.
type Offering struct {
	Code string

	mesh   config.Mesh
	logf   func(string, ...any)
	pc     net.PacketConn
	tcp    net.Listener
	taken  chan string
	closed sync.Once
	done   chan struct{}
}

// Offer starts announcing this mesh on the local network and serving its
// invite to whoever proves they know the code.
func Offer(m config.Mesh, code string, logf func(string, ...any)) (*Offering, error) {
	return OfferOn(m, code, fmt.Sprintf(":%d", HandoffPort), true, logf)
}

// OfferOn is Offer with an explicit listen address, and discovery optional.
func OfferOn(m config.Mesh, code, addr string, announce bool, logf func(string, ...any)) (*Offering, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen for a joining machine on %s: %w", addr, err)
	}
	o := &Offering{
		Code: code, mesh: m, logf: logf, tcp: tcp,
		taken: make(chan string, 1), done: make(chan struct{}),
	}

	if !announce {
		go o.serve()
		return o, nil
	}
	if err := o.listenForProbes(); err != nil {
		// Discovery is a convenience; the handoff still works if the joining
		// side is told an address with --from.
		logf("local discovery unavailable (%v); the other machine will need --from <this machine's IP>", err)
	}
	go o.serve()
	return o, nil
}

// listenForProbes joins the discovery group on every interface that can carry
// multicast. Joining on the system default alone is not enough: a machine with
// a VPN, a container bridge or several NICs routinely defaults to the wrong
// one, and the failure is silent.
func (o *Offering) listenForProbes() error {
	group, err := net.ResolveUDPAddr("udp4", DiscoveryAddr)
	if err != nil {
		return err
	}
	c, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", group.Port))
	if err != nil {
		return err
	}
	p := ipv4.NewPacketConn(c)
	joined := 0
	for _, ifi := range multicastInterfaces() {
		if err := p.JoinGroup(&ifi, group); err == nil {
			joined++
		}
	}
	if joined == 0 {
		c.Close()
		return fmt.Errorf("no interface accepted a multicast join")
	}
	o.pc = c
	go o.announce()
	return nil
}

// multicastInterfaces lists the interfaces worth trying, loopback included so
// that two nodes on one machine can find each other.
func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		out = append(out, ifi)
	}
	return out
}

// announce answers discovery probes with this mesh's name and handoff port.
func (o *Offering) announce() {
	buf := make([]byte, 1024)
	for {
		n, from, err := o.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		var p probe
		if json.Unmarshal(buf[:n], &p) != nil || p.Kind != probeKind {
			continue
		}
		reply, _ := json.Marshal(offer{Kind: offerKind, Mesh: o.mesh.Name, Port: HandoffPort})
		// Answer point to point: everyone on the network may hear the
		// question, but only the asker needs the answer.
		o.pc.WriteTo(reply, from)
	}
}

// serve hands the encrypted invite to a caller that knows the code.
func (o *Offering) serve() {
	for {
		c, err := o.tcp.Accept()
		if err != nil {
			return
		}
		go o.handoff(c)
	}
}

func (o *Offering) handoff(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))

	salt := make([]byte, 16)
	if _, err := readFull(c, salt); err != nil {
		return
	}

	invite := []byte(o.mesh.Invite())
	var nonce [24]byte
	rand.Read(nonce[:])
	sealed := secretbox.Seal(nonce[:], invite, &nonce, key(o.Code, salt))

	// Whether the caller knew the code is decided on their side, by whether
	// this opens. Answering identically either way means a wrong guess learns
	// nothing beyond "something is here", which discovery already said.
	if _, err := c.Write(sealed); err != nil {
		return
	}
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	o.logf("handed the invite to %s", host)
	select {
	case o.taken <- host:
	default:
	}
}

// Addr is where this offering is serving its invite.
func (o *Offering) Addr() string { return o.tcp.Addr().String() }

// Taken reports the address of a machine that collected the invite.
func (o *Offering) Taken() <-chan string { return o.taken }

// Close stops offering.
func (o *Offering) Close() {
	o.closed.Do(func() {
		close(o.done)
		o.tcp.Close()
		if o.pc != nil {
			o.pc.Close()
		}
	})
}

// Found is a mesh discovered on the local network.
type Found struct {
	Mesh string
	Addr string
}

// Discover looks for meshes offering an invite on this network.
//
// The probe goes out once per interface rather than once overall, because the
// route a multicast packet takes by default is frequently not the one the other
// machine is on -- and the symptom is silence, which is the worst kind of bug
// to hand a person who is just trying to pair two computers.
func Discover(ctx context.Context, wait time.Duration) ([]Found, error) {
	group, err := net.ResolveUDPAddr("udp4", DiscoveryAddr)
	if err != nil {
		return nil, err
	}
	c, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	defer c.Close()

	msg, _ := json.Marshal(probe{Kind: probeKind})
	p := ipv4.NewPacketConn(c)
	sent := 0
	var lastErr error
	for _, ifi := range multicastInterfaces() {
		if err := p.SetMulticastInterface(&ifi); err != nil {
			continue
		}
		if _, err := c.WriteTo(msg, group); err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no interface could send on this network")
		}
		return nil, fmt.Errorf("cannot search the local network: %w", lastErr)
	}

	deadline := time.Now().Add(wait)
	c.SetReadDeadline(deadline)
	seen := map[string]bool{}
	var out []Found
	buf := make([]byte, 1024)
	for {
		if ctx.Err() != nil {
			break
		}
		n, from, err := c.ReadFrom(buf)
		if err != nil {
			break
		}
		var o offer
		if json.Unmarshal(buf[:n], &o) != nil || o.Kind != offerKind {
			continue
		}
		host, _, err := net.SplitHostPort(from.String())
		if err != nil {
			continue
		}
		addr := net.JoinHostPort(host, fmt.Sprint(o.Port))
		if seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, Found{Mesh: o.Mesh, Addr: addr})
	}
	return out, nil
}

// Fetch collects the invite from an offering machine, decrypting it with the
// code. A wrong code is indistinguishable from a wrong machine, which is why
// this can be tried against every responder.
func Fetch(ctx context.Context, addr, code string) (config.Mesh, error) {
	var m config.Mesh
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, fmt.Sprint(HandoffPort))
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return m, fmt.Errorf("cannot reach %s: %w", addr, err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))

	salt := make([]byte, 16)
	rand.Read(salt)
	if _, err := c.Write(salt); err != nil {
		return m, err
	}

	sealed, err := readAll(c, 64*1024)
	if err != nil || len(sealed) < 25 {
		return m, fmt.Errorf("%s did not offer an invite", addr)
	}
	var nonce [24]byte
	copy(nonce[:], sealed[:24])
	plain, ok := secretbox.Open(nil, sealed[24:], &nonce, key(code, salt))
	if !ok {
		return m, fmt.Errorf("that code does not match the machine at %s", addr)
	}
	return config.ParseInvite(string(plain))
}

func readFull(c net.Conn, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := c.Read(b[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func readAll(c net.Conn, max int) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for len(out) < max {
		n, err := c.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
	}
	return out, nil
}
