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
	r, g, b := RGB("not-a-colour")
	if r < 0 || g < 0 || b < 0 || r > 255 || g > 255 || b > 255 {
		t.Fatalf("got %d,%d,%d", r, g, b)
	}
}

// The converted palettes must be available, and the hand tuned ones must win
// where the names collide: a mechanical mapping of a terminal palette does not
// always pick the colour a person would have.
func TestConvertedThemesAreAvailable(t *testing.T) {
	if len(Names()) < 300 {
		t.Fatalf("only %d themes; the converted set is not wired in", len(Names()))
	}
	for _, want := range []string{"dracula", "nord", "gruvbox", "catppuccin"} {
		if _, ok := Get(want); !ok {
			t.Fatalf("%s is missing", want)
		}
	}
}

func TestCuratedThemesTakePrecedence(t *testing.T) {
	got, ok := Get("dracula")
	if !ok {
		t.Fatal("dracula is missing")
	}
	if got.Accent != builtin[0].Accent {
		t.Fatalf("dracula accent is %s, want the curated %s", got.Accent, builtin[0].Accent)
	}
}

func TestEveryThemeIsUsable(t *testing.T) {
	for _, name := range Names() {
		th, ok := Get(name)
		if !ok {
			t.Fatalf("%s listed but not resolvable", name)
		}
		for label, c := range map[string]string{
			"accent": th.Accent, "ok": th.Ok, "warn": th.Warn,
			"bad": th.Bad, "muted": th.Muted, "rule": th.Rule, "fg": th.Fg,
		} {
			if len(c) != 7 || c[0] != '#' {
				t.Fatalf("theme %s has a bad %s colour: %q", name, label, c)
			}
		}
		if len(th.Spectrum) < 2 {
			t.Fatalf("theme %s has %d spectrum colours", name, len(th.Spectrum))
		}
	}
}

func TestNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range Names() {
		if seen[n] {
			t.Fatalf("duplicate theme name %q", n)
		}
		seen[n] = true
	}
}
