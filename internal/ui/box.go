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
	b.WriteString(topEdge(r.w, title, edge))

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

// topEdge builds a panel's top border with the title inlaid.
//
// It is a separate function so its width can be asserted. The edge has to come
// out exactly w cells wide: one column short and the corner sits inside the
// right edge, which makes the box look crooked next to its neighbours.
func topEdge(w int, title, edge string) string {
	if w < 4 {
		return ""
	}
	var b strings.Builder
	b.WriteString(edge + "╭")
	if title == "" {
		b.WriteString(strings.Repeat("─", w-2))
	} else {
		t := truncate(title, w-6)
		// Three cells go out before the fill: the leading rule and a space
		// either side of the title.
		used := visibleWidth(t) + 3
		fmt.Fprintf(&b, "─%s %s%s%s %s", reset, fg, t, reset, edge)
		if rest := w - 2 - used; rest > 0 {
			b.WriteString(strings.Repeat("─", rest))
		}
	}
	b.WriteString("╮" + reset)
	return b.String()
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
			// The travelled part is heavy and the rest light, so the track
			// reads as a position at a glance rather than as a solid rule.
			b.WriteString("─")
		}
	}
	b.WriteString(reset)
	return b.String()
}
