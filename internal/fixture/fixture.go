// Package fixture records and replays UDP query exchanges, so a query made
// by node-gamedig can be replayed byte for byte against this module.
package fixture

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
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
	// datagrams the server sent back before the next request.
	Exchanges []Exchange `json:"exchanges"`
	// GamedigVersion is the node-gamedig version that produced Expected.
	GamedigVersion string `json:"gamedig_version"`
	// Expected is node-gamedig's JSON output with ping removed.
	Expected json.RawMessage `json:"expected"`
}

// Exchange is one request and its replies.
type Exchange struct {
	Request   Hex   `json:"request"`
	Responses []Hex `json:"responses"`
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
// and the loopback port in queryPort and connect.
func Normalize(v map[string]any, port int) map[string]any {
	delete(v, "ping")
	if q, ok := v["queryPort"].(float64); ok && int(q) == port {
		v["queryPort"] = 0
	}
	if c, ok := v["connect"].(string); ok && c == fmt.Sprintf("127.0.0.1:%d", port) {
		v["connect"] = "127.0.0.1:PORT"
	}
	return v
}

// Server answers each request that exactly matches the next unused
// exchange with that exchange's responses. Unmatched requests are kept in
// Unmatched and get no reply, like a server ignoring a bad query.
type Server struct {
	conn      *net.UDPConn
	exchanges []Exchange
	used      []bool
	mu        sync.Mutex
	unmatched [][]byte
	done      chan struct{}
}

// Serve starts a server on a loopback port.
func Serve(exchanges []Exchange) (*Server, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	s := &Server{conn: conn, exchanges: exchanges, used: make([]bool, len(exchanges)), done: make(chan struct{})}
	go s.loop()
	return s, nil
}

// Port is the server's UDP port.
func (s *Server) Port() int { return s.conn.LocalAddr().(*net.UDPAddr).Port }

// Unmatched returns requests that matched no exchange.
func (s *Server) Unmatched() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.unmatched...)
}

// Close stops the server.
func (s *Server) Close() error {
	err := s.conn.Close()
	<-s.done
	return err
}

func (s *Server) loop() {
	defer close(s.done)
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
			if !s.used[i] && bytes.Equal(e.Request, req) {
				s.used[i], responses, matched = true, e.Responses, true
				break
			}
		}
		if !matched {
			s.unmatched = append(s.unmatched, req)
		}
		s.mu.Unlock()
		for _, r := range responses {
			_, _ = s.conn.WriteToUDP(r, from)
		}
	}
}

// Proxy forwards datagrams between a local client and a remote server and
// records them as exchanges.
type Proxy struct {
	local  *net.UDPConn
	remote *net.UDPConn
	mu     sync.Mutex
	ex     []Exchange
	client *net.UDPAddr
	done   chan struct{}
}

// NewProxy starts a recording proxy to target on a loopback port.
func NewProxy(target string) (*Proxy, error) {
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	remote, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, err
	}
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		remote.Close()
		return nil, err
	}
	p := &Proxy{local: local, remote: remote, done: make(chan struct{})}
	go p.upstream()
	go p.downstream()
	return p, nil
}

// Port is the proxy's local UDP port.
func (p *Proxy) Port() int { return p.local.LocalAddr().(*net.UDPAddr).Port }

// Close stops the proxy, waits briefly for late replies, and returns the
// recorded exchanges.
func (p *Proxy) Close() []Exchange {
	time.Sleep(200 * time.Millisecond)
	p.local.Close()
	p.remote.Close()
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ex
}

func (p *Proxy) upstream() {
	buf := make([]byte, 65535)
	for {
		n, from, err := p.local.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		p.client = from
		p.ex = append(p.ex, Exchange{Request: bytes.Clone(buf[:n]), Responses: []Hex{}})
		p.mu.Unlock()
		_, _ = p.remote.Write(buf[:n])
	}
}

func (p *Proxy) downstream() {
	defer close(p.done)
	buf := make([]byte, 65535)
	for {
		n, err := p.remote.Read(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		client := p.client
		if len(p.ex) > 0 {
			last := &p.ex[len(p.ex)-1]
			last.Responses = append(last.Responses, bytes.Clone(buf[:n]))
		}
		p.mu.Unlock()
		if client != nil {
			_, _ = p.local.WriteToUDP(buf[:n], client)
		}
	}
}
