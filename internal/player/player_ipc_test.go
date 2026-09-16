package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeMpv answers the IPC protocol enough to exercise the client: every
// command gets a success reply carrying its request id, and the server can
// push property-change events at will.
type fakeMpv struct {
	ln   net.Listener
	conn net.Conn
	mu   sync.Mutex
}

func newFakeMpv(t *testing.T) (*fakeMpv, *Player) {
	t.Helper()
	dir, err := os.MkdirTemp("", "crate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "mpv.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeMpv{ln: ln}
	ready := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(ready)
			return
		}
		f.mu.Lock()
		f.conn = conn
		f.mu.Unlock()
		close(ready)
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var req struct {
				RequestID int `json:"request_id"`
			}
			if json.Unmarshal(sc.Bytes(), &req) != nil || req.RequestID == 0 {
				continue
			}
			fmt.Fprintf(conn, `{"request_id":%d,"error":"success","data":1.5}`+"\n", req.RequestID)
		}
	}()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	p := &Player{conn: conn, sock: sock, pending: map[int]chan json.RawMessage{}}
	go p.readLoop()
	t.Cleanup(func() { p.Close(); ln.Close() })
	return f, p
}

func (f *fakeMpv) push(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		fmt.Fprintln(f.conn, event)
	}
}

// Commands are issued from more than one goroutine: the input reader and
// backgrounded track changes both talk to mpv. Replies must land with their
// own callers.
func TestCommandConcurrentRoundTrips(t *testing.T) {
	_, p := newFakeMpv(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				data, ok := p.command("get_property", "time-pos")
				if !ok {
					t.Error("command failed")
					return
				}
				if string(data) != "1.5" {
					t.Errorf("got %s", data)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// Observed properties arrive as events, not replies, and update the state
// the drawing loop reads.
func TestPropertyEventsUpdateState(t *testing.T) {
	f, p := newFakeMpv(t)
	p.mu.Lock()
	p.state = State{Path: "x", Playing: true}
	p.mu.Unlock()

	f.push(`{"event":"property-change","id":1,"name":"time-pos","data":12.5}`)
	f.push(`{"event":"property-change","id":2,"name":"duration","data":100}`)
	f.push(`{"event":"property-change","id":3,"name":"pause","data":true}`)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := p.State()
		if st.Position >= 12*time.Second && st.Duration == 100*time.Second && !st.Playing {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state never caught up: %+v", p.State())
}

func TestEndFileStopsPlaying(t *testing.T) {
	f, p := newFakeMpv(t)
	p.mu.Lock()
	p.state = State{Path: "x", Playing: true}
	p.mu.Unlock()
	f.push(`{"event":"end-file"}`)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !p.State().Playing {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("end-file never stopped the state")
}

// A command in flight when the player closes must fail promptly rather than
// sitting out its whole reply timeout.
func TestCloseReleasesPendingCommands(t *testing.T) {
	_, p := newFakeMpv(t)
	// Park a request the server will never answer.
	p.mu.Lock()
	p.reqID++
	id := p.reqID
	ch := make(chan json.RawMessage, 1)
	p.pending[id] = ch
	p.mu.Unlock()

	start := time.Now()
	go p.Close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected the channel closed, not a reply")
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("pending command not released on close")
	}
	if time.Since(start) > time.Second {
		t.Fatal("release took too long")
	}
}

// State interpolation must not run past the end of the track or backwards.
func TestStateCarriesPositionForwardBounded(t *testing.T) {
	p := &Player{pending: map[int]chan json.RawMessage{}}
	p.mu.Lock()
	p.state = State{Path: "x", Playing: true, Position: 10 * time.Second, Duration: 11 * time.Second}
	p.posAt = time.Now().Add(-5 * time.Second)
	p.mu.Unlock()
	if got := p.State().Position; got != 11*time.Second {
		t.Fatalf("position ran to %v, want clamped to the duration", got)
	}
}
