package ui

import (
	"testing"
	"time"
)

func TestVisibleWidthIgnoresEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"\x1b[1mabc\x1b[0m", 3},
		{"\x1b[38;5;75mhello\x1b[0m world", 11},
		{"\x1b[1ms\x1b[0m \x1b[2mquit\x1b[0m", 6},
		{"héllo", 5},
	}
	for _, c := range cases {
		if got := visibleWidth(c.in); got != c.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateNeverSplitsAnEscape(t *testing.T) {
	styled := "\x1b[1mabcdef\x1b[0m"
	got := truncate(styled, 4)
	if visibleWidth(got) > 4 {
		t.Errorf("truncate produced %d visible cells, want <= 4: %q", visibleWidth(got), got)
	}
	// A split escape would leave a bare ESC with no final byte.
	for i := 0; i < len(got); i++ {
		if got[i] == 0x1b && escapeLen(got[i:]) > len(got)-i {
			t.Errorf("truncate split an escape sequence: %q", got)
		}
	}
}

func TestTruncateLeavesShortStringsAlone(t *testing.T) {
	in := "\x1b[1mshort\x1b[0m"
	if got := truncate(in, 40); got != in {
		t.Errorf("truncate mangled a string that already fits: %q", got)
	}
}

func TestPadCountsVisibleCells(t *testing.T) {
	got := pad("\x1b[1mab\x1b[0m", 6)
	if visibleWidth(got) != 6 {
		t.Errorf("pad gave %d visible cells, want 6: %q", visibleWidth(got), got)
	}
}

// yt-dlp replaces characters a filesystem will not take, and its substitutes
// are fullwidth: "｜" occupies two columns. Counted as one, a line trimmed to
// fit a panel still runs over the border.
func TestVisibleWidthCountsFullwidthAsTwo(t *testing.T) {
	if got := visibleWidth("｜"); got != 2 {
		t.Fatalf("fullwidth bar measured %d cells, want 2", got)
	}
	if got := visibleWidth("ab"); got != 2 {
		t.Fatalf("two ascii letters measured %d", got)
	}
}

func TestTruncateRespectsCellWidth(t *testing.T) {
	s := "aa｜｜bb"
	for _, w := range []int{3, 4, 5, 6, 7} {
		got := truncate(s, w)
		if visibleWidth(got) > w {
			t.Fatalf("truncate(%q, %d) = %q, %d cells wide", s, w, got, visibleWidth(got))
		}
	}
}

func TestTruncateNeverSplitsAWideRune(t *testing.T) {
	// Trimming to an odd width in the middle of a wide rune must drop it
	// rather than emit half of it.
	got := truncate("a｜b", 2)
	if visibleWidth(got) > 2 {
		t.Fatalf("got %q at %d cells", got, visibleWidth(got))
	}
}

// The top edge of a panel has to be exactly as wide as the panel, or its
// corner sits inside the right edge and neighbouring boxes do not line up.
func TestTopEdgeIsExactlyPanelWidth(t *testing.T) {
	for _, title := range []string{"", "library", "now playing", "library  905", "a title far longer than the panel"} {
		for _, w := range []int{10, 20, 40, 80, 150} {
			got := visibleWidth(topEdge(w, title, ""))
			if got != w {
				t.Fatalf("title=%q w=%d: edge measured %d cells", title, w, got)
			}
		}
	}
}

// A title that fits is left alone; one that does not scrolls, and every frame
// of the scroll has to be exactly the width it was given or it disturbs the
// layout around it.
func TestMarqueeLeavesShortTitlesAlone(t *testing.T) {
	if got := marquee("short", 20, time.Unix(0, 0)); got != "short" {
		t.Fatalf("got %q", got)
	}
}

func TestMarqueeNeverExceedsItsWidth(t *testing.T) {
	long := "a very long track title that will not fit in the panel at all"
	for ms := int64(0); ms < 20000; ms += 137 {
		got := marquee(long, 18, time.UnixMilli(ms))
		if w := visibleWidth(got); w > 18 {
			t.Fatalf("at %dms the marquee was %d cells wide: %q", ms, w, got)
		}
	}
}

func TestMarqueeActuallyMoves(t *testing.T) {
	long := "a very long track title that will not fit"
	first := marquee(long, 12, time.UnixMilli(0))
	moved := false
	for ms := int64(0); ms < 8000; ms += 180 {
		if marquee(long, 12, time.UnixMilli(ms)) != first {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("a title too long to fit never scrolled")
	}
}

// Wide runes must not be split in half by the scroll.
func TestMarqueeHandlesWideRunes(t *testing.T) {
	s := "｜｜｜｜｜｜｜｜｜｜｜｜"
	for ms := int64(0); ms < 6000; ms += 180 {
		if w := visibleWidth(marquee(s, 7, time.UnixMilli(ms))); w > 7 {
			t.Fatalf("at %dms width was %d", ms, w)
		}
	}
}

// The marquee has to hold still long enough to be read before it moves. It
// scrolled immediately and quickly at first, which meant the title was in
// motion before the eye had settled on it.
func TestMarqueeHoldsBeforeScrolling(t *testing.T) {
	long := "a very long track title that will not fit in the panel"
	// Start part way through wall time, which is the case that was broken:
	// the pause used to be measured from an absolute clock, so a title that
	// appeared mid cycle was already scrolling.
	base := time.UnixMilli(1737000000123)
	first := marquee(long, 16, base)
	for ms := int64(0); ms < 2400; ms += 100 {
		if got := marquee(long, 16, base.Add(time.Duration(ms)*time.Millisecond)); got != first {
			t.Fatalf("started scrolling %dms after the title appeared", ms)
		}
	}
}

// A different title restarts the pause, rather than inheriting where the
// previous one had got to.
func TestMarqueeRestartsForANewTitle(t *testing.T) {
	base := time.UnixMilli(1737000000123)
	one := "the first long title that does not fit in the space"
	two := "a second long title that also does not fit in there"
	marquee(one, 16, base)
	scrolled := marquee(one, 16, base.Add(6*time.Second))
	if scrolled == marquee(one, 16, base) {
		t.Fatal("the first title never scrolled, so this proves nothing")
	}
	fresh := marquee(two, 16, base.Add(6*time.Second))
	if fresh != marquee(two, 16, base.Add(6*time.Second).Add(time.Second)) {
		t.Fatal("a newly selected title must hold still, not carry on mid scroll")
	}
}
