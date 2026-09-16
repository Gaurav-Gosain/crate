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

// Synthetic key codes for keys that arrive as escape sequences. They sit
// above the ASCII range so they cannot collide with a real byte.
const (
	keyUp byte = iota + 0x80
	keyDown
	keyRight
	keyLeft
	keyHome
	keyEnd
	keyPgUp
	keyPgDn
	keyDelete
)

// parseCSIKey reads one arrow or navigation key from the front of buf.
//
// These are needed because an overlay that filters as you type cannot also use
// j and k to move: the letters have to go into the filter.
func parseCSIKey(buf []byte) (key byte, n int, ok bool, partial bool) {
	if len(buf) < 2 || buf[0] != 0x1b || buf[1] != '[' {
		return 0, 0, false, false
	}
	if len(buf) < 3 {
		return 0, 0, false, true
	}
	switch buf[2] {
	case 'A':
		return keyUp, 3, true, false
	case 'B':
		return keyDown, 3, true, false
	case 'C':
		return keyRight, 3, true, false
	case 'D':
		return keyLeft, 3, true, false
	case 'H':
		return keyHome, 3, true, false
	case 'F':
		return keyEnd, 3, true, false
	}
	// Sequences of the form ESC [ <digits> ~
	if buf[2] >= '0' && buf[2] <= '9' {
		for i := 3; i < len(buf); i++ {
			if buf[i] == '~' {
				switch string(buf[2:i]) {
				case "3":
					return keyDelete, i + 1, true, false
				case "5":
					return keyPgUp, i + 1, true, false
				case "6":
					return keyPgDn, i + 1, true, false
				}
				return 0, i + 1, false, false
			}
			if buf[i] < '0' || buf[i] > '9' {
				return 0, 0, false, false
			}
		}
		return 0, 0, false, true
	}
	return 0, 0, false, false
}

// skipAPC reports the length of an application programme command at the front
// of buf, terminated by ESC backslash.
//
// Terminals answer graphics commands with one of these. Without skipping it
// the reply is handed to the key handler a byte at a time, and the user finds
// the terminal has typed "Gi=7101;OK" into whatever had focus.
func skipAPC(buf []byte) (n int, partial bool) {
	if len(buf) < 2 || buf[0] != 0x1b || buf[1] != '_' {
		return 0, false
	}
	for i := 2; i < len(buf)-1; i++ {
		if buf[i] == 0x1b && buf[i+1] == '\\' {
			return i + 2, false
		}
	}
	return 0, true
}

// itoa keeps the escape building free of fmt in the hot path.
func itoa(n int) string { return strconv.Itoa(n) }
