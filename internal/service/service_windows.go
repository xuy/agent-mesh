package service

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/xuy/agent-mesh/internal/config"
)

type schtasks struct{}

func newManager() Manager { return schtasks{} }

func (schtasks) Describe() string { return "Task Scheduler (a logon task)" }

func taskName(name string) string { return `\` + label(name) }

// Install registers a logon task.
//
// Written on a Mac and verified by the Windows node -- if it is wrong, the
// symptom is a task that exists but never starts, and `mesh service status`
// will say so. schtasks has no equivalent of launchd's KeepAlive on the command
// line, so this restarts at logon but not after a crash; getting crash restart
// needs a task definition in XML with RestartOnFailure, which is the obvious
// next step if it proves necessary.
func (s schtasks) Install(name, exe string) error {
	// MESH_HOME cannot be passed through schtasks, so the task relies on the
	// default location. A node using a custom MESH_HOME must set it as a user
	// environment variable for the task to find its state.
	cmd := fmt.Sprintf(`"%s" up --name %s --foreground`, exe, name)
	out, err := exec.Command("schtasks", "/Create", "/F",
		"/TN", taskName(name),
		"/TR", cmd,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks refused the task: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if config.Home() != "" {
		// Best effort: start it now so the node does not wait for a logon.
		exec.Command("schtasks", "/Run", "/TN", taskName(name)).Run()
	}
	return nil
}

func (s schtasks) Uninstall(name string) error {
	exec.Command("schtasks", "/End", "/TN", taskName(name)).Run()
	out, err := exec.Command("schtasks", "/Delete", "/F", "/TN", taskName(name)).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "cannot find") {
		return fmt.Errorf("schtasks: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s schtasks) Status(name string) (string, error) {
	out, err := exec.Command("schtasks", "/Query", "/TN", taskName(name), "/FO", "LIST").CombinedOutput()
	if err != nil {
		return "not installed", nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Status:") {
			return "installed, " + strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), nil
		}
	}
	return "installed", nil
}
