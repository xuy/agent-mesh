#!/bin/sh
# Exercise every claim the README makes, on a throwaway mesh.
#
# Real tunnels and real relays, so it takes a couple of minutes and needs a
# network. That is the point: the things that break are the things only a real
# tunnel exercises, and none of them show up in `go test`.
#
# Usage: demo/smoke.sh    (leaves nothing behind)
set -eu

ROOT=$(mktemp -d)
A="$ROOT/a"
B="$ROOT/b"
PASS=0
FAIL=0

cleanup() {
	MESH_HOME="$A" MESH_NAME=alpha mesh down >/dev/null 2>&1 || true
	MESH_HOME="$B" MESH_NAME=beta mesh down >/dev/null 2>&1 || true
	rm -rf "$ROOT"
}
trap cleanup EXIT

a() { MESH_HOME="$A" MESH_NAME=alpha mesh "$@"; }
b() { MESH_HOME="$B" MESH_NAME=beta mesh "$@"; }


expect() {
	desc=$1
	want=$2
	got=$3
	case "$got" in
	*"$want"*)
		printf '  ok    %s\n' "$desc"
		PASS=$((PASS + 1))
		;;
	*)
		printf '  FAIL  %s\n         wanted %s\n         got    %s\n' "$desc" "$want" "$got"
		FAIL=$((FAIL + 1))
		;;
	esac
}

echo "bringing up two nodes (this is the slow part)"
a join --name alpha --mesh smoke --note "the first node" >/dev/null
INVITE=$(a invite)
b join --invite "$INVITE" --name beta --note "answers by running a command" \
	--exec 'printf "beta ran: %s" "$MESH_BODY"' >/dev/null

echo
echo "the mesh"
expect "alpha sees beta"            "beta"        "$(a peers)"
expect "beta sees alpha"            "alpha"       "$(b peers)"
expect "alpha is reachable"         '"relay"'     "$(a status --json)"
# A throwaway node deliberately has no service installed, so that one FIX is
# expected; anything else doctor complains about is a real problem.
PROBLEMS=$(a doctor | grep FIX | grep -v "reboot\|mesh service install" || true)
if [ -z "$PROBLEMS" ]; then
	printf '  ok    doctor reports no unexpected problems\n'
	PASS=$((PASS + 1))
else
	printf '  FAIL  doctor reports:\n%s\n' "$PROBLEMS"
	FAIL=$((FAIL + 1))
fi

echo
echo "messages"
expect "a tell is delivered"        "delivered"   "$(a send beta 'hello')"
expect "an exec node answers"       "beta ran"    "$(a ask beta --timeout 60s 'what is your name')"
expect "context survives a thread"  "beta ran"    "$(a ask beta --thread t1 --timeout 60s 'again')"

echo
echo "files"
printf 'the quick brown fox\n' > "$ROOT/note.txt"
a send beta --file "$ROOT/note.txt" "a file for you" >/dev/null
sleep 1
RECV=$(find "$B/nodes/beta/files" -name note.txt 2>/dev/null | head -1)
expect "an attachment arrives"      "note.txt"    "${RECV:-missing}"
if [ -n "${RECV:-}" ]; then
	expect "its bytes are intact"   "quick brown" "$(cat "$RECV")"
fi

echo
echo "groups"
a group add pair beta >/dev/null
expect "a group fans out"           "beta ran"    "$(a ask @pair --timeout 60s 'group question')"
expect "@all is built in"           "beta ran"    "$(a ask @all --timeout 60s 'everyone')"

echo
echo "who may do what"
expect "beta pinned alpha's key"    "alpha"       "$(b trust)"
b block alpha >/dev/null
expect "a block bites immediately"  "blocked"     "$(a send beta 'still there?' 2>&1 || true)"
b unblock alpha >/dev/null
expect "unblock restores it"        "delivered"   "$(a send beta 'back' 2>&1)"
expect "refusals are recorded"      "REFUSED"     "$(b log)"

echo
echo "being reachable without polling"
( sleep 2; b send alpha "woke you" >/dev/null 2>&1 ) &
expect "wait returns on a message"  "woke you"    "$(a wait --timeout 45s)"

echo
echo "a peer that is not there"
# The queue only matters across a real absence, so beta is actually stopped
# rather than simulated -- the failure this replaces is a message lost to a peer
# that restarted a second ago.
b down >/dev/null 2>&1 || true
# Wait for alpha to SEE beta go, not merely for beta to stop. Presence comes
# from the coordinator noticing the control connection close, and a fixed sleep
# here delivered the message to a beta that was still up -- the first version of
# this test passed for the wrong reason.
i=0
while [ "$i" -lt 30 ]; do
	case "$(a peers)" in
	*beta*offline*) break ;;
	esac
	sleep 2
	i=$((i + 1))
done
expect "a tell to a stopped peer is queued"  "queued for beta"           "$(a send beta 'held while you were away' 2>&1)"
expect "the outbox says what is waiting"     "held while you were away"  "$(a outbox)"
expect "and why"                             "offline"                   "$(a outbox)"

b up >/dev/null 2>&1
# Delivery rides the roster update, which follows beta re-registering.
i=0
while [ "$i" -lt 30 ]; do
	case "$(a outbox)" in
	*"nothing waiting"*) break ;;
	esac
	sleep 2
	i=$((i + 1))
done
expect "it drains when the peer comes back"  "nothing waiting"           "$(a outbox)"
expect "and the message actually arrived"    "held while you were away"  "$(b inbox)"

# A queue also has to have a way out, or a test run leaves messages that reach
# someone weeks later with no idea what they refer to. Stop beta again so the
# queue is real rather than delivered instantly.
b down >/dev/null 2>&1 || true
i=0
while [ "$i" -lt 30 ]; do
	case "$(a peers)" in
	*beta*offline*) break ;;
	esac
	sleep 2
	i=$((i + 1))
done
a send beta 'junk from a test run' >/dev/null 2>&1
expect "a queue can be discarded"            "discarded 1 message"       "$(a outbox --drop beta)"
expect "and then nothing is waiting"         "nothing waiting"           "$(a outbox)"
b up >/dev/null 2>&1

echo
echo "reaching the mesh as tools"
# The MCP surface is how every desktop and editor harness reaches the mesh, so
# it is checked the way a client drives it rather than by reading the code.
MCP=$(printf '%s\n' \
	'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
	'{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
	'{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mesh_peers","arguments":{}}}' \
	| MESH_HOME="$A" MESH_NAME=alpha mesh mcp 2>/dev/null)
expect "it announces its tools"      "mesh_ask"   "$MCP"
expect "and answers a tool call"     "beta"       "$MCP"

# And against a real client, when one is installed: our own JSON-RPC agreeing
# with itself proves less than a client we did not write agreeing with us.
if command -v claude >/dev/null 2>&1; then
	CH="$ROOT/clienthome"
	mkdir -p "$CH"
	HOME="$CH" claude mcp add --scope user agent-mesh -- \
		"$(command -v mesh)" mcp --name alpha >/dev/null 2>&1
	expect "a real MCP client connects" "Connected" \
		"$(HOME="$CH" MESH_HOME="$A" MESH_NAME=alpha claude mcp list 2>&1 || true)"
fi

echo
printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
