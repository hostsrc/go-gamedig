package gamespy

import "github.com/hostsrc/go-gamedig/internal/binreader"

// reader mirrors node-gamedig's lib/reader.js for the big-endian, latin1
// configuration the GameSpy protocols use. Its cursor may move past the end
// (and back, with a negative skip) exactly like node's, because the GameSpy
// parsers rely on that; every access is still bounds-checked, and reads
// past the end yield 0 or "" as in node.
type reader struct {
	b []byte
	i int
}

func newReader(b []byte) *reader { return &reader{b: b} }

func (r *reader) done() bool { return r.i >= len(r.b) }

func (r *reader) skip(n int) { r.i += n }

func (r *reader) remaining() int { return len(r.b) - r.i }

// uint reads an unsigned big-endian integer of n (1 or 4) bytes. Like node
// it returns 0 when fewer than n bytes remain but advances regardless.
func (r *reader) uint(n int) uint32 {
	var v uint32
	if r.i >= 0 && r.remaining() >= n {
		for _, c := range r.b[r.i : r.i+n] {
			v = v<<8 | uint32(c)
		}
	}
	r.i += n
	return v
}

// string reads a NUL-terminated Windows-1252 string. Without a terminator
// it takes the rest of the buffer.
func (r *reader) string() string {
	start := max(r.i, 0)
	if start > len(r.b) {
		start = len(r.b)
	}
	end := start
	for end < len(r.b) && r.b[end] != 0 {
		end++
	}
	r.i = end + 1
	return binreader.DecodeWindows1252(r.b[start:end])
}

// rest returns the unread bytes (none if the cursor is past the end).
func (r *reader) rest() []byte {
	if r.i < 0 {
		return r.b
	}
	if r.i >= len(r.b) {
		return nil
	}
	return r.b[r.i:]
}
