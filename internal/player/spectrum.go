package player

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sync"
	"time"
)

const (
	// sampleRate is deliberately low. The visualiser only needs frequencies a
	// listener can see moving, and decoding a whole track at 44.1kHz costs
	// four times the memory to show the same bars.
	sampleRate = 22050
	// fftSize must be a power of two for the radix-2 transform below. 1024
	// samples is ~46ms of audio, short enough to track a beat.
	fftSize = 1024
)

// Spectrum holds a decoded track and turns a playback position into bar
// heights.
//
// The whole track is decoded once, up front, rather than run through a second
// live ffmpeg alongside mpv. Two decoders playing the same file cannot be kept
// in step: they drift, and every seek has to be mirrored to both. Decoding
// once means the bars are indexed by position and are exactly as correct after
// a seek as before it.
type Spectrum struct {
	mu      sync.Mutex
	samples []float64
	// smoothed carries bar heights between frames so they fall away rather
	// than flickering; peaks hang briefly like a real analyser.
	smoothed []float64
	window   []float64
	ready    bool
	err      error
}

// Analyse decodes a file in the background and returns immediately. The bars
// read empty until it finishes, which for a typical track is a second or two.
func Analyse(ctx context.Context, path string) *Spectrum {
	s := &Spectrum{window: hann(fftSize)}
	go func() {
		samples, err := decode(ctx, path)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.samples, s.err, s.ready = samples, err, err == nil
	}()
	return s
}

// decode runs ffmpeg and reads raw mono PCM from its stdout.
func decode(ctx context.Context, path string) ([]float64, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-v", "error",
		"-i", path,
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ac", "1",
		"-ar", fmt.Sprint(sampleRate),
		"-",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("decode for visualiser: %s", bytes.TrimSpace(stderr.Bytes()))
	}
	n := len(out) / 2
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(out[i*2:]))
		samples[i] = float64(v) / 32768.0
	}
	return samples, nil
}

// Ready reports whether decoding has finished.
func (s *Spectrum) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// Err reports a decode failure, if any.
func (s *Spectrum) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Bars returns n bar heights in [0,1] for the audio playing at position pos.
func (s *Spectrum) Bars(pos time.Duration, n int) []float64 {
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.smoothed) != n {
		s.smoothed = make([]float64, n)
	}
	if !s.ready || len(s.samples) == 0 {
		// Decay whatever was on screen rather than snapping to nothing.
		for i := range s.smoothed {
			s.smoothed[i] *= 0.8
		}
		return append([]float64(nil), s.smoothed...)
	}

	start := int(pos.Seconds() * sampleRate)
	if start < 0 {
		start = 0
	}
	if start+fftSize > len(s.samples) {
		for i := range s.smoothed {
			s.smoothed[i] *= 0.8
		}
		return append([]float64(nil), s.smoothed...)
	}

	re := make([]float64, fftSize)
	im := make([]float64, fftSize)
	for i := 0; i < fftSize; i++ {
		re[i] = s.samples[start+i] * s.window[i]
	}
	fft(re, im)

	// Buckets are spaced logarithmically: pitch is logarithmic, so linear
	// buckets would crowd everything audible into the first few bars.
	bins := fftSize / 2
	minBin, maxBin := 1.0, float64(bins)
	for i := 0; i < n; i++ {
		lo := int(minBin * math.Pow(maxBin/minBin, float64(i)/float64(n)))
		hi := int(minBin * math.Pow(maxBin/minBin, float64(i+1)/float64(n)))
		if hi <= lo {
			hi = lo + 1
		}
		if hi > bins {
			hi = bins
		}
		peak := 0.0
		for b := lo; b < hi; b++ {
			// Divide by half the window length so a full scale signal comes
			// out at 0 dB. Without this every bin reads about +54 dB, the
			// scaling below clamps it, and the display is a solid wall.
			m := math.Hypot(re[b], im[b]) / float64(fftSize/2)
			if m > peak {
				peak = m
			}
		}
		// Decibels, then mapped onto [0,1]. Raw magnitude looks almost flat
		// because loud and quiet differ by orders of magnitude, not factors.
		db := 20 * math.Log10(peak+1e-9)
		v := (db + 60) / 60
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		// Rise fast, fall slow.
		if v > s.smoothed[i] {
			s.smoothed[i] = v
		} else {
			s.smoothed[i] = s.smoothed[i]*0.75 + v*0.25
		}
	}
	return append([]float64(nil), s.smoothed...)
}

// hann builds a Hann window, which stops the abrupt ends of each slice from
// smearing energy across every frequency.
func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

// fft transforms in place. Iterative radix-2, which needs len to be a power
// of two; fftSize is chosen to satisfy that.
func fft(re, im []float64) {
	n := len(re)
	if n <= 1 {
		return
	}
	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += length {
			cr, ci := 1.0, 0.0
			for j := 0; j < length/2; j++ {
				ur, ui := re[i+j], im[i+j]
				vr := re[i+j+length/2]*cr - im[i+j+length/2]*ci
				vi := re[i+j+length/2]*ci + im[i+j+length/2]*cr
				re[i+j], im[i+j] = ur+vr, ui+vi
				re[i+j+length/2], im[i+j+length/2] = ur-vr, ui-vi
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}
