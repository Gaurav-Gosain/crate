package library

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/crate/internal/config"
	"github.com/Gaurav-Gosain/crate/internal/oggtag"
)

// EmbedLyrics writes timed words into the tracks themselves.
//
// Into the file rather than into one beside it, because a music server reads
// an external lyrics file only when told to and its own interface does not
// read one at all, so words kept alongside the audio are invisible on a phone
// and in a browser. Embedded, they travel with the music.
func EmbedLyrics(ctx context.Context, c *config.Config, words map[string]string, log func(string, ...any)) (int, error) {
	return embedTag(ctx, c, "LYRICS", words, log)
}

// EmbedIDs writes the video a track came from into the track.
//
// Without it a track is only a title, and a title is not enough to find the
// captions for one particular upload.
func EmbedIDs(ctx context.Context, c *config.Config, ids map[string]string, log func(string, ...any)) (int, error) {
	return embedTag(ctx, c, "youtube_id", ids, log)
}

// embedTag applies one tag to many tracks.
//
// Each file is fetched, edited here, and sent back. The tagging deliberately
// does not run on the server: doing it there meant a python tag editor
// installed alongside the music, and the point of this program is to be one
// binary and the tools a music library already needs. The cost is that a file
// crosses the wire twice to change a few hundred bytes in it, which is worth
// paying for a backfill that happens once. Tracks downloaded from now on are
// tagged before they are ever mirrored, so they never pay it at all.
func embedTag(ctx context.Context, c *config.Config, tag string, values map[string]string, log func(string, ...any)) (int, error) {
	if len(values) == 0 {
		return 0, nil
	}
	if c.Remote.Host == "" || c.Remote.Path == "" {
		return 0, fmt.Errorf("no remote configured: set remote.host and remote.path in %s", config.Path())
	}

	dir, err := os.MkdirTemp("", "crate-tag-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)

	root := strings.TrimSuffix(c.Remote.Path, "/")
	rsh := "ssh -o BatchMode=yes"

	var (
		mu   sync.Mutex
		done int
		wg   sync.WaitGroup
		// Four at a time: each one is a file over the network in both
		// directions, so more would only queue on the same link.
		sem = make(chan struct{}, 4)
	)
	for rel, value := range values {
		wg.Add(1)
		go func(rel, value string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}

			local := filepath.Join(dir, filepath.Base(rel))
			remote := c.Remote.Host + ":" + root + "/" + rel

			if out, err := exec.CommandContext(ctx, "rsync", "-t", "--timeout", "120",
				"-e", rsh, remote, local).CombinedOutput(); err != nil {
				if log != nil {
					log("could not fetch %s: %s", short(rel), strings.TrimSpace(string(out)))
				}
				return
			}
			defer os.Remove(local)

			if err := oggtag.Set(local, tag, value); err != nil {
				if log != nil {
					log("could not tag %s: %v", short(rel), err)
				}
				return
			}

			// --inplace so the server rewrites the file it already has rather
			// than building a copy beside it, which would need room for a
			// second library on a disk that does not have it.
			if out, err := exec.CommandContext(ctx, "rsync", "-t", "--inplace",
				"--no-perms", "--no-owner", "--no-group", "--timeout", "120",
				"-e", rsh, local, remote).CombinedOutput(); err != nil {
				if log != nil {
					log("could not send %s back: %s", short(rel), strings.TrimSpace(string(out)))
				}
				return
			}

			mu.Lock()
			done++
			n := done
			mu.Unlock()
			if log != nil && n%50 == 0 {
				log("tagged %d so far", n)
			}
		}(rel, value)
	}
	wg.Wait()
	return done, nil
}

func short(s string) string {
	r := []rune(s)
	if len(r) <= 48 {
		return string(r)
	}
	return string(r[:47]) + "…"
}
