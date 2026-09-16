package lyrics

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Captions read the words back off the video the track came from.
//
// This is a better source than a lyrics database in one important way and a
// worse one in another. Better, because captions are timed against the exact
// upload the audio was taken from, so they cannot drift: a database catalogues
// a release, and these files are uploads of it, often a different edit.
// Worse, because they are a machine's transcription of singing, so the words
// are less reliable than a curated set. Which is why the database is tried
// first and this is the fallback.

// cueTime matches a WebVTT timestamp.
var cueTime = regexp.MustCompile(`(\d\d):(\d\d):(\d\d)\.(\d\d\d)\s*-->`)

// tagged matches the inline timing spans YouTube puts inside a cue.
var tagged = regexp.MustCompile(`<[^>]*>`)

// ParseVTT turns WebVTT captions into timed lines.
//
// YouTube writes automatic captions as a rolling window: each cue repeats the
// tail of the one before it with a few new words appended, so the same phrase
// appears in several cues running. Emitting every line produces a stuttering
// transcript where each line is shown three or four times. Only text not
// already seen is kept, which recovers the transcript as written.
func ParseVTT(vtt string) []Line {
	var out []Line
	seen := map[string]bool{}

	blocks := strings.Split(strings.ReplaceAll(vtt, "\r\n", "\n"), "\n\n")
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 2 {
			continue
		}
		var at time.Duration
		var body []string
		found := false
		for _, l := range lines {
			if m := cueTime.FindStringSubmatch(l); m != nil {
				h, _ := strconv.Atoi(m[1])
				mn, _ := strconv.Atoi(m[2])
				sec, _ := strconv.Atoi(m[3])
				ms, _ := strconv.Atoi(m[4])
				at = time.Duration(h)*time.Hour + time.Duration(mn)*time.Minute +
					time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
				found = true
				continue
			}
			if !found {
				continue // cue identifier or header
			}
			body = append(body, l)
		}
		if !found {
			continue
		}
		for _, raw := range body {
			text := strings.TrimSpace(tagged.ReplaceAllString(raw, ""))
			text = strings.Join(strings.Fields(text), " ")
			if text == "" {
				continue
			}
			key := fold(text)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Line{At: at, Text: text})
		}
	}
	return out
}

// FromVideo fetches the captions for a video and returns them as timed lines.
//
// The original language is asked for ahead of the translations. YouTube offers
// an automatic translation into every language it knows, and taking English
// for a Punjabi song gets a machine translation of a machine transcription,
// which is two guesses deep.
func FromVideo(ctx context.Context, videoID string) (*Lyrics, error) {
	if videoID == "" {
		return nil, fmt.Errorf("no video id")
	}
	dir, err := os.MkdirTemp("", "crate-subs-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--skip-download",
		"--write-subs",
		"--write-auto-subs",
		// Original language first, then the widely written ones. yt-dlp takes
		// the first of these that exists.
		"--sub-langs", "*-orig,pa,hi,ur,en",
		"--sub-format", "vtt",
		"--no-warnings",
		"-o", filepath.Join(dir, "%(id)s.%(ext)s"),
		"https://www.youtube.com/watch?v="+videoID,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("captions: %s", strings.TrimSpace(string(out)))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	// Prefer an original-language track over a translation of one.
	var best string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".vtt") {
			continue
		}
		if strings.Contains(n, "-orig.") {
			best = n
			break
		}
		if best == "" {
			best = n
		}
	}
	if best == "" {
		return nil, fmt.Errorf("no captions for %s", videoID)
	}

	b, err := os.ReadFile(filepath.Join(dir, best))
	if err != nil {
		return nil, err
	}
	lines := ParseVTT(string(b))
	if len(lines) == 0 {
		return nil, fmt.Errorf("captions for %s had no usable text", videoID)
	}
	return &Lyrics{Lines: lines, Synced: true, Title: videoID, Artist: "captions"}, nil
}

// VideoID reads the id crate recorded when it downloaded a track.
//
// Without it a track is only a title, which is what made matching guesswork in
// the first place. With it the captions for the exact upload can be fetched,
// and those cannot be timed against a different edit because there is no other
// edit involved.
func VideoID(ctx context.Context, src string) string {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format_tags=youtube_id:stream_tags=youtube_id",
		"-of", "default=noprint_wrappers=1:nokey=1",
		src,
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
