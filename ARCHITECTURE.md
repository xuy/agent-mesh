# agentmesh — architecture (proposal, awaiting review)

A named mesh where any agent that can run a CLI can address any other agent by
name, over an encrypted p2p link, with no dependency on any vendor's backend.

Status: **built and running.** Section 12 records what the build changed about
this design, and section 13 what a fresh agent gets for free.

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
// request
{"v":1,"id":"01J…","from":"master","to":"opencode","kind":"ask",
 "thread":"t-4f2","deadline":"2026-09-01T20:04:00Z","body":"is the build green?"}

// reply frames, same connection
{"v":1,"corr":"01J…","kind":"chunk","body":"checking…"}
{"v":1,"corr":"01J…","kind":"done","body":"green, 412 tests, 0 failures"}
{"v":1,"corr":"01J…","kind":"error","body":"adapter exited 1: …"}
```

`kind`:
- `tell` — fire and forget; appended to the recipient's inbox, `ack` returned.
- `ask` — request/response; the recipient's adapter produces the reply.
- `chunk` / `done` / `error` — reply frames.

`thread` groups an exchange so an adapter can keep model context across turns
(§7). Unknown fields are ignored, so the format can grow.

## 6. The node daemon (`meshd`)

One process per agent. Holds:

1. a `tailcat.Server` with `OnTCP(7020)` → the wire handler above;
2. a **client cache** — one `tailcat.Client` per peer, dialed lazily, reused,
   so only the first message to a peer pays the DERP handshake;
3. a **unix control socket** at `~/.mesh/<name>.sock` — how the local CLI (and
   therefore how *I*, via Bash) sends without paying startup cost;
4. an **inbox** at `~/.mesh/inbox/<name>.jsonl`, append-only;
5. a **registrar loop** — register with the hub, hold a long-lived subscribe
   connection for roster pushes, heartbeat every 30s, cache roster to disk.

## 7. Agent adapters — what makes this a *mesh of agents*

An adapter turns an inbound `ask` into a reply. Two, both built:

- **`mailbox`** (default; what `master` runs). The message lands in the inbox
  and the ask parks. I read it with `mesh inbox` and answer with
  `mesh reply <id> "…"`. Zero integration — works for any agent, including a
  human.
- **`exec`** (what `opencode` runs). The daemon shells out to a configured
  command and streams stdout back as the reply:
  `mesh up --name opencode --exec 'opencode run --agent build {{body}}'`.
  For opencode specifically, the daemon maps `thread` → an opencode session id
  and appends `--session <id>`, so a multi-turn exchange keeps model context on
  the far side. That is the difference between a message pipe and an agent
  mesh.

Concurrency, timeouts and a max in-flight cap live in the adapter layer, not in
each adapter.

## 8. CLI surface

```
mesh init   --name master              # generate node key + config
mesh hub                               # run the control plane; prints join token
mesh up     --hub <blob> [--exec CMD]  # run the node daemon
mesh peers                             # roster: name, pubkey, kinds, last seen
mesh ping   <peer>                     # tunnel liveness + direct-vs-DERP path
mesh send   <peer> <text>              # tell
mesh ask    <peer> <text> [--timeout]  # ask, stream the reply to stdout
mesh inbox  [--follow] [--json]        # read what came in
mesh reply  <id> <text>                # answer a parked ask (mailbox adapter)
```

`send`/`ask`/`inbox` talk to the local daemon over the unix socket, so they
return immediately and are safe to call from a tool loop.

## 9. Known limits, stated up front

- **Public DERP relays are rate-limited with no uptime guarantee** (tailcat's
  own README says so). Fine for a prototype; the fix is a self-hosted DERP,
  which is a config change (`--derpmap-url`), not a redesign.
- **The hub is a single point of discovery**, though not of traffic. If it is
  down, new peers cannot join and address changes do not propagate; existing
  peers keep working off the cached roster.
- **No store-and-forward.** Messaging an offline peer fails fast rather than
  queueing. Adding a hub-side spool later is additive.
- **tailcat has no API stability promise** and its wire format may change.
  Pinned at v0.4.0.
- Two nodes on this one Mac still bootstrap through DERP before upgrading to a
  direct path. Expect a slow first message, fast steady state — `mesh ping`
  reports which path is in use so this is observable, not folklore.

## 10. Build order

Each milestone is independently demonstrable.

1. **Data plane.** `wire`, `meshd`, unix socket, `send`/`ask`/`inbox`, peers
   from a static file. Demo: two daemons on this Mac, both mailbox, message
   round-trips. Verifies the remote-addr→pubkey attribution assumption.
2. **Control plane.** `meshhub`, register/roster/subscribe, name claiming, join
   secret, disk-cached roster, `mesh peers`. Demo: a node joins with one token
   and discovers the other by name.
3. **Agent adapters.** `exec` adapter + opencode session threading. Demo: I run
   `mesh ask opencode "…"` and a real model answer comes back — the deliverable
   the prototype was asked for.
4. **Hardening + docs.** Allowlist enforcement, timeouts, `mesh ping` path
   reporting, README with a cross-machine (`@Windows`) runbook.

Not in scope for the prototype, but the design leaves room: hub-side spool,
self-hosted DERP, an MCP server so opencode can call `mesh` as a native tool
instead of shelling out, and group/broadcast addressing.

## 11. Repo layout

```
noah-mesh/
  cmd/mesh/            # single binary, all subcommands
  internal/wire/       # envelope + framing
  internal/node/       # daemon: tailcat server, client cache, ctl socket, inbox
  internal/hub/        # control plane
  internal/adapter/    # mailbox, exec
  internal/config/     # ~/.mesh: node.key, config.json, roster.json, join.key
  demo/                # two-agent scripts
  ARCHITECTURE.md README.md
```

Module path `github.com/ericxu/mesh`, binary `mesh` — deliberately not
Noah-branded; this is infrastructure, not a Noah feature.


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
