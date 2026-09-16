package lyrics

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// A lyrics service catalogues a release. These files are YouTube uploads of
// it, and the two are often not the same edit: an upload may open with a
// label sting or a few seconds of silence, or be a slightly different master.
// The words are then right and their timing is not, by a constant amount.
//
// Rather than guess at a correction from the audio, which is unreliable, the
// offset is something you set once by ear and it is remembered for that
// track. Measured across a sample of this library, only about two thirds of
// matches were within a second of the file's own length, so this is the
// common case rather than an edge one.

// offsetPath is the nudge stored beside the cached words.
func offsetPath(artist, title string) string {
	return strings.TrimSuffix(cachePath(artist, title), ".lrc") + ".offset"
}

// LoadOffset reads the saved nudge for a track, zero if there is none.
func LoadOffset(artist, title string) time.Duration {
	b, err := os.ReadFile(offsetPath(artist, title))
	if err != nil {
		return 0
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// SaveOffset remembers the nudge. A zero offset removes the file rather than
// writing one, so a track that needed no correction leaves nothing behind.
func SaveOffset(artist, title string, d time.Duration) error {
	p := offsetPath(artist, title)
	if d == 0 {
		os.Remove(p)
		return nil
	}
	return os.WriteFile(p, []byte(strconv.FormatInt(d.Milliseconds(), 10)), 0o644)
}
