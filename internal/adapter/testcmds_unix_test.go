//go:build !windows

package adapter

// Shell commands used by the adapter tests, written for the platform's shell.
// They exist per platform rather than being skipped on Windows because the
// behaviour under test -- env passing, streaming, error surfacing -- is exactly
// what differs between the two shells, so skipping would test nothing where it
// matters most.

const (
	cmdEchoBody     = `printf '%s' "$MESH_BODY"`
	cmdTwoLines     = `echo one; echo two`
	cmdFailStderr   = `echo "the model is not configured" >&2; exit 3`
	cmdEchoContinue = `printf '%s' "${MESH_CONTINUE:-none}"`
)

func cmdWriteFrom(path string) string { return `printf '%s' "$MESH_FROM" > '` + path + `'` }
