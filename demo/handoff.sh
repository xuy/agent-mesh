#!/bin/sh
# Hand a task from a desktop chat to a coding agent, including the agent asking
# a question back mid-task -- using nothing but messages.
#
# There is no task type on the wire here. No task id, no state machine, no
# lifecycle. Two agents, a shared thread, and messages going both ways. The
# "task" is a convention the two ends agree on, which is the point: a different
# pair of agents can agree on a different one without the mesh having an
# opinion. This is the argument for why the mesh has no task model, run rather
# than asserted.
#
# Everything lives in a throwaway directory. Usage: demo/handoff.sh
set -eu

ROOT=$(mktemp -d)
THREAD="handoff-$$"
cleanup() {
	MESH_HOME="$ROOT/desk" MESH_NAME=desk mesh down >/dev/null 2>&1 || true
	MESH_HOME="$ROOT/coder" MESH_NAME=coder mesh down >/dev/null 2>&1 || true
	rm -rf "$ROOT"
}
trap cleanup EXIT

desk() { MESH_HOME="$ROOT/desk" MESH_NAME=desk mesh "$@"; }
coder() { MESH_HOME="$ROOT/coder" MESH_NAME=coder mesh "$@"; }
say() { printf '\n\033[1m%s\033[0m\n' "$1"; }

echo "bringing up two nodes"
desk join --name desk --mesh handoff --note "stands in for a desktop chat" >/dev/null
coder join --invite "$(desk invite)" --name coder --note "stands in for a coding agent" >/dev/null

say "1. the desktop hands work to the coding agent"
desk send coder --thread "$THREAD" "check whether the build is green and tell me what failed"

say "2. the coding agent is woken by it, and needs to know which branch"
coder wait --timeout 30s
coder send desk --thread "$THREAD" "which branch -- main, or the release branch?"

say "3. the desktop is woken by the question and answers on the same thread"
desk wait --timeout 30s
desk send coder --thread "$THREAD" "main"

say "4. the coding agent is woken by the answer and reports back"
coder wait --timeout 30s
coder send desk --thread "$THREAD" "main is green: 412 tests, 0 failures"

say "5. the desktop receives the result"
desk wait --timeout 30s

say "the whole exchange: one thread, both directions, no task model"
desk inbox --all -n 4
