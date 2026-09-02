//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// stopProcess ends a process. Windows has no SIGTERM to send across process
// boundaries, so a detached daemon cannot be asked politely from here; prefer
// the control socket (`mesh down`), which shuts down cleanly on every platform.
func stopProcess(p *os.Process) error { return p.Kill() }

// processAlive reports whether a pid is still running. Windows has no signal 0:
// os.FindProcess succeeds for any pid, so liveness has to come from the exit
// code. STILL_ACTIVE (259) means it has not exited. A pid we cannot open is
// treated as gone, which is the safe answer for the one caller -- `mesh down`,
// which has already seen the control socket close.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
