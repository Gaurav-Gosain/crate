package ui

import (
	"testing"

	"github.com/Gaurav-Gosain/crate/internal/library"
)

// paneCombos is every way the four panes can be switched, so the layout is
// asserted for each of them rather than only the default.
func paneCombos() []paneSet {
	var out []paneSet
	for i := 0; i < 16; i++ {
		out = append(out, paneSet{
			art:      i&1 != 0,
			vinyl:    i&2 != 0,
			spectrum: i&4 != 0,
			lyrics:   i&8 != 0,
		})
	}
	return out
}

// onScreen fails the test when a rectangle leaves the content area. A border
// past the right edge wraps onto the next row, which shears the whole frame.
func onScreen(t *testing.T, name string, r rect, w, top, contentH int) {
	t.Helper()
	if r.w == 0 || r.h == 0 {
		return
	}
	if r.x < 1 || r.y < top || r.x+r.w-1 > w || r.y+r.h > top+contentH {
		t.Fatalf("%s %+v leaves the screen (w=%d top=%d contentH=%d)",
			name, r, w, top, contentH)
	}
}

// overlaps reports whether two non-empty rectangles share a cell.
func overlaps(a, b rect) bool {
	if a.w == 0 || a.h == 0 || b.w == 0 || b.h == 0 {
		return false
	}
	return a.x < b.x+b.w && b.x < a.x+a.w && a.y < b.y+b.h && b.y < a.y+a.h
}

// Every pane the layout emits has to be fully on screen and clear of its
// neighbours, at every size the drawing accepts and for every combination of
// panes, or the frame shears the moment a toggle or a resize hits the wrong
// pair of numbers.
func TestPlayLayoutStaysOnScreen(t *testing.T) {
	for _, ps := range paneCombos() {
		for w := 60; w <= 300; w += 4 {
			for h := 20; h <= 60; h += 2 {
				top := 3
				contentH := h - top - 1
				lay := playLayout(w, top, contentH, ps)

				onScreen(t, "list", lay.list, w, top, contentH)
				onScreen(t, "now", lay.now, w, top, contentH)
				onScreen(t, "lyrics", lay.lyr, w, top, contentH)
				onScreen(t, "spectrum", lay.spec, w, top, contentH)

				panels := []struct {
					name string
					r    rect
				}{
					{"list", lay.list}, {"now", lay.now},
					{"lyrics", lay.lyr}, {"spectrum", lay.spec},
				}
				for i := range panels {
					for j := i + 1; j < len(panels); j++ {
						if overlaps(panels[i].r, panels[j].r) {
							t.Fatalf("w=%d h=%d panes=%+v: %s %+v overlaps %s %+v",
								w, h, ps, panels[i].name, panels[i].r,
								panels[j].name, panels[j].r)
						}
					}
				}

				// The right column tiles exactly: a gap would be dead screen
				// and an overlap would be a shear.
				if lay.now.h+lay.lyr.h+lay.spec.h != contentH {
					t.Fatalf("w=%d h=%d panes=%+v: column covers %d of %d rows",
						w, h, ps, lay.now.h+lay.lyr.h+lay.spec.h, contentH)
				}
				if lay.lyr.h > 0 && lay.lyr.y != lay.now.y+lay.now.h {
					t.Fatalf("w=%d h=%d: lyrics do not sit under the now panel", w, h)
				}
				if lay.spec.h > 0 && lay.spec.y != lay.now.y+lay.now.h+lay.lyr.h {
					t.Fatalf("w=%d h=%d: spectrum is not the bottom pane", w, h)
				}

				// A pane that is off must never appear.
				if !ps.art && lay.art.w > 0 {
					t.Fatalf("w=%d h=%d: art drawn while off", w, h)
				}
				if !ps.vinyl && lay.vinyl.w > 0 {
					t.Fatalf("w=%d h=%d: vinyl drawn while off", w, h)
				}
				if !ps.lyrics && lay.lyr.h > 0 {
					t.Fatalf("w=%d h=%d: lyrics drawn while off", w, h)
				}
				if !ps.spectrum && lay.spec.h > 0 {
					t.Fatalf("w=%d h=%d: spectrum drawn while off", w, h)
				}

				// The squares: 2:1 so they render square on terminal cells,
				// inside the now panel, clear of each other, and clear of
				// the transport row at the panel's inner bottom.
				inner := lay.now.inner()
				for _, s := range []struct {
					name string
					r    rect
				}{{"art", lay.art}, {"vinyl", lay.vinyl}} {
					if s.r.w == 0 {
						continue
					}
					if s.r.w != 2*s.r.h {
						t.Fatalf("w=%d h=%d: %s %+v is not square on a 2:1 cell",
							w, h, s.name, s.r)
					}
					if s.r.x < inner.x || s.r.y < inner.y ||
						s.r.x+s.r.w > inner.x+inner.w ||
						s.r.y+s.r.h > inner.y+inner.h-1 {
						t.Fatalf("w=%d h=%d: %s %+v leaves the now panel %+v",
							w, h, s.name, s.r, inner)
					}
				}
				if overlaps(lay.art, lay.vinyl) {
					t.Fatalf("w=%d h=%d: art %+v overlaps vinyl %+v",
						w, h, lay.art, lay.vinyl)
				}

				// On a roomy terminal nothing that is on may be dropped:
				// that is the whole point of the layout.
				if w >= 110 && h >= 30 {
					if ps.art && lay.art.w == 0 {
						t.Fatalf("w=%d h=%d: art dropped on a large screen", w, h)
					}
					if ps.vinyl && lay.vinyl.w == 0 {
						t.Fatalf("w=%d h=%d: vinyl dropped on a large screen", w, h)
					}
					if ps.lyrics && lay.lyr.h == 0 {
						t.Fatalf("w=%d h=%d: lyrics dropped on a large screen", w, h)
					}
					if ps.spectrum && lay.spec.h == 0 {
						t.Fatalf("w=%d h=%d: spectrum dropped on a large screen", w, h)
					}
				}
			}
		}
	}
}

func TestBuildPlayRowsGroupsByArtist(t *testing.T) {
	songs := []library.Song{
		{Rel: "a/1", Artist: "alpha", Title: "one"},
		{Rel: "a/2", Artist: "alpha", Title: "two"},
		{Rel: "b/1", Artist: "beta", Title: "three"},
		{Rel: "x", Artist: "", Title: "four"},
	}
	rows, pos := buildPlayRows(songs)

	seen := make(map[int]bool)
	lastHeader := ""
	for i, r := range rows {
		if r.song >= 0 {
			if seen[r.song] {
				t.Fatalf("song %d appears twice", r.song)
			}
			seen[r.song] = true
			if pos[r.song] != i {
				t.Fatalf("song %d recorded at row %d but found at %d", r.song, pos[r.song], i)
			}
			if lastHeader == "" {
				t.Fatalf("song %d has no heading above it", r.song)
			}
			continue
		}
		if r.header != "" {
			lastHeader = r.header
		}
	}
	for i := range songs {
		if !seen[i] {
			t.Fatalf("song %d never shown", i)
		}
	}
	// The empty artist still gets a heading, or its songs would appear to
	// belong to whoever came before them.
	found := false
	for _, r := range rows {
		if r.header == "unknown artist" {
			found = true
		}
	}
	if !found {
		t.Fatal("no heading for songs with no artist")
	}
}

func TestWrapCellsRespectsWidthAndLineCount(t *testing.T) {
	cases := []struct {
		in       string
		w, lines int
	}{
		{"a plain title", 8, 2},
		{"a plain title that runs on for quite some time", 12, 2},
		{"word", 10, 1},
		{"ａ　ｗｉｄｅ　ｔｉｔｌｅ", 6, 2}, // fullwidth runes are two cells each
		{"an-unbroken-word-longer-than-the-width", 10, 2},
	}
	for _, c := range cases {
		got := wrapCells(c.in, c.w, c.lines)
		if len(got) > c.lines {
			t.Fatalf("wrapCells(%q, %d, %d) gave %d lines", c.in, c.w, c.lines, len(got))
		}
		for _, ln := range got {
			if visibleWidth(ln) > c.w {
				t.Fatalf("wrapCells(%q, %d, %d): line %q is %d cells",
					c.in, c.w, c.lines, ln, visibleWidth(ln))
			}
		}
	}
}

// A title that does not fit its line budget must end in an ellipsis, so the
// reader knows there was more, and everything that fits must survive.
func TestWrapCellsMarksTheCut(t *testing.T) {
	got := wrapCells("one two three four five six seven", 9, 2)
	if len(got) != 2 {
		t.Fatalf("got %d lines", len(got))
	}
	last := got[len(got)-1]
	if last[len(last)-len("…"):] != "…" {
		t.Fatalf("cut line %q does not end in an ellipsis", last)
	}
}
