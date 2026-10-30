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

**Minecraft: the query answer is used.** For Minecraft, node-gamedig asks
three things at once: the Java status ping (over TCP), the "query" that Java
servers answer when `enable-query=true` is set (GameSpy 3, over UDP), and the
Bedrock ping (over UDP). Because of a mistake in node-gamedig, the query
request and its follow-up leave from two different local ports, and real
Minecraft servers ignore the follow-up. So node-gamedig in practice never
gets the query answer on its first try, and shows only the status ping.
go-gamedig sends both from one port, as node-gamedig means to, and does get
the answer. For servers with query turned on, the server name and the player
list then come from the query: the full list of players instead of the few
the status ping shows. Everything is combined by node-gamedig's own rules.
This is on purpose for now; we may change it to copy node-gamedig exactly.

**Minecraft: the whole status reply is read.** node-gamedig can stop reading
the Java status reply 1 to 3 bytes too early, when a network packet happens
to end there, and then fails. go-gamedig waits for the whole reply. It also
refuses a status reply that says it is bigger than 1 MiB, instead of waiting
for it.

**Minecraft: no reply sooner.** When nothing listens on the server's UDP
port, the network says so straight away. go-gamedig then stops the query and
Bedrock requests at once, so a Java server without query enabled answers
about 2 seconds sooner. The result is the same.

**Minecraft: rare cases.** A Java server's status can contain text that
JavaScript keeps but Go cannot, such as half of an emoji written as
`\ud800`; go-gamedig prints U+FFFD (the replacement character) there.
Host names with non-English letters are not converted to their `xn--` form
before the DNS lookup. When a name has several Minecraft SRV records, the
one Go puts first is used (lowest priority), not simply the first one in the
DNS answer.

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

1. **Recorded conversations.** The `testdata/` folder holds scripted
   conversations between a pretend server and a client, one file each. For
   Valve's protocol they cover:
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

   For Minecraft they cover: plain and formatted (chat component) server
   descriptions; player samples; server icons; Forge mod lists; the Better
   Compatibility Checker mod; the query answer, also split into pieces
   that arrive out of order; Bedrock replies, including broken ones; a Java
   server that accepts the connection but never answers; a server that
   refuses it; an old (before 1.7) server; odd JSON such as missing fields,
   player counts that are text or fractions, huge numbers and invalid text;
   and servers that never answer. Minecraft uses TCP as well as UDP, so a
   conversation step can be either. The pretend server listens for both on
   the same port, and on TCP it answers once the client has sent exactly
   the expected bytes.

   `go run ./tools/fixture synth` plays each conversation to node-gamedig
   5.3.3 and saves exactly what it sent and the JSON it printed.

2. **Playback.** `go test ./...` plays the same conversations to go-gamedig.
   It must send byte-for-byte the same requests, no more and no fewer, and
   print JSON with the same values. The `ping` value is not compared, because it depends on timing.

3. **Real servers.** `go run ./tools/fixture record` sits between
   node-gamedig and a real server and saves the conversation. Running
   `GAMEDIG_FIXTURES=<folder> go test ./cmd/gamedig` then plays those saved
   conversations to go-gamedig as well. Before the first release this was
   done with 90 real CS 1.6, CS2 and Euro Truck Simulator 2 servers, with
   server settings requested. All 90 matched. 79 of the servers answered,
   and 76 of those sent replies split into pieces. These recordings contain
   other people's server and player names, so they are not stored in the
   repository, and this step does not run in CI. Recordings of live
   Minecraft servers (Java and Bedrock, one with query turned on) matched
   the same way.

4. **Random input.** Go's built-in fuzz testing (the `Fuzz...` functions in
   each protocol's tests) feeds random data to the query code and the reply
   readers to make sure bad replies cannot crash it.

CI (GitHub Actions) records the conversations again with node-gamedig on
every change and fails if the result differs from what is stored. So if a
new node-gamedig release changed its behaviour, we would notice.

The commands for these steps are in [CONTRIBUTING.md](CONTRIBUTING.md).
