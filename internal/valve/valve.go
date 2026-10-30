// Package valve implements the Valve server query protocol (A2S) used by
// Source, GoldSrc and many third-party engines.
//
// Behaviour follows node-gamedig's protocols/valve.js so results are
// interchangeable with it. Compressed split replies are additionally checked
// against their declared size and CRC32, as rust-gamedig does.
//
// References:
//   - https://developer.valvesoftware.com/wiki/Server_queries
//   - https://github.com/gamedig/node-gamedig/blob/master/protocols/valve.js
//   - https://github.com/gamedig/rust-gamedig/tree/main/crates/lib/src/protocols/valve
package valve

import (
	"bytes"
	"compress/bzip2"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hostsrc/go-gamedig/internal/binreader"
)

// Steam app IDs that need protocol quirks.
const (
	appSquad             = 393380
	appBat1944           = 489940
	appShip              = 2400
	appRust              = 252490
	appCSGO              = 730
	appCSSource          = 240
	appEternalSilence    = 17550
	appInsurgencyMIC     = 17700
	appSourceSDKBase2006 = 215
	appSoulmask          = 2646460
)

// Packet types.
const (
	reqInfo      = 0x54
	reqPlayers   = 0x55
	reqRules     = 0x56
	resChallenge = 0x41
	resInfo      = 0x49
	resInfoOld   = 0x6D
	resPlayers   = 0x44
	resRules     = 0x45

	noType = -1
)

// Limits for replies from untrusted servers.
const (
	maxDatagram     = 65535
	maxSplitBytes   = 1 << 20
	maxSplitIDs     = 16
	maxDecompressed = 1 << 20
	maxChallenges   = 3
)

var (
	errSocketTimeout = errors.New("UDP - Timed out")
	// errNetwork wraps socket errors other than timeouts, such as ICMP
	// unreachable replies.
	errNetwork = errors.New("UDP")
)

// noReply reports whether err means the server sent nothing usable, which
// optional requests (players, rules) treat as an empty answer.
func noReply(err error) bool {
	return errors.Is(err, errSocketTimeout) || errors.Is(err, errNetwork)
}

// Options control one query.
type Options struct {
	// GoldSrcInfo sends the pre-2013 "details" info request and parses the
	// 0x6D reply. Only gamedig's "goldsrc" protocol sets it; current GoldSrc
	// servers answer the Source request.
	GoldSrcInfo    bool
	RequestPlayers bool
	RequestRules   bool
	// SocketTimeout bounds each request/response exchange.
	SocketTimeout time.Duration
	// Debugf, when set, receives a trace of every datagram.
	Debugf func(format string, args ...any)
}

// RawPlayer is one entry of A2S_PLAYER.
type RawPlayer struct {
	Name  string  `json:"name"`
	Score int32   `json:"score"`
	Time  float64 `json:"time"`
}

// Buffer marshals like a Node.js Buffer passed through JSON.stringify.
type Buffer []byte

// MarshalJSON implements json.Marshaler.
func (b Buffer) MarshalJSON() ([]byte, error) {
	var s strings.Builder
	s.WriteString(`{"type":"Buffer","data":[`)
	for i, c := range b {
		if i > 0 {
			s.WriteByte(',')
		}
		s.WriteString(strconv.Itoa(int(c)))
	}
	s.WriteString("]}")
	return []byte(s.String()), nil
}

// Response is the decoded server state.
type Response struct {
	Name       string
	Map        string
	Password   bool
	Version    string
	NumPlayers int
	MaxPlayers int
	// GamePort is the game port the server advertises, or 0.
	GamePort int
	// Raw holds protocol-specific fields under node-gamedig's key names.
	Raw     map[string]any
	Players []RawPlayer
	Bots    []RawPlayer
	// RTT is the shortest observed round trip.
	RTT time.Duration
}

type query struct {
	conn          net.Conn
	opts          Options
	challenge     uint32
	hasChallenge  bool
	goldsrcSplits bool
	skipSize      bool
	rtt           time.Duration
	buf           []byte
}

// Query runs info, players and rules requests over conn, which must be a
// UDP socket connected to the server's query port.
func Query(ctx context.Context, conn net.Conn, opts Options) (*Response, error) {
	if opts.SocketTimeout <= 0 {
		opts.SocketTimeout = 2 * time.Second
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	q := &query{conn: conn, opts: opts, buf: make([]byte, maxDatagram)}
	res := &Response{Raw: map[string]any{}}
	if err := q.queryInfo(ctx, res); err != nil {
		return nil, err
	}
	if err := q.queryPlayers(ctx, res); err != nil {
		return nil, err
	}
	if err := q.queryRules(ctx, res); err != nil {
		return nil, err
	}
	classify(res)
	res.RTT = q.rtt
	return res, nil
}

func (q *query) debugf(format string, args ...any) {
	if q.opts.Debugf != nil {
		q.opts.Debugf(format, args...)
	}
}

func (q *query) queryInfo(ctx context.Context, res *Response) error {
	typ, payload, expect := reqInfo, []byte("Source Engine Query\x00"), byte(resInfo)
	if q.opts.GoldSrcInfo {
		typ, payload, expect = noType, []byte("details"), resInfoOld
	}
	b, _, err := q.sendPacket(ctx, typ, payload, expect, false)
	if err != nil {
		return err
	}
	ParseInfo(b, q.opts.GoldSrcInfo, res)

	protocol, _ := res.Raw["protocol"].(int)
	appID, _ := res.Raw["appId"].(int)
	if protocol == 7 && (appID == appSourceSDKBase2006 || appID == appEternalSilence ||
		appID == appInsurgencyMIC || appID == appCSSource) {
		q.skipSize = true
	}
	if protocol == 48 {
		q.debugf("GoldSrc detected, using its split format")
		q.goldsrcSplits = true
	}
	return nil
}

// ParseInfo decodes an A2S_INFO reply body (after the type byte) into res.
func ParseInfo(b []byte, goldsrc bool, res *Response) {
	r := binreader.New(b)
	raw := res.Raw

	if goldsrc {
		raw["address"] = r.String()
	} else {
		raw["protocol"] = int(r.Uint8())
	}
	res.Name = r.String()
	res.Map = r.String()
	raw["folder"] = r.String()
	raw["game"] = r.String()
	if !goldsrc {
		raw["appId"] = int(r.Uint16())
	}
	res.NumPlayers = int(r.Uint8())
	res.MaxPlayers = int(r.Uint8())
	if goldsrc {
		raw["protocol"] = int(r.Uint8())
	} else {
		raw["numbots"] = int(r.Uint8())
	}
	raw["listentype"] = string(rune(r.Uint8()))
	raw["environment"] = string(rune(r.Uint8()))
	res.Password = r.Uint8() != 0

	if goldsrc {
		isMod := int(r.Uint8())
		raw["ismod"] = isMod
		if isMod != 0 {
			raw["modlink"] = r.String()
			raw["moddownload"] = r.String()
			r.Skip(1)
			raw["modversion"] = int(r.Uint32())
			raw["modsize"] = int(r.Uint32())
			raw["modtype"] = int(r.Uint8())
			raw["moddll"] = int(r.Uint8())
		}
	} else {
		raw["secure"] = int(r.Uint8())
		if raw["appId"] == appShip {
			raw["shipmode"] = int(r.Uint8())
			raw["shipwitnesses"] = int(r.Uint8())
			raw["shipduration"] = int(r.Uint8())
		}
		res.Version = r.String()

		extra := r.Uint8()
		if extra&0x80 != 0 {
			res.GamePort = int(r.Uint16())
		}
		if extra&0x10 != 0 {
			raw["steamid"] = strconv.FormatUint(r.Uint64(), 10)
		}
		if extra&0x40 != 0 {
			raw["sourcetvport"] = int(r.Uint16())
			raw["sourcetvname"] = r.String()
		}
		if extra&0x20 != 0 {
			raw["tags"] = strings.Split(r.String(), ",")
		}
		if extra&0x01 != 0 {
			if appID := uint32(r.Uint64()) & 0xffffff; appID != 0 {
				raw["appId"] = int(appID)
			}
		}
	}

	if raw["appId"] == appRust {
		tags, _ := raw["tags"].([]string)
		for _, tag := range tags {
			if v, ok := parseInt(strings.TrimPrefix(tag, "mp")); ok && strings.HasPrefix(tag, "mp") {
				res.MaxPlayers = v
			}
			if v, ok := parseInt(strings.TrimPrefix(tag, "cp")); ok && strings.HasPrefix(tag, "cp") {
				res.NumPlayers = v
			}
		}
	}
}

func (q *query) queryPlayers(ctx context.Context, res *Response) error {
	res.Raw["players"] = []RawPlayer{}
	if !q.opts.RequestPlayers {
		return nil
	}
	typ, payload := reqPlayers, []byte(nil)
	if q.opts.GoldSrcInfo {
		typ, payload = noType, []byte("players")
	}
	b, ok, err := q.sendPacket(ctx, typ, payload, resPlayers, true)
	if err != nil {
		return err
	}
	if !ok {
		// No reply: CS:GO hides players unless host_players_show is 2, and
		// some games never answer. Keep the info.
		return nil
	}
	appID, _ := res.Raw["appId"].(int)
	res.Raw["players"] = ParsePlayers(b, appID)
	return nil
}

// ParsePlayers decodes an A2S_PLAYER reply body (after the type byte).
func ParsePlayers(b []byte, appID int) []RawPlayer {
	r := binreader.New(b)
	n := int(r.Uint8())
	players := make([]RawPlayer, 0, n)
	for range n {
		r.Skip(1)
		p := RawPlayer{Name: r.String(), Score: r.Int32(), Time: float64(r.Float32())}
		// CS:GO may add a fake "Max Players" entry.
		if appID == appCSGO && p.Name == "Max Players" {
			continue
		}
		players = append(players, p)
	}
	return players
}

func (q *query) queryRules(ctx context.Context, res *Response) error {
	appID, _ := res.Raw["appId"].(int)
	if appID != appSquad && appID != appBat1944 && !q.opts.RequestRules {
		return nil
	}
	rules := map[string]string{}
	res.Raw["rules"] = rules
	res.Raw["rulesBytes"] = Buffer{}

	if q.opts.GoldSrcInfo {
		// node-gamedig hands the first raw datagram, header included, to the
		// rules parser here; do the same for identical output.
		b, ok, err := q.sendFirst(ctx, []byte("\xff\xff\xff\xffrules"))
		if err != nil || !ok {
			return err
		}
		res.Raw["rulesBytes"] = Buffer(b)
		r := binreader.New(b)
		for !r.Done() {
			k := r.String()
			rules[k] = r.String()
		}
	} else {
		b, ok, err := q.sendPacket(ctx, reqRules, nil, resRules, true)
		if err != nil || !ok {
			return err
		}
		res.Raw["rulesBytes"] = Buffer(b)
		for k, v := range ParseRules(b) {
			rules[k] = v
		}
	}

	switch appID {
	case appBat1944:
		// Battalion 1944 reports its info through rules.
		if v, ok := rules["bat_name_s"]; ok {
			res.Name = v
			delete(rules, "bat_name_s")
			if v, ok := rules["bat_player_count_s"]; ok {
				res.NumPlayers, _ = parseInt(v)
				delete(rules, "bat_player_count_s")
			}
			if v, ok := rules["bat_max_players_i"]; ok {
				res.MaxPlayers, _ = parseInt(v)
				delete(rules, "bat_max_players_i")
			}
			if v, ok := rules["bat_has_password_s"]; ok {
				res.Password = v == "Y"
				delete(rules, "bat_has_password_s")
			}
			delete(rules, "bat_map_s")
		}
	case appSquad:
		if rules["Password_b"] == "true" {
			res.Password = true
		}
	case appSoulmask:
		// Soulmask's A2S_INFO version is hardcoded; the real one is a rule.
		if v, ok := rules["NO_s"]; ok {
			res.Version = v
		}
	}
	return nil
}

// ParseRules decodes an A2S_RULES reply body (after the type byte).
func ParseRules(b []byte) map[string]string {
	r := binreader.New(b)
	n := int(r.Uint16())
	rules := make(map[string]string, min(n, 256))
	for range n {
		k := r.String()
		rules[k] = r.String()
	}
	return rules
}

// classify splits players into humans and bots the way node-gamedig does:
// the numbots entries with the longest (or unknown) connection time are bots.
func classify(res *Response) {
	raw, _ := res.Raw["players"].([]RawPlayer)
	sorted := append([]RawPlayer(nil), raw...)
	prob := func(p RawPlayer) float64 {
		if p.Time == -1 {
			return math.MaxFloat64
		}
		return p.Time
	}
	sort.SliceStable(sorted, func(i, j int) bool { return prob(sorted[i]) < prob(sorted[j]) })

	numBots, _ := res.Raw["numbots"].(int)
	res.Bots, res.Players = []RawPlayer{}, []RawPlayer{}
	for len(res.Bots) < numBots && len(sorted) > 0 {
		res.Bots = append(res.Bots, sorted[len(sorted)-1])
		sorted = sorted[:len(sorted)-1]
	}
	for len(sorted) > 0 {
		res.Players = append(res.Players, sorted[len(sorted)-1])
		sorted = sorted[:len(sorted)-1]
	}
}

// sendPacket sends one request and returns the body of the first reply of
// the expected type. A challenge reply updates the key and resends. When
// allowTimeout is set, a timeout returns ok=false instead of an error.
func (q *query) sendPacket(ctx context.Context, typ int, payload []byte, expect byte, allowTimeout bool) (body []byte, ok bool, err error) {
	for range maxChallenges {
		newKey := false
		body, err := q.sendRaw(ctx, typ, payload, func(p []byte) ([]byte, bool) {
			r := binreader.New(p)
			t := r.Uint8()
			q.debugf("received 0x%02x, expected 0x%02x", t, expect)
			if t == resChallenge {
				if key := r.Uint32(); !q.hasChallenge || key != q.challenge {
					q.debugf("new challenge key 0x%08x", key)
					q.challenge, q.hasChallenge = key, true
					newKey = true
				}
			}
			if t == expect {
				return r.Rest(), true
			}
			return nil, newKey
		})
		if err != nil {
			if allowTimeout && noReply(err) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if !newKey {
			return body, true, nil
		}
	}
	return nil, false, errors.New("Received too many challenge key responses")
}

func (q *query) buildPacket(typ int, payload []byte) []byte {
	challenge := q.challenge
	if challenge == 0 {
		challenge = 0xffffffff
	}
	pkt := []byte{0xff, 0xff, 0xff, 0xff}
	if typ != noType {
		pkt = append(pkt, byte(typ))
	}
	if typ == reqPlayers || typ == reqRules {
		pkt = binary.LittleEndian.AppendUint32(pkt, challenge)
	}
	pkt = append(pkt, payload...)
	if typ == reqInfo && q.challenge != 0 {
		pkt = binary.LittleEndian.AppendUint32(pkt, challenge)
	}
	return pkt
}

// sendRaw sends a request and feeds every complete (reassembled) reply to
// onResponse until it reports done or the exchange times out.
func (q *query) sendRaw(ctx context.Context, typ int, payload []byte, onResponse func([]byte) ([]byte, bool)) ([]byte, error) {
	split := map[uint32]*splitState{}
	splitBytes := 0

	var result []byte
	err := q.exchange(ctx, q.buildPacket(typ, payload), func(d []byte) (bool, error) {
		r := binreader.New(d)
		switch r.Int32() {
		case -1:
			body, done := onResponse(r.Rest())
			if done {
				result = bytes.Clone(body)
			}
			return done, nil
		case -2:
			assembled, err := q.addSplit(r, split, &splitBytes)
			if err != nil || assembled == nil {
				return false, err
			}
			ar := binreader.New(assembled)
			ar.Skip(4)
			body, done := onResponse(ar.Rest())
			if done {
				result = bytes.Clone(body)
			}
			return done, nil
		}
		return false, nil
	})
	return result, err
}

// sendFirst sends pkt and returns the first datagram received.
func (q *query) sendFirst(ctx context.Context, pkt []byte) ([]byte, bool, error) {
	var result []byte
	err := q.exchange(ctx, pkt, func(d []byte) (bool, error) {
		result = bytes.Clone(d)
		return true, nil
	})
	if noReply(err) {
		return nil, false, nil
	}
	return result, err == nil, err
}

// exchange writes pkt and passes each received datagram to handle until it
// returns true, an error, or SocketTimeout passes.
func (q *query) exchange(ctx context.Context, pkt []byte, handle func([]byte) (bool, error)) error {
	q.debugf("--> %x", pkt)
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := q.conn.Write(pkt); err != nil {
		return fmt.Errorf("%w: %w", errNetwork, err)
	}
	start := time.Now()
	deadline := start.Add(q.opts.SocketTimeout)
	ctxDeadline, hasCtxDeadline := ctx.Deadline()
	if hasCtxDeadline && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for {
		if err := q.conn.SetReadDeadline(deadline); err != nil {
			return err
		}
		// Checked after setting the deadline: a cancellation that raced the
		// call above has already moved the deadline to now, or is seen here.
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := q.conn.Read(q.buf)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if hasCtxDeadline && !time.Now().Before(ctxDeadline) {
					return context.DeadlineExceeded
				}
				return fmt.Errorf("%w after %s", errSocketTimeout, q.opts.SocketTimeout)
			}
			return fmt.Errorf("%w: %w", errNetwork, err)
		}
		q.registerRTT(time.Since(start))
		q.debugf("<-- %x", q.buf[:n])
		done, err := handle(q.buf[:n])
		if err != nil || done {
			return err
		}
	}
}

func (q *query) registerRTT(d time.Duration) {
	d = d.Truncate(time.Millisecond)
	if q.rtt == 0 || d < q.rtt {
		q.rtt = d
	}
}

type splitState struct {
	parts      map[int][]byte
	bzip       bool
	bzipSize   uint32
	bzipCRC    uint32
	bzipHeader bool
}

// addSplit stores one part of a split reply and returns the reassembled
// payload once every part has arrived.
func (q *query) addSplit(r *binreader.Reader, states map[uint32]*splitState, total *int) ([]byte, error) {
	uid := r.Uint32()
	st := states[uid]
	if st == nil {
		if len(states) >= maxSplitIDs {
			return nil, errors.New("too many concurrent split replies")
		}
		st = &splitState{parts: map[int][]byte{}}
		states[uid] = st
	}
	st.bzip = !q.goldsrcSplits && uid&0x80000000 != 0

	var num, count int
	if q.goldsrcSplits {
		b := r.Uint8()
		count = int(b & 0x0f)
		num = int(b >> 4)
	} else {
		count = int(r.Uint8())
		num = int(r.Uint8())
		if !q.skipSize {
			r.Skip(2)
		}
		if num == 0 && st.bzip {
			st.bzipSize = r.Uint32()
			st.bzipCRC = r.Uint32()
			st.bzipHeader = true
		}
	}
	part := bytes.Clone(r.Rest())
	*total += len(part) - len(st.parts[num])
	if *total > maxSplitBytes {
		return nil, errors.New("split reply too large")
	}
	st.parts[num] = part
	q.debugf("split part uid=0x%08x %d (%d/%d)", uid, num, len(st.parts), count)
	if len(st.parts) != count {
		return nil, nil
	}

	var assembled []byte
	for i := range count {
		p, ok := st.parts[i]
		if !ok {
			return nil, fmt.Errorf("Missing packet #%d", i)
		}
		assembled = append(assembled, p...)
	}
	if st.bzip {
		return decompress(assembled, st)
	}
	return assembled, nil
}

func decompress(data []byte, st *splitState) ([]byte, error) {
	if !st.bzipHeader || st.bzipSize > maxDecompressed {
		return nil, errors.New("Invalid bzip packet")
	}
	out, err := io.ReadAll(io.LimitReader(bzip2.NewReader(bytes.NewReader(data)), int64(st.bzipSize)+1))
	if err != nil {
		return nil, errors.New("Invalid bzip packet")
	}
	if len(out) != int(st.bzipSize) || crc32.ChecksumIEEE(out) != st.bzipCRC {
		return nil, errors.New("bzip packet failed its size or CRC check")
	}
	return out, nil
}

// parseInt mirrors JavaScript's parseInt(s): optional leading whitespace,
// sign and 0x prefix, then as many digits as are present.
func parseInt(s string) (int, bool) {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	base := 10
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base, s = 16, s[2:]
	}
	n, digits := 0, 0
	for ; digits < len(s); digits++ {
		d := digitValue(s[digits])
		if d >= base {
			break
		}
		if n < math.MaxInt32 {
			n = n*base + d
		}
	}
	if digits == 0 {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 99
}
