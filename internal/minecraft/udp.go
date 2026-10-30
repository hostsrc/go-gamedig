package minecraft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// udpMux shares one UDP socket between the GameSpy 3 and Bedrock
// sub-queries, as node-gamedig's GlobalUdpSocket does: both send from the
// same local port and every datagram from the server is offered to every
// waiting sub-query, which skips what isn't its reply.
type udpMux struct {
	conn  net.Conn
	debug func(string, ...any)

	mu   sync.Mutex
	subs map[chan udpEvent]struct{}
	done chan struct{}
}

type udpEvent struct {
	data []byte
	err  error
}

const (
	maxDatagram = 65535
	// udpQueue bounds the datagrams buffered for a slow sub-query; a
	// flooding server loses the excess instead of growing memory.
	udpQueue = 64
)

var errUDPNetwork = errors.New("UDP")

func newUDPMux(conn net.Conn, debug func(string, ...any)) *udpMux {
	m := &udpMux{conn: conn, debug: debug, subs: map[chan udpEvent]struct{}{}, done: make(chan struct{})}
	go m.read()
	return m
}

// close closes the socket and waits for the reader to stop.
func (m *udpMux) close() {
	m.conn.Close()
	<-m.done
}

func (m *udpMux) read() {
	defer close(m.done)
	buf := make([]byte, maxDatagram)
	failures := 0
	for {
		n, err := m.conn.Read(buf)
		var ev udpEvent
		if err != nil {
			if failures++; errors.Is(err, net.ErrClosed) || failures > 16 {
				return
			}
			// Typically ICMP port unreachable. node-gamedig's unconnected
			// socket never sees it and waits for its timeout; failing the
			// waiting requests now gives the same result sooner.
			ev.err = fmt.Errorf("%w: %w", errUDPNetwork, err)
		} else {
			failures = 0
			m.debug("<--UDP %x", buf[:n])
			ev.data = bytes.Clone(buf[:n])
		}
		m.mu.Lock()
		for ch := range m.subs {
			select {
			case ch <- ev:
			default:
			}
		}
		m.mu.Unlock()
	}
}

// exchange sends pkt and offers each datagram received afterwards to
// handle until it reports done or an error, or timeout passes.
func (m *udpMux) exchange(ctx context.Context, pkt []byte, timeout time.Duration, rtt *time.Duration, handle func([]byte) (bool, error)) error {
	ch := make(chan udpEvent, udpQueue)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	m.debug("UDP--> %x", pkt)
	start := time.Now()
	if _, err := m.conn.Write(pkt); err != nil {
		return fmt.Errorf("%w: %w", errUDPNetwork, err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("UDP - Timed out after %s", timeout)
		case ev := <-ch:
			if ev.err != nil {
				return ev.err
			}
			registerRTT(rtt, time.Since(start))
			done, err := handle(ev.data)
			if err != nil || done {
				return err
			}
		}
	}
}

// newConn returns a view of the shared socket as a net.Conn of its own,
// for a sub-query written against a plain UDP connection: writes go out
// on the shared socket and reads return every datagram received after the
// latest write. Like a node-gamedig udpSend callback, which is registered
// once the send completes, datagrams that arrived before a write are
// dropped.
func (m *udpMux) newConn() *subConn {
	c := &subConn{mux: m, ch: make(chan udpEvent, udpQueue), wake: make(chan struct{}, 1), closed: make(chan struct{})}
	m.mu.Lock()
	m.subs[c.ch] = struct{}{}
	m.mu.Unlock()
	return c
}

type subConn struct {
	mux    *udpMux
	ch     chan udpEvent
	wake   chan struct{}
	closed chan struct{}
	once   sync.Once

	mu       sync.Mutex
	deadline time.Time
}

func (c *subConn) Write(b []byte) (int, error) {
	c.drain()
	c.mux.debug("UDP--> %x", b)
	return c.mux.conn.Write(b)
}

// drain drops datagrams received before the next request.
func (c *subConn) drain() {
	for {
		select {
		case <-c.ch:
		default:
			return
		}
	}
}

func (c *subConn) Read(b []byte) (int, error) {
	for {
		c.mu.Lock()
		dl := c.deadline
		c.mu.Unlock()
		var timeout <-chan time.Time
		if !dl.IsZero() {
			d := time.Until(dl)
			if d <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
			timer := time.NewTimer(d)
			timeout = timer.C
			defer timer.Stop() // at most once per deadline change
		}
		select {
		case ev := <-c.ch:
			if ev.err != nil {
				return 0, ev.err
			}
			return copy(b, ev.data), nil
		case <-c.wake: // the deadline changed
		case <-timeout:
			return 0, os.ErrDeadlineExceeded
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
}

func (c *subConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

func (c *subConn) SetDeadline(t time.Time) error    { return c.SetReadDeadline(t) }
func (c *subConn) SetWriteDeadline(time.Time) error { return nil }
func (c *subConn) LocalAddr() net.Addr              { return c.mux.conn.LocalAddr() }
func (c *subConn) RemoteAddr() net.Addr             { return c.mux.conn.RemoteAddr() }

// Close unsubscribes from the shared socket, which stays open.
func (c *subConn) Close() error {
	c.once.Do(func() {
		c.mux.mu.Lock()
		delete(c.mux.subs, c.ch)
		c.mux.mu.Unlock()
		close(c.closed)
	})
	return nil
}
