//go:build !windows

package main

import (
	"os"
	"syscall"
)

// stopProcess asks a process to shut down cleanly.
func stopProcess(p *os.Process) error { return p.Signal(syscall.SIGTERM) }

// processAlive reports whether a pid is still running. Signal 0 performs the
// permission and existence checks without delivering anything; EPERM means the
// process exists and is not ours, which still counts as alive.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
