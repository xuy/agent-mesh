package pair

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xuy/agent-mesh/internal/config"
)

func testMesh() config.Mesh {
	return config.Mesh{Name: "noah", Hub: "tcoSOMEADDRESS", Join: "joinkey123"}
}

func TestHandoffDeliversTheInvite(t *testing.T) {
	m := testMesh()
	code := NewCode()
	o, err := OfferOn(m, code, "127.0.0.1:0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	got, err := Fetch(context.Background(), o.Addr(), code)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hub != m.Hub || got.Join != m.Join || got.Name != m.Name {
		t.Fatalf("invite did not survive the handoff: %+v", got)
	}
}

// The code is the entire security of the exchange, so a wrong one must yield
// nothing -- not a partial invite, not a distinguishable error.
func TestWrongCodeGetsNothing(t *testing.T) {
	o, err := OfferOn(testMesh(), NewCode(), "127.0.0.1:0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	_, err = Fetch(context.Background(), o.Addr(), "AAAAAAAA")
	if err == nil {
		t.Fatal("a wrong code got the invite")
	}
	if !strings.Contains(err.Error(), "code does not match") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestCodeIsForgivingAboutHowItIsTyped(t *testing.T) {
	o, err := OfferOn(testMesh(), "K7M29QPX", "127.0.0.1:0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	// Someone reading a code off another screen will lowercase it, or add the
	// dash they saw, or both.
	for _, typed := range []string{"K7M29QPX", "k7m29qpx", "K7M2-9QPX", " k7m2 9qpx "} {
		if _, err := Fetch(context.Background(), o.Addr(), typed); err != nil {
			t.Errorf("code typed as %q was rejected: %v", typed, err)
		}
	}
}

// A code is read off one screen and typed at another, so what matters is not
// that it avoids particular characters but that it never contains *both* halves
// of a confusable pair: 8 is fine as long as B is not also in the alphabet.
func TestCodeAlphabetHasNoConfusablePairs(t *testing.T) {
	pairs := []string{"O0", "IL", "I1", "L1", "B8", "S5", "Z2", "G6", "U V"}
	for _, p := range pairs {
		p = strings.ReplaceAll(p, " ", "")
		if strings.ContainsRune(codeAlphabet, rune(p[0])) && strings.ContainsRune(codeAlphabet, rune(p[1])) {
			t.Errorf("the alphabet contains both %q and %q, which people confuse", p[0], p[1])
		}
	}
	for i := 0; i < 200; i++ {
		for _, r := range NewCode() {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code contains %q, which is outside the alphabet", r)
			}
		}
	}
}

func TestCodesDiffer(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		c := NewCode()
		if seen[c] {
			t.Fatal("pairing codes repeat")
		}
		seen[c] = true
		if len(c) != CodeLength {
			t.Fatalf("code is the wrong length: %q", c)
		}
	}
}

func TestOfferReportsWhoTookIt(t *testing.T) {
	code := NewCode()
	o, err := OfferOn(testMesh(), code, "127.0.0.1:0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	if _, err := Fetch(context.Background(), o.Addr(), code); err != nil {
		t.Fatal(err)
	}
	select {
	case host := <-o.Taken():
		if host != "127.0.0.1" {
			t.Fatalf("reported the wrong taker: %q", host)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the offering machine was never told the invite was taken")
	}
}
