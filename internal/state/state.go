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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

const (
	sourcesFile = "sources.json"
	archiveFile = "archive"
)

type shared struct {
	Sources []config.Source `json:"sources"`
}

// Pull copies shared state down and applies it to c.
//
// Missing remote state is not an error: the first machine to run simply has
// nothing to fetch, and its own state becomes the shared one on the next push.
func Pull(ctx context.Context, c *config.Config) error {
	dir, err := localDir(c)
	if err != nil {
		return err
	}
	if err := fetch(ctx, c, sourcesFile, filepath.Join(dir, sourcesFile)); err != nil {
		return err
	}
	if err := fetch(ctx, c, archiveFile, c.ArchivePath()); err != nil {
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
	c.Sources = sh.Sources
	return c.Save()
}

// Push sends the local sources and archive back up.
func Push(ctx context.Context, c *config.Config) error {
	dir, err := localDir(c)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(shared{Sources: c.Sources}, "", "  ")
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

func localDir(c *config.Config) (string, error) {
	dir := filepath.Join(filepath.Dir(config.Path()), "state")
	return dir, os.MkdirAll(dir, 0o700)
}

func fetch(ctx context.Context, c *config.Config, name, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	src := fmt.Sprintf("%s:%s/%s", c.Remote.Host, strings.TrimSuffix(c.Remote.StatePath(), "/"), name)
	cmd := exec.CommandContext(ctx, "rsync", "-qt", "--timeout", "60", src, dest)
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
