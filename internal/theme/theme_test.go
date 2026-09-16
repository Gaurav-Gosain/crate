package theme

import "strings"

import "testing"

func TestSetRejectsUnknownName(t *testing.T) {
	err := Set("solarizzled")
	if err == nil {
		t.Fatal("an unknown theme must be reported, not ignored: a typo in the config would otherwise look like a theme that renders identically to the default")
	}
	if !strings.Contains(err.Error(), "solarizzled") {
		t.Fatalf("the error should name the bad theme, got %v", err)
	}
}

func TestSetEmptyKeepsCurrent(t *testing.T) {
	before := Current().Name
	if err := Set(""); err != nil {
		t.Fatalf("an empty name means no preference, not an error: %v", err)
	}
	if Current().Name != before {
		t.Fatal("an empty name should leave the theme alone")
	}
}

func TestNextCyclesThroughAll(t *testing.T) {
	seen := map[string]bool{}
	start := Current().Name
	for range Names() {
		seen[Next()] = true
	}
	if len(seen) != len(Names()) {
		t.Fatalf("cycling visited %d of %d themes", len(seen), len(Names()))
	}
	if Current().Name != start {
		t.Fatalf("a full cycle should return to where it started, got %s want %s", Current().Name, start)
	}
}

func TestSpectrumAtStaysInGamut(t *testing.T) {
	th := Current()
	for _, p := range []float64{-1, 0, 0.5, 1, 2} {
		got := th.SpectrumAt(p)
		if !strings.HasPrefix(got, "\x1b[38;2;") {
			t.Fatalf("p=%v produced %q", p, got)
		}
	}
}

func TestUnparseableColourDoesNotProduceNegatives(t *testing.T) {
	// A bad hex value must not end up as a negative component, which would be
	// written into the escape sequence verbatim and corrupt the frame.
	r, g, b := rgb("not-a-colour")
	if r < 0 || g < 0 || b < 0 || r > 255 || g > 255 || b > 255 {
		t.Fatalf("got %d,%d,%d", r, g, b)
	}
}
