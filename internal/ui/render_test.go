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
