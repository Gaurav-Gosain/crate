// Package player plays audio by driving mpv over its JSON IPC socket.
//
// mpv rather than ffplay because ffplay offers no way to ask it anything: no
// position, no duration, no pause without a terminal attached to it. A player
// that cannot report where it is cannot drive a progress bar or a spinning
// record. mpv exposes all of it over a unix socket, and is already a
// dependency of nothing else here, so it is asked for rather than assumed.
package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// State is a snapshot of what the player is doing, safe to read from the
// drawing goroutine.
type State struct {
	Path     string
	Playing  bool
	Position time.Duration
	Duration time.Duration
	Err      error
}

// Player wraps one long-lived mpv process.
type Player struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	conn net.Conn
	sock string

	state State
	reqID int
	// pending maps a request id to the channel waiting for its reply.
	pending map[int]chan json.RawMessage
	closed  bool
}

// New starts mpv in idle mode with no window and waits for its socket.
func New() (*Player, error) {
	if _, err := exec.LookPath("mpv"); err != nil {
		return nil, fmt.Errorf("mpv is not installed: play mode needs it for playback")
	}
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("crate-mpv-%d.sock", os.Getpid()))
	os.Remove(sock)

	cmd := exec.Command("mpv",
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--really-quiet",
		// Keep decoding cheap; this is a background process behind a TUI.
		"--audio-display=no",
		"--input-ipc-server="+sock,
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mpv: %w", err)
	}

	p := &Player{cmd: cmd, sock: sock, pending: map[int]chan json.RawMessage{}}

	// mpv creates the socket a moment after starting.
	var conn net.Conn
	var err error
	for i := 0; i < 100; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("mpv did not open its control socket: %w", err)
	}
	p.conn = conn

	go p.readLoop()
	go p.pollLoop()
	return p, nil
}

// readLoop dispatches replies and events from mpv.
func (p *Player) readLoop() {
	sc := bufio.NewScanner(p.conn)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	for sc.Scan() {
		var msg struct {
			RequestID int             `json:"request_id"`
			Data      json.RawMessage `json:"data"`
			Event     string          `json:"event"`
			Error     string          `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Event == "end-file" {
			p.mu.Lock()
			p.state.Playing = false
			p.mu.Unlock()
			continue
		}
		if msg.RequestID == 0 {
			continue
		}
		p.mu.Lock()
		ch, ok := p.pending[msg.RequestID]
		delete(p.pending, msg.RequestID)
		p.mu.Unlock()
		if ok {
			if msg.Error != "success" {
				close(ch)
			} else {
				ch <- msg.Data
				close(ch)
			}
		}
	}
}

// pollLoop keeps position and duration fresh. mpv can push property changes,
// but polling four times a second is simpler and costs nothing measurable,
// and the display only needs to be roughly current.
func (p *Player) pollLoop() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		if pos, ok := p.getFloat("time-pos"); ok {
			p.mu.Lock()
			p.state.Position = time.Duration(pos * float64(time.Second))
			p.mu.Unlock()
		}
		if dur, ok := p.getFloat("duration"); ok {
			p.mu.Lock()
			p.state.Duration = time.Duration(dur * float64(time.Second))
			p.mu.Unlock()
		}
		if paused, ok := p.getBool("pause"); ok {
			p.mu.Lock()
			if p.state.Path != "" {
				p.state.Playing = !paused
			}
			p.mu.Unlock()
		}
	}
}

// command sends one IPC command and waits briefly for its reply.
func (p *Player) command(args ...any) (json.RawMessage, bool) {
	p.mu.Lock()
	if p.closed || p.conn == nil {
		p.mu.Unlock()
		return nil, false
	}
	p.reqID++
	id := p.reqID
	ch := make(chan json.RawMessage, 1)
	p.pending[id] = ch
	payload, err := json.Marshal(map[string]any{"command": args, "request_id": id})
	if err != nil {
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, false
	}
	conn := p.conn
	p.mu.Unlock()

	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, false
	}
	select {
	case data, ok := <-ch:
		return data, ok
	case <-time.After(2 * time.Second):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, false
	}
}

func (p *Player) getFloat(prop string) (float64, bool) {
	data, ok := p.command("get_property", prop)
	if !ok || len(data) == 0 {
		return 0, false
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, false
	}
	return v, true
}

func (p *Player) getBool(prop string) (bool, bool) {
	data, ok := p.command("get_property", prop)
	if !ok || len(data) == 0 {
		return false, false
	}
	var v bool
	if err := json.Unmarshal(data, &v); err != nil {
		return false, false
	}
	return v, true
}

// Play loads a file or URL and starts it.
func (p *Player) Play(path string) error {
	// Only a local path can be checked ahead of time. Statting a URL fails
	// with "no such file or directory", which is a confusing way to report a
	// stream that would have played perfectly well.
	if !strings.Contains(path, "://") {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("cannot play: %w", err)
		}
	}
	p.mu.Lock()
	p.state = State{Path: path, Playing: true}
	p.mu.Unlock()
	if _, ok := p.command("loadfile", path, "replace"); !ok {
		return fmt.Errorf("mpv refused the file")
	}
	p.command("set_property", "pause", false)
	return nil
}

// Toggle pauses or resumes.
func (p *Player) Toggle() {
	p.command("cycle", "pause")
}

// Seek moves by a relative number of seconds.
func (p *Player) Seek(d time.Duration) {
	p.command("seek", d.Seconds(), "relative")
}

// Stop halts playback but leaves mpv running for the next track.
func (p *Player) Stop() {
	p.command("stop")
	p.mu.Lock()
	p.state = State{}
	p.mu.Unlock()
}

// State returns a snapshot.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Close shuts mpv down and removes its socket.
func (p *Player) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	conn := p.conn
	cmd := p.cmd
	sock := p.sock
	p.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
	os.Remove(sock)
}
