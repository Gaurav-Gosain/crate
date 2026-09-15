package ui

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/crate/internal/config"
	"github.com/Gaurav-Gosain/crate/internal/library"
)

func (a *App) watchResize() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	for range ch {
		a.redraw()
	}
}

func (a *App) readKeys() {
	buf := make([]byte, 64)
	for {
		n, err := a.tty.Read(buf)
		if err != nil || n == 0 {
			close(a.quit)
			return
		}
		for i := 0; i < n; i++ {
			if a.handleKey(buf[i]) {
				close(a.quit)
				return
			}
		}
		a.redraw()
	}
}

// handleKey processes one byte and reports whether the app should exit.
func (a *App) handleKey(c byte) bool {
	a.mu.Lock()
	editing := a.prompt != ""
	a.mu.Unlock()

	if editing {
		return a.handleEditKey(c)
	}

	switch c {
	case 'q', 3: // q or ctrl-c
		return true
	case 'j', 14:
		a.moveCursor(1)
	case 'k', 16:
		a.moveCursor(-1)
	case 'g':
		a.setCursor(0)
	case 'G':
		a.mu.Lock()
		n := len(a.rows)
		a.mu.Unlock()
		a.setCursor(n - 1)
	case 's':
		go a.syncAll()
	case '\r', '\n':
		go a.syncSelected()
	case 'a':
		a.beginPrompt("url:", a.addSource)
	case 'd':
		a.removeSelected()
	case 'r':
		go a.scanOnly()
	case '?':
		a.logf("keys: s sync all | enter sync selected | a add | d remove | r rescan server | j/k move | q quit")
	}
	return false
}

func (a *App) handleEditKey(c byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch c {
	case 27: // esc
		a.prompt, a.buf, a.onSubm = "", nil, nil
	case '\r', '\n':
		val := strings.TrimSpace(string(a.buf))
		fn := a.onSubm
		a.prompt, a.buf, a.onSubm = "", nil, nil
		if fn != nil && val != "" {
			go fn(val)
		}
	case 127, 8: // backspace
		if len(a.buf) > 0 {
			a.buf = a.buf[:len(a.buf)-1]
		}
	case 3:
		return true
	default:
		if c >= 32 && c < 127 {
			a.buf = append(a.buf, rune(c))
		}
	}
	return false
}

func (a *App) beginPrompt(label string, fn func(string)) {
	a.mu.Lock()
	a.prompt, a.buf, a.onSubm = label, nil, fn
	a.mu.Unlock()
	a.redraw()
}

func (a *App) moveCursor(d int) {
	a.mu.Lock()
	a.cursor += d
	if a.cursor < 0 {
		a.cursor = 0
	}
	if a.cursor >= len(a.rows) {
		a.cursor = max(len(a.rows)-1, 0)
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *App) setCursor(i int) {
	a.mu.Lock()
	if i < 0 {
		i = 0
	}
	if i >= len(a.rows) {
		i = max(len(a.rows)-1, 0)
	}
	a.cursor = i
	a.mu.Unlock()
	a.redraw()
}

// addSource derives a readable name from the URL so the list is scannable
// without the user having to name every playlist by hand.
func (a *App) addSource(url string) {
	name := deriveName(url)
	s := config.Source{Name: name, URL: url}

	a.mu.Lock()
	a.cfg.Sources = append(a.cfg.Sources, s)
	a.rows = append(a.rows, row{src: s, state: idle})
	a.mu.Unlock()

	if err := a.cfg.Save(); err != nil {
		a.logf("could not save config: %v", err)
		return
	}
	a.logf("added %s", name)
}

func (a *App) removeSelected() {
	a.mu.Lock()
	if len(a.rows) == 0 {
		a.mu.Unlock()
		return
	}
	i := a.cursor
	name := a.rows[i].src.Name
	a.rows = append(a.rows[:i], a.rows[i+1:]...)
	a.cfg.Sources = append(a.cfg.Sources[:i], a.cfg.Sources[i+1:]...)
	if a.cursor >= len(a.rows) {
		a.cursor = max(len(a.rows)-1, 0)
	}
	a.mu.Unlock()

	if err := a.cfg.Save(); err != nil {
		a.logf("could not save config: %v", err)
		return
	}
	a.logf("removed %s", name)
}

func deriveName(raw string) string {
	s := raw
	for _, p := range []string{"https://", "http://", "www."} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.IndexAny(s, "?&"); i > 0 {
		// keep a playlist id if that is all that distinguishes the url
		if strings.Contains(s, "list=") {
			if j := strings.Index(s, "list="); j >= 0 {
				id := s[j+5:]
				if k := strings.IndexAny(id, "&"); k > 0 {
					id = id[:k]
				}
				return truncate(strings.TrimSuffix(s[:i], "/")+" "+id, 40)
			}
		}
		s = s[:i]
	}
	return truncate(strings.TrimSuffix(s, "/"), 40)
}

func (a *App) scanOnly() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := library.TriggerScan(ctx, a.cfg); err != nil {
		a.logf("rescan failed: %v", err)
		return
	}
	a.logf("asked the server to rescan")
}
