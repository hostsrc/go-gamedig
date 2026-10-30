// Package quake implements the Quake family of server status queries: Quake
// 1 (QuakeWorld), Quake 2 and Quake 3 "getstatus", plus the node-gamedig
// protocols built on them (hexen2, warsow, xonotic). Call of Duty 1-4, Jedi
// Knight, Wolfenstein: Enemy Territory, Urban Terror and many other id Tech
// games answer the Quake 3 query.
//
// A query is one datagram each way. The reply is a header line, a line of
// backslash-separated cvars and one line per player:
//
//	\xff\xff\xff\xffstatusResponse\n
//	\sv_hostname\My server\mapname\q3dm17\n
//	12 50 "Player"\n
//
// Behaviour follows node-gamedig 5.3.3 (protocols/quake1.js, quake2.js,
// quake3.js, hexen2.js, warsow.js, xonotic.js) so results are
// interchangeable with it, including its quirks: cvars that become
// password, maxplayers and numplayers stay strings, and player numbers are
// JavaScript parseInt results (NaN when unparsable). rust-gamedig
// (crates/lib/src/protocols/quake) was used as a second reading of the
// packet format; its response headers and the "\xff\xff\xff\xff" prefix
// check agree with node-gamedig's.
//
// References:
//   - https://github.com/gamedig/node-gamedig/tree/master/protocols
//   - https://github.com/gamedig/rust-gamedig/tree/main/crates/lib/src/protocols/quake
package quake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

// Variant selects one of node-gamedig's Quake protocol classes.
type Variant int

// Variants, named after node-gamedig's protocols.
const (
	Quake1 Variant = iota
	Quake2
	Quake3
	Hexen2
	Warsow
	Xonotic
)

type variantSpec struct {
	send, response string
	// quake1 replies have no newline after the header and a different
	// player line.
	quake1 bool
	// colors means the quake3 family: names and cvars may carry ^ color
	// codes that StripColors removes.
	colors bool
}

var specs = map[Variant]variantSpec{
	Quake1:  {send: "status", response: "n", quake1: true},
	Hexen2:  {send: "\xffstatus\n", response: "\xffn", quake1: true},
	Quake2:  {send: "status", response: "print"},
	Quake3:  {send: "getstatus", response: "statusResponse", colors: true},
	Warsow:  {send: "getstatus", response: "statusResponse", colors: true},
	Xonotic: {send: "getstatus", response: "statusResponse", colors: true},
}

const maxDatagram = 65535

var (
	errSocketTimeout = errors.New("UDP - Timed out")
	errNetwork       = errors.New("UDP")
)

// Options control one query.
type Options struct {
	Variant Variant
	// StripColors removes Quake 3 color codes (^1, ^Xrrggbb) from the server
	// name, cvars and player names. node-gamedig strips them by default.
	StripColors bool
	// SocketTimeout bounds the request/response exchange.
	SocketTimeout time.Duration
	// Debugf, when set, receives a trace of every datagram.
	Debugf func(format string, args ...any)
}

// Player is one player line. Raw holds node-gamedig's fields: numbers are
// float64 (parseInt results, possibly NaN) and text fields are strings.
type Player struct {
	Name string
	Raw  map[string]any
}

// Response is the decoded server state.
type Response struct {
	Name    string
	Map     string
	Version string
	// Password is g_needpass, reported by node-gamedig as the server's
	// string. HasPassword is false when the cvar is absent (node: false).
	Password    string
	HasPassword bool
	// MaxPlayers is sv_maxclients or maxclients as a string. HasMaxPlayers
	// is false when neither is present (node: 0).
	MaxPlayers    string
	HasMaxPlayers bool
	// NumPlayers is the player count, unless HasNumPlayersText: node-gamedig
	// then reports the "clients" cvar string NumPlayersText instead.
	NumPlayers        int
	NumPlayersText    string
	HasNumPlayersText bool
	// Raw holds every cvar.
	Raw     map[string]string
	Players []Player
	Bots    []Player
	// RTT is the shortest observed round trip.
	RTT time.Duration
}

// Query sends the status request over conn, which must be a UDP socket
// connected to the server's query port, and parses the reply.
func Query(ctx context.Context, conn net.Conn, opts Options) (*Response, error) {
	spec, ok := specs[opts.Variant]
	if !ok {
		return nil, fmt.Errorf("unknown quake variant %d", opts.Variant)
	}
	if opts.SocketTimeout <= 0 {
		opts.SocketTimeout = 2 * time.Second
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	req := append([]byte("\xff\xff\xff\xff"+spec.send), 0)
	body, rtt, err := exchange(ctx, conn, req, opts, func(p []byte) ([]byte, bool) {
		return MatchReply(p, opts.Variant)
	})
	if err != nil {
		return nil, err
	}
	res := Parse(body, opts.Variant, opts.StripColors)
	res.RTT = rtt
	return res, nil
}

// MatchReply checks that p is a status reply for v and returns its body.
func MatchReply(p []byte, v Variant) ([]byte, bool) {
	spec := specs[v]
	r := binreader.New(p)
	if !bytes.Equal(r.Bytes(4), []byte{0xff, 0xff, 0xff, 0xff}) {
		return nil, false
	}
	// The headers are ASCII apart from hexen2's leading 0xFF, which
	// Windows-1252 maps to U+00FF; comparing bytes is the same as
	// node-gamedig comparing decoded strings.
	var typ []byte
	if spec.quake1 {
		typ = r.Bytes(len(spec.response))
	} else {
		typ = r.Until('\n')
	}
	if string(typ) != spec.response {
		return nil, false
	}
	return r.Rest(), true
}

// Parse decodes a reply body (after the header) the way node-gamedig does.
func Parse(body []byte, v Variant, stripColors bool) *Response {
	spec := specs[v]
	r := binreader.New(body)
	res := &Response{Raw: map[string]string{}, Players: []Player{}, Bots: []Player{}}

	info := strings.Split(binreader.DecodeWindows1252(r.Until('\n')), "\\")
	if info[0] == "" {
		info = info[1:]
	}
	for i := 0; i+1 < len(info); i += 2 {
		// Assigning a string to __proto__ is a no-op in JavaScript.
		if info[i] != "__proto__" {
			res.Raw[info[i]] = info[i+1]
		}
	}

	for !r.Done() {
		line := binreader.DecodeWindows1252(r.Until('\n'))
		if line == "" || line[0] == 0 {
			break
		}
		args := splitArgs(line)
		var p Player
		var ping float64
		if spec.quake1 {
			p.Raw = map[string]any{
				"id":    parseInt(shift(&args)),
				"score": parseInt(shift(&args)),
				"time":  parseInt(shift(&args)),
			}
			ping = parseInt(shift(&args))
			p.Raw["ping"] = ping
			if len(args) > 0 {
				p.Name = shift(&args)
			}
			if len(args) > 0 {
				p.Raw["skin"] = shift(&args)
			}
			p.Raw["color1"] = parseInt(shift(&args))
			p.Raw["color2"] = parseInt(shift(&args))
		} else {
			ping = parseFragsPing(&args, &p)
			p.Name = shift(&args)
			if addr := shift(&args); addr != "" {
				p.Raw["address"] = addr
			}
		}
		// node: (player.ping ? players : bots). NaN and ±0 are falsy.
		if ping != 0 && !math.IsNaN(ping) {
			res.Players = append(res.Players, p)
		} else {
			res.Bots = append(res.Bots, p)
		}
	}

	raw := res.Raw
	if s, ok := raw["g_needpass"]; ok {
		res.Password, res.HasPassword = s, true
	}
	if s, ok := raw["mapname"]; ok {
		res.Map = s
	}
	if s, ok := raw["sv_maxclients"]; ok {
		res.MaxPlayers, res.HasMaxPlayers = s, true
	}
	if s, ok := raw["maxclients"]; ok {
		res.MaxPlayers, res.HasMaxPlayers = s, true
	}
	if s, ok := raw["sv_hostname"]; ok {
		res.Name = s
	}
	if s, ok := raw["hostname"]; ok {
		res.Name = s
	}
	if s, ok := raw["version"]; ok {
		res.Version = s
	}
	// node-gamedig's quake2.js only uses the "clients" cvar when "iv" is
	// present; otherwise it counts the player lines.
	if s, ok := raw["iv"]; ok {
		res.Version = s
		if c, ok := raw["clients"]; ok {
			res.NumPlayersText, res.HasNumPlayersText = c, true
		}
	} else {
		res.NumPlayers = len(res.Players) + len(res.Bots)
	}

	if spec.quake1 {
		if s, ok := raw["*version"]; ok {
			res.Version = s
		}
	}
	if spec.colors && stripColors {
		res.Name = StripColors(res.Name)
		for k, s := range raw {
			raw[k] = StripColors(s)
		}
		for i := range res.Players {
			res.Players[i].Name = StripColors(res.Players[i].Name)
		}
		for i := range res.Bots {
			res.Bots[i].Name = StripColors(res.Bots[i].Name)
		}
	}
	if spec.colors {
		// quake3.js re-reads version after stripping the cvars, so it wins
		// over "iv" here.
		if s, ok := raw["version"]; ok {
			res.Version = s
		}
	}
	if v == Xonotic {
		// xonotic.js: a numeric name with an address field is the team, and
		// the address is the name. Only players, not bots.
		for i := range res.Players {
			p := &res.Players[i]
			if addr, _ := p.Raw["address"].(string); addr != "" && isNumeric(p.Name) {
				p.Raw["team"] = p.Name
				p.Name = addr
			}
		}
	}
	// warsow.js copies player.address to player.team, but address lives in
	// player.raw, so it only sets an undefined field JSON leaves out: its
	// output is quake3's.
	return res
}

// parseFragsPing reads the frags and ping of a quake2/quake3 player line.
func parseFragsPing(args *[]string, p *Player) float64 {
	frags := parseInt(shift(args))
	ping := parseInt(shift(args))
	p.Raw = map[string]any{"frags": frags, "ping": ping}
	return ping
}

// shift removes and returns the first argument, or "" when none are left.
// Callers that must tell "missing" from "empty" check len first.
func shift(args *[]string) string {
	if len(*args) == 0 {
		return ""
	}
	s := (*args)[0]
	*args = (*args)[1:]
	return s
}

// splitArgs splits a player line like node-gamedig: text inside double
// quotes is one argument (even when empty), text outside is split on
// spaces with empty pieces dropped.
func splitArgs(line string) []string {
	var args []string
	for i, part := range strings.Split(line, `"`) {
		if i%2 == 1 {
			args = append(args, part)
			continue
		}
		for _, sub := range strings.Split(part, " ") {
			if sub != "" {
				args = append(args, sub)
			}
		}
	}
	return args
}

// StripColors removes Quake 3 color codes like node-gamedig's
// str.replace(/\^(X.{6}|.)/g, ”): a caret followed by X and six
// characters (an RGB code), or by any one character. JavaScript's "."
// matches one UTF-16 unit other than \n, \r, U+2028 and U+2029; Quake
// strings are decoded from Windows-1252, so a unit is always one rune.
func StripColors(s string) string {
	if !strings.Contains(s, "^") {
		return s
	}
	rs := []rune(s)
	dot := func(r rune) bool { return r != '\n' && r != '\r' && r != 0x2028 && r != 0x2029 }
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(rs); {
		if rs[i] == '^' && i+1 < len(rs) {
			if rs[i+1] == 'X' && i+7 < len(rs) {
				ok := true
				for _, c := range rs[i+2 : i+8] {
					ok = ok && dot(c)
				}
				if ok {
					i += 8
					continue
				}
			}
			if dot(rs[i+1]) {
				i += 2
				continue
			}
		}
		out.WriteRune(rs[i])
		i++
	}
	return out.String()
}

// exchange writes req and passes each datagram to match until it accepts
// one or SocketTimeout passes.
func exchange(ctx context.Context, conn net.Conn, req []byte, opts Options, match func([]byte) ([]byte, bool)) ([]byte, time.Duration, error) {
	debugf := func(format string, args ...any) {
		if opts.Debugf != nil {
			opts.Debugf(format, args...)
		}
	}
	debugf("--> %x", req)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if _, err := conn.Write(req); err != nil {
		return nil, 0, fmt.Errorf("%w: %w", errNetwork, err)
	}
	buf := make([]byte, maxDatagram)
	start := time.Now()
	deadline := start.Add(opts.SocketTimeout)
	ctxDeadline, hasCtxDeadline := ctx.Deadline()
	if hasCtxDeadline && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	var rtt time.Duration
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, 0, err
		}
		// Checked after setting the deadline: a cancellation that raced the
		// call above has already moved the deadline to now, or is seen here.
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		n, err := conn.Read(buf)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, 0, ctxErr
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if hasCtxDeadline && !time.Now().Before(ctxDeadline) {
					return nil, 0, context.DeadlineExceeded
				}
				return nil, 0, fmt.Errorf("%w after %s", errSocketTimeout, opts.SocketTimeout)
			}
			return nil, 0, fmt.Errorf("%w: %w", errNetwork, err)
		}
		// node-gamedig registers a round trip for every datagram.
		if d := time.Since(start).Truncate(time.Millisecond); rtt == 0 || d < rtt {
			rtt = d
		}
		debugf("<-- %x", buf[:n])
		if body, ok := match(buf[:n]); ok {
			return bytes.Clone(body), rtt, nil
		}
	}
}
