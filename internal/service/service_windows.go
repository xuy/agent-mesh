package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/xuy/agent-mesh/internal/config"
)

type schtasks struct{}

func newManager() Manager { return schtasks{} }

func (schtasks) Describe() string { return "Task Scheduler (a logon task)" }

func taskName(name string) string { return `\` + label(name) }

// Install registers a logon task from an XML definition.
//
// The obvious form, `schtasks /Create /SC ONLOGON`, cannot be used: it fails
// with "Access is denied" unless the shell is elevated. Verified on Windows 11
// by elimination -- ONCE, HOURLY and MINUTE all succeed unelevated while
// ONLOGON and ONSTART do not, in the root folder and in a subfolder alike. A
// logon trigger scoped to one named user, which is what the XML below carries,
// needs no such privilege, so the XML path is the one that works for a person
// who has not thought about elevation.
//
// It also carries three settings that have no command-line flag, each of which
// is a way for the daemon to die quietly:
//
//   - ExecutionTimeLimit defaults to 72 hours. Task Scheduler kills the daemon
//     three days in, which reads as a node that vanished for no reason.
//   - The battery defaults stop the task on battery and refuse to start it
//     there, so on a laptop the mesh ends when the charger comes out.
//   - Nothing restarts a daemon that dies. RestartOnFailure looks like
//     launchd's KeepAlive and is not: it covers a task that fails to *start*,
//     while a process that exits non-zero counts as the task completing. Killed
//     the daemon and watched Task Scheduler record "Last Result: 1", state
//     "Ready", and do nothing for 200 seconds. The mechanism that does work is
//     a repeating TimeTrigger, which fires every minute forever;
//     MultipleInstancesPolicy IgnoreNew makes every tick a no-op while the
//     daemon is alive, so the repeat only has an effect once it is not. The
//     repetition has to hang off a TimeTrigger and not the LogonTrigger: a
//     trigger's repetition only arms when the trigger itself fires, so a logon
//     trigger repeats nothing until the next logon -- which is never, for the
//     session that just installed it.
//
// MESH_HOME still cannot be passed through -- Task Scheduler has no environment
// element -- so a node using a custom location needs it set as a user
// environment variable.
func (s schtasks) Install(name, exe string) error {
	who, err := user.Current()
	if err != nil {
		return fmt.Errorf("cannot tell which user to run as: %w", err)
	}
	doc, err := taskXML(who.Username, exe, name)
	if err != nil {
		return err
	}

	// schtasks reads the definition from a file, and wants UTF-16.
	f, err := os.CreateTemp("", "agent-mesh-task-*.xml")
	if err != nil {
		return fmt.Errorf("writing the task definition: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	_, err = f.Write(utf16LE(doc))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing the task definition: %w", err)
	}

	out, err := exec.Command("schtasks", "/Create", "/F",
		"/TN", taskName(name),
		"/XML", filepath.Clean(path),
	).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "Access is denied") {
			return fmt.Errorf("Task Scheduler refused the task: %s\n"+
				"run this from an elevated shell, or install it by hand from %s", msg, path)
		}
		return fmt.Errorf("schtasks refused the task: %v: %s", err, msg)
	}
	if config.Home() != "" {
		// Best effort: start it now so the node does not wait for a logon.
		exec.Command("schtasks", "/Run", "/TN", taskName(name)).Run()
	}
	return nil
}

// taskXML renders the task definition. Every value that comes from outside --
// the user's name and the path to the binary -- is XML-escaped, because a
// Windows account name may legitimately contain an ampersand.
func taskXML(username, exe, node string) (string, error) {
	esc := func(s string) (string, error) {
		var b bytes.Buffer
		if err := xml.EscapeText(&b, []byte(s)); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	u, err := esc(username)
	if err != nil {
		return "", err
	}
	cmd, err := esc(exe)
	if err != nil {
		return "", err
	}
	args, err := esc(fmt.Sprintf("up --name %s --foreground", node))
	if err != nil {
		return "", err
	}
	// A start boundary just in the past, so the watchdog's first tick is due
	// immediately rather than a minute after installing.
	started := time.Now().Add(-time.Minute).Format("2006-01-02T15:04:05")
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>agent-mesh node ` + esc0(node) + `</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + u + `</UserId>
    </LogonTrigger>
    <TimeTrigger>
      <Enabled>true</Enabled>
      <StartBoundary>` + started + `</StartBoundary>
      <Repetition>
        <Interval>PT1M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
    </TimeTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + u + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + cmd + `</Command>
      <Arguments>` + args + `</Arguments>
    </Exec>
  </Actions>
</Task>
`, nil
}

// esc0 escapes for the one place a failure cannot be reported, the description.
func esc0(s string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return ""
	}
	return b.String()
}

// utf16LE encodes with a byte order mark, which is what schtasks /XML expects.
func utf16LE(s string) []byte {
	rs := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2+len(rs)*2)
	b = append(b, 0xFF, 0xFE)
	for _, r := range rs {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
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
