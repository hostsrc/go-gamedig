# go-gamedig

Query game servers from Go: name, map, players, bots, rules and more.

go-gamedig is a Go port of [GameDig](https://github.com/gamedig). It follows
[node-gamedig](https://github.com/gamedig/node-gamedig) and cross-checks
protocol details against [rust-gamedig](https://github.com/gamedig/rust-gamedig).
It returns the same result shape and JSON values as node-gamedig 5, so it
works as a Go library or as a replacement for the `gamedig` command with no
Node.js runtime.

It is not affiliated with the GameDig organization. See [Credits](#credits).

## Status

| Protocol | Games | State |
|---|---|---|
| Valve A2S (`valve`, `goldsrc`) | 138, including CS 1.6, CS:Source, CS2, TF2, Rust, Garry's Mod, ARK: Survival Evolved, ETS2 | implemented |
| everything else (Minecraft, Quake 3, GameSpy, …) | 224 | not yet |

The full catalog of 362 games is embedded. `gamedig --list` shows each game
and whether it is supported; querying an unsupported game returns a clear
error.

## Install

```sh
go install github.com/hostsrc/go-gamedig/cmd/gamedig@latest
```

Prebuilt Linux and macOS binaries are attached to each
[release](https://github.com/hostsrc/go-gamedig/releases).

## Command line

The command takes node-gamedig's flags and prints the same JSON:

```sh
gamedig --type=counterstrike16 --port=27015 --givenPortOnly 203.0.113.10
gamedig --type=counterstrike2 --requestRules --pretty 203.0.113.10:27015
```

As in node-gamedig, a failed query prints `{"error":"Failed all 1 attempts"}`
and exits 0. Extra flags:

- `--strict`: exit 1 when the query fails
- `--list`: print the game catalog
- `--version`: print the version

Supported node-gamedig flags: `--type`, `--port`, `--givenPortOnly`,
`--maxRetries`, `--socketTimeout`, `--attemptTimeout` (milliseconds),
`--requestPlayers=false`, `--requestRules`, `--checkOldIDs`,
`--noBreadthOrder`, `--pretty` and `--debug`. Unknown flags are ignored, as
node-gamedig ignores them.

## Library

```go
import "github.com/hostsrc/go-gamedig"

res, err := gamedig.Query(ctx, gamedig.Options{
	Type:          "counterstrike16",
	Host:          "203.0.113.10",
	Port:          27015,
	GivenPortOnly: true,
})
if err != nil {
	return err // *gamedig.AttemptsError when every attempt failed
}
fmt.Println(res.Name, res.Map, res.NumPlayers, res.MaxPlayers)
```

`Result` marshals to the same JSON values as node-gamedig. Protocol-specific
fields live in `Result.Raw` under node-gamedig's key names, using only JSON
value types (`Number` for floats, so NaN prints as `null` like JavaScript).
Errors can be checked with `errors.Is` against `ErrNoGame`, `ErrUnknownGame`,
`ErrUnsupported` and `ErrNoHost`, or `errors.As` with `*AttemptsError`.

The API may still change before v1.0.

## Differences from node-gamedig

Results match node-gamedig 5.3.3 (see [Verification](#verification)). The
deliberate differences are:

- **Compressed replies are checked.** Like rust-gamedig, a bzip2-compressed
  split reply must match its declared size and CRC32. node-gamedig skips
  that check.
- **Limits for untrusted replies.** Reassembled replies are capped at 1 MiB,
  decompressed replies at 1 MiB, and at most 16 split replies can be in
  progress at once.
- **Faster failure.** A network error such as ICMP port unreachable ends a
  request straight away instead of waiting for the timeout. For the optional
  players and rules requests it counts as no reply, as in node-gamedig.
- **JSON text.** Values are the same, but object keys inside `raw` come out
  sorted rather than in insertion order, and U+2028/U+2029 are escaped.
- **`--debug`** prints a packet trace and the per-attempt errors to stderr;
  stdout still gets the normal JSON.
- **Bad `--maxRetries`.** It is clamped to 1–100. node-gamedig makes zero
  attempts for a negative value.
- **Not ported:** the in-process port cache, `--requestPlayersRequired` and
  `--requestRulesRequired`, `--ipFamily`, and `--listenUdpPort`.

## Verification

Compatibility is checked against node-gamedig itself rather than against a
reading of its code:

1. **Fixtures.** `testdata/` holds 14 scripted server conversations. They
   cover every A2S extra-data flag, challenge handshakes, GoldSrc and Source
   split replies (including out of order and protocol 7 without the size
   field), bzip2 compression, Windows-1251 player names, bot detection, the
   CS:GO, Rust, Squad and The Ship quirks, NaN/infinite/negative-zero
   floats, a zero challenge key, timeouts and challenge loops.
   `tools/fixture synth` serves each one to node-gamedig 5.3.3 and records
   both the exact bytes it sent and its JSON output.
2. **Replay.** `go test ./...` serves the same conversations to go-gamedig.
   It must send byte-identical requests and print JSON that decodes to the
   same values (ping excluded).
3. **Live servers.** `tools/fixture record` captures node-gamedig querying a
   real server through a proxy, and `GAMEDIG_FIXTURES=dir go test ./cmd/gamedig`
   replays those captures. Before the first release this was run against 90
   production CS 1.6, CS2 and ETS2 servers with rules requested. All 90
   replays matched, including the 79 servers that answered, 76 of which sent
   split replies. Live captures contain other people's server and player
   names, so they are not committed, and this step can't be reproduced in CI.
4. **Fuzzing.** `FuzzQuery`, `FuzzParse` and `FuzzDecodeUTF8` feed arbitrary
   datagrams to the full query path and the parsers.

CI regenerates the fixtures with node-gamedig and fails if its output
changes, so a new node-gamedig release can't drift from this port unnoticed.

## Development

```sh
go test -race ./...
go test ./internal/valve -run '^$' -fuzz FuzzQuery -fuzztime 60s

# re-record fixtures (needs node-gamedig and bzip2)
npm install -g gamedig@5.3.3
go run ./tools/fixture synth -gamedig-version 5.3.3 -out testdata

# regenerate the game catalog from a node-gamedig checkout
node tools/gen-games/gen-games.mjs ../node-gamedig/lib/games.js > games.json
```

To add a protocol, port it from node-gamedig's `protocols/` (and
rust-gamedig where it has one), register it in `protocols` in `gamedig.go`,
and add scenarios to `tools/fixture/scenarios.go` so its output is recorded
from node-gamedig.

## Credits

- [node-gamedig](https://github.com/gamedig/node-gamedig), by the
  node-gamedig developers: protocol behaviour, result shape, command line
  and the game catalog (`games.json` is generated from its `lib/games.js`).
- [rust-gamedig](https://github.com/gamedig/rust-gamedig), by the GameDig
  Organization and contributors: compressed split reply validation and a
  second reading of the Valve protocol.
- Valve's [Server queries](https://developer.valvesoftware.com/wiki/Server_queries)
  documentation.

Both GameDig projects are MIT licensed; their notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## License

MIT, see [LICENSE](LICENSE).
