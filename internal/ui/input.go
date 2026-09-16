package ui

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/config"
	"github.com/Gaurav-Gosain/crate/internal/library"
	"github.com/Gaurav-Gosain/crate/internal/state"
)

func (a *App) watchResize() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	for range ch {
		a.redraw()
	}
}

func (a *App) readKeys() {
	buf := make([]byte, 256)
	// pending holds bytes that arrived mid sequence. A mouse report split
	// across two reads must not be handed to the key handler, or its tail is
	// typed into whatever has focus.
	var pending []byte
	for {
		n, err := a.tty.Read(buf)
		if err != nil || n == 0 {
			a.stop()
			return
		}
		pending = append(pending, buf[:n]...)

		for len(pending) > 0 {
			if n, part := skipAPC(pending); part {
				break
			} else if n > 0 {
				// Graphics replies are suppressed on success, so anything
				// that arrives is the terminal objecting. Saying so is the
				// difference between a blank rectangle and a reason for it.
				if msg := graphicsError(pending[:n]); msg != "" {
					a.logf("album art: %s", msg)
				}
				pending = pending[n:]
				continue
			}
			if k, n, ok, part := parseCSIKey(pending); part {
				break
			} else if ok {
				pending = pending[n:]
				if a.handleKey(k) {
					a.stop()
					return
				}
				continue
			} else if n > 0 {
				pending = pending[n:]
				continue
			}
			ev, consumed, ok, partial := parseMouse(pending)
			if partial {
				break // wait for the rest of it
			}
			if ok {
				pending = pending[consumed:]
				if a.handleMouse(ev) {
					a.stop()
					return
				}
				continue
			}
			if consumed > 0 {
				// A malformed report: drop it rather than typing it.
				pending = pending[consumed:]
				continue
			}
			if a.handleKey(pending[0]) {
				a.stop()
				return
			}
			pending = pending[1:]
		}
		a.redraw()
	}
}

// handleKey processes one byte and reports whether the app should exit.
func (a *App) handleKey(c byte) bool {
	a.mu.Lock()
	editing := a.prompt != ""
	m := a.mode
	a.mu.Unlock()

	if editing {
		return a.handleEditKey(c)
	}
	if m == modeSearch {
		return a.handleSearchKey(c)
	}
	if m == modeOverlay {
		return a.handleOverlayKey(c)
	}
	if m == modePlay {
		return a.handlePlayKey(c)
	}

	switch c {
	case 'q', 3: // q or ctrl-c
		return true
	case 'j', 14, keyDown:
		a.moveCursor(1)
	case 'k', 16, keyUp:
		a.moveCursor(-1)
	case keyPgDn:
		a.moveCursor(8)
	case keyPgUp:
		a.moveCursor(-8)
	case 'g', keyHome:
		a.setCursor(0)
	case 'G', keyEnd:
		a.mu.Lock()
		n := len(a.rows)
		a.mu.Unlock()
		a.setCursor(n - 1)
	case 's':
		go a.syncAll()
	case '\r', '\n':
		go a.syncSelected()
	case 'a':
		a.beginPrompt("url:", a.addSource)
	case '/':
		a.beginSearch()
	case 'd':
		a.removeSelected()
	case 'p':
		a.enterPlay()
	case 't':
		a.openThemes()
	case ':', 11: // ':' or ctrl-k
		a.openPalette()
	case 'r':
		go a.scanOnly()
	case '?':
		a.logf("keys: / search | s sync all | enter sync selected | a add url | d remove | r rescan | j/k move | q quit")
	}
	return false
}

func (a *App) handleEditKey(c byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch c {
	case 27: // esc
		a.prompt, a.buf, a.onSubm = "", nil, nil
	case '\r', '\n':
		val := strings.TrimSpace(string(a.buf))
		fn := a.onSubm
		a.prompt, a.buf, a.onSubm = "", nil, nil
		if fn != nil && val != "" {
			go fn(val)
		}
	case 127, 8: // backspace
		if len(a.buf) > 0 {
			a.buf = a.buf[:len(a.buf)-1]
		}
	case 3:
		return true
	default:
		if c >= 32 && c < 127 {
			a.buf = append(a.buf, rune(c))
		}
	}
	return false
}

func (a *App) beginPrompt(label string, fn func(string)) {
	a.mu.Lock()
	a.prompt, a.buf, a.onSubm = label, nil, fn
	a.mu.Unlock()
	a.redraw()
}

func (a *App) moveCursor(d int) {
	a.mu.Lock()
	a.cursor = clamp(a.cursor+d, 0, max(len(a.rows)-1, 0))
	a.mu.Unlock()
	a.redraw()
}

func (a *App) setCursor(i int) {
	a.mu.Lock()
	a.cursor = clamp(i, 0, max(len(a.rows)-1, 0))
	a.mu.Unlock()
	a.redraw()
}

// commitSource appends a source to the config and the list, saves, and
// reports it in the log along with a warning for the source shapes that pull
// in far more than an album. It returns the new row's index, or -1 when the
// save failed. Shared by the url prompt and the search results, which used
// to carry two drifting copies of this.
func (a *App) commitSource(s config.Source) int {
	a.mu.Lock()
	a.cfg.ClearRemoved(s.URL)
	a.cfg.Sources = append(a.cfg.Sources, s)
	a.rows = append(a.rows, row{src: s, state: idle})
	idx := len(a.rows) - 1
	a.cursor = idx
	a.mu.Unlock()

	if err := a.saveCfg(); err != nil {
		a.logf("could not save config: %v", err)
		return -1
	}
	a.logf("added %s", s.Name)
	switch {
	case config.IsEndlessMix(s.URL):
		a.logf("note: that is a generated radio mix, which has no end and can pull in hundreds of tracks")
	case config.IsArtistChannel(s.URL):
		a.logf("note: that is a whole artist catalogue, usually hundreds of tracks and several gigabytes")
	}
	return idx
}

// addSource derives a readable name from the URL so the list is scannable
// without the user having to name every playlist by hand.
func (a *App) addSource(url string) {
	if n := config.NormalizeURL(url); n != url {
		a.logf("using the releases tab, which has album and track tags")
		url = n
	}
	idx := a.commitSource(config.Source{Name: deriveName(url), URL: url})
	if idx < 0 {
		return
	}

	// Adding a source and having nothing happen is not what anyone means by
	// adding it, so fetch it straight away. Syncing the whole library again
	// later is free for anything already in the download archive.
	a.redraw()
	a.run([]int{idx})
}

func (a *App) removeSelected() {
	a.mu.Lock()
	if len(a.rows) == 0 || a.busy {
		a.mu.Unlock()
		return
	}
	i := a.cursor
	src := a.rows[i].src
	a.rows = slices.Delete(a.rows, i, i+1)
	a.cfg.Sources = slices.Delete(a.cfg.Sources, i, i+1)
	if a.cursor >= len(a.rows) {
		a.cursor = max(len(a.rows)-1, 0)
	}
	// The tombstone is what makes the removal stick. Without it the next
	// pull from the shared state finds the source still listed by another
	// device and puts it straight back, music and all.
	a.cfg.MarkRemoved(src.URL)
	a.mu.Unlock()

	if err := a.saveCfg(); err != nil {
		a.logf("could not save config: %v", err)
		return
	}
	a.logf("removed %s", src.Name)
	a.redraw()

	// Deleting the tracks needs the network, so it happens in the background.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		// Publish the tombstone first, so the removal sticks even if the
		// track deletion below fails part way.
		if err := state.Push(ctx, a.cfgSnapshot()); err != nil {
			a.logf("   could not publish removal: %v", err)
		}
		r, err := library.RemoveTracks(ctx, a.cfg, src, a.logf)
		if err != nil {
			a.logf("   could not remove tracks: %v", err)
			return
		}
		// Push again now the archive has forgotten the removed ids. The
		// first push sent the archive with the ids still in it, and leaving
		// that copy on the remote meant the next pull resurrected them, so
		// re-adding the source downloaded nothing.
		if err := state.Push(ctx, a.cfgSnapshot()); err != nil {
			a.logf("   could not publish the pruned archive: %v", err)
		}
		if r.Files == 0 {
			a.logf("   no tracks to remove")
			return
		}
		a.logf("   removed %d track(s), kept %d shared by other sources", r.Files, r.Kept)
		if err := library.TriggerScan(ctx, a.cfg); err != nil {
			a.logf("   reindex failed: %v", err)
		}
		a.redraw()
	}()
}

func deriveName(raw string) string {
	s := raw
	for _, p := range []string{"https://", "http://", "www."} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.IndexAny(s, "?&"); i > 0 {
		// keep a playlist id if that is all that distinguishes the url
		if strings.Contains(s, "list=") {
			if j := strings.Index(s, "list="); j >= 0 {
				id := s[j+5:]
				if k := strings.IndexAny(id, "&"); k > 0 {
					id = id[:k]
				}
				return truncate(strings.TrimSuffix(s[:i], "/")+" "+id, 40)
			}
		}
		s = s[:i]
	}
	return truncate(strings.TrimSuffix(s, "/"), 40)
}

func (a *App) scanOnly() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := library.TriggerScan(ctx, a.cfg); err != nil {
		a.logf("rescan failed: %v", err)
		return
	}
	a.logf("asked the server to rescan")
}
