package gamespy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"os"
	"strings"
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

// jsResults are node 22's Number(s) and parseInt(s) for each input, as
// printed by String() ("-0" marks negative zero).
var jsResults = map[string][2]string{
	"": {"0", "NaN"}, " ": {"0", "NaN"}, "12": {"12", "12"}, " 12 ": {"12", "12"},
	"\u00a012\u3000": {"12", "12"}, "\uFEFF7": {"7", "7"}, "1e3": {"1000", "1"}, "1E+3": {"1000", "1"},
	".5": {"0.5", "NaN"}, "5.": {"5", "5"}, "-.5e-1": {"-0.05", "NaN"}, "+5": {"5", "5"},
	"-0": {"-0", "-0"}, "0x1F": {"31", "31"}, "0X1f": {"31", "31"}, "-0x10": {"NaN", "-16"},
	"0o17": {"15", "0"}, "0b101": {"5", "0"}, "0x": {"NaN", "NaN"}, "Infinity": {"Infinity", "NaN"},
	"-Infinity": {"-Infinity", "NaN"}, "+Infinity": {"Infinity", "NaN"}, "infinity": {"NaN", "NaN"},
	"1e400": {"Infinity", "1"}, "1e-400": {"0", "1"}, "12abc": {"NaN", "12"}, "1_000": {"NaN", "1"},
	"0.1.2": {"NaN", "0"}, "abc": {"NaN", "NaN"}, "\u0085 5": {"NaN", "NaN"}, "  9": {"9", "9"},
	"00012": {"12", "12"}, "9007199254740993": {"9007199254740992", "9007199254740992"},
	"0x20000000000001":               {"9007199254740992", "9007199254740992"},
	"123456789012345678901234567890": {"1.2345678901234568e+29", "1.2345678901234568e+29"},
	"1.7976931348623159e308":         {"Infinity", "1"},
}

func jsString(f float64) string {
	if f == 0 && math.Signbit(f) {
		return "-0"
	}
	return numberString(f)
}

func TestJSNumbers(t *testing.T) {
	for in, want := range jsResults {
		if got := jsString(toNumber(in)); got != want[0] {
			t.Errorf("Number(%q) = %s, want %s", in, got, want[0])
		}
		if got := jsString(parseInt(in)); got != want[1] {
			t.Errorf("parseInt(%q) = %s, want %s", in, got, want[1])
		}
	}
	// String(n) in node 22.
	for f, want := range map[float64]string{
		0.30000000000000004: "0.30000000000000004", 1e21: "1e+21", 1e-7: "1e-7", 123e-20: "1.23e-18",
		-1.5: "-1.5", 1<<53 + 2: "9007199254740994", 5e-324: "5e-324",
		math.MaxFloat64: "1.7976931348623157e+308", 100: "100", 0.000001: "0.000001",
	} {
		if got := numberString(f); got != want {
			t.Errorf("String(%v) = %s, want %s", f, got, want)
		}
	}
}

func TestObjectKeyOrder(t *testing.T) {
	o := newObject()
	for _, k := range []string{"b", "10", "a", "2", "01", "4294967295", "4294967294", "__proto__"} {
		o.set(k, k)
	}
	o.del("a")
	o.set("a", "again")
	got := strings.Join(o.keys(), ",")
	if want := "2,10,4294967294,b,01,4294967295,a"; got != want {
		t.Errorf("keys = %s, want %s", got, want)
	}
	o.define("__proto__", "own")
	if !o.has("__proto__") {
		t.Error("define should create __proto__")
	}
}

func TestStripColors(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b\xff@@Red":   "Red",
		"\x1b\n12":        "\x1b12",
		"a\x1b\x01\x02":   "a\x1b",
		"\x1a\x1b\x1cKey": "y",
		"plain":           "plain",
	} {
		if got := stripUnreal2Colors(in); got != want {
			t.Errorf("stripUnreal2Colors(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGamespy1Limits(t *testing.T) {
	var replies [][]byte
	for range maxParts + 1 {
		replies = append(replies, nil)
	}
	_, err := Query1(context.Background(), &fakeConn{replies: replies}, Options{})
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("err = %v, want too many parts", err)
	}

	big := bytes.Repeat([]byte("\\k\\v"), 16000)
	replies = nil
	for range 20 {
		replies = append(replies, big)
	}
	_, err = Query1(context.Background(), &fakeConn{replies: replies}, Options{})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want reply too large", err)
	}
}

func gs3Packet(id byte, body string) []byte {
	return append(append([]byte{0, 0, 0, 0, 1}, "splitnum\x00"...), append([]byte{id, 1}, body...)...)
}

func TestGamespy3Limits(t *testing.T) {
	challenge := []byte("\x09\x00\x00\x00\x01-1\x00")

	// Each field starts at offset 255, so a few hundred fields exceed the
	// slot budget long before the reply gets large.
	var body strings.Builder
	body.WriteString("\x00")
	for i := range 300 {
		body.WriteString("f_" + strings.Repeat("x", i%7+1) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "\x00\xffv\x00\x00")
	}
	conn := &fakeConn{replies: [][]byte{challenge, gs3Packet(0x80, body.String())}}
	_, err := Query3(context.Background(), conn, Options{})
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("err = %v, want too many items", err)
	}
	if want := []byte{0xfe, 0xfd, 0, 0, 0, 0, 1, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1}; !bytes.Equal(conn.sent[1], want) {
		t.Errorf("status request %x, want %x", conn.sent[1], want)
	}

	big := strings.Repeat("y", 60000)
	replies := [][]byte{challenge}
	for id := range byte(20) {
		replies = append(replies, gs3Packet(id, big))
	}
	_, err = Query3(context.Background(), &fakeConn{replies: replies}, Options{})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want reply too large", err)
	}
}

func TestTimeoutAndCancel(t *testing.T) {
	for name, q := range map[string]func(context.Context, net.Conn, Options) (*Response, error){
		"gamespy1": Query1, "gamespy2": Query2, "gamespy3": Query3,
	} {
		if _, err := q(context.Background(), &fakeConn{}, Options{}); !errors.Is(err, errSocketTimeout) {
			t.Errorf("%s: err = %v, want socket timeout", name, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := q(ctx, &fakeConn{}, Options{}); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
	}
}

// checkResponse fails if res holds invalid UTF-8 or values that are not
// JSON-encodable.
func checkResponse(t *testing.T, res *Response) {
	t.Helper()
	for _, s := range []string{res.Name, res.Map, res.Version, res.GamePort} {
		if !utf8.ValidString(s) {
			t.Fatalf("invalid UTF-8 %q", s)
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			if !utf8.ValidString(v) {
				t.Fatalf("invalid UTF-8 %q", v)
			}
		case bool, nil:
		case float64:
		case map[string]any:
			for k, e := range v {
				walk(k)
				walk(e)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		default:
			t.Fatalf("unexpected value type %T", v)
		}
	}
	walk(res.Raw)
	for _, p := range res.Players {
		walk(p)
	}
	// NaN and Inf are replaced by the root package; everything else must
	// encode.
	if _, err := json.Marshal(res.Players); err != nil && !strings.Contains(err.Error(), "unsupported value") {
		t.Fatal(err)
	}
}

// FuzzQuery1 feeds arbitrary datagrams to a full GameSpy 1 query. It must
// never panic, hang or produce invalid output.
func FuzzQuery1(f *testing.F) {
	f.Add([]byte(`\hostname\a\player_0\b\team_0\1\teamname_0\x\numplayers\-1\queryid\1.1\final\`), []byte{})
	f.Add([]byte(`\queryid\9.2\final\`), []byte(`\gamename\bfield1942\team_1e3\\queryid\9.1`))
	f.Fuzz(func(t *testing.T, first, second []byte) {
		res, err := Query1(context.Background(), &fakeConn{replies: [][]byte{first, second, first}}, Options{})
		if err == nil {
			checkResponse(t, res)
		}
	})
}

// FuzzQuery2 feeds arbitrary datagrams to a full GameSpy 2 query.
func FuzzQuery2(f *testing.F) {
	f.Add([]byte("\x00\x00\x00\x00\x01hostname\x00x\x00gamename\x00armygame\x00\x00"),
		[]byte("\x00\x00\x00\x00\x01\x00\x02player_\x00score_\x00\x00a\x001\x00b\x00x\x00"),
		[]byte("\x00\x00\x00\x00\x01\x00team_t\x00\x00t\x00"))
	f.Fuzz(func(t *testing.T, info, players, teams []byte) {
		res, err := Query2(context.Background(), &fakeConn{replies: [][]byte{info, players, teams}}, Options{})
		if err == nil {
			checkResponse(t, res)
		}
	})
}

// FuzzQuery3 feeds arbitrary datagrams to a full GameSpy 3 query.
func FuzzQuery3(f *testing.F) {
	f.Add([]byte("\x09\x00\x00\x00\x010\x00"),
		gs3Packet(0x80, "hostname\x00x\x00\x00\x01player_\x00\x00a\x00\x00score_\x00\x00\x31\x00\x00"), []byte{})
	f.Add([]byte("\x09\x00\x00\x00\x01-5\x00"), gs3Packet(1, "a\x00"), gs3Packet(0x80, "b\x00b\x00\x00"))
	f.Fuzz(func(t *testing.T, challenge, first, second []byte) {
		res, err := Query3(context.Background(), &fakeConn{replies: [][]byte{challenge, first, second}}, Options{})
		if err == nil {
			checkResponse(t, res)
		}
	})
}

// FuzzParse runs every parser on arbitrary bytes.
func FuzzParse(f *testing.F) {
	f.Add([]byte("\\a\\b\\player_1\\x"), []byte("\x00\x00f_\x00\x00v\x00"), []byte("k\x00v\x00\x00\x03x_t\x00\x05y\x00"))
	f.Fuzz(func(t *testing.T, a, b, c []byte) {
		out := newObject()
		for i, kv := 0, strings.Split(binreader.DecodeWindows1252(a), `\`); i+1 < len(kv); i += 2 {
			out.set(kv[i], kv[i+1])
		}
		checkResponse(t, Parse1(out))
		checkResponse(t, Parse2(a, b, c, len(a) > 0 && a[0]&1 == 0))
		if res, err := Parse3([][]byte{a, b, c}); err == nil {
			checkResponse(t, res)
		}
	})
}
