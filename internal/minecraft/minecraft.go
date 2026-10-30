// Package minecraft queries Minecraft servers the way node-gamedig's
// "minecraft" protocol does: Java Edition Server List Ping over TCP,
// GameSpy 3 query and Bedrock Edition RakNet ping over UDP, all at once on
// the same port, merged into one result. The answers of every sub-query
// that succeeded are kept whole under raw.vanilla, raw.gamespy and
// raw.bedrock.
//
// Behaviour follows node-gamedig 5.3.3's protocols/minecraft.js,
// minecraftvanilla.js, minecraftbedrock.js and gamespy3.js, including how
// names are flattened and which answer wins each field.
//
// References:
//   - https://minecraft.wiki/w/Java_Edition_protocol/Server_List_Ping
//   - https://minecraft.wiki/w/Query
//   - https://github.com/gamedig/node-gamedig/blob/master/protocols/minecraft.js
//   - https://github.com/gamedig/rust-gamedig/tree/main/crates/lib/src/games/minecraft
package minecraft

import (
	"context"
	"errors"
	"math"
	"net"
	"strconv"
	"sync"
	"time"
)

// Options control one query.
type Options struct {
	// Host is the server name as the user gave it. It is resolved through
	// the _minecraft._tcp SRV record and sent in the Java handshake.
	Host string
	// Port is the server port, used for TCP and UDP alike. A SRV record's
	// port is ignored, as node-gamedig ignores it whenever a port is known.
	Port int
	// SocketTimeout bounds each request/response exchange. Default 2s.
	SocketTimeout time.Duration
	// Debugf, when set, receives a trace of the query.
	Debugf func(format string, args ...any)
	// Resolver looks up SRV records and addresses. Default
	// net.DefaultResolver.
	Resolver Resolver
	// Dial opens connections. Default net.Dialer.DialContext.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Player is one player in the merged result.
type Player struct {
	Name string
	Raw  map[string]any
}

// Response is the merged server state. Raw holds the sub-query results
// as JSON values, with numbers as float64.
type Response struct {
	Name       string
	Map        string
	Version    string
	NumPlayers int
	MaxPlayers int
	Players    []Player
	Raw        map[string]any
	// RTT is the shortest round trip of any sub-query.
	RTT time.Duration
}

type query struct {
	opts    Options
	address string
	udp     *udpMux
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
}

func (q *query) debugf(format string, args ...any) {
	if q.opts.Debugf != nil {
		q.opts.Debugf(format, args...)
	}
}

// Query queries one server.
func Query(ctx context.Context, opts Options) (*Response, error) {
	if opts.SocketTimeout <= 0 {
		opts.SocketTimeout = 2 * time.Second
	}
	if opts.Resolver == nil {
		opts.Resolver = net.DefaultResolver
	}
	q := &query{opts: opts, dial: opts.Dial}
	if q.dial == nil {
		var d net.Dialer
		q.dial = d.DialContext
	}
	address, err := resolve(ctx, opts.Resolver, opts.Host, 0, q.debugf)
	if err != nil {
		return nil, err
	}
	q.address = address

	var vanilla, gamespy, bedrock *sub
	var verr, gerr, berr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		vanilla, verr = q.queryJava(ctx)
	}()
	udpConn, uerr := q.dial(ctx, "udp", net.JoinHostPort(address, strconv.Itoa(opts.Port)))
	if uerr == nil {
		q.udp = newUDPMux(udpConn, q.debugf)
		wg.Add(2)
		go func() {
			defer wg.Done()
			gamespy, gerr = q.queryGamespy3(ctx)
		}()
		go func() {
			defer wg.Done()
			bedrock, berr = q.queryBedrock(ctx)
		}()
	} else {
		gerr, berr = uerr, uerr
	}
	wg.Wait()
	if q.udp != nil {
		q.udp.close()
	}
	q.debugf("vanilla: %v, gamespy: %v, bedrock: %v", verr, gerr, berr)

	// node-gamedig's attempt timer starts before the sub-queries' own, so
	// a sub-query still running at the deadline fails the whole attempt
	// even if another one answered.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if vanilla == nil && gamespy == nil && bedrock == nil {
		return nil, errors.New("No protocols succeeded")
	}
	return merge(opts, vanilla, gamespy, bedrock), nil
}

// merge combines the sub-results as minecraft.js does, from least to most
// trusted: Java status, then GameSpy 3, then Bedrock.
func merge(opts Options, vanilla, gamespy, bedrock *sub) *Response {
	var (
		name                   = ""
		mapName                = ""
		numPlayers, maxPlayers any
		version                any = ""
		players                []player
		rtt                    time.Duration
	)
	numPlayers, maxPlayers = 0.0, 0.0
	raw := map[string]any{}

	if vanilla != nil {
		if n, ok := javaName(prop(vanilla.raw, "description")); ok {
			name = n
		}
		if truthy(vanilla.numPlayers) {
			numPlayers = vanilla.numPlayers
		}
		if truthy(vanilla.maxPlayers) {
			maxPlayers = vanilla.maxPlayers
		}
		if len(vanilla.players) > 0 {
			players = vanilla.players
		}
		if vanilla.rtt > 0 {
			registerRTT(&rtt, vanilla.rtt)
		}
		if v := prop(vanilla.raw, "version"); truthy(v) {
			version = prop(v, "name")
		}
	}
	if gamespy != nil {
		if n := jsTrim(gamespy.name); n != "" {
			name = n
		}
		if truthy(gamespy.numPlayers) {
			numPlayers = gamespy.numPlayers
		}
		if truthy(gamespy.maxPlayers) {
			maxPlayers = gamespy.maxPlayers
		}
		if len(gamespy.players) > 0 {
			players = gamespy.players
		}
		if gamespy.rtt > 0 {
			registerRTT(&rtt, gamespy.rtt)
		}
	}
	if bedrock != nil {
		if n := jsTrim(bedrock.name); n != "" {
			name = n
		}
		if truthy(bedrock.numPlayers) {
			numPlayers = bedrock.numPlayers
		}
		if truthy(bedrock.maxPlayers) {
			maxPlayers = bedrock.maxPlayers
		}
		if bedrock.mapName != "" {
			mapName = bedrock.mapName
		}
		if bedrock.rtt > 0 {
			registerRTT(&rtt, bedrock.rtt)
		}
		if v := bedrock.raw["mcVersion"]; truthy(v) {
			version = v
		}
	}

	// Results are serialized only now: flattening the description above
	// reverses its "extra" arrays in place, and node-gamedig prints them
	// that way.
	for key, s := range map[string]*sub{"vanilla": vanilla, "gamespy": gamespy, "bedrock": bedrock} {
		if s != nil {
			raw[key] = s.json(opts.Host, opts.Port)
		}
	}

	res := &Response{
		Name:       cleanName(name),
		Map:        mapName,
		Version:    jsString(version),
		NumPlayers: toInt(numPlayers),
		MaxPlayers: toInt(maxPlayers),
		Raw:        raw,
		RTT:        rtt,
	}
	for _, p := range players {
		// Names are strings unless the status JSON says otherwise.
		n, ok := p.name.(string)
		if !ok {
			n = jsString(p.name)
		}
		res.Players = append(res.Players, Player{Name: n, Raw: p.raw})
	}
	return res
}

// toInt converts a player count to an int. node-gamedig passes through
// whatever the status JSON holds; Result can only hold integers.
func toInt(v any) int {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int(max(min(f, math.MaxInt32), math.MinInt32))
}

// javaName flattens a status description into the server name like
// minecraft.js: a string is used as is; for a chat component the "text" of
// every component is joined depth first through "extra". As in
// JavaScript, the walk reverses each "extra" array in place, and a null
// component aborts it (ok is false and the name is left unchanged).
func javaName(desc any) (name string, ok bool) {
	switch desc.(type) {
	case string:
		return desc.(string), true
	case map[string]any, []any, nil:
	default:
		return "", true // numbers, booleans, undefined
	}
	stack := []any{desc}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == nil {
			return "", false // TypeError reading null.text
		}
		if text := prop(current, "text"); truthy(text) {
			name += jsString(text)
		}
		if extra, ok := prop(current, "extra").([]any); ok {
			for i, j := 0, len(extra)-1; i < j; i, j = i+1, j-1 {
				extra[i], extra[j] = extra[j], extra[i]
			}
			stack = append(stack, extra...)
		}
	}
	return name, true
}
