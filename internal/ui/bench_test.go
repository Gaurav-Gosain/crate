package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/crate/internal/library"
	"github.com/Gaurav-Gosain/crate/internal/theme"
)

func BenchmarkVinyl(b *testing.B) {
	t := theme.Current()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		vinyl(34, 17, float64(i)*0.1, t)
	}
}

func BenchmarkSpectrumBars(b *testing.B) {
	t := theme.Current()
	vals := make([]float64, 48)
	peaks := make([]float64, 48)
	for i := range vals {
		vals[i] = float64(i%10) / 10
		peaks[i] = vals[i] + 0.1
	}
	b.ReportAllocs()
	for b.Loop() {
		spectrumBars(vals, peaks, 12, t)
	}
}

func benchSongs(n int) []library.Song {
	songs := make([]library.Song, n)
	for i := range songs {
		songs[i] = library.Song{
			Rel:    fmt.Sprintf("Artist %d/Album %d/%02d - A Track Title Of Reasonable Length.opus", i/40, i/12, i%12),
			Artist: fmt.Sprintf("Artist %d", i/40),
			Album:  fmt.Sprintf("Album %d", i/12),
			Title:  "A Track Title Of Reasonable Length",
		}
	}
	return songs
}

// BenchmarkDrawPlay measures one whole play-mode frame, which the animate
// loop asks for fifteen times a second.
func BenchmarkDrawPlay(b *testing.B) {
	a := New(newBenchConfig())
	a.songs = benchSongs(900)
	a.nowPlaying = a.songs[450]
	a.playCursor = 450
	var sb strings.Builder
	b.ReportAllocs()
	for b.Loop() {
		sb.Reset()
		a.drawPlay(&sb, 200, 50)
	}
}

func BenchmarkTruncateStyled(b *testing.B) {
	s := "\x1b[1m04 - A Fairly Long Track Title ｜ With Wide Runes\x1b[0m and a tail"
	b.ReportAllocs()
	for b.Loop() {
		truncate(s, 30)
	}
}
