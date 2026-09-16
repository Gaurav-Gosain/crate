package oggtag

// Ogg uses a CRC-32 that is not the common one: the same polynomial as
// Ethernet, but fed most significant bit first with no reflection of the input
// or output and no final inversion. Using the usual implementation produces a
// file that every player rejects, so the table is built here rather than taken
// from the standard library.
var crcTable = func() [256]uint32 {
	const poly = 0x04c11db7
	var t [256]uint32
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ poly
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

// crc computes the page checksum. The caller must have zeroed the checksum
// field first: it is part of the data being summed.
func crc(b []byte) uint32 {
	var r uint32
	for _, c := range b {
		r = r<<8 ^ crcTable[byte(r>>24)^c]
	}
	return r
}
