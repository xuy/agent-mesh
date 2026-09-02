//go:build windows

package adapter

// PowerShell counterparts of the Unix test commands. See the note in
// testcmds_unix_test.go.
//
// [Console]::Out.Write is used instead of Write-Output where the test compares
// exact bytes, because Write-Output appends a newline.

const (
	cmdEchoBody     = `[Console]::Out.Write($env:MESH_BODY)`
	cmdTwoLines     = `Write-Output "one"; Write-Output "two"`
	cmdFailStderr   = `[Console]::Error.WriteLine("the model is not configured"); exit 3`
	cmdEchoContinue = `$v = $env:MESH_CONTINUE; if (-not $v) { $v = "none" }; [Console]::Out.Write($v)`
)

func cmdWriteFrom(path string) string {
	return `Set-Content -NoNewline -LiteralPath '` + path + `' -Value $env:MESH_FROM`
}
