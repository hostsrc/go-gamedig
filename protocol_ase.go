package gamedig

import (
	"context"
	"math"

	"github.com/hostsrc/go-gamedig/internal/ase"
)

func init() {
	registerProtocol("ase", 0, queryASE)
}

func queryASE(ctx context.Context, t target) (*Result, error) {
	conn, err := t.Dial(ctx, "udp")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	opts := t.Opts
	a, err := ase.Query(ctx, conn, ase.Options{SocketTimeout: opts.SocketTimeout, Debugf: opts.Debugf})
	if err != nil {
		return nil, err
	}
	raw := make(map[string]any, len(a.Raw))
	for k, v := range a.Raw {
		raw[k] = v
	}
	res := &Result{
		Name:       a.Name,
		Map:        a.Map,
		Password:   a.Password,
		Raw:        raw,
		Version:    a.Version,
		MaxPlayers: jsInt(a.MaxPlayers),
		NumPlayers: jsInt(a.NumPlayers),
		Players:    asePlayers(a.Players),
		Bots:       []Player{},
		Ping:       int(a.RTT.Milliseconds()),
	}
	// numplayers and maxplayers are parseInt results. When the int field
	// can't hold one exactly (NaN for a missing or non-numeric field, or
	// beyond ±2^53), JSON gets node-gamedig's number instead.
	node := map[string]any{}
	for key, f := range map[string]float64{"numplayers": a.NumPlayers, "maxplayers": a.MaxPlayers} {
		if math.IsNaN(f) || math.Abs(f) > 1<<53 {
			node[key] = Number(f)
		}
	}
	if len(node) > 0 {
		res.nodeJSON = node
	}
	// node-gamedig uses the advertised game port when it is truthy (not 0
	// or NaN), formatted as JavaScript's String(number).
	if p := a.GamePort; p != 0 && !math.IsNaN(p) {
		s, _ := Number(p).MarshalJSON()
		if math.IsInf(p, 0) {
			s = []byte("Infinity")
			if p < 0 {
				s = []byte("-Infinity")
			}
		}
		res.Connect = opts.Host + ":" + string(s)
	}
	return res, nil
}

func asePlayers(in []ase.Player) []Player {
	out := make([]Player, 0, len(in))
	for _, p := range in {
		raw := map[string]any{}
		if p.Flags&ase.FlagTeam != 0 {
			raw["team"] = p.Team
		}
		if p.Flags&ase.FlagSkin != 0 {
			raw["skin"] = p.Skin
		}
		if p.Flags&ase.FlagScore != 0 {
			raw["score"] = Number(p.Score)
		}
		if p.Flags&ase.FlagPing != 0 {
			raw["ping"] = Number(p.Ping)
		}
		if p.Flags&ase.FlagTime != 0 {
			raw["time"] = Number(p.Time)
		}
		out = append(out, Player{Name: p.Name, Raw: raw})
	}
	return out
}

// jsInt converts a parseInt result for an int field: NaN becomes 0 and
// out-of-range values are clamped.
func jsInt(f float64) int {
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt:
		return math.MaxInt
	case f <= math.MinInt:
		return math.MinInt
	}
	return int(f)
}
