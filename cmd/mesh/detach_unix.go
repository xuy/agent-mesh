//go:build !windows

package main

import "syscall"

// detachAttrs puts the daemon in its own session so it outlives the shell that
// started it -- including a shell that is killed rather than exited, which is
// the normal case when an agent starts one from a tool call.
func detachAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
