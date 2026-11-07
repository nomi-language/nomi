package websocket

import (
	"io"
	"net"
	"os"
	"sync"
	"time"
)

var zeroTime time.Time

// Pipe is two connected ends in memory, each end's writes the other's
// reads, with deadlines. Unlike net.Pipe it buffers what is written, so two
// ends that both write before reading (a close frame each way) do not
// deadlock. For testing a WebSocket handler with no socket.
func Pipe() (net.Conn, net.Conn) {
	ab := newPipeBuffer()
	ba := newPipeBuffer()
	return &pipeEnd{in: ba, out: ab}, &pipeEnd{in: ab, out: ba}
}

type pipeBuffer struct {
	mu     sync.Mutex
	data   []byte
	closed bool
	wake   chan struct{}
}

func newPipeBuffer() *pipeBuffer { return &pipeBuffer{wake: make(chan struct{})} }

// signal wakes every waiter; the caller holds mu.
func (b *pipeBuffer) signal() {
	close(b.wake)
	b.wake = make(chan struct{})
}

type pipeEnd struct {
	in, out *pipeBuffer

	dmu           sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
	deadlineWake  chan struct{}
	closeOnce     sync.Once
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

func (p *pipeEnd) LocalAddr() net.Addr  { return pipeAddr{} }
func (p *pipeEnd) RemoteAddr() net.Addr { return pipeAddr{} }

func (p *pipeEnd) deadline() (time.Time, chan struct{}) {
	p.dmu.Lock()
	defer p.dmu.Unlock()
	if p.deadlineWake == nil {
		p.deadlineWake = make(chan struct{})
	}
	return p.readDeadline, p.deadlineWake
}

func (p *pipeEnd) Read(b []byte) (int, error) {
	for {
		p.in.mu.Lock()
		if len(p.in.data) > 0 {
			n := copy(b, p.in.data)
			p.in.data = p.in.data[n:]
			p.in.mu.Unlock()
			return n, nil
		}
		if p.in.closed {
			p.in.mu.Unlock()
			return 0, io.EOF
		}
		wake := p.in.wake
		p.in.mu.Unlock()
		at, changed := p.deadline()
		var timer <-chan time.Time
		var t *time.Timer
		if !at.IsZero() {
			left := time.Until(at)
			if left <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
			t = time.NewTimer(left)
			timer = t.C
		}
		expired := false
		select {
		case <-wake:
		case <-changed:
		case <-timer:
			expired = true
		}
		if t != nil {
			t.Stop()
		}
		if expired {
			return 0, os.ErrDeadlineExceeded
		}
	}
}

func (p *pipeEnd) Write(b []byte) (int, error) {
	p.dmu.Lock()
	at := p.writeDeadline
	p.dmu.Unlock()
	if !at.IsZero() && !time.Now().Before(at) {
		return 0, os.ErrDeadlineExceeded
	}
	p.out.mu.Lock()
	defer p.out.mu.Unlock()
	if p.out.closed {
		return 0, io.ErrClosedPipe
	}
	p.out.data = append(p.out.data, b...)
	p.out.signal()
	return len(b), nil
}

func (p *pipeEnd) Close() error {
	p.closeOnce.Do(func() {
		for _, b := range []*pipeBuffer{p.in, p.out} {
			b.mu.Lock()
			b.closed = true
			b.signal()
			b.mu.Unlock()
		}
	})
	return nil
}

func (p *pipeEnd) setDeadlines(read, write bool, t time.Time) {
	p.dmu.Lock()
	defer p.dmu.Unlock()
	if read {
		p.readDeadline = t
	}
	if write {
		p.writeDeadline = t
	}
	if p.deadlineWake != nil {
		close(p.deadlineWake)
	}
	p.deadlineWake = make(chan struct{})
}

func (p *pipeEnd) SetDeadline(t time.Time) error      { p.setDeadlines(true, true, t); return nil }
func (p *pipeEnd) SetReadDeadline(t time.Time) error  { p.setDeadlines(true, false, t); return nil }
func (p *pipeEnd) SetWriteDeadline(t time.Time) error { p.setDeadlines(false, true, t); return nil }
