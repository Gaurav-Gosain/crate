package ui

import "strconv"

// Mouse reporting is enabled in SGR mode. The older X10 encoding packs the
// coordinates into single bytes and simply cannot address a column past 223,
// which any full screen terminal exceeds; SGR reports them as decimal numbers
// and has no such limit.
//
// 1000 reports button presses, 1002 adds motion while a button is held, which
// is what makes a progress bar draggable rather than merely clickable.
const (
	enableMouse  = "\x1b[?1000h\x1b[?1002h\x1b[?1006h"
	disableMouse = "\x1b[?1006l\x1b[?1002l\x1b[?1000l"
)

type mouseKind int

const (
	mousePress mouseKind = iota
	mouseRelease
	mouseDrag
	mouseWheelUp
	mouseWheelDown
)

type mouseEvent struct {
	kind   mouseKind
	button int
	// x and y are one-based, matching the terminal's own coordinates.
	x, y int
}

// parseMouse reads one SGR mouse report from the front of buf.
//
// It returns how many bytes it consumed. A zero with ok false means the buffer
// does not start with a mouse report; a zero with ok true means it starts with
// one that has not arrived in full yet, and the caller should wait rather than
// treating the bytes as keystrokes.
func parseMouse(buf []byte) (ev mouseEvent, consumed int, ok bool, partial bool) {
	if len(buf) < 3 {
		if len(buf) > 0 && buf[0] == 0x1b {
			return ev, 0, false, true
		}
		return ev, 0, false, false
	}
	if buf[0] != 0x1b || buf[1] != '[' || buf[2] != '<' {
		return ev, 0, false, false
	}

	// Find the terminator: M for a press or motion, m for a release.
	end := -1
	for i := 3; i < len(buf); i++ {
		if buf[i] == 'M' || buf[i] == 'm' {
			end = i
			break
		}
	}
	if end < 0 {
		// The report is still arriving.
		return ev, 0, false, true
	}

	var nums [3]int
	field, val, seen := 0, 0, false
	for i := 3; i < end; i++ {
		c := buf[i]
		switch {
		case c >= '0' && c <= '9':
			val = val*10 + int(c-'0')
			seen = true
		case c == ';':
			if field < 3 {
				nums[field] = val
			}
			field, val, seen = field+1, 0, false
		default:
			return ev, end + 1, false, false
		}
	}
	if field < 3 && seen {
		nums[field] = val
		field++
	}
	if field < 3 {
		return ev, end + 1, false, false
	}

	code, x, y := nums[0], nums[1], nums[2]
	ev.x, ev.y = x, y

	switch {
	case code&64 != 0:
		// Wheel events are reported as buttons 64 and 65.
		if code&1 == 0 {
			ev.kind = mouseWheelUp
		} else {
			ev.kind = mouseWheelDown
		}
	case code&32 != 0:
		ev.kind = mouseDrag
		ev.button = code & 3
	case buf[end] == 'm':
		ev.kind = mouseRelease
		ev.button = code & 3
	default:
		ev.kind = mousePress
		ev.button = code & 3
	}
	return ev, end + 1, true, false
}

// itoa keeps the escape building free of fmt in the hot path.
func itoa(n int) string { return strconv.Itoa(n) }
