package gamedig

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// games.json is generated from node-gamedig's lib/games.js by
// tools/gen-games/gen-games.mjs.
//
//go:embed games.json
var gamesJSON []byte

// Game describes one entry of the game catalog.
type Game struct {
	ID       string `json:"-"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	// Port is the default game port.
	Port int `json:"port,omitempty"`
	// QueryPort, when set, is a fixed query port tried in addition to the
	// game port.
	QueryPort int `json:"port_query,omitempty"`
	// QueryPortOffsets are added to the game port to find the query port.
	QueryPortOffsets []int `json:"port_query_offset,omitempty"`
	// OldID is the ID this game had before GameDig 5.
	OldID string `json:"old_id,omitempty"`
}

// Supported reports whether this module implements the game's protocol.
func (g Game) Supported() bool {
	_, ok := protocols[g.Protocol]
	return ok
}

var catalog = func() map[string]Game {
	var m map[string]Game
	if err := json.Unmarshal(gamesJSON, &m); err != nil {
		panic("gamedig: invalid embedded games.json: " + err.Error())
	}
	for id, g := range m {
		g.ID = id
		m[id] = g
	}
	return m
}()

// Games returns the whole catalog sorted by ID, including games whose
// protocol is not implemented yet.
func Games() []Game {
	out := make([]Game, 0, len(catalog))
	for _, g := range catalog {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup resolves a game type the way node-gamedig does. "protocol-<name>"
// selects a protocol directly. With checkOldIDs, pre-GameDig 5 IDs such as
// "cs16" are accepted too.
func Lookup(gameType string, checkOldIDs bool) (Game, error) {
	if gameType == "" {
		return Game{}, ErrNoGame
	}
	if p, ok := strings.CutPrefix(gameType, "protocol-"); ok {
		return Game{ID: gameType, Protocol: p}, nil
	}
	g, ok := catalog[gameType]
	if checkOldIDs {
		for _, c := range Games() {
			if c.OldID == gameType {
				g, ok = c, true
			}
		}
	}
	if !ok {
		return Game{}, fmt.Errorf("%w: %s", ErrUnknownGame, gameType)
	}
	return g, nil
}
