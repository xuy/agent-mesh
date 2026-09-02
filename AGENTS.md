# agent-mesh

Go. `make build` · `make test` · `make install` (installs `mesh` to `~/.local/bin`).

Read `ARCHITECTURE.md` before changing anything structural. The short version:
a control plane (`internal/hub`) resolves names to addresses and nothing else;
agents talk to each other directly over tailcat tunnels (`internal/node`).

Two things that look like details and are not:

- **A node holds two node keys.** A DERP relay admits one connection per public
  key, so serving and dialing need separate identities. See `internal/ident`.
- **Sender attribution comes from the tunnel address**, which derives from the
  caller's node key (`ident.Addr`). Never trust the `from` field on an envelope
  without checking it against the connection it arrived on.

Install with `make install`, never `cp` over the existing binary: macOS
invalidates the code signature of a binary modified in place and kills it on
exec, silently.

## Two agents, one main

This repo is worked by more than one agent on more than one machine, and a
merge to `main` is a ship. Run `make hooks` once per clone; the `pre-push` hook
then refuses, on `main`, a push that would discard commits you do not have, and
a push whose tree is red.

- **Never force-push `main`.** If your push is refused, someone else shipped
  while you were working: `git pull --rebase origin main`, re-run the tests,
  push again. Rewriting a commit a peer has already pulled leaves them with a
  duplicate, and they will not notice until it conflicts.
- **Rebase, do not merge.** The history here is linear and worth keeping so.
- **Say what you are taking before you take it**, over the mesh, naming files
  rather than milestones: `internal/node/node.go` and `cmd/mesh/node_cmds.go`
  are where two agents collide.
- **Commit your own work before running anything destructive.** `git add -A`
  inside a throwaway commit followed by `reset --hard` takes your uncommitted
  work with it.
- The bypass is `MESH_PUSH_ANYWAY=1`. It exists for the case you have already
  discussed with the other agent, not for the case you are in a hurry.
