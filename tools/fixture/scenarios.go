package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"os/exec"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

// scenario is a scripted server conversation. Requests are the exact bytes
// node-gamedig is expected to send; recording fails if it sends others.
type scenario struct {
	Name        string
	Description string
	Args        []string
	Exchanges   []fixture.Exchange
}

// scenarioSets holds one function per protocol family. Each family adds
// its own from an init function in scenarios_<family>.go.
var scenarioSets = []func() []scenario{valveScenarios}

func scenarios() []scenario {
	var all []scenario
	for _, set := range scenarioSets {
		all = append(all, set()...)
	}
	return all
}

func valveScenarios() []scenario {
	const key1, key2, key3 = 0x11223344, 0xA1B2C3D4, 0x0BADF00D

	infoReq := pkt().header().u8(0x54).str("Source Engine Query").b
	playersReq := func(challenge uint32) []byte { return pkt().header().u8(0x55).u32(challenge).b }
	rulesReq := func(challenge uint32) []byte { return pkt().header().u8(0x56).u32(challenge).b }
	challengeRes := func(k uint32) []byte { return pkt().header().u8(0x41).u32(k).b }
	withKey := func(b []byte, k uint32) []byte { return binary.LittleEndian.AppendUint32(bytes.Clone(b), k) }

	cs2Info := sourceInfo(info{
		protocol: 17, name: "  Padded ünï € name\t", mapName: "de_dust2", folder: "cs", game: "Counter-Strike 2",
		appID: 730, players: 3, maxPlayers: 10, bots: 1, server: 'd', env: 'l', vac: 1, version: "1.40.0.0",
		edf: 0x80 | 0x10 | 0x40 | 0x20 | 0x01, gamePort: 27016, steamID: 90071996842377216,
		tvPort: 27020, tvName: "GOTV", tags: "secure,competitive", gameID: 730,
	})
	cs2Players := playersRes(
		player{"alice", 12, 345.5},
		player{"Max Players", 0, 0},
		player{"bob", -3, 0.1},
		player{"BOT Kurt", 0, 1234.75},
	)

	cs16Info := sourceInfo(info{
		protocol: 48, name: "\xcf\xf0\xe8\xe2\xe5\xf2 server \xe2\x82 #1", mapName: "de_inferno", folder: "cstrike",
		game: "Counter-Strike", appID: 10, players: 5, maxPlayers: 32, bots: 2, server: 'd', env: 'l', version: "1.1.2.7/Stdio",
		edf: 0x80 | 0x20, gamePort: 27015, tags: "",
	})
	cs16OldInfo := pkt().header().u8(0x6D).str("127.0.0.1:27015").str("old reply").b
	cs16Players := playersRes(
		player{"[\xc0\xe4\xec\xe8\xed] player", 40, 3600.5},
		player{"zombie", 2, 61.25},
		player{"\xf0\x9f\x8e\xae gamer", 7, 12},
		player{"bot one", 0, -1},
		player{"bot two", 0, 99999},
		player{"late", 0, 0.5},
	)
	cs16Rules := rulesRes(rulesN(30, "mp_"))

	bigRules := rulesRes(rulesN(60, "sv_long_rule_name_"))

	cssInfo := sourceInfo(info{
		protocol: 7, name: "CSS", mapName: "cs_office", folder: "cstrike", game: "Counter-Strike: Source",
		appID: 240, players: 3, maxPlayers: 24, server: 'd', env: 'w', vac: 1, version: "1.0.0.34", edf: 0x80, gamePort: 27015,
	})
	cssPlayers := playersRes(
		player{"one", 1, 10}, player{"two", 2, 20}, player{"three", 3, 30},
		player{"a name long enough to need a second packet", 4, 40},
	)

	return []scenario{
		{
			Name:        "source_info_flags",
			Description: "CS2 A2S_INFO with every extra-data flag, a padded name, a players challenge and the 'Max Players' filter",
			Args:        []string{"--type=counterstrike2"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, cs2Info),
				ex(playersReq(0xFFFFFFFF), challengeRes(key1)),
				ex(playersReq(key1), cs2Players),
			},
		},
		{
			Name:        "info_challenge",
			Description: "A2S_INFO answered with a challenge (Valve's 2020 change), then the key reused for players",
			Args:        []string{"--type=counterstrike2"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, challengeRes(key2)),
				ex(withKey(infoReq, key2), cs2Info),
				ex(playersReq(key2), playersRes()),
			},
		},
		{
			Name: "goldsrc_split",
			Description: "CS 1.6 (protocol 48): stale 0x6D reply ignored, Windows-1251 names, " +
				"players and rules in GoldSrc split format arriving out of order, bot by time -1",
			Args: []string{"--type=counterstrike16", "--requestRules"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, cs16OldInfo, cs16Info),
				ex(playersReq(0xFFFFFFFF), challengeRes(key1)),
				ex(playersReq(key1), reorder(goldsrcSplit(0x2a, cs16Players, 3), 2, 0, 1)...),
				ex(rulesReq(key1), goldsrcSplit(0x2b, cs16Rules, 2)...),
			},
		},
		{
			Name:        "source_split_bzip",
			Description: "Source split rules reply compressed with bzip2, with size and CRC32",
			Args:        []string{"--type=counterstrike2", "--requestRules"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, cs2Info),
				ex(playersReq(0xFFFFFFFF), cs2Players),
				ex(rulesReq(0xFFFFFFFF), sourceSplitBzip(0x80000007, bigRules, 3)...),
			},
		},
		{
			Name:        "source_split_protocol7",
			Description: "CS:Source protocol 7 split reply without the packet size field",
			Args:        []string{"--type=css"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, cssInfo),
				ex(playersReq(0xFFFFFFFF), sourceSplit(0x00000005, cssPlayers, 2, false)...),
			},
		},
		{
			Name:        "goldsrc_legacy",
			Description: "pre-2013 GoldSrc 'details' reply (0x6D) with mod info, 'players' and raw 'rules'",
			Args:        []string{"--type=counterstrike15", "--requestRules"},
			Exchanges: []fixture.Exchange{
				ex([]byte("\xff\xff\xff\xffdetails"), pkt().header().u8(0x6D).
					str("127.0.0.1:27015").str("Legacy").str("de_aztec").str("cstrike").str("Counter-Strike").
					u8(4).u8(20).u8(47).u8('d').u8('l').u8(1).u8(1).
					str("http://mod.example").str("http://dl.example").u8(0).u32(15).u32(184000000).u8(0).u8(1).b),
				ex([]byte("\xff\xff\xff\xffplayers"), playersRes(player{"old timer", 5, 1500})),
				ex([]byte("\xff\xff\xff\xffrules"), rulesRes(rulesN(3, "sv_"))),
			},
		},
		{
			Name:        "players_timeout",
			Description: "server that answers info but never the player request",
			Args:        []string{"--type=counterstrike2", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, cs2Info),
				ex(playersReq(0xFFFFFFFF)),
			},
		},
		{
			Name:        "rust_tags",
			Description: "Rust reports player counts in mp/cp tags",
			Args:        []string{"--type=rust"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, sourceInfo(info{
					protocol: 17, name: "Rust", mapName: "Procedural Map", folder: "rust", game: "Rust",
					appID: 0xFFFF & 252490, server: 'd', env: 'l', vac: 1, version: "2501",
					edf: 0x20 | 0x01, tags: "mp100,cp42,biweekly,v2501", gameID: 252490,
				})),
				ex(playersReq(0xFFFFFFFF), playersRes(player{"rustacean", 0, 77.5})),
			},
		},
		{
			Name:        "squad_password_rule",
			Description: "Squad (app 393380): rules are always requested and Password_b sets password",
			Args:        []string{"--type=protocol-valve"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, sourceInfo(info{
					protocol: 17, name: "Squad", mapName: "Narva", folder: "squad", game: "Squad",
					appID: 0xFFFF & 393380, players: 1, maxPlayers: 100, server: 'd', env: 'l', version: "8.0",
					edf: 0x01, gameID: 393380,
				})),
				ex(playersReq(0xFFFFFFFF), playersRes(player{"squaddie", 0, 5})),
				ex(rulesReq(0xFFFFFFFF), rulesRes([][2]string{{"Password_b", "true"}, {"GameMode_s", "RAAS"}})),
			},
		},
		{
			Name:        "the_ship",
			Description: "The Ship (app 2400) adds mode, witnesses and duration to A2S_INFO",
			Args:        []string{"--type=theship", "--requestPlayers=false"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, sourceInfo(info{
					protocol: 7, name: "Ship", mapName: "batavier", folder: "ship", game: "The Ship",
					appID: 2400, players: 2, maxPlayers: 16, server: 'd', env: 'w', ship: []byte{1, 3, 10}, version: "1.0.0.4",
				})),
			},
		},
		{
			Name:        "player_odd_floats",
			Description: "player times that are NaN, infinite or negative zero, mixed with normal ones",
			Args:        []string{"--type=counterstrike2"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, cs2Info),
				ex(playersReq(0xFFFFFFFF), playersRes(
					player{"normal", 1, 30},
					player{"nan", 2, math.Float32frombits(0x7fc00000)},
					player{"inf", 3, float32(math.Inf(1))},
					player{"neginf", 4, float32(math.Inf(-1))},
					player{"negzero", 5, float32(math.Copysign(0, -1))},
					player{"tiny", 6, 1e-7},
					player{"huge", 7, 3e38},
				)),
			},
		},
		{
			Name:        "challenge_key_zero",
			Description: "a challenge key of 0 counts as new, like node-gamedig's initial empty key",
			Args:        []string{"--type=counterstrike2"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, challengeRes(0)),
				ex(infoReq, cs2Info),
				ex(playersReq(0xFFFFFFFF), playersRes()),
			},
		},
		{
			Name:        "info_timeout",
			Description: "no reply at all",
			Args:        []string{"--type=counterstrike16", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{ex(infoReq)},
		},
		{
			Name:        "challenge_loop",
			Description: "a server that keeps issuing new challenge keys",
			Args:        []string{"--type=counterstrike2"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, challengeRes(key1)),
				ex(withKey(infoReq, key1), challengeRes(key2)),
				ex(withKey(infoReq, key2), challengeRes(key3)),
			},
		},
	}
}

func ex(req []byte, responses ...[]byte) fixture.Exchange {
	e := fixture.Exchange{Request: req, Responses: []fixture.Hex{}}
	for _, r := range responses {
		e.Responses = append(e.Responses, r)
	}
	return e
}

type builder struct{ b []byte }

func pkt() *builder                       { return &builder{} }
func (p *builder) header() *builder       { return p.i32(-1) }
func (p *builder) u8(v byte) *builder     { p.b = append(p.b, v); return p }
func (p *builder) u16(v uint16) *builder  { p.b = binary.LittleEndian.AppendUint16(p.b, v); return p }
func (p *builder) u32(v uint32) *builder  { p.b = binary.LittleEndian.AppendUint32(p.b, v); return p }
func (p *builder) i32(v int32) *builder   { return p.u32(uint32(v)) }
func (p *builder) u64(v uint64) *builder  { p.b = binary.LittleEndian.AppendUint64(p.b, v); return p }
func (p *builder) f32(v float32) *builder { return p.u32(math.Float32bits(v)) }
func (p *builder) str(s string) *builder  { p.b = append(append(p.b, s...), 0); return p }

type info struct {
	protocol                  byte
	name, mapName, folder     string
	game                      string
	appID                     uint16
	players, maxPlayers, bots byte
	server, env               byte
	password, vac             byte
	ship                      []byte
	version                   string
	edf                       byte
	gamePort                  uint16
	steamID                   uint64
	tvPort                    uint16
	tvName                    string
	tags                      string
	gameID                    uint64
}

func sourceInfo(i info) []byte {
	p := pkt().header().u8(0x49).u8(i.protocol).str(i.name).str(i.mapName).str(i.folder).str(i.game).
		u16(i.appID).u8(i.players).u8(i.maxPlayers).u8(i.bots).u8(i.server).u8(i.env).u8(i.password).u8(i.vac)
	p.b = append(p.b, i.ship...)
	p.str(i.version).u8(i.edf)
	if i.edf&0x80 != 0 {
		p.u16(i.gamePort)
	}
	if i.edf&0x10 != 0 {
		p.u64(i.steamID)
	}
	if i.edf&0x40 != 0 {
		p.u16(i.tvPort).str(i.tvName)
	}
	if i.edf&0x20 != 0 {
		p.str(i.tags)
	}
	if i.edf&0x01 != 0 {
		p.u64(i.gameID)
	}
	return p.b
}

type player struct {
	name  string
	score int32
	time  float32
}

func playersRes(players ...player) []byte {
	p := pkt().header().u8(0x44).u8(byte(len(players)))
	for i, pl := range players {
		p.u8(byte(i)).str(pl.name).i32(pl.score).f32(pl.time)
	}
	return p.b
}

func rulesN(n int, prefix string) [][2]string {
	out := make([][2]string, n)
	for i := range out {
		out[i] = [2]string{prefix + string(rune('a'+i%26)) + string(rune('a'+i/26)), "value " + string(rune('A'+i%26))}
	}
	return out
}

func rulesRes(rules [][2]string) []byte {
	p := pkt().header().u8(0x45).u16(uint16(len(rules)))
	for _, r := range rules {
		p.str(r[0]).str(r[1])
	}
	return p.b
}

func chunks(b []byte, n int) [][]byte {
	size := (len(b) + n - 1) / n
	var out [][]byte
	for i := 0; i < len(b); i += size {
		out = append(out, b[i:min(i+size, len(b))])
	}
	return out
}

func goldsrcSplit(uid uint32, full []byte, n int) [][]byte {
	parts := chunks(full, n)
	var out [][]byte
	for i, c := range parts {
		p := pkt().i32(-2).u32(uid).u8(byte(i<<4 | len(parts)))
		out = append(out, append(p.b, c...))
	}
	return out
}

func sourceSplit(uid uint32, full []byte, n int, withSize bool) [][]byte {
	parts := chunks(full, n)
	var out [][]byte
	for i, c := range parts {
		p := pkt().i32(-2).u32(uid).u8(byte(len(parts))).u8(byte(i))
		if withSize {
			p.u16(1248)
		}
		out = append(out, append(p.b, c...))
	}
	return out
}

func sourceSplitBzip(uid uint32, full []byte, n int) [][]byte {
	cmd := exec.Command("bzip2", "-c", "-9")
	cmd.Stdin = bytes.NewReader(full)
	compressed, err := cmd.Output()
	if err != nil {
		panic("bzip2: " + err.Error())
	}
	parts := chunks(compressed, n)
	var out [][]byte
	for i, c := range parts {
		p := pkt().i32(-2).u32(uid).u8(byte(len(parts))).u8(byte(i)).u16(1248)
		if i == 0 {
			p.u32(uint32(len(full))).u32(crc32.ChecksumIEEE(full))
		}
		out = append(out, append(p.b, c...))
	}
	return out
}

func reorder(parts [][]byte, order ...int) [][]byte {
	out := make([][]byte, 0, len(order))
	for _, i := range order {
		out = append(out, parts[i])
	}
	return out
}
