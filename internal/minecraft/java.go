package minecraft

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

// Java Edition Server List Ping, as node-gamedig's
// protocols/minecraftvanilla.js sends it: a handshake announcing protocol
// 47 and next state 1 (status), then an empty status request, both framed
// as VarInt length + VarInt packet ID + data. The reply is one status
// packet holding a JSON string.
//
// References:
//   - https://minecraft.wiki/w/Java_Edition_protocol/Server_List_Ping
//   - https://github.com/gamedig/node-gamedig/blob/master/protocols/minecraftvanilla.js
//   - https://github.com/gamedig/rust-gamedig/blob/main/crates/lib/src/games/minecraft/protocol/java.rs

const (
	handshakeProtocol = 47
	// maxStatusPacket caps the declared status packet length. Real replies
	// are a few KiB, up to ~50 KiB with a favicon.
	maxStatusPacket = 1 << 20
	// minStatusRead is how many bytes node-gamedig waits for before it
	// looks at the reply at all.
	minStatusRead = 10
)

var errVarint = errors.New("Could not decode varint")

// readVarint decodes an unsigned LEB128 VarInt at b[off:] like the npm
// varint package node-gamedig uses: at most 8 bytes, no minimal-length or
// 32-bit check. It returns the value and the bytes read.
func readVarint(b []byte, off int) (uint64, int, error) {
	var v uint64
	for i := 0; i < 8; i++ {
		if off+i >= len(b) || off+i < 0 {
			return 0, 0, errVarint
		}
		c := b[off+i]
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errVarint
}

func appendVarint(b []byte, v uint32) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendPacket(b []byte, id uint32, data []byte) []byte {
	idBuf := appendVarint(nil, id)
	b = appendVarint(b, uint32(len(idBuf)+len(data)))
	return append(append(b, idBuf...), data...)
}

// javaRequest is the handshake followed by the status request.
func javaRequest(host string, port int) []byte {
	var hs []byte
	hs = appendVarint(hs, handshakeProtocol)
	hs = appendVarint(hs, uint32(len(host)))
	hs = append(hs, host...)
	hs = binary.BigEndian.AppendUint16(hs, uint16(port))
	hs = appendVarint(hs, 1)
	return appendPacket(appendPacket(nil, 0, hs), 0, nil)
}

// statusComplete reports whether data (everything received so far) holds
// the whole status packet. It returns the bytes after the packet length,
// including any that arrived after the packet, as node-gamedig does.
//
// node-gamedig compares the bytes received, length prefix included, with
// the declared length, so it can stop 1-3 bytes short when a TCP segment
// ends there. This waits for the whole packet instead.
func statusComplete(data []byte) ([]byte, bool, error) {
	if len(data) < minStatusRead {
		return nil, false, nil
	}
	length, n, err := readVarint(data, 0)
	if err != nil {
		return nil, false, err
	}
	if length > maxStatusPacket {
		return nil, false, fmt.Errorf("status packet too large: %d bytes", length)
	}
	if uint64(len(data)-n) < length {
		return nil, false, nil
	}
	return data[n:], true, nil
}

// parseStatus turns the status packet (after its length prefix) into
// node-gamedig's vanilla Results.
func parseStatus(data []byte) (*sub, error) {
	_, n, err := readVarint(data, 0) // packet ID, not checked
	if err != nil {
		return nil, err
	}
	strLen, m, err := readVarint(data, n)
	if err != nil {
		return nil, err
	}
	rest := data[n+m:]
	end := uint64(len(rest))
	if strLen < end {
		end = strLen
	}
	status, err := parseJSON(binreader.DecodeUTF8(rest[:end]))
	if err != nil {
		return nil, err
	}
	obj, ok := status.(map[string]any)
	if !ok {
		return nil, errors.New("TypeError: status is not an object")
	}
	players, ok := obj["players"]
	if !ok || players == nil {
		return nil, errors.New("TypeError: status has no players")
	}

	s := newSub()
	s.maxPlayers = prop(players, "max")
	s.numPlayers = prop(players, "online")
	if sample := prop(players, "sample"); truthy(sample) {
		switch sample := sample.(type) {
		case []any:
			for _, p := range sample {
				if p == nil {
					return nil, errors.New("TypeError: null player in sample")
				}
				raw := map[string]any{}
				if id := prop(p, "id"); !isUndefined(id) {
					raw["id"] = id
				}
				s.players = append(s.players, newPlayer(prop(p, "name"), raw))
			}
		case string:
			// for...of iterates a string by code point.
			for range sample {
				s.players = append(s.players, newPlayer(undefined, map[string]any{}))
			}
		default:
			return nil, errors.New("TypeError: players.sample is not iterable")
		}
	}

	// Better Compatibility Checker sends a second JSON document after the
	// status string; node-gamedig skips one byte (its length) and parses
	// the rest.
	var bcc any = map[string]any{}
	if uint64(len(rest)) > strLen {
		bcc, err = parseJSON(binreader.DecodeUTF8(rest[strLen+1:]))
		if err != nil {
			return nil, fmt.Errorf("bcc: %w", err)
		}
	}
	obj["bcc"] = bcc
	s.raw = obj
	return s, nil
}

// parseJSON is JSON.parse for text that is already valid UTF-8. Numbers
// become float64; out-of-range ones become ±Inf as in JavaScript.
func parseJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("SyntaxError: %w", err)
	}
	if strings.TrimLeft(s[dec.InputOffset():], " \t\r\n") != "" {
		return nil, errors.New("SyntaxError: unexpected non-whitespace after JSON")
	}
	return numbersToFloat(v), nil
}

func numbersToFloat(v any) any {
	switch v := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return math.NaN()
		}
		return f
	case []any:
		for i, e := range v {
			v[i] = numbersToFloat(e)
		}
	case map[string]any:
		for k, e := range v {
			v[k] = numbersToFloat(e)
		}
	}
	return v
}

// queryJava runs the Server List Ping over a new TCP connection.
func (q *query) queryJava(ctx context.Context) (*sub, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(q.address, strconv.Itoa(q.opts.Port))
	dctx, cancel := context.WithTimeout(ctx, q.opts.SocketTimeout)
	start := time.Now()
	conn, err := q.dial(dctx, "tcp", addr)
	cancel()
	if err != nil {
		if ctx.Err() == nil && dctx.Err() != nil {
			return nil, fmt.Errorf("TCP Opening - Timed out after %s", q.opts.SocketTimeout)
		}
		return nil, fmt.Errorf("TCP Connection Refused: %w", err)
	}
	defer conn.Close()
	var rtt time.Duration
	registerRTT(&rtt, time.Since(start))
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	deadline := time.Now().Add(q.opts.SocketTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	req := javaRequest(q.opts.Host, q.opts.Port)
	q.debugf("%s TCP--> %x", addr, req)
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}

	var data []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			q.debugf("%s <--TCP %x", addr, buf[:n])
			data = append(data, buf[:n]...)
			body, ok, perr := statusComplete(data)
			if perr != nil {
				return nil, perr
			}
			if ok {
				s, err := parseStatus(body)
				if err != nil {
					return nil, err
				}
				s.rtt = rtt
				return s, nil
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, io.EOF) {
				// node-gamedig ignores the connection closing and waits
				// for its timeout; either way the sub-query fails.
				return nil, fmt.Errorf("TCP - Timed out after %s", q.opts.SocketTimeout)
			}
			return nil, err
		}
	}
}
