package ui

import "testing"

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
