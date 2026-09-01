//go:build !windows

package adapter

// shellArgv returns the argv that runs cmd through the platform's shell.
// An adapter command reads the question from "$MESH_BODY" or stdin.
func shellArgv(shell, cmd string) []string {
	if shell == "" {
		shell = "sh"
	}
	return []string{shell, "-c", cmd}
}

// ShellHint describes how an adapter command refers to the question on this
// platform, for error messages and docs.
const ShellHint = `sh -c, so refer to the question as "$MESH_BODY"`
