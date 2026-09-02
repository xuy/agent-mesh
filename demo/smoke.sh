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
printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
