package ui

import (
	"math"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/crate/internal/theme"
)

// vinyl draws a spinning record.
//
// Each character cell is drawn as two stacked pixels using the upper half
// block, with the foreground colouring the top pixel and the background the
// bottom one. Terminal cells are about twice as tall as they are wide, so this
// both doubles the vertical resolution and makes the pixels square, which is
// what stops the record looking like an ellipse.
//
// Rotation is visible because of the shine, not the grooves: grooves are
// concentric, so a spinning record made only of grooves looks completely
// still. The highlight sweeping around the disc is what the eye reads as
// motion.
func vinyl(cols, rows int, rot float64, t theme.Theme) []string {
	if cols < 4 || rows < 2 {
		return nil
	}
	w, h := cols, rows*2

	lr, lg, lb := theme.RGB(t.Accent)
	out := make([]string, 0, rows)
	var b []byte

	// Normalise to [-1,1] with the shorter axis setting the scale, so the
	// record stays circular in any terminal shape.
	size := math.Min(float64(w), float64(h))
	shade := func(px, py int) (int, int, int) {
		x := (float64(px) - float64(w)/2) / (size / 2)
		y := (float64(py) - float64(h)/2) / (size / 2)
		r := math.Hypot(x, y)
		if r > 1.0 {
			return -1, -1, -1 // transparent, caller leaves the cell blank
		}
		th := math.Atan2(y, x)

		// The label, with a spindle hole punched through the middle.
		if r < 0.28 {
			if r < 0.04 {
				return 10, 10, 12
			}
			// The label is left plain. At this size a notch drawn on it is
			// only a few pixels and reads as a scratch rather than as a mark
			// turning with the record; the sheen already carries the
			// rotation. It is shaded towards its edge so it looks round
			// rather than like a flat sticker.
			f := 1.0 - 0.30*(r/0.28)
			return int(float64(lr) * f), int(float64(lg) * f), int(float64(lb) * f)
		}

		// Vinyl is black. The body stays dark and the gloss does the work,
		// which is what makes it read as a record rather than a grey ball.
		base := 0.08

		// Grooves: concentric rings across the playing area only.
		//
		// The frequency has to stay under what the pixel grid can resolve. At
		// this size the radius is only about seventeen pixels, so anything
		// above roughly eight rings aliases: an earlier version used a
		// hundred and thirty radians per unit radius and the moire pattern
		// drew a dark cross straight through the record.
		if r > 0.32 && r < 0.93 {
			base += 0.05 * math.Sin(r*34)
		}

		// The sheen is an arc across one side of the record, not a beam
		// through its middle. Raising cos to a high power put a bright bar
		// straight across the disc and left dark quadrants either side, which
		// read as a pinwheel rather than as light on a glossy surface.
		d := math.Abs(angleDiff(th, rot))
		ang := math.Exp(-(d * d) / 0.55)
		// Light falls on the outer part of a tilted disc more than the middle.
		rad := math.Exp(-((r - 0.72) * (r - 0.72)) / 0.16)
		base += 0.55 * ang * rad
		// A weaker sheen opposite, as a real record picks up a second source.
		d2 := math.Abs(angleDiff(th, rot+math.Pi))
		base += 0.16 * math.Exp(-(d2*d2)/0.40) * rad

		// The lead-in groove and the outer rim both catch the light.
		if r > 0.94 {
			base += 0.16
		}
		if r > 0.30 && r < 0.325 {
			base += 0.10
		}
		v := int(base * 255)
		if v > 255 {
			v = 255
		}
		// Clamp low as well: a negative component would be written into the
		// escape sequence verbatim and corrupt the rest of the frame.
		if v < 0 {
			v = 0
		}
		return v, v, v
	}

	// Number formatting is done by hand: this runs for every pixel of every
	// frame, fifteen times a second, and going through fmt here was about a
	// third of the whole frame's cost.
	for row := range rows {
		b = b[:0]
		for col := range cols {
			tr, tg, tb := shade(col, row*2)
			br, bg, bb := shade(col, row*2+1)
			switch {
			case tr < 0 && br < 0:
				b = append(b, ' ')
			case tr < 0:
				b = appendRGB(b, "\x1b[38;2;", br, bg, bb)
				b = append(b, "▄"+reset...)
			case br < 0:
				b = appendRGB(b, "\x1b[38;2;", tr, tg, tb)
				b = append(b, "▀"+reset...)
			default:
				b = appendRGB(b, "\x1b[38;2;", tr, tg, tb)
				b = appendRGB(b, "\x1b[48;2;", br, bg, bb)
				b = append(b, "▀"+reset...)
			}
		}
		out = append(out, string(b))
	}
	return out
}

// appendRGB writes one truecolor escape, prefix;r;g;bm, without fmt.
func appendRGB(b []byte, prefix string, r, g, bl int) []byte {
	b = append(b, prefix...)
	b = strconv.AppendInt(b, int64(r), 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(g), 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(bl), 10)
	return append(b, 'm')
}

// angleDiff returns the smallest signed angle between two bearings, so a notch
// at 359 degrees and a record at 1 degree are two degrees apart rather than
// three hundred and fifty eight.
func angleDiff(a, b float64) float64 {
	d := math.Mod(a-b+math.Pi, 2*math.Pi)
	if d < 0 {
		d += 2 * math.Pi
	}
	return d - math.Pi
}

// spectrumBars renders the visualiser.
//
// Bars are drawn two cells wide with a gap between them. Drawn one cell wide
// and touching, as they were first, the display is a single mass whose top
// edge wobbles, rather than a set of bars that individually rise and fall.
//
// Eighth-block characters let a bar grow by an eighth of a row instead of
// jumping a whole one, which is the difference between a smooth analyser and
// a flickering one.
func spectrumBars(vals, peaks []float64, rows int, t theme.Theme) []string {
	if rows < 1 || len(vals) == 0 {
		return nil
	}
	const ramp = " ▁▂▃▄▅▆▇█"
	glyphs := []rune(ramp)
	const barW, gap = 2, 1

	lines := make([]string, rows)
	var b strings.Builder
	for row := range rows {
		b.Reset()
		rowFromBottom := rows - 1 - row
		// Colour by height so a tall bar shades through the gradient, which
		// is what gives the display its depth. The gradient position only
		// depends on the row, so the colours are sampled once per row here
		// rather than once per cell: SpectrumAt parses hex stops and
		// formats an escape, and per cell it dominated the whole renderer.
		p := float64(rowFromBottom) / float64(rows)
		rowColor := t.SpectrumAt(p)
		peakColor := t.SpectrumAt(math.Min(1, p+0.15))
		for i, v := range vals {
			filled := v * float64(rows)
			cell := filled - float64(rowFromBottom)

			var g rune
			switch {
			case cell >= 1:
				g = glyphs[8]
			case cell <= 0:
				g = ' '
			default:
				g = glyphs[int(cell*8)]
			}

			// The peak mark sits above the bar and falls back slowly.
			markHere := false
			if i < len(peaks) && peaks[i] > v {
				pk := int(peaks[i] * float64(rows))
				if pk == rowFromBottom && pk > 0 {
					markHere = true
				}
			}

			switch {
			case markHere:
				b.WriteString(peakColor)
				for range barW {
					b.WriteString("▁")
				}
				b.WriteString(reset)
			case g == ' ':
				for range barW {
					b.WriteByte(' ')
				}
			default:
				b.WriteString(rowColor)
				for range barW {
					b.WriteRune(g)
				}
				b.WriteString(reset)
			}
			if i < len(vals)-1 {
				for range gap {
					b.WriteByte(' ')
				}
			}
		}
		lines[row] = b.String()
	}
	return lines
}

// BarCount reports how many bars fit in a given width, given the bar width and
// the gap between them.
func BarCount(width int) int {
	const stride = 3 // two cells of bar plus one of gap
	n := (width + 1) / stride
	if n < 1 {
		n = 1
	}
	return n
}

// VinylPreview and BarsPreview expose the renderers for the preview command,
// which draws one frame so it can be looked at without the interface redrawing
// underneath it.
func VinylPreview(cols, rows int, rot float64, t theme.Theme) []string {
	return vinyl(cols, rows, rot, t)
}

func BarsPreview(vals, peaks []float64, rows int, t theme.Theme) []string {
	return spectrumBars(vals, peaks, rows, t)
}
