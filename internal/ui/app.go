// Package ui is crate's terminal interface. It is written directly against
// the terminal rather than a framework, matching youterm.
package ui

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/config"
	"golang.org/x/term"
)

type status int

const (
	idle status = iota
	running
	succeeded
	failed
)

func (s status) label() (string, string) {
	switch s {
	case running:
		return "working", warn
	case succeeded:
		return "synced", ok
	case failed:
		return "failed", bad
	default:
		return "idle", muted
	}
}

type row struct {
	src    config.Source
	state  status
	pct    float64
	detail string
}

// App owns the terminal and all mutable view state.
type App struct {
	cfg *config.Config
	tty *os.File
	fd  int

	mu     sync.Mutex
	rows   []row
	logs   []string
	cursor int
	busy   bool

	// input mode: when prompt is non-empty the footer is an editable field
	prompt string
	buf    []rune
	onSubm func(string)

	redrawCh chan struct{}
	quit     chan struct{}
	cancel   context.CancelFunc
}

const maxLogs = 500

func New(cfg *config.Config) *App {
	a := &App{
		cfg:      cfg,
		redrawCh: make(chan struct{}, 1),
		quit:     make(chan struct{}),
	}
	for _, s := range cfg.Sources {
		a.rows = append(a.rows, row{src: s, state: idle})
	}
	return a
}

func (a *App) redraw() {
	select {
	case a.redrawCh <- struct{}{}:
	default:
	}
}

func (a *App) logf(format string, args ...any) {
	line := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	a.mu.Lock()
	a.logs = append(a.logs, line)
	if len(a.logs) > maxLogs {
		a.logs = a.logs[len(a.logs)-maxLogs:]
	}
	a.mu.Unlock()
	a.redraw()
}

// Run takes over the terminal until the user quits.
func (a *App) Run() error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	a.tty = tty
	a.fd = int(tty.Fd())

	old, err := term.MakeRaw(a.fd)
	if err != nil {
		return err
	}
	defer term.Restore(a.fd, old)

	// Alternate screen, cursor hidden. Restored on every exit path.
	tty.WriteString("\x1b[?1049h\x1b[?25l")
	defer tty.WriteString("\x1b[?25h\x1b[?1049l")

	go a.readKeys()

	// Resize handling: redraw on SIGWINCH rather than polling the size.
	go a.watchResize()

	a.logf("crate ready. %d source(s). press ? for keys", len(a.rows))
	a.draw()

	for {
		select {
		case <-a.quit:
			if a.cancel != nil {
				a.cancel()
			}
			return nil
		case <-a.redrawCh:
			a.draw()
		}
	}
}
