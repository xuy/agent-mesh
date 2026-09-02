# Before this repo goes public

Short list. Everything here needs a person, not a commit.

## 1. The opening example is now illustrative, not a real exchange

**Taken care of, but you may want it back the other way.**

It used to be a real exchange — the README quoted what an agent on the Windows
PC actually answered, which is most of why it worked, because a reader can tell
an invented example. The Windows node pointed out that this meant publishing
what is on that machine's D drive (a 296.9GB Steam library) and how full C: is,
and raised it rather than letting it ship quietly. It was right that this is
the owner's call, not an agent's.

Since nobody had ruled on it, the README now uses a build-status example in the
present tense: it describes how the thing works and claims nothing about a
specific past exchange, so it is honest either way.

If you would rather have the real one back, two real answers are on file:

- the D-drive answer above; or
- `Windows 11 Home build 26200, 13th Gen Intel Core i9-13900H, 16GB RAM,
  windows/amd64, Go 1.27.0` — hardware, no drive contents.

Both are real details of your personal machine going into a public README.

The same question applies to the mesh name in some examples, which is a
hostname.

## 2. The repo must be public for two things in the README to work

- `curl -fsSL .../install.sh | sh` fetches from `raw.githubusercontent.com`.
- `irm .../install.ps1 | iex` does the same on Windows.
- `go install github.com/xuy/agent-mesh/cmd/mesh@latest` needs a public module.

Both are broken links until `gh repo edit --visibility public`.

## 3. Cut a release first

`install.sh` reads the latest release tag and downloads a per-platform binary.
With no release, it fails with "could not find the latest release". Tag and let
the release workflow build, or the first thing a reader tries will not work.

## 4. Anyone who installed the Windows service the old way needs one command

An early build registered the Task Scheduler task from an elevated shell, and a
task created elevated cannot be replaced by an unelevated one. Those machines
need one `schtasks /Delete /TN \agent-mesh-<name> /F` from an admin shell
before `mesh service install` will work. Worth a line in the release notes.

## 5. Say what is not built

The README's limits section is accurate as of writing: an `ask` to an offline
peer fails immediately (a `send` queues), public relays are best-effort, a
blocked peer can still open a tunnel, discovery has a single point, and a
desktop chat driving the mesh has the node's authority. Check it still matches
the code before publishing — a limits section that has drifted optimistic is
worse than none.

This has already gone stale once. It said "no store-and-forward" for weeks
after the Windows node shipped the outbox, in three separate documents. When a
limit stops being true, the doc that states it is part of the change.
