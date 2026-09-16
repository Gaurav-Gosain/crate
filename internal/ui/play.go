package ui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/library"
	"github.com/Gaurav-Gosain/crate/internal/lyrics"
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
	a.lyrics, a.lyricsFor, a.lyricsNote = nil, "", ""
	a.mode = modeList
	a.mu.Unlock()
	if cover != nil {
		a.write(cover.deleteCmd())
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
	slow := 0
	for range t.C {
		a.mu.Lock()
		p := a.player
		inPlay := a.mode == modePlay
		a.mu.Unlock()
		if !inPlay || p == nil {
			return
		}
		// The record only needs full frame rate while it is turning. Paused,
		// the view is nearly still, but not entirely: the selected title
		// scrolls if it does not fit, so a slower beat keeps that moving
		// without repainting a static screen fifteen times a second.
		if !p.State().Playing {
			slow++
			if slow%3 != 0 {
				continue
			}
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
		a.write(old.deleteCmd())
		old.cleanup()
	}

	// Lyrics are looked up in the background: the track is already playing and
	// should not wait on a web request that may find nothing.
	go func() {
		a.mu.Lock()
		a.lyrics, a.lyricsFor, a.lyricsNote = nil, song.Rel, "looking for lyrics..."
		a.mu.Unlock()

		// Wait for the track length before searching. Duration is the
		// strongest evidence that a result is the same recording rather than
		// a cover or a remix, and mpv only knows it a moment after the file
		// opens. Searching without it matched a different artist's version.
		var dur time.Duration
		for i := 0; i < 30; i++ {
			if d := p.State().Duration; d > 0 {
				dur = d
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		lctx, lcancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer lcancel()
		got, err := lyrics.Fetch(lctx, song.Artist, song.Title, dur)

		a.mu.Lock()
		defer a.mu.Unlock()
		if a.lyricsFor != song.Rel {
			return // the track moved on while this was in flight
		}
		if err != nil {
			a.lyrics, a.lyricsNote = nil, "no synced lyrics for this track"
			return
		}
		a.lyrics, a.lyricsNote = got, ""
	}()

	if !graphicsSupported() {
		return
	}
	go func() {
		actx, acancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer acancel()
		art, err := loadArt(actx, src, 320)
		if err != nil {
			// A missing cover is ordinary: plenty of files have none, and the
			// record is drawn instead. It is not worth a line in the log.
			return
		}
		a.mu.Lock()
		stale := a.coverFor != song.Rel
		a.mu.Unlock()
		if stale {
			art.cleanup()
			return
		}

		a.mu.Lock()
		if a.coverFor == song.Rel {
			a.cover = art
		}
		a.mu.Unlock()
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
	case 'j', 14, keyDown:
		a.movePlayCursor(1, n)
	case 'k', 16, keyUp:
		a.movePlayCursor(-1, n)
	case keyPgDn:
		a.movePlayCursor(10, n)
	case keyPgUp:
		a.movePlayCursor(-10, n)
	case keyHome:
		a.mu.Lock()
		a.playCursor = 0
		a.mu.Unlock()
	case keyEnd:
		a.mu.Lock()
		a.playCursor = max(0, len(a.songs)-1)
		a.mu.Unlock()
	case '\r', '\n':
		go a.playSelected()
	case 'n':
		go a.playStep(1)
	case 'b':
		go a.playStep(-1)
	case 'l', keyRight:
		if p != nil {
			p.Seek(5 * time.Second)
		}
	case 'h', keyLeft:
		if p != nil {
			p.Seek(-5 * time.Second)
		}
	case 'y':
		a.mu.Lock()
		a.showLyrics = !a.showLyrics
		on := a.showLyrics
		a.mu.Unlock()
		if on {
			a.logf("showing lyrics")
		} else {
			a.logf("showing the spectrum")
		}
	case 't':
		a.openThemes()
	case ':', 11: // ':' or ctrl-k
		a.openPalette()
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
//
// The screen is three panels: the library on the left, the now playing panel
// top right, and the spectrum under it. The now playing panel is sized to its
// content, artwork plus a transport line, rather than to a fixed fraction of
// the screen; sized by fraction it was mostly empty on a tall terminal, which
// is exactly the void the panel used to read as.
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
		clearExcept(b, w, top, contentH, rect{})
		moveTo(b, top+1, 4)
		fmt.Fprintf(b, "%sopening the record player...%s", muted, reset)
		return
	}

	var st player.State
	if p != nil {
		st = p.State()
	}

	lay := playLayout(w, top, contentH)

	// Work out where the artwork sits before clearing, so those cells can be
	// left alone. Everything else on screen is wiped and redrawn.
	artBox := a.artRect(lay.now.inner())
	clearExcept(b, w, top, contentH, artBox)

	panel(b, lay.list, fmt.Sprintf("library · %d tracks", len(songs)), true)
	panel(b, lay.now, "now playing", false)
	a.mu.Lock()
	showLyrics, lyr, lyrNote := a.showLyrics, a.lyrics, a.lyricsNote
	a.mu.Unlock()
	if lay.spec.h > 0 {
		title := "spectrum"
		if showLyrics {
			title = "lyrics"
		}
		panel(b, lay.spec, title, false)
	}

	trackNo := 0
	if now.Rel != "" {
		for i := range songs {
			if songs[i].Rel == now.Rel {
				trackNo = i + 1
				break
			}
		}
	}

	a.drawPlayList(b, songs, cursor, now, lay.list.inner())
	a.drawNowPanel(b, st, now, cover, lay.now.inner(), trackNo, len(songs))
	if lay.spec.h > 0 {
		if showLyrics {
			a.drawLyrics(b, st, lyr, lyrNote, lay.spec.inner())
		} else {
			a.drawSpectrum(b, st, sp, lay.spec.inner())
		}
	}
}

// playPanels is where the three panels of the play view land. spec has zero
// height when the screen is too short to give the visualiser a useful one.
type playPanels struct {
	list, now, spec rect
}

// playLayout works out the three panel rectangles.
//
// It is a pure function of the screen size so the arithmetic can be asserted:
// the panels have to tile the content area exactly, and the now playing panel
// has to come out tall enough for its artwork and transport line.
func playLayout(w, top, contentH int) playPanels {
	// A column of margin either side, so the panels read as objects on the
	// screen rather than as a grid welded to its edges.
	const margin = 1

	// The list gives way on a narrow screen: the right column has the
	// artwork and the transport, which stop working below about 24 columns,
	// where a narrower list merely shows shorter titles.
	listW := clamp(w/3, 24, 50)
	if maxList := w - 2*margin - 1 - 24; listW > maxList {
		listW = max(16, maxList)
	}

	list := rect{1 + margin, top, listW, contentH}
	rightX := list.x + listW + 1
	rightW := w - margin - rightX + 1

	// The artwork sets the panel height: rows for the art, one blank row, the
	// transport, and the two border rows. The art gets what the screen can
	// spare once the spectrum has a workable height.
	artRows := clamp(contentH-13, 7, 16)
	nowH := artRows + 4
	if nowH > contentH {
		nowH = contentH
	}
	specH := contentH - nowH
	if specH < 5 {
		// Too short to split: the spectrum is dropped rather than squeezed
		// into a strip of border with one row of bars inside it.
		specH = 0
		nowH = contentH
	}

	return playPanels{
		list: list,
		now:  rect{rightX, top, rightW, nowH},
		spec: rect{rightX, top + nowH, rightW, specH},
	}
}

// playRow is one line of the library pane: a song, an artist heading, or the
// blank row that separates one artist's group from the next.
type playRow struct {
	song   int    // index into songs, or -1
	header string // artist name when this row is a heading
}

// buildPlayRows lays the library out as artist groups.
//
// A flat run of nine hundred titles gives the eye nothing to hold on to; the
// headings are what let a reader keep track of where in the library they are.
// It also returns each song's display position, so the scroll window can be
// centred on the cursor in display rows rather than song indices.
func buildPlayRows(songs []library.Song) ([]playRow, []int) {
	rows := make([]playRow, 0, len(songs)+16)
	pos := make([]int, len(songs))
	last := "\x00" // never equal to a real artist, including the empty one
	for i, s := range songs {
		if s.Artist != last {
			if len(rows) > 0 {
				rows = append(rows, playRow{song: -1})
			}
			name := s.Artist
			if name == "" {
				name = "unknown artist"
			}
			rows = append(rows, playRow{song: -1, header: name})
			last = s.Artist
		}
		pos[i] = len(rows)
		rows = append(rows, playRow{song: i})
	}
	return rows, pos
}

// drawPlayList shows the library grouped by artist, with the playing track
// marked and the selected one filled.
func (a *App) drawPlayList(b *strings.Builder, songs []library.Song, cursor int, now library.Song, r rect) {
	if r.h < 1 {
		return
	}
	if len(songs) == 0 {
		moveTo(b, r.y, r.x+1)
		fmt.Fprintf(b, "%snothing here yet%s", muted, reset)
		return
	}

	rows, pos := buildPlayRows(songs)
	cp := 0
	if cursor >= 0 && cursor < len(pos) {
		cp = pos[cursor]
	}
	start := cp - r.h/2
	if start > len(rows)-r.h {
		start = len(rows) - r.h
	}
	if start < 0 {
		start = 0
	}

	// Which song each visible row is, for turning a click back into a track.
	// Headings and spacers map to none.
	rowSong := make([]int, r.h)
	for i := range rowSong {
		rowSong[i] = -1
	}

	x0 := r.x + 1
	avail := r.w - 2
	for i := 0; i < r.h && start+i < len(rows); i++ {
		row := rows[start+i]
		if row.song < 0 {
			if row.header == "" {
				continue
			}
			name := truncate(row.header, avail-4)
			fill := avail - visibleWidth(name) - 1
			moveTo(b, r.y+i, x0)
			fmt.Fprintf(b, "%s%s%s%s ", bold, muted, name, reset)
			if fill > 0 {
				fmt.Fprintf(b, "%s%s%s", rule, strings.Repeat("─", fill), reset)
			}
			continue
		}

		rowSong[i] = row.song
		s := songs[row.song]
		playing := s.Rel == now.Rel && now.Rel != ""
		mark := "  "
		if playing {
			mark = "▸ "
		}
		titleW := avail - 2
		label := truncate(s.Title, titleW)
		if row.song == cursor {
			// The selected row scrolls when its title does not fit, so a long
			// name can still be read without widening the panel.
			label = marquee(s.Title, titleW, time.Now())
		}
		moveTo(b, r.y+i, r.x)
		switch {
		case row.song == cursor:
			// Selected rows are filled rather than merely coloured. Colour
			// alone is ambiguous next to the playing row, which is also
			// coloured, and the two are often the same row.
			t := theme.Current()
			fmt.Fprintf(b, "%s%s %s%s%s%s", theme.Bg(t.Accent), theme.Ink(t.Accent), mark, label,
				strings.Repeat(" ", max(0, r.w-3-visibleWidth(label))), reset)
		case playing:
			fmt.Fprintf(b, " %s%s%s%s", accent, mark, label, reset)
		default:
			fmt.Fprintf(b, " %s%s%s%s", mark, fg, label, reset)
		}
	}

	a.mu.Lock()
	a.hitList, a.hitListTop = r, start
	a.hitPlayRows = rowSong
	a.mu.Unlock()
}

// artRect returns the cells the artwork occupies inside the now playing panel.
// The geometry lives here rather than inside the drawing so the clearing pass
// can avoid exactly those cells.
func (a *App) artRect(r rect) rect {
	if r.h < 5 || r.w < 16 {
		return rect{}
	}
	// Rows for the art, a blank row, and the transport line fill the panel
	// exactly, so there is no dead band above or below.
	artRows := r.h - 2
	artCols := artRows * 2
	if artCols > r.w/2 {
		// A narrow panel caps the art by width instead. Kept even so the
		// halved value stays square.
		artCols = r.w / 2
		artCols -= artCols % 2
		artRows = artCols / 2
	}
	if artRows < 2 || artCols < 4 {
		return rect{}
	}
	artY := r.y + max(0, (r.h-2-artRows)/2)
	return rect{r.x + 1, artY, artCols, artRows}
}

// clearExcept blanks the content area a row at a time, skipping any cells
// inside keep. Writing spaces over a cell erases it just as a screen clear
// would, so the artwork has to be stepped around rather than painted over.
func clearExcept(b *strings.Builder, w, top, h int, keep rect) {
	for y := top; y < top+h; y++ {
		if keep.w == 0 || y < keep.y || y >= keep.y+keep.h {
			moveTo(b, y, 1)
			b.WriteString(strings.Repeat(" ", w))
			continue
		}
		if keep.x > 1 {
			moveTo(b, y, 1)
			b.WriteString(strings.Repeat(" ", keep.x-1))
		}
		if right := w - (keep.x + keep.w) + 1; right > 0 {
			moveTo(b, y, keep.x+keep.w)
			b.WriteString(strings.Repeat(" ", right))
		}
	}
}

// drawNowPanel draws the cover, the track details and the transport.
func (a *App) drawNowPanel(b *strings.Builder, st player.State, now library.Song, cover *art, r rect, trackNo, total int) {
	if r.h < 5 || r.w < 16 {
		return
	}

	ar := a.artRect(r)
	if ar.w == 0 {
		return
	}

	if cover != nil {
		// The placement rides in the frame at the cursor, so position first.
		// displayCmd remembers where it last placed the image and sends
		// nothing when the rectangle has not moved.
		moveTo(b, ar.y, ar.x)
		b.WriteString(cover.displayCmd(ar))
	} else {
		// No cover, or a terminal that cannot show one: spin a record instead.
		rot := float64(time.Now().UnixMilli()%2600) / 2600 * 2 * math.Pi
		if !st.Playing {
			rot = float64(st.Position.Milliseconds()%2600) / 2600 * 2 * math.Pi
		}
		for i, line := range vinyl(ar.w, ar.h, rot, theme.Current()) {
			moveTo(b, ar.y+i, ar.x)
			b.WriteString(line)
		}
	}

	// The details, as a block beside the art: title, artist, album, then a
	// quiet line placing the track in the library. The block is centred on
	// the art so the two read as one composition.
	tx := ar.x + ar.w + 3
	tw := r.x + r.w - tx - 1
	if tw >= 12 {
		type line struct{ style, text string }
		var lines []line
		if now.Rel == "" {
			lines = []line{
				{muted, "nothing playing"},
				{"", ""},
				{muted, "enter plays the selected track"},
			}
		} else {
			for _, tl := range wrapCells(now.Title, tw, 2) {
				lines = append(lines, line{bold + fg, tl})
			}
			if now.Artist != "" {
				lines = append(lines, line{accent, truncate(now.Artist, tw)})
			}
			if now.Album != "" {
				lines = append(lines, line{muted, truncate(now.Album, tw)})
			}
			meta := ""
			if trackNo > 0 && total > 0 {
				meta = fmt.Sprintf("track %d of %d", trackNo, total)
			}
			if ext := strings.TrimPrefix(strings.ToLower(pathExt(now.Rel)), "."); ext != "" {
				if meta != "" {
					meta += " · "
				}
				meta += ext
			}
			if meta != "" {
				lines = append(lines, line{"", ""}, line{muted, truncate(meta, tw)})
			}
		}
		ty := ar.y + max(0, (ar.h-len(lines))/2)
		for i, ln := range lines {
			if ty+i > r.y+r.h-3 {
				break
			}
			if ln.text == "" {
				continue
			}
			moveTo(b, ty+i, tx)
			fmt.Fprintf(b, "%s%s%s", ln.style, ln.text, reset)
		}
	}

	a.drawTransport(b, st, rect{r.x + 1, r.y + r.h - 1, r.w - 2, 1})
}

// drawTransport draws the play state, elapsed time, position bar and time
// remaining along one row, and records the bar as the click target for seeks.
func (a *App) drawTransport(b *strings.Builder, st player.State, r rect) {
	// The icon reports state rather than naming the key: a turning record
	// shows an arrow, a paused one shows the bars.
	icon, style := "▶", accent
	if !st.Playing {
		icon, style = "❚❚", muted
	}

	elapsed := clock(st.Position)
	remain := "-" + clock(max(0, st.Duration-st.Position))
	if st.Duration == 0 {
		remain = "-0:00"
	}

	barX := r.x + 2 + 2 + visibleWidth(elapsed) + 2
	barW := r.x + r.w - barX - visibleWidth(remain) - 2
	if barW < 6 {
		barW = 6
	}

	pct := 0.0
	if st.Duration > 0 {
		pct = st.Position.Seconds() / st.Duration.Seconds()
	}

	moveTo(b, r.y, r.x)
	fmt.Fprintf(b, "%s%s%s", style, pad(icon, 2), reset)
	moveTo(b, r.y, r.x+4)
	fmt.Fprintf(b, "%s%s%s", fg, elapsed, reset)
	moveTo(b, r.y, barX)
	b.WriteString(progress(pct, barW))
	moveTo(b, r.y, barX+barW+2)
	fmt.Fprintf(b, "%s%s%s", muted, remain, reset)

	a.mu.Lock()
	a.hitProgress = rect{barX, r.y, barW, 1}
	a.mu.Unlock()
}

// drawLyrics shows the words for the moment being played, the line in hand
// bright and its neighbours dimmed.
//
// The window follows the track rather than scrolling steadily, so the current
// line stays in the same place and the eye does not have to chase it. Nothing
// is guessed: where the service has no timed words for a recording, that is
// said plainly rather than showing untimed ones, which would be confidently
// wrong about every line.
func (a *App) drawLyrics(b *strings.Builder, st player.State, l *lyrics.Lyrics, note string, r rect) {
	if r.h < 2 || r.w < 12 {
		return
	}
	if l == nil || len(l.Lines) == 0 {
		msg := note
		if msg == "" {
			msg = "looking for lyrics..."
		}
		moveTo(b, r.y+r.h/2, r.x+max(0, (r.w-visibleWidth(msg))/2))
		fmt.Fprintf(b, "%s%s%s", muted, msg, reset)
		return
	}

	cur := l.At(st.Position)
	// Hold the current line a third of the way down: enough of what is coming
	// to read ahead, enough of what has gone to keep your place.
	first := cur - r.h/3
	t := theme.Current()

	for i := 0; i < r.h; i++ {
		idx := first + i
		if idx < 0 || idx >= len(l.Lines) {
			continue
		}
		text := l.Lines[idx].Text
		if text == "" {
			continue
		}
		line := truncate(text, r.w-2)
		moveTo(b, r.y+i, r.x+max(0, (r.w-visibleWidth(line))/2))
		switch {
		case idx == cur:
			fmt.Fprintf(b, "%s%s%s%s", bold, theme.Fg(t.Accent), line, reset)
		case idx == cur-1 || idx == cur+1:
			fmt.Fprintf(b, "%s%s%s", fg, line, reset)
		default:
			fmt.Fprintf(b, "%s%s%s", muted, line, reset)
		}
	}
}

// drawSpectrum draws the visualiser, inset from the panel border so the bars
// have air around them, with a baseline for the bars to stand on.
func (a *App) drawSpectrum(b *strings.Builder, st player.State, sp *player.Spectrum, r rect) {
	if r.h < 3 || r.w < 12 {
		return
	}
	x0, w0 := r.x+2, r.w-4
	y0, h0 := r.y+1, r.h-2
	if h0 < 2 || w0 < 5 {
		return
	}

	n := BarCount(w0)
	used := n*3 - 1
	off := (w0 - used) / 2

	if sp != nil {
		vals, peaks := sp.BarsWithPeaks(st.Position, n)
		for i, line := range spectrumBars(vals, peaks, h0-1, theme.Current()) {
			moveTo(b, y0+i, x0+off)
			b.WriteString(line)
		}
	}
	// The baseline gives the bars a floor to stand on; without it they hang
	// against the border below them. It is drawn even before anything plays,
	// so the panel reads as an instrument at rest rather than an empty box.
	moveTo(b, y0+h0-1, x0+off)
	fmt.Fprintf(b, "%s%s%s", rule, strings.Repeat("▔", used), reset)
}

// pathExt is filepath.Ext without the import: the extension names the codec
// on the transport's metadata line.
func pathExt(p string) string {
	for i := len(p) - 1; i >= 0 && p[i] != '/'; i-- {
		if p[i] == '.' {
			return p[i:]
		}
	}
	return ""
}

// handleMouse acts on a click, drag or scroll.
func (a *App) handleMouse(ev mouseEvent) bool {
	a.mu.Lock()
	m := a.mode
	list := a.hitList
	prog := a.hitProgress
	sources := a.hitSources
	p := a.player
	n := len(a.songs)
	a.mu.Unlock()

	switch m {
	case modeOverlay:
		a.handleOverlayMouse(ev)
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
				// Rows are looked up rather than computed: the list holds
				// artist headings and spacers as well as songs, so the row
				// under the pointer is not simply top plus offset.
				row := ev.y - list.y
				idx := -1
				a.mu.Lock()
				if row >= 0 && row < len(a.hitPlayRows) {
					idx = a.hitPlayRows[row]
				}
				if idx >= 0 && idx < len(a.songs) {
					a.playCursor = idx
				} else {
					idx = -1
				}
				a.mu.Unlock()
				if idx >= 0 {
					go a.playSelected()
				}
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
