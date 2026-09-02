package config

import (
	"strings"
	"testing"
)

func TestInviteCarriesWhatJoiningNeeds(t *testing.T) {
	want := Mesh{Name: "noah", Hub: "tcoSOMEBLOB", Join: NewJoinKey(), Note: "one Mac for now"}
	got, err := ParseInvite(want.Invite())
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name || got.Hub != want.Hub || got.Join != want.Join {
		t.Fatalf("invite lost something joining needs:\n got %+v\nwant %+v", got, want)
	}
	// Note is decoration; leaving it out keeps the string shorter, and the
	// coordinator can say it once the node is in.
	if got.Note != "" {
		t.Errorf("invite is carrying decoration: %q", got.Note)
	}
}

// An invite is read off one screen and typed at another, so its length is a
// feature, not an implementation detail. This is the guard against quietly
// growing it back.
func TestInviteStaysShortEnoughToCarry(t *testing.T) {
	m := Mesh{
		Name: "erics-macbook-pro",
		// A real address, region-shortened, is about 100 characters.
		Hub:  "tco" + strings.Repeat("x", 97),
		Join: NewJoinKey(),
		Note: "a note that should not appear in the invite at all, however long it gets",
	}
	if n := len(m.Invite()); n > 180 {
		t.Fatalf("the invite has grown to %d characters; it has to stay short enough to type", n)
	}
}

func TestInviteSurvivesSurroundingWhitespace(t *testing.T) {
	m := Mesh{Name: "noah", Hub: "tcoADDR", Join: "k1"}
	got, err := ParseInvite("  " + m.Invite() + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hub != m.Hub {
		t.Fatal("a pasted invite with stray whitespace was rejected")
	}
}

func TestSanitizeMeshName(t *testing.T) {
	// The name travels in a dot-separated invite, so it cannot contain a dot,
	// and it should survive a hostname with capitals or punctuation.
	for in, want := range map[string]string{
		"Eric's MacBook Pro": "eric-s-macbook-pro",
		"noah.local":         "noah-local",
		"":                   "mesh",
		"---":                "mesh",
	} {
		if got := SanitizeMeshName(in); got != want {
			t.Errorf("SanitizeMeshName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"a.b", "x y", "Ünicode"} {
		if strings.Contains(SanitizeMeshName(in), ".") {
			t.Errorf("sanitized name still contains a dot, which would split the invite: %q", in)
		}
	}
}

// The coordinator is a fact about one machine. If an invite carried it, a node
// on the far machine sharing that name would believe it coordinates the mesh
// and would publish its own address as the one everyone should join.
func TestInviteDoesNotCarryTheCoordinator(t *testing.T) {
	m := Mesh{Name: "noah", Hub: "tcoBLOB", Join: "k", Coordinator: "master"}
	got, err := ParseInvite(m.Invite())
	if err != nil {
		t.Fatal(err)
	}
	if got.Coordinator != "" {
		t.Fatalf("invite carried the coordinator across machines: %q", got.Coordinator)
	}
}

func TestParseInviteRejectsJunk(t *testing.T) {
	for _, s := range []string{"", "hello", "am1.", "am1.noah", "am1.noah.tcoADDR", "am1.noah.notanaddress.key"} {
		if _, err := ParseInvite(s); err == nil {
			t.Errorf("accepted %q as an invite", s)
		}
	}
}

func TestJoinKeysDiffer(t *testing.T) {
	if NewJoinKey() == NewJoinKey() {
		t.Fatal("join keys are not random")
	}
}
