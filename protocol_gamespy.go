package gamedig

import (
	"context"
	"math"
	"net"

	"github.com/hostsrc/go-gamedig/internal/gamespy"
)

func init() {
	// node-gamedig's GameSpy classes have no default port; every catalog
	// game gives one.
	registerProtocol("gamespy1", 0, queryGamespy(gamespy.Query1))
	registerProtocol("gamespy2", 0, queryGamespy(gamespy.Query2))
	registerProtocol("gamespy3", 0, queryGamespy(gamespy.Query3))
}

func queryGamespy(query func(context.Context, net.Conn, gamespy.Options) (*gamespy.Response, error)) protocolFunc {
	return func(ctx context.Context, t target) (*Result, error) {
		conn, err := t.Dial(ctx, "udp")
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		g, err := query(ctx, conn, gamespy.Options{
			StripColors:   !t.Opts.KeepColors,
			SocketTimeout: t.Opts.SocketTimeout,
			Debugf:        t.Opts.Debugf,
		})
		if err != nil {
			return nil, err
		}
		res := &Result{
			Name:       g.Name,
			Map:        g.Map,
			Password:   g.Password,
			Raw:        jsValue(g.Raw).(map[string]any),
			Version:    g.Version,
			MaxPlayers: jsCount(g.MaxPlayers),
			NumPlayers: jsCount(g.NumPlayers),
			Players:    make([]Player, 0, len(g.Players)),
			Bots:       []Player{},
			Ping:       int(g.RTT.Milliseconds()),
		}
		// node-gamedig prints the counts as JavaScript numbers, which can be
		// NaN (null) or fractional when a server sends odd values; the int
		// fields keep the nearest count and JSON gets node's value.
		for key, f := range map[string]float64{"maxplayers": g.MaxPlayers, "numplayers": g.NumPlayers} {
			if float64(jsCount(f)) != f {
				if res.nodeJSON == nil {
					res.nodeJSON = map[string]any{}
				}
				res.nodeJSON[key] = Number(f)
			}
		}
		for _, p := range g.Players {
			// node-gamedig's Player takes "name" out of the object when it
			// is truthy and keeps everything else as raw.
			name, _ := p["name"].(string)
			delete(p, "name")
			res.Players = append(res.Players, Player{Name: name, Raw: jsValue(p).(map[string]any)})
		}
		// Like node-gamedig, connect is host:port without IPv6 brackets.
		if g.GamePort != "" {
			res.Connect = t.Host + ":" + g.GamePort
		}
		return res, nil
	}
}

// jsCount converts a JavaScript number to a player count: NaN and values
// beyond ±2^53 become 0 and fractions are truncated.
func jsCount(f float64) int {
	if math.IsNaN(f) || math.Abs(f) > 1<<53 {
		return 0
	}
	return int(f)
}

// jsValue converts the gamespy package's float64 numbers to Number,
// recursively, so Raw holds only the documented JSON value types.
func jsValue(v any) any {
	switch v := v.(type) {
	case float64:
		return Number(v)
	case map[string]any:
		for k, e := range v {
			v[k] = jsValue(e)
		}
		return v
	case []any:
		for i, e := range v {
			v[i] = jsValue(e)
		}
		return v
	}
	return v
}
