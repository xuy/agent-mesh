//go:build windows

package main

import "syscall"

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detachAttrs is the Windows equivalent of a new session: no inherited console,
// and its own process group so a Ctrl-C in the parent's console does not reach
// the daemon.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}
