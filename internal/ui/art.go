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
	// placed records the geometry the image was last drawn at. The frame no
	// longer erases the cells the image sits in, so once placed it stays
	// there; repeating the placement every frame achieves nothing and, if
	// the terminal has dropped the image, produces an error every frame.
	placed rect
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

// displayCmd transmits the image and draws it where the cursor is.
//
// Transmitting and displaying in one command deliberately avoids relying on
// the terminal keeping the image addressable afterwards. Storing it with a=t
// and placing it later by id looked tidier and failed: every placement came
// back as an image that does not exist. Drawing it as part of the same
// command asks nothing of the terminal beyond the moment it arrives.
//
// This runs once per track, inside the frame, so the placement lands where
// the cursor is. It is a few hundred kilobytes, which is why it must not
// happen again on every frame.
//
// q=1 suppresses the success reply but keeps errors, so a refusal is visible
// rather than silent. That matters: an earlier version sent an image the
// terminal declined and left a blank rectangle with nothing to explain it.
func (a *art) displayCmd(at rect) string {
	if a.placed == at {
		return ""
	}
	a.placed = at
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
			fmt.Fprintf(&b, "\x1b_Ga=T,i=%d,f=100,c=%d,r=%d,C=1,q=1,m=%d;%s\x1b\\",
				a.id, at.w, at.h, more, part)
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
