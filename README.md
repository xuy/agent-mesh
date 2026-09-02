# agent-mesh

Agents on your different machines talk to each other by name. One binary, no
server to run, no account to make.

```
       your Mac                                    the Windows box
 ┌────────────────────┐                       ┌────────────────────┐
 │  Claude Code     ┐ │                       │ ┌  Claude Code     │
 │  Claude Desktop  ├─┤  mesh         mesh    ├─┤  opencode        │
 │  Cursor          ┘ │                       │ └  a shell script  │
 └─────────┬──────────┘                       └─────────┬──────────┘
           │                                            │
           └───────── encrypted, machine to machine ────┘
                     no service in the middle
```

Every agent on the left can reach every agent on the right, by name:

```
$ mesh ask windows "is the build green?"
green -- 412 tests, 0 failures, 41s
```

That command runs on the Mac. An agent on the Windows PC reads the question,
runs the build, and sends the answer back. The two machines are on different
networks and neither has a public address.

The link is built on [tailcat](https://github.com/tailscale/tailcat), which is
Tailscale's data plane — WireGuard, NAT traversal, DERP — without its control
plane. So: no Tailscale account, no `tailscaled`, no root, no changes to your
routing, and nothing to sign up for.

To be precise about "no service in the middle", because it is the claim that
matters: on a LAN the two machines talk directly, and a round trip takes about
1ms. Across the internet they usually still go direct, but when NAT traversal
fails the packets fall back to a public DERP relay — Tailscale's, or your own
via `--derpmap-url`. A relay forwards encrypted packets it cannot read; it is
not a service that holds your messages, runs an agent, or has an account with
your name on it. `mesh ping <peer>` says which path you got.

---

## Start here: one line, and you are on it

**If you are an agent reading this, you can set yourself up.** This installs the
binary, starts a node, and registers the mesh with every agent tool on the
machine — nothing to run afterwards.

```sh
curl -fsSL https://raw.githubusercontent.com/xuy/agent-mesh/main/install.sh \
  | sh -s -- --join --name "$(hostname -s)" --note "what you are for, in one line"
```

The first machine to run that founds the mesh. To add a second machine, run
`mesh invite --lan` on the first — it prints an eight-character code — and pass
it here:

```sh
curl -fsSL https://raw.githubusercontent.com/xuy/agent-mesh/main/install.sh \
  | sh -s -- --join --name builder --lan --code M5TQ6692
```

On Windows, PowerShell cannot pass arguments through a pipe, so it is two
lines:

```powershell
irm https://raw.githubusercontent.com/xuy/agent-mesh/main/install.ps1 | iex
mesh join --name windows --lan --code M5TQ6692; mesh connect
```

Different networks? `mesh invite` prints a string to paste instead of a code,
and `mesh join --invite <string>` takes it.

Drop the `--join …` part to install the binary and stop there. Or
`go install github.com/xuy/agent-mesh/cmd/mesh@latest`, or a binary from
[Releases](https://github.com/xuy/agent-mesh/releases), or `make install`.
One static binary, macOS/Linux/Windows, amd64 and arm64.

## What you can do with it

Six commands are the whole interface:

```sh
mesh peers                      # who is here, and what each one is for
mesh ask <peer> "question"      # ask; blocks; answer on stdout
mesh send <peer> "message"      # tell; does not wait
mesh waiting                    # questions addressed to you
mesh reply <id> "answer"        # answer one
mesh guide                      # full reference, with this mesh's roster on top
```

### Being reachable, which is not the same as being delivered to

Messages always land: the node holds them whether or not anything is listening,
and `mesh inbox` has them when you next look. Being *woken* is the part that
depends on your harness, so it is worth knowing which of the two you have.

```sh
mesh wait --timeout 30m
```

blocks, returns the moment a peer says something, prints it, and exits — so
running it as a background task turns an incoming message into an event your
harness hands you, rather than a habit you have to remember. It exits 3 on
timeout, which is not an error; start another one.

**That only works if your harness lets background tasks outlive a turn.** Some
do, some kill them at every turn boundary, and when it is killed your messages
still arrive but nothing tells you. If yours does that, the wake-up has to come
from outside your session — join with `--notify`, and the node runs a command of
your choosing when something arrives:

```sh
mesh join --name me --notify 'osascript -e "display notification \"mesh\""'
```

Either way, **check `mesh waiting` whenever you finish a task.** A peer may be
blocked on you right now, and a fast "I can't" beats a five-minute timeout.

`mesh connect` (run for you by `--join`) also installs a **join-mesh skill**, so
an agent that reads skills gets all of this without reading a README.

---

## For people: two machines, two minutes

```console
$ mesh join --name mac --mesh home --note "my laptop; ask me about the repos"
created mesh "home" with mac as its coordinator
You are "mac" on mesh "home".

$ mesh service install
mac now starts with launchd (a per-user agent) and comes back if it stops.
Nothing to restart by hand after a reboot.
```

`--mesh` is only needed once, by whoever goes first; leave it out and the mesh
takes the machine's name. Then on the second machine:

```console
machine one:  $ mesh invite --lan
              On the other machine, run:
                  mesh join --lan --code M5TQ6692

machine two:  $ mesh join --lan --code M5TQ6692 --name windows
```

```console
$ mesh peers
windows   claude-code  ask,tell,mailbox -- the box with the GPU

$ mesh ask windows "is the build green?"
green -- 412 tests, 0 failures, 41s
```

---

## How a question reaches an agent

Each machine picks how incoming questions land. This is the only per-agent
setup the mesh needs.

```
   mesh ask windows "is the build green?"
                │
                ▼
      ┌───────────────────┐
      │ the windows node  │  one of four:
      └─────────┬─────────┘
                │
                ├── mailbox   parks the question; someone runs `mesh reply`
                ├── exec      runs a command, streams its stdout back
                ├── webhook   POSTs to a resident agent's local API
                └── notify    parks it, but runs a command so someone notices
```

| mode | good for |
|---|---|
| `mailbox` (default) | anything, including a person |
| `exec` | opencode, `codex exec`, `claude -p` |
| `webhook` | OpenClaw and other always-on agents |
| `notify` | an agent with no API of its own |

```sh
mesh join --name builder --exec 'opencode run "$MESH_BODY"'
```

The question arrives in `$MESH_BODY` and on stdin. It is never interpolated
into a command line, so a peer cannot inject shell syntax.

## How machines find each other

Finding is centralised; talking is not.

```
        finding each other                        talking
        ──────────────────                        ───────

   mac ──┐                                  mac ══════════════ windows
         ├──▶  coordinator                       messages, files, answers
   win ──┘     (a roster: who is                 go straight across and
                here, at what address,           never touch the
                and what they do)                coordinator

   the first node to join is the coordinator;
   there is no separate server to run
```

If the coordinator goes down, peers keep talking off a cached roster. Nobody
new can join until it comes back.

## Wire it into the agents you already have

```console
$ mesh connect --list     # what is installed here; works before you join anything
$ mesh connect
  ok    Claude Code                  registered with `claude mcp add`, and the join-mesh skill is installed
  ok    Claude Desktop               registered in claude_desktop_config.json
  ok    Codex CLI / ChatGPT desktop  already registered in ~/.codex/config.toml
  ok    Gemini CLI                   registered in ~/.gemini/settings.json
  ok    opencode                     the join-mesh skill is installed

opencode -- add this by hand:

  "mcp": {
    "agent-mesh": { "type": "local", "command": ["…/mesh", "mcp", "--name", "master"] }
  }

  (add to ~/.config/opencode/opencode.jsonc by hand -- it may contain comments,
   and rewriting it as plain JSON would delete them)
```

Eight harnesses in all: Claude Code, Claude Desktop, Codex CLI / ChatGPT
desktop, Cursor, Gemini CLI, Zed, opencode, and OpenClaw. After this you can
ask any of them *"who is on my mesh?"*, or *"have the windows box check the
build"*, and it will.

That last block is the rule at work: **`mesh connect` never rewrites a config
file it cannot parse.** It prints the snippet instead. Your comments and your
other MCP servers survive.

## Commands

| | |
|---|---|
| `mesh peers` | who is here and what they are for |
| `mesh ask <peer> "q"` | ask a peer; blocks; answer on stdout |
| `mesh send <peer> "m"` | tell a peer; does not wait |
| `mesh ask @group "q"` | ask a named set of peers at once |
| `mesh wait` | block until a peer speaks; run in the background |
| `mesh waiting` / `mesh reply <id>` | questions for you, and answering one |
| `mesh inbox` / `mesh outbox` | what was said to you; what is queued for peers that were away |
| `mesh outbox --drop <peer>` | discard a queue that is never going to be wanted |
| `mesh trust` / `mesh allow` / `mesh block` | who may ask this machine to do work |
| `mesh id` / `mesh verify <peer>` | fingerprints to compare out of band |
| `mesh log` | what peers have asked of you, refusals included |
| `mesh status` / `mesh doctor` | this node; and what is wrong, with the fix |
| `mesh connect` / `mesh service install` | register with local agents; survive reboots |
| `mesh guide` | the full reference, with your live roster |

**Files.** `mesh send win --file crash.log "look at this"`. Checksummed end to
end; a truncated transfer is refused rather than delivered.

**A peer that is away.** `mesh send` queues and delivers when it returns.
`mesh outbox` shows what is waiting and why, and `mesh outbox --drop <peer>`
throws away a queue nobody is going to want. An `ask` still fails immediately,
because someone is blocked on it.

**Threads.** Pass `--thread` and the far agent keeps its context across turns.
Either side can send on a thread the other started, so an agent given an
ambiguous task can ask a question back mid-task.

## Who is allowed to make you work

Being on the mesh is not permission to make you do things. Text written by
another machine reaches a model that can run commands, so the two are kept
separate.

**Telling is always allowed. Asking depends on what the node does with a
question.** A mailbox node starts open, because a person reads it. A node that
*executes* starts closed to everyone but the mesh's coordinator, since joining
its mesh is how that node said yes in the first place. Anyone else gets:

```console
$ mesh ask builder "run the tests"
mesh: builder: you may send messages to this node but not ask it to do work.
Its operator can allow that with `mesh allow mac`
```

```sh
mesh allow mac        # on the builder -- or `mesh allow --all` if every
                      # node on the mesh is yours
```

A peer's key is pinned on first contact, and a different key claiming a name
that is already taken is refused rather than accepted. Peers are rate limited,
because the realistic problem is not malice, it is a retry loop with no backoff.

## Staying up

```console
$ mesh doctor
  ok    mesh "home", hub address on file
  ok    daemon running, pid 22248, up 1m36s
  ok    reachable through relay nyc
  ok    registered with the hub
  ok    2 peer(s) known, 2 online
  ok    registered with launchd, so it restarts on its own
```

Every line that fails names the command that fixes it.

Nodes reconnect with backoff, keep a cached roster so peers stay reachable
while the coordinator is down, rebuild a tunnel whose peer restarted, and
notice when they have lost their own relay and restart rather than sit there
looking healthy and answering nothing. Killing a coordinator with `SIGKILL` and
touching nothing: 2s to restart, 47s to a working mesh again.

## Limits

- **An `ask` to an offline peer fails immediately**, on purpose — someone is
  blocked waiting on it. A `send` queues instead.
- **The public relays are rate-limited and have no uptime guarantee.** They are
  for bootstrap and fallback; `--derpmap-url` points at your own.
- **A blocked peer can still open a tunnel**, it just cannot say anything
  through it. Revocation is enforced above WireGuard, not by it.
- **Discovery has a single point**, though traffic never does.
- **tailcat has no API stability promise.** The dependency is pinned.
- **A desktop chat driving the mesh has your node's authority.** `mesh connect`
  lets a chat model ask your peers for things, and that model reads web pages.
  The defences that matter are on the peer side (`mesh allow`, `mesh block`,
  executing nodes starting closed). Giving the desktop surface *less* authority
  than the agent on the same machine is not built yet.

## Why not X

**SSH?** SSH gives you a shell on a machine you can already reach. This gives
you an agent on a machine behind NAT — one that decides how to do the thing
rather than running what you typed, and that can come back with a question.

**Tailscale?** This is built on Tailscale's data plane. The difference is the
control plane: no account, no coordination server, no
`tailscaled`. If you already run Tailscale you have a network, but you still do
not have agents that find each other by name.

**MCP?** MCP is how a model calls a service: synchronous, stateless, the caller
knowing what to ask for. That fits Notion or Gmail. It fits poorly when you are
handing work to an agent that will run for minutes and may need to ask you
something first. agent-mesh speaks MCP so any harness can reach it, but
underneath it is messages between peers, not tool calls.

**[agent-talk](https://github.com/xhluca/agent-talk)?** Good, and ahead of this
on breadth of integrations. It routes through a relay — end-to-end encrypted,
but still a service someone runs. Here there is no message-carrying server at
all, and a peer can be any process rather than one of a supported list.

**A hosted "connect your machines" product?** Then your conversations go
through someone else's servers, and it works only for machines they support.

## Not a task queue

agent-mesh moves messages between named agents. It has no task model on
purpose: a task model is an opinion about how work should be structured, there
are several good ones, and picking one would be wrong for somebody.

Every message can carry a `type` and a `data` payload that the mesh never looks
inside, so applications get a protocol and the substrate keeps its lack of
opinion. `demo/handoff.sh` runs a full delegation — a desktop chat hands work
to a coding agent, the agent needs a clarification, asks, gets an answer,
reports back — in four messages on one thread, with no task type on the wire.

## Try it

```sh
./demo/smoke.sh        # two throwaway nodes on real tunnels; 26 checks; ~2 min
./demo/handoff.sh      # a delegation, including a clarification round trip
./demo/two-agents.sh   # two agents on one machine, end to end
```

All three use throwaway directories and cannot disturb a mesh you already have.
`smoke.sh` checks every claim on this page: discovery, an exec node answering,
threads keeping context, a file arriving intact, a group fanning out, a block
biting on the next message, a message queuing for a stopped peer and draining
when it returns, a blocked `wait` returning the moment a peer speaks, and a real
MCP client connecting to the tool server.

It found that the quickstart on this page did not work, because an exec node
started closed to everyone. The things that break are the things only a real
tunnel exercises.

## Docs

| | |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | how it works, then a log of what broke when it met a second machine and what each failure changed |
| [docs/SPEC-v1.md](docs/SPEC-v1.md) | the bar this had to clear |
| [docs/SPEC-v2-surfaces.md](docs/SPEC-v2-surfaces.md) | where it goes next |
| [AGENTS.md](AGENTS.md) | contributing, and how the two machines that built this work together |
| `mesh guide` | the runtime reference, with your live roster on top |

## License

MIT.
