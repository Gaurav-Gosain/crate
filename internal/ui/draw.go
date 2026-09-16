package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/library"
	"golang.org/x/term"
)

const (
	headerRows = 2
	footerRows = 2
	minLogRows = 4
)

func (a *App) draw() {
	w, h, err := term.GetSize(a.fd)
	if err != nil || w < 44 || h < 14 {
		return
	}

	a.mu.Lock()
	m := a.mode
	rows := make([]row, len(a.rows))
	copy(rows, a.rows)
	logs := a.logs
	cursor := a.cursor
	busy, searching := a.busy, a.searching
	started := a.started
	prompt, buf := a.prompt, string(a.buf)
	a.mu.Unlock()

	var b strings.Builder
	// Synchronised update: the terminal presents a whole frame rather than
	// tearing partway through.
	b.WriteString("\x1b[?2026h")
	b.WriteString("\x1b[2J")

	a.drawHeader(&b, w, rows, busy, searching, started)

	if m == modePlay {
		a.drawPlay(&b, w, h)
		a.drawFooter(&b, w, h, prompt, buf)
		b.WriteString("\x1b[?2026l")
		a.tty.WriteString(b.String())
		return
	}

	if m == modeSearch {
		a.drawResults(&b, w, h)
		a.drawFooter(&b, w, h, prompt, buf)
		b.WriteString("\x1b[?2026l")
		a.tty.WriteString(b.String())
		return
	}

	// Vertical budget. The list takes what it needs up to a third of the
	// screen; activity takes the rest, so the pane that grows is the one
	// with something to say.
	const (
		top     = 4 // header, rule, blank, section label
		gapRows = 2 // blank plus rule between the panes
		bottom  = 3 // rule, keys, and the line they sit on
	)
	listMax := max((h-top-gapRows-bottom)/2, 3)
	listRows := clamp(max(len(rows), 1), 1, listMax)

	section(&b, 3, w, "sources", sourcesSummary(rows))
	a.drawList(&b, w, 4, rows, cursor, listRows)

	sep := 4 + listRows
	moveTo(&b, sep, 1)
	clearLine(&b)
	fmt.Fprintf(&b, "%s%s%s", rule, strings.Repeat("─", w), reset)

	a.drawLogs(&b, w, h, sep+1, logs)
	a.drawFooter(&b, w, h, prompt, buf)

	b.WriteString("\x1b[?2026l")
	a.tty.WriteString(b.String())
}

// sourcesSummary is the right hand figure on the sources header: what the set
// looks like as a whole, so the eye does not have to count rows.
func sourcesSummary(rows []row) string {
	if len(rows) == 0 {
		return ""
	}
	var working, done int
	for _, r := range rows {
		switch r.state {
		case running:
			working++
		case succeeded:
			done++
		}
	}
	switch {
	case working > 0:
		return fmt.Sprintf("%s%d working%s %s·%s %s%d%s",
			warn, working, reset, rule, reset, muted, len(rows), reset)
	case done > 0:
		return fmt.Sprintf("%s%d of %d synced%s", muted, done, len(rows), reset)
	}
	return fmt.Sprintf("%s%d source%s%s", muted, len(rows), plural(len(rows)), reset)
}

// drawHeader is the one line that says what the whole program is doing. The
// name sits left, the figures right on the spine every other right-aligned
// element uses.
func (a *App) drawHeader(b *strings.Builder, w int, rows []row, busy, searching bool, started time.Time) {
	moveTo(b, 1, 1)
	clearLine(b)
	fmt.Fprintf(b, " %s%scrate%s", bold, accent, reset)

	var right string
	switch {
	case searching:
		right = accent + "searching" + reset
	case busy:
		done := 0
		for _, r := range rows {
			done += r.done
		}
		el := time.Since(started).Truncate(time.Second)
		right = fmt.Sprintf("%s%d done%s %s·%s %s%s%s",
			ok, done, reset, rule, reset, muted, el, reset)
	default:
		right = muted + "idle" + reset
	}
	rightAt(b, 1, w, right)

	moveTo(b, 2, 1)
	clearLine(b)
	fmt.Fprintf(b, "%s%s%s", rule, strings.Repeat("─", w), reset)
}

// section draws a quiet header: lowercase and muted, so it frames its section
// without competing with it. Bold is spent on rows, not on furniture.
func section(b *strings.Builder, row, w int, label, right string) {
	moveTo(b, row, 1)
	clearLine(b)
	fmt.Fprintf(b, " %s%s%s", muted, label, reset)
	if right != "" {
		rightAt(b, row, w, right)
	}
}

func rightAt(b *strings.Builder, row, w int, s string) {
	col := w - visibleWidth(s)
	if col < 1 {
		col = 1
	}
	moveTo(b, row, col)
	b.WriteString(s)
}

func (a *App) drawList(b *strings.Builder, w, first int, rows []row, cursor, visible int) {
	if len(rows) == 0 {
		moveTo(b, first, 1)
		clearLine(b)
		fmt.Fprintf(b, " %spress %s/%s to search, or %sa%s to add a url%s",
			dim, reset+bold, reset+dim, reset+bold, reset+dim, reset)
		return
	}

	start := 0
	if cursor >= visible {
		start = cursor - visible + 1
	}

	nameW := clamp(w/3, 14, 32)

	for i := 0; i < visible && start+i < len(rows); i++ {
		r := rows[start+i]
		line := first + i
		moveTo(b, line, 1)
		clearLine(b)

		marker, nameStyle := "  ", ""
		if start+i == cursor {
			// The one bold voice, spent on the row a human is looking at.
			marker, nameStyle = accent+"▌ "+reset, bold
		}

		fmt.Fprintf(b, "%s%s%s%s", marker, nameStyle, pad(r.src.Name, nameW), reset)

		lbl, col := r.state.label()
		if r.state == running && r.phase != library.PhaseIdle {
			lbl = r.phase.String()
		}
		fmt.Fprintf(b, "  %s%s%s", col, pad(lbl, 11), reset)

		// Everything after the label is detail, sized by what is left.
		rest := max(w-4-nameW-13, 8)
		fmt.Fprint(b, truncate(rowDetail(r), rest))
	}
}

// rowDetail is the part that says what is actually happening: a bar and the
// track being worked on while running, a count when finished, the reason when
// it failed.
func rowDetail(r row) string {
	switch r.state {
	case running:
		var parts []string
		if r.pct >= 0 {
			parts = append(parts, fmt.Sprintf("%s %s%4.0f%%%s", bar(r.pct, 14), muted, r.pct, reset))
		}
		if r.file != "" {
			parts = append(parts, muted+trimExt(r.file)+reset)
		}
		if r.speed != "" {
			parts = append(parts, dim+r.speed+reset)
		}
		if r.eta != "" {
			parts = append(parts, dim+"eta "+r.eta+reset)
		}
		if r.done > 0 {
			parts = append(parts, fmt.Sprintf("%s%d done%s", ok, r.done, reset))
		}
		return strings.Join(parts, "  ")
	case succeeded:
		if r.done > 0 {
			return fmt.Sprintf("%s%d new track%s%s", muted, r.done, plural(r.done), reset)
		}
		return muted + "up to date" + reset
	case failed:
		return bad + r.note + reset
	}
	return ""
}

// drawLogs fills the activity pane from the bottom up, so the newest line
// always sits just above the footer rule and the empty space, when there is
// little to show, is above the text rather than a void below it.
// drawLogs fills the activity pane from the bottom up, so the newest line sits
// just above the footer rule. The section label travels with the block rather
// than being stranded at the top of an empty pane, so when there is little to
// show the gap reads as deliberate spacing instead of a hole.
func (a *App) drawLogs(b *strings.Builder, w, h, top int, logs []string) {
	last := h - footerRows - 1
	avail := last - top + 1 - 1 // one row reserved for the label
	if avail < 1 {
		return
	}

	show := logs
	if len(show) > avail {
		show = show[len(show)-avail:]
	}
	for r := top; r <= last; r++ {
		moveTo(b, r, 1)
		clearLine(b)
	}

	labelRow := last - len(show)
	if labelRow < top {
		labelRow = top
	}
	section(b, labelRow, w, "activity", "")

	startRow := labelRow + 1
	for i, l := range show {
		moveTo(b, startRow+i, 1)
		// The timestamp is furniture; dim it so the message reads first.
		if len(l) > 9 && l[2] == ':' && l[5] == ':' {
			fmt.Fprintf(b, " %s%s%s %s", rule, l[:8], reset, truncate(muted+l[9:]+reset, w-11))
		} else {
			fmt.Fprintf(b, " %s", truncate(muted+l+reset, w-2))
		}
	}
}

func (a *App) drawFooter(b *strings.Builder, w, h int, prompt, buf string) {
	moveTo(b, h-1, 1)
	clearLine(b)
	fmt.Fprintf(b, "%s%s%s", rule, strings.Repeat("─", w), reset)

	moveTo(b, h, 1)
	clearLine(b)
	if prompt != "" {
		fmt.Fprintf(b, " %s%s%s %s%s%s", accent, prompt, reset, buf, accent, "▏"+reset)
		return
	}
	a.mu.Lock()
	inPlay := a.mode == modePlay
	a.mu.Unlock()

	keys := [][2]string{
		{"/", "search"}, {"s", "sync"}, {"a", "add"},
		{"d", "remove"}, {"r", "rescan"}, {"p", "play"}, {"q", "quit"},
	}
	if inPlay {
		keys = [][2]string{
			{"space", "pause"}, {"enter", "play"}, {"n/b", "next/prev"},
			{"h/l", "seek"}, {"t", "theme"}, {"q", "back"},
		}
	}
	var parts []string
	for _, kv := range keys {
		parts = append(parts, fmt.Sprintf("%s%s%s %s%s%s", bold, kv[0], reset, dim, kv[1], reset))
	}
	fmt.Fprintf(b, " %s", truncate(strings.Join(parts, "   "), w-2))
}

func trimExt(s string) string {
	if i := strings.LastIndexByte(s, '.'); i > 0 {
		return s[:i]
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
