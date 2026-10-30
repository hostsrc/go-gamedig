package minecraft

import (
	"bytes"
	"encoding/binary"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

// jsReader mirrors node-gamedig's lib/reader.js with big-endian numbers:
// the position may run past the end (or back with a negative skip), short
// reads return zero or empty values, and remaining() can go negative.
type jsReader struct {
	b []byte
	i int
}

func (r *jsReader) remaining() int { return len(r.b) - r.i }
func (r *jsReader) done() bool     { return r.i >= len(r.b) }
func (r *jsReader) skip(n int)     { r.i += n }

func (r *jsReader) inRange(n int) bool { return r.i >= 0 && r.remaining() >= n }

func (r *jsReader) uint8() int {
	v := 0
	if r.inRange(1) {
		v = int(r.b[r.i])
	}
	r.i++
	return v
}

func (r *jsReader) uint16() int {
	v := 0
	if r.inRange(2) {
		v = int(binary.BigEndian.Uint16(r.b[r.i:]))
	}
	r.i += 2
	return v
}

func (r *jsReader) uint32() uint32 {
	var v uint32
	if r.inRange(4) {
		v = binary.BigEndian.Uint32(r.b[r.i:])
	}
	r.i += 4
	return v
}

// part returns the next n bytes, or nothing if fewer are left.
func (r *jsReader) part(n int) []byte {
	var v []byte
	if r.inRange(n) {
		v = r.b[r.i : r.i+n]
	}
	r.i += n
	return v
}

// rest returns every unread byte without moving the position, like
// Buffer.slice(i).
func (r *jsReader) rest() []byte {
	if r.i >= len(r.b) {
		return nil
	}
	return r.b[max(r.i, 0):]
}

// string reads a NUL-terminated UTF-8 string; without a terminator it
// takes the rest of the buffer.
func (r *jsReader) string() string {
	start := max(r.i, 0)
	if start >= len(r.b) {
		r.i = len(r.b) + 1
		return ""
	}
	end := len(r.b)
	if j := bytes.IndexByte(r.b[start:], 0); j >= 0 {
		end = start + j
	}
	r.i = end + 1
	return binreader.DecodeUTF8(r.b[start:end])
}
