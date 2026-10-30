# Compatibility with node-gamedig

go-gamedig aims to give the same answers as
[node-gamedig](https://github.com/gamedig/node-gamedig) version 5.3.3. For a
supported game it sends the same network requests and prints JSON with the
same values. This page lists where it differs on purpose, and explains how
we check that the rest matches.

## Differences

**Compressed replies are checked.** Some servers send their reply in several
pieces and compress it (with bzip2). Each such reply says how big it should
be once unpacked and includes a checksum (CRC32). go-gamedig checks both,
as rust-gamedig does, and rejects a reply that does not match. node-gamedig
skips this check.

**Limits on what a server can send.** A server can send anything, so
go-gamedig limits how much it will accept: at most 1 MiB for a reply put back
together from pieces, at most 1 MiB once unpacked, and at most 16 split
replies being collected at the same time.

**Failing faster.** If the network reports straight away that nobody is
listening (for example "connection refused" or "port unreachable"),
go-gamedig stops that try at once instead of waiting for the timeout. For the
optional player list and settings requests this counts as "no reply", the
same as in node-gamedig.

**JSON text.** The values are the same, but the text can differ slightly:

- Keys inside `raw` are printed in alphabetical order, not in the order the
  server sent them.
- The characters U+2028 and U+2029 (line and paragraph separators) are
  written as `\u2028` and `\u2029`.

**`--debug`.** It prints a trace of the network traffic and the error from
each try to stderr. The normal JSON still goes to stdout. node-gamedig's
debug output looks different.

**`--maxRetries` out of range.** Values are kept between 1 and 100. With a
negative value node-gamedig makes no attempts at all.

**Options not available.** These node-gamedig features are not ported:

- the port cache that node-gamedig keeps while it is running
- `--requestPlayersRequired` and `--requestRulesRequired`
- `--ipFamily`
- `--listenUdpPort`

Like node-gamedig, unknown options are ignored, so passing one of these does
not cause an error; it just has no effect.

**Extra options.** `--strict`, `--list` and `--version` exist only in
go-gamedig. See the [README](../README.md#options).

## How we check that the output matches

We compare against node-gamedig itself, by running it, instead of only
reading its code.

1. **Recorded conversations.** The `testdata/` folder holds 14 scripted
   conversations between a pretend server and a client. Together they cover:
   every optional field in Valve's server info reply; servers that first
   send a "challenge" number the client has to repeat back; replies split
   into several pieces, in both the old (GoldSrc, used by CS 1.6) and newer
   (Source) formats, including pieces that arrive out of order and the older
   Source format without a size field; compressed replies; player names in
   the Windows-1251 character set; telling bots apart from players; special
   cases for CS:GO, Rust, Squad and The Ship; player times that are not
   normal numbers (NaN, infinite, negative zero); a challenge number of
   zero; servers that never answer; and servers that keep sending new
   challenges.

   `go run ./tools/fixture synth` plays each conversation to node-gamedig
   5.3.3 and saves exactly what it sent and the JSON it printed.

2. **Playback.** `go test ./...` plays the same conversations to go-gamedig.
   It must send byte-for-byte the same requests and print JSON with the same
   values. The `ping` value is not compared, because it depends on timing.

3. **Real servers.** `go run ./tools/fixture record` sits between
   node-gamedig and a real server and saves the conversation. Running
   `GAMEDIG_FIXTURES=<folder> go test ./cmd/gamedig` then plays those saved
   conversations to go-gamedig as well. Before the first release this was
   done with 90 real CS 1.6, CS2 and Euro Truck Simulator 2 servers, with
   server settings requested. All 90 matched. 79 of the servers answered,
   and 76 of those sent replies split into pieces. These recordings contain
   other people's server and player names, so they are not stored in the
   repository, and this step does not run in CI.

4. **Random input.** Go's built-in fuzz testing (`FuzzQuery`, `FuzzParse`
   and `FuzzDecodeUTF8`) feeds random data to the query code and the reply
   readers to make sure bad replies cannot crash it.

CI (GitHub Actions) records the conversations again with node-gamedig on
every change and fails if the result differs from what is stored. So if a
new node-gamedig release changed its behaviour, we would notice.

The commands for these steps are in [CONTRIBUTING.md](CONTRIBUTING.md).
