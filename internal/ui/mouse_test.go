package ui

import "testing"

func TestParseMouseLeftPress(t *testing.T) {
	ev, n, ok, partial := parseMouse([]byte("\x1b[<0;12;34M"))
	if !ok || partial {
		t.Fatalf("ok=%v partial=%v", ok, partial)
	}
	if n != 11 {
		t.Fatalf("consumed %d", n)
	}
	if ev.kind != mousePress || ev.x != 12 || ev.y != 34 || ev.button != 0 {
		t.Fatalf("got %+v", ev)
	}
}

func TestParseMouseRelease(t *testing.T) {
	ev, _, ok, _ := parseMouse([]byte("\x1b[<0;5;6m"))
	if !ok || ev.kind != mouseRelease {
		t.Fatalf("got %+v ok=%v", ev, ok)
	}
}

func TestParseMouseWheel(t *testing.T) {
	up, _, _, _ := parseMouse([]byte("\x1b[<64;1;1M"))
	down, _, _, _ := parseMouse([]byte("\x1b[<65;1;1M"))
	if up.kind != mouseWheelUp || down.kind != mouseWheelDown {
		t.Fatalf("up=%v down=%v", up.kind, down.kind)
	}
}

func TestParseMouseDrag(t *testing.T) {
	ev, _, ok, _ := parseMouse([]byte("\x1b[<32;9;9M"))
	if !ok || ev.kind != mouseDrag {
		t.Fatalf("got %+v", ev)
	}
}

// Large coordinates are the reason for using SGR rather than the older
// encoding, which cannot report a column past 223 at all.
func TestParseMouseHandlesLargeCoordinates(t *testing.T) {
	ev, _, ok, _ := parseMouse([]byte("\x1b[<0;250;120M"))
	if !ok || ev.x != 250 || ev.y != 120 {
		t.Fatalf("got %+v", ev)
	}
}

// A report split across two reads must not be mistaken for keystrokes, or the
// tail of it ends up typed into whatever has focus.
func TestParseMouseReportsPartial(t *testing.T) {
	_, n, ok, partial := parseMouse([]byte("\x1b[<0;12;3"))
	if ok || !partial || n != 0 {
		t.Fatalf("ok=%v partial=%v n=%d", ok, partial, n)
	}
}

func TestParseMouseIgnoresPlainKeys(t *testing.T) {
	_, _, ok, partial := parseMouse([]byte("hello"))
	if ok || partial {
		t.Fatal("plain text must not look like a mouse report")
	}
}
