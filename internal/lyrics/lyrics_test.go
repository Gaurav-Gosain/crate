package lyrics

import (
	"testing"
	"time"
)

func TestParseReadsTimestamps(t *testing.T) {
	lrc := "[00:12.50]one\n[01:05.00]two\n[02:00]three\n"
	got := Parse(lrc)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d", len(got))
	}
	want := []time.Duration{
		12*time.Second + 500*time.Millisecond,
		65 * time.Second,
		120 * time.Second,
	}
	for i, w := range want {
		if got[i].At != w {
			t.Fatalf("line %d at %v, want %v", i, got[i].At, w)
		}
	}
}

// A fraction of two digits is hundredths and three is thousandths. Reading
// them the same way puts every line out by up to nine tenths of a second,
// which is the difference between a lyric landing on the beat and after it.
func TestParseHandlesFractionWidths(t *testing.T) {
	two := Parse("[00:01.05]x")
	three := Parse("[00:01.050]x")
	if two[0].At != time.Second+50*time.Millisecond {
		t.Fatalf("two digit fraction read as %v", two[0].At)
	}
	if three[0].At != time.Second+50*time.Millisecond {
		t.Fatalf("three digit fraction read as %v", three[0].At)
	}
}

// One line can carry several timestamps when the words repeat.
func TestParseExpandsRepeatedTimestamps(t *testing.T) {
	got := Parse("[00:10.00][01:10.00]chorus")
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].At != 10*time.Second || got[1].At != 70*time.Second {
		t.Fatalf("got %v and %v", got[0].At, got[1].At)
	}
}

// Header lines and plain sheets have no timing. Showing them at an arbitrary
// moment is worse than not showing them.
func TestParseDropsUntimedLines(t *testing.T) {
	got := Parse("[ar:Someone]\n[ti:Something]\njust some text\n[00:05.00]real\n")
	if len(got) != 1 || got[0].At != 5*time.Second {
		t.Fatalf("got %d lines: %+v", len(got), got)
	}
}

func TestAtFindsTheCurrentLine(t *testing.T) {
	l := &Lyrics{Lines: Parse("[00:00.00]a\n[00:10.00]b\n[00:20.00]c\n")}
	cases := []struct {
		at   time.Duration
		want int
	}{
		{0, 0},
		{5 * time.Second, 0},
		{10 * time.Second, 1},
		{19 * time.Second, 1},
		{25 * time.Second, 2},
	}
	for _, c := range cases {
		if got := l.At(c.at); got != c.want {
			t.Fatalf("at %v got line %d, want %d", c.at, got, c.want)
		}
	}
}

// Before the first line there is nothing to show yet.
func TestAtBeforeTheFirstLine(t *testing.T) {
	l := &Lyrics{Lines: Parse("[00:30.00]late start")}
	if got := l.At(time.Second); got != -1 {
		t.Fatalf("got %d, want -1", got)
	}
}

func TestAtOnEmptyLyrics(t *testing.T) {
	var l *Lyrics
	if got := l.At(time.Second); got != -1 {
		t.Fatalf("got %d", got)
	}
}

// Duration is the strongest signal that a result is the same recording, and a
// remix must not be chosen for a track that is not one.
func TestPickPrefersMatchingDuration(t *testing.T) {
	cands := []candidate{
		{TrackName: "Song", ArtistName: "Someone", Duration: 400, SyncedLyrics: "[00:01.00]a"},
		{TrackName: "Song", ArtistName: "Someone", Duration: 181, SyncedLyrics: "[00:02.00]b"},
	}
	got := pick(cands, "Someone", "Song", 180*time.Second)
	if got == nil || got.Duration != 181 {
		t.Fatalf("picked %+v", got)
	}
}

func TestPickRejectsARemixForAPlainTrack(t *testing.T) {
	cands := []candidate{
		{TrackName: "Song - Remix", ArtistName: "Someone", Duration: 180, SyncedLyrics: "[00:01.00]a"},
		{TrackName: "Song", ArtistName: "Someone", Duration: 180, SyncedLyrics: "[00:02.00]b"},
	}
	got := pick(cands, "Someone", "Song", 180*time.Second)
	if got == nil || got.TrackName != "Song" {
		t.Fatalf("picked %q", got.TrackName)
	}
}

// A plain sheet cannot be followed along with, so it is never chosen.
func TestPickIgnoresUnsyncedResults(t *testing.T) {
	cands := []candidate{
		{TrackName: "Song", ArtistName: "Someone", Duration: 180, PlainLyrics: "words"},
	}
	if got := pick(cands, "Someone", "Song", 180*time.Second); got != nil {
		t.Fatal("an unsynced result must not be chosen")
	}
}

// These files list every collaborator in the artist field, so the service's
// single artist name is usually a substring of it.
func TestPickMatchesAnArtistBuriedInAList(t *testing.T) {
	cands := []candidate{
		{TrackName: "Song", ArtistName: "Nobody", Duration: 180, SyncedLyrics: "[00:01.00]a"},
		{TrackName: "Song", ArtistName: "Karan Aujla", Duration: 180, SyncedLyrics: "[00:02.00]b"},
	}
	got := pick(cands, "Avvy Sra, Karan Aujla, Jaani", "Song", 180*time.Second)
	if got == nil || got.ArtistName != "Karan Aujla" {
		t.Fatalf("picked %+v", got)
	}
}

// These titles are common, and a result that names a different artist and
// runs to a different length is some other performance. Showing its words in
// time with the music is worse than showing none, because it looks right.
func TestPickRejectsAnUnrelatedPerformance(t *testing.T) {
	cands := []candidate{
		{TrackName: "Heer", ArtistName: "Somebody Else", Duration: 300, SyncedLyrics: "[00:01.00]a"},
	}
	if got := pick(cands, "Diljit Dosanjh", "Heer", 254*time.Second); got != nil {
		t.Fatalf("picked an unrelated recording: %+v", got)
	}
}

// A different artist is acceptable when the length matches closely: these
// files credit whoever the uploader felt like crediting.
func TestPickAllowsADifferentArtistWhenTheLengthMatches(t *testing.T) {
	cands := []candidate{
		{TrackName: "Heer", ArtistName: "Somebody Else", Duration: 255, SyncedLyrics: "[00:01.00]a"},
	}
	if got := pick(cands, "Diljit Dosanjh", "Heer", 254*time.Second); got == nil {
		t.Fatal("a close length is evidence enough")
	}
}

// And the named artist is enough even when no length is known.
func TestPickAllowsTheNamedArtistWithoutADuration(t *testing.T) {
	cands := []candidate{
		{TrackName: "Song", ArtistName: "Karan Aujla", Duration: 0, SyncedLyrics: "[00:01.00]a"},
	}
	if got := pick(cands, "Avvy Sra, Karan Aujla", "Song", 0); got == nil {
		t.Fatal("the artist being named is evidence enough")
	}
}
