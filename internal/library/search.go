package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Kind distinguishes a single track from a collection, because they are added
// differently: a track is downloaded once, a collection becomes a source that
// is checked for new items on every sync.
type Kind int

const (
	Track Kind = iota
	Album
)

func (k Kind) String() string {
	if k == Album {
		return "album"
	}
	return "track"
}

type Result struct {
	Kind     Kind
	ID       string
	Title    string
	URL      string
	Artist   string
	Album    string
	Year     int
	Duration float64
}

// raw mirrors the subset of yt-dlp's json this needs.
type raw struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Track         string  `json:"track"`
	Artist        string  `json:"artist"`
	Album         string  `json:"album"`
	Channel       string  `json:"channel"`
	Uploader      string  `json:"uploader"`
	WebpageURL    string  `json:"webpage_url"`
	Duration      float64 `json:"duration"`
	ReleaseYear   int     `json:"release_year"`
	PlaylistID    string  `json:"playlist_id"`
	PlaylistTitle string  `json:"playlist_title"`
}

// Search queries YouTube Music and returns both individual tracks and the
// albums they belong to.
//
// YouTube Music is used rather than plain YouTube because its entries carry
// real artist, album and track tags. Plain YouTube returns video titles like
// "M I L E S D A V I S - Kind Of Blue - Full Album", which file badly.
//
// This deliberately does not pass --flat-playlist. Flat results come back as
// bare browse ids with no title or artist at all, so yt-dlp has to resolve
// each entry. That costs roughly two seconds per result, which is why callers
// should run this in the background and bound count.
func Search(ctx context.Context, query string, count int) ([]Result, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, fmt.Errorf("yt-dlp not found in PATH")
	}
	if count <= 0 {
		count = 12
	}

	target := strings.TrimSpace(query)
	if target == "" {
		return nil, fmt.Errorf("empty query")
	}
	// A pasted URL is inspected directly, so album and playlist links work
	// the same way as a text search.
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://music.youtube.com/search?q=" + urlEscape(target)
	}

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--dump-json",
		"--no-warnings",
		"--ignore-errors",
		"--playlist-end", fmt.Sprint(count),
		target,
	)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("yt-dlp: %s", firstLine(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}

	var (
		tracks []Result
		albums []Result
		seen   = map[string]bool{}
	)

	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var r raw
		if err := dec.Decode(&r); err != nil {
			continue
		}
		title := firstNonEmpty(r.Track, r.Title)
		if title == "" {
			continue
		}
		artist := firstNonEmpty(r.Artist, r.Channel, r.Uploader)

		url := r.WebpageURL
		if url == "" && r.ID != "" {
			url = "https://music.youtube.com/watch?v=" + r.ID
		}
		if url != "" && !seen[url] {
			seen[url] = true
			tracks = append(tracks, Result{
				Kind:     Track,
				ID:       r.ID,
				Title:    title,
				Artist:   artist,
				Album:    r.Album,
				Year:     r.ReleaseYear,
				Duration: r.Duration,
				URL:      url,
			})
		}

		// Every track names the album it came from, so the albums in a
		// result set fall out of the same query rather than needing another.
		if r.Album != "" && r.PlaylistID != "" {
			aurl := "https://music.youtube.com/playlist?list=" + r.PlaylistID
			if !seen[aurl] {
				seen[aurl] = true
				albums = append(albums, Result{
					Kind:   Album,
					ID:     r.PlaylistID,
					Title:  r.Album,
					Artist: artist,
					Album:  r.Album,
					Year:   r.ReleaseYear,
					URL:    aurl,
				})
			}
		}
	}

	if len(tracks) == 0 && len(albums) == 0 {
		return nil, fmt.Errorf("no results")
	}
	// Albums first: picking a whole album is usually the intent when the
	// query names one, and it is the more expensive thing to reconstruct
	// by hand from individual tracks.
	return append(albums, tracks...), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('+')
		default:
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String()
}
