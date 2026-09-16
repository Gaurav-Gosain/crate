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
	// posAt is when Position was last read from mpv. Between polls the
	// position is carried forward by the clock: mpv is asked four times a
	// second but the display draws fifteen, so without this the visualiser
	// analyses the same moment of audio four frames running and then jumps,
	// which reads as stuttering rather than as sound.
	posAt time.Time
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
	for range 100 {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		cmd.Process.Kill()
		// Reap it, or the dead process lingers as a zombie for the rest of
		// the session.
		cmd.Wait()
		return nil, fmt.Errorf("mpv did not open its control socket: %w", err)
	}
	p.conn = conn

	go p.readLoop()
	p.observe()
	return p, nil
}

// readLoop dispatches replies and events from mpv.
func (p *Player) readLoop() {
	// Whatever ends the loop, release anyone still waiting on a reply.
	// Without this a command issued just as the player closes sits out its
	// whole timeout for an answer that can never arrive.
	defer func() {
		p.mu.Lock()
		for id, ch := range p.pending {
			delete(p.pending, id)
			close(ch)
		}
		p.mu.Unlock()
	}()
	sc := bufio.NewScanner(p.conn)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	for sc.Scan() {
		var msg struct {
			RequestID int             `json:"request_id"`
			Data      json.RawMessage `json:"data"`
			Event     string          `json:"event"`
			Name      string          `json:"name"`
			Error     string          `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Event == "property-change" {
			p.applyProperty(msg.Name, msg.Data)
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

// applyProperty records a property mpv has told us about.
func (p *Player) applyProperty(name string, data json.RawMessage) {
	switch name {
	case "time-pos":
		var v float64
		if json.Unmarshal(data, &v) != nil {
			return
		}
		p.mu.Lock()
		p.state.Position = time.Duration(v * float64(time.Second))
		p.posAt = time.Now()
		p.mu.Unlock()
	case "duration":
		var v float64
		if json.Unmarshal(data, &v) != nil {
			return
		}
		p.mu.Lock()
		p.state.Duration = time.Duration(v * float64(time.Second))
		p.mu.Unlock()
	case "pause":
		var v bool
		if json.Unmarshal(data, &v) != nil {
			return
		}
		p.mu.Lock()
		if p.state.Path != "" {
			p.state.Playing = !v
			// Reset the clock the interpolation counts from, so a pause is
			// not later treated as time the track spent playing.
			p.posAt = time.Now()
		}
		p.mu.Unlock()
	}
}

// observe asks mpv to report these properties as they change.
//
// Polling for them meant three synchronous round trips every quarter second,
// and when any of them was slow the readings arrived late. The display carries
// the position forward from the last reading, so a late reading means the
// clock runs on and then snaps back when it lands: it visibly jumps. Letting
// mpv push the changes removes both the round trips and the jump.
func (p *Player) observe() {
	for i, prop := range []string{"time-pos", "duration", "pause"} {
		p.command("observe_property", i+1, prop)
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
		// Take the optimistic state back, or the interface shows a spinning
		// record for a track that never started.
		p.mu.Lock()
		p.state = State{}
		p.mu.Unlock()
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

// SeekTo jumps to an absolute position, which is what clicking or dragging a
// progress bar means.
func (p *Player) SeekTo(d time.Duration) {
	if d < 0 {
		d = 0
	}
	p.command("seek", d.Seconds(), "absolute")
}

// Stop halts playback but leaves mpv running for the next track.
func (p *Player) Stop() {
	p.command("stop")
	p.mu.Lock()
	p.state = State{}
	p.mu.Unlock()
}

// State returns a snapshot, with the position carried forward from the last
// reading so it advances smoothly between polls.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state
	if st.Playing && !p.posAt.IsZero() {
		// Carry the position forward between updates so it advances smoothly,
		// but only so far. mpv reports time-pos several times a second; if an
		// update goes missing, running the clock on indefinitely would show a
		// time the track never reached and then jump back when it resumed.
		st.Position += min(time.Since(p.posAt), time.Second)
		if st.Duration > 0 {
			st.Position = min(st.Position, st.Duration)
		}
	}
	return st
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
