// Package gamedig queries game servers for their name, map, players and
// other state. It is a Go port of GameDig (node-gamedig, with protocol
// details cross-checked against rust-gamedig) and returns the same result
// shape and JSON values as node-gamedig 5, so it can replace it as a library or,
// through cmd/gamedig, as a command.
//
// Only the Valve protocol (A2S), which covers 138 of the catalog's games, is
// implemented so far; Lookup and Games report the rest as unsupported.
package gamedig

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hostsrc/go-gamedig/internal/valve"
)

// Options select the server and control the query. The zero value of every
// field except Type and Host is a usable default.
type Options struct {
	// Type is a game ID from the catalog, such as "counterstrike16", or
	// "protocol-<name>" to use a protocol directly.
	Type string
	// Host is the server's hostname or IP address.
	Host string
	// Port is the game port. Zero uses the game's default.
	Port int
	// GivenPortOnly queries Port as is, skipping the game's query port
	// offsets and fixed query port.
	GivenPortOnly bool
	// CheckOldIDs also accepts pre-GameDig 5 game IDs.
	CheckOldIDs bool
	// MaxRetries is how many times each candidate port is tried. Default 1,
	// at most 100.
	MaxRetries int
	// NoBreadthOrder tries every retry of a port before the next port.
	NoBreadthOrder bool
	// SocketTimeout bounds each request/response exchange. Default 2s.
	SocketTimeout time.Duration
	// AttemptTimeout bounds one attempt against one port. Default 10s.
	AttemptTimeout time.Duration
	// SkipPlayers skips the player list request.
	SkipPlayers bool
	// RequestRules also requests server rules (cvars).
	RequestRules bool
	// Debugf, when set, receives a trace of the query.
	Debugf func(format string, args ...any)
}

// Errors returned before any query is sent. Their messages match
// node-gamedig's.
var (
	ErrNoGame      = errors.New("No game specified")
	ErrUnknownGame = errors.New("Invalid game")
	// ErrUnsupported means the game exists but its protocol is not
	// implemented yet.
	ErrUnsupported = errors.New("protocol not implemented yet")
	ErrNoHost      = errors.New("no host given")
)

// Number is a numeric value that marshals like JavaScript's JSON.stringify:
// NaN and ±Inf become null and -0 becomes 0. Untrusted servers can send any
// float, and encoding/json refuses NaN and Inf.
type Number float64

// MarshalJSON implements json.Marshaler.
func (n Number) MarshalJSON() ([]byte, error) {
	f := float64(n)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	if f == 0 {
		return []byte("0"), nil
	}
	// Same thresholds as JavaScript (and encoding/json): exponent form only
	// below 1e-6 or from 1e21, written as 1e-7 rather than 1e-07.
	format := byte('f')
	if abs := math.Abs(f); abs < 1e-6 || abs >= 1e21 {
		format = 'e'
	}
	b := strconv.AppendFloat(nil, f, format, -1, 64)
	if format == 'e' {
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b = append(b[:n-2], b[n-1])
		}
	}
	return b, nil
}

// Player is one player or bot. For the Valve protocol Raw holds "score"
// (int) and "time" (Number, seconds connected).
type Player struct {
	Name string         `json:"name"`
	Raw  map[string]any `json:"raw"`
}

// Result is a server's state in node-gamedig's result shape. Raw holds
// protocol-specific fields under node-gamedig's key names, using only JSON
// value types: string, bool, int, Number, []string, []any,
// map[string]string and map[string]any.
type Result struct {
	Name       string         `json:"name"`
	Map        string         `json:"map"`
	Password   bool           `json:"password"`
	Raw        map[string]any `json:"raw"`
	Version    string         `json:"version"`
	MaxPlayers int            `json:"maxplayers"`
	NumPlayers int            `json:"numplayers"`
	Players    []Player       `json:"players"`
	Bots       []Player       `json:"bots"`
	QueryPort  int            `json:"queryPort"`
	Connect    string         `json:"connect"`
	// Ping is the shortest round trip in milliseconds.
	Ping int `json:"ping"`
}

// AttemptsError reports that every attempt failed. Errors holds the
// failure of each attempt in order.
type AttemptsError struct {
	Errors []error
}

// Summary is node-gamedig's message for the failure, which cmd/gamedig
// prints: "Failed all N attempts".
func (e *AttemptsError) Summary() string {
	return fmt.Sprintf("Failed all %d attempts", len(e.Errors))
}

func (e *AttemptsError) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, err := range e.Errors {
		msgs[i] = err.Error()
	}
	return e.Summary() + ": " + strings.Join(msgs, "; ")
}

// Unwrap returns the failure of each attempt.
func (e *AttemptsError) Unwrap() []error { return e.Errors }

type protocolFunc func(ctx context.Context, conn net.Conn, opts Options) (*Result, error)

var protocols = map[string]protocolFunc{
	"valve":   queryValve(false),
	"goldsrc": queryValve(true),
}

// defaultPorts are used when neither the caller nor the catalog gives a
// port, as node-gamedig's protocol classes do.
var defaultPorts = map[string]int{
	"valve":   27015,
	"goldsrc": 27015,
}

const maxRetries = 100

// Query queries one server. When every attempt fails the error is an
// *AttemptsError.
func Query(ctx context.Context, opts Options) (*Result, error) {
	game, err := Lookup(opts.Type, opts.CheckOldIDs)
	if err != nil {
		return nil, err
	}
	run, ok := protocols[game.Protocol]
	if !ok {
		return nil, fmt.Errorf("%w: %q (game %q)", ErrUnsupported, game.Protocol, opts.Type)
	}
	if opts.Host == "" {
		return nil, ErrNoHost
	}
	if opts.SocketTimeout <= 0 {
		opts.SocketTimeout = 2 * time.Second
	}
	if opts.AttemptTimeout <= 0 {
		opts.AttemptTimeout = 10 * time.Second
	}
	retries := min(max(opts.MaxRetries, 1), maxRetries)

	ports := attemptPorts(game, opts)
	var order []int
	if opts.NoBreadthOrder {
		for _, p := range ports {
			for range retries {
				order = append(order, p)
			}
		}
	} else {
		for range retries {
			order = append(order, ports...)
		}
	}

	var errs []error
	for i, port := range order {
		if port == 0 {
			port = defaultPorts[game.Protocol]
		}
		res, err := attempt(ctx, run, opts, port)
		if err == nil {
			return res, nil
		}
		errs = append(errs, fmt.Errorf("attempt #%d port=%d: %w", i+1, port, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, &AttemptsError{Errors: errs}
}

// attemptPorts lists candidate query ports in node-gamedig's order. The
// given port is always tried last, even if an offset of 0 already did.
func attemptPorts(game Game, opts Options) []int {
	offsets := game.QueryPortOffsets
	if len(offsets) == 0 {
		offsets = []int{0}
	}
	var ports []int
	switch {
	case opts.Port != 0:
		if !opts.GivenPortOnly {
			for _, off := range offsets {
				ports = append(ports, opts.Port+off)
			}
			if opts.Port == game.Port && game.QueryPort != 0 {
				ports = append(ports, game.QueryPort)
			}
		}
		ports = append(ports, opts.Port)
	case game.QueryPort != 0:
		ports = append(ports, game.QueryPort)
	case game.Port != 0:
		for _, off := range offsets {
			ports = append(ports, game.Port+off)
		}
	default:
		ports = append(ports, 0)
	}
	return ports
}

func attempt(ctx context.Context, run protocolFunc, opts Options, port int) (*Result, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("Invalid tcp/ip port: %d", port)
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, opts.AttemptTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(opts.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	res, err := run(ctx, conn, opts)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil {
			return nil, fmt.Errorf("Attempt - Timed out after %s", opts.AttemptTimeout)
		}
		return nil, err
	}
	res.QueryPort = port
	if res.Connect == "" {
		res.Connect = opts.Host + ":" + strconv.Itoa(port)
	}
	// Many servers pad their name to sort first in server browsers.
	res.Name = strings.TrimFunc(res.Name, isJSSpace)
	return res, nil
}

func queryValve(goldsrcInfo bool) protocolFunc {
	return func(ctx context.Context, conn net.Conn, opts Options) (*Result, error) {
		v, err := valve.Query(ctx, conn, valve.Options{
			GoldSrcInfo:    goldsrcInfo,
			RequestPlayers: !opts.SkipPlayers,
			RequestRules:   opts.RequestRules,
			SocketTimeout:  opts.SocketTimeout,
			Debugf:         opts.Debugf,
		})
		if err != nil {
			return nil, err
		}
		res := &Result{
			Name:       v.Name,
			Map:        v.Map,
			Password:   v.Password,
			Raw:        jsonRaw(v.Raw),
			Version:    v.Version,
			MaxPlayers: v.MaxPlayers,
			NumPlayers: v.NumPlayers,
			Players:    valvePlayers(v.Players),
			Bots:       valvePlayers(v.Bots),
			Ping:       int(v.RTT.Milliseconds()),
		}
		// Like node-gamedig, connect is host:port without IPv6 brackets.
		if v.GamePort != 0 {
			res.Connect = opts.Host + ":" + strconv.Itoa(v.GamePort)
		}
		return res, nil
	}
}

func valvePlayers(in []valve.RawPlayer) []Player {
	out := make([]Player, 0, len(in))
	for _, p := range in {
		out = append(out, Player{Name: p.Name, Raw: map[string]any{"score": int(p.Score), "time": Number(p.Time)}})
	}
	return out
}

// jsonRaw replaces the protocol package's types in raw with JSON value
// types, so callers can type-assert Raw without importing internals.
func jsonRaw(raw map[string]any) map[string]any {
	for k, v := range raw {
		switch v := v.(type) {
		case []valve.RawPlayer:
			list := make([]any, len(v))
			for i, p := range v {
				list[i] = map[string]any{"name": p.Name, "score": int(p.Score), "time": Number(p.Time)}
			}
			raw[k] = list
		case valve.Buffer:
			data := make([]any, len(v))
			for i, b := range v {
				data[i] = int(b)
			}
			raw[k] = map[string]any{"type": "Buffer", "data": data}
		}
	}
	return raw
}

// isJSSpace matches the characters JavaScript's String.prototype.trim
// removes.
func isJSSpace(r rune) bool {
	return (unicode.IsSpace(r) && r != '\u0085') || r == '\uFEFF'
}
