package ui

import (
	"github.com/Gaurav-Gosain/crate/internal/theme"
)

// openPalette shows every command the interface has, searchable.
//
// The keys are still there and still faster, but a palette means nothing is
// hidden behind a key nobody remembers, and it is where a new command goes
// without having to find a free letter for it.
func (a *App) openPalette() {
	a.mu.Lock()
	inPlay := a.mode == modePlay
	a.mu.Unlock()

	items := []overlayItem{
		{label: "sync all sources", detail: "s", run: func() { go a.syncAll() }},
		{label: "sync selected source", detail: "enter", run: func() { go a.syncSelected() }},
		{label: "add a source", detail: "a", run: func() { a.beginPrompt("url:", a.addSource) }},
		{label: "search", detail: "/", run: a.beginSearch},
		{label: "remove selected source", detail: "d", run: a.removeSelected},
		{label: "rescan the music server", detail: "r", run: func() { go a.scanOnly() }},
		{label: "choose a theme", detail: "t", run: a.openThemes},
	}
	if inPlay {
		items = append([]overlayItem{
			{label: "play or pause", detail: "space", run: func() {
				a.mu.Lock()
				p := a.player
				a.mu.Unlock()
				if p != nil {
					p.Toggle()
				}
			}},
			{label: "next track", detail: "n", run: func() { go a.playStep(1) }},
			{label: "previous track", detail: "b", run: func() { go a.playStep(-1) }},
			{label: "toggle the album art pane", detail: "a", run: func() { a.togglePane("art") }},
			{label: "toggle the vinyl pane", detail: "v", run: func() { a.togglePane("vinyl") }},
			{label: "toggle the spectrum pane", detail: "s", run: func() { a.togglePane("spectrum") }},
			{label: "toggle the lyrics pane", detail: "y", run: func() { a.togglePane("lyrics") }},
			{label: "leave play mode", detail: "q", run: a.leavePlay},
		}, items...)
	} else {
		items = append(items, overlayItem{
			label: "play mode", detail: "p", run: a.enterPlay,
		})
	}
	items = append(items, overlayItem{label: "quit", detail: "q", run: func() {
		a.stop()
	}})

	a.openOverlay(&overlayState{
		title: "commands",
		hint:  "type to filter   ↑↓ move   ⏎ run   esc cancel",
		all:   items,
	})
}

// openThemes shows the palettes with a swatch of each, applied as the cursor
// moves so a theme is chosen by seeing it rather than by reading its name.
func (a *App) openThemes() {
	before := theme.Current().Name

	var items []overlayItem
	for _, name := range theme.Names() {
		n := name
		var sw []string
		if t, ok := theme.Get(n); ok {
			sw = []string{t.Accent, t.Ok, t.Warn, t.Bad, t.Muted}
		}
		items = append(items, overlayItem{
			label:  n,
			swatch: sw,
			run: func() {
				if err := theme.Set(n); err != nil {
					a.logf("theme: %v", err)
					return
				}
				a.mu.Lock()
				a.cfg.Theme = n
				a.mu.Unlock()
				if err := a.saveCfg(); err != nil {
					a.logf("could not remember the theme: %v", err)
				}
				a.logf("theme: %s", n)
			},
		})
	}

	a.openOverlay(&overlayState{
		title: "themes",
		hint:  "↑↓ preview   ⏎ apply   esc cancel",
		all:   items,
		// The palette itself is refreshed by the drawing loop; these only
		// switch the active theme and ask for a frame.
		preview: func(it overlayItem) {
			_ = theme.Set(it.label)
			a.redraw()
		},
		cancel: func() {
			_ = theme.Set(before)
			a.redraw()
		},
	})
	// Start on the theme in use, so the picker opens where the eye expects.
	a.mu.Lock()
	if a.ov != nil {
		for i, it := range a.ov.shown {
			if it.label == before {
				a.ov.cursor = i
				break
			}
		}
	}
	a.mu.Unlock()
}
