package ui

import (
	"fmt"
	"math"
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

	lr, lg, lb := hexRGB(t.Accent)
	out := make([]string, 0, rows)
	var b strings.Builder

	shade := func(px, py int) (int, int, int) {
		// Normalise to [-1,1] with the shorter axis setting the scale, so the
		// record stays circular in any terminal shape.
		size := math.Min(float64(w), float64(h))
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
			// A single notch turning with the record. One mark reads as
			// rotation; the earlier three-lobed pattern just looked like a
			// flower stamped on the label.
			d := math.Abs(angleDiff(th, rot))
			f := 1.0
			if d < 0.30 {
				f = 0.55
			}
			// Shade the label a little towards its edge so it looks round
			// rather than like a flat sticker.
			f *= 1.0 - 0.25*(r/0.28)
			return int(float64(lr) * f), int(float64(lg) * f), int(float64(lb) * f)
		}

		// Vinyl is black. The body stays dark and the gloss does the work,
		// which is what makes it read as a record rather than a grey ball.
		base := 0.10
		// Grooves: fine concentric rings, stronger in the playing area.
		base += 0.045 * math.Sin(r*110)
		// Shine: a narrow highlight sweeping round as rot advances.
		sh := math.Cos(th - rot)
		if sh > 0 {
			base += 0.62 * math.Pow(sh, 10)
		} else {
			base += 0.20 * math.Pow(-sh, 14)
		}
		// The lead-in groove and the outer rim both catch the light.
		if r > 0.94 {
			base += 0.22
		}
		if r > 0.30 && r < 0.33 {
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

	for row := 0; row < rows; row++ {
		b.Reset()
		for col := 0; col < cols; col++ {
			tr, tg, tb := shade(col, row*2)
			br, bg, bb := shade(col, row*2+1)
			switch {
			case tr < 0 && br < 0:
				b.WriteByte(' ')
			case tr < 0:
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm▄%s", br, bg, bb, reset)
			case br < 0:
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm▀%s", tr, tg, tb, reset)
			default:
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀%s",
					tr, tg, tb, br, bg, bb, reset)
			}
		}
		out = append(out, b.String())
	}
	return out
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

// hexRGB parses "#rrggbb" for the vinyl label.
func hexRGB(hex string) (int, int, int) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return 200, 200, 200
	}
	var r, g, bl int
	fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &bl)
	return r, g, bl
}

// spectrumBars renders the visualiser as vertical bars using eighth-block
// characters, so a bar can grow by an eighth of a row rather than jumping a
// whole one.
func spectrumBars(vals []float64, rows int, t theme.Theme) []string {
	if rows < 1 || len(vals) == 0 {
		return nil
	}
	const ramp = " ▁▂▃▄▅▆▇█"
	glyphs := []rune(ramp)

	lines := make([]string, rows)
	var b strings.Builder
	for row := 0; row < rows; row++ {
		b.Reset()
		// Row 0 is the top of the display.
		rowFromBottom := rows - 1 - row
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
			if g == ' ' {
				b.WriteByte(' ')
				continue
			}
			// Colour by height, so the top of a loud bar is a different shade
			// from its base and the whole display reads as a gradient.
			p := float64(rowFromBottom) / float64(rows)
			b.WriteString(t.SpectrumAt(p))
			b.WriteRune(g)
			b.WriteString(reset)
			_ = i
		}
		lines[row] = b.String()
	}
	return lines
}

// VinylPreview and BarsPreview expose the renderers for the preview command,
// which draws one frame so it can be looked at without the interface redrawing
// underneath it.
func VinylPreview(cols, rows int, rot float64, t theme.Theme) []string {
	return vinyl(cols, rows, rot, t)
}

func BarsPreview(vals []float64, rows int, t theme.Theme) []string {
	return spectrumBars(vals, rows, t)
}
