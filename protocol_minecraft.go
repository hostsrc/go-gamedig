package gamedig

import (
	"context"

	"github.com/hostsrc/go-gamedig/internal/minecraft"
)

func init() {
	// Like node-gamedig, the protocol has no default port of its own: the
	// catalog gives 25565 (Java) and 19132 (Bedrock).
	registerProtocol("minecraft", 0, queryMinecraft)
}

func queryMinecraft(ctx context.Context, t target) (*Result, error) {
	opts := t.Opts
	m, err := minecraft.Query(ctx, minecraft.Options{
		Host:          t.Host,
		Port:          t.Port,
		StripColors:   !opts.KeepColors,
		SocketTimeout: opts.SocketTimeout,
		Debugf:        opts.Debugf,
	})
	if err != nil {
		return nil, err
	}
	res := &Result{
		Name:       m.Name,
		Map:        m.Map,
		Raw:        jsValue(m.Raw).(map[string]any),
		Version:    m.Version,
		MaxPlayers: m.MaxPlayers,
		NumPlayers: m.NumPlayers,
		Players:    make([]Player, 0, len(m.Players)),
		Bots:       []Player{},
		Ping:       int(m.RTT.Milliseconds()),
	}
	// The status JSON can make the counts fractions, strings or objects,
	// and player names something other than strings. The Go fields keep
	// the nearest count and a string name; JSON gets node-gamedig's value.
	override := func(key string, v any) {
		if res.nodeJSON == nil {
			res.nodeJSON = map[string]any{}
		}
		res.nodeJSON[key] = jsValue(v)
	}
	if f, ok := m.MaxPlayersValue.(float64); !ok || float64(m.MaxPlayers) != f {
		override("maxplayers", m.MaxPlayersValue)
	}
	if f, ok := m.NumPlayersValue.(float64); !ok || float64(m.NumPlayers) != f {
		override("numplayers", m.NumPlayersValue)
	}
	nodePlayers := make([]any, 0, len(m.Players))
	oddNames := false
	for _, p := range m.Players {
		raw := jsValue(p.Raw).(map[string]any)
		res.Players = append(res.Players, Player{Name: p.Name, Raw: raw})
		if _, ok := p.NameValue.(string); !ok {
			oddNames = true
		}
		nodePlayers = append(nodePlayers, map[string]any{"name": jsValue(p.NameValue), "raw": raw})
	}
	if oddNames {
		override("players", nodePlayers)
	}
	return res, nil
}
