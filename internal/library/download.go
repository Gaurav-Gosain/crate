// Package library downloads audio and mirrors it to a remote music server.
package library

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// Phase is what a source is currently doing, so the interface can say
// something more useful than "working".
type Phase int

const (
	PhaseIdle Phase = iota
	PhaseFetching
	PhaseConverting
	PhaseTagging
	PhaseMirroring
	PhasePlaylisting
)

func (p Phase) String() string {
	switch p {
	case PhaseFetching:
		return "fetching"
	case PhaseConverting:
		return "converting"
	case PhaseTagging:
		return "tagging"
	case PhaseMirroring:
		return "mirroring"
	case PhasePlaylisting:
		return "playlists"
	}
	return "idle"
}

// Event is one parsed line of progress from a running step. Text is kept for
// the log; the other fields are what the interface draws.
type Event struct {
	Source string
	Text   string
	Phase  Phase
	// Pct is -1 when the step reports no percentage.
	Pct float64
	// Speed and ETA are as yt-dlp reports them, already formatted.
	Speed string
	ETA   string
	// File is the track being worked on, without its directory.
	File string
	// Finished marks a track that just completed, so callers can count.
	Finished bool
}

// yt-dlp writes progress as:
//
//	[download]  42.0% of    3.56MiB at    1.01MiB/s ETA 00:03
//
// Pulling the pieces out lets the interface show what is happening rather
// than a bare percentage, and keeps yt-dlp's carriage-return redraws out of
// the log.
var (
	pctRe   = regexp.MustCompile(`(\d{1,3}\.\d)%`)
	speedRe = regexp.MustCompile(`(?:at\s+)?([0-9.]+\s*[KMG]?i?B/s)`)
	etaRe   = regexp.MustCompile(`ETA\s+([0-9:]+)`)
	destRe  = regexp.MustCompile(`^\[(?:download|ExtractAudio)\]\s+Destination:\s+(.*)$`)
)

// parseLine turns one line of yt-dlp output into an Event.
func parseLine(source, line string) Event {
	e := Event{Source: source, Text: line, Pct: -1}

	switch {
	case strings.HasPrefix(line, "[ExtractAudio]"):
		e.Phase = PhaseConverting
	case strings.HasPrefix(line, "[Metadata]"), strings.HasPrefix(line, "[EmbedThumbnail]"):
		e.Phase = PhaseTagging
	case strings.HasPrefix(line, "[download]"):
		e.Phase = PhaseFetching
	}

	if m := destRe.FindStringSubmatch(line); m != nil {
		e.File = filepath.Base(strings.TrimSpace(m[1]))
		// A conversion destination means the download for that track is done.
		if strings.HasPrefix(line, "[ExtractAudio]") {
			e.Finished = true
		}
	}
	if m := pctRe.FindStringSubmatch(line); m != nil {
		fmt.Sscanf(m[1], "%f", &e.Pct)
	}
	if m := speedRe.FindStringSubmatch(line); m != nil {
		e.Speed = strings.TrimSpace(m[1])
	}
	if m := etaRe.FindStringSubmatch(line); m != nil {
		e.ETA = m[1]
	}
	return e
}

// outputTemplate lays files out the way music servers expect:
// Artist/Album/Track. Fields fall back through yt-dlp's alternates so a
// bare video with no album metadata still files somewhere sensible.
const outputTemplate = `%(artist,album_artist,creator,uploader,channel|Unknown Artist)s/` +
	`%(album,playlist_title,playlist|Singles)s/` +
	`%(track_number,playlist_index|)s%(track_number,playlist_index& - |)s%(track,title)s.%(ext)s`

// Download fetches anything new from one source.
//
// The download archive makes this incremental: tracks already recorded there
// are skipped without touching the network, so running a sync repeatedly is
// cheap and safe.
//
// Work is split across c.Parallel yt-dlp processes using strided playlist
// selection, so worker k of n takes items k+1, k+1+n, k+1+2n and so on. This
// keeps each process inside the playlist, which matters because album and
// track-number metadata come from that context and would be lost if items
// were fetched as standalone URLs.
func Download(ctx context.Context, c *config.Config, s config.Source, ev chan<- Event) error {
	dest := c.Library
	if s.Dir != "" {
		dest = filepath.Join(c.Library, s.Dir)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	n := c.Parallel
	if n < 1 {
		n = 1
	}

	groups := plan(ctx, s, n)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, g := range groups {
		wg.Add(1)
		go func(g work) {
			defer wg.Done()
			if err := downloadShard(ctx, c, s, dest, g.shard, g.urls, ev); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()

	// yt-dlp exits non-zero when a shard selects no items, which is normal
	// for the trailing workers on a short playlist. Only report a failure if
	// every shard failed.
	if len(errs) == len(groups) && len(groups) > 0 {
		return errs[0]
	}
	return nil
}

// work is one yt-dlp invocation: the urls to fetch and, for a flat playlist,
// the slice of it this worker is responsible for.
type work struct {
	urls  []string
	shard string
}

// plan divides a source between n workers.
//
// --playlist-items applies to every playlist yt-dlp opens, nested ones
// included. An artist's releases tab is a list of albums, so striding over it
// takes every nth album and then every nth track inside each of those albums:
// most of the music is never fetched, and because the arithmetic is the same
// every time, running the sync again selects the same fraction and the gaps
// never fill. Sources like that are split by album instead, one disjoint set
// of whole albums per worker, with no item selection in play.
//
// A flat playlist has no nesting to leak into, so it keeps the stride, which
// also keeps each process inside the playlist: album and track-number
// metadata come from that context and would be lost if items were fetched as
// standalone URLs.
func plan(ctx context.Context, s config.Source, n int) []work {
	url := config.NormalizeURL(s.URL)

	var albums []string
	if top, err := listing(ctx, url); err == nil {
		for _, e := range top {
			if e.nested {
				albums = append(albums, "https://www.youtube.com/playlist?list="+e.id)
			}
		}
	}

	if len(albums) == 0 {
		var out []work
		for k := 0; k < n; k++ {
			shard := ""
			if n > 1 {
				shard = fmt.Sprintf("%d::%d", k+1, n)
			}
			out = append(out, work{urls: []string{url}, shard: shard})
		}
		return out
	}

	return shardAlbums(albums, n)
}

// shardAlbums deals whole albums out between n workers. Every album must land
// in exactly one worker's hand: one missed album is an album of music that
// never downloads.
func shardAlbums(albums []string, n int) []work {
	var out []work
	for k := 0; k < n; k++ {
		var g []string
		for i := k; i < len(albums); i += n {
			g = append(g, albums[i])
		}
		if len(g) > 0 {
			out = append(out, work{urls: g})
		}
	}
	return out
}

func downloadShard(ctx context.Context, c *config.Config, s config.Source, dest, shard string, urls []string, ev chan<- Event) error {
	args := []string{
		"--no-colors",
		"--newline",
		"--ignore-errors",
		"--no-overwrites",
		// Shared across the parallel workers. yt-dlp appends one short line
		// per download with O_APPEND, which is atomic below PIPE_BUF, so the
		// concurrent writes do not interleave.
		"--download-archive", c.ArchivePath(),
		"--extract-audio",
		"--audio-format", c.Format,
		"--audio-quality", c.Quality,
		"--embed-metadata",
		"--embed-thumbnail",
		// The metadata pass runs "ffmpeg -map 0 -c copy", which tries to copy
		// every stream including the cover art that --embed-thumbnail added.
		// An opus file cannot carry a PNG stream through the ogg muxer, so
		// ffmpeg refuses with "Unsupported codec id in stream 1" and yt-dlp
		// reports "Conversion failed!". That only bites on a second pass over
		// a file that already has a cover, and it is self sustaining: the
		// failure stops the download being recorded in the archive, so the
		// next run tries the same file and fails the same way forever.
		// Dropping video for the metadata write sidesteps it; the thumbnail
		// is re-attached afterwards by the embed step, which uses mutagen.
		"--postprocessor-args", "Metadata:-vn",
		// Split each file into parallel fragment downloads as well, which
		// helps when a shard has only one long track.
		"--concurrent-fragments", "4",
		"--parse-metadata", "%(title)s:%(?P<artist>.+?) - (?P<track>.+)",
		"--parse-metadata", "%(playlist_title,album|)s:%(album)s",
		// Album playlists often carry no track numbers, which leaves a
		// music server sorting the record alphabetically. The position in
		// the playlist is the track order, so fall back to it.
		"--parse-metadata", "%(track_number,playlist_index|)s:%(track_number)s",
		// Some hosts put an uploader email where the artist belongs, which
		// produced folders like "alan@smithee.com".
		"--replace-in-metadata", "artist,album_artist,uploader,channel",
		`^\S+@\S+\.\S+$`, "Unknown Artist",
		// Placeholders are not artists. yt-dlp hands back a literal "NA" when
		// a field is absent, which then becomes a folder and an entry in the
		// music server's artist list.
		"--replace-in-metadata", "artist,album_artist",
		`(?i)^\s*(na|n/a|none|null|unknown|various artists?)\s*$`, "Unknown Artist",
		// Same placeholder, but an empty album is better than an album
		// literally called "NA" showing up in the music server.
		"--replace-in-metadata", "album,track",
		`(?i)^\s*(na|n/a|none|null)\s*$`, "",
		// Drop the video description, synopsis, comment and url. They carry
		// the entire youtube blurb, which bloats every file and buries the
		// fields a music server actually reads.
		"--parse-metadata", ":(?P<meta_description>)",
		"--parse-metadata", ":(?P<meta_synopsis>)",
		"--parse-metadata", ":(?P<meta_comment>)",
		"--parse-metadata", ":(?P<meta_purl>)",
		// Album artist drives grouping. Take the first credited name rather
		// than the whole comma joined list, so a record lands under one
		// artist instead of inventing one per combination of collaborators.
		"--parse-metadata", "%(artist,album_artist,creator|)s:(?P<meta_album_artist>[^,;/]+)",
		"--paths", dest,
		"--output", outputTemplate,
		"--trim-filenames", "180",
	}
	if shard != "" {
		args = append(args, "--playlist-items", shard)
	}
	args = append(args, urls...)

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("yt-dlp: %w", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		ev <- parseLine(s.Name, line)
	}
	return cmd.Wait()
}
