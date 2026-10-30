package quake

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hostsrc/go-gamedig/internal/binreader"
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

// Expected values are String(parseInt(input)) from Node.js 22.
var parseIntCases = []struct{ in, want string }{
	{"", "NaN"}, {" ", "NaN"}, {"42", "42"}, {" 7x", "7"}, {"-3", "-3"}, {"+5", "5"},
	{"0012", "12"}, {"0x1f", "31"}, {"-0X10", "-16"}, {"12abc", "12"}, {"x1", "NaN"},
	{"-", "NaN"}, {"0x", "NaN"}, {"0xg", "NaN"}, {"-0", "-0"}, {"+0x0", "0"}, {"1e5", "1"},
	{"1.9", "1"}, {"\u00a05", "5"}, {"\ufeff9", "9"}, {"\u30008", "8"}, {"\u20288", "8"},
	{"\u00855", "NaN"}, {"99999999999999999999999", "1e+23"},
	{"9007199254740993", "9007199254740992"}, {"0x1fffffffffffff1", "144115188075855860"},
	{"0xFFFFFFFFFFFFFFFFF", "295147905179352830000"},
	{"-" + string(bytes.Repeat([]byte("9"), 320)), "-Infinity"},
	{"123456789012345678901234567890", "1.2345678901234568e+29"},
	{"\t\n\r\u000b\f 17", "17"}, {"0x 1", "NaN"}, {"- 1", "NaN"}, {"\u0663", "NaN"},
}

func TestParseIntMatchesJS(t *testing.T) {
	for _, c := range parseIntCases {
		got := parseInt(c.in)
		want, err := strconv.ParseFloat(c.want, 64)
		if err != nil && c.want != "-Infinity" {
			if c.want == "-0" {
				want = math.Copysign(0, -1)
			} else {
				want = math.NaN()
			}
		}
		if c.want == "-Infinity" {
			want = math.Inf(-1)
		}
		same := got == want && math.Signbit(got) == math.Signbit(want) || math.IsNaN(got) && math.IsNaN(want)
		if !same {
			t.Errorf("parseInt(%q) = %v, want %s", c.in, got, c.want)
		}
	}
}

// Expected values are !isNaN(input) from Node.js 22.
var numericCases = []struct {
	in   string
	want bool
}{
	{"", true}, {" ", true}, {"0", true}, {"-0", true}, {"+1", true}, {"1.", true}, {".5", true},
	{".", false}, {"1e5", true}, {"1E+5", true}, {"1e-5", true}, {"1e", false}, {"e1", false},
	{"0x10", true}, {"0X1f", true}, {"-0x10", false}, {"0x", false}, {"0b101", true}, {"0b2", false},
	{"0o17", true}, {"0o8", false}, {"Infinity", true}, {"-Infinity", true}, {"+Infinity", true},
	{"infinity", false}, {"NaN", false}, {"1_0", false}, {"12abc", false}, {" 12 ", true},
	{"\u00a03\u3000", true}, {"\u00853", false}, {"1.2.3", false}, {"+-1", false}, {"00", true},
	{"0.0e0", true}, {"\u0661", false}, {"\u202812\u2029", true}, {"+.5", true}, {"-.e1", false},
	{"5e+", false}, {"0x1g", false},
}

func TestIsNumericMatchesJS(t *testing.T) {
	for _, c := range numericCases {
		if got := isNumeric(c.in); got != c.want {
			t.Errorf("isNumeric(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Expected values are input.replace(/\^(X.{6}|.)/g, ”) from Node.js 22.
var stripCases = []struct{ in, want string }{
	{"", ""}, {"plain", "plain"}, {"^1a", "a"}, {"^Xabcdef1", "1"}, {"^Xab\rcdefg", "ab\rcdefg"},
	{"^\r", "^\r"}, {"^\n", "^\n"}, {"^^1x", "1x"}, {"a^", "a^"}, {"^X12345", "12345"}, {"^X", ""},
	{"^ x", "x"}, {"^\u2028x", "^\u2028x"}, {"^\u2029x", "^\u2029x"}, {"^\u0085x", "x"},
	{"^\u00a0x", "x"}, {"^Xa bcdefg", "fg"}, {"^^", ""}, {"^^^", "^"}, {"^X^1^2^3", ""},
	{"^XFF8800orange", "orange"}, {"\u00e9^\u00e9\u00e9", "\u00e9\u00e9"}, {"^7|^1|", "||"},
	{"^Xab\u2028cdefg", "ab\u2028cdefg"}, {"^Xabcdef", ""}, {"^Xabcde", "abcde"},
}

func TestStripColorsMatchesJS(t *testing.T) {
	for _, c := range stripCases {
		if got := StripColors(c.in); got != c.want {
			t.Errorf("StripColors(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQueryRequestsAndHeaders(t *testing.T) {
	tests := []struct {
		v     Variant
		req   string
		reply string
	}{
		{Quake1, "\xff\xff\xff\xffstatus\x00", "\xff\xff\xff\xffn\\hostname\\x"},
		{Hexen2, "\xff\xff\xff\xff\xffstatus\n\x00", "\xff\xff\xff\xff\xffn\\hostname\\x"},
		{Quake2, "\xff\xff\xff\xffstatus\x00", "\xff\xff\xff\xffprint\n\\hostname\\x"},
		{Quake3, "\xff\xff\xff\xffgetstatus\x00", "\xff\xff\xff\xffstatusResponse\n\\hostname\\x"},
		{Warsow, "\xff\xff\xff\xffgetstatus\x00", "\xff\xff\xff\xffstatusResponse\n\\hostname\\x"},
		{Xonotic, "\xff\xff\xff\xffgetstatus\x00", "\xff\xff\xff\xffstatusResponse\n\\hostname\\x"},
	}
	for _, tt := range tests {
		conn := &fakeConn{replies: [][]byte{[]byte("junk"), []byte("\xff\xff\xff\xffother\n"), []byte(tt.reply)}}
		res, err := Query(context.Background(), conn, Options{Variant: tt.v})
		if err != nil {
			t.Fatalf("variant %d: %v", tt.v, err)
		}
		if len(conn.sent) != 1 || string(conn.sent[0]) != tt.req {
			t.Errorf("variant %d sent %q, want %q", tt.v, conn.sent, tt.req)
		}
		if res.Name != "x" {
			t.Errorf("variant %d name = %q", tt.v, res.Name)
		}
	}
}

func TestQueryTimeoutAndCancel(t *testing.T) {
	_, err := Query(context.Background(), &fakeConn{replies: [][]byte{[]byte("\xff\xff\xff\xffprint\n")}}, Options{Variant: Quake3})
	if !errors.Is(err, errSocketTimeout) {
		t.Fatalf("err = %v, want socket timeout", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Query(ctx, &fakeConn{replies: [][]byte{[]byte("\xff\xff\xff\xffstatusResponse\n")}}, Options{Variant: Quake3})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestQueryOverRealSocket(t *testing.T) {
	srv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn, err := net.DialUDP("udp", nil, srv.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Silent server: the socket timeout ends the query.
	start := time.Now()
	_, err = Query(context.Background(), conn, Options{Variant: Quake3, SocketTimeout: 50 * time.Millisecond})
	if !errors.Is(err, errSocketTimeout) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
	// Cancelling the context interrupts a blocked read.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = Query(ctx, conn, Options{Variant: Quake3, SocketTimeout: 5 * time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestParseQuirks(t *testing.T) {
	body := []byte("\\sv_hostname\\^1A\\iv\\9\\clients\\3\\version\\^2v\\__proto__\\x\n" +
		"1 0 \"^3bot\"\n2 -1 \"neg\" \"addr\"\n\n3 3 \"after blank\"\n")
	res := Parse(body, Quake3, true)
	if res.Name != "A" || res.Version != "v" || !res.HasNumPlayersText || res.NumPlayersText != "3" {
		t.Errorf("res = %+v", res)
	}
	if _, ok := res.Raw["__proto__"]; ok {
		t.Error("__proto__ should be dropped like a JavaScript assignment")
	}
	if len(res.Bots) != 1 || res.Bots[0].Name != "bot" || len(res.Players) != 1 || res.Players[0].Raw["address"] != "addr" {
		t.Errorf("players = %+v, bots = %+v", res.Players, res.Bots)
	}

	kept := Parse(body, Quake3, false)
	if kept.Name != "^1A" || kept.Version != "^2v" {
		t.Errorf("kept colors: %+v", kept)
	}
	q2 := Parse(body, Quake2, true)
	if q2.Name != "^1A" || q2.Version != "9" {
		t.Errorf("quake2 must not strip and prefers iv: %+v", q2)
	}
}

func checkResponse(t *testing.T, res *Response) {
	t.Helper()
	strs := []string{res.Name, res.Map, res.Version, res.Password, res.MaxPlayers, res.NumPlayersText}
	for k, v := range res.Raw {
		strs = append(strs, k, v)
	}
	for _, p := range append(res.Players, res.Bots...) {
		strs = append(strs, p.Name)
		for k, v := range p.Raw {
			strs = append(strs, k)
			switch v := v.(type) {
			case string:
				strs = append(strs, v)
			case float64:
			default:
				t.Fatalf("player raw %q has type %T", k, v)
			}
		}
	}
	for _, s := range strs {
		if !utf8.ValidString(s) {
			t.Fatalf("invalid UTF-8 %q", s)
		}
	}
	if _, iv := res.Raw["iv"]; !iv && res.NumPlayers != len(res.Players)+len(res.Bots) {
		t.Fatalf("numplayers %d != %d players", res.NumPlayers, len(res.Players)+len(res.Bots))
	}
}

// FuzzQuery feeds arbitrary datagrams to a full query of every variant.
// It must never panic and must return valid UTF-8 and JSON-able values.
func FuzzQuery(f *testing.F) {
	f.Add([]byte("\xff\xff\xff\xffstatusResponse\n\\sv_hostname\\^1x\n1 2 \"p\"\n"), []byte{}, uint8(Quake3), true)
	f.Add([]byte("\xff\xff\xff\xffn\\hostname\\x\n1 2 3 4 \"n\" \"s\" 1 2\n"), []byte("junk"), uint8(Quake1), false)
	f.Add([]byte("\xff\xff\xff\xff\xffn"), []byte("\xff\xff\xff\xffprint"), uint8(Hexen2), false)
	f.Add([]byte("\xff\xff\xff\xffprint\n\\iv\\1\\clients\\2\n"), []byte{}, uint8(Quake2), true)
	f.Add([]byte("\xff\xff\xff\xffstatusResponse\n\n5 5 \"1\" \"x\"\n"), []byte{}, uint8(Xonotic), true)
	f.Fuzz(func(t *testing.T, first, second []byte, variant uint8, strip bool) {
		v := Variant(int(variant) % len(specs))
		conn := &fakeConn{replies: [][]byte{first, second}}
		res, err := Query(context.Background(), conn, Options{Variant: v, StripColors: strip})
		if err != nil {
			return
		}
		checkResponse(t, res)
	})
}

// FuzzParse feeds arbitrary reply bodies to the parser.
func FuzzParse(f *testing.F) {
	f.Add([]byte("\\sv_hostname\\^1x\\g_needpass\\0\n1 2 \"p\" \"a\"\n0 0 \"b\"\n"), uint8(Quake3), true)
	f.Add([]byte("\\hostname\\x\\*version\\2\n1 2 3 4 \"n\" \"s\" 1 2\n14 3\n"), uint8(Quake1), false)
	f.Add([]byte("x\\y\\z\n\x00\n"), uint8(Warsow), true)
	f.Add([]byte("\n5 5 \"0x1\" \"x\"\n5 5 \"\xa0\" \"y\"\n"), uint8(Xonotic), true)
	f.Fuzz(func(t *testing.T, body []byte, variant uint8, strip bool) {
		checkResponse(t, Parse(body, Variant(int(variant)%len(specs)), strip))
	})
}

var jsColorCode = regexp.MustCompile(`\^(X[^\n\r\x{2028}\x{2029}]{6}|[^\n\r\x{2028}\x{2029}])`)

// FuzzStripColors compares StripColors with a regular expression that has
// the same leftmost-first semantics as JavaScript's, on strings decoded
// from Windows-1252 as Quake replies are.
func FuzzStripColors(f *testing.F) {
	for _, c := range stripCases {
		f.Add([]byte(c.in))
	}
	f.Add([]byte("^X\r^^X123456^"))
	f.Fuzz(func(t *testing.T, b []byte) {
		s := binreader.DecodeWindows1252(b)
		if got, want := StripColors(s), jsColorCode.ReplaceAllString(s, ""); got != want {
			t.Fatalf("StripColors(%q) = %q, regexp gives %q", s, got, want)
		}
	})
}

// FuzzJSNumbers checks parseInt and isNumeric never panic and stay
// consistent with each other.
func FuzzJSNumbers(f *testing.F) {
	for _, c := range parseIntCases {
		f.Add(c.in)
	}
	for _, c := range numericCases {
		f.Add(c.in)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := parseInt(s)
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n != math.Trunc(n) {
			t.Fatalf("parseInt(%q) = %v is not an integer", s, n)
		}
		// A string of plain decimal digits is numeric and parses to itself.
		if isPlainDigits(s) {
			want, _ := strconv.ParseFloat(s, 64)
			if !isNumeric(s) || n != want {
				t.Fatalf("%q: isNumeric %v, parseInt %v", s, isNumeric(s), n)
			}
		}
	})
}

func isPlainDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
