# agent-mesh v2 — hand a task to your machines from wherever you are

Status: **proposal, for review.** v1 (docs/SPEC-v1.md) makes agents reach each
other. This is about the surface a *person* uses, which is where the value
actually lands.

---

## 0. The thesis

**The last mile was already solved. The missing hop was machine to machine.**

Desktop chat apps can already call a tool. What they could not do is reach a
program running on a computer somewhere else, so every product that wanted to
offer it built the same thing: a hosted service, a tunnel down to an agent on
your machine, and your data through both. That architecture is not a choice
anyone made because it was good; it is what you build when peer-to-peer is
hard.

tailcat makes it not hard. So the shape becomes:

    you, in a chat  ->  a tool on your own machine  ->  encrypted p2p  ->  the machine that has the thing

with no service in the middle. Not a smaller middle, or a more private middle.
None.

That is the whole product, and everything below is what stands between it being
technically true today and being something a stranger would install.

## 1. What is actually true today, per surface

Verified, September 2026, because the plan depends on it:

| surface | local tool servers | agent-mesh today |
|---|---|---|
| **Claude Desktop** | yes, local stdio | **works now** — `mesh mcp` |
| Claude Code, Cursor, opencode | yes, local stdio | works now |
| **ChatGPT Desktop** | **no** | needs a public door (§5) |

ChatGPT's connectors are remote HTTP/SSE servers that OpenAI's own
infrastructure connects to. A local stdio server is not an option, and
`localhost` is not reachable from their side. This is ChatGPT's architecture,
not a gap in ours, and no amount of peer-to-peer removes it.

So the honest version of the pitch splits:

- **On Claude Desktop the middle really does disappear**, today, with no
  infrastructure at all. That is the demo.
- **On ChatGPT one public endpoint is unavoidable** — but it can be *yours*, it
  can be a dumb door rather than a service, and it does not have to be anywhere
  near the machines that do the work. That is still a categorically better
  trade than a vendor's hosted agent, and §5 says how.

Getting this the wrong way round in a launch post would be the expensive kind
of wrong, so it is written down here first.

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

### D2. Delegation is asynchronous

A tool call in a chat has a short timeout and a person watching it. Real work —
a build, a scan, a cleanup — takes minutes. Blocking is wrong on both counts.

Split it, taking the shape from A2A rather than inventing one:

- `mesh_delegate(peer, task)` returns a task id immediately.
- `mesh_check(task_id)` returns state, progress so far, and the result when
  there is one.
- States: `working`, `input-required`, `done`, `failed`.

This also fixes something v1 has wrong for every caller, not just chat: an
`ask` that takes ten minutes currently occupies a connection for ten minutes.

### D3. The far agent can ask a question back

`input-required` is the state that makes this feel like delegation instead of
remote execution. "Which downloads folder — the one on the desktop or in your
home directory?" surfaces in the chat, gets answered, and the task continues.

Without it, every ambiguous task fails or guesses. With it, an agent that is
unsure behaves the way a competent colleague does.

This is the single feature I would protect if the rest were cut.

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

D1, D2 and D3 are the product. D4 and D5 make it good. The safety items in §3
gate telling anyone about it.

1. **D1 `mesh connect`** — half a day, and nothing else matters without it.
2. **D2 async tasks** — the largest piece, and it improves v1 as well.
3. **D3 `input-required`** — small on top of D2, and it is the magic.
4. **§3 safety** — before any public instruction to install this.
5. **D4 capability cards**, **D5 artifacts**.
6. **§5 the ChatGPT door.**

## 5. The ChatGPT door

ChatGPT needs an HTTPS endpoint its servers can reach. The design constraint is
that the endpoint must be as close to nothing as possible:

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

What it does **not** do is remove the public endpoint. Anyone who says
otherwise about ChatGPT is selling something.

## 6. What we still do not build

Unchanged from v1, and this section is why the project stays small: no
orchestration, no shared task lists, no hosted service, no human chat UI, no
accounts. A door you run for yourself is not a hosted service; the moment we
run one for other people, we have become the thing we replaced.
