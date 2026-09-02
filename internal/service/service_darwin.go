package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuy/agent-mesh/internal/config"
)

type launchd struct{}

func newManager() Manager { return launchd{} }

func (launchd) Describe() string { return "launchd (a per-user agent)" }

func plistPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label(name)+".plist")
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func (l launchd) Install(name, exe string) error {
	log := config.LogPath(name)
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return err
	}
	// KeepAlive is what makes this self-healing: launchd restarts the daemon
	// if it exits for any reason, including a crash.
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string><string>up</string>
    <string>--name</string><string>%s</string>
    <string>--foreground</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict><key>MESH_HOME</key><string>%s</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label(name), exe, name, config.Home(), log, log)

	p := plistPath(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		return err
	}
	// Replace any previous registration rather than failing on it.
	exec.Command("launchctl", "bootout", domain()+"/"+label(name)).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain(), p).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl refused the service: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (l launchd) Uninstall(name string) error {
	exec.Command("launchctl", "bootout", domain()+"/"+label(name)).Run()
	if err := os.Remove(plistPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l launchd) Status(name string) (string, error) {
	if _, err := os.Stat(plistPath(name)); os.IsNotExist(err) {
		return "not installed", nil
	}
	out, err := exec.Command("launchctl", "print", domain()+"/"+label(name)).CombinedOutput()
	if err != nil {
		return "installed, but launchd does not have it loaded", nil
	}
	state := "loaded"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "state = ") {
			state = strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
			break
		}
	}
	return "installed, " + state, nil
}
