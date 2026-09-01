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
