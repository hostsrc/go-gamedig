package gamedig

import (
	"context"
	"strconv"

	"github.com/hostsrc/go-gamedig/internal/valve"
)

func init() {
	registerProtocol("valve", 27015, queryValve(false))
	registerProtocol("goldsrc", 27015, queryValve(true))
}

func queryValve(goldsrcInfo bool) protocolFunc {
	return func(ctx context.Context, t target) (*Result, error) {
		conn, err := t.Dial(ctx, "udp")
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		opts := t.Opts
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
