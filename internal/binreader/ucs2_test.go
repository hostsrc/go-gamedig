package binreader

import (
	"encoding/hex"
	"testing"
	"unicode/utf8"
)

// Expected values come from Node.js 22's Buffer.toString("ucs2"). Lone
// surrogates, which Node keeps and Go can't, are written as U+FFFD.
func TestDecodeUCS2MatchesNode(t *testing.T) {
	for in, want := range map[string]string{
		"41004200":   "AB",
		"410042":     "A",
		"00d84100":   "�A",
		"3dd800de":   "😀",
		"00dc":       "�",
		"":           "",
		"ff":         "",
		"e9006d0000": "ém",
	} {
		b, _ := hex.DecodeString(in)
		if got := DecodeUCS2(b); got != want {
			t.Errorf("DecodeUCS2(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestPeekUint8(t *testing.T) {
	r := New([]byte("a"))
	if b, ok := r.PeekUint8(); !ok || b != 'a' || r.Remaining() != 1 {
		t.Fatalf("PeekUint8 = %c, %v", b, ok)
	}
	r.Skip(1)
	if _, ok := r.PeekUint8(); ok {
		t.Fatal("PeekUint8 at end should fail")
	}
}

func FuzzDecodeUCS2(f *testing.F) {
	f.Add([]byte("\x00\xd8\x41\x00"))
	f.Add([]byte{0x3d, 0xd8, 0x00, 0xde, 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		if u := DecodeUCS2(b); !utf8.ValidString(u) || utf8.RuneCountInString(u) > len(b)/2 {
			t.Fatalf("DecodeUCS2(%x) = %q", b, u)
		}
	})
}
