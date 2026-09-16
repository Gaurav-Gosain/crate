package player

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"slices"
	"sync"
	"time"
)

const (
	// sampleRate is deliberately low. The visualiser only needs frequencies a
	// listener can see moving, and decoding a whole track at 44.1kHz costs
	// four times the memory to show the same bars.
	sampleRate = 22050
	// fftSize must be a power of two for the radix-2 transform below.
	//
	// 4096 samples is 186ms of audio and bins 5.4Hz apart. The previous 1024
	// gave 21.5Hz bins, which is coarser than the spacing of the bars at the
	// bottom of the display: the lowest eight bars each fell inside a single
	// bin and neighbouring pairs shared one, so they moved as a block and the
	// bass end had no detail in it at all.
	fftSize = 4096
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
	peaks    []float64
	// vel carries how fast a bar is falling, so it accelerates downwards
	// instead of creeping.
	vel []float64
	// Scratch space, kept between frames so a display running fifteen times a
	// second is not allocating a megabyte a second to throw away.
	re, im, mag []float64
	raw, work   []float64
	// agc tracks how loud the loudest bar has been recently, so the display
	// uses its full height whatever the track is mastered at. Without it a
	// quiet record draws a row of stubs and a loud one slams into the
	// ceiling, and neither shows what the music is doing.
	agc    float64
	window []float64
	ready  bool
	// done marks the decoder finished, so a position past the end of the
	// audio can be told apart from one that simply has not arrived yet.
	done bool
	err  error
}

// Analyse starts decoding a file in the background and returns immediately.
//
// Samples are taken as they arrive rather than waiting for the whole track.
// The first version called ffmpeg and waited for it to finish, which for a
// streamed track means downloading all of it before a single bar can be
// drawn: switching songs left the display dead for as long as that took,
// with the download competing for the same connection the music was
// arriving on.
func Analyse(ctx context.Context, path string) *Spectrum {
	s := &Spectrum{window: hann(fftSize)}
	go s.decodeInto(ctx, path)
	return s
}

// decodeInto streams raw mono PCM out of ffmpeg, appending as it comes.
func (s *Spectrum) decodeInto(ctx context.Context, path string) {
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

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.fail(err)
		return
	}
	if err := cmd.Start(); err != nil {
		s.fail(err)
		return
	}

	buf := make([]byte, 64*1024)
	// A read can end between the two bytes of a sample. The odd byte is
	// carried into the next read rather than dropped, which would swap the
	// byte order of everything after it and turn the rest into noise.
	var carry []byte
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if len(carry) > 0 {
				chunk = append(carry, chunk...)
				carry = nil
			}
			if odd := len(chunk) % 2; odd != 0 {
				carry = append(carry, chunk[len(chunk)-odd:]...)
				chunk = chunk[:len(chunk)-odd]
			}
			s.appendSamples(chunk)
		}
		if rerr != nil {
			break
		}
	}

	werr := cmd.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	if werr != nil && len(s.samples) == 0 {
		s.err = fmt.Errorf("decode for visualiser: %s", bytes.TrimSpace(stderr.Bytes()))
	}
}

// appendSamples adds decoded audio and marks the spectrum usable as soon as
// there is a window's worth to transform.
func (s *Spectrum) appendSamples(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i+1 < len(chunk); i += 2 {
		v := int16(binary.LittleEndian.Uint16(chunk[i:]))
		s.samples = append(s.samples, float64(v)/32768.0)
	}
	if len(s.samples) >= fftSize {
		s.ready = true
	}
}

func (s *Spectrum) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
	s.done = true
}

// Ready reports whether there is enough decoded audio to draw from.
func (s *Spectrum) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// Done reports whether the whole track has been decoded.
func (s *Spectrum) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// Err reports a decode failure, if any.
func (s *Spectrum) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// BarsWithPeaks returns bar heights and the slowly falling peak marks above
// them, which is what makes an analyser look like it is measuring something
// rather than just wobbling.
func (s *Spectrum) BarsWithPeaks(pos time.Duration, n int) ([]float64, []float64) {
	vals := s.Bars(pos, n)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.peaks) != len(vals) {
		s.peaks = make([]float64, len(vals))
	}
	for i, v := range vals {
		if v >= s.peaks[i] {
			s.peaks[i] = v
		} else {
			// Fall steadily, and faster the further behind the bar it is.
			// Hanging on too long leaves a mark floating over a short bar
			// with nothing under it, which reads as a glitch rather than as
			// a peak that is on its way down.
			s.peaks[i] -= 0.03 + 0.14*(s.peaks[i]-v)
			if s.peaks[i] < v {
				s.peaks[i] = v
			}
		}
	}
	return vals, append([]float64(nil), s.peaks...)
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
		s.peaks = make([]float64, n)
		s.vel = make([]float64, n)
		s.raw = make([]float64, n)
		s.work = make([]float64, n)
	}
	if !s.ready || len(s.samples) == 0 {
		// Decay whatever was on screen rather than snapping to nothing.
		for i := range s.smoothed {
			s.smoothed[i] *= 0.8
		}
		return append([]float64(nil), s.smoothed...)
	}

	start := max(int(pos.Seconds()*sampleRate), 0)
	if start+fftSize > len(s.samples) {
		for i := range s.smoothed {
			s.smoothed[i] *= 0.8
		}
		return append([]float64(nil), s.smoothed...)
	}

	if len(s.re) != fftSize {
		s.re = make([]float64, fftSize)
		s.im = make([]float64, fftSize)
		s.mag = make([]float64, fftSize/2)
	}
	re, im := s.re, s.im
	for i := range fftSize {
		re[i] = s.samples[start+i] * s.window[i]
		im[i] = 0
	}
	fft(re, im)

	// Divide by half the window length so a full scale signal comes out at
	// 0 dB. Without this every bin reads about +54 dB, the scaling below
	// clamps it, and the display is a solid wall.
	bins := fftSize / 2
	for b := range bins {
		s.mag[b] = math.Hypot(re[b], im[b]) / float64(fftSize/2)
	}

	// Buckets are spaced logarithmically: pitch is logarithmic, so linear
	// buckets would crowd everything audible into the first few bars.
	//
	// The range stops short of both ends. Below about 35 Hz there is nothing
	// but rumble, and the top octave of a lossy encode is mostly empty, so
	// including them spends bars on dead air.
	lowHz, highHz := 35.0, 14000.0
	minBin := lowHz * float64(fftSize) / float64(sampleRate)
	maxBin := highHz * float64(fftSize) / float64(sampleRate)
	maxBin = min(maxBin, float64(bins-1))

	for i := range n {
		lo := minBin * math.Pow(maxBin/minBin, float64(i)/float64(n))
		hi := minBin * math.Pow(maxBin/minBin, float64(i+1)/float64(n))

		var peak float64
		if hi-lo < 1 {
			// Narrower than a bin. Reading the one bin it lands in makes
			// neighbouring bars identical wherever several fall inside the
			// same bin, which is what made the bass end move as a block.
			// Interpolating gives each bar its own value.
			peak = s.interp((lo + hi) / 2)
		} else {
			for b := int(lo); b <= int(hi) && b < bins; b++ {
				if s.mag[b] > peak {
					peak = s.mag[b]
				}
			}
		}

		db := 20 * math.Log10(peak+1e-12)

		// Music has roughly pink spectrum: energy falls away as frequency
		// rises. Displayed flat, the left of the analyser is always tall and
		// the right always dead. Tilting the response upwards with frequency
		// is what makes the whole width of the display do something.
		centre := max((lo+hi)/2*float64(sampleRate)/float64(fftSize), 20)
		db += 4.5 * math.Log2(centre/180)

		v := clamp01((db + 70) / 55)
		if v < 0.11 {
			v = 0
		} else {
			v = (v - 0.11) / 0.89
		}
		s.raw[i] = v
	}

	// Let each bar lift its neighbours, falling away with distance. Computed
	// independently, bars jump about on their own and the display looks like
	// noise; coupling them is what turns it into the moving landscape an
	// analyser is supposed to be.
	spread(s.raw, s.work)

	for i := range n {
		v := s.work[i]
		if v > s.smoothed[i] {
			// Rise immediately: a transient that arrives late has been missed.
			s.smoothed[i] = v
			s.vel[i] = 0
			continue
		}
		// Fall under gravity rather than by a fixed fraction. A constant decay
		// crawls the last of the way down and leaves the display looking like
		// it is still settling from the last bar long after it ended.
		s.vel[i] += 0.012
		s.smoothed[i] -= s.vel[i]
		if s.smoothed[i] < v {
			s.smoothed[i] = v
			s.vel[i] = 0
		}
		if s.smoothed[i] < 0 {
			s.smoothed[i] = 0
			s.vel[i] = 0
		}
	}

	return s.normalise()
}

// normalise scales the bars so the loudest reaches near the top.
//
// The gain follows the recent peak: quickly when the music gets louder, so a
// transient is not clipped, and slowly when it gets quieter, so the display
// settles rather than pumping. It refuses to amplify near silence, which
// would turn the noise floor between tracks into a full height display.
func (s *Spectrum) normalise() []float64 {
	peak := slices.Max(s.smoothed)
	if peak > s.agc {
		s.agc += (peak - s.agc) * 0.35
	} else {
		s.agc += (peak - s.agc) * 0.02
	}
	s.agc = max(s.agc, 0.18)

	scale := 0.96 / s.agc
	out := make([]float64, len(s.smoothed))
	for i, v := range s.smoothed {
		out[i] = min(v*scale, 1)
	}
	return out
}

// clamp01 pins a value into [0,1].
func clamp01(v float64) float64 {
	return min(max(v, 0), 1)
}

// interp reads the magnitude at a fractional bin, between the two either side.
func (s *Spectrum) interp(bin float64) float64 {
	if bin < 0 {
		bin = 0
	}
	i := int(bin)
	if i >= len(s.mag)-1 {
		return s.mag[len(s.mag)-1]
	}
	f := bin - float64(i)
	return s.mag[i]*(1-f) + s.mag[i+1]*f
}

// spread lets every bar raise its neighbours, weaker with distance.
//
// This is what gives an analyser its shape. Each bar measures a narrow slice
// of the spectrum and, left alone, rises and falls independently of the ones
// beside it, so the display reads as a row of unrelated flickering columns.
// Coupling them makes a peak a hill rather than a spike, which is both easier
// to look at and a fairer picture of sound that is never confined to one band.
func spread(in, out []float64) {
	const reach = 3
	for i := range out {
		out[i] = in[i]
	}
	for i, v := range in {
		if v <= 0 {
			continue
		}
		for d := 1; d <= reach; d++ {
			// Each step out halves again, so the influence is local.
			w := v / math.Pow(2, float64(d))
			if j := i - d; j >= 0 && out[j] < w {
				out[j] = w
			}
			if j := i + d; j < len(out) && out[j] < w {
				out[j] = w
			}
		}
	}
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
