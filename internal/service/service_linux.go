package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xuy/agent-mesh/internal/config"
)

type systemd struct{}

func newManager() Manager { return systemd{} }

func (systemd) Describe() string { return "systemd (a user unit)" }

func unitPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", label(name)+".service")
}

func (s systemd) Install(name, exe string) error {
	unit := fmt.Sprintf(`[Unit]
Description=agent-mesh node %s
After=network-online.target

[Service]
Type=simple
Environment=MESH_HOME=%s
ExecStart=%s up --name %s --foreground
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, name, config.Home(), exe, name)

	p := unitPath(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", label(name)+".service").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// Without lingering, a user unit stops when the last session ends, which
	// is exactly when an unattended machine should keep running.
	if err := exec.Command("loginctl", "enable-linger").Run(); err != nil {
		return fmt.Errorf("the service is installed, but this user is not set to linger, "+
			"so it will stop when you log out. Enable it with: sudo loginctl enable-linger %s", os.Getenv("USER"))
	}
	return nil
}

func (s systemd) Uninstall(name string) error {
	exec.Command("systemctl", "--user", "disable", "--now", label(name)+".service").Run()
	if err := os.Remove(unitPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func (s systemd) Status(name string) (string, error) {
	if _, err := os.Stat(unitPath(name)); os.IsNotExist(err) {
		return "not installed", nil
	}
	out, _ := exec.Command("systemctl", "--user", "is-active", label(name)+".service").CombinedOutput()
	return "installed, " + strings.TrimSpace(string(out)), nil
}
