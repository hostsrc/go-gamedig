package gamespy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"regexp"
	"strings"
)

const sessionID = 1

// Query3 runs a GameSpy 3 query (challenge, then the full status) over c,
// a UDP socket connected to the server's query port.
func Query3(ctx context.Context, c net.Conn, opts Options) (*Response, error) {
	q, stop := newConn(ctx, c, opts)
	defer stop()

	// node-gamedig passes `false` as the challenge of the handshake; since
	// that is not null its request carries four zero bytes where the
	// challenge goes. rust-gamedig sends the 7-byte form; servers accept
	// both, and this matches node byte for byte.
	body, err := q.send3(ctx, 9, request3(9, 0, true, nil))
	if err != nil {
		return nil, err
	}
	cr := newReader(body[0])
	cr.utf8 = opts.UTF8
	challenge := parseInt(cr.string())
	q.debugf("challenge key %v", challenge)
	// Some servers (BF2) send 0 when they don't want a challenge key used.
	hasChallenge := challenge != 0
	if hasChallenge && (challenge > math.MaxInt32 || challenge < math.MinInt32) {
		// node's Buffer.writeInt32BE throws for out-of-range values.
		return nil, fmt.Errorf("challenge key %s is out of int32 range", numberString(challenge))
	}
	chal := int32(0) // NaN is written as 0, like Buffer.writeInt32BE
	if !math.IsNaN(challenge) {
		chal = int32(challenge)
	}

	packets, err := q.send3(ctx, 0, request3(0, chal, hasChallenge, []byte{0xff, 0xff, 0xff, 0x01}))
	if err != nil {
		return nil, err
	}
	res, err := parse3(packets, opts.UTF8)
	if err != nil {
		return nil, err
	}
	res.RTT = q.rtt
	return res, nil
}

func request3(typ byte, challenge int32, withChallenge bool, payload []byte) []byte {
	b := []byte{0xfe, 0xfd, typ}
	b = binary.BigEndian.AppendUint32(b, sessionID)
	if withChallenge {
		b = binary.BigEndian.AppendUint32(b, uint32(challenge))
	}
	return append(b, payload...)
}

// send3 sends req and returns the reply body. For the status request
// (typ 0) it reassembles the "splitnum" packets and returns them in order;
// otherwise it returns the first matching packet's body.
func (q *conn) send3(ctx context.Context, typ byte, req []byte) ([][]byte, error) {
	assemble := typ == 0
	numPackets := 0
	packets := map[int][]byte{}
	total := 0
	var result [][]byte
	err := q.exchange(ctx, req, func(d []byte) (bool, error) {
		r := newReader(d)
		if r.uint(1) != uint32(typ) {
			q.debugf("skipping packet, type mismatch")
			return false, nil
		}
		if r.uint(4) != sessionID {
			q.debugf("skipping packet, session id mismatch")
			return false, nil
		}
		if !assemble {
			result = [][]byte{bytes.Clone(r.rest())}
			return true, nil
		}
		r.skip(9) // usually "splitnum\x00"
		id := int(r.uint(1))
		last := id&0x80 != 0
		id &= 0x7f
		if last {
			numPackets = id + 1
		}
		r.skip(1) // another packet number byte, not understood
		part := bytes.Clone(r.rest())
		if total += len(part) - len(packets[id]); total > maxReplyBytes {
			return false, errors.New("reply too large")
		}
		packets[id] = part
		q.debugf("received packet #%d last=%v", id, last)
		if numPackets == 0 || len(packets) != numPackets {
			return false, nil
		}
		for i := range numPackets {
			p, ok := packets[i]
			if !ok {
				return false, fmt.Errorf("Missing packet #%d", i)
			}
			result = append(result, p)
		}
		return true, nil
	})
	return result, err
}

var ut3Placeholder = regexp.MustCompile(`^p[0-9]+$`)

// gamespy3IntKeys are the player fields node-gamedig converts with
// parseInt.
var gamespy3IntKeys = map[string]bool{"score": true, "ping": true, "team": true, "deaths": true, "pid": true}

// Parse3 decodes the reassembled status packets. The first starts with
// server key/value pairs; every packet then carries player, team and item
// columns.
func Parse3(packets [][]byte) (*Response, error) { return parse3(packets, false) }

func parse3(packets [][]byte, utf8 bool) (*Response, error) {
	raw := newObject()
	info := newObject() // raw.playerTeamInfo: item type -> *[]*object
	slots := 0

	for i, packet := range packets {
		r := newReader(packet)
		r.utf8 = utf8
		if i == 0 {
			for !r.done() {
				key := r.string()
				if key == "" {
					break
				}
				value := r.string()
				// UT3 sends some keys without values.
				for ut3Placeholder.MatchString(value) {
					value = r.string()
				}
				if key == "playerTeamInfo" {
					// Overwrites node-gamedig's own field with a string,
					// which makes its parser throw.
					return nil, errors.New("server sent a playerTeamInfo key")
				}
				raw.set(key, value)
			}
		}

		for !r.done() {
			if r.uint(1) <= 2 {
				continue
			}
			r.skip(-1)
			fieldID := r.string()
			if fieldID == "" {
				continue
			}
			split := strings.Split(fieldID, "_")
			fieldName := split[0]
			itemType := "no_"
			if len(split) > 1 {
				itemType = split[1]
			}
			// `itemType in playerTeamInfo` also finds Object.prototype's
			// members, which node then fails to use as an item list.
			inherited := inheritedKeys[itemType]
			if !inherited && !info.has(itemType) {
				info.set(itemType, &[]*object{})
			}

			offset := int(r.uint(1))
			for !r.done() {
				item := r.string()
				if item == "" {
					break
				}
				if inherited {
					return nil, fmt.Errorf("item type %q is not usable", itemType)
				}
				items := mustGet(info, itemType).(*[]*object)
				for len(*items) <= offset {
					if slots++; slots > maxItems {
						return nil, errors.New("too many player/team items")
					}
					*items = append(*items, newObject())
				}
				(*items)[offset].set(fieldName, item)
				offset++
			}
		}
	}

	res := &Response{Players: []map[string]any{}}
	if v, ok := raw.get("hostname"); ok {
		res.Name = v.(string)
	} else if v, ok := raw.get("servername"); ok {
		res.Name = v.(string)
	}
	if v, ok := raw.get("mapname"); ok {
		res.Map = v.(string)
	}
	res.Password = mustGet(raw, "password") == "1"
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

	if v, ok := info.get(""); ok {
		for _, playerInfo := range *v.(*[]*object) {
			player := newObject()
			for _, key := range playerInfo.keys() {
				var value any = mustGet(playerInfo, key)
				if key == "player" {
					key = "name"
				}
				if gamespy3IntKeys[key] {
					value = parseInt(value.(string))
				}
				player.set(key, value)
			}
			res.Players = append(res.Players, player.toMap())
		}
	}
	if v, ok := raw.get("numplayers"); ok {
		res.NumPlayers = parseInt(v.(string))
	} else {
		res.NumPlayers = float64(len(res.Players))
	}

	teamInfo := make(map[string]any, info.len())
	for _, k := range info.keys() {
		items := *mustGet(info, k).(*[]*object)
		list := make([]any, len(items))
		for i, it := range items {
			list[i] = it.toMap()
		}
		teamInfo[k] = list
	}
	res.Raw = raw.toMap()
	res.Raw["playerTeamInfo"] = teamInfo
	return res, nil
}
