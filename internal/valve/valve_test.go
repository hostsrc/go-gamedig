package valve

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net"
	"os"
	"os/exec"
	"strings"
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

func info(protocol byte, appID uint16) []byte {
	b := []byte{0xff, 0xff, 0xff, 0xff, 0x49, protocol}
	b = append(b, "name\x00map\x00folder\x00game\x00"...)
	b = binary.LittleEndian.AppendUint16(b, appID)
	b = append(b, 2, 10, 0, 'd', 'l', 0, 0)
	return append(b, "1.0\x00\x00"...)
}

func split(uid uint32, total, num byte, sizeField bool, extra, payload []byte) []byte {
	b := binary.LittleEndian.AppendUint32([]byte{0xfe, 0xff, 0xff, 0xff}, uid)
	b = append(b, total, num)
	if sizeField {
		b = binary.LittleEndian.AppendUint16(b, 1248)
	}
	return append(append(b, extra...), payload...)
}

func bzip(t *testing.T, b []byte) []byte {
	t.Helper()
	cmd := exec.Command("bzip2", "-c")
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.Output()
	if err != nil {
		t.Skip("bzip2 not available:", err)
	}
	return out
}

func rulesPacket(n int) []byte {
	b := binary.LittleEndian.AppendUint16([]byte{0xff, 0xff, 0xff, 0xff, 0x45}, uint16(n))
	for i := range n {
		b = append(b, strings.Repeat("k", i+1)+"\x00v\x00"...)
	}
	return b
}

func TestCompressedSplitChecks(t *testing.T) {
	full := rulesPacket(20)
	compressed := bzip(t, full)
	header := func(size, crc uint32) []byte {
		return binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, size), crc)
	}
	tests := []struct {
		name    string
		size    uint32
		crc     uint32
		wantErr string
	}{
		{"valid", uint32(len(full)), crc32.ChecksumIEEE(full), ""},
		{"wrong crc", uint32(len(full)), 1, "CRC"},
		{"wrong size", uint32(len(full) - 1), crc32.ChecksumIEEE(full), "CRC"},
		{"declared bomb", maxDecompressed + 1, 0, "Invalid bzip packet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &fakeConn{replies: [][]byte{
				info(17, 730),
				split(0x80000001, 1, 0, true, header(tt.size, tt.crc), compressed),
			}}
			res, err := Query(context.Background(), conn, Options{RequestRules: true})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got := len(res.Raw["rules"].(map[string]string)); got != 20 {
					t.Fatalf("got %d rules, want 20", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSplitLimits(t *testing.T) {
	var replies [][]byte
	replies = append(replies, info(17, 730))
	for uid := range uint32(maxSplitIDs + 1) {
		replies = append(replies, split(uid, 2, 0, true, nil, []byte("x")))
	}
	_, err := Query(context.Background(), &fakeConn{replies: replies}, Options{RequestRules: true})
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("err = %v, want too many split replies", err)
	}

	big := bytes.Repeat([]byte("y"), 60000)
	replies = [][]byte{info(17, 730)}
	for num := range byte(20) {
		replies = append(replies, split(7, 255, num, true, nil, big))
	}
	_, err = Query(context.Background(), &fakeConn{replies: replies}, Options{RequestRules: true})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want split reply too large", err)
	}
}

func TestMissingSplitPart(t *testing.T) {
	conn := &fakeConn{replies: [][]byte{
		info(17, 730),
		split(9, 2, 0, true, nil, []byte("a")),
		split(9, 2, 5, true, nil, []byte("b")),
	}}
	_, err := Query(context.Background(), conn, Options{RequestRules: true})
	if err == nil || err.Error() != "Missing packet #1" {
		t.Fatalf("err = %v, want Missing packet #1", err)
	}
}

func TestInfoTimeoutIsError(t *testing.T) {
	_, err := Query(context.Background(), &fakeConn{}, Options{})
	if !errors.Is(err, errSocketTimeout) {
		t.Fatalf("err = %v, want socket timeout", err)
	}
}

func TestContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Query(ctx, &fakeConn{replies: [][]byte{info(17, 730)}}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestParseInt(t *testing.T) {
	for in, want := range map[string]int{"42": 42, " 7x": 7, "-3": -3, "+5": 5, "0012": 12, "0x1f": 31, "-0X10": -16, "12abc": 12} {
		if got, ok := parseInt(in); !ok || got != want {
			t.Errorf("parseInt(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "x1", "-", " ", "0x", "0xg"} {
		if _, ok := parseInt(in); ok {
			t.Errorf("parseInt(%q) ok, want NaN", in)
		}
	}
}

// FuzzQuery feeds arbitrary datagrams to a full query. It must never panic,
// hang, or produce invalid UTF-8.
func FuzzQuery(f *testing.F) {
	f.Add(info(48, 10), []byte{}, false)
	f.Add(info(7, 240), split(5, 2, 0, false, nil, []byte("\xff\xff\xff\xff\x44\x01\x00a\x00")), true)
	f.Add(info(17, 393380&0xffff), split(0x80000001, 1, 0, true, make([]byte, 8), []byte("BZh9")), true)
	f.Add([]byte("\xff\xff\xff\xff\x41\x01\x02\x03\x04"), []byte("\xff\xff\xff\xff\x6d"), false)
	f.Fuzz(func(t *testing.T, first, second []byte, rules bool) {
		conn := &fakeConn{replies: [][]byte{first, second, second, first}}
		res, err := Query(context.Background(), conn, Options{RequestPlayers: true, RequestRules: rules, GoldSrcInfo: len(first) > 0 && first[0] == 1})
		if err != nil {
			return
		}
		for _, s := range []string{res.Name, res.Map, res.Version} {
			if !utf8.ValidString(s) {
				t.Fatalf("invalid UTF-8 %q", s)
			}
		}
		for _, p := range append(res.Players, res.Bots...) {
			if !utf8.ValidString(p.Name) {
				t.Fatalf("invalid UTF-8 player %q", p.Name)
			}
		}
	})
}

func FuzzParse(f *testing.F) {
	f.Add(info(17, 730)[5:])
	f.Add([]byte("\x02\x00a\x00\x01\x00\x00\x00\x00\x00\x80\x3f"))
	f.Fuzz(func(t *testing.T, b []byte) {
		ParseInfo(b, false, &Response{Raw: map[string]any{}})
		ParseInfo(b, true, &Response{Raw: map[string]any{}})
		_ = ParsePlayers(b, 730)
		_ = ParseRules(b)
	})
}
