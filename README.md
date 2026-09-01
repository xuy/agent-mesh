# agent-mesh

A named mesh for agents. Any agent that can run a shell command can reach any
other agent by name, over an encrypted peer-to-peer link, with no dependency on
any vendor's backend.

    mesh ask windows "is the build green?"

The agent on the other side is a real agent: a model runs, and its answer comes
back on stdout.

Built on [tailcat](https://github.com/tailscale/tailcat) — Tailscale's data
plane (WireGuard, NAT traversal, DERP relay bootstrap) with none of its control
plane. No Tailscale account, no `tailscaled`, no root, no changes to your
routing or DNS.

## Install

    make install          # builds and installs `mesh` to ~/.local/bin

Go 1.24+ is the only requirement.

## Quickstart: two agents on one machine

Start the control plane once, anywhere in the mesh:

    mesh hub --mesh noah --note "Eric's agents"

Then each agent joins. On the same machine this takes no arguments at all —
the hub's address is already on disk:

    mesh join --name master   --note "Claude Code; ask me about the repos"
    mesh join --name opencode --note "opencode + glm-5.3" \
              --exec "$PWD/demo/opencode-adapter.sh"

They can now talk:

    $ MESH_NAME=master mesh peers
    opencode   opencode ask,tell,exec -- opencode + glm-5.3

    $ MESH_NAME=master mesh ask opencode "what model are you?"
    I'm opencode running the glm-5.3 model (zai-coding-plan/glm-5.3).

`demo/two-agents.sh` runs that whole sequence.

## Adding a machine

On the machine running the hub:

    mesh invite

That prints one string. It is a secret — it carries the join key, so hand it
over the way you would a password. On the new machine:

    mesh join --invite am1_...

Nothing else. NAT traversal, encryption and relay fallback are tailcat's job.

## How an agent answers

Every node picks one of two adapters.

**`mailbox`** (the default) parks the question for a human or an agent to
answer by hand. No integration at all:

    mesh waiting                     # what is asked of you
    mesh reply <id> "your answer"    # answer one

**`exec`** answers automatically by running a command:

    mesh join --name opencode --exec 'opencode run "$MESH_BODY"'

The question arrives in `$MESH_BODY` and on stdin — never interpolated into the
command line, so a peer cannot inject shell syntax. `$MESH_CONTINUE` is
`--continue` when this `--thread` has been seen before, which is how a
multi-turn exchange keeps model context on the far side:

    mesh ask opencode --thread bug-9 "what does value-judge.ts do?"
    mesh ask opencode --thread bug-9 "and who calls it?"     # it remembers

## Teaching a new agent to use it

    mesh install-skill

Writes a `join-mesh` skill for Claude Code and links it for opencode, so the
next session that starts on the machine can find and use its peers without
being told. For native tools instead of shell commands, `mesh mcp` serves the
mesh over MCP:

    "agent-mesh": { "type": "local", "command": ["mesh", "mcp"] }

Any agent already on the mesh can run `mesh guide`, which prints the full usage
along with the live roster.

## Commands

    mesh join      join the mesh and start answering       mesh hub     run the control plane
    mesh peers     who is here                             mesh invite  string to add a machine
    mesh ask       ask a peer, wait for the answer         mesh up      start this node's daemon
    mesh send      tell a peer, do not wait                mesh down    stop it
    mesh inbox     what has been said to you               mesh status  this node
    mesh waiting   questions awaiting your answer          mesh doctor  what is wrong, and the fix
    mesh reply     answer one of them                      mesh ping    is a peer reachable, and how
    mesh guide     the full agent-facing reference         mesh mcp     serve the mesh as MCP tools

Every command takes `--json`.

## What it does not do

Stated plainly, because a prototype that hides its edges is worse than one that
does not:

- **No store-and-forward.** Messaging an offline peer fails immediately.
- **The public DERP relays are rate-limited with no uptime guarantee.** Fine for
  a prototype. Point `--derpmap-url` at your own DERP to remove the dependency.
- **Peer removal needs a daemon restart.** tailcat can grant a peer key at
  runtime but not revoke one, so a peer dropped from the mesh stays reachable
  until the node restarts.
- **The coordinator accepts tunnels from nodes it does not know**, because that
  is what joining is. It still refuses to *talk* to anyone outside the roster,
  and registering needs the mesh's join key -- but unlike a plain node, an
  unknown peer reaches our code before being turned away.
- **Discovery has a single point**, though traffic never does. While the
  coordinator is down, existing peers keep talking off their cached rosters;
  only joining and address changes stall.
- **tailcat has no API stability promise.** Pinned at v0.4.0.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the design and why it is shaped this
way.
