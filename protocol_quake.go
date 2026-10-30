package gamedig

import (
	"context"
	"math"
	"strconv"

	"github.com/hostsrc/go-gamedig/internal/quake"
)

func init() {
	// Unlike valve.js, node-gamedig's Quake classes have no default port:
	// "protocol-quake3" without one fails, and catalog games supply theirs.
	registerProtocol("quake1", 0, queryQuake(quake.Quake1))
	registerProtocol("quake2", 0, queryQuake(quake.Quake2))
	registerProtocol("quake3", 0, queryQuake(quake.Quake3))
	registerProtocol("hexen2", 0, queryQuake(quake.Hexen2))
	registerProtocol("warsow", 0, queryQuake(quake.Warsow))
	registerProtocol("xonotic", 0, queryQuake(quake.Xonotic))
}

func queryQuake(variant quake.Variant) protocolFunc {
	return func(ctx context.Context, t target) (*Result, error) {
		conn, err := t.Dial(ctx, "udp")
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		opts := t.Opts
		q, err := quake.Query(ctx, conn, quake.Options{
			Variant:       variant,
			StripColors:   !opts.KeepColors,
			SocketTimeout: opts.SocketTimeout,
			Debugf:        opts.Debugf,
		})
		if err != nil {
			return nil, err
		}
		return quakeResult(q, variant, t), nil
	}
}

// quakeResult converts a parsed reply to a Result.
func quakeResult(q *quake.Response, variant quake.Variant, t target) *Result {
	res := &Result{
		Name:       q.Name,
		Map:        q.Map,
		Raw:        quakeRaw(q.Raw),
		Version:    q.Version,
		NumPlayers: q.NumPlayers,
		Players:    quakePlayers(q.Players),
		Bots:       quakePlayers(q.Bots),
		Ping:       int(q.RTT.Milliseconds()),
	}
	// node-gamedig copies these cvars into the result unconverted, so JSON
	// gets the server's strings while the Go fields get their meaning.
	node := map[string]any{}
	if q.HasPassword {
		res.Password = flagSet(q.Password)
		node["password"] = q.Password
	}
	if q.HasMaxPlayers {
		res.MaxPlayers = parseCount(q.MaxPlayers)
		node["maxplayers"] = q.MaxPlayers
	}
	if q.HasNumPlayersText {
		res.NumPlayers = parseCount(q.NumPlayersText)
		node["numplayers"] = q.NumPlayersText
	}
	if len(node) > 0 {
		res.nodeJSON = node
	}
	if variant == quake.Hexen2 {
		// hexen2.js reports the game port as the query port minus 50.
		if gp := t.Port - 50; gp != 0 {
			res.Connect = t.Host + ":" + strconv.Itoa(gp)
		}
	}
	return res
}

func quakeRaw(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func quakePlayers(in []quake.Player) []Player {
	out := make([]Player, 0, len(in))
	for _, p := range in {
		raw := make(map[string]any, len(p.Raw))
		for k, v := range p.Raw {
			if f, ok := v.(float64); ok {
				raw[k] = Number(f)
			} else {
				raw[k] = v
			}
		}
		out = append(out, Player{Name: p.Name, Raw: raw})
	}
	return out
}

// parseCount reads a player count cvar like JavaScript's parseInt, giving
// 0 when it is not a number.
func parseCount(s string) int {
	n, ok := parseLeadingInt(s)
	if !ok {
		return 0
	}
	return n
}

// flagSet reports whether a flag cvar such as g_needpass is set: a
// non-zero number, or "true"/"yes".
func flagSet(s string) bool {
	if n, ok := parseLeadingInt(s); ok {
		return n != 0
	}
	return s == "true" || s == "yes"
}

// parseLeadingInt parses an optionally signed decimal prefix after leading
// spaces, clamped to the int32 range.
func parseLeadingInt(s string) (int, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
	}
	start, n := i, 0
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		if n < math.MaxInt32 {
			n = n*10 + int(s[i]-'0')
		}
	}
	if i == start {
		return 0, false
	}
	n = min(n, math.MaxInt32)
	if neg {
		n = -n
	}
	return n, true
}
