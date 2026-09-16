package library

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// RunAll downloads every source, mirrors once, then asks the server to
// reindex. It is the same sequence the interface performs, factored out so it
// can also run unattended from cron.
//
// Mirroring once at the end rather than after each source means a single rsync
// pass and a single reindex, which matters when the link is slow.
func RunAll(ctx context.Context, c *config.Config, log func(string, ...any)) error {
	if len(c.Sources) == 0 {
		log("no sources configured")
		return nil
	}

	ev := make(chan Event, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range ev {
			if e.Pct < 0 && Interesting(e.Text) {
				log("%s", e.Text)
			}
		}
	}()

	// Sources run concurrently as well as being sharded internally. The
	// semaphore is shared by every shard of every source, so it is what
	// actually bounds the number of yt-dlp processes.
	sem := make(chan struct{}, max(c.Parallel, 1))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures int
	)
	for _, s := range c.Sources {
		wg.Add(1)
		go func(s config.Source) {
			defer wg.Done()
			log("== %s", s.Name)
			if err := Download(ctx, c, s, ev, sem); err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				log("   %s failed: %v", s.Name, err)
			}
		}(s)
	}
	wg.Wait()

	log("== mirroring to %s", c.Remote.Host)
	if err := Sync(ctx, c, ev); err != nil {
		log("   sync failed: %v", err)
		failures++
	} else {
		// Playlists are built after the mirror so the index they match
		// against includes whatever this run just downloaded, and synced
		// again afterwards because the .m3u files themselves are new.
		log("== building playlists")
		if pls, err := Playlists(ctx, c, ev); err != nil {
			log("   playlists failed: %v", err)
		} else if len(pls) > 0 {
			for _, p := range pls {
				log("   %s: %d/%d tracks", p.Name, p.Matched, p.Total)
			}
			if err := Sync(ctx, c, ev); err != nil {
				log("   playlist sync failed: %v", err)
			}
		}
		if err := TriggerScan(ctx, c); err != nil {
			log("   reindex failed: %v", err)
		}
		if n, err := ClearStaging(c); err != nil {
			log("   could not clear staging: %v", err)
		} else if n > 0 {
			log("   cleared %d staged item(s); the remote is the only copy", n)
		}
	}

	close(ev)
	<-done

	if failures > 0 {
		return fmt.Errorf("%d step(s) failed", failures)
	}
	return nil
}

// Interesting filters the tool firehose down to the lines a person would
// want in a log. It lives here rather than in the interface because both the
// interface and the headless sync read the same yt-dlp and rsync output.
//
// The archive-hit line is matched anywhere in the string: it reads
// "[download] <title> has already been recorded in the archive", so an
// earlier prefix match never fired and those lines silently vanished from
// the headless log.
func Interesting(s string) bool {
	switch {
	case strings.HasPrefix(s, "[download] Destination:"),
		strings.HasPrefix(s, "[ExtractAudio]"),
		strings.HasPrefix(s, "[Metadata]"),
		strings.Contains(s, "has already been recorded"),
		strings.HasPrefix(s, "ERROR"),
		strings.HasPrefix(s, "WARNING"):
		return true
	case strings.Contains(s, "sent ") && strings.Contains(s, "bytes"):
		// rsync's closing summary, which is the one line of a mirror worth
		// keeping.
		return true
	}
	return false
}
