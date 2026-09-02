// Package service keeps a node's daemon running without anyone tending it.
//
// A mesh that stops at the next reboot is not infrastructure, it is a demo. The
// daemon already reconnects on its own -- it retries the coordinator with
// backoff, keeps a cached roster so peers stay reachable while discovery is
// down, and rebuilds a tunnel whose peer restarted -- but nothing brings it
// back after a crash or a reboot. That is what the platform's own service
// manager is for, so this registers with it rather than inventing a supervisor.
package service

import (
	"fmt"
	"os"
)

// Manager registers a node's daemon with the platform's service manager.
type Manager interface {
	// Describe names the mechanism, so errors and docs can be specific.
	Describe() string
	// Install makes the node start at login and come back after a crash.
	Install(name, exe string) error
	// Uninstall removes it. Removing something absent is not an error.
	Uninstall(name string) error
	// Status reports what the service manager thinks, in one line.
	Status(name string) (string, error)
}

// New returns the manager for this platform.
func New() Manager { return newManager() }

// Executable returns the path to install, resolving symlinks so the service
// keeps working after the binary is replaced through a link.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find this binary to install: %w", err)
	}
	return exe, nil
}

// label is the service identifier for a node, shared by every platform so a
// person sees the same name wherever they look.
func label(name string) string { return "agent-mesh-" + name }
