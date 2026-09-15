package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/config"
	"github.com/Gaurav-Gosain/crate/internal/library"
)

// searchCount bounds how many entries yt-dlp resolves. Each one costs a
// round trip of roughly two seconds, and album entries expand into their
// tracks, so this is the main lever on how long a search takes.
const searchCount = 8

func (a *App) beginSearch() {
	a.beginPrompt("search:", a.runSearch)
}

func (a *App) runSearch(q string) {
	a.mu.Lock()
	if a.searching {
		a.mu.Unlock()
		return
	}
	a.searching = true
	a.query = q
	a.mu.Unlock()
	a.logf("searching youtube music for %q, this takes a moment", q)
	a.redraw()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res, err := library.Search(ctx, q, searchCount)

	a.mu.Lock()
	a.searching = false
	if err != nil {
		a.mu.Unlock()
		a.logf("search failed: %v", err)
		return
	}
	a.results = res
	a.rcursor = 0
	a.mode = modeSearch
	a.mu.Unlock()
	a.logf("%d result(s). enter adds, esc cancels", len(res))
	a.redraw()
}

// addResult turns a search hit into a source.
//
// Both tracks and albums become sources rather than one-shot downloads: the
// download archive makes re-syncing an already-fetched track free, and an
// album kept as a source will pick up anything added to it later.
func (a *App) addResult(r library.Result) {
	name := r.Title
	if r.Artist != "" {
		name = r.Artist + " - " + r.Title
	}
	if r.Kind == library.Album {
		name = "album: " + name
	}

	src := config.Source{Name: name, URL: config.NormalizeURL(r.URL)}

	a.mu.Lock()
	for _, s := range a.cfg.Sources {
		if s.URL == src.URL {
			a.mu.Unlock()
			a.logf("already added: %s", name)
			return
		}
	}
	a.cfg.Sources = append(a.cfg.Sources, src)
	a.rows = append(a.rows, row{src: src, state: idle})
	a.mode = modeList
	a.results = nil
	a.cursor = len(a.rows) - 1
	a.mu.Unlock()

	if err := a.cfg.Save(); err != nil {
		a.logf("could not save config: %v", err)
		return
	}
	a.logf("added %s", name)
	switch {
	case config.IsEndlessMix(src.URL):
		a.logf("note: that is a generated radio mix, which has no end and can pull in hundreds of tracks")
	case config.IsArtistChannel(src.URL):
		a.logf("note: that is a whole artist catalogue, usually hundreds of tracks and several gigabytes")
	}
	a.redraw()

	a.mu.Lock()
	idx := len(a.rows) - 1
	a.mu.Unlock()
	a.run([]int{idx})
}

func (a *App) handleSearchKey(c byte) bool {
	switch c {
	case 'q', 3:
		return true
	case 27: // esc
		a.mu.Lock()
		a.mode, a.results = modeList, nil
		a.mu.Unlock()
		a.redraw()
	case 'j', 14:
		a.moveResult(1)
	case 'k', 16:
		a.moveResult(-1)
	case '\r', '\n':
		a.mu.Lock()
		var r library.Result
		ok := a.rcursor >= 0 && a.rcursor < len(a.results)
		if ok {
			r = a.results[a.rcursor]
		}
		a.mu.Unlock()
		if ok {
			a.addResult(r)
		}
	}
	return false
}

func (a *App) moveResult(d int) {
	a.mu.Lock()
	a.rcursor += d
	if a.rcursor < 0 {
		a.rcursor = 0
	}
	if a.rcursor >= len(a.results) {
		a.rcursor = max(len(a.results)-1, 0)
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *App) drawResults(b *strings.Builder, w, h int) {
	a.mu.Lock()
	res := a.results
	cur := a.rcursor
	q := a.query
	a.mu.Unlock()

	moveTo(b, headerRows+1, 1)
	clearLine(b)
	fmt.Fprintf(b, " %sresults for%s %s%s%s   %senter add   esc back%s",
		dim, reset, bold, truncate(q, 40), reset, dim, reset)

	top := headerRows + 2
	avail := h - top - footerRows - 1
	start := 0
	if cur >= avail {
		start = cur - avail + 1
	}

	for i := 0; i < avail; i++ {
		moveTo(b, top+i, 1)
		clearLine(b)
		if start+i >= len(res) {
			continue
		}
		r := res[start+i]

		marker := "  "
		nameStyle := ""
		if start+i == cur {
			marker = accent + "▌ " + reset
			nameStyle = bold
		}

		kindCol := muted
		if r.Kind == library.Album {
			kindCol = warn
		}

		meta := r.Artist
		if r.Kind == library.Track && r.Album != "" {
			meta = fmt.Sprintf("%s · %s", r.Artist, r.Album)
		}
		if r.Year > 0 {
			meta = fmt.Sprintf("%s · %d", meta, r.Year)
		}
		if r.Kind == library.Track && r.Duration > 0 {
			meta = fmt.Sprintf("%s · %s", meta, dur(r.Duration))
		}

		titleW := max(w/2-6, 16)
		fmt.Fprintf(b, "%s%s%-5s%s %s%s%s  %s",
			marker, kindCol, r.Kind, reset,
			nameStyle, pad(r.Title, titleW), reset,
			truncate(muted+meta+reset, max(w-titleW-12, 6)))
	}
}

func dur(sec float64) string {
	s := int(sec)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
