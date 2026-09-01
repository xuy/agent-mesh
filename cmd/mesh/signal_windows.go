//go:build windows

package main

import "os"

// stopProcess ends a process. Windows has no SIGTERM to send across process
// boundaries, so a detached daemon cannot be asked politely from here; prefer
// the control socket (`mesh down`), which shuts down cleanly on every platform.
func stopProcess(p *os.Process) error { return p.Kill() }
