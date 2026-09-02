# agent-mesh

**Your agents, on every machine you own, reachable by name — with no server in
the middle.**

```
$ mesh ask windows "what's eating disk on the D drive?"
D: is 2.3TB, 296GB used, almost entirely one thing: SteamLibrary at 296.9GB.
Everything else rounds to zero. C: is the one that's actually tight -- 430GB
used, 220GB free.
```

That is a real exchange, not an illustration. It went from a Mac to a Windows
PC over an encrypted peer-to-peer link: no relay service, no account, no hosted
agent, no port forwarding. The machine that answered is running an agent of its
own, and it answered because it chose to — nothing proxied a command into it.

Built on [tailcat](https://github.com/tailscale/tailcat) — Tailscale's data
plane (WireGuard, NAT traversal, DERP for bootstrap) without its control plane.
No Tailscale account, no `tailscaled`, no root, no changes to your routing.

---

## Why this exists

Coding agents are stuck on the machine they were started on. The moment work
spans two computers — your laptop and the build box, your Mac and the Windows
machine with the GPU — *you* become the messenger, copying context between
terminals by hand.

Every product that fixes this builds the same thing: a hosted service, a tunnel
down to an agent on your machine, and your conversations through both. Nobody
chose that because it was good. It is what you build when peer-to-peer is hard.

It stopped being hard. So:

    your agent  ──encrypted p2p──>  the machine that has the thing

No middle. Not a smaller middle, or a more private middle. None. On a LAN the
connection upgrades off the relay entirely and runs at **1ms**.

## Install

macOS, Linux and Windows, on amd64 and arm64. One static binary.

    curl -fsSL https://raw.githubusercontent.com/xuy/agent-mesh/main/install.sh | sh

or `go install github.com/xuy/agent-mesh/cmd/mesh@latest`, or take a binary
from [Releases](https://github.com/xuy/agent-mesh/releases), or build it with
`make install`.

## Two minutes to a working mesh

There is no server to run. The first agent to join founds the mesh and
coordinates it from inside its own daemon.

**On the first machine:**

    $ mesh join --name mac --mesh home --note "my laptop; ask me about the repos"
    created mesh "home" with mac as its coordinator
    You are "mac" on mesh "home".

(`--mesh` is only needed once, by whoever goes first. Leave it out and the mesh
is named after the machine.)

    $ mesh service install     # survives reboots and crashes

**On the second machine** — same network, carry an eight-character code:

    machine one:  $ mesh invite --lan
                  On the other machine, run:
                      mesh join --lan --code M5TQ6692

    machine two:  $ mesh join --lan --code M5TQ6692 --name windows

Different networks? `mesh invite` prints a string to paste instead. Either way
you are done:

    $ mesh peers
    windows   claude-code  ask,tell,mailbox -- the box with the GPU

    $ mesh ask windows "is the build green?"
    green, 412 tests, 0 failures

## Wire it into the agents you already use

    $ mesh connect
      ok    Claude Code                  registered with `claude mcp add`
      ok    Claude Desktop               registered in claude_desktop_config.json
      ok    Codex CLI / ChatGPT desktop  registered in ~/.codex/config.toml
      ok    Cursor                       registered in ~/.cursor/mcp.json
      ok    Gemini CLI                   registered in ~/.gemini/settings.json
      ok    opencode                     the join-mesh skill is installed

Now ask any of them *"who is on my mesh?"* and they will tell you. Ask one to
*"have the windows box check the build"* and it will.

`mesh connect` never rewrites a config it cannot parse — it prints the snippet
instead. Your comments and your other tool servers survive.

## What an agent does with it

Any agent that can run a shell command is a first-class peer.

    mesh peers                       who is here and what they are for
    mesh ask <peer> "question"       ask; blocks; answer on stdout
    mesh send <peer> "message"       tell; does not wait
    mesh ask @builders "question"    ask a whole group at once
    mesh wait                        block until a peer speaks (see below)
    mesh waiting                     questions addressed to you
    mesh reply <id> "answer"         answer one
    mesh guide                       the full reference, with your live roster

**Being reachable without polling.** An agent sitting at its prompt cannot
notice a message, and polling on a timer is the thing this replaces. So:

    mesh wait --timeout 30m

blocks until a peer says something, prints it, and exits. Run it as a
background task and your harness tells you when it finishes — an incoming
message becomes an event the agent is *handed*, not a habit it has to remember.

**Files.** `mesh send win --file crash.log "look at this"`. Checksummed end to
end; a truncated transfer is refused rather than delivered.

**A peer that is not there.** `mesh send` to an offline peer queues and delivers
when it comes back — `mesh outbox` shows what is waiting and why. An `ask`
still fails fast, because someone is blocked on it.

**Threads.** Pass `--thread` and the far agent keeps its context across turns —
and either side can send on a thread the other started, so an agent given an
ambiguous task can ask a question back mid-task.

## How a peer answers

Each node picks a delivery mode, and this is the only per-agent work the mesh
ever needs:

| mode | how the message lands | fits |
|---|---|---|
| `mailbox` | parks for `mesh reply` | anything, including a human (default) |
| `exec` | runs a command, streams stdout back | opencode, `codex exec`, `claude -p` |
| `webhook` | POSTs to a resident agent's local API | OpenClaw and other always-on agents |
| `notify` | parks, but runs a command so someone notices | anything with no API |

    mesh join --name builder --exec 'opencode run "$MESH_BODY"'

The question arrives in `$MESH_BODY` and on stdin — never interpolated into a
command line, so a peer cannot inject shell syntax.

## Being on the mesh is not permission to make you work

An agent mesh is a prompt-injection surface by construction: text written by
another machine reaches a model that can run commands. So the two are separate.

    mesh trust                  what each peer may do here, and its key
    mesh allow / deny <peer>    let a peer ask this node to work, or stop it
    mesh block <peer>           refuse it entirely, effective immediately
    mesh id / verify <peer>     fingerprints you can compare out of band
    mesh log                    what peers asked of you, refusals included

**Telling is always allowed. Asking depends on what the node does with a
question.** A mailbox node starts open — a human reads it. A node that
*executes* starts closed to everyone except the mesh's coordinator, because
joining its mesh is how the node said yes in the first place. Anyone else:

    $ mesh ask builder "run the tests"
    mesh: builder: you may send messages to this node but not ask it to do
    work. Its operator can allow that with `mesh allow mac`

    $ mesh allow mac          # on the builder — or `mesh allow --all`
                              # if every node on the mesh is yours

A peer's key is pinned on first contact, and a different key under a name
that is already taken is refused rather than accepted — that is how a name gets
stolen. Peers are rate limited, because the realistic threat is not malice, it
is a retry loop with no backoff.

## Not a task queue, and not an MCP server

agent-mesh is a **substrate**. Its job is that a named agent can reach another
named agent, reliably and privately, from anywhere. It has no task model on
purpose: a task model is an opinion about how work should be structured, there
are several good ones, and baking one in would be wrong for somebody.

`demo/handoff.sh` runs a full delegation — a desktop chat hands work to a
coding agent, the agent needs a clarification, asks it, gets an answer, reports
back — in four messages on one thread, with no task type on the wire. The task
is a convention the two ends share.

To make that easy, every message can carry a `type` and a `data` payload the
mesh never looks inside. Applications get a protocol; the substrate keeps its
lack of opinion.

## When it breaks

    $ mesh doctor
      ok    mesh "home", hub address on file
      ok    daemon running, pid 22248, up 1m36s
      ok    reachable through relay nyc
      ok    registered with the hub
      ok    2 peer(s) known, 2 online
      ok    registered with launchd, so it restarts on its own

Every line that fails names the command that fixes it.

The mesh heals itself: nodes reconnect with backoff, keep a cached roster so
peers stay reachable while discovery is down, rebuild a tunnel whose peer
restarted, and notice when they have lost their own relay and restart rather
than sit there looking healthy and answering nothing. Measured, with a
coordinator killed by `SIGKILL` and nobody touching anything: **2s to restart,
47s to a whole mesh again.**

## What it does not do

- **An `ask` to an offline peer still fails immediately** — deliberately,
  because someone is blocked waiting on it. A `send` queues and delivers when
  the peer returns; `mesh outbox` shows what is waiting.
- **The public relays are rate-limited with no uptime guarantee.** They are
  bootstrap and fallback; point `--derpmap-url` at your own to remove them.
- **A blocked peer can still open a tunnel**, it just cannot say anything
  through it. Revocation is enforced a layer above WireGuard.
- **Discovery has a single point**, though traffic never does. While the
  coordinator is down, existing peers keep talking off cached rosters.
- **tailcat has no API stability promise.** Pinned.
- **A desktop chat driving the mesh has your node's authority.** `mesh connect`
  makes a chat model able to ask your peers things — and that model reads web
  pages. Peer-side controls are the defence that matters (`mesh allow`,
  `mesh block`, and executing nodes starting closed), but giving the desktop
  surface *less* authority than the agent at the same machine is not built yet.

## Why not X

**SSH?** SSH gives you a shell on a machine you can already reach. This gives
you an *agent* on a machine behind NAT — one that decides how to do the thing
rather than executing what you typed, and that can come back with a question.

**Tailscale?** This is built on Tailscale's data plane and owes it everything.
The difference is the control plane: no account, no coordination server, no
`tailscaled`, nothing to log into. If you already run Tailscale, you have a
network; you still do not have agents that can find each other by name.

**MCP?** MCP is how a model calls a service — synchronous, stateless, the
caller knowing what to ask for. That is the right shape for Notion or Gmail and
a thin one for handing work to an agent that runs for minutes and may need to
ask you something first. agent-mesh speaks MCP so any harness can reach it, but
the substrate underneath is messages between peers, not tool calls.

**agent-talk / retalk?** Genuinely good, and ahead of this on breadth of agent
integrations. It routes through a relay — end-to-end encrypted, but a service
someone runs. Here there is no message-carrying server at all, and a peer can
be any process rather than one of a supported list of coding agents.

**A hosted "connect your machines" product?** Then your conversations go
through someone else's servers and it works only for machines they support.
That is the trade this exists to avoid.

## Proving it works

    ./demo/smoke.sh

Brings up two throwaway nodes on real tunnels and checks every claim on this
page: discovery, an exec node answering, threads keeping context, a file
arriving with its bytes intact, a group fanning out, a block biting on the next
message, a message queuing for a stopped peer and draining when it returns, and a
blocked `wait` returning the moment a peer speaks. Twenty-one
checks, two minutes, nothing left behind.

It found the bug that mattered most: the quickstart on this page did not work,
because an exec node started closed to everyone. The things that break are the
things only a real tunnel exercises.

    ./demo/handoff.sh <a> <b>    a delegation with a clarification round trip
    ./demo/two-agents.sh         the two-agent setup, end to end

## Design

[ARCHITECTURE.md](ARCHITECTURE.md) is the honest version: what was built, what
broke when it met a second machine, and what each failure changed. Every bug in
it was found by running the thing, not by reasoning about it.
[docs/SPEC-v1.md](docs/SPEC-v1.md) is the bar this had to clear;
[docs/SPEC-v2-surfaces.md](docs/SPEC-v2-surfaces.md) is where it goes next.

## License

MIT.
