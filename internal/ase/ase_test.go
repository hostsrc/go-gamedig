package ase

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeConn replays queued datagrams and then times out.
type fakeConn struct {
	net.Conn
	replies [][]byte
	sent    [][]byte
}

func (c *fakeConn) Write(b []byte) (int, error) {
	c.sent = append(c.sent, bytes.Clone(b))
	return len(b), nil
}

func (c *fakeConn) Read(b []byte) (int, error) {
	if len(c.replies) == 0 {
		return 0, os.ErrDeadlineExceeded
	}
	n := copy(b, c.replies[0])
	c.replies = c.replies[1:]
	return n, nil
}

func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }

func str(s string) []byte { return append([]byte{byte(len(s) + 1)}, s...) }

func reply(fields ...string) []byte {
	b := []byte("EYE1")
	for _, f := range fields {
		b = append(b, str(f)...)
	}
	return b
}

func TestQuery(t *testing.T) {
	b := reply("mta", "22003", "name", "type", "map", "1.6", "1", "2", "32", "k", "v", "")
	b = append(append(b, 0x01|0x08), str("alice")...)
	b = append(b, str("10")...)
	conn := &fakeConn{replies: [][]byte{[]byte("junk"), b}}
	res, err := Query(context.Background(), conn, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conn.sent) != 1 || string(conn.sent[0]) != "s" {
		t.Errorf("sent %q", conn.sent)
	}
	if res.Name != "name" || res.Map != "map" || !res.Password || res.NumPlayers != 2 || res.MaxPlayers != 32 ||
		res.GamePort != 22003 || res.Raw["gamename"] != "mta" || res.Raw["k"] != "v" {
		t.Errorf("res = %+v", res)
	}
	if len(res.Players) != 1 || res.Players[0].Name != "alice" || res.Players[0].Score != 10 || res.Players[0].Flags != 9 {
		t.Errorf("players = %+v", res.Players)
	}
}

func TestTimeoutAndCancel(t *testing.T) {
	if _, err := Query(context.Background(), &fakeConn{replies: [][]byte{[]byte("EYE")}}, Options{}); !errors.Is(err, errSocketTimeout) {
		t.Errorf("err = %v, want socket timeout", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Query(ctx, &fakeConn{replies: [][]byte{reply()}}, Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestParseShort(t *testing.T) {
	res := Parse(nil)
	if !math.IsNaN(res.NumPlayers) || !math.IsNaN(res.GamePort) || res.Name != "" || len(res.Players) != 0 {
		t.Errorf("res = %+v", res)
	}
	// A length byte of 0 reads as an empty string without consuming more.
	res = Parse([]byte{0, 0, 3, 'a', 'b'})
	if res.Raw["gamename"] != "" || res.Name != "ab" {
		t.Errorf("res = %+v", res)
	}
}

// Expected values are JavaScript's String(parseInt(s)) in Node.js 22.
func TestParseIntMatchesJavaScript(t *testing.T) {
	for in, want := range map[string]string{
		"42": "42", " 7x": "7", "-3": "-3", "+5": "5", "1e3": "1", " 0x1F": "31", "-0x10": "-16",
		"99999999999999999999999": "1e+23", "-0": "-0", "0b1": "0", "\uFEFF12": "12", "\u200B12": "NaN",
		"\u008512": "NaN", "\u180E12": "NaN", "\u3000-12": "-12", "+-1": "NaN", "": "NaN", "0x": "NaN",
		"0x1fffffffffffff": "9007199254740991", "0x20000000000001": "9007199254740992",
		"0x20000000000003": "9007199254740996",
	} {
		got := ParseInt(in)
		var s string
		switch {
		case math.IsNaN(got):
			s = "NaN"
		case got == 0 && math.Signbit(got):
			s = "-0"
		case math.Abs(got) >= 1e21:
			s = strconv.FormatFloat(got, 'e', -1, 64)
		default:
			s = strconv.FormatFloat(got, 'f', -1, 64)
		}
		if s != want {
			t.Errorf("ParseInt(%q) = %s, want %s", in, s, want)
		}
	}
}

func checkResponse(t *testing.T, res *Response, n int) {
	for _, s := range []string{res.Name, res.Map, res.Version} {
		if !utf8.ValidString(s) {
			t.Fatalf("invalid UTF-8 %q", s)
		}
	}
	for k, v := range res.Raw {
		if !utf8.ValidString(k) || !utf8.ValidString(v) {
			t.Fatalf("invalid UTF-8 rule %q=%q", k, v)
		}
	}
	if len(res.Players) > n {
		t.Fatalf("%d players from %d bytes", len(res.Players), n)
	}
	for _, p := range res.Players {
		for _, s := range []string{p.Name, p.Team, p.Skin} {
			if !utf8.ValidString(s) {
				t.Fatalf("invalid UTF-8 player %q", s)
			}
		}
	}
}

// FuzzQuery feeds arbitrary datagrams to a full query. It must never panic
// or produce invalid UTF-8.
func FuzzQuery(f *testing.F) {
	f.Add([]byte("junk"), reply("mta", "1", "n", "t", "m", "v", "0", "1", "2", "", "")[:20])
	f.Add([]byte("EYE1\x00\x00\x00"), []byte{})
	f.Add([]byte{}, append(reply("a", "b", "c", "d", "e", "f", "g", "h", "i", "k", "v", ""), 0x3f, 2, 'x'))
	f.Fuzz(func(t *testing.T, first, second []byte) {
		res, err := Query(context.Background(), &fakeConn{replies: [][]byte{first, second}}, Options{})
		if err != nil {
			return
		}
		checkResponse(t, res, len(first)+len(second))
	})
}

func FuzzParse(f *testing.F) {
	f.Add(reply("mta", "22003", "n", "t", "m", "1", "0", "3", "8", "k", "v", "")[4:])
	f.Add([]byte{0x3f, 0, 0, 0, 0, 0, 0})
	f.Add([]byte("\x02a\x02b\xff"))
	f.Fuzz(func(t *testing.T, b []byte) {
		checkResponse(t, Parse(b), len(b))
		_ = ParseInt(string(b))
	})
}
