package main

import (
	"fmt"
	"strings"

	"github.com/hostsrc/go-gamedig/internal/fixture"
)

func init() { scenarioSets = append(scenarioSets, quakeScenarios) }

// quakeReply is a connectionless datagram: four 0xFF bytes, then text.
func quakeReply(parts ...string) []byte {
	return []byte("\xff\xff\xff\xff" + strings.Join(parts, ""))
}

// cvars renders key/value pairs as a backslash-separated info string.
func cvars(kv ...string) string {
	var s strings.Builder
	for _, v := range kv {
		s.WriteString("\\" + v)
	}
	return s.String()
}

func quakeScenarios() []scenario {
	q1Req := []byte("\xff\xff\xff\xffstatus\x00")
	q2Req := []byte("\xff\xff\xff\xffstatus\x00")
	q3Req := []byte("\xff\xff\xff\xffgetstatus\x00")
	hexen2Req := []byte("\xff\xff\xff\xff\xffstatus\n\x00")

	// A busy Call of Duty 4 server: colored names, RGB color codes, bots
	// (ping 0) and a full player list.
	var cod4Players strings.Builder
	for i := range 40 {
		ping := 30 + i*7%200
		if i%9 == 4 {
			ping = 0 // bots answer with ping 0
		}
		fmt.Fprintf(&cod4Players, "%d %d \"^%d[TAG]^7Player%02d\"\n", (i*37)%90-5, ping, i%10, i)
	}
	cod4 := quakeReply("statusResponse\n",
		cvars("_Admin", "^1Admin", "_Email", "admin@example.org", "fs_game", "mods/promod", "g_compassShowEnemies", "0",
			"g_gametype", "sd", "gamename", "Call of Duty 4", "mapname", "mp_crash", "protocol", "6", "shortversion", "1.7",
			"sv_allowAnonymous", "0", "sv_disableClientConsole", "0", "sv_floodprotect", "4",
			"sv_hostname", "  ^1Clan ^7Server ^3#1 ^XFF8800orange^7  ", "sv_maxclients", "24", "sv_maxPing", "0",
			"sv_privateClients", "2", "sv_punkbuster", "1", "sv_pure", "1", "sv_voice", "0", "ui_maxclients", "32",
			"pswrd", "0", "mod", "1", "g_needpass", "0", "version", "^2CoD4 X^7 1.8 linux-i386 build 2017"),
		"\n", cod4Players.String())

	q3a := quakeReply("statusResponse\n",
		cvars("sv_hostname", "^3Q^13^7A ^Xff0000Arena", "mapname", "q3dm17", "sv_maxclients", "16", "g_gametype", "0",
			"fraglimit", "20", "timelimit", "0", "version", "ioq3 1.36_GIT_^1beta linux-x86_64", "g_needpass", "1",
			"gamename", "baseq3", "sv_privateClients", "0"),
		"\n",
		"20 45 \"^1Sarge\"\n",
		"7 0 \"^4Anarki\"\n",
		"-1 120 \"^Xa0b0c0Visor ^7(spec)\"\n")

	return []scenario{
		{
			Name: "quake3_cod4",
			Description: "Call of Duty 4: colored hostname, cvars and player names stripped by default, " +
				"RGB codes, padded name, g_needpass/sv_maxclients copied as strings, 40 players with bots at ping 0",
			Args:      []string{"--type=cod4mw"},
			Exchanges: []fixture.Exchange{ex(q3Req, cod4)},
		},
		{
			Name:        "quake3_colors_kept",
			Description: "Quake 3 Arena with --stripColors=false: color codes stay in the name, cvars and players",
			Args:        []string{"--type=q3a", "--stripColors=false"},
			Exchanges:   []fixture.Exchange{ex(q3Req, q3a)},
		},
		{
			Name: "quake3_iv_clients",
			Description: "both hostname and sv_hostname, maxclients and sv_maxclients, and iv with clients: " +
				"numplayers becomes the clients string and the stripped version cvar wins over iv",
			Args: []string{"--type=protocol-quake3"},
			Exchanges: []fixture.Exchange{ex(q3Req, quakeReply("statusResponse\n",
				cvars("sv_hostname", "ignored", "hostname", "^5Real ^7name", "sv_maxclients", "10", "maxclients", "12",
					"iv", "^1iv-1.0", "clients", "07", "version", "^2v^32", "g_needpass", "true"),
				"\n", "1 10 \"one\"\n", "2 0 \"two\"\n"))},
		},
		{
			Name:        "quake3_iv_without_clients",
			Description: "Urban Terror reply with iv but no clients cvar: node-gamedig then leaves numplayers at 0",
			Args:        []string{"--type=urbanterror"},
			Exchanges: []fixture.Exchange{ex(q3Req, quakeReply("statusResponse\n",
				cvars("sv_hostname", "^7|UrT| ^2Public", "mapname", "ut4_turnpike", "sv_maxclients", "20", "iv", "^14.3.4",
					"g_gametype", "4", "auth", "1"),
				"\n", "15 60 \"^1Red^7Player\"\n", "3 80 \"BluePlayer\"\n"))},
		},
		{
			Name: "quake3_odd_text",
			Description: "Windows-1252 bytes including undefined ones, NBSP padding, color-code edge cases " +
				"(trailing ^, ^ before CR, short ^X), __proto__ and duplicate cvars, a dangling key, and odd player " +
				"lines: unquoted, extra fields, missing ping, hex, signs, huge numbers, empty and split quotes; " +
				"a line starting with NUL ends the list",
			Args: []string{"--type=q3a"},
			Exchanges: []fixture.Exchange{ex(q3Req, quakeReply("statusResponse\n",
				"\\sv_hostname\\\xa0 Caf\xe9 \x80\x81\x8d\x8f\x90\x9d\x9f \xff^\xa0\xa0",
				"\\mapname\\^1q3\xe9dm1",
				"\\__proto__\\polluted\\constructor\\ctor\\dup\\first\\dup\\second",
				"\\edge1\\a^\\edge2\\x^\ry\\edge3\\^Xab\\edge4\\^Xab\rcdefg\\edge5\\^^1z\\edge6\\^X123456789",
				"\\version\\\x93quoted\x94 ^7v1",
				"\\dangling",
				"\n",
				"5 20 bareword\n",
				"1 2 \"named\" \"addr\" extra fields\n",
				"7\n",
				"0x1F 30 \"hex\"\n",
				"-5 -1 \"negative ping\"\n",
				"3 abc \"nan ping\"\n",
				"99999999999999999999999 1e3 \"huge\"\n",
				"+4 +9 \"plus\"\n",
				"  2   3    \"spaced\"   \n",
				"1\t2 3 \"tab\"\n",
				"4 5 \"\"\n",
				"4 5 a\"b c\"d\n",
				"-0 0x0 \"zeroes\"\n",
				"6 7 \"\xc9\xe9\x85 \x80\"\n",
				"\x00 9 9 \"after nul\"\n",
				"8 8 \"never read\"\n"))},
		},
		{
			Name: "quake3_stray_packets",
			Description: "Wolfenstein: Enemy Territory: a datagram without the 0xFF prefix, one with the wrong type, " +
				"one with a longer type, then the reply (no players, no trailing newline) and a late duplicate",
			Args: []string{"--type=wet"},
			Exchanges: []fixture.Exchange{ex(q3Req,
				[]byte("statusResponse\n\\sv_hostname\\nope\n"),
				quakeReply("print\n\\sv_hostname\\wrong type\n"),
				quakeReply("statusResponseX\n\\sv_hostname\\wrong too\n"),
				quakeReply("statusResponse\n", cvars("sv_hostname", "^1ET ^7Server", "mapname", "oasis", "sv_maxclients", "32")),
				quakeReply("statusResponse\n", cvars("sv_hostname", "late duplicate")),
			)},
		},
		{
			Name:        "quake3_header_only",
			Description: "Call of Duty: World at War reply that ends after the header, without a newline",
			Args:        []string{"--type=codwaw"},
			Exchanges:   []fixture.Exchange{ex(q3Req, quakeReply("statusResponse"))},
		},
		{
			Name:        "quake3_timeout",
			Description: "no reply at all",
			Args:        []string{"--type=q3a", "--socketTimeout=300"},
			Exchanges:   []fixture.Exchange{ex(q3Req)},
		},
		{
			Name: "quake2_status",
			Description: "Quake 2 'print' reply: hostname and maxclients, Latin-1 name, colors not stripped, " +
				"players with and without an address, a bot",
			Args: []string{"--type=quake2"},
			Exchanges: []fixture.Exchange{ex(q2Req, quakeReply("print\n",
				cvars("cheats", "0", "deathmatch", "1", "dmflags", "16", "fraglimit", "0", "gamename", "baseq2",
					"hostname", "^1Q2 \xe9 server", "mapname", "q2dm1", "maxclients", "16", "protocol", "34",
					"timelimit", "0", "version", "R1Q2 b8012 i386 Linux"),
				"\n",
				"12 45 \"Player\"\n",
				"0 0 \"Bot\"\n",
				"3 120 \"Other one\" \"203.0.113.7:27901\"\n"))},
		},
		{
			Name:        "quake2_needpass",
			Description: "Daikatana (quake2): sv_hostname, sv_maxclients and g_needpass, iv without clients",
			Args:        []string{"--type=daikatana"},
			Exchanges: []fixture.Exchange{ex(q2Req, quakeReply("print\n",
				cvars("sv_hostname", " Dk ", "sv_maxclients", "8", "g_needpass", "1", "iv", "1.3", "mapname", "e1m1a"),
				"\n", "1 99 \"Hiro\"\n"))},
		},
		{
			Name: "quake1_status",
			Description: "QuakeWorld 'n' reply without a newline after the type: *version, map cvar (not mapname), " +
				"full player lines, an empty skin and a short line",
			Args: []string{"--type=quake"},
			Exchanges: []fixture.Exchange{ex(q1Req, quakeReply("n",
				cvars("maxclients", "8", "*version", "2.40", "*progs", "666", "hostname", "QW \xe9 ^1Duel", "map", "dm4",
					"fraglimit", "30", "version", "ignored"),
				"\n",
				"12 25 340 50 \"Player One\" \"base\" 4 4\n",
				"13 0 12 0 \"camper\" \"\" 0 0\n",
				"14 3 5\n"))},
		},
		{
			Name:        "quake_hexen2_status",
			Description: "Hexen II: 0xFF-prefixed request and reply type, connect port is the query port minus 50",
			Args:        []string{"--type=hexen2"},
			Exchanges: []fixture.Exchange{ex(hexen2Req,
				quakeReply("n", cvars("hostname", "should be ignored")),
				quakeReply("\xffn", cvars("hostname", "Hexen II", "maxclients", "8", "map", "demo1"),
					"\n", "1 5 60 33 \"Paladin\" \"\" 1 2\n"))},
		},
		{
			Name: "quake_warsow_players",
			Description: "Warsow: the team field ends up in raw.address (warsow.js's team copy reads the wrong " +
				"object), names stripped",
			Args: []string{"--type=warsow"},
			Exchanges: []fixture.Exchange{ex(q3Req, quakeReply("statusResponse\n",
				cvars("sv_hostname", "^9war^7sow", "mapname", "wca1", "sv_maxclients", "16", "gametype", "ca",
					"version", "2.1 x86_64"),
				"\n", "10 50 \"^2Name\" 1\n", "3 0 \"^3bot\" 2\n", "0 70 \"spec\"\n"))},
		},
		{
			Name: "quake_xonotic_players",
			Description: "Xonotic: numeric names (after stripping) with an address become the team and the " +
				"address the name; JavaScript isNaN edge cases; bots are left alone",
			Args: []string{"--type=xonotic"},
			Exchanges: []fixture.Exchange{ex(q3Req, quakeReply("statusResponse\n",
				cvars("sv_hostname", "^xF00Xon^7otic", "mapname", "solarium", "sv_maxclients", "24", "version", "0.8.6"),
				"\n",
				"5 50 \"1\" \"^1Real^7Name\"\n",
				"5 50 \"\" \"Empty\"\n",
				"5 50 \"Infinity\" \"Inf\"\n",
				"5 50 \"-0x10\" \"NegHex\"\n",
				"5 50 \"0x10\" \"Hex\"\n",
				"5 50 \"0b101\" \"Bin\"\n",
				"5 50 \"\xa012 \" \"Spaced\"\n",
				"5 50 \"1e5\" \"Exp\"\n",
				"5 50 \".5\" \"Frac\"\n",
				"5 50 \"5.\" \"Dot\"\n",
				"5 50 \"1_0\" \"Sep\"\n",
				"5 50 \"12abc\" \"Not\"\n",
				"5 50 \"^13\" \"Colored\"\n",
				"5 50 \"4\"\n",
				"0 0 \"2\" \"BotAddr\"\n"))},
		},
	}
}
