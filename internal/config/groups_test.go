package config

import (
	"reflect"
	"testing"
)

func TestGroupsAddAndRemove(t *testing.T) {
	g := Groups{}
	g.Add("builders", "windows", "opencode")
	g.Add("builders", "windows") // adding twice must not duplicate
	if want := []string{"opencode", "windows"}; !reflect.DeepEqual(g["builders"], want) {
		t.Fatalf("got %v, want %v", g["builders"], want)
	}
	g.Remove("builders", "windows")
	if want := []string{"opencode"}; !reflect.DeepEqual(g["builders"], want) {
		t.Fatalf("got %v, want %v", g["builders"], want)
	}
	// Removing the last member removes the group rather than leaving an empty
	// one that would fail confusingly at send time.
	g.Remove("builders", "opencode")
	if _, ok := g["builders"]; ok {
		t.Fatal("an emptied group was left behind")
	}
}

func TestMembersDropsPeersNoLongerOnTheMesh(t *testing.T) {
	g := Groups{"builders": {"windows", "ghost"}}
	got, err := g.Members("@builders", []string{"windows", "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"windows"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A group whose members have all left should say so, and say who is missing,
// rather than silently sending to nobody.
func TestMembersReportsWhenNobodyIsAround(t *testing.T) {
	g := Groups{"builders": {"ghost"}}
	_, err := g.Members("@builders", []string{"windows"})
	if err == nil {
		t.Fatal("a group with no reachable members reported success")
	}
	if !contains(err.Error(), "ghost") {
		t.Errorf("error does not say who is missing: %v", err)
	}
}

func TestAllGroupIsBuiltIn(t *testing.T) {
	g := Groups{}
	got, err := g.Members("@all", []string{"windows", "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("@all did not resolve to every peer: %v", got)
	}
}

func TestUnknownGroupSaysHowToMakeOne(t *testing.T) {
	_, err := Groups{}.Members("@nope", []string{"windows"})
	if err == nil {
		t.Fatal("an unknown group resolved")
	}
	if !contains(err.Error(), "mesh group add nope") {
		t.Errorf("error does not name the fix: %v", err)
	}
}

func TestIsGroup(t *testing.T) {
	if !IsGroup("@builders") || IsGroup("windows") {
		t.Fatal("group addresses are not recognised by their marker")
	}
	if GroupName("@builders") != "builders" {
		t.Fatal("group marker not stripped")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
