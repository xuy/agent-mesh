package service

import (
	"strings"
	"testing"
)

// Each of these settings is a way the daemon dies quietly, and none of them has
// a command-line flag -- which is the whole reason this backend builds XML
// rather than calling schtasks with /SC ONLOGON.
func TestTaskXMLCarriesTheSettingsWithNoFlag(t *testing.T) {
	doc, err := taskXML(`GPU2\xulea`, `C:\Users\xulea\.local\bin\mesh.exe`, "windows")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// Defaults to 72 hours, after which Task Scheduler kills the daemon
		// and it looks like a node that vanished for no reason.
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		// On a laptop these two end the mesh when the charger comes out.
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
		// The actual crash restart. RestartOnFailure is NOT launchd's
		// KeepAlive -- it covers a task that fails to start, not a process
		// that died -- so recovery comes from repeating the trigger, made
		// harmless while the daemon is alive by IgnoreNew below.
		"<TimeTrigger>",
		"<Repetition>",
		"<Interval>PT1M</Interval>",
		"<StopAtDurationEnd>false</StopAtDurationEnd>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<RestartOnFailure>",
		// A logon trigger scoped to one user is what installs unelevated.
		"<LogonTrigger>",
		`<UserId>GPU2\xulea</UserId>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("task definition is missing %s", want)
		}
	}
}

func TestTaskXMLEscapes(t *testing.T) {
	// A Windows account name may legitimately contain an ampersand, and an
	// unescaped one makes the whole definition unparseable -- which schtasks
	// reports as a generic refusal, not as a quoting problem.
	doc, err := taskXML(`DOM\a&b`, `C:\p&q\mesh.exe`, "n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, "a&b") || strings.Contains(doc, "p&q") {
		t.Fatal("an ampersand reached the document unescaped")
	}
	if !strings.Contains(doc, "a&amp;b") || !strings.Contains(doc, "p&amp;q") {
		t.Fatal("expected the ampersands to be escaped")
	}
}

func TestUTF16LEHasABOM(t *testing.T) {
	// schtasks /XML rejects a definition that is not UTF-16.
	b := utf16LE("<Task/>")
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatal("missing the UTF-16 LE byte order mark")
	}
	if b[2] != '<' || b[3] != 0x00 {
		t.Fatalf("not little-endian UTF-16: % x", b[:6])
	}
}
