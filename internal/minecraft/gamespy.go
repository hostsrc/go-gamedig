package minecraft

import (
	"context"

	"github.com/hostsrc/go-gamedig/internal/gamespy"
)

// queryGamespy3 runs the GameSpy 3 query (what a Java server with
// enable-query=true answers) with UTF-8 strings, as minecraft.js
// configures protocols/gamespy3.js, over the socket shared with Bedrock.
func (q *query) queryGamespy3(ctx context.Context) (*sub, error) {
	c := q.udp.newConn()
	defer c.Close()
	g, err := gamespy.Query3(ctx, c, gamespy.Options{
		StripColors:   q.opts.StripColors,
		UTF8:          true,
		SocketTimeout: q.opts.SocketTimeout,
		Debugf:        q.opts.Debugf,
	})
	if err != nil {
		return nil, err
	}
	s := newSub()
	s.name = g.Name
	s.mapName = g.Map
	s.password = g.Password
	s.raw = g.Raw
	s.version = g.Version
	s.maxPlayers = g.MaxPlayers
	s.numPlayers = g.NumPlayers
	s.gamePort = g.GamePort
	s.rtt = g.RTT
	for _, p := range g.Players {
		name, ok := p["name"]
		if !ok {
			name = undefined
		}
		delete(p, "name")
		s.players = append(s.players, newPlayer(name, p))
	}
	return s, nil
}
