// Package ase implements the All-Seeing Eye query protocol, used by
// Multi Theft Auto, Far Cry, Call of Juarez, Age of Empires 2 and others.
//
// Behaviour follows node-gamedig's protocols/ase.js so results are
// interchangeable with it. rust-gamedig has no ASE implementation to
// cross-check against.
//
// References:
//   - https://github.com/gamedig/node-gamedig/blob/master/protocols/ase.js
package ase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

const maxDatagram = 65535

var (
	errSocketTimeout = errors.New("UDP - Timed out")
	errNetwork       = errors.New("UDP")
)

// Options control one query.
type Options struct {
	// SocketTimeout bounds the request/response exchange.
	SocketTimeout time.Duration
	// Debugf, when set, receives a trace of every datagram.
	Debugf func(format string, args ...any)
}

// Player field flags, as sent before each player.
const (
	FlagName  = 1
	FlagTeam  = 2
	FlagSkin  = 4
	FlagScore = 8
	FlagPing  = 16
	FlagTime  = 32
)

// Player is one player entry. Only the fields whose flag is set in Flags
// were sent. Score, Ping and Time are JavaScript parseInt results, so they
// can be NaN.
type Player struct {
	Flags byte
	Name  string
	Team  string
	Skin  string
	Score float64
	Ping  float64
	Time  float64
}

// Response is the decoded server state.
type Response struct {
	Name     string
	Map      string
	Version  string
	Password bool
	// NumPlayers, MaxPlayers and GamePort are JavaScript parseInt results:
	// NaN when the field is missing or not a number.
	NumPlayers float64
	MaxPlayers float64
	GamePort   float64
	// Raw holds "gamename", "gametype" and every server rule, under
	// node-gamedig's key names. A rule named like one of the first two
	// replaces it, as in node-gamedig.
	Raw     map[string]string
	Players []Player
	// RTT is the shortest observed round trip.
	RTT time.Duration
}

// Query sends the ASE status request over conn, which must be a UDP
// socket connected to the server's query port, and decodes the reply.
func Query(ctx context.Context, conn net.Conn, opts Options) (*Response, error) {
	if opts.SocketTimeout <= 0 {
		opts.SocketTimeout = 2 * time.Second
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	debugf := func(format string, args ...any) {
		if opts.Debugf != nil {
			opts.Debugf(format, args...)
		}
	}
	req := []byte("s")
	debugf("--> %x", req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("%w: %w", errNetwork, err)
	}
	start := time.Now()
	deadline := start.Add(opts.SocketTimeout)
	ctxDeadline, hasCtxDeadline := ctx.Deadline()
	if hasCtxDeadline && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	buf := make([]byte, maxDatagram)
	var rtt time.Duration
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if hasCtxDeadline && !time.Now().Before(ctxDeadline) {
					return nil, context.DeadlineExceeded
				}
				return nil, fmt.Errorf("%w after %s", errSocketTimeout, opts.SocketTimeout)
			}
			return nil, fmt.Errorf("%w: %w", errNetwork, err)
		}
		if d := time.Since(start).Truncate(time.Millisecond); rtt == 0 || d < rtt {
			rtt = d
		}
		debugf("<-- %x", buf[:n])
		if body, ok := bytes.CutPrefix(buf[:n], []byte("EYE1")); ok {
			res := Parse(body)
			res.RTT = rtt
			return res, nil
		}
	}
}

// Parse decodes the body of an ASE reply, after the "EYE1" header.
func Parse(b []byte) *Response {
	r := binreader.New(b)
	res := &Response{Raw: map[string]string{}, Players: []Player{}}
	res.Raw["gamename"] = readString(r)
	res.GamePort = ParseInt(readString(r))
	res.Name = readString(r)
	res.Raw["gametype"] = readString(r)
	res.Map = readString(r)
	res.Version = readString(r)
	res.Password = readString(r) == "1"
	res.NumPlayers = ParseInt(readString(r))
	res.MaxPlayers = ParseInt(readString(r))

	for !r.Done() {
		key := readString(r)
		if key == "" {
			break
		}
		value := readString(r)
		// Assigning a string to __proto__ is a no-op in JavaScript.
		if key != "__proto__" {
			res.Raw[key] = value
		}
	}

	// Each player takes at least its flags byte, so this is bounded by the
	// datagram size.
	for !r.Done() {
		p := Player{Flags: r.Uint8()}
		if p.Flags&FlagName != 0 {
			p.Name = readString(r)
		}
		if p.Flags&FlagTeam != 0 {
			p.Team = readString(r)
		}
		if p.Flags&FlagSkin != 0 {
			p.Skin = readString(r)
		}
		if p.Flags&FlagScore != 0 {
			p.Score = ParseInt(readString(r))
		}
		if p.Flags&FlagPing != 0 {
			p.Ping = ParseInt(readString(r))
		}
		if p.Flags&FlagTime != 0 {
			p.Time = ParseInt(readString(r))
		}
		res.Players = append(res.Players, p)
	}
	return res
}

// readString reads a string prefixed by one length byte that counts
// itself, decoded as UTF-8 (node-gamedig's pascalString(1, -1)).
func readString(r *binreader.Reader) string {
	return binreader.DecodeUTF8(r.Bytes(int(r.Uint8()) - 1))
}

// ParseInt mirrors JavaScript's parseInt(s) with no radix: leading
// whitespace, an optional sign and 0x prefix, then the longest run of
// digits, or NaN when there are none. The result is correctly rounded, as
// in V8.
func ParseInt(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	hex := len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')
	if hex {
		s = s[2:]
	}
	n := 0
	for n < len(s) && isDigit(s[n], hex) {
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	var v float64
	if hex {
		i, _ := new(big.Int).SetString(s[:n], 16)
		v, _ = new(big.Float).SetInt(i).Float64()
	} else {
		// ParseFloat rounds correctly and gives ±Inf when out of range,
		// like parseInt.
		v, _ = strconv.ParseFloat(s[:n], 64)
	}
	if neg {
		v = -v
	}
	return v
}

func isDigit(c byte, hex bool) bool {
	if c >= '0' && c <= '9' {
		return true
	}
	return hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')
}

// isJSSpace matches JavaScript's WhiteSpace and LineTerminator characters.
func isJSSpace(r rune) bool {
	return (unicode.IsSpace(r) && r != '\u0085') || r == '\uFEFF'
}
