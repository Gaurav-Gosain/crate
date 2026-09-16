package oggtag

import (
	"encoding/binary"
	"strings"
	"testing"
)

// buildStream makes a small but valid Ogg Opus stream to work on, so the tests
// do not need a real music file to hand.
func buildStream(t *testing.T, entries []string, audioPages int) []byte {
	t.Helper()

	head := append([]byte("OpusHead"), 1, 2)
	head = append(head, make([]byte, 9)...)

	c := &comments{vendor: "crate test"}
	c.entries = entries
	tags := c.build()

	var out []byte
	out = append(out, page{
		headerType: 2, // beginning of stream
		serial:     42,
		sequence:   0,
		segments:   segmentsFor(len(head)),
		payload:    head,
	}.build()...)

	seq := uint32(1)
	for off := 0; ; {
		const maxPayload = 255 * 255
		n := min(len(tags)-off, maxPayload)
		p := page{serial: 42, sequence: seq, payload: tags[off : off+n]}
		if off > 0 {
			p.headerType = 1
		}
		if off+n == len(tags) {
			p.segments = segmentsFor(n)
			out = append(out, p.build()...)
			seq++
			break
		}
		p.segments = make([]byte, 255)
		for i := range p.segments {
			p.segments[i] = 255
		}
		out = append(out, p.build()...)
		seq++
		off += n
	}

	for i := range audioPages {
		body := []byte(strings.Repeat("a", 40+i))
		out = append(out, page{
			serial:   42,
			sequence: seq,
			granule:  uint64(960 * (i + 1)),
			segments: segmentsFor(len(body)),
			payload:  body,
		}.build()...)
		seq++
	}
	return out
}

// checkStream re-parses a stream and insists every page is intact: the
// checksums right and the sequence numbers unbroken. A page that fails either
// is one a player will refuse.
func checkStream(t *testing.T, b []byte) []page {
	t.Helper()
	pages, err := parsePages(b)
	if err != nil {
		t.Fatalf("stream will not parse: %v", err)
	}
	for i, p := range pages {
		raw := p.build()
		want := binary.LittleEndian.Uint32(raw[22:26])
		got := binary.LittleEndian.Uint32(b[indexOfPage(t, b, i)+22:])
		if want != got {
			t.Fatalf("page %d has checksum %08x, recomputes to %08x", i, got, want)
		}
		if p.sequence != uint32(i) {
			t.Fatalf("page %d carries sequence %d", i, p.sequence)
		}
	}
	return pages
}

// indexOfPage finds where the nth page starts.
func indexOfPage(t *testing.T, b []byte, n int) int {
	t.Helper()
	off, count := 0, 0
	for off < len(b) {
		if count == n {
			return off
		}
		nseg := int(b[off+26])
		size := 0
		for _, s := range b[off+27 : off+27+nseg] {
			size += int(s)
		}
		off += 27 + nseg + size
		count++
	}
	t.Fatalf("no page %d", n)
	return 0
}

func TestSetAndGetRoundTrip(t *testing.T) {
	in := buildStream(t, []string{"TITLE=placeholder", "ARTIST=someone"}, 2)
	out, err := setInStream(in, "youtube_id", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	checkStream(t, out)

	pages, _ := parsePages(out)
	packet, _, _, _ := commentPacket(pages)
	c, err := parseComments(packet)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := c.get("youtube_id"); !ok || v != "abc123" {
		t.Fatalf("read back %q, %v", v, ok)
	}
}

// The cover art is one of these entries. Losing it is the exact failure that
// made re-encoding unusable, so it has to survive a tag write untouched.
func TestExistingEntriesSurvive(t *testing.T) {
	art := "METADATA_BLOCK_PICTURE=" + strings.Repeat("Zm9vYmFy", 9000) // ~72KB
	in := buildStream(t, []string{"TITLE=placeholder", art, "ARTIST=someone"}, 1)
	out, err := setInStream(in, "LYRICS", "[00:00.00]placeholder line")
	if err != nil {
		t.Fatal(err)
	}
	checkStream(t, out)

	pages, _ := parsePages(out)
	packet, _, _, _ := commentPacket(pages)
	c, _ := parseComments(packet)
	got, ok := c.get("METADATA_BLOCK_PICTURE")
	if !ok {
		t.Fatal("the cover art did not survive the write")
	}
	if len(got) != len(art)-len("METADATA_BLOCK_PICTURE=") {
		t.Fatalf("the cover art changed length: %d", len(got))
	}
	if v, _ := c.get("TITLE"); v != "placeholder" {
		t.Fatalf("title became %q", v)
	}
}

// A packet larger than one page has to be split correctly, and the pages after
// it renumbered, or the file ends mid-stream.
func TestLargeCommentSpansPages(t *testing.T) {
	big := "METADATA_BLOCK_PICTURE=" + strings.Repeat("A", 200_000)
	in := buildStream(t, []string{big}, 3)
	out, err := setInStream(in, "youtube_id", "xyz")
	if err != nil {
		t.Fatal(err)
	}
	pages := checkStream(t, out)
	if len(pages) < 5 {
		t.Fatalf("expected the comment to span several pages, got %d in total", len(pages))
	}
	packet, _, _, _ := commentPacket(pages)
	c, _ := parseComments(packet)
	if v, ok := c.get("youtube_id"); !ok || v != "xyz" {
		t.Fatalf("tag lost across pages: %q %v", v, ok)
	}
}

// Writing the same tag twice must replace it, not accumulate copies.
func TestSetReplacesRatherThanAppends(t *testing.T) {
	in := buildStream(t, []string{"YOUTUBE_ID=old"}, 1)
	out, _ := setInStream(in, "youtube_id", "new")
	out, _ = setInStream(out, "youtube_id", "newer")
	checkStream(t, out)

	pages, _ := parsePages(out)
	packet, _, _, _ := commentPacket(pages)
	c, _ := parseComments(packet)
	n := 0
	for _, e := range c.entries {
		if strings.HasPrefix(strings.ToUpper(e), "YOUTUBE_ID=") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d copies of the field", n)
	}
	if v, _ := c.get("youtube_id"); v != "newer" {
		t.Fatalf("value is %q", v)
	}
}

// Field names are case insensitive in this format, so a tag written by another
// tool under a different spelling has to be found and replaced, not doubled.
func TestFieldNamesAreCaseInsensitive(t *testing.T) {
	in := buildStream(t, []string{"Lyrics=old"}, 1)
	out, _ := setInStream(in, "LYRICS", "new")
	pages, _ := parsePages(out)
	packet, _, _, _ := commentPacket(pages)
	c, _ := parseComments(packet)
	if len(c.entries) != 1 {
		t.Fatalf("got %d entries, want the one replaced: %v", len(c.entries), c.entries)
	}
	if v, _ := c.get("lyrics"); v != "new" {
		t.Fatalf("value is %q", v)
	}
}

func TestRejectsSomethingThatIsNotOgg(t *testing.T) {
	if _, err := setInStream([]byte("this is not an ogg file"), "a", "b"); err == nil {
		t.Fatal("rubbish was accepted as a stream")
	}
}

// A packet whose length is an exact multiple of 255 needs a trailing zero
// lacing value, or the next packet is read as its continuation.
func TestSegmentsForExactMultiple(t *testing.T) {
	segs := segmentsFor(510)
	if len(segs) != 3 || segs[0] != 255 || segs[1] != 255 || segs[2] != 0 {
		t.Fatalf("got %v", segs)
	}
}
