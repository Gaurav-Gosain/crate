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

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// Event is a single line of progress from a running step.
type Event struct {
	Source string
	Text   string
	// Pct is -1 when the step reports no percentage.
	Pct  float64
	Err  bool
	Done bool
}

// yt-dlp writes progress as "[download]  42.0% of ...". Parsing the percent
// lets the UI draw a bar without yt-dlp's carriage-return redraws leaking
// into the log.
var pctRe = regexp.MustCompile(`(\d{1,3}\.\d)%`)

// outputTemplate lays files out the way music servers expect:
// Artist/Album/Track. Fields fall back through yt-dlp's alternates so a
// bare video with no album metadata still files somewhere sensible.
const outputTemplate = `%(artist,album_artist,creator,uploader,channel|Unknown Artist)s/` +
	`%(album,playlist_title,playlist|Singles)s/` +
	`%(track_number|)s%(track_number& - |)s%(track,title)s.%(ext)s`

// Download fetches anything new from one source.
//
// The download archive makes this incremental: tracks already recorded there
// are skipped without touching the network, so running a sync repeatedly is
// cheap and safe.
func Download(ctx context.Context, c *config.Config, s config.Source, ev chan<- Event) error {
	dest := c.Library
	if s.Dir != "" {
		dest = filepath.Join(c.Library, s.Dir)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	args := []string{
		"--no-colors",
		"--newline",
		"--ignore-errors",
		"--no-overwrites",
		"--download-archive", c.ArchivePath(),
		"--extract-audio",
		"--audio-format", c.Format,
		"--audio-quality", c.Quality,
		"--embed-metadata",
		"--embed-thumbnail",
		// Videos carry a title but rarely clean artist/track tags. Splitting
		// "Artist - Track" gives the music server something to group on.
		"--parse-metadata", "%(title)s:%(?P<artist>.+?) - (?P<track>.+)",
		"--parse-metadata", "%(playlist_title,album)s:%(album)s",
		// Some hosts put an uploader email where the artist belongs, which
		// produced folders like "alan@smithee.com". Anything shaped like an
		// address is not an artist name.
		"--replace-in-metadata", "artist,album_artist,uploader,channel",
		`^\S+@\S+\.\S+$`, "Unknown Artist",
		"--paths", dest,
		"--output", outputTemplate,
		"--trim-filenames", "180",
		s.URL,
	}

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
		pct := -1.0
		if m := pctRe.FindStringSubmatch(line); m != nil {
			fmt.Sscanf(m[1], "%f", &pct)
		}
		ev <- Event{Source: s.Name, Text: line, Pct: pct}
	}
	return cmd.Wait()
}
