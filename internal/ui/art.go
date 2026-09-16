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
// The image is cropped square first: the covers on these files are video
// thumbnails, and a 16:9 picture in a square hole either stretches faces or
// leaves bars down the sides. It is encoded as a PNG rather than sent as raw
// pixels, which is a quarter of the bytes and so a quarter of the chunks.
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
		"-f", "image2",
		"-c:v", "png",
		"-",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("no cover: %s", strings.TrimSpace(stderr.String()))
	}
	// A PNG starts with a fixed signature. Checking it catches ffmpeg writing
	// a diagnostic to stdout instead of an image, which would otherwise be
	// sent to the terminal as if it were pixels.
	if len(raw) < 8 || string(raw[1:4]) != "PNG" {
		return nil, fmt.Errorf("cover is not a png (%d bytes)", len(raw))
	}

	return &art{
		id:   id,
		data: base64.StdEncoding.EncodeToString(raw),
		px:   px,
	}, nil
}

// chunkSize is the largest payload the protocol allows in one escape.
const chunkSize = 4096

// transmitCmd stores the image in the terminal without displaying it.
//
// Transmission is separated from placement, and sent on its own rather than
// as part of a frame, for two reasons. A frame is wrapped in a synchronised
// update, and burying a few hundred kilobytes of image inside one asks the
// terminal to buffer the lot before it may draw anything. And every frame
// begins by clearing the screen, which discards placements; keeping the
// stored image and the placement separate means a cleared screen costs only
// the placement, which the next frame puts back for a handful of bytes.
//
// q=1 suppresses the success reply but keeps errors, so a refusal is visible
// rather than silent. That matters: the first version of this sent an image
// the terminal declined and left a blank rectangle with nothing to explain it.
func (a *art) transmitCmd() string {
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
			// f=100 is PNG. The terminal decodes it, which costs it nothing
			// and saves sending three quarters of the bytes.
			fmt.Fprintf(&b, "\x1b_Ga=t,i=%d,f=100,q=1,m=%d;%s\x1b\\",
				a.id, more, part)
			first = false
			continue
		}
		fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, part)
	}
	return b.String()
}

// place draws the stored image at the cursor. C=1 leaves the cursor alone so
// text can be drawn beside it.
func (a *art) place(cols, rows int) string {
	return fmt.Sprintf("\x1b_Ga=p,i=%d,c=%d,r=%d,C=1,q=1;\x1b\\", a.id, cols, rows)
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
