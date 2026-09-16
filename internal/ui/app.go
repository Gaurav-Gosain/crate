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
	"github.com/Gaurav-Gosain/crate/internal/library"
	"github.com/Gaurav-Gosain/crate/internal/lyrics"
	"github.com/Gaurav-Gosain/crate/internal/player"
	"github.com/Gaurav-Gosain/crate/internal/theme"
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
	src   config.Source
	state status
	pct   float64

	// live detail, so a working row can say what it is actually doing
	phase library.Phase
	file  string // current track, without directory
	speed string
	eta   string
	done  int // tracks finished in this run
	note  string
}

type mode int

const (
	modeList mode = iota
	modeSearch
	modePlay
	modeOverlay
)

// write sends a string to the terminal, one writer at a time.
func (a *App) write(s string) {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	a.tty.WriteString(s)
}

// App owns the terminal and all mutable view state.
type App struct {
	cfg *config.Config
	tty *os.File
	fd  int

	mu     sync.Mutex
	mode   mode
	rows   []row
	logs   []string
	cursor int
	busy   bool

	// search results overlay
	results   []library.Result
	rcursor   int
	searching bool
	query     string

	// input mode: when prompt is non-empty the footer is an editable field
	prompt string
	buf    []rune
	onSubm func(string)

	// started is when the current run began, for the elapsed clock.
	started time.Time

	// play mode: the record player, the library it plays from, and the
	// decoded audio behind the visualiser.
	player       *player.Player
	songs        []library.Song
	playCursor   int
	playLoading  bool
	nowPlaying   library.Song
	spectrum     *player.Spectrum
	spectrumStop context.CancelFunc
	cover        *art
	coverFor     string

	// Lyrics for the playing track.
	lyrics     *lyrics.Lyrics
	lyricsFor  string
	lyricsNote string

	// panes is which of play mode's optional panes are on screen. It starts
	// from the config and is edited live by the toggles, which write the
	// change back so it survives a restart.
	panes paneSet

	// Regions the mouse can act on, recorded as the frame is drawn. Working
	// them out again on a click would mean duplicating the layout arithmetic
	// and keeping the two copies in step.
	hitList     rect
	hitListTop  int
	hitPlayRows []int
	hitProgress rect
	hitSources  rect

	// ov is the command palette or theme picker when one is open. prevMode
	// is what to go back to when it closes.
	ov       *overlayState
	prevMode mode

	// wmu serialises writes to the terminal. Several goroutines write: the
	// drawing loop, and whatever loads album art or releases it. A write is
	// not atomic, so without this a large one is split and the others land
	// inside it. That corrupts both: the frame arrives spliced with image
	// data, and the image arrives truncated, which the terminal reports
	// later as an image that does not exist.
	wmu sync.Mutex

	redrawCh chan struct{}
	quit     chan struct{}
	cancel   context.CancelFunc
}

const maxLogs = 500

func New(cfg *config.Config) *App {
	if err := theme.Set(cfg.Theme); err != nil {
		// A bad name in the config is worth saying out loud; falling back
		// silently looks like the theme simply had no effect.
		fmt.Fprintf(os.Stderr, "crate: %v\n", err)
	}
	applyTheme()
	a := &App{
		cfg:      cfg,
		redrawCh: make(chan struct{}, 1),
		quit:     make(chan struct{}),
	}
	// A bad pane name is reported the same way a bad theme is: out loud, on
	// both stderr and the activity log, with the recognised panes kept. A
	// typo must not silently blank part of the play view.
	on, perr := cfg.Play.Enabled()
	a.panes = paneSetFrom(on)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "crate: %v\n", perr)
		a.logf("%v", perr)
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
	tty.WriteString("\x1b[?1049h\x1b[?25l" + enableMouse)
	defer tty.WriteString(disableMouse + "\x1b[?25h\x1b[?1049l")

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
