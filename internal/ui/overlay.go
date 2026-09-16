package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Gaurav-Gosain/crate/internal/theme"
)

// overlayItem is one row in a searchable overlay.
type overlayItem struct {
	label  string
	detail string
	// swatch is a set of hex colours drawn beside the label, which is how a
	// theme is chosen by looking at it rather than by reading its name.
	swatch []string
	run    func()
}

// overlayState is a searchable list floating over the interface, used for both
// the command palette and the theme picker. One implementation because they
// differ only in what fills the list and what picking a row does.
type overlayState struct {
	title  string
	hint   string
	all    []overlayItem
	shown  []overlayItem
	filter []rune
	cursor int
	top    int
	box    rect
	rows   rect
	// preview runs as the cursor moves, so a theme can be seen before it is
	// chosen. Picking makes it permanent; cancelling puts back what was there.
	preview func(overlayItem)
	cancel  func()
}

// filterItems narrows the list. Matching is a subsequence test rather than a
// substring one, so "nwp" finds "now playing" the way a fuzzy finder would.
func filterItems(all []overlayItem, filter []rune) []overlayItem {
	if len(filter) == 0 {
		return all
	}
	want := strings.ToLower(string(filter))
	var out []overlayItem
	for _, it := range all {
		if subsequence(strings.ToLower(it.label+" "+it.detail), want) {
			out = append(out, it)
		}
	}
	return out
}

func subsequence(hay, needle string) bool {
	i := 0
	for _, r := range hay {
		if i < len(needle) && rune(needle[i]) == r {
			i++
		}
	}
	return i == len(needle)
}

// openOverlay puts a searchable list on screen.
func (a *App) openOverlay(ov *overlayState) {
	ov.shown = filterItems(ov.all, ov.filter)
	a.mu.Lock()
	a.ov = ov
	a.prevMode = a.mode
	a.mode = modeOverlay
	a.mu.Unlock()
	a.redraw()
}

// closeOverlay puts the interface back as it was.
func (a *App) closeOverlay(cancelled bool) {
	a.mu.Lock()
	ov := a.ov
	a.ov = nil
	a.mode = a.prevMode
	a.mu.Unlock()
	if cancelled && ov != nil && ov.cancel != nil {
		ov.cancel()
	}
	a.redraw()
}

// handleOverlayKey processes a key while an overlay is open.
func (a *App) handleOverlayKey(c byte) bool {
	a.mu.Lock()
	ov := a.ov
	a.mu.Unlock()
	if ov == nil {
		return false
	}

	switch c {
	case 27: // escape
		a.closeOverlay(true)
		return false
	case 3: // ctrl-c closes the overlay rather than the app, which is what
		// every other program with a palette does.
		a.closeOverlay(true)
		return false
	case '\r', '\n':
		a.mu.Lock()
		var pick *overlayItem
		if ov.cursor >= 0 && ov.cursor < len(ov.shown) {
			it := ov.shown[ov.cursor]
			pick = &it
		}
		a.mu.Unlock()
		a.closeOverlay(false)
		if pick != nil && pick.run != nil {
			pick.run()
		}
		return false
	case 127, 8: // backspace
		a.mu.Lock()
		if len(ov.filter) > 0 {
			ov.filter = ov.filter[:len(ov.filter)-1]
		}
		ov.shown = filterItems(ov.all, ov.filter)
		ov.cursor, ov.top = 0, 0
		a.mu.Unlock()
		a.previewCurrent()
	case keyUp, 16: // up, ctrl-p
		a.moveOverlay(-1)
	case keyDown, 14: // down, ctrl-n
		a.moveOverlay(1)
	case keyPgUp:
		a.moveOverlay(-8)
	case keyPgDn:
		a.moveOverlay(8)
	default:
		if c >= 32 && c < 127 {
			a.mu.Lock()
			ov.filter = append(ov.filter, rune(c))
			ov.shown = filterItems(ov.all, ov.filter)
			ov.cursor, ov.top = 0, 0
			a.mu.Unlock()
			a.previewCurrent()
		}
	}
	a.redraw()
	return false
}

func (a *App) moveOverlay(d int) {
	a.mu.Lock()
	ov := a.ov
	if ov == nil || len(ov.shown) == 0 {
		a.mu.Unlock()
		return
	}
	ov.cursor += d
	if ov.cursor < 0 {
		ov.cursor = 0
	}
	if ov.cursor >= len(ov.shown) {
		ov.cursor = len(ov.shown) - 1
	}
	a.mu.Unlock()
	a.previewCurrent()
}

// previewCurrent shows what the highlighted row would do, where that is
// something worth seeing before committing to it.
func (a *App) previewCurrent() {
	a.mu.Lock()
	ov := a.ov
	var it *overlayItem
	if ov != nil && ov.cursor >= 0 && ov.cursor < len(ov.shown) {
		v := ov.shown[ov.cursor]
		it = &v
	}
	a.mu.Unlock()
	if ov != nil && ov.preview != nil && it != nil {
		ov.preview(*it)
	}
}

// drawOverlay renders the floating list.
func (a *App) drawOverlay(b *strings.Builder, w, h int) {
	a.mu.Lock()
	ov := a.ov
	a.mu.Unlock()
	if ov == nil {
		return
	}

	bw := w * 2 / 3
	if bw > 68 {
		bw = 68
	}
	if bw < 30 {
		bw = 30
	}
	visible := 12
	if visible > h-10 {
		visible = h - 10
	}
	if visible < 3 {
		visible = 3
	}
	bh := visible + 5
	bx := (w-bw)/2 + 1
	by := (h-bh)/2 + 1

	box := rect{bx, by, bw, bh}
	// Clear the area first: the overlay floats over the interface, and
	// without this the text underneath shows through the gaps.
	for i := 0; i < bh; i++ {
		moveTo(b, by+i, bx)
		b.WriteString(strings.Repeat(" ", bw))
	}
	panel(b, box, ov.title, true)
	in := box.inner()

	// Filter line.
	moveTo(b, in.y, in.x+1)
	shown := string(ov.filter)
	if shown == "" {
		fmt.Fprintf(b, "%s%s type to filter%s", muted, "›", reset)
	} else {
		fmt.Fprintf(b, "%s›%s %s%s%s%s", accent, reset, fg, truncate(shown, in.w-4), reset, accent+"▏"+reset)
	}
	moveTo(b, in.y+1, in.x)
	fmt.Fprintf(b, "%s%s%s", rule, strings.Repeat("─", in.w), reset)

	listY := in.y + 2
	a.mu.Lock()
	if ov.cursor < ov.top {
		ov.top = ov.cursor
	}
	if ov.cursor >= ov.top+visible {
		ov.top = ov.cursor - visible + 1
	}
	top := ov.top
	ov.box, ov.rows = box, rect{in.x, listY, in.w, visible}
	a.mu.Unlock()

	if len(ov.shown) == 0 {
		moveTo(b, listY, in.x+1)
		fmt.Fprintf(b, "%sno matches%s", muted, reset)
	}

	for i := 0; i < visible && top+i < len(ov.shown); i++ {
		it := ov.shown[top+i]
		moveTo(b, listY+i, in.x)
		selected := top+i == ov.cursor

		// The selected row is built without any styling of its own. Every
		// reset inside a line clears the background as well as the colour,
		// so a row with a muted suffix ends its highlight where that suffix
		// starts and the selection looks half painted.
		var plain strings.Builder
		if len(it.swatch) > 0 {
			plain.WriteString(strings.Repeat("●", len(it.swatch)) + " ")
		}
		plain.WriteString(it.label)
		if it.detail != "" {
			plain.WriteString("  " + it.detail)
		}
		text := truncate(plain.String(), in.w-2)

		if selected {
			t := theme.Current()
			pad := max(0, in.w-2-visibleWidth(text))
			moveTo(b, listY+i, in.x)
			fmt.Fprintf(b, "%s%s %s%s%s", theme.Bg(t.Accent), theme.Ink(t.Accent),
				text, strings.Repeat(" ", pad+1), reset)
			continue
		}

		var row strings.Builder
		if len(it.swatch) > 0 {
			for _, hex := range it.swatch {
				row.WriteString(theme.Fg(hex) + "●")
			}
			row.WriteString(reset + " ")
		}
		row.WriteString(fg + it.label + reset)
		if it.detail != "" {
			row.WriteString("  " + muted + it.detail + reset)
		}
		fmt.Fprintf(b, " %s", truncate(row.String(), in.w-2))
	}

	// Footer hint.
	moveTo(b, in.y+in.h-1, in.x+1)
	fmt.Fprintf(b, "%s%s%s", muted, truncate(ov.hint, in.w-2), reset)
}

// handleOverlayMouse lets the overlay be driven with the pointer.
func (a *App) handleOverlayMouse(ev mouseEvent) {
	a.mu.Lock()
	ov := a.ov
	a.mu.Unlock()
	if ov == nil {
		return
	}
	switch ev.kind {
	case mouseWheelUp:
		a.moveOverlay(-2)
	case mouseWheelDown:
		a.moveOverlay(2)
	case mousePress:
		a.mu.Lock()
		rows, top := ov.rows, ov.top
		inBox := ov.box.contains(ev.x, ev.y)
		a.mu.Unlock()
		if !inBox {
			// Clicking away from a palette dismisses it.
			a.closeOverlay(true)
			return
		}
		if rows.contains(ev.x, ev.y) {
			idx := top + (ev.y - rows.y)
			a.mu.Lock()
			if idx >= 0 && idx < len(ov.shown) {
				ov.cursor = idx
			}
			a.mu.Unlock()
			a.previewCurrent()
			a.handleOverlayKey('\r')
		}
	}
}

// marqueePhase remembers when the current label appeared, so the pause at the
// start of a pass is measured from then.
//
// The phase has to belong to the label rather than to the clock. Deriving it
// from wall time, as this first did, means the hold only lands at the start of
// some global cycle: select a row part way through one and its title is
// already mid scroll, which is exactly the complaint that the pause did not
// work. Keying on the text resets it whenever the selection changes.
var marqueePhase struct {
	mu    sync.Mutex
	key   string
	since time.Time
}

// marquee scrolls a string that does not fit, so a long title can still be
// read in full when it is the one selected.
//
// It holds still at the start of each pass. A label that slides continuously
// is hard to read; one that sits still long enough to be taken in, travels,
// and then starts again can be.
func marquee(s string, w int, now time.Time) string {
	if w <= 0 {
		return ""
	}
	full := visibleWidth(s)
	if full <= w {
		return s
	}

	marqueePhase.mu.Lock()
	if marqueePhase.key != s {
		marqueePhase.key = s
		marqueePhase.since = now
	}
	elapsed := now.Sub(marqueePhase.since)
	marqueePhase.mu.Unlock()

	const (
		hold = 2500 * time.Millisecond // still, long enough to read the start
		step = 320 * time.Millisecond  // then a column at a time
	)
	if elapsed < hold {
		return sliceCells(s, 0, w)
	}

	gap := 6
	period := full + gap
	off := int((elapsed-hold)/step) % period
	padded := s + strings.Repeat(" ", gap) + s
	return sliceCells(padded, off, w)
}

// sliceCells returns w cells of s starting at cell offset off, keeping any
// styling intact and never splitting a wide rune.
func sliceCells(s string, off, w int) string {
	var b strings.Builder
	cell := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			l := escapeLen(s[i:])
			b.WriteString(s[i : i+l])
			i += l
			continue
		}
		r, size := utf8DecodeRune(s[i:])
		rw := runeWidth(r)
		if cell >= off && cell+rw <= off+w {
			b.WriteRune(r)
		}
		cell += rw
		i += size
		if cell >= off+w {
			break
		}
	}
	return b.String()
}

// utf8DecodeRune is a tiny wrapper so this file does not import unicode/utf8
// under a name that shadows the unicode import used above.
func utf8DecodeRune(s string) (rune, int) {
	for i, r := range s {
		_ = i
		return r, len(string(r))
	}
	return unicode.ReplacementChar, 1
}
