//go:build !windows

package main

import (
	"os"
	"syscall"
)

// stopProcess asks a process to shut down cleanly.
func stopProcess(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
