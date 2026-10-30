package minecraft

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
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
