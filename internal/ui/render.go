package ui

import (
	"fmt"
	"strings"
)

// A small palette, kept to what a 256-colour terminal reliably shows.
const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	accent = "\x1b[38;5;75m"
	ok     = "\x1b[38;5;114m"
	warn   = "\x1b[38;5;179m"
	bad    = "\x1b[38;5;174m"
	muted  = "\x1b[38;5;244m"
	rule   = "\x1b[38;5;238m"
)

func moveTo(b *strings.Builder, row, col int) {
	fmt.Fprintf(b, "\x1b[%d;%dH", row, col)
}

func clearLine(b *strings.Builder) { b.WriteString("\x1b[2K") }

// truncate cuts to w display cells, appending an ellipsis when it had to cut.
// It counts runes rather than bytes so multi-byte titles are not sliced apart.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func pad(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-n)
}

// bar draws a determinate progress bar. A negative pct renders as an empty
// track, which is how a step with no reported percentage shows up.
func bar(pct float64, w int) string {
	if w <= 2 {
		return ""
	}
	inner := w - 2
	filled := 0
	if pct >= 0 {
		filled = int(pct / 100 * float64(inner))
		if filled > inner {
			filled = inner
		}
	}
	return "│" + accent + strings.Repeat("━", filled) + rule +
		strings.Repeat("─", inner-filled) + reset + "│"
}
