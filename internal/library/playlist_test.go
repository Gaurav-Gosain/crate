package library

import "testing"

// A filename has been through yt-dlp's sanitiser, which swaps out the
// characters a filesystem will not take. Folding has to see through that or
// no track ever matches its own title.
func TestFoldIgnoresSanitisedCharacters(t *testing.T) {
	title := "Hukam (Full Video) Karan Aujla | Latest: Punjabi/Songs"
	file := "Hukam (Full Video) Karan Aujla ｜ Latest： Punjabi⧸Songs"
	if fold(title) != fold(file) {
		t.Fatalf("sanitised filename did not match its title\n title=%q\n file=%q", fold(title), fold(file))
	}
}

func TestTrackNumberStripped(t *testing.T) {
	for _, s := range []string{"04 - Jhanjar", "4 - Jhanjar", "04. Jhanjar", "4 Jhanjar"} {
		if got := fold(trackNumRe.ReplaceAllString(s, "")); got != "jhanjar" {
			t.Fatalf("%q folded to %q", s, got)
		}
	}
}

func TestSafeNameStripsPathSeparators(t *testing.T) {
	if got := safeName("AC/DC: Live"); got != "AC_DC_ Live" {
		t.Fatalf("got %q", got)
	}
}

func TestFoldIsCaseAndSpacingInsensitive(t *testing.T) {
	if fold("Don't  Look") != fold("DONT LOOK") {
		t.Fatal("folding should ignore case, spacing and punctuation")
	}
}
