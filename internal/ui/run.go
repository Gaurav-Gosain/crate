package ui

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/config"

	"github.com/Gaurav-Gosain/crate/internal/library"
)

func (a *App) setRow(i int, fn func(*row)) {
	a.mu.Lock()
	if i >= 0 && i < len(a.rows) {
		fn(&a.rows[i])
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *App) setBusy(v bool) {
	a.mu.Lock()
	a.busy = v
	a.mu.Unlock()
	a.redraw()
}

func (a *App) syncSelected() {
	a.mu.Lock()
	i := a.cursor
	n := len(a.rows)
	a.mu.Unlock()
	if n == 0 {
		return
	}
	a.run([]int{i})
}

func (a *App) syncAll() {
	a.mu.Lock()
	idx := make([]int, len(a.rows))
	for i := range a.rows {
		idx[i] = i
	}
	a.mu.Unlock()
	if len(idx) == 0 {
		a.logf("nothing to sync, add a source with a")
		return
	}
	a.run(idx)
}

// run downloads the given sources, then mirrors once at the end.
//
// Mirroring after all downloads rather than after each one means a single
// rsync pass and a single server rescan, which matters when the link to the
// server is slow.
func (a *App) run(idx []int) {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		a.logf("already working")
		return
	}
	a.busy = true
	a.started = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.setBusy(false)
	}()
	a.redraw()

	ev := make(chan library.Event, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range ev {
			// yt-dlp and rsync are chatty; only surface lines worth reading.
			if e.Pct >= 0 {
				continue
			}
			if interesting(e.Text) {
				a.logf("%s", e.Text)
			}
		}
	}()

	// Sources download concurrently, each internally sharded. The semaphore
	// bounds the total number of yt-dlp processes; without it n sources each
	// fanning out to n workers would start n squared of them.
	sem := make(chan struct{}, a.cfg.Parallel)
	var (
		wg       sync.WaitGroup
		fmu      sync.Mutex
		failures int
	)
	for _, i := range idx {
		a.mu.Lock()
		if i >= len(a.rows) {
			a.mu.Unlock()
			continue
		}
		src := a.rows[i].src
		a.mu.Unlock()

		wg.Add(1)
		go func(i int, src config.Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			a.setRow(i, func(r *row) {
				r.state, r.pct, r.phase = running, -1, library.PhaseFetching
				r.file, r.speed, r.eta, r.done, r.note = "", "", "", 0, ""
			})
			a.logf("── %s", src.Name)

			// Forward percentages to this row while it runs.
			rowEv := make(chan library.Event, 64)
			relay := make(chan struct{})
			go func() {
				defer close(relay)
				for e := range rowEv {
					a.setRow(i, func(r *row) {
						if e.Pct >= 0 {
							r.pct = e.Pct
						}
						if e.Phase != library.PhaseIdle {
							r.phase = e.Phase
						}
						if e.File != "" {
							r.file = e.File
						}
						if e.Speed != "" {
							r.speed = e.Speed
						}
						if e.ETA != "" {
							r.eta = e.ETA
						}
						if e.Finished {
							r.done++
							r.pct = -1
						}
					})
					ev <- e
				}
			}()

			err := library.Download(ctx, a.cfg, src, rowEv)
			close(rowEv)
			<-relay

			if err != nil {
				fmu.Lock()
				failures++
				fmu.Unlock()
				a.setRow(i, func(r *row) { r.state, r.note = failed, err.Error() })
				a.logf("%s failed: %v", src.Name, err)
				return
			}
			a.setRow(i, func(r *row) {
				r.state, r.pct, r.phase, r.file = succeeded, -1, library.PhaseIdle, ""
			})
		}(i, src)
	}
	wg.Wait()

	a.logf("── mirroring to %s", a.cfg.Remote.Host)
	if err := library.Sync(ctx, a.cfg, ev); err != nil {
		a.logf("sync failed: %v", err)
		failures++
	} else {
		a.logf("mirror complete")
		if err := library.TriggerScan(ctx, a.cfg); err != nil {
			a.logf("rescan failed: %v", err)
		} else if a.cfg.Remote.ScanURL != "" {
			a.logf("server rescanning")
		}
	}

	close(ev)
	<-done

	if failures == 0 {
		a.logf("done, everything up to date")
	} else {
		a.logf("done with %d failure(s)", failures)
	}
}

// interesting filters the firehose down to lines a person would want in a log.
func interesting(s string) bool {
	switch {
	case strings.HasPrefix(s, "[download] Destination:"),
		strings.HasPrefix(s, "[ExtractAudio]"),
		strings.HasPrefix(s, "[Metadata]"),
		strings.Contains(s, "has already been recorded"),
		strings.HasPrefix(s, "ERROR"),
		strings.HasPrefix(s, "WARNING"):
		return true
	case strings.Contains(s, "sent ") && strings.Contains(s, "bytes"):
		return true
	}
	return false
}
