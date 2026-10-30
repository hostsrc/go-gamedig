package minecraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net"
	"testing"
	"time"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

func TestReadVarintLikeNpm(t *testing.T) {
	tests := []struct {
		in   []byte
		want uint64
		n    int
		err  bool
	}{
		{[]byte{0}, 0, 1, false},
		{[]byte{0x7f}, 127, 1, false},
		{[]byte{0xff, 0x01}, 255, 2, false},
		{[]byte{0x80, 0x00}, 0, 2, false}, // non-minimal is accepted
		{[]byte{0xff, 0xff, 0xff, 0xff, 0x0f}, 1<<32 - 1, 5, false},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, 1<<56 - 1, 8, false},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, 0, 0, true}, // 9 bytes
		{[]byte{0x80}, 0, 0, true},
		{nil, 0, 0, true},
	}
	for _, tt := range tests {
		v, n, err := readVarint(tt.in, 0)
		if (err != nil) != tt.err || v != tt.want || n != tt.n {
			t.Errorf("readVarint(%x) = %d, %d, %v", tt.in, v, n, err)
		}
	}
}

func TestJavaRequestIsNodes(t *testing.T) {
	got := javaRequest("127.0.0.1", 0x1234)
	want := []byte("\x0f\x00\x2f\x09127.0.0.1\x12\x34\x01\x01\x00")
	if !bytes.Equal(got, want) {
		t.Errorf("javaRequest = %x, want %x", got, want)
	}
}

func TestParseInt(t *testing.T) {
	tests := map[string]float64{
		"42": 42, "  -7x": -7, "+3": 3, "0x1F": 31, "  12": 12, "1e3": 1,
		"99999999999999999999": 1e20,
	}
	for in, want := range tests {
		if got := parseInt(in); got != want {
			t.Errorf("parseInt(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "abc", "-", "0x", " "} {
		if got := parseInt(in); !math.IsNaN(got) {
			t.Errorf("parseInt(%q) = %v, want NaN", in, got)
		}
	}
}

func TestCleanName(t *testing.T) {
	tests := map[string]string{
		"  §aA   §lMinecraft§r\n Server  ": " A Minecraft Server ",
		"a§\nb":                            "ab",
		"§§a":                              "a",
		"end §":                            "end §",
		"§🎮x":                              "�x",
		"tab\t  x":                         "tab x",
	}
	for in, want := range tests {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJavaNameReversesExtraAndStopsAtNull(t *testing.T) {
	var desc any
	_ = json.Unmarshal([]byte(`{"text":"a","extra":[{"text":"b"},{"text":"c","extra":[{"text":"d"},{"text":"e"}]}]}`), &desc)
	name, ok := javaName(desc)
	if !ok || name != "abcde" {
		t.Errorf("javaName = %q, %v", name, ok)
	}
	b, _ := json.Marshal(desc)
	if want := `{"extra":[{"extra":[{"text":"e"},{"text":"d"}],"text":"c"},{"text":"b"}],"text":"a"}`; string(b) != want {
		t.Errorf("extra after walk = %s, want %s", b, want)
	}
	_ = json.Unmarshal([]byte(`{"text":"a","extra":[null,{"text":"b"}]}`), &desc)
	if _, ok := javaName(desc); ok {
		t.Error("a null component should abort the walk")
	}
	if name, ok := javaName(nil); ok || name != "" {
		t.Errorf("null description: %q, %v", name, ok)
	}
	if name, ok := javaName(5.0); !ok || name != "" {
		t.Errorf("number description: %q, %v", name, ok)
	}
}

type fakeResolver struct {
	srv   map[string][]*net.SRV
	hosts map[string][]string
	calls []string
}

func (r *fakeResolver) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	r.calls = append(r.calls, "srv:"+name)
	if s, ok := r.srv[name]; ok {
		return "", s, nil
	}
	return "", nil, errors.New("no such host")
}

func (r *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	r.calls = append(r.calls, "host:"+host)
	if a, ok := r.hosts[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func TestResolveSRV(t *testing.T) {
	r := &fakeResolver{
		srv: map[string][]*net.SRV{
			"mc.example.com":   {{Target: "node1.example.net.", Port: 25570}},
			"loop.example.com": {{Target: "loop.example.com.", Port: 1}},
			"bad.example.com":  {{Target: "missing.example.net.", Port: 1}},
			"a.example.com":    {{Target: "b.example.com.", Port: 1}},
			"b.example.com":    {{Target: "a.example.com.", Port: 1}},
		},
		hosts: map[string][]string{
			"node1.example.net": {"192.0.2.10", "192.0.2.11"},
			"loop.example.com":  {"192.0.2.20"},
			"bad.example.com":   {"192.0.2.30"},
			"plain.example.com": {"192.0.2.40"},
			"a.example.com":     {"192.0.2.50"},
		},
	}
	debugf := func(string, ...any) {}
	tests := map[string]string{
		"mc.example.com":    "192.0.2.10",
		"loop.example.com":  "192.0.2.20",
		"bad.example.com":   "192.0.2.30",
		"plain.example.com": "192.0.2.40",
		"a.example.com":     "192.0.2.50",
	}
	for host, want := range tests {
		got, err := resolve(context.Background(), r, host, 0, debugf)
		if err != nil || got != want {
			t.Errorf("resolve(%s) = %s, %v; want %s", host, got, err, want)
		}
	}
	r.calls = nil
	for _, ip := range []string{"203.0.113.5", "2001:db8::1"} {
		if got, err := resolve(context.Background(), r, ip, 0, debugf); err != nil || got != ip {
			t.Errorf("resolve(%s) = %s, %v", ip, got, err)
		}
	}
	if len(r.calls) != 0 {
		t.Errorf("IP literals looked up: %v", r.calls)
	}
	if _, err := resolve(context.Background(), r, "nowhere.example", 0, debugf); err == nil {
		t.Error("want an error for an unknown name")
	}
}

// serve runs a fixture server and queries it.
func serve(t *testing.T, timeout time.Duration, exchanges ...fixture.Exchange) (*Response, error, time.Duration) {
	t.Helper()
	srv, err := fixture.Serve(exchanges)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	start := time.Now()
	res, err := Query(context.Background(), Options{Host: "127.0.0.1", Port: srv.Port(), SocketTimeout: timeout})
	return res, err, time.Since(start)
}

func tcpEx(responses ...[]byte) fixture.Exchange {
	e := fixture.Exchange{Network: "tcp", Request: javaRequest("127.0.0.1", 0), PortOffsets: []int{13}}
	for _, r := range responses {
		e.Responses = append(e.Responses, r)
	}
	return e
}

func statusPacket(js string) []byte {
	data := append(appendVarint([]byte{0}, uint32(len(js))), js...)
	return append(appendVarint(nil, uint32(len(data))), data...)
}

func TestJavaOversizedStatusFailsFast(t *testing.T) {
	huge := appendVarint(nil, maxStatusPacket+1)
	huge = append(huge, make([]byte, 16)...)
	srv, err := fixture.Serve([]fixture.Exchange{tcpEx(huge)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var d net.Dialer
	q := &query{opts: Options{Host: "127.0.0.1", Port: srv.Port(), SocketTimeout: 2 * time.Second}, address: "127.0.0.1", dial: d.DialContext}
	start := time.Now()
	if _, err := q.queryJava(context.Background()); err == nil || time.Since(start) > time.Second {
		t.Errorf("err = %v after %s; want a quick failure", err, time.Since(start))
	}
}

func TestQueryJavaOnly(t *testing.T) {
	res, err, _ := serve(t, 200*time.Millisecond, tcpEx(statusPacket(`{"players":{"max":5,"online":1},"description":{"text":"§cHi"}}`)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "Hi" || res.MaxPlayers != 5 || res.NumPlayers != 1 || res.Version != "" {
		t.Errorf("res = %+v", res)
	}
	if _, ok := res.Raw["vanilla"]; !ok || len(res.Raw) != 1 {
		t.Errorf("raw = %v", res.Raw)
	}
}

func TestQueryHonoursContext(t *testing.T) {
	srv, err := fixture.Serve([]fixture.Exchange{tcpEx()})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Query(ctx, Options{Host: "127.0.0.1", Port: srv.Port(), SocketTimeout: 5 * time.Second})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("err = %v after %s", err, time.Since(start))
	}
}

func TestGamespy3ChallengeRange(t *testing.T) {
	for in, want := range map[string]any{"0": nil, "-0": nil, "abc": int32(0), "-42": int32(-42), "2147483647": int32(math.MaxInt32)} {
		c, err := gs3ParseChallenge(append([]byte(in), 0))
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if (want == nil) != (c == nil) || c != nil && *c != want.(int32) {
			t.Errorf("challenge %q = %v, want %v", in, c, want)
		}
	}
	if _, err := gs3ParseChallenge([]byte("2147483648")); err == nil {
		t.Error("an out-of-range key should fail like Buffer.writeInt32BE")
	}
}

func TestGamespy3InheritedItemType(t *testing.T) {
	if _, err := parseGamespy3([][]byte{[]byte("\x00x_constructor\x00\x00val\x00\x00")}); err == nil {
		t.Error("an item type found on Object.prototype should fail")
	}
	s, err := parseGamespy3([][]byte{[]byte("\x00x_toString\x00\x00\x00")})
	if err != nil {
		t.Fatal(err)
	}
	if pti := s.raw["playerTeamInfo"].(map[string]any); len(pti) != 0 {
		t.Errorf("playerTeamInfo = %v", pti)
	}
}

// checkJSON fails unless v holds only JSON value types and marshals once
// NaN and Inf are replaced, as the root package's Number does.
func checkJSON(t *testing.T, v any) {
	t.Helper()
	var walk func(any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case nil, bool, string, int:
			return v
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil
			}
			return v
		case []any:
			out := make([]any, len(v))
			for i, e := range v {
				out[i] = walk(e)
			}
			return out
		case map[string]any:
			out := map[string]any{}
			for k, e := range v {
				out[k] = walk(e)
			}
			return out
		}
		t.Fatalf("non-JSON value %T in result", v)
		return nil
	}
	if _, err := json.Marshal(walk(v)); err != nil {
		t.Fatal(err)
	}
}

func FuzzVarint(f *testing.F) {
	f.Add([]byte{0xff, 0xff, 0x03})
	f.Add([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01})
	f.Fuzz(func(t *testing.T, b []byte) {
		v, n, err := readVarint(b, 0)
		if err != nil {
			return
		}
		if n < 1 || n > 8 || n > len(b) || b[n-1] >= 0x80 {
			t.Fatalf("readVarint(%x) read %d bytes", b, n)
		}
		if v <= math.MaxUint32 {
			enc := appendVarint(nil, uint32(v))
			if w, m, err := readVarint(enc, 0); err != nil || w != v || m != len(enc) {
				t.Fatalf("round trip of %d: %d, %d, %v", v, w, m, err)
			}
		}
	})
}

// FuzzStatusPacket feeds arbitrary bytes through the TCP reply path: the
// completeness check, packet parsing, JSON decoding and the merge.
func FuzzStatusPacket(f *testing.F) {
	f.Add(statusPacket(`{"players":{"max":1,"online":0,"sample":[{"name":"a","id":"b"}]},"description":{"extra":[{"text":"x"}]}}`))
	f.Add(append(statusPacket(`{"players":{}}`), "x{}"...))
	f.Add([]byte{0xff, 0x00, 0x23, 0x00, 0xa7, 0x00, 0x31, 0x00, 0x00, 0x00, 0x37})
	f.Fuzz(func(t *testing.T, data []byte) {
		body, ok, err := statusComplete(data)
		if err != nil || !ok {
			return
		}
		s, err := parseStatus(body)
		if err != nil {
			return
		}
		res := merge(Options{Host: "h", Port: 1}, s, nil, nil)
		checkJSON(t, res.Raw)
		for _, p := range res.Players {
			checkJSON(t, p.Raw)
		}
	})
}

// FuzzStatusJSON exercises the JSON and merge rules with well-framed
// packets around arbitrary status text.
func FuzzStatusJSON(f *testing.F) {
	for _, s := range []string{
		`{"players":{"max":5,"online":"x","sample":"abc"},"version":3,"description":[1,{"text":null}]}`,
		`{"players":1,"description":{"text":{"a":[]},"extra":[{"extra":[null]}]}}`,
		`{"players":{"sample":[1,"a",[],{}]},"version":{"name":[null,1.5e300]},"n":1e999}`,
	} {
		f.Add(s, "")
	}
	f.Fuzz(func(t *testing.T, status, bcc string) {
		data := append(appendVarint([]byte{0}, uint32(len(status))), status...)
		if bcc != "" {
			data = append(append(data, 0), bcc...)
		}
		s, err := parseStatus(data)
		if err != nil {
			return
		}
		res := merge(Options{Host: "h", Port: 1}, s, nil, nil)
		checkJSON(t, res.Raw)
	})
}

func FuzzBedrock(f *testing.F) {
	pong := func(status string) []byte {
		b := append([]byte{0x1c}, bedrockNonce...)
		b = append(b, make([]byte, 8)...)
		b = append(b, bedrockMagic...)
		b = binary.BigEndian.AppendUint16(b, uint16(len(status)))
		return append(b, status...)
	}
	f.Add(pong("MCPE;name;1;1.0;2;3;id;map;mode;1;19132;19133;"))
	f.Add(pong("a;b"))
	f.Add([]byte{0x1c})
	f.Fuzz(func(t *testing.T, p []byte) {
		s, err := parseBedrock(p)
		if err != nil || s == nil {
			return
		}
		checkJSON(t, s.json("h", 1))
		res := merge(Options{Host: "h", Port: 1}, nil, nil, s)
		checkJSON(t, res.Raw)
	})
}

// FuzzGamespy3 splits the input on "|" into datagrams and runs them
// through reassembly and parsing.
func FuzzGamespy3(f *testing.F) {
	f.Add([]byte("\x00\x00\x00\x00\x01splitnum\x00\x80\x00hostname\x00x\x00\x00\x01player_\x00\x00a\x00\x00"))
	f.Add([]byte("\x00\x00\x00\x00\x01splitnum\x00\x81\x00\x01team_t\x00\x05r\x00|\x00\x00\x00\x00\x01splitnum\x00\x00\x00k\x00p1\x00v\x00"))
	f.Fuzz(func(t *testing.T, in []byte) {
		asm := &gs3Assembler{packets: map[int][]byte{}}
		for _, p := range bytes.Split(in, []byte("|")) {
			r, ok := gs3Header(p, gs3Stat)
			if !ok {
				continue
			}
			list, err := asm.add(r)
			if err != nil {
				return
			}
			if list == nil {
				continue
			}
			s, err := parseGamespy3(list)
			if err != nil {
				return
			}
			checkJSON(t, s.json("h", 1))
			res := merge(Options{Host: "h", Port: 1}, nil, s, nil)
			checkJSON(t, res.Raw)
			return
		}
		_, _ = gs3ParseChallenge(in)
	})
}
