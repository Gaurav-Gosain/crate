package library

import (
	"context"
	"fmt"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// BackfillIDs works out which video each existing track came from.
//
// crate only started recording this when it downloaded a track, so everything
// fetched before then carries a title and nothing else. A title is not enough
// to find the captions for a particular upload, which is why the caption
// fallback found nothing at all on a library that predates the change.
//
// The mapping is recovered the same way playlists are built: ask each source
// what it contains, which gives an id beside every title, then match those
// titles against the files on disk. The matching is the same folded, prefix
// tolerant comparison, so it copes with the filename having been trimmed.
func BackfillIDs(ctx context.Context, c *config.Config, log func(string, ...any)) (int, error) {
	idx, err := index(ctx, c)
	if err != nil {
		return 0, err
	}

	found := map[string]string{}
	for _, s := range c.Sources {
		entries, err := resolve(ctx, s)
		if err != nil {
			if log != nil {
				log("   %s: %v", s.Name, err)
			}
			continue
		}
		matched := 0
		for _, e := range entries {
			if e.id == "" {
				continue
			}
			rel, ok := idx.lookup(e.title)
			if !ok {
				continue
			}
			// First source to claim a file wins. A track can appear in
			// several sources, and any of their ids will do: they are all the
			// same recording as far as the captions are concerned.
			if _, taken := found[rel]; taken {
				continue
			}
			found[rel] = e.id
			matched++
		}
		if log != nil {
			log("   %s: matched %d of %d", s.Name, matched, len(entries))
		}
	}

	if len(found) == 0 {
		return 0, fmt.Errorf("no tracks could be matched to a video")
	}
	return EmbedIDs(ctx, c, found, log)
}
