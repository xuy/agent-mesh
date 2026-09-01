#!/bin/sh
# Bring up a two-agent mesh on this machine and make them talk.
#
# master  -- answers by mailbox (a human or an agent answers by hand)
# opencode -- answers by exec (opencode runs and its model answers)
set -eu

here=$(cd "$(dirname "$0")" && pwd)

mesh hub --mesh demo --note "two agents on one Mac"

mesh join --name master --agent claude-code \
  --note "Claude Code; ask me about this machine's repos"

mesh join --name opencode --agent opencode \
  --note "opencode; ask me to read code or answer questions" \
  --exec "$here/opencode-adapter.sh"

echo
echo "--- who is here, as master sees it ---"
MESH_NAME=master mesh peers

echo
echo "--- master asks opencode something ---"
MESH_NAME=master mesh ask opencode --timeout 3m "In one sentence: what model are you?"

echo
echo "--- and the same conversation, continued ---"
MESH_NAME=master mesh ask opencode --timeout 3m --thread demo "Which directory are you running in?"

echo
echo "Now try the other direction: run"
echo "    MESH_NAME=opencode mesh ask master 'what branch is this repo on?'"
echo "and answer it from another terminal with"
echo "    MESH_NAME=master mesh waiting"
echo "    MESH_NAME=master mesh reply <id> 'your answer'"
