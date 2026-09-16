package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
)

// Album art is drawn with the kitty graphics protocol, which is the only way
// to get real pixels into a terminal without pretending half blocks are an
// image.
//
// Ids are taken from a high range so they cannot collide with anything else
// the terminal is holding, and each track gets a fresh one so a stale image is
// never shown under a new title.
const artImageIDBase = 7100

var nextArtID atomic.Uint32

// art is one decoded cover, ready to send.
type art struct {
	id   uint32
	data string // the pixels, base64 encoded
	px   int
	sent atomic.Bool
}

// graphicsSupported reports whether the terminal understands the protocol.
//
// This is decided from the environment rather than by querying the terminal.
// A query means writing an escape sequence and reading the reply out of the
// same stream the key handler is reading, and getting that wrong swallows a
// keypress. The environment is right for every terminal that actually
// implements this.
func graphicsSupported() bool {
	if os.Getenv("CRATE_NO_GRAPHICS") != "" {
		return false
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	term := os.Getenv("TERM")
	if strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") {
		return true
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm", "kitty":
		return true
	}
	return false
}

// loadArt pulls the cover out of a track and prepares it for sending.
//
// ffmpeg writes raw RGB rather than a PNG so nothing has to be decoded again
// on this side. The image is cropped square first: the covers on these files
// are video thumbnails, and a 16:9 picture in a square hole either stretches
// faces or leaves bars down the sides.
func loadArt(ctx context.Context, src string, px int) (*art, error) {
	if px < 32 {
		px = 32
	}
	id := artImageIDBase + nextArtID.Add(1)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-v", "error",
		"-i", src,
		"-an",
		"-frames:v", "1",
		"-vf", fmt.Sprintf("crop='min(iw,ih)':'min(iw,ih)',scale=%d:%d", px, px),
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("no cover: %s", strings.TrimSpace(stderr.String()))
	}
	if want := px * px * 3; len(raw) != want {
		return nil, fmt.Errorf("cover is %d bytes, expected %d", len(raw), want)
	}

	return &art{
		id:   id,
		data: base64.StdEncoding.EncodeToString(raw),
		px:   px,
	}, nil
}

// chunkSize is the largest payload the protocol allows in one escape.
const chunkSize = 4096

// place returns the escape sequence drawing the cover at the cursor.
//
// The pixels are sent inline rather than by writing a temp file and handing
// over its path. The file route needs the terminal to agree that the path is
// somewhere it is willing to read, which depends on the terminal, the
// platform's idea of a temporary directory, and whether the file survives long
// enough to be read. Sending the bytes has none of those failure modes, and
// they are only sent once.
//
// The first call transmits and displays; every later call only places the
// image the terminal already holds. Transmitting every frame would leave the
// terminal holding a fresh copy each time, which is how a picture viewer ends
// up using a gigabyte of the terminal's memory in a minute.
//
// C=1 leaves the cursor alone so text can be drawn beside the image.
func (a *art) place(cols, rows int) string {
	if !a.sent.CompareAndSwap(false, true) {
		return fmt.Sprintf("\x1b_Ga=p,i=%d,c=%d,r=%d,C=1,q=2;\x1b\\", a.id, cols, rows)
	}

	var b strings.Builder
	data := a.data
	first := true
	for len(data) > 0 {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}
		part := data[:n]
		data = data[n:]
		more := 0
		if len(data) > 0 {
			more = 1
		}
		if first {
			fmt.Fprintf(&b, "\x1b_Ga=T,i=%d,f=24,s=%d,v=%d,c=%d,r=%d,C=1,q=2,m=%d;%s\x1b\\",
				a.id, a.px, a.px, cols, rows, more, part)
			first = false
			continue
		}
		fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, part)
	}
	return b.String()
}

// deleteCmd frees the image inside the terminal. d=I deletes by id and
// releases the pixel data; d=i alone only removes the placement.
func (a *art) deleteCmd() string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2;\x1b\\", a.id)
}

// cleanup releases the encoded pixels. Nothing is written to disk, so there
// is no file to remove; dropping the data keeps a long session from holding
// on to every cover it has decoded.
func (a *art) cleanup() {
	a.data = ""
}
