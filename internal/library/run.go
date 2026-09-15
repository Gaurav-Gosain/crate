package library

import (
	"context"
	"fmt"

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
			if e.Pct < 0 && interestingLine(e.Text) {
				log("%s", e.Text)
			}
		}
	}()

	var failures int
	for _, s := range c.Sources {
		log("== %s", s.Name)
		if err := Download(ctx, c, s, ev); err != nil {
			log("   failed: %v", err)
			failures++
		}
	}

	log("== mirroring to %s", c.Remote.Host)
	if err := Sync(ctx, c, ev); err != nil {
		log("   sync failed: %v", err)
		failures++
	} else if err := TriggerScan(ctx, c); err != nil {
		log("   reindex failed: %v", err)
	}

	close(ev)
	<-done

	if failures > 0 {
		return fmt.Errorf("%d step(s) failed", failures)
	}
	return nil
}

func interestingLine(s string) bool {
	for _, p := range []string{
		"[download] Destination:", "[ExtractAudio]", "ERROR", "WARNING",
		"has already been recorded",
	} {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}
