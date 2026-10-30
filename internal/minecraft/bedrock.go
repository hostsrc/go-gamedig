package minecraft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

// Bedrock Edition RakNet unconnected ping, as node-gamedig's
// protocols/minecraftbedrock.js sends it. The pong carries a
// semicolon-separated status string.
//
// References:
//   - https://minecraft.wiki/w/RakNet#Unconnected_Ping
//   - https://github.com/gamedig/node-gamedig/blob/master/protocols/minecraftbedrock.js
//   - https://github.com/gamedig/rust-gamedig/blob/main/crates/lib/src/games/minecraft/protocol/bedrock.rs

var (
	bedrockNonce = []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	bedrockMagic = []byte{
		0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe,
		0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78,
	}
	// bedrockRequest is ID_UNCONNECTED_PING, the nonce node-gamedig uses as
	// its timestamp, the offline magic and a zero client GUID.
	bedrockRequest = func() []byte {
		b := []byte{0x01}
		b = append(b, bedrockNonce...)
		b = append(b, bedrockMagic...)
		return append(b, make([]byte, 8)...)
	}()
)

// parseBedrock handles one datagram. Datagrams that are not this pong are
// skipped (nil, nil); a malformed pong fails the sub-query.
func parseBedrock(p []byte) (*sub, error) {
	r := &jsReader{b: p}
	if r.uint8() != 0x1c { // ID_UNCONNECTED_PONG
		return nil, nil
	}
	if !bytes.Equal(r.part(8), bedrockNonce) {
		return nil, nil
	}
	r.skip(8) // server GUID
	if !bytes.Equal(r.part(16), bedrockMagic) {
		return nil, nil
	}
	statusLen := r.uint16()
	if r.remaining() != statusLen {
		return nil, fmt.Errorf("Invalid status length: %d vs %d", r.remaining(), statusLen)
	}
	split := strings.Split(binreader.DecodeUTF8(r.rest()), ";")
	if len(split) < 6 {
		return nil, errors.New("Missing enough chunks in status str")
	}
	shift := func() string {
		v := split[0]
		split = split[1:]
		return v
	}
	s := newSub()
	s.raw["edition"] = shift()
	s.name = shift()
	s.raw["protocolVersion"] = shift()
	mcVersion := shift()
	s.raw["mcVersion"] = mcVersion
	s.version = mcVersion
	s.numPlayers = parseInt(shift())
	s.maxPlayers = parseInt(shift())
	if len(split) > 0 {
		s.raw["serverId"] = shift()
	}
	if len(split) > 0 {
		s.mapName = shift()
	}
	if len(split) > 0 {
		s.raw["gameMode"] = shift()
	}
	if len(split) > 0 {
		s.raw["nintendoOnly"] = truthy(parseInt(shift()))
	}
	if len(split) > 0 {
		s.raw["ipv4Port"] = shift()
	}
	if len(split) > 0 {
		s.raw["ipv6Port"] = shift()
	}
	return s, nil
}

func (q *query) queryBedrock(ctx context.Context) (*sub, error) {
	var res *sub
	var rtt time.Duration
	err := q.udp.exchange(ctx, bedrockRequest, q.opts.SocketTimeout, &rtt, func(p []byte) (bool, error) {
		s, err := parseBedrock(p)
		if err != nil || s == nil {
			return false, err
		}
		res = s
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	res.rtt = rtt
	return res, nil
}
