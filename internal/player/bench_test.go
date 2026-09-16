package player

import (
	"math"
	"testing"
	"time"
)

func BenchmarkBars(b *testing.B) {
	s := &Spectrum{window: hann(fftSize), ready: true}
	s.samples = make([]float64, sampleRate*10)
	for i := range s.samples {
		s.samples[i] = 0.4 * math.Sin(2*math.Pi*440*float64(i)/sampleRate)
	}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		s.BarsWithPeaks(time.Duration(i%100)*66*time.Millisecond, 48)
	}
}
