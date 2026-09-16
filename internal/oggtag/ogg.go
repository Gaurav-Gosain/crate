// Package oggtag reads and writes the comment tags inside an Ogg Opus file.
//
// This exists so crate does not need a Python tag editor on the music server.
// ffmpeg cannot do the job: rewriting an opus file to change a tag drops the
// cover art, because the picture is carried as a base64 comment and ffmpeg
// re-muxes rather than editing in place. Editing the comment packet directly
// keeps everything else in the file exactly as it was.
package oggtag

import (
	"encoding/binary"
	"fmt"
)

// An Ogg page begins with this magic, then a fixed 27 byte header followed by
// a table saying how the payload divides into segments.
const (
	magic      = "OggS"
	headerSize = 27
)

type page struct {
	headerType byte
	granule    uint64
	serial     uint32
	sequence   uint32
	segments   []byte
	payload    []byte
}

// parsePages splits a file into its pages.
func parsePages(b []byte) ([]page, error) {
	var pages []page
	for off := 0; off < len(b); {
		if off+headerSize > len(b) || string(b[off:off+4]) != magic {
			return nil, fmt.Errorf("not an ogg stream at offset %d", off)
		}
		nseg := int(b[off+26])
		if off+headerSize+nseg > len(b) {
			return nil, fmt.Errorf("truncated segment table at offset %d", off)
		}
		segs := b[off+headerSize : off+headerSize+nseg]
		size := 0
		for _, s := range segs {
			size += int(s)
		}
		start := off + headerSize + nseg
		if start+size > len(b) {
			return nil, fmt.Errorf("truncated page payload at offset %d", off)
		}
		pages = append(pages, page{
			headerType: b[off+5],
			granule:    binary.LittleEndian.Uint64(b[off+6:]),
			serial:     binary.LittleEndian.Uint32(b[off+14:]),
			sequence:   binary.LittleEndian.Uint32(b[off+18:]),
			segments:   append([]byte(nil), segs...),
			payload:    append([]byte(nil), b[start:start+size]...),
		})
		off = start + size
	}
	if len(pages) < 2 {
		return nil, fmt.Errorf("stream has %d pages, expected at least two", len(pages))
	}
	return pages, nil
}

// build writes a page back out, with its checksum computed over the whole page
// with the checksum field zeroed, as the format requires.
func (p page) build() []byte {
	out := make([]byte, 0, headerSize+len(p.segments)+len(p.payload))
	out = append(out, magic...)
	out = append(out, 0, p.headerType)
	out = binary.LittleEndian.AppendUint64(out, p.granule)
	out = binary.LittleEndian.AppendUint32(out, p.serial)
	out = binary.LittleEndian.AppendUint32(out, p.sequence)
	out = append(out, 0, 0, 0, 0) // checksum, filled in below
	out = append(out, byte(len(p.segments)))
	out = append(out, p.segments...)
	out = append(out, p.payload...)
	binary.LittleEndian.PutUint32(out[22:26], crc(out))
	return out
}

// segmentsFor divides a packet into the lacing values the format uses: runs of
// 255 followed by a final value below it. A packet whose length is a multiple
// of 255 needs a trailing zero, or the next packet is read as its
// continuation.
func segmentsFor(n int) []byte {
	var segs []byte
	for n >= 255 {
		segs = append(segs, 255)
		n -= 255
	}
	return append(segs, byte(n))
}

// commentPacket gathers the second packet, which holds the tags. It may run
// across more than one page when the cover art is large, which it usually is.
func commentPacket(pages []page) (data []byte, first, last int, err error) {
	first = 1
	for i := first; i < len(pages); i++ {
		data = append(data, pages[i].payload...)
		// A page whose final lacing value is below 255 ends the packet.
		segs := pages[i].segments
		if len(segs) > 0 && segs[len(segs)-1] < 255 {
			return data, first, i, nil
		}
	}
	return nil, 0, 0, fmt.Errorf("comment packet never ends")
}
