package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A second catalogue, because the first one does not have much of this
// library. Tested against tracks the first one missed entirely, this found
// timed words for most of them.
//
// Its search is loose, though, and that matters more than the coverage. Asked
// for one song it will happily return a different one from the same film, or
// something that merely shares a word, and return real timed lyrics for it.
// Words shown in time with music look right even when they belong to another
// song, so every result is put through the same test as the first catalogue:
// the title has to match and the length has to agree.

type neteaseSong struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Duration int    `json:"duration"` // milliseconds
	Artists  []struct {
		Name string `json:"name"`
	} `json:"artists"`
}

// fetchNetease searches the second catalogue and returns anything credible.
func fetchNetease(ctx context.Context, names []string, title string, dur time.Duration) (*Lyrics, error) {
	q := title
	if len(names) > 0 {
		q += " " + names[0]
	}
	u := "https://music.163.com/api/search/get?type=1&limit=8&s=" + url.QueryEscape(q)

	var found struct {
		Result struct {
			Songs []neteaseSong `json:"songs"`
		} `json:"result"`
	}
	if err := neteaseGet(ctx, u, &found); err != nil {
		return nil, err
	}

	for _, s := range found.Result.Songs {
		// The title has to actually be the title. Without this the loose
		// search hands back a different song from the same film.
		if !titlesAgree(s.Name, title) {
			continue
		}
		if dur > 0 && s.Duration > 0 {
			off := float64(s.Duration)/1000 - dur.Seconds()
			if off < 0 {
				off = -off
			}
			if off > 5 {
				continue
			}
		} else if len(names) > 0 {
			// With no length to check against, the artist has to vouch for it.
			if !artistAgrees(s.Artists, names) {
				continue
			}
		} else {
			continue
		}

		var lyric struct {
			Lrc struct {
				Lyric string `json:"lyric"`
			} `json:"lrc"`
		}
		lu := fmt.Sprintf("https://music.163.com/api/song/lyric?id=%d&lv=1&kv=1&tv=-1", s.ID)
		if err := neteaseGet(ctx, lu, &lyric); err != nil {
			continue
		}
		lines := Parse(lyric.Lrc.Lyric)
		if len(lines) < 5 {
			continue
		}
		artist := ""
		if len(s.Artists) > 0 {
			artist = s.Artists[0].Name
		}
		return &Lyrics{Lines: lines, Synced: true, Title: s.Name, Artist: artist}, nil
	}
	return nil, fmt.Errorf("no match in the second catalogue for %q", title)
}

// titlesAgree reports whether two titles are the same song, allowing for one
// carrying a parenthetical the other does not.
func titlesAgree(a, b string) bool {
	fa, fb := fold(a), fold(b)
	if fa == "" || fb == "" {
		return false
	}
	if fa == fb {
		return true
	}
	// One being a prefix of the other covers "Song" against "Song (From ...)",
	// but only when the shared part is long enough to mean something.
	short, long := fa, fb
	if len(short) > len(long) {
		short, long = long, short
	}
	return len(short) >= 6 && strings.HasPrefix(long, short)
}

func artistAgrees(artists []struct {
	Name string `json:"name"`
}, names []string) bool {
	for _, a := range artists {
		fa := fold(a.Name)
		for _, n := range names {
			fn := fold(n)
			if fa != "" && fn != "" && (strings.Contains(fa, fn) || strings.Contains(fn, fa)) {
				return true
			}
		}
	}
	return false
}

func neteaseGet(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	// This endpoint answers to a browser and ignores anything else.
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://music.163.com/")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
