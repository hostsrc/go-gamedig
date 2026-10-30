// Package gamespy implements the GameSpy 1, 2 and 3 server query
// protocols, used by many games from 1999-2010 (Battlefield 1942 and 2,
// ARMA, Crysis, America's Army, Unreal Tournament and others).
//
// Behaviour follows node-gamedig 5.3.3's protocols/gamespy1.js,
// gamespy2.js and gamespy3.js so results are interchangeable with it,
// including their JavaScript type conversions. Details were cross-checked
// against rust-gamedig's protocols/gamespy; where its stricter handling of
// untrusted replies applies, this package caps reply sizes and counts
// instead of failing on them (see maxReplyBytes, maxParts, maxItems).
//
// References:
//   - https://github.com/gamedig/node-gamedig/tree/master/protocols
//   - https://github.com/gamedig/rust-gamedig/tree/main/crates/lib/src/protocols/gamespy
package gamespy

import (
	"context"
	"errors"
	"math"
	"net"
	"strings"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

// gamespy1StringKeys are value types node-gamedig never converts to
// numbers or booleans.
var gamespy1StringKeys = map[string]bool{"website": true, "gametype": true, "gamemode": true, "player": true}

// Query1 runs a GameSpy 1 query ("\status\") over c, a UDP socket
// connected to the server's query port.
func Query1(ctx context.Context, c net.Conn, opts Options) (*Response, error) {
	q, stop := newConn(ctx, c, opts)
	defer stop()

	output, err := q.status1(ctx)
	if err != nil {
		return nil, err
	}
	res := Parse1(output)
	res.RTT = q.rtt
	return res, nil
}

// status1 sends the status request and merges every part of the reply
// into one object, as gamespy1.js's sendPacket does.
func (q *conn) status1(ctx context.Context) (*object, error) {
	output := newObject()
	var receivedQueryID string
	parts := map[float64]bool{}
	maxPartNum := 0.0
	total := 0

	err := q.exchange(ctx, []byte(`\status\xserverquery`), func(d []byte) (bool, error) {
		split := strings.Split(binreader.DecodeWindows1252(d), `\`)[1:]
		data := newObject()
		for i := 0; i < len(split); i += 2 {
			value := ""
			if i+1 < len(split) {
				value = split[i+1]
			}
			data.set(split[i], value)
		}

		partFinal := data.has("final")
		queryID := ""
		partNum := 0.0
		if v, _ := data.get("queryid"); v != nil && v.(string) != "" {
			idSplit := strings.Split(v.(string), ".")
			if len(idSplit) >= 2 {
				partNum = toNumber(idSplit[1])
			}
			queryID = idSplit[0]
		}
		data.del("final")
		data.del("queryid")
		q.debugf("part num=%v queryId=%q final=%v", partNum, queryID, partFinal)

		if queryID != "" {
			if receivedQueryID != "" && receivedQueryID != queryID {
				q.debugf("rejected packet (wrong query ID)")
				return false, nil
			}
			receivedQueryID = queryID
		}
		if partNum == 0 || math.IsNaN(partNum) {
			partNum = float64(len(parts))
		}
		if parts[partNum] {
			q.debugf("rejected packet (duplicate part)")
			return false, nil
		}
		if len(parts) >= maxParts {
			return false, errors.New("too many reply parts")
		}
		if total += len(d); total > maxReplyBytes {
			return false, errors.New("reply too large")
		}
		parts[partNum] = true
		if partFinal {
			maxPartNum = partNum
		}
		for _, k := range data.keys() {
			v, _ := data.get(k)
			output.set(k, v)
		}
		return maxPartNum != 0 && float64(len(parts)) == maxPartNum, nil
	})
	return output, err
}

// normalize1 is gamespy1.js's normalizeEntry: lowercase keys, and turn
// values that look like booleans or numbers into them, except for keys that
// hold names and a few known text fields.
func normalize1(key, value string) (string, any) {
	key = strings.ToLower(key)
	keyType := key
	if split := strings.Split(key, "_"); len(split) == 2 && !math.IsNaN(toNumber(split[1])) {
		keyType = split[0]
	}
	if gamespy1StringKeys[keyType] || strings.Contains(keyType, "name") {
		return key, value
	}
	switch strings.ToLower(value) {
	case "true":
		return key, true
	case "false":
		return key, false
	}
	if n := toNumber(value); value != "" && !math.IsNaN(n) {
		return key, n
	}
	return key, value
}

// Parse1 turns the merged GameSpy 1 key/value reply into a response.
func Parse1(output *object) *Response {
	data := newObject()
	for _, k := range output.keys() {
		v, _ := output.get(k)
		data.define(normalize1(k, v.(string)))
	}

	res := &Response{Players: []map[string]any{}}
	if v, ok := data.get("hostname"); ok {
		res.Name, _ = v.(string)
	}
	if v, ok := data.get("mapname"); ok {
		res.Map, _ = v.(string)
	}
	res.Password = trueTest(mustGet(data, "password"))
	if v, ok := data.get("maxplayers"); ok {
		res.MaxPlayers = valueToNumber(v)
	}
	if v, ok := data.get("hostport"); ok {
		if port := valueToNumber(v); truthy(port) {
			res.GamePort = numberString(port)
		}
	}

	teamOffByOne := mustGet(data, "gamename") == "bfield1942"
	playersByID := newObject()
	teamNames := newObject()
	for _, ident := range data.keys() {
		split := strings.Split(ident, "_")
		if len(split) != 2 {
			continue
		}
		key := strings.ToLower(split[0])
		id := toNumber(split[1])
		if math.IsNaN(id) {
			continue
		}
		value := mustGet(data, ident)
		data.del(ident)

		if key != "team" && strings.HasPrefix(key, "team") {
			// Team info; only the name is kept.
			if key == "teamname" {
				teamNames.set(numberString(id), value)
			}
			continue
		}
		pk := numberString(id)
		p, ok := playersByID.get(pk)
		if !ok {
			p = newObject()
			playersByID.set(pk, p)
		}
		if key == "playername" || key == "player" {
			key = "name"
		}
		if key == "team" && !math.IsNaN(valueToNumber(value)) {
			key = "teamId"
			value = addOffset(value, teamOffByOne)
		}
		p.(*object).set(key, value)
	}
	teams := map[string]any{}
	for _, k := range teamNames.keys() {
		teams[k] = mustGet(teamNames, k)
	}
	data.set("teams", teams)

	for _, k := range playersByID.keys() {
		p := mustGet(playersByID, k).(*object)
		if teamID, ok := p.get("teamId"); ok {
			if teamNames.len() > 0 {
				name, _ := teamNames.get(propertyKey(teamID))
				if !truthy(name) {
					name = ""
				}
				p.set("team", name)
			} else {
				p.set("team", teamID)
				p.del("teamId")
			}
		}
		res.Players = append(res.Players, p.toMap())
	}

	// Some servers (bf1942) send stale player entries when players leave;
	// node-gamedig keeps only the first numplayers.
	if n := valueToNumber(mustGet(data, "numplayers")); !math.IsNaN(n) && float64(len(res.Players)) > n {
		res.Players = res.Players[:sliceEnd(n, len(res.Players))]
	}
	res.NumPlayers = float64(len(res.Players))
	res.Version = valueToString(mustGet(data, "gamever"))
	res.Raw = data.toMap()
	return res
}

func mustGet(o *object, k string) any {
	v, _ := o.get(k)
	return v
}

// addOffset is `value += teamOffByOne ? -1 : 0` in JavaScript, where value
// is a number, a boolean or the empty string (the only string that is not
// NaN here, and which concatenates).
func addOffset(value any, offByOne bool) any {
	off := 0.0
	if offByOne {
		off = -1
	}
	if s, ok := value.(string); ok {
		return s + numberString(off)
	}
	return valueToNumber(value) + off
}

// sliceEnd is the end index Array.prototype.slice(0, end) uses.
func sliceEnd(end float64, length int) int {
	switch {
	case math.IsNaN(end):
		return 0
	case end < 0:
		return max(length+int(math.Max(math.Trunc(end), -float64(length))), 0)
	case end >= float64(length):
		return length
	}
	return int(end)
}
