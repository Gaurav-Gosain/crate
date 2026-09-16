package ui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/library"
	"github.com/Gaurav-Gosain/crate/internal/player"
	"github.com/Gaurav-Gosain/crate/internal/theme"
)

// enterPlay opens play mode, starting mpv and listing the library.
//
// Both are done in the background: mpv takes a moment to open its socket, and
// the library list is a round trip to the music server. Blocking the interface
// on either would make the key press feel broken.
func (a *App) enterPlay() {
	a.mu.Lock()
	if a.mode == modePlay {
		a.mu.Unlock()
		return
	}
	a.mode = modePlay
	a.playLoading = true
	a.mu.Unlock()
	a.redraw()

	go func() {
		p, err := player.New()
		if err != nil {
			a.mu.Lock()
			a.mode, a.playLoading = modeList, false
			a.mu.Unlock()
			a.logf("play: %v", err)
			a.redraw()
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		songs, lerr := library.Tracks(ctx, a.cfg)
		cancel()

		a.mu.Lock()
		a.player = p
		a.songs = songs
		a.playLoading = false
		a.playCursor = 0
		a.mu.Unlock()
		if lerr != nil {
			a.logf("play: could not list the library: %v", lerr)
		}
		a.redraw()
		go a.animate()
	}()
}

// leavePlay stops playback and returns to the list.
func (a *App) leavePlay() {
	a.mu.Lock()
	p := a.player
	cover := a.cover
	a.player = nil
	a.spectrum = nil
	a.cover, a.coverFor = nil, ""
	a.mode = modeList
	a.mu.Unlock()
	if cover != nil {
		a.tty.WriteString(cover.deleteCmd())
		cover.cleanup()
	}
	if p != nil {
		p.Close()
	}
	a.redraw()
}

// animate redraws while a record is turning. Fifteen frames a second is
// enough for the vinyl to look smooth and the bars to track a beat, and cheap
// enough that it does not compete with a running sync.
func (a *App) animate() {
	t := time.NewTicker(66 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		a.mu.Lock()
		p := a.player
		inPlay := a.mode == modePlay
		a.mu.Unlock()
		if !inPlay || p == nil {
			return
		}
		// Only redraw while the record is actually turning. A paused or
		// stopped view is a still image, and repainting it fifteen times a
		// second burns CPU and tears the frame for no benefit.
		if !p.State().Playing {
			continue
		}
		a.redraw()
	}
}

// playSelected starts the highlighted track.
func (a *App) playSelected() {
	a.mu.Lock()
	if a.player == nil || a.playCursor < 0 || a.playCursor >= len(a.songs) {
		a.mu.Unlock()
		return
	}
	song := a.songs[a.playCursor]
	p := a.player
	a.mu.Unlock()

	src, err := library.Source(a.cfg, song)
	if err != nil {
		a.logf("play: %v", err)
		return
	}
	if err := p.Play(src); err != nil {
		a.logf("play: %v", err)
		return
	}
	// The visualiser decodes the same source separately. It is the only way
	// to see inside the audio: mpv will report where it is, but not what it
	// sounds like.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	sp := player.Analyse(ctx, src)
	a.mu.Lock()
	if a.spectrumStop != nil {
		a.spectrumStop()
	}
	a.spectrum, a.spectrumStop, a.nowPlaying = sp, cancel, song
	old := a.cover
	a.cover, a.coverFor = nil, song.Rel
	a.mu.Unlock()

	// Release the previous cover inside the terminal. Without this the
	// terminal keeps every image it has ever been sent, and a long listening
	// session quietly grows its memory by a cover a track.
	if old != nil {
		a.tty.WriteString(old.deleteCmd())
		old.cleanup()
	}

	if !graphicsSupported() {
		return
	}
	go func() {
		actx, acancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer acancel()
		art, err := loadArt(actx, src, 480)
		if err != nil {
			// A missing cover is ordinary: plenty of files have none, and the
			// record is drawn instead. It is not worth a line in the log.
			return
		}
		a.mu.Lock()
		stale := a.coverFor != song.Rel
		if !stale {
			a.cover = art
		}
		a.mu.Unlock()
		if stale {
			art.cleanup()
			return
		}
		a.redraw()
	}()
}

// playStep moves to the next or previous track and plays it.
func (a *App) playStep(d int) {
	a.mu.Lock()
	if len(a.songs) == 0 {
		a.mu.Unlock()
		return
	}
	a.playCursor = (a.playCursor + d + len(a.songs)) % len(a.songs)
	a.mu.Unlock()
	a.playSelected()
}

// handlePlayKey processes a key in play mode.
func (a *App) handlePlayKey(c byte) bool {
	a.mu.Lock()
	p := a.player
	n := len(a.songs)
	a.mu.Unlock()

	switch c {
	case 'q', 27: // q or escape
		a.leavePlay()
	case 3: // ctrl-c still quits the whole app
		return true
	case ' ':
		if p != nil {
			p.Toggle()
		}
	case 'j', 14:
		a.movePlayCursor(1, n)
	case 'k', 16:
		a.movePlayCursor(-1, n)
	case '\r', '\n':
		go a.playSelected()
	case 'n':
		go a.playStep(1)
	case 'b':
		go a.playStep(-1)
	case 'l':
		if p != nil {
			p.Seek(5 * time.Second)
		}
	case 'h':
		if p != nil {
			p.Seek(-5 * time.Second)
		}
	case 't':
		name := theme.Next()
		applyTheme()
		a.cfg.Theme = name
		if err := a.cfg.Save(); err != nil {
			a.logf("could not remember the theme: %v", err)
		}
		a.logf("theme: %s", name)
	}
	a.redraw()
	return false
}

func (a *App) movePlayCursor(d, n int) {
	if n == 0 {
		return
	}
	a.mu.Lock()
	a.playCursor += d
	if a.playCursor < 0 {
		a.playCursor = 0
	}
	if a.playCursor >= n {
		a.playCursor = n - 1
	}
	a.mu.Unlock()
}

// drawPlay renders the whole play view.
func (a *App) drawPlay(b *strings.Builder, w, h int) {
	a.mu.Lock()
	loading := a.playLoading
	songs := a.songs
	cursor := a.playCursor
	p := a.player
	sp := a.spectrum
	now := a.nowPlaying
	cover := a.cover
	a.mu.Unlock()

	top := 3
	contentH := h - top - 1
	if contentH < 8 {
		return
	}

	if loading {
		moveTo(b, top+1, 4)
		fmt.Fprintf(b, "%sopening the record player...%s", muted, reset)
		return
	}

	var st player.State
	if p != nil {
		st = p.State()
	}

	listW := w / 3
	if listW < 26 {
		listW = 26
	}
	if listW > 44 {
		listW = 44
	}

	listBox := rect{1, top, listW, contentH}
	rightX := listW + 2
	rightW := w - listW - 2
	if rightW < 20 {
		rightW = 20
	}

	nowH := contentH * 3 / 5
	if nowH < 7 {
		nowH = 7
	}
	specH := contentH - nowH
	if specH < 4 {
		specH = 4
		nowH = contentH - specH
	}

	nowBox := rect{rightX, top, rightW, nowH}
	specBox := rect{rightX, top + nowH, rightW, specH}

	panel(b, listBox, fmt.Sprintf("library  %d", len(songs)), true)
	panel(b, nowBox, "now playing", false)
	panel(b, specBox, "", false)

	a.drawPlayList(b, songs, cursor, now, listBox.inner())
	a.drawNowPanel(b, st, now, cover, nowBox.inner())
	a.drawSpectrum(b, st, sp, specBox.inner())
}

// drawPlayList shows the library with the playing track marked.
func (a *App) drawPlayList(b *strings.Builder, songs []library.Song, cursor int, now library.Song, r rect) {
	if r.h < 1 {
		return
	}
	if len(songs) == 0 {
		moveTo(b, r.y, r.x+1)
		fmt.Fprintf(b, "%snothing here yet%s", muted, reset)
		return
	}

	start := cursor - r.h/2
	if start < 0 {
		start = 0
	}
	if start+r.h > len(songs) {
		start = len(songs) - r.h
	}
	if start < 0 {
		start = 0
	}

	a.mu.Lock()
	a.hitList, a.hitListTop = r, start
	a.mu.Unlock()

	for i := 0; i < r.h && start+i < len(songs); i++ {
		s := songs[start+i]
		moveTo(b, r.y+i, r.x)
		playing := s.Rel == now.Rel && now.Rel != ""
		mark := "  "
		if playing {
			mark = "▸ "
		}
		label := truncate(s.Title, r.w-2)
		switch {
		case start+i == cursor:
			// Selected rows are filled rather than merely coloured. Colour
			// alone is ambiguous next to the playing row, which is also
			// coloured, and the two are often the same row.
			t := theme.Current()
			fmt.Fprintf(b, "%s%s%s%s%s%s", theme.Bg(t.Accent), theme.Ink(t.Accent), mark, label,
				strings.Repeat(" ", max(0, r.w-2-visibleWidth(label))), reset)
		case playing:
			fmt.Fprintf(b, "%s%s%s%s", accent, mark, label, reset)
		default:
			fmt.Fprintf(b, "%s%s%s%s", muted, mark, label, reset)
		}
	}
}

// drawNowPanel draws the cover, the track details and the progress bar.
func (a *App) drawNowPanel(b *strings.Builder, st player.State, now library.Song, cover *art, r rect) {
	if r.h < 5 || r.w < 16 {
		return
	}

	// The artwork is square, and terminal cells are about twice as tall as
	// they are wide, so a square needs twice as many columns as rows.
	artRows := r.h - 2
	if artRows > 14 {
		artRows = 14
	}
	artCols := artRows * 2
	if artCols > r.w/2 {
		artCols = r.w / 2
		artRows = artCols / 2
	}

	// Centre the artwork in the space above the transport rather than
	// pinning it to the top, which left the panel looking half empty.
	artY := r.y + max(0, (r.h-1-artRows)/2)

	if cover != nil {
		moveTo(b, artY, r.x)
		b.WriteString(cover.place(artCols, artRows))
	} else {
		// No cover, or a terminal that cannot show one: spin a record instead.
		rot := float64(time.Now().UnixMilli()%2600) / 2600 * 2 * math.Pi
		if !st.Playing {
			rot = float64(st.Position.Milliseconds()%2600) / 2600 * 2 * math.Pi
		}
		for i, line := range vinyl(artCols, artRows, rot, theme.Current()) {
			moveTo(b, artY+i, r.x)
			b.WriteString(line)
		}
	}

	tx := r.x + artCols + 3
	tw := r.x + r.w - tx
	if tw > 4 {
		// The details sit level with the middle of the artwork.
		ty := artY + max(0, artRows/2-1)
		if now.Rel == "" {
			moveTo(b, ty, tx)
			fmt.Fprintf(b, "%sselect a track and press enter%s", muted, reset)
		} else {
			moveTo(b, ty, tx)
			fmt.Fprintf(b, "%s%s%s%s", bold, fg, truncate(now.Title, tw), reset)
			moveTo(b, ty+1, tx)
			fmt.Fprintf(b, "%s%s%s", accent, truncate(now.Artist, tw), reset)
			if now.Album != "" {
				moveTo(b, ty+2, tx)
				fmt.Fprintf(b, "%s%s%s", muted, truncate(now.Album, tw), reset)
			}
		}
	}

	// Transport, along the bottom of the panel.
	row := r.y + r.h - 1
	icon := "▶"
	if st.Playing {
		icon = "❚❚"
	}
	moveTo(b, row, r.x)
	fmt.Fprintf(b, "%s%s%s ", accent, icon, reset)

	times := fmt.Sprintf(" %s / %s", clock(st.Position), clock(st.Duration))
	barW := r.w - visibleWidth(times) - 4
	if barW < 6 {
		barW = 6
	}
	pct := 0.0
	if st.Duration > 0 {
		pct = st.Position.Seconds() / st.Duration.Seconds()
	}
	moveTo(b, row, r.x+3)
	b.WriteString(progress(pct, barW))
	fmt.Fprintf(b, "%s%s%s", muted, times, reset)

	a.mu.Lock()
	a.hitProgress = rect{r.x + 3, row, barW, 1}
	a.mu.Unlock()
}

// drawSpectrum draws the visualiser.
func (a *App) drawSpectrum(b *strings.Builder, st player.State, sp *player.Spectrum, r rect) {
	if r.h < 2 || r.w < 8 || sp == nil {
		return
	}
	vals, peaks := sp.BarsWithPeaks(st.Position, BarCount(r.w))
	for i, line := range spectrumBars(vals, peaks, r.h, theme.Current()) {
		moveTo(b, r.y+i, r.x)
		b.WriteString(line)
	}
}

// handleMouse acts on a click, drag or scroll.
func (a *App) handleMouse(ev mouseEvent) bool {
	a.mu.Lock()
	m := a.mode
	list, top := a.hitList, a.hitListTop
	prog := a.hitProgress
	sources := a.hitSources
	p := a.player
	n := len(a.songs)
	a.mu.Unlock()

	switch m {
	case modePlay:
		switch ev.kind {
		case mouseWheelUp:
			a.movePlayCursor(-3, n)
		case mouseWheelDown:
			a.movePlayCursor(3, n)
		case mousePress, mouseDrag:
			if prog.w > 0 && prog.contains(ev.x, ev.y) && p != nil {
				st := p.State()
				if st.Duration > 0 {
					f := float64(ev.x-prog.x) / float64(prog.w-1)
					p.SeekTo(time.Duration(f * float64(st.Duration)))
				}
				break
			}
			if ev.kind == mousePress && list.contains(ev.x, ev.y) {
				idx := top + (ev.y - list.y)
				a.mu.Lock()
				if idx >= 0 && idx < len(a.songs) {
					a.playCursor = idx
				}
				a.mu.Unlock()
				go a.playSelected()
			}
		}
	case modeList:
		switch ev.kind {
		case mouseWheelUp:
			a.moveCursor(-3)
		case mouseWheelDown:
			a.moveCursor(3)
		case mousePress:
			if sources.contains(ev.x, ev.y) {
				a.mu.Lock()
				top := a.hitListTop
				a.mu.Unlock()
				a.setCursor(top + (ev.y - sources.y))
			}
		}
	}
	a.redraw()
	return false
}

// clock formats a duration as m:ss, which is how long a song is.
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}
