package library

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// Sync mirrors the local library to the remote music server.
//
// rsync is the right tool here rather than re-uploading: only new or changed
// files cross the wire, so a sync after one new album costs one album. There
// is deliberately no --delete, because the remote may hold music that did not
// come from crate and silently removing it would be rude.
func Sync(ctx context.Context, c *config.Config, ev chan<- Event) error {
	if c.Remote.Host == "" || c.Remote.Path == "" {
		return fmt.Errorf("no remote configured: set remote.host and remote.path in %s", config.Path())
	}

	src := strings.TrimSuffix(c.Library, "/") + "/"
	dst := fmt.Sprintf("%s:%s", c.Remote.Host, strings.TrimSuffix(c.Remote.Path, "/")+"/")

	args := []string{
		// -a would try to preserve permissions and ownership, which fails
		// when the destination directory belongs to the music server's user
		// rather than the SSH user. Copy recursively, keep times and links,
		// and let the remote's own umask and setgid bit decide the rest.
		"-rltz",
		"--no-perms",
		"--no-owner",
		"--no-group",
		"--omit-dir-times",
		"--partial",
		"--info=progress2,name0",
		"--human-readable",
		// The archive is crate's bookkeeping, not music.
		"--exclude", ".crate-archive",
		"--exclude", ".DS_Store",
		"--timeout", "120",
		src, dst,
	}

	cmd := exec.CommandContext(ctx, "rsync", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("rsync: %w", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Split(scanProgress)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		e := Event{Source: "mirror", Text: line, Pct: -1, Phase: PhaseMirroring}
		if m := pctRe.FindStringSubmatch(line); m != nil {
			fmt.Sscanf(m[1], "%f", &e.Pct)
		}
		if m := speedRe.FindStringSubmatch(line); m != nil {
			e.Speed = strings.TrimSpace(m[1])
		}
		ev <- e
	}
	return cmd.Wait()
}

// scanProgress splits on carriage returns as well as newlines, because rsync
// redraws its progress line with \r and a plain line scanner would buffer the
// whole transfer into one token.
func scanProgress(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// TriggerScan asks the music server to index new files immediately rather than
// waiting for its own schedule. Navidrome speaks the Subsonic API, where the
// password is sent as a salted MD5 token instead of in the clear.
func TriggerScan(ctx context.Context, c *config.Config) error {
	if c.Remote.ScanURL == "" {
		return nil
	}
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	s := hex.EncodeToString(salt)
	sum := md5.Sum([]byte(c.Remote.ScanPass + s))

	q := url.Values{
		"u": {c.Remote.ScanUser},
		"t": {hex.EncodeToString(sum[:])},
		"s": {s},
		"v": {"1.16.1"},
		"c": {"crate"},
		"f": {"json"},
	}
	endpoint := strings.TrimSuffix(c.Remote.ScanURL, "/") + "/rest/startScan?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	cl := &http.Client{Timeout: 20 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("scan trigger: http %d", resp.StatusCode)
	}
	return nil
}

// ClearStaging empties the local library after a successful mirror.
//
// Only call this once the mirror has succeeded. With KeepLocal off the remote
// is the only copy, so clearing before a confirmed push would lose the
// download. The archive lives beside the config, not in here, so what has
// already been fetched survives.
func ClearStaging(c *config.Config) (int, error) {
	if c.Keep() {
		return 0, nil
	}
	root := c.Library
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		// Never touch dotfiles: older versions kept the archive in here.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
