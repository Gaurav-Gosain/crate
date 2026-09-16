package ui

import (
	"strings"
	"sync"
	"testing"
)

func testOverlayApp() *App {
	a := New(newBenchConfig())
	items := make([]overlayItem, 0, 40)
	for _, l := range []string{
		"sync all sources", "sync selected source", "add a source", "search",
		"remove selected source", "rescan the music server", "choose a theme",
		"play mode", "quit", "toggle the album art pane", "toggle the vinyl pane",
	} {
		items = append(items, overlayItem{label: l, detail: "x", run: func() {}})
	}
	a.openOverlay(&overlayState{title: "commands", hint: "hint", all: items})
	return a
}

// The palette is typed into from the input goroutine while the drawing loop
// reads it to paint the frame. Both have to be safe together: the filter,
// the filtered list and the cursor are all mutated mid-keystroke.
func TestOverlayDrawAndTypeConcurrently(t *testing.T) {
	a := testOverlayApp()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			for _, c := range []byte("sync") {
				a.handleOverlayKey(c)
			}
			for range 4 {
				a.handleOverlayKey(127) // backspace
			}
			a.moveOverlay(1)
			a.moveOverlay(-1)
		}
	}()
	var b strings.Builder
	for range 200 {
		b.Reset()
		a.drawOverlay(&b, 120, 40)
	}
	wg.Wait()
}

func TestFilterItemsSubsequence(t *testing.T) {
	all := []overlayItem{{label: "now playing"}, {label: "quit"}}
	got := filterItems(all, []rune("nwp"))
	if len(got) != 1 || got[0].label != "now playing" {
		t.Fatalf("got %v", got)
	}
	if got := filterItems(all, nil); len(got) != 2 {
		t.Fatalf("empty filter should keep everything, got %d", len(got))
	}
}

// The play view is painted by the drawing loop while the input goroutine
// moves the cursor and scrolls; the hit regions are written mid-frame and
// read mid-click, so both directions run here under the race detector.
func TestPlayDrawAndInputConcurrently(t *testing.T) {
	a := New(newBenchConfig())
	a.mu.Lock()
	a.mode = modePlay
	a.songs = benchSongs(120)
	a.nowPlaying = a.songs[3]
	a.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 300 {
			a.handlePlayKey('j')
			a.handlePlayKey('k')
			a.handlePlayKey(keyEnd)
			a.handlePlayKey(keyHome)
			a.handleMouse(mouseEvent{kind: mouseWheelDown, x: 5, y: 8})
			a.handleMouse(mouseEvent{kind: mousePress, x: 5, y: 8})
		}
	}()
	var b strings.Builder
	for range 300 {
		b.Reset()
		a.drawPlay(&b, 160, 45)
	}
	wg.Wait()
}
