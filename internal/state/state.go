// Package state shares crate's bookkeeping between machines.
//
// Two things have to travel with you rather than living on one laptop: the
// list of sources, and the download archive. Without the archive a second
// device re-downloads the entire library, because it has no idea what the
// first one already fetched.
//
// State is kept next to the music on the same remote, not on rsync.net
// directly. That remote is already mirrored to rsync.net nightly, so the state
// inherits the same offsite copy without crate needing a second set of
// credentials.
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

const (
	sourcesFile = "sources.json"
	archiveFile = "archive"
)

type shared struct {
	Sources []config.Source `json:"sources"`
	// Removed carries the tombstones. Sources and tombstones have to travel
	// together: merging two source lists without them turns every deletion
	// into a temporary one, because whichever device still lists a source
	// reintroduces it on its next push.
	Removed map[string]string `json:"removed,omitempty"`
}

// merge combines the shared list with the local one. Sources are unioned so
// that an addition on either side survives, then tombstoned entries are
// dropped, so that a deletion on either side also survives. A tombstone is
// beaten only by adding the source again, which clears it.
func merge(remote shared, c *config.Config) []config.Source {
	tombs := map[string]string{}
	maps.Copy(tombs, remote.Removed)
	maps.Copy(tombs, c.Removed)

	var out []config.Source
	seen := map[string]bool{}
	for _, s := range slices.Concat(remote.Sources, c.Sources) {
		key := config.NormalizeURL(s.URL)
		if seen[key] || tombs[key] != "" {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	c.Removed = tombs
	return out
}

// Pull copies shared state down and applies it to c.
//
// Missing remote state is not an error: the first machine to run simply has
// nothing to fetch, and its own state becomes the shared one on the next push.
func Pull(ctx context.Context, c *config.Config) error {
	dir, err := localDir()
	if err != nil {
		return err
	}
	if err := fetch(ctx, c, sourcesFile, filepath.Join(dir, sourcesFile), false); err != nil {
		return err
	}
	// The archive is fetched with --update so an older remote copy cannot
	// clobber a newer local one. That happened two ways: a run that
	// downloaded but crashed before pushing lost its record at the next
	// start, and a removal that pruned ids locally had them resurrected by
	// the stale remote copy before the pruned version was pushed.
	if err := fetch(ctx, c, archiveFile, c.ArchivePath(), true); err != nil {
		return err
	}

	b, err := os.ReadFile(filepath.Join(dir, sourcesFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var sh shared
	if err := json.Unmarshal(b, &sh); err != nil {
		return fmt.Errorf("shared sources: %w", err)
	}
	c.Sources = merge(sh, c)
	return c.Save()
}

// Push sends the local sources and archive back up.
func Push(ctx context.Context, c *config.Config) error {
	dir, err := localDir()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(shared{Sources: c.Sources, Removed: c.Removed}, "", "  ")
	if err != nil {
		return err
	}
	local := filepath.Join(dir, sourcesFile)
	if err := os.WriteFile(local, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := send(ctx, c, local, sourcesFile); err != nil {
		return err
	}
	// The archive only exists once something has been downloaded.
	if _, err := os.Stat(c.ArchivePath()); err == nil {
		if err := send(ctx, c, c.ArchivePath(), archiveFile); err != nil {
			return err
		}
	}
	return nil
}

func localDir() (string, error) {
	dir := filepath.Join(filepath.Dir(config.Path()), "state")
	return dir, os.MkdirAll(dir, 0o700)
}

// fetch copies one state file down. keepNewer skips the copy when the local
// file is newer than the remote one, for files that are live local state
// rather than staging.
func fetch(ctx context.Context, c *config.Config, name, dest string, keepNewer bool) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	src := fmt.Sprintf("%s:%s/%s", c.Remote.Host, strings.TrimSuffix(c.Remote.StatePath(), "/"), name)
	args := []string{"-qt", "--timeout", "60"}
	if keepNewer {
		args = append(args, "--update")
	}
	cmd := exec.CommandContext(ctx, "rsync", append(args, src, dest)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Exit 23 covers "no such file", which is the normal first-run case.
		if strings.Contains(string(out), "No such file") || strings.Contains(string(out), "change_dir") {
			return nil
		}
		return fmt.Errorf("pull %s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

func send(ctx context.Context, c *config.Config, local, name string) error {
	dst := fmt.Sprintf("%s:%s/%s", c.Remote.Host, strings.TrimSuffix(c.Remote.StatePath(), "/"), name)
	cmd := exec.CommandContext(ctx, "rsync", "-qt", "--mkpath",
		"--no-perms", "--no-owner", "--no-group", "--timeout", "60", local, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("push %s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}
