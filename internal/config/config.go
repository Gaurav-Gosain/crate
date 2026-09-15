// Package config holds crate's on-disk settings.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// Source is one thing to keep in sync: a playlist, album or channel URL.
type Source struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// Added tracks land under Library/Dir when set, otherwise yt-dlp's
	// metadata decides the artist and album folders.
	Dir string `json:"dir,omitempty"`
}

// Remote describes where the library is mirrored to.
type Remote struct {
	// SSH host, as understood by ssh and rsync. An entry in ~/.ssh/config
	// is the tidiest way to express this.
	Host string `json:"host"`
	Path string `json:"path"`
	// ScanURL, when set, is pinged after a successful sync so the music
	// server indexes new files without waiting for its own schedule.
	ScanURL  string `json:"scan_url,omitempty"`
	ScanUser string `json:"scan_user,omitempty"`
	ScanPass string `json:"scan_pass,omitempty"`
}

type Config struct {
	// Library is the local staging directory. Downloads land here and are
	// mirrored to the remote; it doubles as an offline copy.
	Library string `json:"library"`
	// Format is passed to yt-dlp's --audio-format.
	Format string `json:"format"`
	// Quality is yt-dlp's --audio-quality, 0 is best.
	Quality string `json:"quality"`
	// Parallel bounds how many yt-dlp processes run at once, across all
	// sources. Downloads are network bound, so this can comfortably exceed
	// the core count.
	Parallel int      `json:"parallel"`
	Remote   Remote   `json:"remote"`
	Sources  []Source `json:"sources"`
}

func Path() string {
	return filepath.Join(xdg.ConfigHome, "crate", "crate.json")
}

func defaults() Config {
	return Config{
		Library:  filepath.Join(xdg.UserDirs.Music, "crate"),
		Format:   "opus",
		Quality:  "0",
		Parallel: 8,
	}
}

// Load reads the config, creating one with defaults if absent.
func Load() (*Config, error) {
	p := Path()
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		c := defaults()
		if err := c.Save(); err != nil {
			return nil, err
		}
		return &c, nil
	}
	if err != nil {
		return nil, err
	}
	c := defaults()
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if c.Library == "" {
		c.Library = defaults().Library
	}
	if c.Format == "" {
		c.Format = defaults().Format
	}
	if c.Parallel < 1 {
		c.Parallel = defaults().Parallel
	}
	return &c, nil
}

func (c *Config) Save() error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// The file can hold a music-server password, so keep it owner-only.
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

// ArchivePath is yt-dlp's download archive: the record of what has already
// been fetched, which is what makes repeated syncs cheap.
func (c *Config) ArchivePath() string {
	return filepath.Join(c.Library, ".crate-archive")
}
