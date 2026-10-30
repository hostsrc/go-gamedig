package minecraft

import (
	"strconv"
	"time"
)

// sub is the result of one of the three sub-queries, shaped like
// node-gamedig's Results object, which minecraft.js stores whole under
// raw.vanilla, raw.gamespy and raw.bedrock.
type sub struct {
	name     string
	mapName  string
	password bool
	raw      map[string]any
	// version, maxPlayers and numPlayers hold JavaScript values: the Java
	// status JSON can put anything there, and undefined leaves the key out.
	version    any
	maxPlayers any
	numPlayers any
	players    []player
	// gamePort is GameSpy's hostport as node prints it in connect, or "".
	gamePort string
	rtt      time.Duration
}

// player is node-gamedig's Player: name is any truthy value it was given,
// otherwise "".
type player struct {
	name any
	raw  map[string]any
}

func newSub() *sub {
	return &sub{raw: map[string]any{}, version: "", maxPlayers: 0.0, numPlayers: 0.0}
}

func newPlayer(name any, raw map[string]any) player {
	if !truthy(name) {
		name = ""
	}
	return player{name: name, raw: raw}
}

// registerRTT keeps the shortest round trip, as node-gamedig's
// registerRtt does (a zero is replaced by the next value).
func registerRTT(cur *time.Duration, d time.Duration) {
	d = d.Truncate(time.Millisecond)
	if *cur == 0 || d < *cur {
		*cur = d
	}
}

// json is what JSON.stringify prints for the sub-result after
// node-gamedig's runOnce has filled in queryPort, connect and ping.
func (s *sub) json(host string, port int) map[string]any {
	connectPort := strconv.Itoa(port)
	if s.gamePort != "" {
		connectPort = s.gamePort
	}
	players := make([]any, len(s.players))
	for i, p := range s.players {
		players[i] = map[string]any{"name": p.name, "raw": p.raw}
	}
	m := map[string]any{
		"name":      jsTrim(s.name),
		"map":       s.mapName,
		"password":  s.password,
		"raw":       s.raw,
		"version":   jsString(s.version),
		"players":   players,
		"bots":      []any{},
		"queryPort": port,
		"connect":   host + ":" + connectPort,
		"ping":      int(s.rtt.Milliseconds()),
	}
	if !isUndefined(s.maxPlayers) {
		m["maxplayers"] = s.maxPlayers
	}
	if !isUndefined(s.numPlayers) {
		m["numplayers"] = s.numPlayers
	}
	return m
}
