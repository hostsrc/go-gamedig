package binreader

import "unicode/utf16"

// PeekUint8 returns the next byte without consuming it. ok is false at the
// end of the buffer.
func (r *Reader) PeekUint8() (b byte, ok bool) {
	if r.Remaining() == 0 {
		return 0, false
	}
	return r.b[r.i], true
}

// DecodeUCS2 decodes b as little-endian UTF-16 like Node.js's
// Buffer.toString("ucs2"): a trailing odd byte is dropped. Node keeps
// unpaired surrogates in its string, which JSON.stringify writes as escapes
// such as "\ud800"; a Go string can't hold them, so they become U+FFFD, which
// is also what those escapes decode to in Go.
func DecodeUCS2(b []byte) string {
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}
