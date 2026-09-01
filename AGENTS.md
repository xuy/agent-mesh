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
