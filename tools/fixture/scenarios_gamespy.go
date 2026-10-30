package main

import (
	"encoding/binary"
	"strings"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

func init() { scenarioSets = append(scenarioSets, gamespyScenarios) }

func gamespyScenarios() []scenario {
	return append(append(gamespy1Scenarios(), gamespy2Scenarios()...), gamespy3Scenarios()...)
}

// gs1 builds a GameSpy 1 reply from alternating keys and values.
func gs1(kv ...string) []byte {
	var b strings.Builder
	for _, s := range kv {
		b.WriteString(`\` + s)
	}
	return []byte(b.String())
}

func gamespy1Scenarios() []scenario {
	status := []byte(`\status\xserverquery`)

	bfInfo := gs1("gamename", "bfield1942", "gamever", "v1.612", "location", "0",
		"hostname", "  [BF] Wake Island 24/7 \x80 ", "hostport", "14567", "mapname", "Wake Island",
		"gametype", "conquest", "gamemode", "openplaying", "numplayers", "3", "maxplayers", "64",
		"password", "0", "tickets1", "150", "unpure", "1",
		"teamname_0", "Axis", "teamname_1", "Allies", "teamscore_0", "120", "queryid", "4567.1")
	bfPlayers := gs1("playername_0", "Sgt. M\xfcller", "score_0", "12", "deaths_0", "3", "ping_0", "45",
		"team_0", "1", "keyhash_0", "a1b2c3", "playername_1", "J\xf3zef \x81\x8d", "score_1", "-2",
		"team_1", "2", "queryid", "4567.2")
	bfFinal := gs1("playername_2", "Rookie", "team_2", "2", "ping_2", "999",
		"playername_3", "Left long ago", "team_3", "1", "final", "", "queryid", "4567.3")

	return []scenario{
		{
			Name: "gamespy1_bf1942",
			Description: "Battlefield 1942 reply in three parts out of order, plus a duplicate part and one with " +
				"another query ID (both ignored); team names with bfield1942's off-by-one team IDs, " +
				"Windows-1252 names and a stale player beyond numplayers",
			Args: []string{"--type=battlefield1942"},
			Exchanges: []fixture.Exchange{
				ex(status, bfFinal, bfInfo, gs1("hostname", "other query", "queryid", "9999.2"), bfInfo, bfPlayers),
			},
		},
		{
			Name: "gamespy1_types",
			Description: "Unreal Tournament single-part reply: lowercased duplicate keys, true/false/number " +
				"conversion with JavaScript Number() edge cases, keys that stay strings, a __PROTO__ key, " +
				"numeric team IDs without team names and an empty team",
			Args: []string{"--type=unrealtournament"},
			Exchanges: []fixture.Exchange{
				ex(status, gs1(
					"hostname", "UT99 \x93Quoted\x94 \x9f\xff", "mapname", "DM-Deck16][", "maxplayers", "16",
					"numplayers", "2", "hostport", "7777", "password", "True", "gamever", "451",
					"minnetver", "432", "AdminName", "Admin", "AdminEMail", "admin@example.com",
					"gametype", "DeathMatch", "gamemode", "openplaying", "changelevels", "FALSE",
					"tournament", "false", "fraglimit", " 30 ", "timelimit", "0x1E", "goalteamscore", "1e3",
					"balanceteams", "Infinity", "worldlog", "", "botskill", "Skilled", "website", "123",
					"listenserver", "-0", "maxteams", ".5", "minplayers", "1e400", "Hostname", "renamed",
					"name_tag_distance", "true",
					"player_0", "Neo", "frags_0", "5", "ping_0", " 44", "team_0", "255", "mesh_0", "Male Commando",
					"ngsecret_0", "true", "player_1", "Trinity", "frags_1", "-1", "ping_1", "0", "team_1", "",
					"__PROTO__", "x", "queryid", "77.1", "final", "")),
			},
		},
		{
			Name: "gamespy1_missing_fields",
			Description: "Operation Flashpoint reply without hostname, gamever or maxplayers; a non-numeric " +
				"numplayers, string team names, players listed out of ID order and a key with a non-numeric ID",
			Args: []string{"--type=ofcwc"},
			Exchanges: []fixture.Exchange{
				ex(status, gs1("gamename", "opflashr", "mapname", "Intro", "numplayers", "abc",
					"player_1", "Alpha", "team_1", "Red", "score_1", "10",
					"player_0", "Bravo", "team_0", "", "score_0", "x", "deaths_0.5", "1",
					"player_x", "nobody", "queryid", "5.1", "final", "")),
			},
		},
		{
			Name: "gamespy1_odd_counts",
			Description: "Unreal: fractional maxplayers and numplayers (which keeps only the first player) and a " +
				"negative game port, printed as node-gamedig prints JavaScript numbers",
			Args: []string{"--type=unreal"},
			Exchanges: []fixture.Exchange{
				ex(status, gs1("hostname", "Odd", "maxplayers", "12.5", "numplayers", "1.5", "hostport", "-5",
					"player_0", "first", "player_1", "second", "queryid", "3.1", "final", "")),
			},
		},
		{
			Name:        "gamespy1_no_queryid",
			Description: "a final part without queryid is part #0, which node-gamedig never treats as complete",
			Args:        []string{"--type=vietcong", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				ex(status, gs1("hostname", "Vietcong", "mapname", "Hue", "final", "")),
			},
		},
		{
			Name:        "gamespy1_timeout",
			Description: "no reply at all",
			Args:        []string{"--type=mohaa", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{ex(status)},
		},
	}
}

// gs2Request and gs2Reply frame GameSpy 2 packets; the ping ID is 1.
func gs2Request(a, b, c byte) []byte { return []byte{0xfe, 0xfd, 0, 0, 0, 0, 1, a, b, c} }

func gs2Reply(body ...string) []byte {
	return append([]byte{0, 0, 0, 0, 1}, strings.Join(body, "")...)
}

// z NUL-terminates each string.
func z(s ...string) string { return strings.Join(s, "\x00") + "\x00" }

func gamespy2Scenarios() []scenario {
	infoReq, playersReq, teamsReq := gs2Request(0xff, 0, 0), gs2Request(0, 0xff, 0), gs2Request(0, 0, 0xff)

	armaInfo := gs2Reply(z("hostname", " ARMA \xc4rmed Server ", "gamever", "1.14", "hostport", "2302",
		"password", "0", "gamename", "armedass", "gametype", "CTI", "mapname", "Sara",
		"numplayers", "3", "maxplayers", "32abc", "mod", "RHS;CA", "teams", "overwritten"), "\x00")

	return []scenario{
		{
			Name:        "gamespy2_basic",
			Description: "ARMA: Armed Assault info, players and teams with row counts, empty scores and a game port",
			Args:        []string{"--type=aaa"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, armaInfo),
				ex(playersReq, gs2Reply("\x00\x03", z("player_", "score_", "deaths_", "ping_", "team_", ""),
					z("\xc5ngstr\xf6m", "10", "5", "50", "1"),
					z("Bob", "", "0", "120", "2"),
					z("Charlie", "-4", "x", "0x1f", "1"))),
				ex(teamsReq, gs2Reply("\x00\x02", z("team_t", "score_t", "tickets_t", ""),
					z("BLUFOR", "15", "300"), z("OPFOR", "7", ""))),
			},
		},
		{
			Name: "gamespy2_armygame",
			Description: "America's Army: replies with the wrong header or ping ID are ignored, Unreal 2 colour " +
				"codes are stripped (not from gamever or teams), and the player count byte is missing",
			Args: []string{"--type=americasarmy"},
			Exchanges: []fixture.Exchange{
				ex(infoReq,
					[]byte{0, 0, 0, 0, 2, 'x', 0},
					[]byte{1, 0, 0, 0, 1, 'y', 0},
					gs2Reply(z("hostname", "\x1b\xff\x40\x40[AA] \x1b\x01\x02\x03Official\x01 \x1b\n12",
						"gamename", "armygame", "gamever", "2.8.5\x1b\x10\x20\x30", "mapname", "\x1bABCBridge",
						"numplayers", "2", "maxplayers", "26", "password", "True", "tour", "\x1b\x11\x22", "hostport", "0"))),
				ex(playersReq, gs2Reply("\x00", z("player_", "score_", "ping_", "honor_", ""),
					z("\x1b\x80\x80\x80Sniper\x05", "8", "80", "42"),
					"Truncated\x00 7")),
				ex(teamsReq, gs2Reply("\x00\x00")),
			},
		},
		{
			Name: "gamespy2_missing_fields",
			Description: "SWAT 4 info without name, ports or counts; non-numeric ping; a row cut short by an " +
				"empty first value; an empty teams reply",
			Args: []string{"--type=swat4"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, gs2Reply(z("gamename", "swat4", "", ""))),
				ex(playersReq, gs2Reply("\x00\x00", z("player", "score_", "ping_", ""),
					z("Lone", "", "abc"), z("", "1", "2"), z("Never", "3", "4"))),
				ex(teamsReq, gs2Reply()),
			},
		},
		{
			Name:        "gamespy2_armygame_colors_kept",
			Description: "America's Army with --stripColors=false keeps the colour codes",
			Args:        []string{"--type=americasarmy2", "--stripColors=false"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, gs2Reply(z("hostname", "\x1b\xff\x40\x40[AA] Server\x01", "gamename", "armygame",
					"mapname", "\x1bABCBridge", "numplayers", "1"), "\x00")),
				ex(playersReq, gs2Reply("\x00\x01", z("player_", ""), z("\x1b\x80\x80\x80Sniper"))),
				ex(teamsReq, gs2Reply("\x00\x00")),
			},
		},
		{
			Name:        "gamespy2_odd_counts",
			Description: "Breed: a non-numeric maxplayers prints null, numplayers \"1e3\" parses as 1, hex game port",
			Args:        []string{"--type=breed"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, gs2Reply(z("hostname", "Breed", "maxplayers", "lots", "numplayers", "1e3", "hostport", "0x1F90"), "\x00")),
				ex(playersReq, gs2Reply("\x00\x00")),
				ex(teamsReq, gs2Reply("\x00\x00")),
			},
		},
		{
			Name:        "gamespy2_players_timeout",
			Description: "info answered but the player request never is, which fails the query",
			Args:        []string{"--type=halo", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				ex(infoReq, gs2Reply(z("hostname", "Halo", "numplayers", "0"), "\x00")),
				ex(playersReq),
			},
		},
	}
}

// gs3Request builds a GameSpy 3 request for session ID 1.
func gs3Request(typ byte, challenge []byte, payload ...byte) []byte {
	b := []byte{0xfe, 0xfd, typ, 0, 0, 0, 1}
	return append(append(b, challenge...), payload...)
}

func gs3Challenge(key string) []byte { return append([]byte{9, 0, 0, 0, 1}, z(key)...) }

// gs3Split frames one status packet: number id, with the last flag.
func gs3Split(id byte, last bool, body ...string) []byte {
	if last {
		id |= 0x80
	}
	b := append([]byte{0, 0, 0, 0, 1}, "splitnum\x00"...)
	b = append(b, id, 1)
	return append(b, strings.Join(body, "")...)
}

func be32(v int32) []byte { return binary.BigEndian.AppendUint32(nil, uint32(v)) }

func gamespy3Scenarios() []scenario {
	handshake := gs3Request(9, []byte{0, 0, 0, 0})
	full := []byte{0xff, 0xff, 0xff, 0x01}

	crysis0 := gs3Split(0, false,
		z("hostname", "Crysis Wars \xa9 Server", "gamever", "1.5.1", "hostport", "64087", "mapname", "Mesa",
			"gametype", "PowerStruggle", "numplayers", "3", "maxplayers", "32", "password", "0", ""),
		"\x01", "player_\x00\x00", z("Nomad", "Psycho", ""),
		"score_\x00\x00", z("10", "-3", ""))
	crysis1 := gs3Split(1, false,
		"ping_\x00\x00", z("50", "70", ""),
		"team_\x00\x00", z("1", "2", ""),
		"player_\x00\x02", z("Prophet", ""))
	crysis2 := gs3Split(2, true,
		"score_\x00\x02", z("7", ""),
		"deaths_\x00\x00", z("4", "1", "0", ""),
		"team_t\x00\x00", z("US", "NK", ""),
		"score_t\x00\x00", z("1500", "1200", ""))

	return []scenario{
		{
			Name: "gamespy3_challenge",
			Description: "Crysis Wars: negative challenge key, a stray packet of the wrong type, three status " +
				"packets out of order with player columns split across them and team columns",
			Args: []string{"--type=crysiswars"},
			Exchanges: []fixture.Exchange{
				ex(handshake, gs3Challenge("-12345678")),
				ex(gs3Request(0, be32(-12345678), full...), gs3Challenge("1"), crysis2, crysis0, crysis1),
			},
		},
		{
			Name: "gamespy3_no_challenge",
			Description: "Battlefield 2 sends challenge 0, meaning none; one packet with servername instead of " +
				"hostname, UT3-style placeholder values, a non-numeric pid and fields without an item type",
			Args: []string{"--type=battlefield2"},
			Exchanges: []fixture.Exchange{
				ex(handshake, gs3Challenge("0")),
				ex(gs3Request(0, nil, full...), gs3Split(0, true,
					z("servername", " BF2 \x96 Karkand ", "mapname", "Strike at Karkand", "maxplayers", "64abc",
						"password", "1", "bots", "p1073741827", "p268435704", "4", "bf2_ranked", "1", ""),
					"\x00\x02",
					"player_\x00\x00", z("A", "B", ""),
					"pid_\x00\x00", z("x", " 12", ""),
					"AIBot_\x00\x01", z("1", ""),
					"deaths\x00\x00", z("3", ""))),
			},
		},
		{
			Name:        "gamespy3_missing_packet",
			Description: "S.T.A.L.K.E.R.: two packets numbered 5 and 1 (last) leave packet #0 missing",
			Args:        []string{"--type=stalker"},
			Exchanges: []fixture.Exchange{
				ex(handshake, gs3Challenge("123456")),
				ex(gs3Request(0, be32(123456), full...),
					gs3Split(5, false, z("hostname", "x", "")), gs3Split(1, true, z("mapname", "y", ""))),
			},
		},
		{
			Name:        "gamespy3_challenge_range",
			Description: "a challenge key above the int32 range, which node-gamedig cannot encode, fails the query",
			Args:        []string{"--type=crysis2"},
			Exchanges: []fixture.Exchange{
				ex(handshake, gs3Challenge("3000000000")),
			},
		},
		{
			Name:        "gamespy3_inherited_item_type",
			Description: "an item type named like an Object.prototype member, which node-gamedig fails on",
			Args:        []string{"--type=prb2"},
			Exchanges: []fixture.Exchange{
				ex(handshake, gs3Challenge("0")),
				ex(gs3Request(0, nil, full...), gs3Split(0, true, z("hostname", "PR", ""), "x_constructor\x00\x00", z("v", ""))),
			},
		},
		{
			Name:        "gamespy3_timeout",
			Description: "challenge answered but the status request is not",
			Args:        []string{"--type=battlefield2142", "--socketTimeout=300"},
			Exchanges: []fixture.Exchange{
				ex(handshake, gs3Challenge("42")),
				ex(gs3Request(0, be32(42), full...)),
			},
		},
	}
}
