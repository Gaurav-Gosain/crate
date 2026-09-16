package library

import "testing"

// The archive-hit line appears mid-string, not at the start. The headless
// log filter used to test it as a prefix, so those lines never showed and a
// cron sync gave no hint that everything was already fetched.
func TestInterestingMatchesArchiveHits(t *testing.T) {
	line := "[download] Some Track Title has already been recorded in the archive"
	if !Interesting(line) {
		t.Fatal("archive-hit lines must reach the log")
	}
}

func TestInterestingFiltersNoise(t *testing.T) {
	for _, line := range []string{
		"[youtube] abc123: Downloading webpage",
		"[download]  42.0% of 3.56MiB at 1.01MiB/s ETA 00:03",
	} {
		if Interesting(line) {
			t.Fatalf("noise line passed the filter: %q", line)
		}
	}
}

func TestInterestingKeepsErrorsAndDestinations(t *testing.T) {
	for _, line := range []string{
		"ERROR: [youtube] abc: Video unavailable",
		"WARNING: something odd",
		"[download] Destination: a/b/01 - x.opus",
		"[ExtractAudio] Destination: a/b/01 - x.opus",
	} {
		if !Interesting(line) {
			t.Fatalf("wanted %q in the log", line)
		}
	}
}

// A single video has nothing nested and nothing to shard, so planning it
// must not spend a flat-listing round trip before the download starts.
func TestSingleVideo(t *testing.T) {
	cases := map[string]bool{
		"https://www.youtube.com/watch?v=abc123":          true,
		"https://music.youtube.com/watch?v=abc123":        true,
		"https://youtu.be/abc123":                         true,
		"https://www.youtube.com/watch?v=abc123&list=PLx": false,
		"https://www.youtube.com/playlist?list=PLx":       false,
		"https://www.youtube.com/@artist/releases":        false,
	}
	for url, want := range cases {
		if got := singleVideo(url); got != want {
			t.Errorf("singleVideo(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestParseLineReadsProgress(t *testing.T) {
	e := parseLine("src", "[download]  42.0% of    3.56MiB at    1.01MiB/s ETA 00:03")
	if e.Phase != PhaseFetching || e.Pct != 42.0 || e.Speed != "1.01MiB/s" || e.ETA != "00:03" {
		t.Fatalf("got %+v", e)
	}
}

func TestParseLineMarksFinishedTracks(t *testing.T) {
	e := parseLine("src", "[ExtractAudio] Destination: Artist/Album/01 - Song.opus")
	if !e.Finished || e.File != "01 - Song.opus" || e.Phase != PhaseConverting {
		t.Fatalf("got %+v", e)
	}
}
