# Contributing

You need Go 1.22 or newer. Some steps also need Node.js, node-gamedig and
`bzip2`; those are marked below.

## Run the tests

```sh
go test -race ./...
```

This also plays back every recorded conversation in `testdata/` (see
[COMPATIBILITY.md](COMPATIBILITY.md)).

To play back your own recordings of real servers as well, put them in a
folder and run:

```sh
GAMEDIG_FIXTURES=/path/to/recordings go test ./cmd/gamedig
```

To run the fuzz tests (random input) for a minute:

```sh
go test ./internal/valve -run '^$' -fuzz '^FuzzQuery$' -fuzztime 60s
go test ./internal/valve -run '^$' -fuzz '^FuzzParse$' -fuzztime 30s
go test ./internal/binreader -run '^$' -fuzz '^FuzzDecodeUTF8$' -fuzztime 30s
go test ./internal/minecraft -run '^$' -fuzz '^FuzzStatusPacket$' -fuzztime 60s
```

Every protocol package has its own `Fuzz...` functions; CI runs each of them
for 30 seconds.

CI runs `gofmt`, `go vet` and `go test -race ./...` on Linux and macOS, all
the fuzz tests, and a check that the recordings still match node-gamedig.

## Record conversations again

This needs Node.js, node-gamedig 5.3.3 and `bzip2`.

```sh
npm install -g gamedig@5.3.3
go run ./tools/fixture synth -gamedig-version 5.3.3 -out testdata
```

Add `-only <name>` to record a single conversation. If `git diff testdata`
shows changes afterwards, node-gamedig now behaves differently from the
stored recordings.

To record node-gamedig talking to a real server:

```sh
go run ./tools/fixture record -gamedig-version 5.3.3 -type counterstrike16 \
  -target 203.0.113.10:27015 -out cs16.json
```

Recordings of other people's servers contain their server and player names.
Do not commit them.

## Update the game list

`games.json` is made from node-gamedig's `lib/games.js`. With a copy of the
node-gamedig source next to this repository:

```sh
node tools/gen-games/gen-games.mjs ../node-gamedig/lib/games.js > games.json
```

## Add support for more games

Games are grouped by the protocol they use (see the second column of
`gamedig --list`). To support a protocol:

1. Port it from node-gamedig's `protocols/` folder, and compare with
   rust-gamedig if it has the same protocol.
2. Put it in its own file, `protocol_<name>.go`, with an `init` function
   that calls `registerProtocol` using node-gamedig's protocol name and
   default port. `protocol_valve.go` is an example. You do not need to edit
   `gamedig.go`; every game in `games.json` that uses that protocol is then
   reported as `supported`.
3. Add scripted conversations for it in `tools/fixture`, in a new
   `scenarios_<name>.go` file that adds its set to `scenarioSets` from an
   `init` function. Then record them with node-gamedig (see above) and
   commit the new files in `testdata/`.

The README does not list protocols one by one, so it does not need to change
when you add one.

## Releases

Pushing a tag that starts with `v` (for example `v0.2.0`) makes GitHub
Actions run the tests, build `gamedig` for Linux and macOS (amd64 and arm64)
and publish them with a `SHA256SUMS` file as a GitHub release.
