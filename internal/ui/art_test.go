package ui

import (
	"strings"
	"testing"
)

// The protocol caps a single escape's payload, so a cover has to go out in
// pieces. Every piece but the last has to say more is coming, or the terminal
// draws a fraction of the image and waits forever for the rest.
func TestTransmitChunksTheImage(t *testing.T) {
	a := &art{id: 7101, px: 8, data: strings.Repeat("A", chunkSize*2+10)}
	out := a.displayCmd(rect{1, 1, 10, 5})

	if n := strings.Count(out, "\x1b_G"); n != 3 {
		t.Fatalf("want 3 escapes for a payload of 2 chunks plus a remainder, got %d", n)
	}
	if !strings.Contains(out, "a=T,i=7101,f=100,c=10,r=5,C=1,q=1,m=1;") {
		t.Fatalf("first chunk is missing its header or its continuation flag:\n%s", out[:120])
	}
	if strings.Count(out, "m=1;") != 2 {
		t.Fatalf("want two continuing chunks, got %d", strings.Count(out, "m=1;"))
	}
	if strings.Count(out, "m=0;") != 1 {
		t.Fatal("the last chunk must say the transmission is finished")
	}
}

// Drawing the image again where it already is would cost a few hundred
// kilobytes a frame and gain nothing, since the frame no longer erases it.
func TestDisplayOnlyOncePerPlace(t *testing.T) {
	a := &art{id: 7101, px: 8, data: strings.Repeat("A", 32)}
	at := rect{4, 5, 10, 5}
	if first := a.displayCmd(at); !strings.Contains(first, "a=T") {
		t.Fatalf("the first call must transmit and display, got %q", first)
	}
	if again := a.displayCmd(at); again != "" {
		t.Fatalf("drawing at the same spot should be a no-op, got %d bytes", len(again))
	}
	if moved := a.displayCmd(rect{4, 6, 10, 5}); moved == "" {
		t.Fatal("moving the image must draw it again")
	}
}

// A refusal has to reach the log. Suppressing it is how an image that the
// terminal declined becomes a blank rectangle with no explanation.
func TestGraphicsErrorIsReported(t *testing.T) {
	if got := graphicsError([]byte("\x1b_Gi=7101;EBADF: something went wrong\x1b\\")); got == "" {
		t.Fatal("an error reply must be surfaced")
	}
	if got := graphicsError([]byte("\x1b_Gi=7101;OK\x1b\\")); got != "" {
		t.Fatalf("a success reply must stay quiet, got %q", got)
	}
}

func TestSkipAPCConsumesAReply(t *testing.T) {
	buf := []byte("\x1b_Gi=7101;OK\x1b\\rest")
	n, partial := skipAPC(buf)
	if partial || n != len("\x1b_Gi=7101;OK\x1b\\") {
		t.Fatalf("n=%d partial=%v", n, partial)
	}
	if string(buf[n:]) != "rest" {
		t.Fatalf("left %q", buf[n:])
	}
}

func TestSkipAPCWaitsForTheTerminator(t *testing.T) {
	if n, partial := skipAPC([]byte("\x1b_Gi=7101;OK")); !partial || n != 0 {
		t.Fatalf("an unterminated reply must be waited for, got n=%d partial=%v", n, partial)
	}
}

func TestSkipAPCIgnoresOtherInput(t *testing.T) {
	if n, partial := skipAPC([]byte("hello")); n != 0 || partial {
		t.Fatal("plain input must pass straight through")
	}
}
