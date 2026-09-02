# Before this repo goes public

Short list. Everything here needs a person, not a commit.

## 1. Two real details of a personal machine are in the README

The opening example is a real exchange, which is most of why it works — a
reader can tell it was not invented. It also means the README says what is on
the D drive of Eric's Windows PC (a 296.9GB Steam library) and how full C: is.

The Windows node raised this rather than letting it ship quietly, and it is
right that it is the owner's call and not an agent's.

Innocuous, probably. But decide deliberately:

- **Keep it.** Strongest version. A Steam library is not a secret.
- **Swap it.** A generic answer of the same shape is one edit; the example
  loses the thing that makes it convincing.

The same applies to the mesh name in some examples, which is a hostname.

## 2. The repo must be public for two things in the README to work

- `curl -fsSL .../install.sh | sh` fetches from `raw.githubusercontent.com`.
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

The README's limits section is accurate as of writing: no store-and-forward,
public relays are best-effort, a blocked peer can still open a tunnel, and a
desktop chat driving the mesh has the node's authority. Check it still matches
the code before publishing — a limits section that has drifted optimistic is
worse than none.
