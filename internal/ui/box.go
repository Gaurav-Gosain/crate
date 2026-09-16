package ui

import (
	"fmt"
	"strings"
)

// rect is a region of the screen, in one-based terminal coordinates.
type rect struct {
	x, y, w, h int
}

// contains reports whether a cell is inside the rectangle.
func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// inner returns the area inside a panel's border.
func (r rect) inner() rect {
	return rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
}

// panel draws a rounded box with a title in its top edge.
//
// The interface used to draw everything against bare screen edges, which left
// no way to tell where one part stopped and the next began. A border and a
// title say what a region is without spending a line on a heading.
func panel(b *strings.Builder, r rect, title string, focused bool) {
	if r.w < 4 || r.h < 2 {
		return
	}
	edge := rule
	if focused {
		edge = accent
	}

	// Top edge, with the title inlaid.
	moveTo(b, r.y, r.x)
	b.WriteString(edge + "╭")
	if title != "" {
		t := truncate(title, r.w-6)
		fmt.Fprintf(b, "─%s %s%s%s %s", reset, fg, t, reset, edge)
		used := visibleWidth(t) + 4
		if used < r.w-2 {
			b.WriteString(strings.Repeat("─", r.w-2-used))
		}
	} else {
		b.WriteString(strings.Repeat("─", r.w-2))
	}
	b.WriteString("╮" + reset)

	// Sides.
	for i := 1; i < r.h-1; i++ {
		moveTo(b, r.y+i, r.x)
		b.WriteString(edge + "│" + reset)
		moveTo(b, r.y+i, r.x+r.w-1)
		b.WriteString(edge + "│" + reset)
	}

	// Bottom edge.
	moveTo(b, r.y+r.h-1, r.x)
	b.WriteString(edge + "╰" + strings.Repeat("─", r.w-2) + "╯" + reset)
}

// progress draws a slider: a filled track with a handle at the current point.
// It is drawn rather than reusing bar() because it has to be clickable, and a
// handle shows where a click would land.
func progress(pct float64, w int) string {
	if w < 4 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	pos := int(pct * float64(w-1))
	var b strings.Builder
	b.WriteString(accent)
	for i := 0; i < w; i++ {
		switch {
		case i == pos:
			b.WriteString("●")
		case i < pos:
			b.WriteString("━")
		default:
			if i == pos+1 {
				b.WriteString(reset + rule)
			}
			b.WriteString("━")
		}
	}
	b.WriteString(reset)
	return b.String()
}
