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

	a.drawHeader(&b, w, rows, busy, searching, started)

	if m == modeSearch {
		a.drawResults(&b, w, h)
		a.drawFooter(&b, w, h, prompt, buf)
		b.WriteString("\x1b[?2026l")
		a.tty.WriteString(b.String())
		return
	}

	listRows := max(len(rows), 1)
	if maxList := h - headerRows - footerRows - minLogRows - 2; listRows > maxList {
		listRows = maxList
	}

	a.drawList(&b, w, rows, cursor, listRows)
	a.drawLogs(&b, w, h, headerRows+listRows+1, logs)
	a.drawFooter(&b, w, h, prompt, buf)

	b.WriteString("\x1b[?2026l")
	a.tty.WriteString(b.String())
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
		n := 0
		for _, r := range rows {
			if r.state == succeeded {
				n++
			}
		}
		if n > 0 {
			right = fmt.Sprintf("%s%d of %d synced%s", muted, n, len(rows), reset)
		} else {
			right = muted + "idle" + reset
		}
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

func (a *App) drawList(b *strings.Builder, w int, rows []row, cursor, visible int) {
	if len(rows) == 0 {
		moveTo(b, headerRows+1, 1)
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
		line := headerRows + 1 + i
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

func (a *App) drawLogs(b *strings.Builder, w, h, top int, logs []string) {
	section(b, top, w, "activity", "")

	avail := h - top - footerRows - 1
	if avail < 1 {
		return
	}
	start := max(len(logs)-avail, 0)
	for i := 0; i < avail; i++ {
		moveTo(b, top+1+i, 1)
		clearLine(b)
		if start+i >= len(logs) {
			continue
		}
		l := logs[start+i]
		// The timestamp is furniture; dim it so the message reads first.
		if len(l) > 8 && l[2] == ':' && l[5] == ':' {
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
	keys := [][2]string{
		{"/", "search"}, {"s", "sync"}, {"a", "add"},
		{"d", "remove"}, {"r", "rescan"}, {"q", "quit"},
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
