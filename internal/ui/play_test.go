package ui

import (
	"testing"

	"github.com/Gaurav-Gosain/crate/internal/library"
)

// The three panels have to tile the content area and stay on the screen at
// every size the drawing accepts, or a border lands past the right edge and
// the terminal wraps it onto the next row, which shears the whole frame.
func TestPlayLayoutStaysOnScreen(t *testing.T) {
	for w := 44; w <= 300; w += 7 {
		for h := 14; h <= 90; h += 5 {
			top := 3
			contentH := h - top - 1
			lay := playLayout(w, top, contentH)

			if lay.list.x < 1 {
				t.Fatalf("w=%d h=%d: list starts at column %d", w, h, lay.list.x)
			}
			if lay.list.x+lay.list.w >= lay.now.x {
				t.Fatalf("w=%d h=%d: list and now panel overlap", w, h)
			}
			if right := lay.now.x + lay.now.w - 1; right > w {
				t.Fatalf("w=%d h=%d: right panel ends at column %d of %d", w, h, right, w)
			}
			if lay.now.h+lay.spec.h != contentH {
				t.Fatalf("w=%d h=%d: panels cover %d of %d rows",
					w, h, lay.now.h+lay.spec.h, contentH)
			}
			if lay.spec.h > 0 && lay.spec.y != lay.now.y+lay.now.h {
				t.Fatalf("w=%d h=%d: spectrum does not sit under the now panel", w, h)
			}
		}
	}
}

// The artwork is square and terminal cells are about twice as tall as they
// are wide, so the art must always come out at twice as many columns as rows
// and must fit inside the panel it was measured for.
func TestArtRectIsSquareAndFits(t *testing.T) {
	var a App
	for w := 16; w <= 200; w += 3 {
		for h := 5; h <= 40; h += 2 {
			r := rect{5, 4, w, h}
			ar := a.artRect(r)
			if ar.w == 0 {
				continue
			}
			if ar.w != 2*ar.h {
				t.Fatalf("inner %dx%d: art %dx%d is not square on a 2:1 cell",
					w, h, ar.w, ar.h)
			}
			if ar.x < r.x || ar.y < r.y || ar.x+ar.w > r.x+r.w || ar.y+ar.h > r.y+r.h {
				t.Fatalf("inner %+v: art %+v sticks out", r, ar)
			}
			// The blank row and the transport row under the art must fit too.
			if ar.y+ar.h > r.y+r.h-2 {
				t.Fatalf("inner %+v: art %+v leaves no room for the transport", r, ar)
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
