package ui

import (
	"strings"
	"testing"
)

// The protocol caps a single escape's payload, so a cover has to go out in
// pieces. Every piece but the last has to say more is coming, or the terminal
// draws a fraction of the image and waits forever for the rest.
func TestPlaceChunksTheImage(t *testing.T) {
	a := &art{id: 7101, px: 8, data: strings.Repeat("A", chunkSize*2+10)}
	out := a.place(10, 5)

	if n := strings.Count(out, "\x1b_G"); n != 3 {
		t.Fatalf("want 3 escapes for a payload of 2 chunks plus a remainder, got %d", n)
	}
	if !strings.Contains(out, "a=T,i=7101,f=24,s=8,v=8,c=10,r=5,C=1,q=2,m=1;") {
		t.Fatalf("first chunk is missing its header or its continuation flag:\n%s", out[:120])
	}
	if strings.Count(out, "m=1;") != 2 {
		t.Fatalf("want two continuing chunks, got %d", strings.Count(out, "m=1;"))
	}
	if strings.Count(out, "m=0;") != 1 {
		t.Fatal("the last chunk must say the transmission is finished")
	}
}

// After the pixels have been sent once, later frames only place the image.
// Re-sending it every frame leaves the terminal holding a copy per frame.
func TestPlaceOnlyTransmitsOnce(t *testing.T) {
	a := &art{id: 7101, px: 8, data: strings.Repeat("A", 32)}
	first := a.place(10, 5)
	second := a.place(10, 5)
	if !strings.Contains(first, "a=T") {
		t.Fatal("the first call must transmit")
	}
	if strings.Contains(second, "a=T") {
		t.Fatal("the second call must not transmit again")
	}
	if !strings.Contains(second, "a=p,i=7101,c=10,r=5") {
		t.Fatalf("the second call must place the stored image, got %q", second)
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
