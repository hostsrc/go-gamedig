package gamespy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// Limits for replies from untrusted servers.
const (
	maxDatagram = 65535
	// maxReplyBytes caps the bytes kept from one multi-packet reply.
	maxReplyBytes = 1 << 20
	// maxParts caps the packets of one GameSpy 1 reply.
	maxParts = 1024
	// maxItems caps the player/team slots a GameSpy 3 reply can create.
	maxItems = 1 << 16
)

var (
	errSocketTimeout = errors.New("UDP - Timed out")
	// errNetwork wraps socket errors other than timeouts, such as ICMP
	// unreachable replies.
	errNetwork = errors.New("UDP")
)

// Options control one query.
type Options struct {
	// StripColors removes America's Army colour codes, as node-gamedig's
	// stripColors option (on by default) does.
	StripColors bool
	// UTF8 decodes GameSpy 3 strings as UTF-8 rather than Windows-1252, as
	// node-gamedig's Minecraft protocol asks for.
	UTF8 bool
	// SocketTimeout bounds each request/response exchange. Default 2s.
	SocketTimeout time.Duration
	// Debugf, when set, receives a trace of every datagram.
	Debugf func(format string, args ...any)
}

// Response is the decoded server state in node-gamedig's shape. Numbers
// inside Raw and Players are float64 (JavaScript numbers, possibly NaN).
type Response struct {
	Name     string
	Map      string
	Password bool
	Version  string
	// MaxPlayers and NumPlayers are JavaScript numbers: they may be NaN or
	// fractional when the server sends odd values.
	MaxPlayers float64
	NumPlayers float64
	// GamePort is the advertised game port as node-gamedig prints it in
	// "connect", or "" to use the query port.
	GamePort string
	// Raw holds protocol-specific fields under node-gamedig's key names.
	Raw map[string]any
	// Players are the player objects, each with its "name" (if any) and
	// every other field.
	Players []map[string]any
	// RTT is the shortest observed round trip.
	RTT time.Duration
}

type conn struct {
	c    net.Conn
	opts Options
	rtt  time.Duration
	buf  []byte
}

// newConn wraps c for one query. The returned stop function must be
// called when the query ends.
func newConn(ctx context.Context, c net.Conn, opts Options) (*conn, func() bool) {
	if opts.SocketTimeout <= 0 {
		opts.SocketTimeout = 2 * time.Second
	}
	stop := context.AfterFunc(ctx, func() { _ = c.SetReadDeadline(time.Now()) })
	return &conn{c: c, opts: opts, buf: make([]byte, maxDatagram)}, stop
}

func (q *conn) debugf(format string, args ...any) {
	if q.opts.Debugf != nil {
		q.opts.Debugf(format, args...)
	}
}

// exchange writes pkt and passes each received datagram to handle until it
// returns true, an error, or SocketTimeout passes. Like node-gamedig's
// udpSend, every datagram from the server is offered to handle, which
// ignores the ones it does not want.
func (q *conn) exchange(ctx context.Context, pkt []byte, handle func([]byte) (bool, error)) error {
	q.debugf("--> %x", pkt)
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := q.c.Write(pkt); err != nil {
		return fmt.Errorf("%w: %w", errNetwork, err)
	}
	start := time.Now()
	deadline := start.Add(q.opts.SocketTimeout)
	ctxDeadline, hasCtxDeadline := ctx.Deadline()
	if hasCtxDeadline && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for {
		if err := q.c.SetReadDeadline(deadline); err != nil {
			return err
		}
		// Checked after setting the deadline: a cancellation that raced the
		// call above has already moved the deadline to now, or is seen here.
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := q.c.Read(q.buf)
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

func (q *conn) registerRTT(d time.Duration) {
	d = d.Truncate(time.Millisecond)
	if q.rtt == 0 || d < q.rtt {
		q.rtt = d
	}
}
