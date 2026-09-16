package player

import (
	"math"
	"testing"
	"time"
)

// A pure tone must show up in the bin matching its frequency. If the transform
// is wrong the visualiser still moves, plausibly, which is why this is worth
// asserting rather than eyeballing.
func TestFFTFindsAPureTone(t *testing.T) {
	freq := 1000.0
	re := make([]float64, fftSize)
	im := make([]float64, fftSize)
	for i := range re {
		re[i] = math.Sin(2 * math.Pi * freq * float64(i) / sampleRate)
	}
	fft(re, im)

	peak, peakBin := 0.0, 0
	for b := 1; b < fftSize/2; b++ {
		if m := math.Hypot(re[b], im[b]); m > peak {
			peak, peakBin = m, b
		}
	}
	want := int(freq * float64(fftSize) / float64(sampleRate))
	if peakBin < want-2 || peakBin > want+2 {
		t.Fatalf("peak at bin %d, expected near %d", peakBin, want)
	}
}

func TestHannWindowTapersToZero(t *testing.T) {
	w := hann(64)
	if w[0] > 1e-9 || w[len(w)-1] > 1e-9 {
		t.Fatalf("window must reach zero at both ends, got %v and %v", w[0], w[len(w)-1])
	}
	if w[32] < 0.9 {
		t.Fatalf("window should peak near the middle, got %v", w[32])
	}
}

// Before the decode finishes the bars must still be drawable.
func TestBarsBeforeReady(t *testing.T) {
	s := &Spectrum{window: hann(fftSize)}
	got := s.Bars(0, 24)
	if len(got) != 24 {
		t.Fatalf("want 24 bars, got %d", len(got))
	}
}

func TestBarsStayInRange(t *testing.T) {
	s := &Spectrum{window: hann(fftSize), ready: true}
	s.samples = make([]float64, sampleRate)
	for i := range s.samples {
		s.samples[i] = math.Sin(2 * math.Pi * 440 * float64(i) / sampleRate)
	}
	for _, v := range s.Bars(0, 32) {
		if v < 0 || v > 1 {
			t.Fatalf("bar out of range: %v", v)
		}
	}
}

// The bars have to use their range. An earlier version forgot to normalise the
// transform, so every bin read about +54 dB, clamped to full height, and the
// visualiser was a solid block that never moved.
func TestBarsAreNotAllSaturated(t *testing.T) {
	s := &Spectrum{window: hann(fftSize), ready: true}
	s.samples = make([]float64, sampleRate)
	// A quiet low tone: the bottom bars should respond, the top ones not.
	for i := range s.samples {
		s.samples[i] = 0.25 * math.Sin(2*math.Pi*220*float64(i)/sampleRate)
	}
	bars := s.Bars(0, 24)
	high := 0
	for _, v := range bars {
		if v > 0.95 {
			high++
		}
	}
	if high == len(bars) {
		t.Fatal("every bar is at full height; the transform is not normalised")
	}
}

// A quiet track must still fill the display. Without automatic gain the bars
// are scaled by absolute level, so a quietly mastered record draws a row of
// stubs however lively the music is.
func TestQuietAudioStillUsesTheHeight(t *testing.T) {
	loud := barsFor(t, 0.6)
	quiet := barsFor(t, 0.05)
	if loud < 0.7 {
		t.Fatalf("loud audio peaked at %.2f, expected most of the height", loud)
	}
	if quiet < 0.5 {
		t.Fatalf("quiet audio peaked at only %.2f; the gain is not adapting", quiet)
	}
}

// Silence must not be amplified into a full display.
func TestSilenceStaysFlat(t *testing.T) {
	if got := barsFor(t, 0); got > 0.2 {
		t.Fatalf("silence drew bars %.2f tall", got)
	}
}

// barsFor runs a tone at the given amplitude through enough frames for the
// gain to settle, and reports the tallest bar.
func barsFor(t *testing.T, amp float64) float64 {
	t.Helper()
	s := &Spectrum{window: hann(fftSize), ready: true}
	// Long enough that the frames below stay inside the audio: running past
	// the end returns decaying values and the measurement reads as silence.
	s.samples = make([]float64, sampleRate*5)
	for i := range s.samples {
		s.samples[i] = amp * math.Sin(2*math.Pi*440*float64(i)/sampleRate)
	}
	var out []float64
	for i := 0; i < 80; i++ {
		out = s.Bars(time.Duration(i)*40*time.Millisecond, 24)
	}
	peak := 0.0
	for _, v := range out {
		if v > peak {
			peak = v
		}
	}
	return peak
}
