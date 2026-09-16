package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	id      uint32
	path    string
	pathB64 string
	px      int
	sent    atomic.Bool
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
	path := filepath.Join(os.TempDir(), fmt.Sprintf("crate-art-%d-%d.rgb", os.Getpid(), id))

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-v", "error",
		"-i", src,
		"-an",
		"-frames:v", "1",
		"-vf", fmt.Sprintf("crop='min(iw,ih)':'min(iw,ih)',scale=%d:%d", px, px),
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-y", path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("no cover: %s", strings.TrimSpace(string(out)))
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		os.Remove(path)
		return nil, fmt.Errorf("no cover in this file")
	}

	return &art{
		id:      id,
		path:    path,
		pathB64: base64.StdEncoding.EncodeToString([]byte(path)),
		px:      px,
	}, nil
}

// place returns the escape sequence drawing the cover at the cursor.
//
// The first call transmits the pixels and displays them; every later call only
// places the image the terminal already holds. Transmitting each frame would
// re-read the file and, worse, leave the terminal holding a new copy of the
// image every time, which is how a picture viewer ends up using a gigabyte of
// the terminal's memory in a minute.
//
// C=1 leaves the cursor alone so text can be drawn beside the image.
func (a *art) place(cols, rows int) string {
	if a.sent.CompareAndSwap(false, true) {
		return fmt.Sprintf("\x1b_Ga=T,i=%d,t=t,f=24,s=%d,v=%d,c=%d,r=%d,C=1,q=2;%s\x1b\\",
			a.id, a.px, a.px, cols, rows, a.pathB64)
	}
	return fmt.Sprintf("\x1b_Ga=p,i=%d,c=%d,r=%d,C=1,q=2;\x1b\\", a.id, cols, rows)
}

// deleteCmd frees the image inside the terminal. d=I deletes by id and
// releases the pixel data; d=i alone only removes the placement.
func (a *art) deleteCmd() string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2;\x1b\\", a.id)
}

// cleanup removes the temp file. With t=t the terminal usually deletes it
// after reading, but a cover that was fetched and never drawn still has one.
func (a *art) cleanup() {
	if a.path != "" {
		os.Remove(a.path)
	}
}
