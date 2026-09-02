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

## 2. The mesh does not get a task model

The obvious next move is to build tasks into the mesh -- a task id, a state
machine, progress, cancellation. It is the wrong move, and it is worth being
precise about why, because the argument for it is superficially good.

**agent-mesh is a substrate.** Its job is that a named agent can reach another
named agent, reliably, privately, from anywhere. A task model is an opinion
about how work should be structured, and there will be several good ones: A2A
has a task lifecycle, job queues have another, a code review workflow wants
something else again. Baking one in decides for everybody and is wrong for
somebody. TCP does not know about HTTP, and that is the reason both are still
useful.

The test is whether communication alone is enough. `demo/handoff.sh` runs the
whole scenario this document was written for -- a desktop chat hands work to a
coding agent, the agent needs a clarification, asks it, gets an answer, and
reports back -- with no task type on the wire at all:

    desk  -> coder   "check whether the build is green"      thread t
    coder -> desk    "which branch, main or release?"        thread t
    desk  -> coder   "main"                                  thread t
    coder -> desk    "main is green: 412 tests, 0 failures"  thread t

Four messages, one thread, both directions. The "task" is a convention the two
ends share. A different pair of agents can share a different one, and neither
needs the mesh to agree.

What the substrate owes them is that the convention is expressible. So the
envelope carries two fields the mesh never looks inside:

    type   an application's own vocabulary, namespaced by its owner
    data   an application's own payload

That is the platform position in two fields. An application gets a protocol;
the substrate keeps its lack of opinion. A task model can then be written on
top -- by us, later, as a separate thing, or by somebody else entirely -- and
the mesh does not change to accommodate it.

## 2b. What the substrate is still missing

Which reframes this whole document. The gaps are not features of a task model.
They are gaps in communication, and there are fewer of them than expected.

**Durable delivery.** A message to a peer that is offline should arrive when it
returns. In progress on the Windows node as M3. This is what lets an
application keep state across a restart without the mesh keeping it for them.

**Files.** Sending a patch, a screenshot, a log. This is a communication
primitive by any reading -- it is *what* is being communicated -- and tailcat
already ships SFTP, so the transport exists and the work is a verb over it.
Without it every application invents base64-in-a-message, badly.

**Waiting that cannot lose a message.** Found by writing the demo above, which
hung the first time: `mesh wait` only heard what came *next*, so a message that
arrived while the agent was working was missed. It now keeps a read cursor and
reports what was missed first. An agent that has to track an id between turns
to avoid losing messages is an agent that will lose messages.

**Symmetric threads**, which already work: either end can send on a thread the
other started, which is what made the clarification round trip possible without
a request/response protocol.

That is the list. Everything else in the original draft of this section --
delegation, lifecycle, input-required, progress -- turned out to be an
application built on those, not a change to them.

## 2c. What still belongs to the surface

Two things are genuinely about the person, not the substrate, and stay in this
document:

**Setup is one command.** `mesh connect` should detect the harnesses installed
on this machine and give each the best integration it can take -- a skill where
skills exist, an MCP server where that is the only option. Hand-editing a JSON
config is where most people stop, and nothing else here reaches anyone without
it.

**The model picks the machine.** The person says "check the build", not "ask
the node named windows". Nodes already carry a free-text note; a structured
capability card would let a chat model route without being told. Naming a node
stays possible; needing to is the failure.

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

Finish the substrate, then make it reachable. Nothing here is a task model.

1. **Durable delivery** — in progress on the Windows node.
2. **Files** — the last missing communication primitive.
3. **`mesh connect`** — per-surface, native where possible.
4. **The safety items in §3** — before any public instruction to install this.
5. **Capability cards.**
6. **A door for thin clients**, only if someone actually needs one.

A task model, if we want one, is an application written on top afterwards --
and it should be possible for someone else to write a different one without
asking us.

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
