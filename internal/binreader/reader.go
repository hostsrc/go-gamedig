// Package binreader is a bounds-checked little-endian reader for untrusted
// datagrams. Reads past the end return zero values instead of failing, which
// matches node-gamedig's reader: a short reply yields empty fields, never a
// panic.
package binreader

import (
	"encoding/binary"
	"math"
	"strings"
	"unicode/utf8"
)

// Reader reads values from a byte slice. The zero value is an empty reader.
type Reader struct {
	b []byte
	i int
}

// New returns a reader positioned at the start of b.
func New(b []byte) *Reader { return &Reader{b: b} }

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int {
	if r.i >= len(r.b) {
		return 0
	}
	return len(r.b) - r.i
}

// Done reports whether every byte has been read.
func (r *Reader) Done() bool { return r.Remaining() == 0 }

// Skip advances past n bytes.
func (r *Reader) Skip(n int) {
	if n > 0 {
		r.i += min(n, r.Remaining())
	}
}

func (r *Reader) take(n int) []byte {
	if r.Remaining() < n {
		r.i = len(r.b)
		return nil
	}
	v := r.b[r.i : r.i+n]
	r.i += n
	return v
}

// Uint8 reads one byte.
func (r *Reader) Uint8() uint8 {
	if v := r.take(1); v != nil {
		return v[0]
	}
	return 0
}

// Uint16 reads a little-endian uint16.
func (r *Reader) Uint16() uint16 {
	if v := r.take(2); v != nil {
		return binary.LittleEndian.Uint16(v)
	}
	return 0
}

// Uint32 reads a little-endian uint32.
func (r *Reader) Uint32() uint32 {
	if v := r.take(4); v != nil {
		return binary.LittleEndian.Uint32(v)
	}
	return 0
}

// Int32 reads a little-endian int32.
func (r *Reader) Int32() int32 { return int32(r.Uint32()) }

// Uint64 reads a little-endian uint64.
func (r *Reader) Uint64() uint64 {
	if v := r.take(8); v != nil {
		return binary.LittleEndian.Uint64(v)
	}
	return 0
}

// Float32 reads a little-endian IEEE 754 single.
func (r *Reader) Float32() float32 { return math.Float32frombits(r.Uint32()) }

// String reads a NUL-terminated string and decodes it as UTF-8. A missing
// terminator consumes the rest of the buffer.
func (r *Reader) String() string {
	rest := r.Rest()
	n := len(rest)
	for j, c := range rest {
		if c == 0 {
			n = j
			break
		}
	}
	r.i = len(r.b) - len(rest) + n + 1
	return DecodeUTF8(rest[:n])
}

// Rest returns every unread byte and marks the reader done.
func (r *Reader) Rest() []byte {
	if r.i >= len(r.b) {
		r.i = len(r.b)
		return []byte{}
	}
	v := r.b[r.i:]
	r.i = len(r.b)
	return v
}

// DecodeUTF8 converts b to a string, replacing each maximal invalid subpart
// with U+FFFD as the WHATWG decoder does. Node uses that decoder, so names in
// legacy code pages (common on GoldSrc servers) come out the same as in
// node-gamedig.
func DecodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var s strings.Builder
	s.Grow(len(b) + 8)
	for len(b) > 0 {
		c, size := utf8.DecodeRune(b)
		if c != utf8.RuneError || size > 1 {
			s.WriteRune(c)
			b = b[size:]
			continue
		}
		s.WriteRune(utf8.RuneError)
		b = b[invalidPrefix(b):]
	}
	return s.String()
}

// invalidPrefix returns the length of the maximal subpart at the start of b,
// which is known not to begin a valid sequence.
func invalidPrefix(b []byte) int {
	lead := b[0]
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		need = 1
	case lead == 0xE0:
		need, lo = 2, 0xA0
	case lead == 0xED:
		need, hi = 2, 0x9F
	case lead >= 0xE1 && lead <= 0xEF:
		need = 2
	case lead == 0xF0:
		need, lo = 3, 0x90
	case lead == 0xF4:
		need, hi = 3, 0x8F
	case lead >= 0xF1 && lead <= 0xF3:
		need = 3
	default:
		return 1
	}
	n := 1
	for k := 0; k < need && n < len(b); k++ {
		c := b[n]
		if c < lo || c > hi {
			break
		}
		lo, hi = 0x80, 0xBF
		n++
	}
	return n
}
