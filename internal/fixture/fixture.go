// Package fixture records and replays query exchanges, so a query made by
// node-gamedig can be replayed byte for byte against this module. An
// exchange is either a UDP datagram and its replies or, for protocols such
// as Minecraft's Server List Ping, bytes sent on a TCP connection and the
// bytes written back. UDP and TCP are served on the same loopback port, as
// a game server would.
package fixture

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// Fixture is one recorded query and node-gamedig's result for it.
type Fixture struct {
	// Description says what the fixture covers.
	Description string `json:"description"`
	// Args are the node-gamedig flags used, without the target address and
	// port (fixtures always run with --givenPortOnly against loopback).
	Args []string `json:"args"`
	// Exchanges are the requests node-gamedig sent, in order, each with the
	// replies the server sent back before the next request.
	Exchanges []Exchange `json:"exchanges"`
	// GamedigVersion is the node-gamedig version that produced Expected.
	GamedigVersion string `json:"gamedig_version"`
	// Expected is node-gamedig's JSON output with ping removed.
	Expected json.RawMessage `json:"expected"`
}

// Exchange is one request and its replies.
type Exchange struct {
	// Network is "tcp" or "udp". Empty means "udp", so fixtures written
	// before TCP support stay valid unchanged.
	Network string `json:"network,omitempty"`
	// Request is a UDP datagram, or bytes the client writes on a TCP
	// connection (possibly in several writes).
	Request Hex `json:"request"`
	// PortOffsets are offsets in Request where the client writes the
	// server's port as a big-endian uint16, as Minecraft's handshake does.
	// The fixture server's port differs on every run, so the server puts
	// its own port there before comparing.
	PortOffsets []int `json:"port_offsets,omitempty"`
	// Responses are datagrams (UDP) or writes (TCP) sent back in order.
	Responses []Hex `json:"responses"`
}

// IsTCP reports whether the exchange is on a TCP connection.
func (e Exchange) IsTCP() bool { return e.Network == "tcp" }

// expected returns the request with port written at each PortOffset.
func (e Exchange) expected(port int) []byte {
	b := bytes.Clone(e.Request)
	for _, off := range e.PortOffsets {
		if off >= 0 && off+2 <= len(b) {
			binary.BigEndian.PutUint16(b[off:], uint16(port))
		}
	}
	return b
}

// Hex is a byte string stored as hexadecimal.
type Hex []byte

// MarshalJSON implements json.Marshaler.
func (h Hex) MarshalJSON() ([]byte, error) { return json.Marshal(hex.EncodeToString(h)) }

// UnmarshalJSON implements json.Unmarshaler.
func (h *Hex) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := hex.DecodeString(s)
	*h = v
	return err
}

// Load reads a fixture file.
func Load(path string) (*Fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i, e := range f.Exchanges {
		if e.Network != "" && e.Network != "udp" && e.Network != "tcp" {
			return nil, fmt.Errorf("%s: exchange %d: unknown network %q", path, i, e.Network)
		}
	}
	return &f, nil
}

// Save writes a fixture file.
func (f *Fixture) Save(path string) error {
	b, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Normalize removes what differs between runs from a decoded result: ping,
// and the loopback port in queryPort and connect. Protocols that combine
// several sub-queries (Minecraft) keep each sub-query's whole result under
// raw; those nested results are normalized the same way.
func Normalize(v map[string]any, port int) map[string]any {
	delete(v, "ping")
	if q, ok := v["queryPort"].(float64); ok && int(q) == port {
		v["queryPort"] = 0
	}
	if c, ok := v["connect"].(string); ok {
		switch c {
		case fmt.Sprintf("127.0.0.1:%d", port):
			v["connect"] = "127.0.0.1:PORT"
		case fmt.Sprintf("127.0.0.1:%d", port-50):
			// hexen2 reports the game port as the query port minus 50.
			v["connect"] = "127.0.0.1:PORT-50"
		}
	}
	if raw, ok := v["raw"].(map[string]any); ok {
		for _, sub := range raw {
			if m, ok := sub.(map[string]any); ok && isResult(m) {
				Normalize(m, port)
			}
		}
	}
	return v
}

// isResult reports whether m has the shape of a whole query result.
func isResult(m map[string]any) bool {
	for _, k := range []string{"name", "raw", "players", "bots", "queryPort", "connect", "ping"} {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// udpReplyDelay is how long the server waits before answering a datagram.
const udpReplyDelay = 2 * time.Millisecond

// listenPair binds UDP, and TCP too if withTCP, on the same loopback port.
func listenPair(withTCP bool) (*net.UDPConn, net.Listener, error) {
	var lastErr error
	for range 50 {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return nil, nil, err
		}
		if !withTCP {
			return conn, nil, nil
		}
		ln, err := net.Listen("tcp", conn.LocalAddr().String())
		if err == nil {
			return conn, ln, nil
		}
		conn.Close()
		lastErr = err
	}
	return nil, nil, fmt.Errorf("no loopback port free for both UDP and TCP: %w", lastErr)
}

// Server answers each request that exactly matches the next unused
// exchange with that exchange's responses. Unmatched requests are kept in
// Unmatched and get no reply, like a server ignoring a bad query.
//
// A TCP listener is opened only when some exchange is TCP; otherwise TCP
// connections to the port are refused. On each TCP connection the server
// collects the bytes the client writes until they equal an unused TCP
// exchange's request, writes that exchange's responses and starts
// collecting again. Bytes that can no longer become any request are
// recorded as unmatched. Connections stay open until the client or Close
// ends them.
type Server struct {
	conn      *net.UDPConn
	ln        net.Listener
	exchanges []Exchange
	used      []bool
	mu        sync.Mutex
	unmatched [][]byte
	conns     map[net.Conn]struct{}
	closed    bool
	wg        sync.WaitGroup
}

// Serve starts a server on a loopback port.
func Serve(exchanges []Exchange) (*Server, error) {
	withTCP := false
	for _, e := range exchanges {
		withTCP = withTCP || e.IsTCP()
	}
	conn, ln, err := listenPair(withTCP)
	if err != nil {
		return nil, err
	}
	s := &Server{conn: conn, ln: ln, exchanges: exchanges, used: make([]bool, len(exchanges)), conns: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.loop()
	if ln != nil {
		s.wg.Add(1)
		go s.accept()
	}
	return s, nil
}

// Port is the server's port, for UDP and TCP alike.
func (s *Server) Port() int { return s.conn.LocalAddr().(*net.UDPAddr).Port }

// Unmatched returns requests that matched no exchange.
func (s *Server) Unmatched() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.unmatched...)
}

// Unused returns the indexes of exchanges no request matched.
func (s *Server) Unused() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for i, u := range s.used {
		if !u {
			out = append(out, i)
		}
	}
	return out
}

// Close stops the server and waits for its connections to end.
func (s *Server) Close() error {
	err := s.conn.Close()
	s.mu.Lock()
	s.closed = true
	if s.ln != nil {
		s.ln.Close()
	}
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return err
}

func (s *Server) loop() {
	defer s.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := bytes.Clone(buf[:n])
		s.mu.Lock()
		var responses []Hex
		matched := false
		for i, e := range s.exchanges {
			if !s.used[i] && !e.IsTCP() && bytes.Equal(e.expected(s.Port()), req) {
				s.used[i], responses, matched = true, e.Responses, true
				break
			}
		}
		if !matched {
			s.unmatched = append(s.unmatched, req)
		}
		s.mu.Unlock()
		if len(responses) > 0 {
			// node-gamedig starts listening for a reply only once its send
			// has completed; over loopback an instant reply can arrive
			// first and be missed. Real servers are never that fast.
			time.Sleep(udpReplyDelay)
		}
		for _, r := range responses {
			_, _ = s.conn.WriteToUDP(r, from)
		}
	}
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			return
		}
		s.conns[c] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serveTCP(c)
	}
}

func (s *Server) serveTCP(c net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.Close()
	}()
	var pending []byte
	buf := make([]byte, 65535)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			var responses []Hex
			responses, pending = s.matchTCP(pending)
			for _, r := range responses {
				if _, err := c.Write(r); err != nil {
					break
				}
			}
		}
		if err != nil {
			if len(pending) > 0 {
				s.mu.Lock()
				s.unmatched = append(s.unmatched, pending)
				s.mu.Unlock()
			}
			return
		}
	}
}

// matchTCP consumes every complete request at the start of pending and
// returns their responses and the bytes still waiting for a match.
func (s *Server) matchTCP(pending []byte) ([]Hex, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	port := s.Port()
	var responses []Hex
	for len(pending) > 0 {
		matched, partial := false, false
		for i, e := range s.exchanges {
			if s.used[i] || !e.IsTCP() {
				continue
			}
			want := e.expected(port)
			if bytes.HasPrefix(pending, want) && len(want) > 0 {
				s.used[i] = true
				responses = append(responses, e.Responses...)
				pending = pending[len(want):]
				matched = true
				break
			}
			if bytes.HasPrefix(want, pending) {
				partial = true
			}
		}
		if matched {
			continue
		}
		if !partial {
			s.unmatched = append(s.unmatched, bytes.Clone(pending))
			pending = nil
		}
		break
	}
	return responses, pending
}

// Proxy forwards UDP datagrams and TCP connections between a local client
// and a remote server and records them as exchanges.
type Proxy struct {
	local  *net.UDPConn
	ln     net.Listener
	remote *net.UDPConn
	target string
	mu     sync.Mutex
	ex     []Exchange
	client *net.UDPAddr
	// lastUDP is the index of the latest UDP exchange, or -1.
	lastUDP int
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
}

// NewProxy starts a recording proxy to target on a loopback port, for
// UDP and TCP alike.
func NewProxy(target string) (*Proxy, error) {
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	remote, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, err
	}
	local, ln, err := listenPair(true)
	if err != nil {
		remote.Close()
		return nil, err
	}
	p := &Proxy{local: local, ln: ln, remote: remote, target: target, lastUDP: -1, conns: map[net.Conn]struct{}{}}
	p.wg.Add(3)
	go p.upstream()
	go p.downstream()
	go p.accept()
	return p, nil
}

// Port is the proxy's local port, for UDP and TCP alike.
func (p *Proxy) Port() int { return p.local.LocalAddr().(*net.UDPAddr).Port }

// Close stops the proxy, waits briefly for late replies, and returns the
// recorded exchanges. Wherever a TCP request contains the proxy's port as
// a big-endian uint16 it is marked in PortOffsets, so replay substitutes
// the replay server's port.
func (p *Proxy) Close() []Exchange {
	time.Sleep(200 * time.Millisecond)
	p.local.Close()
	p.remote.Close()
	p.ln.Close()
	p.mu.Lock()
	for c := range p.conns {
		c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], uint16(p.Port()))
	for i := range p.ex {
		e := &p.ex[i]
		if !e.IsTCP() {
			continue
		}
		for off := 0; off+2 <= len(e.Request); {
			j := bytes.Index(e.Request[off:], port[:])
			if j < 0 {
				break
			}
			e.PortOffsets = append(e.PortOffsets, off+j)
			off += j + 2
		}
	}
	return p.ex
}

func (p *Proxy) upstream() {
	defer p.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, from, err := p.local.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		p.client = from
		p.ex = append(p.ex, Exchange{Request: bytes.Clone(buf[:n]), Responses: []Hex{}})
		p.lastUDP = len(p.ex) - 1
		p.mu.Unlock()
		_, _ = p.remote.Write(buf[:n])
	}
}

func (p *Proxy) downstream() {
	defer p.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, err := p.remote.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue // e.g. ICMP port unreachable; keep proxying
		}
		p.mu.Lock()
		client := p.client
		if p.lastUDP >= 0 {
			last := &p.ex[p.lastUDP]
			last.Responses = append(last.Responses, bytes.Clone(buf[:n]))
		}
		p.mu.Unlock()
		if client != nil {
			_, _ = p.local.WriteToUDP(buf[:n], client)
		}
	}
}

func (p *Proxy) accept() {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		r, err := net.DialTimeout("tcp", p.target, 5*time.Second)
		if err != nil {
			// The client has already connected, so the refusal can't be
			// passed on; closing is the nearest equivalent.
			c.Close()
			continue
		}
		p.mu.Lock()
		p.conns[c], p.conns[r] = struct{}{}, struct{}{}
		p.mu.Unlock()
		p.wg.Add(1)
		go p.pipeTCP(c, r)
	}
}

// pipeTCP forwards one connection. Client bytes written before the server
// answers form one exchange's request; server bytes are its responses.
func (p *Proxy) pipeTCP(c, r net.Conn) {
	defer p.wg.Done()
	cur := -1
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer c.Close()
		buf := make([]byte, 65535)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				p.mu.Lock()
				if cur >= 0 {
					p.ex[cur].Responses = append(p.ex[cur].Responses, bytes.Clone(buf[:n]))
				}
				p.mu.Unlock()
				if _, err := c.Write(buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	buf := make([]byte, 65535)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			p.mu.Lock()
			if cur < 0 || len(p.ex[cur].Responses) > 0 {
				p.ex = append(p.ex, Exchange{Network: "tcp", Responses: []Hex{}})
				cur = len(p.ex) - 1
			}
			p.ex[cur].Request = append(p.ex[cur].Request, buf[:n]...)
			p.mu.Unlock()
			if _, err := r.Write(buf[:n]); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	r.Close()
	wg.Wait()
	p.mu.Lock()
	delete(p.conns, c)
	delete(p.conns, r)
	p.mu.Unlock()
}
