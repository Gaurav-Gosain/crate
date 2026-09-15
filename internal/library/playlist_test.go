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

// yt-dlp truncates long filenames, so what is left on disk is a prefix of the
// title. A whole 42 track playlist matched 3 entries before this was handled.
func TestLookupMatchesTruncatedFilename(t *testing.T) {
	full := "गणेश गायत्री मंत्र ｜ Ganesh Gayatri Mantra ｜ Sadhana Sargam ｜ Ganpati Songs"
	cut := "गणेश गायत्री मंत्र ｜ Ganesh Gayatri Mantra ｜ Sadhana Sar"
	ix := libIndex{byKey: map[string]string{fold(cut): "a/b/20 - cut.opus"}}
	ix.keys = []string{fold(cut)}
	if got, ok := ix.lookup(full); !ok || got != "a/b/20 - cut.opus" {
		t.Fatalf("truncated filename did not match its title: %q ok=%v", got, ok)
	}
}

// When several truncated stems match, the longest is the better evidence.
func TestLookupPrefersLongestPrefix(t *testing.T) {
	short, long := "Ganesh Aarti Song", "Ganesh Aarti Song Sadhana Sarg"
	ix := libIndex{byKey: map[string]string{
		fold(short): "short.opus",
		fold(long):  "long.opus",
	}}
	ix.keys = []string{fold(short), fold(long)}
	if got, _ := ix.lookup(long + "am Full Version"); got != "long.opus" {
		t.Fatalf("got %q, want the longer stem", got)
	}
}

// A short prefix is not evidence of anything.
func TestLookupIgnoresShortPrefix(t *testing.T) {
	ix := libIndex{byKey: map[string]string{fold("Live"): "x.opus"}}
	ix.keys = []string{fold("Live")}
	if _, ok := ix.lookup("Live At The Very Long Concert Recording"); ok {
		t.Fatal("short prefix should not match")
	}
}

func TestLookupPrefersExactMatch(t *testing.T) {
	ix := libIndex{byKey: map[string]string{
		fold("Tauba Tauba"): "exact.opus",
		fold("Tauba Taub"):  "prefix.opus",
	}}
	ix.keys = []string{fold("Tauba Tauba"), fold("Tauba Taub")}
	if got, _ := ix.lookup("Tauba Tauba"); got != "exact.opus" {
		t.Fatalf("got %q", got)
	}
}
