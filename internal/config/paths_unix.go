//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

func defaultHome() string {
	d, err := os.UserHomeDir()
	if err != nil {
		return ".agent-mesh"
	}
	return filepath.Join(d, ".agent-mesh")
}

// socketDir is the OS temp dir on Unix because a unix socket path has a hard
// ~104-byte limit and MESH_HOME may be deep.
func socketDir() string { return os.TempDir() }
