package gamedig

import (
	"context"
	"strconv"

	"github.com/hostsrc/go-gamedig/internal/unreal2"
)

func init() {
	registerProtocol("unreal2", 0, queryUnreal2)
}

func queryUnreal2(ctx context.Context, t target) (*Result, error) {
	conn, err := t.Dial(ctx, "udp")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	opts := t.Opts
	u, err := unreal2.Query(ctx, conn, unreal2.Options{
		SocketTimeout: opts.SocketTimeout,
		KeepColors:    opts.KeepColors,
		Debugf:        opts.Debugf,
	})
	if err != nil {
		return nil, err
	}
	res := &Result{
		Name:       u.Name,
		Map:        u.Map,
		Password:   u.Password,
		Raw:        u.Raw,
		Version:    u.Version,
		MaxPlayers: u.MaxPlayers,
		NumPlayers: u.NumPlayers,
		Players:    unreal2Players(u.Players),
		Bots:       unreal2Players(u.Bots),
		Ping:       int(u.RTT.Milliseconds()),
	}
	if u.GamePort != 0 {
		res.Connect = opts.Host + ":" + strconv.Itoa(u.GamePort)
	}
	return res, nil
}

func unreal2Players(in []unreal2.Player) []Player {
	out := make([]Player, 0, len(in))
	for _, p := range in {
		out = append(out, Player{Name: p.Name, Raw: map[string]any{
			"id": int(p.ID), "ping": int(p.Ping), "score": int(p.Score), "statsId": int(p.StatsID),
		}})
	}
	return out
}
