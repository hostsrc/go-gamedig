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
		SocketTimeout: opts.SocketTimeout,
		Debugf:        opts.Debugf,
	})
	if err != nil {
		return nil, err
	}
	res := &Result{
		Name:       m.Name,
		Map:        m.Map,
		Raw:        jsonNumbers(m.Raw).(map[string]any),
		Version:    m.Version,
		MaxPlayers: m.MaxPlayers,
		NumPlayers: m.NumPlayers,
		Players:    make([]Player, 0, len(m.Players)),
		Bots:       []Player{},
		Ping:       int(m.RTT.Milliseconds()),
	}
	for _, p := range m.Players {
		res.Players = append(res.Players, Player{Name: p.Name, Raw: jsonNumbers(p.Raw).(map[string]any)})
	}
	return res, nil
}

// jsonNumbers replaces float64 with Number throughout decoded JSON, so
// values such as NaN or 1e21 print as JavaScript would.
func jsonNumbers(v any) any {
	switch v := v.(type) {
	case float64:
		return Number(v)
	case []any:
		for i, e := range v {
			v[i] = jsonNumbers(e)
		}
	case map[string]any:
		for k, e := range v {
			v[k] = jsonNumbers(e)
		}
	}
	return v
}
