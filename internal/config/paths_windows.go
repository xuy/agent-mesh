//go:build windows

package config

import (
	"os"
	"path/filepath"
)

func defaultHome() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "agent-mesh")
	}
	d, err := os.UserHomeDir()
	if err != nil {
		return "agent-mesh"
	}
	return filepath.Join(d, "AppData", "Local", "agent-mesh")
}

// socketDir keeps the control socket beside the rest of the node's state on
// Windows, where the Unix path-length limit does not apply and the user's
// AppData directory already carries a user-only ACL.
func socketDir() string { return defaultHome() }
