package ui

import (
	"fmt"
	"strings"

	"golang.org/x/term"
)

const (
	headerRows = 2
	footerRows = 2
	minLogRows = 3
)

func (a *App) draw() {
	w, h, err := term.GetSize(a.fd)
	if err != nil || w < 40 || h < 12 {
		return
	}

	a.mu.Lock()
	m := a.mode
	rows := make([]row, len(a.rows))
	copy(rows, a.rows)
	logs := a.logs
	cursor := a.cursor
	busy := a.busy
	prompt, buf := a.prompt, string(a.buf)
	a.mu.Unlock()

	var b strings.Builder
	// Synchronised update: the terminal presents the whole frame at once
	// rather than tearing partway through a redraw.
	b.WriteString("\x1b[?2026h")

	a.drawHeader(&b, w, busy)

	if m == modeSearch {
		a.drawResults(&b, w, h)
		a.drawFooter(&b, w, h, prompt, buf)
		b.WriteString("\x1b[?2026l")
		a.tty.WriteString(b.String())
		return
	}

	listRows := len(rows)
	if listRows == 0 {
		listRows = 1
	}
	maxList := h - headerRows - footerRows - minLogRows - 1
	if listRows > maxList {
		listRows = maxList
	}

	a.drawList(&b, w, rows, cursor, listRows)

	logTop := headerRows + listRows + 1
	a.drawLogs(&b, w, h, logTop, logs)
	a.drawFooter(&b, w, h, prompt, buf)

	b.WriteString("\x1b[?2026l")
	a.tty.WriteString(b.String())
}

func (a *App) drawHeader(b *strings.Builder, w int, busy bool) {
	moveTo(b, 1, 1)
	clearLine(b)
	fmt.Fprintf(b, " %s%scrate%s %s%s%s", bold, accent, reset, dim, "music, kept in sync", reset)

	a.mu.Lock()
	searching := a.searching
	a.mu.Unlock()

	state := "idle"
	col := muted
	switch {
	case searching:
		// A search resolves each entry through yt-dlp and can run for the
		// better part of a minute, so say so rather than looking hung.
		state, col = "searching", accent
	case busy:
		state, col = "working", warn
	}
	right := fmt.Sprintf("%s%s%s ", col, state, reset)
	moveTo(b, 1, w-len(state)-1)
	b.WriteString(right)

	moveTo(b, 2, 1)
	clearLine(b)
	fmt.Fprintf(b, "%s%s%s", rule, strings.Repeat("─", w), reset)
}

func (a *App) drawList(b *strings.Builder, w int, rows []row, cursor, visible int) {
	if len(rows) == 0 {
		moveTo(b, headerRows+1, 1)
		clearLine(b)
		fmt.Fprintf(b, " %sno sources yet. press a to add one.%s", dim, reset)
		return
	}

	// Keep the cursor in view without jumping the whole list around.
	start := 0
	if cursor >= visible {
		start = cursor - visible + 1
	}

	nameW := w / 3
	if nameW > 34 {
		nameW = 34
	}
	if nameW < 12 {
		nameW = 12
	}

	for i := 0; i < visible && start+i < len(rows); i++ {
		r := rows[start+i]
		line := headerRows + 1 + i
		moveTo(b, line, 1)
		clearLine(b)

		marker, nameCol := "  ", ""
		if start+i == cursor {
			marker = accent + "▌ " + reset
			nameCol = bold
		}

		label, lcol := r.state.label()
		detail := r.detail
		if r.state == running && r.pct >= 0 {
			detail = fmt.Sprintf("%s %5.1f%%", bar(r.pct, 16), r.pct)
		}

		fmt.Fprintf(b, "%s%s%s%s  %s%-8s%s  %s",
			marker, nameCol, pad(r.src.Name, nameW), reset,
			lcol, label, reset,
			truncate(detail, max(w-nameW-16, 4)))
	}
}

func (a *App) drawLogs(b *strings.Builder, w, h, top int, logs []string) {
	moveTo(b, top, 1)
	clearLine(b)
	fmt.Fprintf(b, "%s%s%s", rule, strings.Repeat("─", w), reset)

	avail := h - top - footerRows
	if avail < 1 {
		return
	}
	start := 0
	if len(logs) > avail {
		start = len(logs) - avail
	}
	for i := 0; i < avail; i++ {
		moveTo(b, top+1+i, 1)
		clearLine(b)
		if start+i < len(logs) {
			fmt.Fprintf(b, " %s%s%s", muted, truncate(logs[start+i], w-2), reset)
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
		fmt.Fprintf(b, " %s%s%s %s%s", accent, prompt, reset, buf, "▎")
		return
	}
	keys := []struct{ k, d string }{
		{"/", "search"}, {"s", "sync all"}, {"enter", "sync one"},
		{"a", "add url"}, {"d", "remove"}, {"q", "quit"},
	}
	var parts []string
	for _, kv := range keys {
		parts = append(parts, fmt.Sprintf("%s%s%s %s%s%s", bold, kv.k, reset, dim, kv.d, reset))
	}
	fmt.Fprintf(b, " %s", truncate(strings.Join(parts, "  "), w-2))
}
