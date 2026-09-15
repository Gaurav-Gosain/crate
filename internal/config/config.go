// Package config holds crate's on-disk settings.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	// State is where the shared sources list and download archive live on
	// the remote. It sits beside the music rather than inside it, so the
	// music server does not try to index it. Empty means derive it.
	State string `json:"state,omitempty"`
}

// StatePath is where shared bookkeeping lives on the remote. It defaults to a
// hidden directory alongside the music root, which keeps it out of the music
// server's way while staying inside whatever backs that volume up.
func (r Remote) StatePath() string {
	if r.State != "" {
		return r.State
	}
	if r.Path == "" {
		return ""
	}
	return path.Join(path.Dir(strings.TrimSuffix(r.Path, "/")), ".crate-state")
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
	Parallel int `json:"parallel"`
	// KeepLocal decides whether downloaded files stay on this machine after
	// they have been mirrored. It is off by default, which makes the library
	// a staging area: the remote is the only copy, so there is one source of
	// truth and the two cannot drift apart. Turn it on to also keep an
	// offline copy, and accept that the two can then disagree.
	KeepLocal *bool    `json:"keep_local,omitempty"`
	Remote    Remote   `json:"remote"`
	Sources   []Source `json:"sources"`
	// Removed records sources that were deliberately deleted, keyed by
	// normalised URL. Without it a removal cannot survive a round trip
	// through the shared state: another device still listing the source
	// would simply put it back, and the deleted music would return with it.
	Removed map[string]string `json:"removed,omitempty"`
}

// MarkRemoved records a tombstone for a source.
func (c *Config) MarkRemoved(url string) {
	if c.Removed == nil {
		c.Removed = map[string]string{}
	}
	c.Removed[NormalizeURL(url)] = time.Now().UTC().Format(time.RFC3339)
}

// IsRemoved reports whether a source was deliberately deleted.
func (c *Config) IsRemoved(url string) bool {
	_, ok := c.Removed[NormalizeURL(url)]
	return ok
}

// ClearRemoved forgets a tombstone, so that adding a source back works.
func (c *Config) ClearRemoved(url string) {
	delete(c.Removed, NormalizeURL(url))
}

// artistHandle matches a youtube or youtube music channel handle url, with or
// without a trailing tab.
var artistHandle = regexp.MustCompile(`^https?://(?:www\.|music\.)?youtube\.com/(@[^/?#]+)(?:/([a-z]+))?`)

// NormalizeURL rewrites a url to the form that yields the best metadata.
//
// An artist handle on its own lands on the Videos tab, which is music videos:
// the audio has to be extracted from them and the titles are promotional
// rather than track names. The releases tab is the same artist's albums and
// singles, which carry real album and track tags.
func NormalizeURL(raw string) string {
	m := artistHandle.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return raw
	}
	// Only redirect a bare handle. An explicit tab is the caller's choice.
	if m[2] != "" {
		return raw
	}
	return "https://www.youtube.com/" + m[1] + "/releases"
}

// IsArtistChannel reports whether a url is a whole artist's catalogue, which
// is hundreds of tracks rather than an album's worth.
func IsArtistChannel(raw string) bool {
	return artistHandle.MatchString(strings.TrimSpace(raw)) ||
		strings.Contains(raw, "/channel/")
}

// IsEndlessMix reports whether a URL points at one of YouTube's generated
// radio playlists. Those have no end: they keep proposing tracks, so adding
// one as a source pulls in hundreds of files rather than an album's worth.
func IsEndlessMix(url string) bool {
	return strings.Contains(url, "list=RD")
}

func Path() string {
	return filepath.Join(xdg.ConfigHome, "crate", "crate.json")
}

func defaults() Config {
	return Config{
		Library:  filepath.Join(xdg.CacheHome, "crate", "staging"),
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
	c.migrateArchive()
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

// Keep reports whether to retain local files after mirroring. It defaults to
// true, because throwing away a user's only copy should be opt in.
func (c *Config) Keep() bool {
	return c.KeepLocal != nil && *c.KeepLocal
}

// ArchivePath is yt-dlp's download archive: the record of what has already
// been fetched, which is what makes repeated syncs cheap.
//
// It lives beside the config rather than inside the library, because the
// library can be ephemeral. Clearing downloaded files must not destroy the
// record of what was downloaded, or the next sync fetches everything again.
func (c *Config) ArchivePath() string {
	return filepath.Join(filepath.Dir(Path()), "archive")
}

// legacyArchivePath is where the archive used to live, inside the library.
func (c *Config) legacyArchivePath() string {
	return filepath.Join(c.Library, ".crate-archive")
}

// migrateArchive moves an archive left in the old location, so upgrading does
// not silently re-download a library.
func (c *Config) migrateArchive() {
	newPath := c.ArchivePath()
	if _, err := os.Stat(newPath); err == nil {
		return
	}
	old := c.legacyArchivePath()
	if _, err := os.Stat(old); err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err == nil {
		_ = os.Rename(old, newPath)
	}
}
