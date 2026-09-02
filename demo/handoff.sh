#!/bin/sh
# Hand a task from a desktop chat to a coding agent, including the agent
# asking a question back mid-task -- using nothing but messages.
#
# There is no task type on the wire here. No task id, no state machine, no
# lifecycle. Just two agents, a shared thread, and messages going both ways.
# The "task" is a convention the two ends agree on, which is the point: a
# different pair of agents can agree on a different one without the mesh
# having an opinion.
#
# Usage: demo/handoff.sh <desk-home> <coder-home>
set -eu

DESK_HOME=$1
CODER_HOME=$2
THREAD="handoff-$$"

desk()  { MESH_HOME="$DESK_HOME"  MESH_NAME=desk  mesh "$@"; }
coder() { MESH_HOME="$CODER_HOME" MESH_NAME=coder mesh "$@"; }

say() { printf '\n\033[1m%s\033[0m\n' "$1"; }

say "1. the desktop hands work to the coding agent"
desk send coder --thread "$THREAD" "check whether the build is green and tell me what failed"

say "2. the coding agent is woken by it, and needs to know which branch"
coder wait --timeout 30s --json | head -20
coder send desk --thread "$THREAD" "which branch -- main, or the release branch?"

say "3. the desktop is woken by the question and answers on the same thread"
desk wait --timeout 30s --json | head -20
desk send coder --thread "$THREAD" "main"

say "4. the coding agent is woken by the answer and reports back"
coder wait --timeout 30s --json | head -20
coder send desk --thread "$THREAD" "main is green: 412 tests, 0 failures"

say "5. the desktop receives the result"
desk wait --timeout 30s

say "the whole exchange, one thread, both directions"
desk inbox --all -n 6
