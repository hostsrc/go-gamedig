package minecraft

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// GameSpy 3 (UT3 query), the protocol a Java server with enable-query=true
// answers on its query port. node-gamedig's minecraft.js runs
// protocols/gamespy3.js with UTF-8 strings alongside the other two
// sub-queries; this is a port of just that configuration (session ID 1,
// no single-split or JC2MP quirks).
//
// References:
//   - https://minecraft.wiki/w/Query
//   - https://github.com/gamedig/node-gamedig/blob/master/protocols/gamespy3.js

const (
	gs3SessionID = 1
	gs3Challenge = 9
	gs3Stat      = 0
)

var ut3ValueBug = regexp.MustCompile(`^p[0-9]+$`)

// gs3Item is one entry of playerTeamInfo: field values in JavaScript
// property order.
type gs3Item struct {
	keys []string
	vals map[string]string
}

func (it *gs3Item) set(k, v string) {
	if k == "__proto__" { // assigning a string to __proto__ does nothing
		return
	}
	if it.vals == nil {
		it.vals = map[string]string{}
	}
	if _, ok := it.vals[k]; !ok {
		it.keys = append(it.keys, k)
	}
	it.vals[k] = v
}

// orderedKeys lists keys as Object.keys does: integer indexes ascending,
// then the rest in insertion order.
func (it *gs3Item) orderedKeys() []string {
	var idx, other []string
	for _, k := range it.keys {
		if isArrayIndex(k) {
			idx = append(idx, k)
		} else {
			other = append(other, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool { return len(idx[i]) < len(idx[j]) || len(idx[i]) == len(idx[j]) && idx[i] < idx[j] })
	return append(idx, other...)
}

func gs3Packet(typ byte, challenge *int32, payload []byte) []byte {
	b := []byte{0xfe, 0xfd, typ}
	b = binary.BigEndian.AppendUint32(b, gs3SessionID)
	if challenge != nil {
		b = binary.BigEndian.AppendUint32(b, uint32(*challenge))
	}
	return append(b, payload...)
}

// gs3Header checks a reply's type and session ID, skipping replies to
// other requests (including Bedrock pongs on the shared socket).
func gs3Header(p []byte, typ byte) (*jsReader, bool) {
	r := &jsReader{b: p}
	if r.uint8() != int(typ) {
		return nil, false
	}
	if r.uint32() != gs3SessionID {
		return nil, false
	}
	return r, true
}

// gs3Challenge parses the challenge reply. A nil key means none is sent.
func gs3ParseChallenge(body []byte) (*int32, error) {
	r := &jsReader{b: body}
	c := parseInt(r.string())
	if c == 0 {
		// Some servers send 0 when they don't want a key (BF2 does).
		return nil, nil
	}
	if math.IsNaN(c) {
		var zero int32 // Buffer.writeInt32BE(NaN) writes 0
		return &zero, nil
	}
	if c > math.MaxInt32 || c < math.MinInt32 {
		return nil, fmt.Errorf("RangeError: challenge %s out of range", formatNumber(c))
	}
	v := int32(c)
	return &v, nil
}

// gs3Assembler collects the split full-stat reply.
type gs3Assembler struct {
	packets    map[int][]byte
	numPackets int
}

func (a *gs3Assembler) add(r *jsReader) ([][]byte, error) {
	r.skip(9) // "splitnum\0"
	id := r.uint8()
	last := id & 0x80
	id &= 0x7f
	if last != 0 {
		a.numPackets = id + 1
	}
	r.skip(1)
	a.packets[id] = append([]byte(nil), r.rest()...)
	if a.numPackets == 0 || len(a.packets) != a.numPackets {
		return nil, nil
	}
	list := make([][]byte, a.numPackets)
	for i := range list {
		p, ok := a.packets[i]
		if !ok {
			return nil, fmt.Errorf("Missing packet #%d", i)
		}
		list[i] = p
	}
	return list, nil
}

func (q *query) queryGamespy3(ctx context.Context) (*sub, error) {
	var rtt time.Duration
	var body []byte
	// node-gamedig passes false as this request's challenge; false isn't
	// null, so it still writes a key, of 0.
	var zero int32
	err := q.udp.exchange(ctx, gs3Packet(gs3Challenge, &zero, nil), q.opts.SocketTimeout, &rtt, func(p []byte) (bool, error) {
		r, ok := gs3Header(p, gs3Challenge)
		if ok {
			body = append([]byte{}, r.rest()...)
		}
		return ok, nil
	})
	if err != nil {
		return nil, err
	}
	challenge, err := gs3ParseChallenge(body)
	if err != nil {
		return nil, err
	}
	var packets [][]byte
	asm := &gs3Assembler{packets: map[int][]byte{}}
	err = q.udp.exchange(ctx, gs3Packet(gs3Stat, challenge, []byte{0xff, 0xff, 0xff, 0x01}), q.opts.SocketTimeout, &rtt, func(p []byte) (bool, error) {
		r, ok := gs3Header(p, gs3Stat)
		if !ok {
			return false, nil
		}
		list, err := asm.add(r)
		if err != nil || list == nil {
			return false, err
		}
		packets = list
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	s, err := parseGamespy3(packets)
	if err != nil {
		return nil, err
	}
	s.rtt = rtt
	return s, nil
}

// parseGamespy3 decodes the assembled full-stat packets.
func parseGamespy3(packets [][]byte) (*sub, error) {
	s := newSub()
	vals := map[string]string{}
	teamInfo := map[string][]*gs3Item{}
	var teamOrder []string

	for i, packet := range packets {
		r := &jsReader{b: packet}
		if i == 0 {
			for !r.done() {
				key := r.string()
				if key == "" {
					break
				}
				value := r.string()
				for ut3ValueBug.MatchString(value) {
					value = r.string() // UT3 sends some keys without values
				}
				if key == "playerTeamInfo" {
					// Replaces the object node-gamedig is about to use
					// with a string, which then throws.
					return nil, errors.New("TypeError: playerTeamInfo overwritten")
				}
				if key != "__proto__" {
					vals[key] = value
				}
			}
		}
		for !r.done() {
			if r.uint8() <= 2 {
				continue
			}
			r.skip(-1)
			fieldID := r.string()
			if fieldID == "" {
				continue
			}
			fieldName, itemType, found := strings.Cut(fieldID, "_")
			if !found {
				itemType = "no_"
			} else if j := strings.IndexByte(itemType, '_'); j >= 0 {
				itemType = itemType[:j] // split('_')[1]
			}
			inherited := objectPrototypeKeys[itemType]
			if _, ok := teamInfo[itemType]; !ok && !inherited {
				teamInfo[itemType] = nil
				teamOrder = append(teamOrder, itemType)
			}
			offset := r.uint8()
			for !r.done() {
				item := r.string()
				if item == "" {
					break
				}
				if inherited {
					// The "in" check found Object.prototype's property,
					// which isn't an array.
					return nil, errors.New("TypeError: not an array")
				}
				items := teamInfo[itemType]
				for len(items) <= offset {
					items = append(items, &gs3Item{})
				}
				items[offset].set(fieldName, item)
				teamInfo[itemType] = items
				offset++
			}
		}
	}

	raw := map[string]any{}
	for k, v := range vals {
		raw[k] = v
	}
	pti := map[string]any{}
	for _, t := range teamOrder {
		list := make([]any, len(teamInfo[t]))
		for i, it := range teamInfo[t] {
			m := map[string]any{}
			for k, v := range it.vals {
				m[k] = v
			}
			list[i] = m
		}
		pti[t] = list
	}
	raw["playerTeamInfo"] = pti
	s.raw = raw

	if v, ok := vals["hostname"]; ok {
		s.name = v
	} else if v, ok := vals["servername"]; ok {
		s.name = v
	}
	if v, ok := vals["mapname"]; ok {
		s.mapName = v
	}
	s.password = vals["password"] == "1"
	if v, ok := vals["maxplayers"]; ok {
		s.maxPlayers = parseInt(v)
	}
	s.gamePort = math.NaN()
	if v, ok := vals["hostport"]; ok {
		s.gamePort = parseInt(v)
	}
	if v, ok := vals["gamever"]; ok {
		s.version = v
	}
	for _, it := range teamInfo[""] {
		p := map[string]any{}
		for _, from := range it.orderedKeys() {
			key := from
			var value any = it.vals[from]
			if key == "player" {
				key = "name"
			}
			switch key {
			case "score", "ping", "team", "deaths", "pid":
				value = parseInt(it.vals[from])
			}
			p[key] = value
		}
		name, ok := p["name"]
		if !ok {
			name = undefined
		}
		delete(p, "name")
		s.players = append(s.players, newPlayer(name, p))
	}
	if v, ok := vals["numplayers"]; ok {
		s.numPlayers = parseInt(v)
	} else {
		s.numPlayers = float64(len(s.players))
	}
	return s, nil
}
