package unreal2

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeConn answers each request with the datagrams queued for its type,
// then times out.
type fakeConn struct {
	net.Conn
	replies map[byte][][]byte
	pending [][]byte
	sent    [][]byte
	readErr error
}

func (c *fakeConn) Write(b []byte) (int, error) {
	c.sent = append(c.sent, bytes.Clone(b))
	if len(b) == 5 {
		c.pending = c.replies[b[4]]
	}
	return len(b), nil
}

func (c *fakeConn) Read(b []byte) (int, error) {
	if len(c.pending) == 0 {
		if c.readErr != nil {
			return 0, c.readErr
		}
		return 0, os.ErrDeadlineExceeded
	}
	n := copy(b, c.pending[0])
	c.pending = c.pending[1:]
	return n, nil
}

func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }

func packet(typ byte, body ...[]byte) []byte {
	b := []byte{0x80, 0, 0, 0, typ}
	for _, p := range body {
		b = append(b, p...)
	}
	return b
}

func str(s string) []byte { return append(append([]byte{byte(len(s) + 1)}, s...), 0) }

func u32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func infoPacket() []byte {
	return packet(0, u32(1), str("1.2.3.4"), u32(7777), u32(7778), str("name"), str("map"), str("DM"), u32(2), u32(16), u32(5))
}

func TestQuery(t *testing.T) {
	conn := &fakeConn{replies: map[byte][][]byte{
		0: {infoPacket()},
		1: {packet(1, str("Mutator"), str("M1")), packet(2, str("ignored")), packet(1, str("A"), str("1"), str("A"), str("2"))},
		2: {packet(2, u32(4), str("p"), u32(30), u32(9), u32(0), u32(5), str("bot"), u32(0), u32(1), u32(0))},
	}}
	res, err := Query(context.Background(), conn, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{{0x79, 0, 0, 0, 0}, {0x79, 0, 0, 0, 1}, {0x79, 0, 0, 0, 2}}
	for i := range want {
		if i >= len(conn.sent) || !bytes.Equal(conn.sent[i], want[i]) {
			t.Fatalf("sent %x", conn.sent)
		}
	}
	if res.Name != "name" || res.GamePort != 7777 || res.NumPlayers != 2 || res.Raw["ping"] != 5 {
		t.Errorf("res = %+v", res)
	}
	if rules := res.Raw["rules"].(map[string]string); rules["A"] != "1,2" || len(rules) != 1 {
		t.Errorf("rules = %v", rules)
	}
	if len(res.Players) != 1 || res.Players[0].Name != "p" || len(res.Bots) != 1 || res.Bots[0].Name != "bot" {
		t.Errorf("players = %+v bots = %+v", res.Players, res.Bots)
	}
}

func TestRequiredAndOptional(t *testing.T) {
	_, err := Query(context.Background(), &fakeConn{replies: map[byte][][]byte{0: {infoPacket()}}}, Options{})
	if !errors.Is(err, errSocketTimeout) {
		t.Errorf("no rules reply: err = %v, want socket timeout", err)
	}
	// A network error (ICMP unreachable) ends collection like a timeout.
	conn := &fakeConn{replies: map[byte][][]byte{0: {infoPacket()}, 1: {packet(1)}}, readErr: syscall.ECONNREFUSED}
	res, err := Query(context.Background(), conn, Options{})
	if err != nil || len(res.Players) != 0 {
		t.Errorf("optional players: res = %+v, err = %v", res, err)
	}
	conn = &fakeConn{readErr: syscall.ECONNREFUSED}
	if _, err := Query(context.Background(), conn, Options{}); !errors.Is(err, errNetwork) {
		t.Errorf("err = %v, want network error", err)
	}
}

func TestReplyCap(t *testing.T) {
	big := packet(0, make([]byte, 60000))
	var flood [][]byte
	for range 20 {
		flood = append(flood, big)
	}
	_, err := Query(context.Background(), &fakeConn{replies: map[byte][][]byte{0: flood}}, Options{})
	if !errors.Is(err, errTooLarge) {
		t.Fatalf("err = %v, want reply too large", err)
	}
}

func TestContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Query(ctx, &fakeConn{replies: map[byte][][]byte{0: {infoPacket()}}}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// Expected values are node-gamedig's
// s.replace(/\x1b...|[\x00-\x1a]/gus, ”) in Node.js 22.
func TestStripColorCodesMatchesNode(t *testing.T) {
	for in, want := range map[string]string{
		"a\x1bxyzb":            "ab",
		"\x1b":                 "\x1b",
		"\x1bab":               "\x1bab",
		"\x1babc":              "",
		"x\x1b\x1b\x1b\x1by":   "xy",
		"\x01\x1a\x1b\x1c\x1d": "\x1b\x1c\x1d",
		"\x1b😀😀😀z":             "z",
		"tab\there\nnl":        "tabherenl",
		"\x1b\x00\x00\x00\x00": "",
		"plain":                "plain",
	} {
		if got := stripColorCodes(in); got != want {
			t.Errorf("stripColorCodes(%q) = %q, want %q", in, got, want)
		}
	}
}

func checkResponse(t *testing.T, res *Response) {
	strs := []string{res.Name, res.Map, res.Version}
	for _, v := range res.Raw {
		switch v := v.(type) {
		case string:
			strs = append(strs, v)
		case []string:
			strs = append(strs, v...)
		case map[string]string:
			for k, s := range v {
				strs = append(strs, k, s)
			}
		case map[string]any:
			for k := range v {
				strs = append(strs, k)
			}
		}
	}
	for _, p := range append(res.Players, res.Bots...) {
		strs = append(strs, p.Name)
	}
	for _, s := range strs {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) && res.Raw["ip"] != s {
			t.Fatalf("bad string %q", s)
		}
	}
}

// FuzzParse feeds arbitrary bodies to the parser. It must never panic or
// produce invalid UTF-8 or, after colour stripping, NUL characters.
func FuzzParse(f *testing.F) {
	f.Add(infoPacket()[5:], packet(1, str("Mutator"), str("KillingFloorMut"))[5:], []byte{})
	f.Add([]byte{0, 0, 0, 0, 0x85, 1, 'a', 0, 'b'}, []byte{0x81, 0x00, 0xd8, 3, 'a', 0}, []byte{1, 0, 0, 0, 2, 'x', 0, 9, 0, 0, 0})
	f.Fuzz(func(t *testing.T, info, rules, players []byte) {
		checkResponse(t, Parse(info, rules, players, true))
		_ = Parse(info, rules, players, false)
	})
}

// FuzzQuery feeds arbitrary datagrams to a full query.
func FuzzQuery(f *testing.F) {
	f.Add(infoPacket(), packet(1, str("k"), str("v")), packet(2, u32(1), str("p"), u32(1), u32(1), u32(1)))
	f.Add([]byte{1, 2}, []byte{0, 0, 0, 0, 1}, []byte{})
	f.Fuzz(func(t *testing.T, a, b, c []byte) {
		all := [][]byte{a, b, c}
		conn := &fakeConn{replies: map[byte][][]byte{0: all, 1: all, 2: all}}
		res, err := Query(context.Background(), conn, Options{})
		if err != nil {
			return
		}
		checkResponse(t, res)
	})
}
