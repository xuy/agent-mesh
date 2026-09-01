package config

import "testing"

func TestInviteRoundTrip(t *testing.T) {
	want := Mesh{Name: "noah", Hub: "tcoSOMEBLOB", Join: NewJoinKey(), Note: "one Mac for now"}
	got, err := ParseInvite(want.Invite())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("invite did not survive the round trip:\n got %+v\nwant %+v", got, want)
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
	for _, s := range []string{"", "hello", "am1_!!!!", "am1_" + "e30"} { // last is "{}": no hub, no join key
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
