package node

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
)

// The local control channel is a unix socket on every platform. Windows has
// supported AF_UNIX since Windows 10 1803 and Go speaks it there, so this needs
// no platform split and inherits filesystem permissions rather than exposing a
// loopback port any local process could reach. If a Windows build ever proves
// this wrong, the fallback is a named pipe with an explicit ACL -- not a TCP
// port.
const controlNetwork = "unix"

// ListenControl opens the node's local control socket, clearing a stale one
// left behind by a daemon that was killed rather than stopped.
func ListenControl(name string) (net.Listener, error) {
	path := config.SockPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		if c, err := net.DialTimeout(controlNetwork, path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("%s is already running (its control socket is live)", name)
		}
		os.Remove(path)
	}
	ln, err := net.Listen(controlNetwork, path)
	if err != nil {
		return nil, err
	}
	os.Chmod(path, 0o600)
	return ln, nil
}

// RemoveControl deletes the control socket on shutdown.
func RemoveControl(name string) { os.Remove(config.SockPath(name)) }

func dialControl(name string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(controlNetwork, config.SockPath(name), timeout)
}
