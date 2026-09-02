#!/bin/sh
# Two agents on one machine, talking. The shortest thing that shows the point.
#
#   master   answers by mailbox -- a person or an agent replies by hand
#   opencode answers by exec    -- opencode runs and its model replies
#
# Everything lives in a throwaway directory, so this cannot disturb a mesh you
# already have. Needs `opencode` on PATH for the second half.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
ROOT=$(mktemp -d)
cleanup() {
	MESH_HOME="$ROOT/m" MESH_NAME=master mesh down >/dev/null 2>&1 || true
	MESH_HOME="$ROOT/o" MESH_NAME=opencode mesh down >/dev/null 2>&1 || true
	rm -rf "$ROOT"
}
trap cleanup EXIT

master() { MESH_HOME="$ROOT/m" MESH_NAME=master mesh "$@"; }
oc() { MESH_HOME="$ROOT/o" MESH_NAME=opencode mesh "$@"; }

echo "--- the first agent founds the mesh; there is no server to start ---"
master join --name master --mesh demo --agent claude-code \
	--note "Claude Code; ask me about this machine's repos"

echo
echo "--- the second joins with the invite the first printed ---"
oc join --invite "$(master invite)" --name opencode --agent opencode \
	--note "opencode; ask me to read code or answer questions" \
	--exec "$here/opencode-adapter.sh"

echo
echo "--- who is here, as master sees it ---"
master peers

echo
echo "--- master asks opencode something, and a model answers ---"
master ask opencode --timeout 3m "In one sentence: what model are you?"

echo
echo "--- the same conversation, continued: it remembers ---"
master ask opencode --timeout 3m --thread demo "And which directory are you running in?"

echo
echo "--- the other direction: opencode asks master, who answers by hand ---"
oc send master "the build finished"
master inbox -n 1

echo
echo "Done. Everything above lived in $ROOT and is now gone."
