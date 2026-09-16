package oggtag

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// The comment packet opens with this marker, then the vendor string and a list
// of "NAME=value" entries, every length a little endian 32 bit count.
const commentMagic = "OpusTags"

type comments struct {
	vendor string
	// entries are kept in order and with their original spelling, because the
	// cover art lives in one of them and reordering or renaming fields is a
	// good way to lose it.
	entries []string
}

func parseComments(b []byte) (*comments, error) {
	if len(b) < len(commentMagic)+4 || string(b[:len(commentMagic)]) != commentMagic {
		return nil, fmt.Errorf("not a comment packet")
	}
	off := len(commentMagic)
	readLen := func() (int, error) {
		if off+4 > len(b) {
			return 0, fmt.Errorf("comment packet ends mid length")
		}
		n := int(binary.LittleEndian.Uint32(b[off:]))
		off += 4
		if n < 0 || off+n > len(b) {
			return 0, fmt.Errorf("comment field runs past the end of the packet")
		}
		return n, nil
	}

	n, err := readLen()
	if err != nil {
		return nil, err
	}
	c := &comments{vendor: string(b[off : off+n])}
	off += n

	count, err := readLen()
	if err != nil {
		return nil, err
	}
	for range count {
		n, err := readLen()
		if err != nil {
			return nil, err
		}
		c.entries = append(c.entries, string(b[off:off+n]))
		off += n
	}
	return c, nil
}

func (c *comments) build() []byte {
	out := make([]byte, 0, 64)
	out = append(out, commentMagic...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(c.vendor)))
	out = append(out, c.vendor...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(c.entries)))
	for _, e := range c.entries {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(e)))
		out = append(out, e...)
	}
	return out
}

// set replaces every entry with this name, or appends one if there is none.
// Field names are matched without regard to case, which is what the format
// says, and the existing spelling is kept when replacing.
func (c *comments) set(name, value string) {
	want := strings.ToUpper(name)
	replaced := false
	var kept []string
	for _, e := range c.entries {
		k, _, ok := strings.Cut(e, "=")
		if ok && strings.ToUpper(k) == want {
			if replaced {
				continue // drop any duplicates of the same field
			}
			kept = append(kept, k+"="+value)
			replaced = true
			continue
		}
		kept = append(kept, e)
	}
	if !replaced {
		kept = append(kept, name+"="+value)
	}
	c.entries = kept
}

func (c *comments) get(name string) (string, bool) {
	want := strings.ToUpper(name)
	for _, e := range c.entries {
		k, v, ok := strings.Cut(e, "=")
		if ok && strings.ToUpper(k) == want {
			return v, true
		}
	}
	return "", false
}

// Get reads one tag from a file.
func Get(path, name string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	pages, err := parsePages(b)
	if err != nil {
		return "", err
	}
	packet, _, _, err := commentPacket(pages)
	if err != nil {
		return "", err
	}
	c, err := parseComments(packet)
	if err != nil {
		return "", err
	}
	v, _ := c.get(name)
	return v, nil
}

// Set writes one tag, leaving everything else in the file alone.
//
// The file is replaced only once the new one is complete, so an interrupted
// write cannot leave a half rewritten track in the library.
func Set(path, name, value string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := setInStream(b, name, value)
	if err != nil {
		return err
	}

	tmp := path + ".crate-tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// setInStream does the work on bytes, which is what the tests drive.
func setInStream(b []byte, name, value string) ([]byte, error) {
	pages, err := parsePages(b)
	if err != nil {
		return nil, err
	}
	packet, first, last, err := commentPacket(pages)
	if err != nil {
		return nil, err
	}
	c, err := parseComments(packet)
	if err != nil {
		return nil, err
	}
	c.set(name, value)
	rebuilt := c.build()

	// The comment packet gets its own pages, as it had before. Only its length
	// has changed, so the pages after it keep their contents and are simply
	// renumbered, which their checksums have to follow.
	var out []byte
	for i := range first {
		out = append(out, pages[i].build()...)
	}

	seq := pages[first].sequence
	proto := pages[first]
	for off := 0; ; {
		// A page carries at most 255 segments, so at most 255 times 255 bytes.
		const maxPayload = 255 * 255
		n := min(len(rebuilt)-off, maxPayload)
		chunk := rebuilt[off : off+n]
		p := page{
			headerType: 0,
			granule:    proto.granule,
			serial:     proto.serial,
			sequence:   seq,
			payload:    chunk,
		}
		if off > 0 {
			p.headerType = 1 // continued packet
		}
		if off+n == len(rebuilt) {
			p.segments = segmentsFor(n)
			out = append(out, p.build()...)
			seq++
			break
		}
		// A middle page has to end on a 255 so the packet reads as continuing.
		p.segments = make([]byte, 255)
		for i := range p.segments {
			p.segments[i] = 255
		}
		out = append(out, p.build()...)
		seq++
		off += n
	}

	for i := last + 1; i < len(pages); i++ {
		p := pages[i]
		p.sequence = seq
		seq++
		out = append(out, p.build()...)
	}
	return out, nil
}
