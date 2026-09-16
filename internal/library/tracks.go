package library

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// Song is one playable item. It is not called Track because Track is already
// a search result Kind in this package.
type Song struct {
	// Rel is the path relative to the music root, which is the same on the
	// remote, in the local staging directory and inside a playlist.
	Rel    string
	Artist string
	Album  string
	Title  string
}

// Tracks lists the library, newest layout first: artist, then album, then
// track. The remote is the source of truth, as it is for playlists.
func Tracks(ctx context.Context, c *config.Config) ([]Song, error) {
	idx, err := index(ctx, c)
	if err != nil {
		return nil, err
	}
	out := make([]Song, 0, len(idx.byKey))
	for _, rel := range idx.byKey {
		out = append(out, trackFromPath(rel))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Artist != out[j].Artist {
			return strings.ToLower(out[i].Artist) < strings.ToLower(out[j].Artist)
		}
		if out[i].Album != out[j].Album {
			return strings.ToLower(out[i].Album) < strings.ToLower(out[j].Album)
		}
		return out[i].Rel < out[j].Rel
	})
	return out, nil
}

// trackFromPath reads what it can from the layout the output template makes:
// Artist/Album/NN - Title.ext
func trackFromPath(rel string) Song {
	t := Song{Rel: rel}
	parts := strings.Split(rel, "/")
	switch len(parts) {
	case 0:
	case 1:
		t.Title = stem(parts[0])
	case 2:
		t.Artist, t.Title = parts[0], stem(parts[1])
	default:
		t.Artist, t.Album, t.Title = parts[0], parts[1], stem(parts[len(parts)-1])
	}
	t.Title = trackNumRe.ReplaceAllString(t.Title, "")
	return t
}

func stem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// Source returns what to hand the player for a track: the local file when one
// is kept, otherwise an authenticated URL on the streaming endpoint.
//
// Local is preferred when present because it costs no bandwidth and seeks
// instantly. With KeepLocal off there is nothing on disk, which is the whole
// point of that mode, so the remote is the only option.
func Source(c *config.Config, t Song) (string, error) {
	if local := filepath.Join(c.Library, t.Rel); fileExists(local) {
		return local, nil
	}
	if c.Stream.URL == "" {
		return "", errNoStream
	}
	base := strings.TrimSuffix(c.Stream.URL, "/")
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	// The path is assigned unescaped and left to URL.String() to encode.
	// Escaping the segments here as well produced %2520 for every space: one
	// escape from PathEscape and a second from String.
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + t.Rel
	if c.Stream.User != "" {
		u.User = url.UserPassword(c.Stream.User, c.Stream.Pass)
	}
	return u.String(), nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

var errNoStream = &streamErr{}

type streamErr struct{}

func (e *streamErr) Error() string {
	return "no local copy and no stream configured: set stream.url, stream.user and stream.pass in " + config.Path()
}
