//go:build windows

package adapter

// shellArgv returns the argv that runs cmd through the platform's shell.
// An adapter command reads the question from $env:MESH_BODY or stdin.
//
// -NoProfile keeps a user's profile from writing banner text into what the
// mesh will treat as the answer; -NonInteractive makes a command that would
// have prompted fail instead of hanging a peer that is waiting on it.
func shellArgv(shell, cmd string) []string {
	if shell == "" {
		shell = "powershell"
	}
	return []string{shell, "-NoProfile", "-NonInteractive", "-Command", cmd}
}

// ShellHint describes how an adapter command refers to the question on this
// platform, for error messages and docs.
const ShellHint = `powershell -Command, so refer to the question as $env:MESH_BODY`
