package gamespy

import (
	"bytes"
	"context"
	"net"
	"strings"
)

// Query2 runs a GameSpy 2 query (info, players, then teams) over c, a UDP
// socket connected to the server's query port. All three requests must be
// answered, as in node-gamedig.
func Query2(ctx context.Context, c net.Conn, opts Options) (*Response, error) {
	q, stop := newConn(ctx, c, opts)
	defer stop()

	var bodies [3][]byte
	for i, typ := range [3][3]byte{{0xff, 0, 0}, {0, 0xff, 0}, {0, 0, 0xff}} {
		b, err := q.send2(ctx, typ)
		if err != nil {
			return nil, err
		}
		bodies[i] = b
	}
	res := Parse2(bodies[0], bodies[1], bodies[2], opts.StripColors)
	res.RTT = q.rtt
	return res, nil
}

// send2 sends one request and returns the body of the first reply that
// echoes the ping ID.
func (q *conn) send2(ctx context.Context, typ [3]byte) ([]byte, error) {
	req := append([]byte{0xfe, 0xfd, 0x00, 0x00, 0x00, 0x00, 0x01}, typ[:]...)
	var body []byte
	err := q.exchange(ctx, req, func(d []byte) (bool, error) {
		r := newReader(d)
		if r.uint(1) != 0 || r.uint(4) != 1 {
			return false, nil
		}
		body = bytes.Clone(r.rest())
		return true, nil
	})
	return body, err
}

// gamespy2IntKeys are the fields node-gamedig converts with parseInt.
var gamespy2IntKeys = map[string]bool{"score": true, "deaths": true, "ping": true, "team": true, "kills": true, "tickets": true}

// Parse2 decodes the bodies of the info, players and teams replies.
// stripColors is node-gamedig's stripColors option.
func Parse2(info, players, teams []byte, stripColors bool) *Response {
	raw := newObject()
	r := newReader(info)
	for !r.done() {
		key := r.string()
		value := r.string()
		if key == "" {
			break
		}
		raw.set(key, value)
	}

	res := &Response{Players: []map[string]any{}}
	if v, ok := raw.get("hostname"); ok {
		res.Name = v.(string)
	}
	if v, ok := raw.get("mapname"); ok {
		res.Map = v.(string)
	}
	res.Password = trueTest(mustGet(raw, "password"))
	if v, ok := raw.get("maxplayers"); ok {
		res.MaxPlayers = parseInt(v.(string))
	}
	if v, ok := raw.get("hostport"); ok {
		if port := parseInt(v.(string)); truthy(port) {
			res.GamePort = numberString(port)
		}
	}
	if v, ok := raw.get("gamever"); ok {
		res.Version = v.(string)
	}

	for _, p := range readFieldData(newReader(players)) {
		res.Players = append(res.Players, p.toMap())
	}
	if v, ok := raw.get("numplayers"); ok {
		res.NumPlayers = parseInt(v.(string))
	} else {
		res.NumPlayers = float64(len(res.Players))
	}

	teamList := []any{}
	for _, t := range readFieldData(newReader(teams)) {
		teamList = append(teamList, t.toMap())
	}
	raw.set("teams", teamList)

	// America's Army 1 and 2 (gamename "armygame") use Unreal 2 colour
	// codes, which node-gamedig strips unless stripColors is off.
	if mustGet(raw, "gamename") == "armygame" {
		strip := func(s string) string {
			if stripColors {
				return stripUnreal2Colors(s)
			}
			return s
		}
		res.Name = strip(res.Name)
		res.Map = strip(res.Map)
		for _, k := range raw.keys() {
			if s, ok := mustGet(raw, k).(string); ok {
				raw.set(k, strip(s))
			}
		}
		for _, p := range res.Players {
			if s, ok := p["name"].(string); ok {
				p["name"] = strip(s)
			}
		}
	}
	res.Raw = raw.toMap()
	return res
}

// readFieldData is gamespy2.js's readFieldData: a row count (sometimes
// missing), NUL-terminated column names ending with an empty one, then
// rows of values.
func readFieldData(r *reader) []*object {
	r.uint(1) // always 0
	// Some games omit the count byte. Counts are below 64 and column names
	// start with a letter, so a larger byte is the first column name.
	if count := r.uint(1); count > 64 {
		r.skip(-1)
	}

	var fields []string
	for !r.done() {
		field := r.string()
		if field == "" {
			break
		}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return nil
	}

	var units []*object
	for !r.done() {
		unit := newObject()
		for index, key := range fields {
			value := r.string()
			if value == "" && index == 0 {
				return units
			}
			key = strings.TrimSuffix(key, "_") // many fields end with "_"
			switch key {
			case "player", "team_t":
				key = "name"
			case "tickets_t":
				key = "tickets"
			}
			var v any = value
			if gamespy2IntKeys[key] {
				if value == "" {
					continue
				}
				v = parseInt(value)
			}
			unit.set(key, v)
		}
		units = append(units, unit)
	}
	return units
}

// stripUnreal2Colors is gamespy2.js's stripColor, which replaces the regular
// expression /\x1b...|[\x00-\x1a]/g with the empty string.
// An ESC and the three characters after it (none of them a line
// terminator) are removed, as is every control character up to 0x1A.
func stripUnreal2Colors(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == 0x1b && i+3 < len(runes) && !isLineTerminator(runes[i+1]) &&
			!isLineTerminator(runes[i+2]) && !isLineTerminator(runes[i+3]) {
			i += 3
			continue
		}
		if c <= 0x1a {
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func isLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
}
