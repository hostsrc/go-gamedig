package binreader

import (
	"testing"
	"unicode/utf8"
)

// win1252Node is iconv-lite 0.7.2's decode of every byte 0x00-0xFF as
// 'win1252' (node-gamedig's 'latin1'), as UTF-16 code units.
var win1252Node = [256]rune{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
	32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63,
	64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95,
	96, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127,
	8364, 65533, 8218, 402, 8222, 8230, 8224, 8225, 710, 8240, 352, 8249, 338, 65533, 381, 65533,
	65533, 8216, 8217, 8220, 8221, 8226, 8211, 8212, 732, 8482, 353, 8250, 339, 65533, 382, 376,
	160, 161, 162, 163, 164, 165, 166, 167, 168, 169, 170, 171, 172, 173, 174, 175, 176, 177, 178, 179, 180, 181, 182, 183, 184, 185, 186, 187, 188, 189, 190, 191,
	192, 193, 194, 195, 196, 197, 198, 199, 200, 201, 202, 203, 204, 205, 206, 207, 208, 209, 210, 211, 212, 213, 214, 215, 216, 217, 218, 219, 220, 221, 222, 223,
	224, 225, 226, 227, 228, 229, 230, 231, 232, 233, 234, 235, 236, 237, 238, 239, 240, 241, 242, 243, 244, 245, 246, 247, 248, 249, 250, 251, 252, 253, 254, 255,
}

func TestDecodeWindows1252MatchesIconvLite(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	got := []rune(DecodeWindows1252(all))
	if len(got) != 256 {
		t.Fatalf("decoded %d runes, want 256", len(got))
	}
	for i, r := range got {
		if r != win1252Node[i] {
			t.Errorf("byte 0x%02x = U+%04X, iconv-lite gives U+%04X", i, r, win1252Node[i])
		}
	}
	if s := DecodeWindows1252([]byte("plain ascii")); s != "plain ascii" {
		t.Errorf("ascii = %q", s)
	}
}

func TestUntilAndBytes(t *testing.T) {
	r := New([]byte("ab\ncd"))
	if s := string(r.Bytes(1)); s != "a" {
		t.Fatalf("Bytes(1) = %q", s)
	}
	if s := string(r.Until('\n')); s != "b" {
		t.Fatalf("Until = %q", s)
	}
	if s := string(r.Until('\n')); s != "cd" || !r.Done() {
		t.Fatalf("unterminated Until = %q", s)
	}
	// Past the end, like node-gamedig's reader, everything is empty.
	if len(r.Until('\n')) != 0 || len(r.Bytes(4)) != 0 || len(r.Rest()) != 0 || !r.Done() {
		t.Fatal("reads past the end should be empty")
	}
	r = New([]byte("xyz"))
	if s := string(r.Bytes(10)); s != "xyz" || !r.Done() {
		t.Fatalf("short Bytes = %q", s)
	}
	if len(New([]byte("x")).Bytes(-1)) != 0 {
		t.Fatal("negative Bytes should be empty")
	}
}

func FuzzDecodeWindows1252(f *testing.F) {
	f.Add([]byte("\x80\x81\x8d\x8f\x90\x9d\x9f\xa0\xff plain"), byte('\n'), 3)
	f.Fuzz(func(t *testing.T, b []byte, delim byte, n int) {
		s := DecodeWindows1252(b)
		if !utf8.ValidString(s) || utf8.RuneCountInString(s) != len(b) {
			t.Fatalf("decode %x = %q", b, s)
		}
		r := New(b)
		total := 0
		for !r.Done() {
			total += len(r.Bytes(n % 8))
			if part := r.Until(delim); len(part) > len(b) {
				t.Fatal("Until overran")
			}
			if total > len(b) {
				t.Fatal("Bytes overran")
			}
		}
		_ = r.Until(delim)
		_ = r.Bytes(n)
	})
}
