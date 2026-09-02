# agentmesh — architecture (proposal, awaiting review)

A named mesh where any agent that can run a CLI can address any other agent by
name, over an encrypted p2p link, with no dependency on any vendor's backend.

**How to read this.** Sections 1-13 are the design as it stands. Sections 14
onwards are a log: what broke when the design met a second machine, a second
operating system and a second agent, and what each failure changed. The log is
the more useful half. Almost every real bug in it was found by running the
thing rather than reasoning about it, and several were found by the agent on
the other machine reading this one's code.

---

## 1. The problem this solves

Today Eric talks to `@Windows` by going through Claude, which means through
Anthropic's backend. That works but it is not a substrate:

- it only works from a Claude client — an opencode agent cannot originate a message;
- there is no address book: "the Windows agent" is a thing a human knows, not a name a program can resolve;
- the transport is a vendor's product surface, not a socket.

What we want instead: `mesh ask windows "is the build green?"` works from *any*
agent, on *any* machine, and the answer comes back from that agent's model.

## 2. Why tailcat is the right primitive

`github.com/tailscale/tailcat` (v0.4.0) gives us Tailscale's **data plane**
with none of its control plane: WireGuard encryption, disco NAT traversal, DERP
relay bootstrap, netstack TCP — with no Tailscale account, no root, no
tailscaled, no changes to system routing or DNS. Verified: `tailscaled` is dead
on this Mac and irrelevant to tailcat.

Two API facts drive the whole design:

- **`Server.AllowedClients []key.NodePublic` + `Client.Key key.NodePrivate`.**
  A node holds one long-lived WireGuard identity and uses it both to serve and
  to dial. Peers therefore authenticate each other *at the tunnel layer*, by
  public key. No bearer tokens, no shared secret, no TLS to configure. A peer
  not on the allowlist is silently dropped before any of our code runs.
- **`Server.Start()` runs a netcheck and a DERP handshake** — seconds, not
  milliseconds. So a process per message is out. Each agent needs one
  long-lived daemon holding one server and a cache of dialed clients.

What tailcat deliberately does *not* give us, and we therefore must build:
**names, discovery, membership, and liveness.** That is the control plane.

## 3. The two planes

```
              ┌──────────────────── control plane (discovery only) ─────┐
              │                        meshhub                          │
              │        name → {connblob, nodepub, kinds, seen}          │
              └───▲───────────────────────────────────────────▲─────────┘
        register  │                                           │  register
        + roster  │                                           │  + roster
                  │                                           │
    ┌─────────────┴──────────┐                   ┌────────────┴─────────────┐
    │ meshd  name=master     │                   │ meshd  name=opencode     │
    │ tailcat.Server :7020   │◄═════ data plane ═══════════════════════════►│
    │ adapter: mailbox       │   direct p2p WireGuard, hub not involved     │
    │ ctl: ~/.mesh/master.sock                   │ adapter: exec opencode   │
    └────────────▲───────────┘                   └────────────▲─────────────┘
                 │ unix socket                                │
          `mesh ask opencode "…"`                      `opencode run …`
            (me, via Bash)                              (the peer's model)
```

The split is the point: **the hub resolves names, it does not carry traffic.**
Kill the hub and every peer that has already exchanged roster entries keeps
talking, because the roster is cached on disk. The hub is a phone book, not a
switchboard.

## 4. Identity and naming

- A node's identity is a `key.NodePrivate` generated once by `mesh init` and
  stored at `~/.mesh/node.key` (0600). Its public half is the node's permanent
  ID.
- A node's **name** (`master`, `opencode`, `windows`) is a human label the hub
  binds to that pubkey. First registration claims the name; a later
  registration of the same name with a different pubkey is **rejected**, so a
  name cannot be stolen once claimed.
- A node's **address** is its tailcat `ConnBlob` — regenerated on every daemon
  start (the DERP region is embedded), so it is republished on each register
  and refreshed by heartbeat. Names are stable; addresses are not. That is
  exactly the DNS relationship and it is why the hub exists.
- **Mutual auth:** on receiving a roster, each node calls `AddAllowedClient`
  for every peer pubkey. Inbound tunnels from anyone else never reach our code.
- **Sender attribution:** a tailcat node's IPv6 address is derived from its
  node public key, so `conn.RemoteAddr()` on an accepted connection maps back
  to exactly one roster entry. The claimed `from` in the envelope is checked
  against that derived address and a mismatch is dropped. *(Confirmed: the
  derivation is Tailscale's ULA prefix `fd7a:115c:a1e0::/48` with the low 80
  bits taken from the node key. Reproduced in `ident.Addr` and pinned by a
  test, so no in-band handshake is needed.)* Only 80 bits of the key appear in
  the address, which is not a credential on its own -- it does not need to be,
  because WireGuard already refused the tunnel unless the peer's full key was
  allowlisted. The address only disambiguates among peers already authenticated.
- Joining the mesh requires the hub's ConnBlob **and** a join secret
  (`~/.mesh/join.key`). One is an address, the other is authorization; the hub
  ACL is what stops anyone who scrapes a token from registering.

## 5. Wire protocol (data plane, port 7020)

Newline-delimited JSON. One connection per request, kept open for the reply so
long-running agent work can stream.

```jsonc
// a question, with a file attached
{"v":1,"id":"1a05…","from":"mac","to":"windows","kind":"ask","thread":"t-4f2",
 "deadline":"2026-09-02T20:04:00Z","body":"why did this crash?",
 "files":[{"name":"crash.log","size":38724,"sum":"5097…"}]}

// the attachment, in frames, immediately after
{"v":1,"corr":"1a05…","kind":"file","index":0,"chunk":"…base64…"}
{"v":1,"corr":"1a05…","kind":"file","index":0,"last":true}

// reply frames, same connection
{"v":1,"corr":"1a05…","kind":"chunk","body":"reading it…"}
{"v":1,"corr":"1a05…","kind":"done","body":"a nil map write on line 88"}
```

`kind`:
- `tell` — fire and forget; appended to the recipient's inbox, `ack` returned.
- `ask` — request/response; the recipient's adapter produces the reply.
- `file` — one frame of an announced attachment (§22).
- `chunk` / `done` / `error` — reply frames.

`thread` groups an exchange so an adapter can keep model context across turns
(§7), and either side may send on a thread the other started — which is what
lets a delegated task come back with a question (§21).

`type` and `data` carry an application's own vocabulary and payload, and the
mesh never looks inside either (§21). Unknown fields are ignored, so the format
can grow without a version bump.

## 6. The node daemon

One long-lived process per agent, because bringing up a tunnel costs a netcheck
and a relay handshake and no one should pay that per message. It holds:

1. a `tailcat.Server` serving the wire protocol on 7020, and the control plane
   on 7010 if this node coordinates (§14);
2. a **client cache** — one client per peer, dialed lazily and reused, rebuilt
   when the peer's address or start time changes (§14);
3. a **control socket**, how the local CLI reaches the daemon in microseconds;
4. an **inbox** and an **audit log**, append-only and size-capped;
5. a **registrar loop** holding one connection to the coordinator, which
   carries registration, liveness and roster pushes at once;
6. a **policy store** consulted per message (§18);
7. a **relay watchdog**, because a node can be healthy locally and unreachable
   from everywhere else, and that is invisible without one (§16).

## 7. How a question gets answered

A **delivery mode** decides how an inbound question reaches whoever answers it.
This is the only per-agent work the mesh ever requires, which is what keeps
adding an agent cheap:

- **`mailbox`** parks the question for `mesh reply`. No integration at all, so
  it works for any agent and for a human. The default.
- **`exec`** runs a command and streams its stdout back — a fresh agent
  session. The question arrives in `$MESH_BODY` and on stdin, never
  interpolated into a command line. `$MESH_CONTINUE` says whether this thread
  has been seen before, which is how a multi-turn exchange keeps model context
  on the far side.
- **`webhook`** POSTs into a resident agent's local API, reaching a session
  that is *already running* with its context. It accepts either an answer in
  the response or a bare acknowledgement followed later by `mesh reply`, which
  is how a resident assistant actually behaves (§14).
- **`notify`** parks the question but runs a command first, so an idle agent or
  a human finds out it is there.

Concurrency, timeouts and streaming live in the adapter layer rather than in
each adapter.

## 8. CLI surface

```
mesh join      join or found a mesh, and start answering
mesh connect   register the mesh with the agent harnesses installed here
mesh service   keep this node running across reboots and crashes
mesh invite    a string, or --lan for an eight-character pairing code

mesh peers     who is here and what they are for
mesh ask       ask a peer, or @group, and wait for the answer
mesh send      tell a peer or @group; --file attaches a file
mesh wait      block until a peer speaks, then exit
mesh waiting   questions addressed to you · mesh reply answers one
mesh inbox     everything said to you · mesh log what was asked, refusals too

mesh trust     what each peer may do here
mesh allow / deny / block / unblock / verify / forget
mesh id        this node's fingerprint

mesh status / doctor / guide / ping / group / version
mesh mcp       serve the mesh as tools for a harness that speaks MCP
```

Everything that touches the mesh goes through the local daemon over a unix
socket, so a command returns in microseconds and is safe to call from a tool
loop. Every command takes `--json`.

## 9. Known limits, stated up front

- **No store-and-forward yet.** Messaging an offline peer fails immediately
  rather than queueing.
- **The public DERP relays are rate-limited with no uptime guarantee** —
  tailcat's own README says so. They are bootstrap and fallback; a self-hosted
  relay is a config change (`--derpmap-url`), not a redesign.
- **A blocked peer can still open a tunnel**, it just cannot say anything
  through it: tailcat can grant a peer key at runtime but not revoke one, so
  refusal happens a layer up, per message (§18).
- **The coordinator accepts tunnels from nodes it does not know**, because that
  is what joining is (§14). It still refuses to talk to anyone outside the
  roster.
- **Discovery has a single point**, though traffic never does. While the
  coordinator is down, existing peers keep talking off cached rosters.
- **A desktop chat driving the mesh has this node's authority**, and that model
  reads web pages. Peer-side controls are the defence; giving the desktop
  surface less authority than the agent beside it is not built.
- **tailcat has no API stability promise** and its wire format may change.
  Pinned at v0.4.0.

## 10. What was built, in order

The plan was four milestones and it survived contact roughly intact: data
plane, control plane, adapters, hardening. What it did not predict is in the
log from §14 onwards — that a node needs two keys, that a coordinator must not
allowlist, that pinning an address breaks the tunnel cache, and that a task
model was the wrong feature entirely.

## 11. Repo layout

```
agent-mesh/
  cmd/mesh/            # the single binary: every subcommand, the skill, the guide
  internal/wire/       # the envelope and its framing
  internal/node/       # the daemon: tunnel, client cache, control socket,
                       #   inbox, attachments, the relay watchdog
  internal/hub/        # the control plane, run inside the coordinator's daemon
  internal/adapter/    # how a question is answered: mailbox, exec, webhook, notify
  internal/policy/     # who may do what here, and how fast
  internal/pair/       # LAN pairing: discovery and the encrypted handoff
  internal/connect/    # registering with the agent harnesses on this machine
  internal/service/    # launchd, systemd, Task Scheduler
  internal/ident/      # keys, fingerprints, and the address derivation
  internal/config/     # everything on disk, and the invite format
  demo/                # runnable: two agents, a delegation, a resident-agent stub
  docs/                # the bar this had to clear, and where it goes next
```

Module path `github.com/xuy/agent-mesh`, binary `mesh`.

## 12. What the build changed

Four things the design above got wrong or left out, found by running it.

**A node needs two node keys, not one.** A DERP relay keys its clients by node
public key and admits one connection per key. A node that both serves peers and
dials them holds two DERP connections at once, so a single identity would have
had the second connection evict the first. `ident.Identity` therefore carries a
`Server` key (the node's address, what its ConnBlob encodes) and a `Client` key
(its caller identity, what peers allowlist and attribute). Both are published
in the roster; both are permanent.

**The hub had to grow a liveness timeout, and name takeover.** A node that dies
takes its tunnel with it, and netstack may never deliver a FIN, so the hub's
read on that node's control connection blocked forever -- holding the name and
locking the node out of its own identity on restart. Two fixes: a 90-second
read deadline on the control connection (nodes ping every 30s), and a
registration carrying the same *server* key takes its name back rather than
being refused. A node also closes its hub connection on shutdown so the common
case needs no timeout at all.

**The hub's address must be pinned, not regenerated.** A ConnBlob encodes both
the key and the DERP region, so an ephemeral hub key would have invalidated
every invite ever handed out -- the mesh's one bootstrap address would silently
rot across a restart. The hub persists its key *and* the region chosen by the
first netcheck.

**Progress streaming defaults off.** An exec adapter streams its output line by
line and then returns the same text as the answer, which printed everything
twice. `mesh ask` now shows progress only with `--stream`, so stdout is the
answer and nothing else.

One environment trap worth recording: on macOS, `cp`-ing a new binary over an
existing one that has been executed invalidates its code signature in place,
and the kernel then kills it on exec with no output at all. `make install`
writes to a temporary path and renames.

## 13. Agent ergonomics

A mesh nobody can join is not a mesh, so joining and learning are part of the
product rather than documentation around it.

- **`mesh join` takes no arguments on a machine that already knows its mesh.**
  It detects whether it is being run by Claude Code or opencode, picks a name,
  generates an identity, starts the daemon, registers, and prints the roster.
- **`mesh invite` is the entire cross-machine story:** one secret string,
  `mesh join --invite <it>` on the other side.
- **`mesh guide` prints the full agent-facing reference with the live roster on
  top**, so an agent learns the mesh and who is on it in one command, offline.
  (tailcat does the same with its own README, for the same reason.)
- **`mesh install-skill` writes a `join-mesh` skill** for Claude Code and links
  it for opencode, so the *next* session on the machine can find and use its
  peers without being told any of this.
- **`mesh mcp` serves the mesh as MCP tools** for agents that would rather have
  tools than a CLI. Its peer listing is deliberately trimmed of keys and relay
  addresses, which are the daemon's business and pure token cost to a model.
- **`mesh doctor` ends every finding with the command that fixes it**, and so
  does every error the CLI can return.
- Every command takes `--json`.

## 14. Round two: cross-platform, hubless, and reaching a live agent

The prototype proved the wire. This round made it something other people could
use: it runs on Windows, it needs no server, and a message can reach an agent
that is already running rather than starting a fresh one.

### The coordinator moved inside a node

`mesh hub` as a separate process was an adoption tax and it undercut the "no
server" claim. The control plane now runs inside the first node's daemon, on
`wire.HubPort` of the tunnel that node already has. The first agent to join
founds the mesh and coordinates it; joining a second machine is one invite and
one command, with no third process anywhere.

Standalone `mesh hub` still exists for a dedicated coordinator, and the hub
package no longer owns a server: `Hub.Handle` serves a connection, and
`Hub.RegisterLocal` registers the node that hosts it without one, so the
coordinator is not a special case in its own roster.

### Addresses are now pinned, which broke the tunnel cache

A ConnBlob encodes the node's key *and* its DERP region, and the region was
chosen by a latency check at every start. That was survivable when only the hub
published an address; once a node's own address became the mesh's bootstrap
address, a restart would silently invalidate every invite. So each node now
pins the region its first start chose.

That fixed invites and broke something else. Addresses had been an accidental
restart signal: when a peer came back with a new blob, the cached tunnel to it
was obviously stale and got rebuilt. With stable addresses the cache looked
valid while pointing at a dead WireGuard session, and dials hung until their
deadline. Roster entries now carry `Started`, the peer's daemon start time, and
a cached client is rebuilt when either the address or that changes. A dial that
fails also drops its cached client, because a failure is evidence the path is
dead whatever the roster still says.

### The coordinator cannot allowlist

tailcat's `AllowedClients` is per-tunnel, not per-port, and it is
allow-everything until the first key is added. A coordinator that allowlisted
its peers would therefore drop the tunnel of every node that had not joined
yet -- which is every node that is trying to join. The mesh became unjoinable
the moment it had one member, and it only worked at all in early testing
because of the order things happened to start in.

Coordinators no longer allowlist. Identity is still enforced above: an inbound
connection from a caller who is not in the roster is refused before its message
is read, and registering requires the mesh's join key. Plain nodes still
allowlist, so this weakens exactly one node in the mesh, and the fix if that
ever matters is a second tunnel for the control plane rather than a change of
model.

### Delivery modes

An adapter used to answer a question. A *delivery mode* now decides how the
question reaches whoever answers, which is the distinction that matters once
the answering agent is already running:

- `mailbox` parks it for `mesh reply` -- works for anything, including a human.
- `exec` runs a command in a fresh agent session.
- `webhook` POSTs to a resident agent's local API, reaching a live session with
  its context. The endpoint may answer in the response body or acknowledge and
  answer later through `mesh reply`; the adapter accepts both, because a
  resident assistant normally does the second.
- `notify` parks the question but runs a command first, so an idle agent or a
  human finds out it is there.

The posted payload carries its own `reply_with` instruction and a note marking
the content as peer input rather than a user instruction. That is deliberate:
it means an agent with no integration written for it can still take part, which
is the whole O(N) claim, and it puts the trust boundary in the message itself
rather than only in documentation.

### Windows

`Setsid` is Unix-only; Windows detaches with `DETACHED_PROCESS |
CREATE_NEW_PROCESS_GROUP`. State lives under `%LOCALAPPDATA%`. The exec adapter
runs `powershell -NoProfile -NonInteractive -Command` rather than `sh -c`, so an
adapter command refers to the question as `$env:MESH_BODY`. `SIGTERM` cannot be
sent to a detached process, so `mesh down` goes through the control socket,
which works everywhere.

The control socket stays a unix socket on every platform: Windows has supported
AF_UNIX since Windows 10 1803 and Go speaks it there, which keeps filesystem
permissions as the gate instead of opening a loopback port any local process
could reach. If that ever disappoints on a real Windows box, the fallback is a
named pipe with an explicit ACL -- not a TCP port.

## 15. Carrying an invite without carrying a string

An invite has to reach the joining machine somehow. Across the internet that is
unavoidable: two machines that have never met, with no server vouching for
either, have no way to establish that they mean each other unless a person
carries something. tailcat has the same property -- its token is exactly this,
handed over out of band -- and so does Tailscale, whose equivalent is a login.

But that argument only holds for machines that cannot find each other. Two
machines on the same network can, and then the only thing a person needs to
carry is proof that they are standing at both. `mesh invite --lan` announces the
mesh on a multicast group and serves its invite to whoever proves they know an
eight-character code; `mesh join --lan --code <code>` finds it and collects it.
The invite is encrypted under a key derived from the code with argon2id, so
capturing the exchange leaves an attacker brute-forcing 37 bits at roughly a
tenth of a second per guess, against an offer that is open for minutes.

The code alphabet never contains both halves of a pair people misread when
copying between two screens -- 0/O, 1/I/L, 8/B, 5/S, 2/Z, 6/G, U/V. Keeping one
of each is the property that matters, not avoiding the characters themselves; a
test asserts it, and it caught two mistakes in the first alphabet.

Discovery probes every interface rather than the system default. A machine with
a VPN, a container bridge or several NICs routinely defaults to the wrong one,
and the symptom is silence -- the worst thing to hand someone who is trying to
pair two computers.

### The relay number that was not a relay number

Shortening the address for the pasted invite exposed a real bug underneath it.

`Server.ConnBlob()` embeds the chosen relay's whole record, which is most of a
long invite, so the obvious shortening is to name the relay by number instead.
The number in the embedded record, though, is not the relay's public number:
tailcat renumbers it to 1 on the way in. Copying it out produced an address
naming relay 1, which does not exist -- every node that used one failed with
"no such region in derpmap.json".

The same mistake had already been sitting in the region pinning added last
round. It read the region back out of the address it had just produced, got the
renumbered value, and stored nothing useful. Addresses had stayed stable across
restarts only because a latency check run twice in the same place tends to pick
the same relay, which is luck rather than the property that was claimed.

Both are fixed by measuring once and remembering the answer: a node calls
`PickBestRegion` on its first start, stores the public region id (301 for New
York, and so on), pins its relay to it, and publishes an address that names it.
A region cannot be recovered from an address after the fact, so it has to be
kept when it is known. Where it is not known, the address is left long: a long
address that works beats a short one that does not.

The invite went from 398 characters to 161, and about half of what remains is
the two public keys, which is the part that cannot be compressed.

## 16. Staying up without anyone watching

Two machines were live for about an hour before the first thing broke, and it
broke in the way that matters most: a coordinator died and nothing came back.

The daemon was already good at recovering from things it could see. It retries
the coordinator with backoff, keeps the roster on disk so peers stay reachable
while discovery is down, rebuilds a tunnel whose peer restarted, and drops a
cached client whose dial failed. What it could not do was survive its own
death, or notice a peer that died silently.

**Survive its own death.** `mesh service install` registers the node with the
platform's service manager rather than inventing a supervisor: launchd with
KeepAlive, a systemd user unit with Restart=always and a note about lingering,
a Task Scheduler logon task on Windows. `mesh doctor` reports a node that is not
registered, because a mesh that stops at the next reboot is a demo.

**Notice a peer that died silently.** A coordinator killed with SIGKILL takes
its tunnel with it, and netstack has nothing to report to the far side, so a
node's read on its control connection blocked forever: the tunnel was gone, the
node was holding a dead connection, and it never re-registered. This is exactly
the bug fixed on the hub side in section 12, and it was still sitting on the
node side -- one direction had a deadline and the other did not. Both ends now
do.

Recovery is bounded by that deadline, so the keepalive and the deadline were
tightened together: a ping every 15 seconds, and 45 seconds of silence before
the connection is rebuilt. Two missed replies is already conclusive; a
comfortable margin here buys nothing except a longer outage.

Measured end to end, with the coordinator killed by SIGKILL and nobody
touching anything: 2 seconds for launchd to restart it, 47 seconds until the
peer had re-registered and the mesh was whole.

The remaining case that still needs a person is a coordinator that moves to a
different relay -- a laptop that travels, whose pinned region is now the wrong
one. Existing peers keep working from their cached roster, but an invite handed
out earlier points at the old relay. Re-pinning on a large latency change, and
telling peers the new address over the connection they already hold, is the fix,
and it is not built yet.

## 17. Being reachable without polling

Two agents on a mesh still could not work together unattended, because neither
could notice a message. Both were on `mailbox` delivery, which parks a question
until someone runs `mesh waiting` -- and an agent sitting at its prompt never
does. The human went back to being the messenger, which is the thing this
project exists to delete.

`mesh wait` blocks until a peer says something, prints it, and exits. That
shape is deliberate: every agent harness already knows how to run a command in
the background and report when it finishes, so a message becomes an event the
agent is handed rather than a habit it has to remember. No new integration, no
daemon-to-agent protocol, no polling loop -- one blocking command and an exit
code.

Inside, the daemon keeps a set of subscribers and notifies them as inbound
messages arrive. The channels are buffered and dropped on overflow: a watcher
that has stopped reading must never be able to stop the node answering its
peers. Anything already parked counts as arrived, so a peer blocked on us right
now is reported immediately rather than after the next message; and
`--since <id>` reports what landed while the agent was busy, so nothing falls
between two waits.

This is the generic form of the `mcp` delivery mode from section 14 -- the
adapter that costs nothing per agent -- and it is what makes the mesh usable by
an agent whose harness has no hooks at all.

### A peer must not become a stranger

Testing it turned up a third instance of the family of bugs this design keeps
producing. The Windows node had already fixed one: a coordinator restart pushes
a roster holding only the nodes that have reconnected so far, and writing that
through erased the on-disk cache at exactly the moment it was the only way left
to reach anyone.

The same wipe was still happening in memory. A restarted coordinator forgot
every peer, so a legitimate node's next message was refused with "you are not in
the roster" -- wrong, and alarming, for the whole reconnection window. The
roster now merges rather than replaces: presence comes from the live roster,
identity persists. A peer that is not currently connected shows as offline
instead of vanishing, which is also simply more useful to look at.

The general lesson, three times over: a roster is two different things wearing
one name. Who is reachable right now changes constantly and should be replaced
wholesale. Who exists at all changes rarely and must never be dropped because a
transport hiccuped.

### Two daemons for one node

The already-running check lived inside the branch that detaches, so
`mesh up --foreground` skipped it -- and that is precisely the command a
service manager runs. Installing the service while a hand-started daemon was
up therefore started a second one for the same node.

That is worse than it sounds. Both daemons share the node's key, and a DERP
relay admits one connection per key, so the second silently evicts the first
from the relay: a node that appears to be running and cannot be reached. The
check now happens before anything starts, in both modes.

The same install path also stopped the running daemon without saying so, which
from the outside is indistinguishable from a crash. It says so now, and waits
for the control socket to be released before handing over.

## 18. Who is allowed to do what

Being on the mesh lets a peer talk to a node. Whether it can make that node
*work* is a separate question, and conflating the two is how an agent mesh
becomes a remote code execution service with extra steps.

Two defences, both in `internal/policy`, both consulted per message rather than
per connection -- so a decision made a moment ago binds the next request on a
tunnel the peer already has.

**Identity.** A peer's key is pinned on first contact. A different key arriving
under a name that is already taken is refused and the original key is kept:
accepting silently is exactly how a name gets stolen, and from here an
impersonation and a rebuilt node are indistinguishable, so a person has to say
which. `mesh forget <peer>` is that decision, made explicitly. `mesh id` prints
a short grouped fingerprint to read across a desk, and `mesh verify` records
that someone did -- unverified peers are still trusted, but the roster says
plainly that they are trusted because they turned up first.

**Authority.** Telling is always allowed: a message that lands in an inbox costs
nothing and commits no one. Asking is what spends tokens and runs commands, and
whether a stranger may do it depends on what the node does with a question. A
mailbox node's work is showing a human a question, so it starts open -- the
human is the check. An `exec` or `webhook` node's work is running a command or
waking a live agent, so it starts closed. That default is derived from the
delivery mode rather than configured, because the blast radius is the thing
that actually differs and asking an operator to notice that themselves is
asking them to get it wrong.

A refusal names the command that would grant what was refused, so the peer can
act instead of guessing.

Everything a peer asks is recorded, including what was refused -- `mesh log`.
Refusals never reach the inbox, so without a separate record the interesting
half of the history would be the invisible half.

Verified end to end on a live mesh: an exec node accepted a `tell` from an
unvouched peer and refused its `ask`; `mesh allow` opened it; `mesh block` shut
it again on the very next message with no restart, which retires the "peer
removal needs a daemon restart" limitation the README had carried since the
first round. tailcat still cannot revoke an allowlisted key, so a blocked peer
can open a tunnel -- it simply cannot say anything through it.

What is not built: rate limiting. A peer that is allowed to ask can ask as often
as it likes, and one agent in a loop can still spend another's tokens.

## 19. Groups, and a limit on how fast a peer may talk

**Groups are local.** A group is one agent's view of who it works with, not a
fact about the mesh, so it lives in the node's own directory. Creating one takes
no coordination, no agreement and no round trip, and two agents may disagree
about what "builders" means without either being wrong. `@all` is built in and
means whoever is on the mesh right now.

A group ask fans out concurrently -- these are model calls, and asking five
agents in sequence takes five times as long for no reason -- and every answer is
printed under the name it came from, because an unattributed wall of answers is
worse than useless when they disagree. A member being unreachable is reported
and does not fail the others; only every member failing is an error.

Members that have left the mesh are dropped at send time rather than at removal
time, so a group does not rot into a list of names that no longer resolve.

**Rate limiting** is a token bucket per peer, defaulting to sixty messages a
minute with a third of that available as burst. The burst matters: an agent
fanning a question out and reading answers back is a legitimate flurry and must
not look like a runaway. The threat here is not malice, it is a retry loop with
no backoff -- the most ordinary bug there is -- and the point is to stop it in
seconds rather than after it has spent another agent's tokens for an hour.

Order matters in the checks, and it is not arbitrary. Identity first, then
blocking, then rate, then authority. A blocked peer must be told it is blocked
rather than told to slow down, because those call for completely different
actions. And rate comes before authority so a peer hammering a node it is not
even allowed to ask cannot make that node write an audit line per attempt.

### Registering is a promise

A node used to register the moment its tunnel started, which published an
address that did not answer yet. A peer dialling it failed rather than waited,
because the first contact with a tailcat server has its own ten-second ceiling
inside the library regardless of the caller's deadline -- so a generous timeout
did not help.

Registration now waits for the tunnel to have a relay home, which is what makes
inbound connections arrivable. If that does not happen the node still starts,
but says it cannot promise to be reachable rather than promising and failing.

First contact with a peer also retries once with a fresh client. A peer that
started moments ago, or restarted while we held a cached tunnel, fails the
first attempt and succeeds on the second, and retrying here is cheaper than
making every caller understand why.
## 20. Keeping a Windows node alive, and three things that are not what they look like

The Windows service backend registers a Task Scheduler task. Three of its
obvious forms are wrong, each in a way that fails silently, so all three are
recorded here rather than rediscovered.

**`/SC ONLOGON` cannot be used, and the reason is not the folder.** It fails
with "Access is denied" unless the shell is elevated. Isolated by elimination
on Windows 11, unelevated:

| form | result |
|---|---|
| `/SC ONCE`, `/SC HOURLY`, `/SC MINUTE` | SUCCESS |
| `/SC ONLOGON`, `/SC ONSTART` | Access is denied |

Not the task folder -- the root, `\name`, and a subfolder all fail the same way
-- and not `/RL LIMITED`. A logon trigger scoped to one named `UserId`, which
is what an XML definition can express and the command line cannot, needs no
elevation at all. So the backend writes XML. That is also the only way to reach
the settings below, none of which has a flag.

**`RestartOnFailure` is not launchd's `KeepAlive`.** It covers a task that fails
to *start*. A process that exits non-zero counts as the task *completing*: kill
the daemon and Task Scheduler records `Last Result: 1`, state `Ready`, and does
nothing, verified by waiting 200 seconds. Crash recovery instead comes from
repetition -- the task re-runs every minute forever, and
`MultipleInstancesPolicy IgnoreNew` makes each tick a no-op while the daemon is
alive, so it only has an effect once it is not. Measured: killed at 21:43:44,
running again at 21:44:37.

**The repetition has to hang off a `TimeTrigger`, not the `LogonTrigger`.** A
trigger's repetition arms when that trigger fires, so a logon trigger repeats
nothing until the next logon -- which never comes for the session that just
installed it. With the repetition on the logon trigger the watchdog looks
correct, reports no error, and does not fire; `Next Run Time: N/A` is the tell.

Two defaults would otherwise end the node quietly. `ExecutionTimeLimit` is 72
hours, so the daemon is killed three days in and reads as a node that vanished
for no reason. `DisallowStartIfOnBatteries` and `StopIfGoingOnBatteries` are
both true by default, so on a laptop the mesh ends when the charger comes out.
Both are set explicitly.

Two things that remain true and are worth knowing. A task created by an
elevated process cannot be replaced or deleted by an unelevated one, so a
machine that installed the old elevated way needs one elevated `schtasks
/Delete` before the unelevated path works. And the running daemon holds its own
binary open: on Windows an executable cannot be overwritten or deleted while it
runs, so `make install`'s `cp` then `mv -f` fails outright. Renaming a running
binary *is* allowed, so the upgrade is rename-then-move, which is the opposite
of the macOS advice in AGENTS.md and for an unrelated reason.

## 21. Why there is no task model

The obvious next feature was tasks in the mesh: an id, a state machine,
progress, cancellation. It is the wrong feature, and the reasoning is worth
keeping because the argument for it is superficially good.

agent-mesh is a substrate. Its job is that a named agent can reach another
named agent, reliably and privately, from anywhere. A task model is an opinion
about how work should be structured, and there are several good ones. Baking
one in decides for everybody and is wrong for somebody.

The test is whether communication alone is sufficient, and `demo/handoff.sh`
runs it: a desktop chat hands work to a coding agent, the agent needs a
clarification, asks it on the same thread, gets an answer, and reports back.
Four messages, one thread, both directions, no task type on the wire. The task
is a convention the two ends share, and a different pair can share a different
one.

What the substrate owes them is that the convention is expressible, so the
envelope carries `Type` and `Data` -- an application's own vocabulary and its
own payload -- and never looks inside either. Two fields, and applications get
a protocol while the substrate keeps its lack of opinion.

Writing that demo also found the last real gap in waiting. It hung the first
time, because `mesh wait` only heard what came *next*: a message that arrived
while the agent was working was missed entirely. It now keeps a read cursor and
reports what was missed before blocking. An agent that has to track an id
between turns to avoid losing messages is an agent that will lose messages.

The remaining substrate gaps are small and none of them are a task model:
durable delivery for an offline peer, and sending a file. Everything the task
model was going to provide -- delegation, lifecycle, questions asked back,
progress -- turned out to be an application built on those.

## 22. Files are a communication primitive

Agents produce things that are not sentences: a patch, a build log, a
screenshot, a profile. Without a way to send one, every application on the mesh
invents base64 in a message body, and invents it badly.

An attachment travels with the message that announced it, as a run of frames
after it. The two are one unit: if a transfer is cut short the message is
refused rather than delivered with half a file, because an agent handed a
truncated log will reason about it as though it were whole. A SHA-256 announced
up front is what makes that detectable -- a byte count would not catch a
transfer that stopped between chunks of the right size.

Chunks are base64 inside the JSON stream rather than a binary framing beside
it. The third saved is not worth a second framing on the same connection, which
is a reliable source of bugs.

Two things are hostile input and treated that way. The **name** is chosen by the
peer, so it is reduced to a single path element with separators, leading dots
and unusual characters stripped -- a peer must not choose where a file lands.
And the **size** is capped, because a peer can make this node write to disk.

The sender's **local path is never sent**. It is carried internally so the
sender can read the file, and cleared before the announcement goes on the wire:
where a file happens to live is nobody else's business, and leaking it hands a
peer a map of the sender's filesystem.

Verified across the real mesh: a 39KB file from the Mac arrived on the far node
byte for byte, checksum matching.

## 23. Registering with the agents someone actually runs

"Works with your agents" is true or it is marketing, and the difference is
whether a person has to hand-edit four config files in three formats before
anything happens. `mesh connect` finds the harnesses installed on this machine
and registers with each.

Seven are covered: Claude Code, Claude Desktop, the Codex CLI and the ChatGPT
desktop app that shares its harness, Cursor, Gemini CLI, Zed, and opencode.
Each gets the best integration it can accept rather than the same one
everywhere -- a skill where skills exist, a tool server where that is all there
is.

The rule that shapes the code: **never damage a config we do not fully
understand.**

- A file that parses as plain JSON is merged and a backup left beside it. Other
  entries and unrelated settings survive; re-running changes nothing and says
  so, because people re-run setup commands constantly.
- A file that does not parse is **not rewritten**, and the snippet is printed
  instead. Corrupting an editor config someone spent an afternoon on, to save
  them one paste, is not a trade worth making.
- Codex's config is TOML, so the entry is appended rather than the file being
  reparsed and rewritten: appending is the one edit that cannot reorder or drop
  somebody else's settings.
- opencode's config may contain comments, so it is never rewritten as plain
  JSON -- that would silently delete them, and a comment explaining why a
  setting exists is worth more than a saved paste. It gets the skill, which is
  its native surface anyway.
- Claude Code's user config holds session state as well as settings, so this
  shells out to `claude mcp add` rather than reaching into it. Editing a tool's
  state file to avoid a subprocess is how you corrupt someone's history.

All of it is tested against a sandboxed home, including that an unparseable
file comes back byte-identical. And the surface those configs point at is
checked against a client we did not write: `demo/smoke.sh` drives the tool
server the way a harness does, and where the Claude Code CLI is installed it
also asks a real MCP client to connect and report health. Our own JSON-RPC
agreeing with itself proves considerably less.

One trap worth naming, because it is the shape of every future bug here: the
tool-server entry is *close enough* between harnesses to look universal and is
not. Zed ignores an entry without a `source` field, and it fails silently --
the server simply never appears, with no error anywhere. So each target names
its own entry builder rather than inheriting a shared one, and a test asserts
both that Zed gets the field and that nobody else does. A config that is subtly
wrong is worse than one that is missing, because nothing reports it.

## 24. The quickstart did not work

`demo/smoke.sh` exercises every claim the README makes, on a throwaway mesh
with real tunnels and real relays, because the things that break are the things
only a real tunnel exercises and none of them show up in `go test`.

The first run failed six of sixteen, and the most useful failure was the
README's own quickstart. It tells a reader to start an exec node and then ask
it something — and an exec node starts closed, so the answer was a refusal.
Correct security, and a product that does not work out of the box.

The fix is a rule rather than a weakening: **the mesh's coordinator starts
trusted, everyone else does not.** Joining a mesh is how a node says yes to the
node whose mesh it is. Someone had to hand over the join key and someone had to
accept it, and requiring a second per-pair approval afterwards is friction that
buys nothing — a stranger cannot become the coordinator without already holding
the address everyone dials. Being on a mesh together is a weaker relationship
than being the node whose mesh it is, and only the second one is implied
consent.

`mesh allow --all` covers the honest common case of a person whose nodes are
all their own. It grants only to peers already known, so it is not a door left
open for whoever turns up next.

Two of the six failures were the test being wrong rather than the program,
which is worth saying: a smoke test that has never been wrong has probably
never been strict.

### One capability set, whichever way a client reaches the mesh

Group addressing was implemented in the CLI, which meant an agent reaching the
mesh through MCP could not use it: the same daemon, the same mesh, a smaller
product depending on how you knocked. That is the failure mode a project with
several front doors falls into, and it is worth naming because it will happen
again.

The rule: **a capability belongs to the daemon or to a shared helper, never to
one front door.** Group expansion and fan-out are now shared, so `mesh ask
@builders` and a desktop agent calling `mesh_ask` with `@builders` do the same
thing, including `@all`.


## 25. `mesh down && mesh up` did not work

Found by the smoke test after the offline queue landed, and it is the kind of
bug that hides behind a passing test suite: the queue was fine, the peer just
never came back.

`mesh down` returned as soon as the daemon acknowledged the stop, and the
daemon exits a moment after acknowledging. So `mesh down && mesh up` was a race
the second command lost -- it found the control socket still live, reported
"already running", and started nothing. The node stayed down, and everything
downstream looked like a delivery failure.

`mesh down` now waits for the socket to actually stop answering. That sequence
is advice this program gives in several of its own error messages, which is
exactly why it needed to work: telling someone to run two commands that do not
compose is worse than telling them nothing.
