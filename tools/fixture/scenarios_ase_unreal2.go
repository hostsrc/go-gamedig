package main

import (
	"encoding/binary"
	"unicode/utf16"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

func init() { scenarioSets = append(scenarioSets, aseUnreal2Scenarios) }

func aseUnreal2Scenarios() []scenario {
	aseReq := []byte("s")
	ue2Req := func(typ byte) []byte { return []byte{0x79, 0, 0, 0, typ} }
	const ue2Timeout = "--socketTimeout=300"

	ue2Rules := ue2Packet(1).
		ue2(ue2Str("AdminName"), ue2Str("Admin \x1b\xff\xff\x01Bob")).
		ue2(ue2Str("Mutator"), ue2Str("MutInstaGib")).
		ue2(ue2Str("mutator"), ue2Str("MutBigHead")).
		ue2(ue2Str("ServerMode"), ue2Str("dedicated")).
		ue2(ue2Str("GamePassword"), ue2Str("False")).
		ue2(ue2Str("UTComp_Version"), ue2Str("UTCompv17a"))
	ue2Rules2 := ue2Packet(1).
		ue2(ue2Str("ServerMode"), ue2Str("public")).
		ue2(ue2Str("MaxSpectators"), ue2Str("2")).
		ue2(ue2Str(""), ue2Str("")).
		ue2(ue2Str("Translocator"), ue2Str("True"))

	return []scenario{
		{
			Name: "ase_mta",
			Description: "MTA:SA All-Seeing Eye reply after a non-EYE1 datagram: rules (one shadowing gamename, " +
				"one named __proto__), players with every field flag, missing fields, UTF-8 and invalid UTF-8 names, " +
				"and numeric fields parseInt reads as NaN, hex and negative",
			Args: []string{"--type=gtasamta"},
			Exchanges: []fixture.Exchange{
				ex(aseReq,
					[]byte("EYE2not this one"),
					aseReply(
						"mta", "22003", "  Padded MTA ünï server ", "Freeroam", "San Andreas", "1.6", "0", "4", "32",
					).
						rules("gamename", "mta-sa", "weather", "sunny", "__proto__", "x", "ver", "1.6.0-9.22").
						end().
						player(0x3f, "Alice", "Grove", "cj", "12", "45", "300").
						player(0x01|0x08, "\xc3\x28bad\xe2\x82", "", "", "-7", "", "").
						player(0x08|0x10|0x20, "", "", "", "score?", "  0x1F", "-0").
						player(0x01|0x02|0x04, "Zoë 🎮", "", "", "", "", "").
						player(0, "", "", "", "", "", "").b,
				),
			},
		},
		{
			Name:        "ase_empty_password",
			Description: "Far Cry with a password, game port 0 (connect falls back to the query port), no rules and no players",
			Args:        []string{"--type=farcry"},
			Exchanges: []fixture.Exchange{
				ex(aseReq, aseReply("farcry", "0", "\tLocked\t", "ASSAULT", "mp_steam", "1.4", "1", "0", "16").end().b),
			},
		},
		{
			Name: "ase_truncated",
			Description: "Call of Juarez reply with zero-length string bytes, a hex game port, " +
				"and a rule value cut off by the end of the datagram",
			Args: []string{"--type=coj"},
			Exchanges: []fixture.Exchange{
				ex(aseReq, (&aseBuilder{b: []byte("EYE1")}).
					str("coj").str("0x5DC0").str("Juarez").raw(0).str("Hope").raw(0).str("").str("2").str("12").
					rules("mode", "posse", "motd").raw(5, 'l', 'o', 'n').b),
			},
		},
		{
			Name:        "ase_missing_fields",
			Description: "a reply that ends after the map: version, password and the player counts are missing",
			Args:        []string{"--type=gtavcmta"},
			Exchanges: []fixture.Exchange{
				ex(aseReq, (&aseBuilder{b: []byte("EYE1")}).str("mta").str("22003").str("VC").str("DM").str("Vice City").b),
			},
		},
		{
			Name:        "ase_odd_counts",
			Description: "player counts parseInt can't read or reads beyond 2^53, and a player flagged with every field but sent with none",
			Args:        []string{"--type=xpandrally"},
			Exchanges: []fixture.Exchange{
				ex(aseReq, aseReply("xr", "28015", "Rally", "race", "Desert", "1.0", "0", "many", "123456789012345678901234").
					end().raw(0x3f).b),
			},
		},
		{
			Name:        "ase_timeout",
			Description: "no All-Seeing Eye reply",
			Args:        []string{"--type=aoe2", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{ex(aseReq)},
		},
		{
			Name: "unreal2_ut2004",
			Description: "UT2004: colour codes and control characters stripped, rules over two datagrams " +
				"with mutators, a repeated key, an empty pair and an inverted GamePassword, UTComp version, " +
				"a datagram of another type ignored, and players, bots and team scores over two datagrams " +
				"with UCS-2 (extra 0x01 byte), Windows-1252 and undefined Windows-1252 bytes",
			Args: []string{"--type=unrealtournament2004", ue2Timeout},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), ue2Packet(0).u32(7).ue2(ue2Str("127.0.0.1")).u32(7777).u32(7778).
					ue2(ue2Str(" \x1b\xff\x01\x01Red\x1b\x01\xff\x01 \x07Team\x1b\xffServer ")).
					ue2(ue2Str("DM-Rankin")).ue2(ue2Str("xDeathMatch")).u32(3).u32(16).
					u32(0).u32(0x1d).ue2(ue2Str("\x1b\x01\x01\x01Masterful")).b),
				ex(ue2Req(1), ue2Packet(2).u32(1).b, ue2Rules.b, ue2Rules2.b),
				ex(ue2Req(2),
					ue2Packet(2).
						u32(0).ue2(ue2Str("Red Team")).u32(0).i32(42).u32(0).
						u32(0).ue2(ue2Str("Blue Team")).u32(0).i32(-3).u32(0).
						u32(11).ue2(ue2UCS2(true, "Ünïcødé \x1b\u00ff\u0080\u0080!")).u32(48).i32(15).u32(1001).b,
					ue2Packet(2).
						u32(12).ue2(ue2Str("Caf\xe9 \x80\x81\x8d\x90\x9d")).u32(120).i32(-2).u32(0).
						u32(13).ue2(ue2Str("Bot Gorge")).u32(0).i32(7).u32(0).b,
				),
			},
		},
		{
			Name: "unreal2_killingfloor",
			Description: "Killing Floor (KillingFloorMut mutator): wave, ping, flags and skill level after the info " +
				"fields; players are requested even with --requestPlayers=false",
			Args: []string{"--type=killingfloor", ue2Timeout, "--requestPlayers=false"},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), ue2Packet(0).u32(1).ue2(ue2Str("10.0.0.5")).u32(7707).u32(7708).
					ue2(ue2Str("KF Server")).ue2(ue2Str("KF-BioticsLab")).ue2(ue2Str("KFGameType")).
					u32(1).u32(6).u32(4).u32(10).u32(25).u32(0x10).ue2(ue2Str("Hard")).b),
				ex(ue2Req(1), ue2Packet(1).
					ue2(ue2Str("Mutator"), ue2Str("KillingFloorMut")).
					ue2(ue2Str("GamePassword"), ue2Str("True")).b),
				ex(ue2Req(2), ue2Packet(2).u32(5).ue2(ue2Str("Survivor")).u32(60).i32(150).u32(0).b),
			},
		},
		{
			Name: "unreal2_kf_server_version",
			Description: "Killing Floor detected by 'Server Version' 1065, with the info reply cut short " +
				"so the extra fields read as zero",
			Args: []string{"--type=killingfloor", ue2Timeout},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), ue2Packet(0).u32(1).ue2(ue2Str("10.0.0.6")).u32(7707).u32(7708).
					ue2(ue2Str("KF 1065")).ue2(ue2Str("KF-Farm")).ue2(ue2Str("KFGameType")).
					u32(0).u32(6).u32(9).b),
				ex(ue2Req(1), ue2Packet(1).ue2(ue2Str("Server Version"), ue2Str("1065")).b),
				ex(ue2Req(2)),
			},
		},
		{
			Name: "unreal2_old_engine",
			Description: "Red Orchestra without the later flags and skill fields, GamePassword True (no password), " +
				"a rule named __proto__, 'Num trader weapons' empty (not Killing Floor), and no players reply",
			Args: []string{"--type=redorchestra", ue2Timeout},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), ue2Packet(0).u32(3).ue2(ue2Str("192.0.2.1")).u32(7758).u32(7759).
					ue2(ue2Str("RO Server")).ue2(ue2Str("RO-Arad")).ue2(ue2Str("ROTeamGame")).
					u32(0).u32(32).u32(55).u8(1).b),
				ex(ue2Req(1), ue2Packet(1).
					ue2(ue2Str("GamePassword"), ue2Str("True")).
					ue2(ue2Str("__proto__"), ue2Str("polluted")).
					ue2(ue2Str("Num trader weapons"), ue2Str("")).b),
				ex(ue2Req(2)),
			},
		},
		{
			Name: "unreal2_ucs2",
			Description: "UCS-2 strings: a lone surrogate, a colour code, a trailing NUL, a zero length, " +
				"an ESC too close to the end to start a colour code, a length-only (non-UCS-2) string " +
				"read up to its NUL, and a UCS-2 string cut off by the end of the datagram",
			Args: []string{"--type=unrealtournament2003", ue2Timeout},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), ue2Packet(0).u32(9).ue2(ue2UCS2(false, "ÿ1.2.3.4")).u32(7757).u32(7758).
					ue2(ue2UCSUnits([]uint16{'S', 0xD800, 'r', 0x1b, 0xff, 0, 0, 'v', 0x263A, 0})).
					ue2(ue2UCS2(false, "")).
					ue2(ue2UCS2(false, "CTF\x1b\x01")).
					u32(0).u32(8).u32(2).u32(0).
					ue2([]byte{0x86, 'H', 0, 'a', 0, 'r'}).b),
				ex(ue2Req(1), ue2Packet(1).
					ue2([]byte{3, 'a', 'b', 'c', 'd', 'e', 0}, ue2Str("long")).
					ue2([]byte{0}, ue2Str("empty key")).b),
				ex(ue2Req(2)),
			},
		},
		{
			Name: "unreal2_short_datagram",
			Description: "a datagram too short to carry a type counts as an empty info reply, " +
				"as in node-gamedig, leaving an empty server",
			Args: []string{"--type=protocol-unreal2", ue2Timeout},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), []byte{0x80, 0, 0}),
				ex(ue2Req(1), ue2Packet(1).b),
				ex(ue2Req(2), ue2Packet(2).b),
			},
		},
		{
			Name:        "unreal2_colors_kept",
			Description: "--stripColors=false keeps colour codes and control characters; the trailing NUL is still dropped",
			Args:        []string{"--type=u2tax", ue2Timeout, "--stripColors=false"},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), ue2Packet(0).u32(2).ue2(ue2Str("10.1.1.1")).u32(7777).u32(7778).
					ue2(ue2Str("\x1b\xff\x01\x01Red\x07 XMP")).ue2(ue2Str("XMP-Garden")).
					ue2(ue2UCS2(false, "XMP\x1b\u00ff\u00ff\u00ffGame")).u32(1).u32(24).u32(30).b),
				ex(ue2Req(1), ue2Packet(1).ue2(ue2Str("Color\x1b\x01\x02\x03Key"), ue2Str("\x01v")).b),
				ex(ue2Req(2), ue2Packet(2).u32(3).ue2(ue2Str("\x1b\x01\xff\x01Green")).u32(80).i32(2).u32(0).b),
			},
		},
		{
			Name:        "unreal2_info_timeout",
			Description: "no reply to the info request",
			Args:        []string{"--type=unrealtournament2004", ue2Timeout},
			Exchanges:   []fixture.Exchange{ex(ue2Req(0))},
		},
		{
			Name:        "unreal2_rules_timeout",
			Description: "info answered, but the required rules request times out",
			Args:        []string{"--type=devastation", ue2Timeout},
			Exchanges: []fixture.Exchange{
				ex(ue2Req(0), ue2Packet(0).u32(1).ue2(ue2Str("1.1.1.1")).u32(7777).u32(7778).
					ue2(ue2Str("Dev")).ue2(ue2Str("DV-Map")).ue2(ue2Str("DVGame")).u32(0).u32(8).u32(0).b),
				ex(ue2Req(1), ue2Packet(2).u32(1).b),
			},
		},
	}
}

// aseBuilder builds an All-Seeing Eye reply. Strings are prefixed by one
// length byte that counts itself.
type aseBuilder struct{ b []byte }

func aseReply(game, port, name, gametype, mapName, version, password, num, max string) *aseBuilder {
	a := &aseBuilder{b: []byte("EYE1")}
	for _, s := range []string{game, port, name, gametype, mapName, version, password, num, max} {
		a.str(s)
	}
	return a
}

func (a *aseBuilder) str(s string) *aseBuilder {
	a.b = append(append(a.b, byte(len(s)+1)), s...)
	return a
}

func (a *aseBuilder) raw(b ...byte) *aseBuilder { a.b = append(a.b, b...); return a }

func (a *aseBuilder) rules(kv ...string) *aseBuilder {
	for _, s := range kv {
		a.str(s)
	}
	return a
}

// end writes the empty key that ends the rules.
func (a *aseBuilder) end() *aseBuilder { return a.str("") }

func (a *aseBuilder) player(flags byte, name, team, skin, score, ping, time string) *aseBuilder {
	a.raw(flags)
	for i, s := range []string{name, team, skin, score, ping, time} {
		if flags&(1<<i) != 0 {
			a.str(s)
		}
	}
	return a
}

// ue2Packet starts an Unreal 2 reply datagram of the given type.
func ue2Packet(typ byte) *builder { return pkt().u8(0x80).u8(0).u8(0).u8(0).u8(typ) }

func (p *builder) ue2(parts ...[]byte) *builder {
	for _, s := range parts {
		p.b = append(p.b, s...)
	}
	return p
}

// ue2Str is a Windows-1252 string: a length byte counting the NUL, the
// bytes and the NUL.
func ue2Str(s string) []byte { return append(append([]byte{byte(len(s) + 1)}, s...), 0) }

// ue2UCS2 is a UCS-2 string of s plus a NUL, optionally with the stray
// 0x01 byte some games add after the length.
func ue2UCS2(extra01 bool, s string) []byte {
	units := append(utf16.Encode([]rune(s)), 0)
	b := ue2UCSUnits(units)
	if extra01 {
		b = append([]byte{b[0], 1}, b[1:]...)
	}
	return b
}

// ue2UCSUnits is a UCS-2 string of exactly the given code units.
func ue2UCSUnits(units []uint16) []byte {
	b := []byte{0x80 | byte(len(units))}
	for _, u := range units {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return b
}
