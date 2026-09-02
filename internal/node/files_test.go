package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuy/agent-mesh/internal/wire"
)

func TestDescribeFilesRejectsWhatCannotBeSent(t *testing.T) {
	dir := t.TempDir()
	if _, err := describeFiles([]string{filepath.Join(dir, "missing")}); err == nil {
		t.Error("a missing file was accepted")
	}
	if _, err := describeFiles([]string{dir}); err == nil {
		t.Error("a directory was accepted as an attachment")
	}
}

func TestDescribeFilesSummarises(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(p, []byte("hello"), 0o600)
	got, err := describeFiles([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "notes.txt" || got[0].Size != 5 {
		t.Fatalf("wrong summary: %+v", got)
	}
	if got[0].Sum == "" {
		t.Error("no checksum, so a truncated transfer could not be detected")
	}
}

// Where a file lives on the sender's disk is nobody else's business, and
// leaking it hands a peer a map of the sender's filesystem.
func TestLocalPathsNeverGoOnTheWire(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret-project-notes.txt")
	os.WriteFile(p, []byte("x"), 0o600)
	described, err := describeFiles([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	if described[0].Path == "" {
		t.Fatal("the sender needs the path to read the file")
	}
	for _, f := range stripPaths(described) {
		if f.Path != "" {
			t.Errorf("an attachment announcement carried a local path: %q", f.Path)
		}
	}
}

// A peer chooses the attachment name, so it must not be able to choose where
// the file lands.
func TestAttachmentNamesCannotEscapeTheDirectory(t *testing.T) {
	for _, name := range []string{
		"../../etc/passwd", "..\\..\\windows\\system32\\x", "/etc/passwd",
		"....//evil", ".ssh/authorized_keys", "",
	} {
		got := safeName(name)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("safeName(%q) = %q, which still contains a path separator", name, got)
		}
		if strings.HasPrefix(got, ".") {
			t.Errorf("safeName(%q) = %q, which is a dotfile", name, got)
		}
		if got == "" {
			t.Errorf("safeName(%q) produced an empty name", name)
		}
	}
	if got := safeName("build log (2).txt"); got != "build log (2).txt" {
		t.Errorf("an ordinary name was mangled: %q", got)
	}
}

func TestAttachmentNamesAreBounded(t *testing.T) {
	if got := safeName(strings.Repeat("a", 500) + ".txt"); len(got) > 120 {
		t.Errorf("name is %d characters, which some filesystems will refuse", len(got))
	}
}

func TestOversizedAttachmentRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: no bytes are actually written, but the size is real.
	if err := f.Truncate(MaxFileSize + 1); err != nil {
		t.Skip("cannot make a sparse file here")
	}
	f.Close()
	if _, err := describeFiles([]string{p}); err == nil {
		t.Fatal("a file over the limit was accepted")
	}
}

func TestFileFramesRoundTrip(t *testing.T) {
	// The chunk field must survive JSON transport intact, including bytes that
	// are not valid UTF-8 -- which is most of any real file.
	raw := []byte{0x00, 0xff, 0xfe, 'h', 'i', 0x80}
	a, b := netPipe(t)
	go func() {
		wire.NewConn(a).Send(wire.Envelope{Kind: wire.KindFile, Index: 0, Chunk: raw})
	}()
	got, err := wire.NewConn(b).Recv()
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Chunk) != string(raw) {
		t.Fatalf("binary chunk was corrupted: % x", got.Chunk)
	}
}

// A daemon that runs for months must not quietly fill a disk.
func TestAppendOnlyFilesAreRolled(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "inbox.jsonl")
	big := make([]byte, maxLogBytes+1)
	if err := os.WriteFile(p, big, 0o600); err != nil {
		t.Fatal(err)
	}
	rollIfLarge(p)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("an oversized file was not rolled out of the way")
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Error("the previous file was not kept")
	}
	// A small file is left exactly alone.
	small := filepath.Join(dir, "small.jsonl")
	os.WriteFile(small, []byte("one line\n"), 0o600)
	rollIfLarge(small)
	if b, err := os.ReadFile(small); err != nil || string(b) != "one line\n" {
		t.Error("a small file was disturbed")
	}
}
