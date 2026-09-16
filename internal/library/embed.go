package library

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// EmbedLyrics writes timed words into the files themselves, on the server.
//
// Putting them in the track rather than in a file beside it is what makes a
// music server show them. Navidrome reads an external .lrc only when told to,
// and its own interface does not read one at all, so lyrics kept alongside
// the audio are invisible on a phone and in a browser. Embedded, they travel
// with the music and every client sees them.
//
// The tagging runs on the server rather than here. The files live there and
// nowhere else, so the alternative is fetching each one, rewriting it and
// sending it back, which moves the whole library twice to change a few
// hundred bytes in each file.
func EmbedLyrics(ctx context.Context, c *config.Config, words map[string]string, log func(string, ...any)) (int, error) {
	return embedTag(ctx, c, "lyrics", words, log)
}

// EmbedIDs writes the video each track came from into the track.
//
// Tracks downloaded before crate started recording this carry only a title,
// and a title is not enough to find the captions for a particular upload.
func EmbedIDs(ctx context.Context, c *config.Config, ids map[string]string, log func(string, ...any)) (int, error) {
	return embedTag(ctx, c, "youtube_id", ids, log)
}

func embedTag(ctx context.Context, c *config.Config, tag string, words map[string]string, log func(string, ...any)) (int, error) {
	if len(words) == 0 {
		return 0, nil
	}
	if c.Remote.Host == "" || c.Remote.Path == "" {
		return 0, fmt.Errorf("no remote configured: set remote.host and remote.path in %s", config.Path())
	}

	// Stage the words locally, one file per track, laid out under the same
	// relative paths so the server can pair them up without being told.
	dir, err := os.MkdirTemp("", "crate-lyrics-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)

	for rel, lrc := range words {
		dst := filepath.Join(dir, rel+".lrc")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(dst, []byte(lrc), 0o644); err != nil {
			return 0, err
		}
	}

	stage := "/tmp/crate-tag-stage"
	rsh := "ssh -o BatchMode=yes"
	if out, err := exec.CommandContext(ctx, "rsync",
		"-rlt", "--delete", "--no-perms", "--no-owner", "--no-group",
		"--timeout", "120", "-e", rsh,
		strings.TrimSuffix(dir, "/")+"/", c.Remote.Host+":"+stage+"/",
	).CombinedOutput(); err != nil {
		return 0, fmt.Errorf("send lyrics: %s", strings.TrimSpace(string(out)))
	}

	// Built by substitution rather than by Sprintf: the script is Python and
	// uses percent formatting of its own, which Sprintf would try to read as
	// its verbs and mangle.
	script := strings.NewReplacer(
		"__STAGE__", stage,
		"__ROOT__", strings.TrimSuffix(c.Remote.Path, "/"),
		"__TAG__", tag,
	).Replace(embedScript)
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", c.Remote.Host, "sudo python3 -")
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("embed lyrics: %s", strings.TrimSpace(out.String()))
	}

	done := 0
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "embedded ") {
			fmt.Sscanf(line, "embedded %d", &done)
		} else if line != "" && log != nil {
			log("   %s", line)
		}
	}
	return done, nil
}

// embedScript pairs each staged .lrc with its track and writes it into the
// tags. mutagen edits the tags in place, which matters: rewriting an opus
// file through ffmpeg would drop the cover art, because an ogg container
// cannot carry the picture stream back out again.
const embedScript = `
import os, sys
from mutagen import File

stage = "__STAGE__"
root = "__ROOT__"
done = 0
for dirpath, _, names in os.walk(stage):
    for n in names:
        if not n.endswith(".lrc"):
            continue
        lrc = os.path.join(dirpath, n)
        rel = os.path.relpath(lrc, stage)[: -len(".lrc")]
        track = os.path.join(root, rel)
        if not os.path.exists(track):
            print("missing on the server: " + rel[:70])
            continue
        try:
            words = open(lrc, encoding="utf-8").read()
            au = File(track)
            if au is None:
                continue
            au["__TAG__"] = [words]
            au.save()
            done += 1
        except Exception as e:
            print("could not tag %s: %s" % (rel[:50], e))
print("embedded %d" % done)
`
