package ui

import (
	"fmt"

	"github.com/Gaurav-Gosain/crate/internal/theme"
	"strings"
	"unicode/utf8"
)

const (
	reset = "\x1b[0m"
	bold  = "\x1b[1m"
	dim   = "\x1b[2m"
)

// The palette is a set of variables rather than constants because it follows
// the selected theme. applyTheme is called once at startup and again whenever
// the theme changes; everything that draws reads these.
var (
	accent = theme.Fg(theme.Current().Accent)
	ok     = theme.Fg(theme.Current().Ok)
	warn   = theme.Fg(theme.Current().Warn)
	bad    = theme.Fg(theme.Current().Bad)
	muted  = theme.Fg(theme.Current().Muted)
	rule   = theme.Fg(theme.Current().Rule)
	fg     = theme.Fg(theme.Current().Fg)
)

// applyTheme refreshes the palette from the active theme.
func applyTheme() {
	t := theme.Current()
	accent = theme.Fg(t.Accent)
	ok = theme.Fg(t.Ok)
	warn = theme.Fg(t.Warn)
	bad = theme.Fg(t.Bad)
	muted = theme.Fg(t.Muted)
	rule = theme.Fg(t.Rule)
	fg = theme.Fg(t.Fg)
}

func moveTo(b *strings.Builder, row, col int) {
	fmt.Fprintf(b, "\x1b[%d;%dH", row, col)
}

func clearLine(b *strings.Builder) { b.WriteString("\x1b[2K") }

// runeWidth reports how many cells a rune occupies.
//
// Counting runes is not the same as counting cells. These filenames are full
// of characters like "｜", the fullwidth vertical line yt-dlp substitutes for
// a pipe, and every one of them takes two columns. Measured as one, a list
// entry that was trimmed to fit still runs past the edge of its panel and
// over the border.
func runeWidth(r rune) int {
	switch {
	case r < 0x1100:
		return 1
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK radicals through Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// visibleWidth counts display cells, skipping ANSI escape sequences.
//
// Measuring len() or rune count on a styled string counts "\x1b[1m" as four
// characters, so a footer that fits gets cut short and, worse, can be sliced
// in the middle of an escape sequence. That renders as stray characters.
func visibleWidth(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += escapeLen(s[i:])
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n += runeWidth(r)
	}
	return n
}

// escapeLen returns the byte length of the escape sequence starting at s[0],
// or 1 if this is a lone ESC. Handles CSI (ESC [ ... final) and OSC/APC style
// sequences terminated by ST or BEL, which covers what this UI emits.
func escapeLen(s string) int {
	if len(s) < 2 {
		return 1
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']', '_', 'P', '^':
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	return 2
}

// truncate cuts to w display cells, appending an ellipsis when it had to cut.
// Escape sequences pass through without consuming width and are never split,
// and a reset is appended if styling was left open.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visibleWidth(s) <= w {
		return s
	}
	limit := w
	if w > 1 {
		limit = w - 1
	}

	var b strings.Builder
	n := 0
	styled := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			l := escapeLen(s[i:])
			b.WriteString(s[i : i+l])
			styled = true
			i += l
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		// Check before writing: a two cell rune written when only one cell is
		// left still overflows, which is how a trimmed line runs over the
		// border it was trimmed to fit inside.
		if n+runeWidth(r) > limit {
			break
		}
		b.WriteRune(r)
		i += size
		n += runeWidth(r)
	}
	if w > 1 {
		b.WriteString("…")
	}
	if styled {
		b.WriteString(reset)
	}
	return b.String()
}

func pad(s string, w int) string {
	n := visibleWidth(s)
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
