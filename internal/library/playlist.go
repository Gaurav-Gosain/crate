package library

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// audioExt are the extensions worth listing in a playlist.
var audioExt = map[string]bool{
	".opus": true, ".mp3": true, ".m4a": true, ".flac": true, ".ogg": true, ".aac": true,
}

// trackNumRe matches the "04 - " that the output template puts in front of a
// track name, so a filename can be compared against the title it came from.
var trackNumRe = regexp.MustCompile(`^\d+\s*[-.]?\s*`)

// fold reduces a title to its letters and digits, lowercased. Comparing folded
// strings sidesteps yt-dlp's filename sanitising, which rewrites the
// characters a filesystem will not take (":" becomes "：", "/" becomes "⧸")
// and would otherwise make a file look nothing like its own title.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// safeName turns a playlist title into something that can be a filename.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		s = strings.TrimSpace(s[:120])
	}
	return s
}

// libIndex maps folded track titles to paths relative to the music root.
type libIndex struct {
	byKey map[string]string
	keys  []string
}

// lookup finds the file for a title.
//
// An exact match is tried first. Failing that the title is matched by prefix,
// because yt-dlp truncates long filenames and the stem left on disk is then a
// prefix of the title it came from: "Ganesh Gayatri Mantra | Sadhana Sar" is
// all that survives of a much longer name. A prefix has to be long enough to
// be meaningful and has to identify exactly one file, so that two different
// tracks sharing an opening cannot be confused for each other.
func (ix libIndex) lookup(title string) (string, bool) {
	key := fold(title)
	if key == "" {
		return "", false
	}
	if p, ok := ix.byKey[key]; ok {
		return p, true
	}
	const minPrefix = 12
	var (
		best  string
		bestN int
	)
	for _, k := range ix.keys {
		if len(k) < minPrefix || len(k) <= bestN || !strings.HasPrefix(key, k) {
			continue
		}
		best, bestN = ix.byKey[k], len(k)
	}
	return best, best != ""
}

// index maps a folded track title to its path relative to the music root.
//
// The remote is indexed rather than the local library because the remote is
// the authoritative copy: it is complete even when crate runs with no local
// copy kept, and it is what the music server will actually resolve the
// playlist entries against.
func index(ctx context.Context, c *config.Config) (libIndex, error) {
	root := strings.TrimSuffix(c.Remote.Path, "/")
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", c.Remote.Host,
		fmt.Sprintf("find %q -type f", root))
	out, err := cmd.Output()
	if err != nil {
		return libIndex{}, fmt.Errorf("index remote: %w", err)
	}

	idx := libIndex{byKey: make(map[string]string)}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(line, root), "/")
		if rel == "" || !audioExt[strings.ToLower(filepath.Ext(rel))] {
			continue
		}
		// Interrupted downloads leave ".temp" files behind. They are real
		// audio but half of one, so they must not reach a playlist.
		stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		if strings.HasSuffix(stem, ".temp") {
			continue
		}
		key := fold(trackNumRe.ReplaceAllString(stem, ""))
		if key == "" {
			continue
		}
		// First writer wins, so a track that exists in several folders
		// resolves to a stable choice rather than whichever find emitted last.
		if _, seen := idx.byKey[key]; !seen {
			idx.byKey[key] = rel
			idx.keys = append(idx.keys, key)
		}
	}
	return idx, nil
}

// entry is one track as the source lists it.
type entry struct {
	id       string
	title    string
	playlist string
	// nested marks an entry that is itself a playlist rather than a track.
	nested bool
}

// listing runs one flat listing. Flat means yt-dlp reports what a page
// contains without opening any of it, which is fast but only goes one level.
func listing(ctx context.Context, url string) ([]entry, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--flat-playlist",
		"--ignore-errors",
		"--no-warnings",
		"--print", "%(ie_key)s\t%(id)s\t%(playlist_title|)s\t%(track,title|)s",
		url,
	)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("resolve %s: %w", url, err)
	}

	var entries []entry
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			continue
		}
		e := entry{id: parts[1], playlist: parts[2], title: parts[3]}
		// YoutubeTab is the extractor for anything that is a page of other
		// things: a playlist, an album, a channel tab.
		e.nested = parts[0] == "YoutubeTab"
		if !e.nested && strings.TrimSpace(e.title) == "" {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// resolve lists a source's tracks without downloading anything.
//
// One flat listing is not always enough. An artist's releases tab is a list of
// albums, not of songs, so a single pass returns 146 album names for an artist
// with 146 records and none of them is a track. Anything that is itself a
// playlist gets opened once more, which turns the albums into their tracks.
func resolve(ctx context.Context, s config.Source) ([]entry, error) {
	top, err := listing(ctx, config.NormalizeURL(s.URL))
	if err != nil {
		return nil, err
	}

	// The name of the source as a whole, which the tracks of an expanded
	// album must keep: the playlist is "Karan Aujla - Releases", not one
	// entry per record.
	outer := ""
	for _, e := range top {
		if e.playlist != "" {
			outer = e.playlist
			break
		}
	}

	var (
		flat   []entry
		albums []entry
	)
	for _, e := range top {
		if e.nested {
			albums = append(albums, e)
			continue
		}
		flat = append(flat, e)
	}
	if len(albums) == 0 {
		return flat, nil
	}

	// Each album is an independent network round trip, so they run together.
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 12)
	)
	for _, a := range albums {
		wg.Add(1)
		go func(a entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			inner, err := listing(ctx, "https://www.youtube.com/playlist?list="+a.id)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, e := range inner {
				if e.nested {
					continue
				}
				if outer != "" {
					e.playlist = outer
				}
				flat = append(flat, e)
			}
		}(a)
	}
	wg.Wait()
	return flat, nil
}

// Playlist is the outcome of building one source's playlist.
type Playlist struct {
	Name    string
	File    string
	Matched int
	Total   int
}

// Playlists writes one .m3u per configured source into the library root, so
// the music server imports each source as a playlist.
//
// Sources and albums are different groupings and the tags cannot express
// both. A mix or an artist channel spans many albums, so a music server that
// files tracks by album tag scatters one source across dozens of entries.
// Rewriting the album tag to the source name would fix the scattering and
// wreck album browsing in exchange. An .m3u adds the missing grouping beside
// the tags instead of on top of them, so both views stay right.
func Playlists(ctx context.Context, c *config.Config, ev chan<- Event) ([]Playlist, error) {
	if c.Remote.Host == "" || c.Remote.Path == "" {
		return nil, fmt.Errorf("no remote configured: set remote.host and remote.path in %s", config.Path())
	}

	idx, err := index(ctx, c)
	if err != nil {
		return nil, err
	}
	send(ev, Event{Source: "playlists", Text: fmt.Sprintf("indexed %d tracks", len(idx.byKey)), Pct: -1, Phase: PhasePlaylisting})

	// Sources are resolved concurrently: each is a network round trip that
	// spends almost all its time waiting.
	var (
		mu   sync.Mutex
		out  []Playlist
		wg   sync.WaitGroup
		sem  = make(chan struct{}, max(1, c.Parallel))
		errs []string
	)
	for _, s := range c.Sources {
		wg.Add(1)
		go func(s config.Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			entries, err := resolve(ctx, s)
			if err != nil {
				mu.Lock()
				errs = append(errs, err.Error())
				mu.Unlock()
				return
			}
			p, err := writePlaylist(c, s, entries, idx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err.Error())
				return
			}
			if p != nil {
				out = append(out, *p)
				send(ev, Event{
					Source: "playlists",
					Text:   fmt.Sprintf("%s: %d/%d tracks", p.Name, p.Matched, p.Total),
					Pct:    -1, Phase: PhasePlaylisting,
				})
			}
		}(s)
	}
	wg.Wait()

	slices.SortFunc(out, func(a, b Playlist) int { return cmp.Compare(a.Name, b.Name) })
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	// Partial failures still matter: a source that stopped resolving keeps
	// its stale playlist and nothing else would ever say so.
	for _, e := range errs {
		send(ev, Event{Source: "playlists", Text: "WARNING: " + e, Pct: -1, Phase: PhasePlaylisting})
	}
	return out, nil
}

// writePlaylist matches a source's tracks against the library and writes the
// .m3u. It returns nil when nothing matched, because an empty playlist is
// worse than no playlist: it looks like the music went missing.
func writePlaylist(c *config.Config, s config.Source, entries []entry, idx libIndex) (*Playlist, error) {
	name := s.Name
	for _, e := range entries {
		if e.playlist != "" {
			name = e.playlist
			break
		}
	}
	name = safeName(name)
	if name == "" {
		return nil, nil
	}

	var (
		lines []string
		seen  = make(map[string]bool)
	)
	for _, e := range entries {
		rel, ok := idx.lookup(e.title)
		if !ok || seen[rel] {
			continue
		}
		seen[rel] = true
		lines = append(lines, rel)
	}
	if len(lines) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(c.Library, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(c.Library, name+".m3u")
	body := "#EXTM3U\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		return nil, err
	}
	return &Playlist{Name: name, File: file, Matched: len(lines), Total: len(entries)}, nil
}

// send delivers an event unless nobody is listening.
func send(ev chan<- Event, e Event) {
	if ev == nil {
		return
	}
	ev <- e
}
