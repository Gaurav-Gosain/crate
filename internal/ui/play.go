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
	a.player = nil
	a.spectrum = nil
	a.mode = modeList
	a.mu.Unlock()
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
	a.mu.Unlock()
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
	a.mu.Unlock()

	top := 3
	if loading {
		moveTo(b, top+2, 4)
		fmt.Fprintf(b, "%sopening the record player...%s", muted, reset)
		return
	}

	var st player.State
	if p != nil {
		st = p.State()
	}

	// The list takes the left third, the record and bars the rest.
	listW := w / 3
	if listW < 24 {
		listW = 24
	}
	if listW > 46 {
		listW = 46
	}
	rightX := listW + 3
	rightW := w - rightX - 1
	if rightW < 10 {
		rightW = 10
	}

	a.drawPlayList(b, songs, cursor, now, top, listW, h-top-4)
	a.drawStage(b, st, sp, top, rightX, rightW, h-top-5)
	a.drawNowPlaying(b, st, now, h, w)
}

// drawPlayList shows the library with the playing track marked.
func (a *App) drawPlayList(b *strings.Builder, songs []library.Song, cursor int, now library.Song, top, w, rows int) {
	if rows < 1 {
		return
	}
	moveTo(b, top-1, 2)
	fmt.Fprintf(b, "%slibrary%s %s%d%s", muted, reset, dim, len(songs), reset)

	if len(songs) == 0 {
		moveTo(b, top+1, 2)
		fmt.Fprintf(b, "%snothing here yet%s", muted, reset)
		return
	}

	// Keep the cursor in view without jumping the list around more than it
	// needs to.
	start := cursor - rows/2
	if start < 0 {
		start = 0
	}
	if start+rows > len(songs) {
		start = len(songs) - rows
	}
	if start < 0 {
		start = 0
	}

	for i := 0; i < rows && start+i < len(songs); i++ {
		s := songs[start+i]
		moveTo(b, top+i, 2)
		mark := " "
		style := ""
		if s.Rel == now.Rel && now.Rel != "" {
			mark, style = "▸", accent
		}
		if start+i == cursor {
			fmt.Fprintf(b, "%s%s %s%s", accent, mark, truncate(s.Title, w-2), reset)
		} else {
			fmt.Fprintf(b, "%s%s%s %s%s%s", style, mark, reset, style, truncate(s.Title, w-2), reset)
		}
	}
}

// drawStage draws the record and the visualiser.
func (a *App) drawStage(b *strings.Builder, st player.State, sp *player.Spectrum, top, x, w, rows int) {
	if rows < 6 || w < 12 {
		return
	}
	t := theme.Current()

	barRows := rows / 3
	if barRows < 3 {
		barRows = 3
	}
	if barRows > 10 {
		barRows = 10
	}
	discRows := rows - barRows - 1
	if discRows < 3 {
		discRows = 3
	}

	// One full turn every two seconds, like a record at roughly 33rpm sped up
	// enough to read as spinning on a terminal's refresh.
	rot := 0.0
	if st.Playing {
		rot = float64(time.Now().UnixMilli()%2000) / 2000 * 2 * math.Pi
	} else {
		rot = float64(st.Position.Milliseconds()%2000) / 2000 * 2 * math.Pi
	}

	discW := discRows * 2
	if discW > w {
		discW = w
	}
	disc := vinyl(discW, discRows, rot, t)
	offset := x + (w-discW)/2
	for i, line := range disc {
		moveTo(b, top+i, offset)
		b.WriteString(line)
	}

	if sp != nil {
		vals := sp.Bars(st.Position, w)
		bars := spectrumBars(vals, barRows, t)
		for i, line := range bars {
			moveTo(b, top+discRows+1+i, x)
			b.WriteString(line)
		}
	}
}

// drawNowPlaying is the title, times and progress bar along the bottom.
func (a *App) drawNowPlaying(b *strings.Builder, st player.State, now library.Song, h, w int) {
	row := h - 3
	moveTo(b, row, 2)
	if now.Rel == "" {
		fmt.Fprintf(b, "%sselect a track and press enter%s", muted, reset)
	} else {
		icon := "❚❚"
		if st.Playing {
			icon = "▶"
		}
		title := now.Title
		sub := now.Artist
		if now.Album != "" {
			sub += " · " + now.Album
		}
		fmt.Fprintf(b, "%s%s%s %s%s%s  %s%s%s",
			accent, icon, reset,
			bold, truncate(title, w/2), reset,
			muted, truncate(sub, w/3), reset)
	}

	moveTo(b, row+1, 2)
	pct := 0.0
	if st.Duration > 0 {
		pct = st.Position.Seconds() / st.Duration.Seconds()
	}
	width := w - 22
	if width < 10 {
		width = 10
	}
	fmt.Fprintf(b, "%s%s%s %s%s / %s%s",
		accent, bar(pct*100, width), reset,
		muted, clock(st.Position), clock(st.Duration), reset)
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
