# go-gamedig

go-gamedig asks a game server for its current state: the server name, the
map, how many people are playing and who they are. You can use it as a
command-line tool or as a Go library.

It is a Go version of [GameDig](https://github.com/gamedig). It works like
[node-gamedig](https://github.com/gamedig/node-gamedig) (same options, same
JSON output), but it is a single program and does not need Node.js.

go-gamedig is not affiliated with the GameDig organization. See
[Credits](#credits).

## Install

Pick one:

- **Download a ready-made program.** Each
  [release](https://github.com/hostsrc/go-gamedig/releases) has files for
  Linux and macOS, on Intel/AMD (`amd64`) and ARM (`arm64`) machines.
  Download the one that matches your system, rename it to `gamedig` and make
  it executable:

  ```sh
  mv gamedig_v0.1.0_linux_amd64 gamedig   # use the name of the file you downloaded
  chmod +x gamedig
  ./gamedig --version
  ```

  The `SHA256SUMS` file in the same release lets you check the download.

- **Build it with Go** (Go 1.22 or newer):

  ```sh
  go install github.com/hostsrc/go-gamedig/cmd/gamedig@latest
  ```

  This puts `gamedig` in Go's bin folder (usually `~/go/bin`).

## Use it from the command line

Tell it the game and the server address:

```sh
gamedig --type=counterstrike16 203.0.113.10:27015
```

It prints one line of JSON. Add `--pretty` to make it easier to read:

```sh
gamedig --type=counterstrike2 --pretty 203.0.113.10:27015
```

The output looks like this (shortened):

```json
{
  "name": "My CS2 server",
  "map": "de_dust2",
  "password": false,
  "numplayers": 2,
  "maxplayers": 10,
  "players": [
    { "name": "alice", "raw": { "score": 12, "time": 345.5 } },
    { "name": "bob", "raw": { "score": -3, "time": 0.1 } }
  ],
  "bots": [],
  "version": "1.40.8.4",
  "connect": "203.0.113.10:27015",
  "queryPort": 27015,
  "ping": 21,
  "raw": { "game": "Counter-Strike 2", "appId": 730 }
}
```

`raw` holds extra details that depend on the game.

If the server does not answer, it prints an error instead, such as
`{"error":"Failed all 1 attempts"}`. Like node-gamedig, it still exits with
code 0. Add `--strict` if your script needs exit code 1 on failure.

### Options

| Option | What it does |
|---|---|
| `--type=GAME` | The game, for example `counterstrike16`. Required. See [Which games work](#which-games-work). |
| `HOST` or `HOST:PORT` | The server address. You can also use `--host=HOST`. |
| `--port=PORT` | The server port, if you did not put it in the address. Without a port, the game's usual port is used. |
| `--givenPortOnly` | Ask only the port you gave. Without it, gamedig also tries the extra port some games use for status requests. |
| `--requestRules` | Also fetch the server's settings (they appear under `raw.rules`). |
| `--requestPlayers=false` | Skip the player list. |
| `--maxRetries=N` | How many times to try each port. Default 1, at most 100. |
| `--socketTimeout=MS` | How long to wait for each reply, in milliseconds. Default 2000. |
| `--attemptTimeout=MS` | How long one whole try may take, in milliseconds. Default 10000. |
| `--noBreadthOrder` | Use up all tries on one port before moving to the next. By default it tries every port once, then goes round again. |
| `--checkOldIDs` | Also accept the older game names used before GameDig 5, such as `cs16`. |
| `--pretty` | Print the JSON over several lines. |
| `--debug` | Print the network traffic and each failed try to stderr. The normal JSON still goes to stdout. |
| `--strict` | Exit with code 1 when the query fails. |
| `--list` | Print every game it knows about. |
| `--version` | Print the version. |

Options it does not recognize are ignored, as node-gamedig does.

## Use it from Go

```sh
go get github.com/hostsrc/go-gamedig
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/hostsrc/go-gamedig"
)

func main() {
	res, err := gamedig.Query(context.Background(), gamedig.Options{
		Type: "counterstrike16",
		Host: "203.0.113.10",
		Port: 27015,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Name, res.Map, res.NumPlayers, "/", res.MaxPlayers)
	for _, p := range res.Players {
		fmt.Println(p.Name)
	}
}
```

`Options` has the same settings as the command-line options above. The
result turns into the same JSON as the command prints if you pass it to
`encoding/json`.

If the server cannot be reached, the error is a `*gamedig.AttemptsError`
that lists what went wrong on each try. Errors that happen before anything
is sent can be checked with `errors.Is`: `gamedig.ErrNoGame` (no game
given), `gamedig.ErrUnknownGame` (unknown game name),
`gamedig.ErrUnsupported` (game known but not supported yet) and
`gamedig.ErrNoHost` (no address given).

The Go API may still change before version 1.0.

## Which games work

go-gamedig knows the same list of 362 games as node-gamedig. Today 138 of
them work: the games that use Valve's server status protocol. These include
Counter-Strike 1.6, Counter-Strike: Source, Counter-Strike 2, Team
Fortress 2, Left 4 Dead 2, Garry's Mod, Rust, Valheim, ARK: Survival Evolved
and Euro Truck Simulator 2.

Other games, such as Minecraft or Quake 3, are not supported yet. Asking for
one gives an error that says so. More are being added.

To see the current list, run:

```sh
gamedig --list
```

Each line shows the game name to use with `--type`, the protocol it uses,
whether it is `supported` or `unsupported`, and the full game title. To see
only the games that work:

```sh
gamedig --list | grep -w supported
```

If a game that uses Valve's protocol is missing from the list, you can still
ask it with `--type=protocol-valve`.

## Switching from node-gamedig

You can usually swap the `gamedig` command from node-gamedig for this one
without changing your scripts: it takes the same options and prints the same
JSON. There are a few small differences, and a few rarely used options are
not available yet. They are listed in
[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md), along with how we check that
the output matches.

## Contributing

[docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) explains how to run the tests,
how to update the game list and how to add support for more games.

## Credits

- [node-gamedig](https://github.com/gamedig/node-gamedig), by the
  node-gamedig developers. go-gamedig copies its behaviour, its output, its
  command-line options and its list of games (`games.json` is made from its
  `lib/games.js`).
- [rust-gamedig](https://github.com/gamedig/rust-gamedig), by the GameDig
  Organization and contributors. We used it as a second reference for
  Valve's protocol and for checking compressed replies.
- Valve's
  [Server queries](https://developer.valvesoftware.com/wiki/Server_queries)
  documentation.

Both GameDig projects are MIT licensed. Their license texts are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

go-gamedig is not affiliated with the GameDig organization.

## License

MIT, see [LICENSE](LICENSE).
