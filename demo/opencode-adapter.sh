#!/bin/sh
# Answer a mesh question with opencode, printing only the answer.
#
# The mesh runs this with the question in $MESH_BODY (never interpolated into a
# command line, so a peer cannot inject shell syntax). $MESH_CONTINUE is
# "--continue" when this thread has been seen before, which is what keeps model
# context across the turns of one conversation.
set -eu

cd "${MESH_WORKDIR:-$HOME/src/noah-mesh}"

# Capture before filtering: a pipeline reports only its last command's status,
# so filtering inline would turn an opencode failure into a silent empty answer.
if ! out=$(opencode run --agent "${MESH_OPENCODE_AGENT:-build}" ${MESH_CONTINUE:-} "$MESH_BODY" </dev/null 2>&1); then
  printf 'opencode failed:\n%s\n' "$out" >&2
  exit 1
fi

# opencode writes a banner and ANSI colour; the mesh wants the answer alone.
printf '%s\n' "$out" \
  | perl -pe 's/\e\[[0-9;?]*[a-zA-Z]//g' \
  | sed -E '/^> .+ · .+$/d' \
  | sed -e '/./,$!d'
