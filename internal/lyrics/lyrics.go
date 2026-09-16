// Package lyrics fetches time synced lyrics and says which line belongs to a
// moment in a track.
//
// Lyrics come from lrclib.net, which is free, needs no account, and is what
// most open source players use. Nothing is generated here: the service is
// asked for a recording, and what it returns is shown.
package lyrics

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// Line is one lyric line and when it starts.
type Line struct {
	At   time.Duration
	Text string
}

// Lyrics is a whole set of them.
type Lyrics struct {
	Lines  []Line
	Synced bool
	// Title and Artist are what the service matched, which is not always what
	// was asked for and is worth showing so a wrong match is obvious.
	Title  string
	Artist string
}

// timestamp matches the "[mm:ss.xx]" that opens each line of an LRC file. The
// fraction is optional and may be two or three digits.
var timestamp = regexp.MustCompile(`\[(\d+):(\d+)(?:[.:](\d{1,3}))?\]`)

// Parse reads LRC text into lines, in time order.
//
// A line may carry several timestamps when the same words repeat, so each one
// becomes its own entry. Lines without a timestamp are metadata, or a plain
// lyric sheet with no timing, and are dropped: a line with no time cannot be
// shown at the right moment, and showing it at the wrong one is worse than
// leaving it out.
func Parse(lrc string) []Line {
	var out []Line
	for _, raw := range strings.Split(lrc, "\n") {
		stamps := timestamp.FindAllStringSubmatch(raw, -1)
		if len(stamps) == 0 {
			continue
		}
		text := strings.TrimSpace(timestamp.ReplaceAllString(raw, ""))
		for _, s := range stamps {
			min, _ := strconv.Atoi(s[1])
			sec, _ := strconv.Atoi(s[2])
			d := time.Duration(min)*time.Minute + time.Duration(sec)*time.Second
			if s[3] != "" {
				frac := s[3]
				// Two digits are hundredths, three are thousandths.
				n, _ := strconv.Atoi(frac)
				switch len(frac) {
				case 1:
					d += time.Duration(n) * 100 * time.Millisecond
				case 2:
					d += time.Duration(n) * 10 * time.Millisecond
				default:
					d += time.Duration(n) * time.Millisecond
				}
			}
			out = append(out, Line{At: d, Text: text})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// At returns the index of the line playing at pos, or -1 before the first.
func (l *Lyrics) At(pos time.Duration) int {
	if l == nil || len(l.Lines) == 0 {
		return -1
	}
	// The line that is showing is the last one whose time has passed.
	i := sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].At > pos })
	return i - 1
}

// candidate is one search result from the service.
type candidate struct {
	ID           int     `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	SyncedLyrics string  `json:"syncedLyrics"`
	PlainLyrics  string  `json:"plainLyrics"`
}

var client = &http.Client{Timeout: 15 * time.Second}

// Fetch looks up lyrics for a track, preferring a cached copy.
//
// The search endpoint is used rather than the exact one. Exact lookup needs
// the artist to match what the service holds, and these files carry whatever
// the uploader wrote in that field, often a comma separated list of everyone
// involved. Searching by title and scoring the results tolerates that.
func Fetch(ctx context.Context, artist, title string, dur time.Duration) (*Lyrics, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("no title to search for")
	}
	if l, err := readCache(artist, title); err == nil {
		return l, nil
	}

	q := url.Values{}
	q.Set("q", title)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://lrclib.net/api/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "crate (https://github.com/Gaurav-Gosain/crate)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lyrics search: http %d", resp.StatusCode)
	}

	var cands []candidate
	if err := json.NewDecoder(resp.Body).Decode(&cands); err != nil {
		return nil, err
	}
	best := pick(cands, artist, title, dur)
	if best == nil {
		return nil, fmt.Errorf("no lyrics found for %q", title)
	}

	l := &Lyrics{
		Title:  best.TrackName,
		Artist: best.ArtistName,
		Synced: best.SyncedLyrics != "",
		Lines:  Parse(best.SyncedLyrics),
	}
	if len(l.Lines) == 0 {
		return nil, fmt.Errorf("no synced lyrics for %q", title)
	}
	writeCache(artist, title, best.SyncedLyrics, l)
	return l, nil
}

// pick chooses the best candidate.
//
// Synced lyrics win outright: a plain sheet cannot be followed along with, and
// showing a wrong line is worse than showing none. After that, a duration
// close to the track being played is the strongest signal that this is the
// same recording rather than a remix or a cover, and the artist name breaks
// what ties remain.
func pick(cands []candidate, artist, title string, dur time.Duration) *candidate {
	type scored struct {
		c *candidate
		s float64
	}
	var all []scored
	wantArtist := fold(artist)
	wantTitle := fold(title)

	for i := range cands {
		c := &cands[i]
		if c.SyncedLyrics == "" {
			continue
		}
		s := 0.0
		if dur > 0 && c.Duration > 0 {
			off := c.Duration - dur.Seconds()
			if off < 0 {
				off = -off
			}
			switch {
			case off <= 2:
				s += 40
			case off <= 5:
				s += 25
			case off <= 15:
				s += 8
			default:
				s -= 10
			}
		}
		ca := fold(c.ArtistName)
		switch {
		case wantArtist != "" && strings.Contains(wantArtist, ca):
			s += 25
		case wantArtist != "" && strings.Contains(ca, wantArtist):
			s += 20
		}
		ct := fold(c.TrackName)
		if ct == wantTitle {
			s += 20
		} else if strings.Contains(ct, wantTitle) || strings.Contains(wantTitle, ct) {
			s += 10
		}
		// A title carrying "remix" or "live" when the track does not is a
		// different recording, however well the rest matches.
		for _, w := range []string{"remix", "live", "instrumental", "karaoke", "cover"} {
			if strings.Contains(ct, w) != strings.Contains(wantTitle, w) {
				s -= 30
			}
		}
		all = append(all, scored{c, s})
	}
	if len(all) == 0 {
		return nil
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].s > all[j].s })
	return all[0].c
}

func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cachePath keys on artist and title, so a track fetched once is not fetched
// again on every play.
func cachePath(artist, title string) string {
	sum := sha1.Sum([]byte(fold(artist) + "\x00" + fold(title)))
	return filepath.Join(filepath.Dir(config.Path()), "lyrics", hex.EncodeToString(sum[:])+".lrc")
}

func readCache(artist, title string) (*Lyrics, error) {
	b, err := os.ReadFile(cachePath(artist, title))
	if err != nil {
		return nil, err
	}
	lines := Parse(string(b))
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty cache")
	}
	return &Lyrics{Lines: lines, Synced: true}, nil
}

func writeCache(artist, title, lrc string, l *Lyrics) {
	p := cachePath(artist, title)
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	os.WriteFile(p, []byte(lrc), 0o644)
}
