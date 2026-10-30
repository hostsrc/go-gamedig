package binreader

import (
	"strings"
	"unicode/utf8"
)

// windows1252High maps bytes 0x80-0x9F. Every other byte is the Unicode
// code point of the same value. Bytes Windows-1252 leaves undefined (0x81,
// 0x8D, 0x8F, 0x90, 0x9D) decode to U+FFFD, as in iconv-lite, which
// node-gamedig uses for its 'latin1' (win1252) strings.
var windows1252High = [32]rune{
	0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
	0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
}

// DecodeWindows1252 decodes b as Windows-1252, matching iconv-lite's
// "win1252" codec byte for byte.
func DecodeWindows1252(b []byte) string {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return string(b)
	}
	var s strings.Builder
	s.Grow(len(b) + len(b)/2)
	for _, c := range b {
		switch {
		case c < 0x80:
			s.WriteByte(c)
		case c < 0xA0:
			s.WriteRune(windows1252High[c-0x80])
		default:
			var buf [utf8.UTFMax]byte
			n := utf8.EncodeRune(buf[:], rune(c))
			s.Write(buf[:n])
		}
	}
	return s.String()
}

// Until returns the bytes before the next delim and moves past it. A
// missing delimiter returns the rest of the buffer, like node-gamedig's
// reader.string({delimiter}).
func (r *Reader) Until(delim byte) []byte {
	rest := r.Rest()
	n := len(rest)
	for j, c := range rest {
		if c == delim {
			n = j
			break
		}
	}
	r.i = len(r.b) - len(rest) + n + 1
	return rest[:n]
}

// Bytes returns the next n bytes, or fewer when the buffer ends first, like
// node-gamedig's reader.string(length).
func (r *Reader) Bytes(n int) []byte {
	n = min(n, r.Remaining())
	if n <= 0 {
		return []byte{}
	}
	v := r.b[r.i : r.i+n]
	r.i += n
	return v
}
