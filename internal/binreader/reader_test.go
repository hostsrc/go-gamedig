package binreader

import (
	"encoding/hex"
	"testing"
	"unicode/utf8"
)

// Expected values come from Node.js Buffer.toString("utf8"), which
// node-gamedig uses to decode strings.
var utf8Cases = []struct{ in, want string }{
	{"cff0e8e2e5f2", "������"},
	{"e282", "�"},
	{"e282ac", "€"},
	{"f09f8e", "�"},
	{"f09f8eae", "🎮"},
	{"eda080", "���"},
	{"c0af", "��"},
	{"e0809f", "���"},
	{"f4908080", "����"},
	{"ff", "�"},
	{"c3", "�"},
	{"61e28262", "a�b"},
	{"f880808080", "�����"},
	{"ed9fbf", "퟿"},
	{"efbfbd", "�"},
	{"e0a0", "�"},
	{"f0908080", "𐀀"},
	{"80bf", "��"},
	{"c2c3a9", "�é"},
}

func TestDecodeUTF8MatchesNode(t *testing.T) {
	for _, c := range utf8Cases {
		b, _ := hex.DecodeString(c.in)
		if got := DecodeUTF8(b); got != c.want {
			t.Errorf("DecodeUTF8(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReaderPastEnd(t *testing.T) {
	r := New([]byte{1, 2, 3})
	if r.Uint32() != 0 || !r.Done() {
		t.Fatal("short Uint32 should read zero and finish the buffer")
	}
	if r.Uint8() != 0 || r.String() != "" || len(r.Rest()) != 0 {
		t.Fatal("reads after the end should return zero values")
	}
	r = New([]byte("ab\x00cd"))
	if s := r.String(); s != "ab" {
		t.Fatalf("String = %q", s)
	}
	if s := r.String(); s != "cd" || !r.Done() {
		t.Fatalf("unterminated String = %q", s)
	}
}

func FuzzDecodeUTF8(f *testing.F) {
	for _, c := range utf8Cases {
		b, _ := hex.DecodeString(c.in)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		r := New(b)
		for !r.Done() {
			if s := r.String(); !utf8.ValidString(s) {
				t.Fatalf("invalid UTF-8 from %x", b)
			}
		}
	})
}
