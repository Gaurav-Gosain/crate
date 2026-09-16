package lyrics

import (
	"regexp"
	"strings"
)

// Titles on these files are whatever the uploader typed into YouTube, which
// is rarely just the name of the song. They carry the artist, the video
// format, every credited collaborator and the year, in no fixed order and
// separated by whatever punctuation came to hand. Searching a lyrics service
// with the whole string finds nothing, because nobody catalogues a song under
// "Song (FULL VIDEO) Artist ft. Another I Third I 2024".
//
// These rules were derived from the titles in this library that failed to
// match, not invented in the abstract.

// credits matches a trailing featured artist list.
var credits = regexp.MustCompile(`(?i)\s*\b(ft\.?|feat\.?|featuring|with)\b.*$`)

// separators are the characters uploaders use to chain credits onto a title.
// The fullwidth bar is what yt-dlp substitutes for a pipe when it sanitises a
// filename, so it is the most common of them here.
const separators = "｜|·•—–"

// bracketedNoise matches a bracketed aside that is production credits.
var bracketedNoise = regexp.MustCompile(`(?i)[\(\[][^\)\]]*\b(official|video|audio|visuali[sz]er|lyrical|lyrics|full|hd|4k|8k|teaser|trailer|out now|exclusive|music video)\b[^\)\]]*[\)\]]`)

// labelish matches the names record labels and channels go by. The artist
// field on these files is very often the uploading label rather than anyone
// who performed on the track, which makes it useless for finding the song and
// actively misleading if used to confirm a match.
var labelish = regexp.MustCompile(`(?i)\b(records?|music|entertainment|films?|company|vevo|productions?|studios?|media|t-series|saregama|tips|zee|sony|universal|speed|official)\b`)

// IsLabel reports whether an artist field names a label rather than a person.
func IsLabel(artist string) bool {
	return labelish.MatchString(artist)
}

// leadingNoise matches a production word used as a prefix, as in
// "LYRICAL: Song Name".
var leadingNoise = regexp.MustCompile(`(?i)^\s*(lyrical|lyric|official|full song|full video|video song|audio|new song|presenting)\s*[:：\-]\s*`)

// performers pulls candidate artist names out of a title.
//
// Where the artist field is a label, the people who actually made the track
// are usually listed in the title after the first separator, which is the only
// place left to look for them.
func performers(title string) []string {
	var out []string
	parts := strings.FieldsFunc(title, func(r rune) bool {
		return strings.ContainsRune(separators, r)
	})
	for i, p := range parts {
		if i == 0 {
			continue // the first part is the song, not a person
		}
		p = bracketedNoise.ReplaceAllString(p, " ")
		p = strings.Trim(p, " -:：.,_\"＂")
		p = strings.Join(strings.Fields(p), " ")
		// A plausible name: a couple of words, no production vocabulary, and
		// not a year or a stray number.
		if p == "" || len(p) < 4 || len(p) > 40 {
			continue
		}
		if noiseWord.MatchString(p) || labelish.MatchString(p) {
			continue
		}
		if n := len(strings.Fields(p)); n > 4 {
			continue
		}
		out = append(out, p)
		if len(out) == 3 {
			break
		}
	}
	return out
}

// noiseWord matches production vocabulary anywhere in a fragment.
var noiseWord = regexp.MustCompile(`(?i)\b(official|video|audio|visuali[sz]er|lyrical|lyrics|full|hd|4k|8k|teaser|trailer|latest|new|songs?|remix|mix|version|20\d\d)\b`)

// cleanTitle reduces an uploaded title to the name of the song.
func cleanTitle(title, artist string) string {
	s := leadingNoise.ReplaceAllString(title, "")
	// Uploaders wrap titles in quotes, including the fullwidth ones yt-dlp
	// substitutes when it sanitises a filename.
	s = strings.Trim(s, `"＂'`)

	// Everything after the first separator is credits, not the name.
	if i := strings.IndexAny(s, separators); i > 0 {
		s = s[:i]
	}

	// A run of " I " is the same idea typed by someone without a pipe key.
	if parts := strings.Split(s, " I "); len(parts) > 1 {
		s = parts[0]
	}

	// A production aside marks the end of the name: whatever follows
	// "(Official Audio)" is who was involved, not what the song is called.
	// The aside is cut at rather than merely removed, which used to leave the
	// trailing names behind.
	if loc := bracketedNoise.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}

	s = credits.ReplaceAllString(s, "")
	s = stripArtist(s, artist)

	s = strings.Trim(s, " -:：.,_\"＂")
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		// Cleaning must never leave nothing to search for: an empty query
		// matches everything, which is worse than searching the raw title.
		if t := strings.TrimSpace(title); t != "" {
			return t
		}
		return title
	}
	return s
}

// stripArtist removes the artist from either end of "Artist - Song" or
// "Song : Artist", which uploaders write both ways round.
//
// It splits on runes rather than bytes. Doing it by byte index cut the
// fullwidth colon these filenames are full of in half and left the tail of it
// glued to the front of the title.
func stripArtist(title, artist string) string {
	names := artistNames(artist)
	if len(names) == 0 {
		return title
	}
	runes := []rune(title)
	for i, r := range runes {
		if r != '-' && r != ':' && r != '：' {
			continue
		}
		head := fold(string(runes[:i]))
		tail := fold(string(runes[i+1:]))
		switch {
		case head != "" && matchesAny(head, names):
			// A leading artist may be written loosely, so a partial match is
			// enough: "DILJIT DOSANJH" against "Diljit Dosanjh, Someone".
			return string(runes[i+1:])
		case tail != "" && equalsAny(tail, names):
			// A trailing name has to match exactly. Accepting a partial one
			// threw away meaningful parts of the title: "Chharhiyaan - Remix
			// (Remix By Dj Sonu Dhillon)" lost the remix marker because the
			// artist's name appeared inside the rest of it, which would then
			// match the original recording's words against a remix.
			return string(runes[:i])
		}
	}
	return title
}

// artistNames splits the artist field, which carries everyone involved.
func artistNames(artist string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(artist, func(r rune) bool {
		return r == ',' || r == ';' || r == '/' || r == '&'
	}) {
		if f := fold(a); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func matchesAny(s string, names []string) bool {
	for _, n := range names {
		if s == n || strings.Contains(s, n) || strings.Contains(n, s) {
			return true
		}
	}
	return false
}

func equalsAny(s string, names []string) bool {
	for _, n := range names {
		if s == n {
			return true
		}
	}
	return false
}

// firstArtist returns the first credited name, which is the one a lyrics
// service is most likely to have catalogued the song under.
func firstArtist(artist string) string {
	for _, a := range strings.FieldsFunc(artist, func(r rune) bool {
		return r == ',' || r == ';' || r == '/' || r == '&'
	}) {
		if a = strings.TrimSpace(a); a != "" {
			return a
		}
	}
	return strings.TrimSpace(artist)
}
