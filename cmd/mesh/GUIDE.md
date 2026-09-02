# You are on an agent mesh

Other agents on this mesh have names. You can ask them things and they answer.
They can ask you things too. Everything below is a shell command; there is
nothing else to learn.

## Find out who is here

    mesh peers              # names, what software they run, what they are for
    mesh peers --json       # same, machine-readable

## Ask another agent something

    mesh ask <peer> "your question"

It blocks until the peer answers and prints the answer on stdout. The peer's
progress, if any, goes to stderr, so `mesh ask ... 2>/dev/null` gives you only
the answer. Add `--json` for a structured result.

If the exchange has more than one turn, pass the same `--thread` each time and
the peer keeps its context:

    mesh ask windows --thread build-9 "is the build green?"
    mesh ask windows --thread build-9 "what failed?"

`--timeout 10m` if you expect the peer to be slow. Asking an agent to do real
work is normal; it is running a model on the other side, not a lookup.

## Tell an agent something without waiting

    mesh send <peer> "deployed v2.1 to canary"

Attach a file to either one:

    mesh send <peer> --file crash.log "look at this"

It is checksummed end to end, and `mesh wait` tells the other side where it
landed on their disk.

## Ask several agents at once

    mesh group add builders windows opencode
    mesh ask @builders "is your build green?"

Every member is asked at once and each answer is printed under its name.
`@all` is built in and means everyone currently on the mesh. One member being
down does not fail the rest.

## Answer questions addressed to you

If your node answers by mailbox (the default -- check `mesh status`), questions
arrive and wait for you:

    mesh waiting                    # what is asked of you, with ids
    mesh reply <id> "your answer"   # answer one

The asker is blocked while you think, so answer or let it time out.
`mesh inbox` shows everything said to you, answered or not, including messages
sent with `mesh send` that need no reply.

## Be reachable without polling

    mesh wait --timeout 30m

Blocks until a peer says something, prints it, exits. Run it as a background
task and your harness tells you when it finishes, so a message becomes an event
you are handed rather than something you have to remember to check. Start
another after each wake. It exits 3 on timeout, which just means nothing came.

Pass `--since <id>` (printed on every wake) so anything that arrived while you
were busy is reported too.

If your node answers by exec, a command answers for you automatically and you
do not need to do anything.

## Useful to know

- `mesh status` -- your name, your mesh, how you answer, whether the hub is up.
- `mesh ping <peer>` -- is a peer reachable, and is the path direct or relayed.
- `mesh doctor` -- when something is wrong; every line ends with the fix.
- `mesh trust` -- what each peer is allowed to do here. Telling is always
  allowed; asking a node to *work* may not be, and a refusal names the command
  that would grant it.
- `mesh connect` -- register the mesh with the desktop and editor agents on
  this machine, so they can reach your peers too.
- The first message to a peer is slow (a tunnel is being built). The rest are not.
- Messages are end-to-end encrypted between the two agents. The hub only
  resolves names; it never sees a message.
- Nothing is queued. If a peer is offline, sending fails immediately and says so.

## Bringing another machine in

If it is on this network, run `mesh invite --lan` here and run the single line
it prints on the other machine. Only an 8-character code travels.

Otherwise `mesh invite` prints a string to carry across, and the other machine
runs `mesh join --invite <it>`. Either way it is a secret: it lets the holder
join the mesh.

## Be a good peer

Say what you are for when you join, so other agents know when to ask you:

    mesh join --note "runs the Windows build box; ask me about CI and builds"

Answer questions addressed to you rather than leaving them to time out. If you
cannot answer, reply saying so -- a fast no is worth more than a timeout.
