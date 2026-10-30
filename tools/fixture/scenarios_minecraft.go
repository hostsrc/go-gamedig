package main

import (
	"encoding/base64"
	"encoding/binary"
	"strings"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

func init() { scenarioSets = append(scenarioSets, minecraftScenarios) }

// Every minecraft query opens a TCP Server List Ping and sends a GameSpy 3
// challenge and a Bedrock ping over UDP, all to the same port, so every
// scenario lists all three; unanswered ones simply have no responses.
func minecraftScenarios() []scenario {
	silentGS3 := ex(gs3Req(9, nil))
	silentBedrock := ex(bedrockPing())
	silentJava := javaEx()

	favicon := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n not really a png, but long enough to matter "+strings.Repeat("x", 300)))
	plain := `{"version":{"name":"Paper 1.20.4","protocol":765},` +
		`"players":{"max":100,"online":3,"sample":[` +
		`{"name":"Notch","id":"069a79f4-44e9-4726-a5be-fca90e38aaf5"},` +
		`{"name":"jeb_","id":"853c80ef-3c37-49fd-aa49-938b674adae6"},` +
		`{"name":"§7anonymous","id":null},{"id":"00000000-0000-0000-0000-000000000000"}]},` +
		`"description":"  §aA   §lMinecraft§r\n Server  ","favicon":"` + favicon + `",` +
		`"enforcesSecureChat":true,"previewsChat":false}`

	chat := `{"version":{"name":"1.21.1","protocol":767},"players":{"max":20,"online":0},` +
		`"description":{"text":"","extra":[` +
		`{"text":"Hello ","color":"gold","extra":[{"text":"nested ","bold":true,"extra":[{"text":"deep"}]},{"text":" world"}]},` +
		`"plain strings are skipped",{"translate":"no.text.here"},{"text":42},{"text":0},{"text":["a",null,"b"]},` +
		`{"text":{"obj":1}},{"text":false},{"text":true},{"text":" §x tail\t\t end §"}` +
		`]}}`

	forge := `{"version":{"name":"1.20.1","protocol":763},"players":{"max":50,"online":1,` +
		`"sample":[{"name":"modder","id":"11111111-2222-3333-4444-555555555555"}]},` +
		`"description":{"text":"All the Mods 9"},` +
		`"forgeData":{"channels":[{"res":"forge:tier_sorting","version":"1.0","required":false}],` +
		`"mods":[{"modId":"forge","modmarker":"47.2.0"},{"modId":"jei","modmarker":"15.2.0.27"}],` +
		`"fmlNetworkVersion":3,"truncated":false,"d":"ȳ\u0000\u0001"},` +
		`"modinfo":{"type":"FML","modList":[{"modid":"minecraft","version":"1.20.1"}]},` +
		`"preventsChatReports":true,"isModded":true}`

	odd := `{"version":"1.8.9","players":{"online":7,"sample":"ab🎮"},"description":null,` +
		`"numbers":[1e400,-1e400,-0,1.0,0.1,1e21,123456789012345678901234567890,5e-324,-2.5e-8],` +
		`"nested":{"__proto__":{"x":1},"empty":{},"list":[[],[[]],{}]},` +
		`"text":"tab\there \ud83c\udfae \ud800 lone",` +
		"\"raw\":\"bad \xff\xfe utf8 \xe2\x82 cut\"}"

	legacyKick := []byte{0xff, 0x00, 0x23, 0x00, 0xa7, 0x00, 0x31, 0x00, 0x00, 0x00, 0x37, 0x00, 0x38, 0x00, 0x00}
	bccDoc := `{"projectId":12345,"name":"Better Compatibility Checker","version":"4.0.8","releaseType":"release"}`

	allThree := `{"version":{"name":"Velocity 3.3.0","protocol":767},"players":{"max":500,"online":12,` +
		`"sample":[{"name":"javaone","id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}]},"description":"Java MOTD"}`

	gs3Stat := gs3StatReply(
		[][2]string{
			{"hostname", "§6Query  §lMOTD"}, {"gametype", "SMP"}, {"game_id", "MINECRAFT"},
			{"version", "1.20.4"}, {"plugins", "Paper on 1.20.4: Essentials 2.20"}, {"map", "world"},
			{"numplayers", "2"}, {"maxplayers", "20"}, {"hostport", "25565"}, {"hostip", "127.0.0.1"},
		},
		"\x01player_\x00\x00alice\x00\xc3\xa9mile \xf0\x9f\x8e\xae\x00\x00",
	)

	bedrockFull := "MCPE;§bDedicated  Server §r;671;1.20.80;3;10;13253860892328930865;Bedrock level;Survival;1;19132;19133;"
	bedrockShort := "MCPE;Short;390;1.14.60;abc;0x1F"

	return []scenario{
		{
			Name:        "minecraft_java_plain",
			Description: "Java status with a plain-text MOTD (colour codes, runs of spaces), players sample and favicon; no query or Bedrock",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{javaEx(javaStatus(plain)), silentGS3, silentBedrock},
		},
		{
			Name:        "minecraft_java_chat",
			Description: "chat-component MOTD: nested extra arrays (reversed in raw like node-gamedig), plain strings and non-string text values",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{javaEx(javaStatus(chat)), silentGS3, silentBedrock},
		},
		{
			Name:        "minecraft_forge",
			Description: "modded server: forgeData and legacy modinfo are kept in raw",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{javaEx(javaStatus(forge)), silentGS3, silentBedrock},
		},
		{
			Name:        "minecraft_bcc",
			Description: "Better Compatibility Checker JSON after the status string, status reply split over three TCP writes",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				javaEx(splitAt(javaStatusBCC(allThree, bccDoc), 4, 60)...),
				silentGS3, silentBedrock,
			},
		},
		{
			Name:        "minecraft_odd_json",
			Description: "status JSON with a null description, a string players sample, version without a name, odd numbers and invalid UTF-8",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{javaEx(javaStatus(odd)), silentGS3, silentBedrock},
		},
		{
			Name:        "minecraft_java_query",
			Description: "Java server with enable-query: GameSpy 3 name and players override the status ping",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				javaEx(javaStatus(plain)),
				ex(gs3Req(9, nil), gs3ChallengeReply("9513307")),
				ex(gs3Req(0, i32p(9513307)), gs3Stat),
				silentBedrock,
			},
		},
		{
			Name:        "minecraft_all_three",
			Description: "Java, GameSpy 3 (split reply out of order, negative challenge) and Bedrock all answer; Bedrock wins name, map and version",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				javaEx(javaStatus(allThree)),
				ex(gs3Req(9, nil), gs3ChallengeReply("-42")),
				ex(gs3Req(0, i32p(-42)), reorderGS3(gs3Split(
					"hostname\x00GS3 name\x00numplayers\x004\x00maxplayers\x0040\x00gamever\x001.20\x00password\x001\x00mapname\x00gsmap\x00\x00",
					"\x01player_\x00\x00one\x00two\x00\x00score_\x00\x00\x31\x00x\x00\x00team_t\x00\x00red\x00\x00",
				))...),
				ex(bedrockPing(), bedrockPong(bedrockFull)),
			},
		},
		{
			Name:        "minecraft_bedrock_only",
			Description: "Bedrock server: TCP refused, no query, unconnected pong with every optional field",
			Args:        []string{"--type=mbe", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{silentGS3, ex(bedrockPing(), bedrockPong(bedrockFull))},
		},
		{
			Name:        "minecraft_java_silent_bedrock",
			Description: "TCP accepted but the status ping never answered, while Bedrock answers a minimal pong with unparsable player counts",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{silentJava, silentGS3, ex(bedrockPing(), []byte("\x00junk"), bedrockPong(bedrockShort))},
		},
		{
			Name:        "minecraft_bedrock_bad_length",
			Description: "Bedrock pong whose status length is wrong fails that sub-query; Java still answers",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				javaEx(javaStatus(allThree)), silentGS3,
				ex(bedrockPing(), badLength(bedrockPong(bedrockFull))),
			},
		},
		{
			Name:        "minecraft_legacy_kick",
			Description: "pre-1.7 server answering with a legacy 0xFF kick packet, which node-gamedig doesn't parse: the query fails",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{javaEx(legacyKick), silentGS3, silentBedrock},
		},
		{
			Name:        "minecraft_timeout",
			Description: "nothing answers",
			Args:        []string{"--type=minecraft", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{silentJava, silentGS3, silentBedrock},
		},
	}
}

// javaPortOffset is where the handshake holds the port: after the packet
// length, packet ID, protocol VarInt, host length and "127.0.0.1".
const javaPortOffset = 4 + len("127.0.0.1")

// javaEx is the Java status ping on TCP with its reply written in the
// given pieces.
func javaEx(responses ...[]byte) fixture.Exchange {
	e := ex(javaRequest("127.0.0.1"), responses...)
	e.Network = "tcp"
	e.PortOffsets = []int{javaPortOffset}
	return e
}

// javaRequest is node-gamedig's handshake (protocol 47, next state 1) and
// status request; the port is left zero for the server to fill in.
func javaRequest(host string) []byte {
	hs := appendVarint(nil, 47)
	hs = appendVarint(hs, uint64(len(host)))
	hs = append(hs, host...)
	hs = append(hs, 0, 0, 1)
	return mcPacket(mcPacket(nil, hs), nil)
}

// mcPacket appends a packet with ID 0.
func mcPacket(b, data []byte) []byte {
	b = appendVarint(b, uint64(len(data)+1))
	return append(append(b, 0), data...)
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func javaStatus(json string) []byte {
	return mcPacket(nil, append(appendVarint(nil, uint64(len(json))), json...))
}

// javaStatusBCC appends a Better Compatibility Checker document, length
// prefixed, inside the status packet.
func javaStatusBCC(json, bcc string) []byte {
	data := append(appendVarint(nil, uint64(len(json))), json...)
	data = append(appendVarint(data, uint64(len(bcc))), bcc...)
	return mcPacket(nil, data)
}

func splitAt(b []byte, at ...int) [][]byte {
	var out [][]byte
	prev := 0
	for _, i := range at {
		out = append(out, b[prev:i])
		prev = i
	}
	return append(out, b[prev:])
}

func i32p(v int32) *int32 { return &v }

// gs3Req is a GameSpy 3 request. node-gamedig passes false as the
// challenge of the challenge request itself, which still writes a zero key.
func gs3Req(typ byte, challenge *int32) []byte {
	b := []byte{0xfe, 0xfd, typ, 0, 0, 0, 1}
	if typ == 9 {
		b = append(b, 0, 0, 0, 0)
	} else if challenge != nil {
		b = binary.BigEndian.AppendUint32(b, uint32(*challenge))
	}
	if typ == 0 {
		b = append(b, 0xff, 0xff, 0xff, 0x01)
	}
	return b
}

func gs3ChallengeReply(key string) []byte {
	return append([]byte{9, 0, 0, 0, 1}, key+"\x00"...)
}

// gs3StatReply is a single-packet full stat: key/values, then the
// player section as raw bytes.
func gs3StatReply(kv [][2]string, players string) []byte {
	b := []byte{0, 0, 0, 0, 1}
	b = append(b, "splitnum\x00\x80\x00"...)
	for _, p := range kv {
		b = append(append(append(b, p[0]...), 0), append([]byte(p[1]), 0)...)
	}
	b = append(b, 0)
	return append(b, players...)
}

// gs3Split returns a full stat in one packet per part, numbered in order
// with the last flagged.
func gs3Split(parts ...string) [][]byte {
	var out [][]byte
	for i, p := range parts {
		id := byte(i)
		if i == len(parts)-1 {
			id |= 0x80
		}
		b := []byte{0, 0, 0, 0, 1}
		b = append(b, "splitnum\x00"...)
		b = append(b, id, 0)
		out = append(out, append(b, p...))
	}
	return out
}

func reorderGS3(parts [][]byte) [][]byte {
	out := make([][]byte, 0, len(parts))
	for i := len(parts) - 1; i >= 0; i-- {
		out = append(out, parts[i])
	}
	return out
}

func bedrockPing() []byte {
	b := []byte{0x01, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	b = append(b, 0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78)
	return append(b, make([]byte, 8)...)
}

func bedrockPong(status string) []byte {
	b := []byte{0x1c, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	b = append(b, 0xb7, 0xee, 0x3a, 0x4a, 0x1a, 0x2b, 0x3c, 0x31)
	b = append(b, 0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78)
	b = binary.BigEndian.AppendUint16(b, uint16(len(status)))
	return append(b, status...)
}

// badLength makes a pong's declared status length one too long.
func badLength(pong []byte) []byte {
	b := append([]byte(nil), pong...)
	n := binary.BigEndian.Uint16(b[33:])
	binary.BigEndian.PutUint16(b[33:], n+1)
	return b
}
