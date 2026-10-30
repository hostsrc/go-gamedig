// Package unreal2 implements the Unreal Engine 2 server query protocol
// used by Unreal Tournament 2003/2004, Killing Floor, Red Orchestra and
// other UE2 games.
//
// Behaviour follows node-gamedig's protocols/unreal2.js so results are
// interchangeable with it, including the Killing Floor extra fields and
// the inverted GamePassword test. The string format was cross-checked
// against rust-gamedig's Unreal2StringDecoder.
//
// Like node-gamedig, every request collects replies until SocketTimeout
// passes, since a reply may span any number of datagrams. The collected
// bytes are capped, which node-gamedig does not do.
//
// References:
//   - https://github.com/gamedig/node-gamedig/blob/master/protocols/unreal2.js
//   - https://github.com/gamedig/rust-gamedig/tree/main/crates/lib/src/protocols/unreal2
package unreal2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

// Request types.
const (
	reqInfo    = 0
	reqRules   = 1
	reqPlayers = 2
)

// Limits for replies from untrusted servers.
const (
	maxDatagram = 65535
	// maxReplyBytes caps the bytes collected for one request.
	maxReplyBytes = 1 << 20
)

var (
	errSocketTimeout = errors.New("UDP - Timed out")
	errNetwork       = errors.New("UDP")
	errTooLarge      = errors.New("reply too large")
)

// Options control one query.
type Options struct {
	// SocketTimeout is how long each of the three requests collects
	// replies.
	SocketTimeout time.Duration
	// KeepColors disables node-gamedig's stripColors for names, map, game
	// type, rules and player names.
	KeepColors bool
	// Debugf, when set, receives a trace of every datagram.
	Debugf func(format string, args ...any)
}

// Player is one scoreboard entry.
type Player struct {
	ID      uint32
	Name    string
	Ping    uint32
	Score   int32
	StatsID uint32
}

// Response is the decoded server state.
type Response struct {
	Name       string
	Map        string
	Password   bool
	Version    string
	NumPlayers int
	MaxPlayers int
	// GamePort is the game port the server advertises, or 0.
	GamePort int
	// Raw holds protocol-specific fields under node-gamedig's key names:
	// ints, strings, "mutators" ([]string), "rules" (map[string]string) and
	// "scoreboard" (map[string]any of int, team scores).
	Raw     map[string]any
	Players []Player
	Bots    []Player
	// RTT is the shortest observed round trip.
	RTT time.Duration
}

type query struct {
	conn net.Conn
	opts Options
	rtt  time.Duration
	buf  []byte
}

// Query runs the info, rules and players requests over conn, which must
// be a UDP socket connected to the server's query port. All three are sent
// whatever the caller's player and rules options, as node-gamedig does.
func Query(ctx context.Context, conn net.Conn, opts Options) (*Response, error) {
	if opts.SocketTimeout <= 0 {
		opts.SocketTimeout = 2 * time.Second
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	q := &query{conn: conn, opts: opts, buf: make([]byte, maxDatagram)}
	info, err := q.send(ctx, reqInfo, true)
	if err != nil {
		return nil, err
	}
	rules, err := q.send(ctx, reqRules, true)
	if err != nil {
		return nil, err
	}
	players, err := q.send(ctx, reqPlayers, false)
	if err != nil {
		return nil, err
	}
	res := Parse(info, rules, players, !opts.KeepColors)
	res.RTT = q.rtt
	return res, nil
}

// Parse decodes the concatenated reply bodies (after each datagram's
// five-byte header) of the info, rules and players requests.
func Parse(info, rules, players []byte, stripColors bool) *Response {
	res := &Response{Raw: map[string]any{}, Players: []Player{}, Bots: []Player{}}
	raw := res.Raw

	ir := binreader.New(info)
	raw["serverid"] = int(ir.Uint32())
	raw["ip"] = readString(ir, false)
	res.GamePort = int(ir.Uint32())
	raw["queryport"] = int(ir.Uint32())
	res.Name = readString(ir, stripColors)
	res.Map = readString(ir, stripColors)
	raw["gametype"] = readString(ir, stripColors)
	res.NumPlayers = int(ir.Uint32())
	res.MaxPlayers = int(ir.Uint32())

	mutators := []string{}
	ruleMap := map[string]string{}
	rr := binreader.New(rules)
	for !rr.Done() {
		key := readString(rr, stripColors)
		value := readString(rr, stripColors)
		switch {
		case key == "Mutator" || key == "mutator":
			mutators = append(mutators, value)
		case key == "" && value == "":
		case key == "__proto__":
			// Assigning a string to __proto__ is a no-op in JavaScript.
		default:
			if old, ok := ruleMap[key]; ok {
				value = old + "," + value
			}
			ruleMap[key] = value
		}
	}
	raw["mutators"] = mutators
	raw["rules"] = ruleMap
	if v, ok := ruleMap["GamePassword"]; ok {
		// Inverted, as in node-gamedig: any value but "True" means a
		// password is set.
		res.Password = v != "True"
	}
	if v, ok := ruleMap["UTComp_Version"]; ok {
		res.Version = v
	}

	killingFloor := ruleMap["Num trader weapons"] != "" || ruleMap["Server Version"] == "1065"
	for _, m := range mutators {
		killingFloor = killingFloor || m == "KillingFloorMut"
	}
	if killingFloor {
		raw["wavecurrent"] = int(ir.Uint32())
		raw["wavetotal"] = int(ir.Uint32())
		raw["ping"] = int(ir.Uint32())
		raw["flags"] = int(ir.Uint32())
		raw["skillLevel"] = readString(ir, stripColors)
	} else {
		raw["ping"] = int(ir.Uint32())
		// Added in later engine revisions.
		if ir.Remaining() >= 8 {
			raw["flags"] = int(ir.Uint32())
			raw["skill"] = readString(ir, stripColors)
		}
	}

	scoreboard := map[string]any{}
	raw["scoreboard"] = scoreboard
	pr := binreader.New(players)
	// Each entry takes at least four bytes, so this is bounded by the
	// reply size.
	for !pr.Done() {
		p := Player{ID: pr.Uint32()}
		p.Name = readString(pr, stripColors)
		p.Ping = pr.Uint32()
		p.Score = pr.Int32()
		p.StatsID = pr.Uint32()
		switch {
		case p.ID == 0:
			// Team scores. A __proto__ entry is dropped as in JavaScript.
			if p.Name != "__proto__" {
				scoreboard[p.Name] = int(p.Score)
			}
		case p.Ping == 0:
			res.Bots = append(res.Bots, p)
		default:
			res.Players = append(res.Players, p)
		}
	}
	return res
}

// readString reads an Unreal 2 string: a length byte, then either a
// NUL-terminated Windows-1252 string (node-gamedig ignores the length
// here) or, when the length's top bit is set, that many UCS-2 characters.
// A trailing NUL is dropped and, with stripColors, colour codes and control
// characters are removed.
func readString(r *binreader.Reader, stripColors bool) string {
	length := int(r.Uint8())
	var out string
	if length >= 0x80 {
		length = (length & 0x7f) * 2
		// Some games insert an extra 0x01 that the length doesn't count.
		if b, ok := r.PeekUint8(); ok && b == 1 {
			r.Skip(1)
		}
		out = binreader.DecodeUCS2(r.Bytes(length))
	} else if length > 0 {
		out = binreader.DecodeWindows1252(r.Until(0))
	}
	out = strings.TrimSuffix(out, "\x00")
	if stripColors {
		out = stripColorCodes(out)
	}
	return out
}

// stripColorCodes matches node-gamedig's
// out.replace(/\x1b...|[\x00-\x1a]/gus, ”): an ESC and the three
// characters after it (a colour code) and the control characters 0x00-0x1A
// are removed. An ESC with fewer than three characters after it stays.
func stripColorCodes(s string) string {
	if strings.IndexFunc(s, func(c rune) bool { return c <= 0x1b }) < 0 {
		return s
	}
	runes := []rune(s)
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == 0x1b && i+3 < len(runes):
			i += 3
		case c <= 0x1a:
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}

// send sends one request and collects every reply of its type until
// SocketTimeout passes. A required request with no reply fails; an
// optional one returns no bytes.
func (q *query) send(ctx context.Context, typ byte, required bool) ([]byte, error) {
	pkt := []byte{0x79, 0, 0, 0, typ}
	q.debugf("--> %x", pkt)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := q.conn.Write(pkt); err != nil {
		return nil, fmt.Errorf("%w: %w", errNetwork, err)
	}
	start := time.Now()
	deadline := start.Add(q.opts.SocketTimeout)
	ctxDeadline, hasCtxDeadline := ctx.Deadline()
	if hasCtxDeadline && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	var body []byte
	received := false
	for {
		if err := q.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := q.conn.Read(q.buf)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			// A network error such as ICMP unreachable ends collection early,
			// like a timeout.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if hasCtxDeadline && !time.Now().Before(ctxDeadline) {
					return nil, context.DeadlineExceeded
				}
				err = fmt.Errorf("%w after %s", errSocketTimeout, q.opts.SocketTimeout)
			} else {
				err = fmt.Errorf("%w: %w", errNetwork, err)
			}
			if !received && required {
				return nil, err
			}
			return body, nil
		}
		d := time.Since(start).Truncate(time.Millisecond)
		if q.rtt == 0 || d < q.rtt {
			q.rtt = d
		}
		q.debugf("<-- %x", q.buf[:n])
		// node-gamedig reads the type at offset 4 and treats a datagram too
		// short to have one as type 0.
		dg := q.buf[:n]
		var got byte
		if len(dg) >= 5 {
			got = dg[4]
		}
		if got != typ {
			continue
		}
		received = true
		if len(dg) > 5 {
			if len(body)+len(dg)-5 > maxReplyBytes {
				return nil, errTooLarge
			}
			body = append(body, dg[5:]...)
		}
	}
}

func (q *query) debugf(format string, args ...any) {
	if q.opts.Debugf != nil {
		q.opts.Debugf(format, args...)
	}
}
