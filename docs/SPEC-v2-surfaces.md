# agent-mesh v2 — hand a task to your machines from wherever you are

Status: **proposal, for review.** v1 (docs/SPEC-v1.md) makes agents reach each
other. This is about the surface a *person* uses, which is where the value
actually lands.

---

## 0. Two theses, and the second one is the important one

**The last mile was already solved. The missing hop was machine to machine.**

Desktop chat apps can already run something locally. What they could not do is
reach a program on a computer somewhere else, so every product that wanted to
offer it built the same thing: a hosted service, a tunnel down to an agent on
your machine, and your data through both. Nobody chose that because it was
good; it is what you build when peer-to-peer is hard. tailcat makes it not
hard, so the middle disappears -- not a smaller middle, or a more private
middle. None.

**And: calling a service is not the same shape as dispatching work.**

MCP is a protocol for a model to call something. Its unit is a tool call:
request, response, synchronous, nothing carried between calls. That is the
right shape for Notion, Gmail, a calendar -- services that exist to answer
questions and change records. It is why almost every MCP server is an API
wrapper.

Handing work to an agent is a different shape entirely:

| calling a service | dispatching work |
|---|---|
| milliseconds | minutes to hours |
| the caller knows what to ask for | the caller states intent, the other end decides how |
| a wrong call returns an error | an ambiguous task needs a *question asked back* |
| returns a value | produces artifacts, and progress worth watching |
| stateless | a lifecycle you can inspect, resume and cancel |
| the far side is always up | the far side may be asleep, busy, or restarting |

You can carry the second thing over the first -- we do -- but the protocol does
not express it. What you get is a tool call that either blocks for ten minutes
or hands back a handle you have to poll, with all the semantics living in a
prompt rather than in the wire. That is exactly why "@ my bot from a chat app"
feels thin today: people are using a service-calling protocol to do dispatch.

So the product is not an MCP server for your machines. **It is a dispatch layer
between agents, which projects an MCP surface for clients that can only speak
MCP.** Where a local harness exists -- and as of 2026 that is most of them --
the native path is better, because it gets the whole task model instead of a
flattened projection of it.

## 1. What is actually true today, per surface

Verified, September 2026, because the plan depends on it:

| surface | can run something locally | reaches the mesh by |
|---|---|---|
| **Claude Desktop** | yes | local MCP today; native once it has a task client |
| **ChatGPT Desktop** | yes — Codex harness, merged into the desktop app | the harness runs `mesh` directly |
| Claude Code, Codex CLI, opencode, Cursor | yes | skill + CLI, working today |
| OpenClaw and other resident agents | yes | webhook delivery, working today |
| a thin client with only remote connectors | no | a door you own (§5) |

The important line is the second one. OpenAI merged Codex into the ChatGPT
desktop app, so it now has a local harness that can use local files and
programs. That removes the constraint this section previously described -- an
earlier draft said ChatGPT could only reach remote HTTP connectors, which was
true of the connector system and is no longer the whole picture. **Both major
desktop surfaces can now run `mesh` on the machine the person is sitting at.**

Which makes the public-door design a fallback for genuinely thin clients rather
than the ChatGPT story, and moves §5 to the bottom of the list where it belongs.

## 2. What "amazing" means, concretely

The moment to design for is small and specific: someone is talking to a chat
app, and the thing they want doing lives on a computer that is not the one they
are talking to. *Check whether the build went green. Clean up my downloads
folder. Remember this for me. Look at what is eating disk on the Windows box.*

Today that means: stop, switch machines, find a terminal, re-explain. The
feature is that you do not.

Five things have to be true for it to feel like handing work to a colleague
rather than firing a command into the dark.

### D1. Setup is one command

Hand-editing a JSON config file is where most people stop. `mesh connect`
should detect the desktop apps installed on this machine and register the mesh
with each, then say what it did.

    $ mesh connect
    Claude Desktop     configured (restart it to pick this up)
    Cursor             configured
    ChatGPT Desktop    needs a public endpoint -- see `mesh connect --help`

Small, and the highest-leverage item on this list: everything else is worth
nothing to someone who never got it working.

### D2. A task is a first-class thing, not a long tool call

This is the piece that makes the difference between a message pipe with a nice
CLI and something you would actually hand work to.

Today an `ask` is a connection held open until an answer comes back. That is
fine for a question and wrong for work: it dies with the connection, it cannot
be inspected, it cannot be cancelled, it cannot ask anything back, and it
occupies a socket for ten minutes to deliver one paragraph.

A task is durable state on both sides:

    id, from, to, intent, thread
    state:     accepted -> working -> (input-required <-> working) -> done | failed | cancelled
    progress:  a stream you can attach to, late, more than once
    artifacts: files it produced
    cancel:    because a person changes their mind

Two properties matter more than the rest. **It survives a disconnection**, so
you can close your laptop and ask later how it went. And **it survives a daemon
restart**, because a task is state, not a socket -- which is the same
realisation behind the offline spool, and they should share a store.

`mesh ask` stays exactly as it is. It is the cheap path, it is right for a
question, and most traffic will keep using it. Tasks are for work.

### D3. The far agent can ask a question back

`input-required` is the state that makes this feel like delegation instead of
remote execution. "Which downloads folder — the one on the desktop or in your
home directory?" surfaces in the chat, gets answered, and the task continues.

Without it, every ambiguous task fails or guesses. With it, an agent that is
unsure behaves the way a competent colleague does.

This is the single feature I would protect if the rest were cut.

### D3b. Native beats projected

Every harness that can run a program should talk to the mesh directly rather
than through a tool-call shim, because the shim is where the task model gets
flattened back into request/response.

`mesh connect` should therefore install the *best available* integration per
surface rather than the same one everywhere: a skill where skills exist, an MCP
server where that is the only option, a config entry where the harness wants
one. What a surface gets should be decided by what it can do, not by what is
easiest to write once.

### D4. The model picks the machine

The user says "check the build", not "ask the node named windows". Nodes
already carry a free-text note; that becomes a structured capability card —
what this agent is for, what it can reach, what it must not be asked. The chat
model reads the roster and routes.

Naming a node stays possible. Needing to is the failure.

### D5. Results that are not a paragraph

A screenshot, a patch, a log, a file. tailcat already ships SFTP, so the
transport exists; what is missing is treating a task's outputs as artifacts
attached to the task rather than text stuffed into a reply.

## 3. What this means for safety

v1's trust model was built for agents talking to agents. This surface changes
the threat, and the change is not small: **a chat model, driven by whatever the
user pasted into it, can now trigger work on real machines.** The prompt that
does it may have come from a web page the model read a minute ago.

Three additions, none optional before anyone else installs this:

1. **Destructive work needs confirmation.** A node can mark its adapter as
   consequential, and a delegated task from a chat surface then requires an
   explicit confirmation step before it runs. The permission model already
   distinguishes tell from ask; this adds a third tier for the ones you cannot
   take back.
2. **The chat surface is a peer, not a superuser.** `mesh mcp` acts as the
   local node, so every existing per-peer rule applies to it. It should be
   possible to give the desktop surface *less* authority than the agent sitting
   at the same machine, because it is driven by text from the internet.
3. **The audit log is the feature.** `mesh log` already records what was asked
   and what was refused. On this surface it should be visible to the person,
   because "what have my agents been asked to do today" is a question they will
   want answered the first time something surprises them.

## 4. Sequencing

The task model is the spine; everything else hangs off it.

1. **D2 the task model** — durable, resumable, cancellable, with progress. The
   largest piece and the one that changes what this project is. Shares a store
   with the offline spool, so it should land after or alongside that.
2. **D3 `input-required`** — small once D2 exists, and it is the magic.
3. **D1 `mesh connect`** — per-surface, native where possible. Cheap, and
   nothing else reaches anyone without it.
4. **§3 safety** — before any public instruction to install this.
5. **D5 artifacts**, then **D4 capability cards**.
6. **§5 the door**, only if someone actually needs a thin client.

## 5. A door for thin clients

Some clients can only reach a remote HTTPS endpoint -- a connector system with
no local harness, a phone, a browser tab. They are no longer the main case, but
where one matters, the design constraint is that the endpoint must be as close
to nothing as possible:

`mesh mcp --http --listen :443` makes any mesh node serve the same tools over
HTTP/SSE instead of stdio, authenticated with a bearer token. Then either:

- **a machine you already have with a public address** joins the mesh and runs
  it — a VPS, a home server, a box at the office; or
- **a tunnel** from a machine you control.

Either way the door is yours, it holds nothing, and it does not need to be near
the machines that do the work — it is a mesh node like any other, and the mesh
does the reaching. Compared with a vendor's hosted agent this removes the third
party and the requirement that the vendor support your machine, which is most
of the point.

What it does **not** do is remove the public endpoint for a client that can
only speak to one. That is a property of the client, and no amount of
peer-to-peer changes it -- which is why this is the last item on the list and
not the first.

## 6. What we still do not build

Unchanged from v1, and this section is why the project stays small: no
orchestration, no shared task lists, no hosted service, no human chat UI, no
accounts. A door you run for yourself is not a hosted service; the moment we
run one for other people, we have become the thing we replaced.
