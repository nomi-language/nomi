// Package websocket is the WebSocket protocol (RFC 6455) over a net.Conn:
// the opening handshake's accept key and the client's side of it, framing,
// masking, fragmented messages, control frames, and the closing handshake.
// It is std/websocket's Go side, reached through internal/stdhttp's
// adapters, and uses only Go's standard library. It speaks no extension, so
// permessage-deflate is never negotiated.
//
// A Conn has one reader at a time and serializes its writers, so any number
// of tasks may send on one connection while one task receives. A ping is
// answered with a pong by whichever call is reading.
package websocket

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
	"unicode/utf8"
)

// Opcodes (RFC 6455 section 5.2).
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// Close codes (RFC 6455 section 7.4.1).
const (
	CloseNormal          = 1000
	CloseGoingAway       = 1001
	CloseProtocolError   = 1002
	CloseUnsupported     = 1003
	CloseNoStatus        = 1005
	CloseAbnormal        = 1006
	CloseInvalidData     = 1007
	ClosePolicyViolation = 1008
	CloseTooBig          = 1009
	CloseInternalError   = 1011
)

// DefaultCloseTimeout is how long Close waits for the peer's close frame.
const DefaultCloseTimeout = 5 * time.Second

const acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// AcceptKey is the Sec-WebSocket-Accept value for a Sec-WebSocket-Key.
func AcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(acceptGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ValidKey reports whether key is a Sec-WebSocket-Key: 16 bytes in base64.
func ValidKey(key string) bool {
	b, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(b) == 16
}

// NewKey is a fresh Sec-WebSocket-Key.
func NewKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.StdEncoding.EncodeToString(b[:])
}

// Options configure a Conn.
type Options struct {
	// Client masks the frames it sends and refuses masked frames, as a
	// client must; a server does the opposite.
	Client bool
	// MaxMessage is the largest message, in bytes, after its fragments
	// are joined; a larger one closes the connection with 1009. Zero or
	// less is no limit.
	MaxMessage int64
	// PingInterval sends a ping this often while the connection is open;
	// zero sends none.
	PingInterval time.Duration
	// IdleTimeout ends a receive that sees no frame, a pong included, for
	// this long; zero waits forever.
	IdleTimeout time.Duration
	// CloseTimeout is how long Close waits for the peer's close frame;
	// zero is DefaultCloseTimeout.
	CloseTimeout time.Duration
}

// CloseError is how a connection ended without a clean close: the peer's
// close frame with a code other than 1000, 1001 or none, or the connection
// ending with no close frame (Code 1006).
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("websocket: closed with %d", e.Code)
	}
	return fmt.Sprintf("websocket: closed with %d: %s", e.Code, e.Reason)
}

// ProtocolError is the peer breaking the protocol, or sending a message
// over the limit: the connection was closed with Code.
type ProtocolError struct {
	Code   int
	Reason string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("websocket: %s (closed with %d)", e.Reason, e.Code)
}

// ErrClosed is a send, ping or receive on a connection already closed.
var ErrClosed = errors.New("websocket: the connection is closed")

// ErrIdle is a receive that saw no frame within the idle timeout.
var ErrIdle = errors.New("websocket: no frame arrived within the idle timeout")

// Message is one data message.
type Message struct {
	Text bool
	Data []byte
}

// Conn is one WebSocket connection, either end.
type Conn struct {
	nc   net.Conn
	br   *bufio.Reader
	opts Options

	// wmu serializes frame writes; closeSent is under it.
	wmu       sync.Mutex
	closeSent bool

	// rmu is held by the one reader.
	rmu sync.Mutex

	// done is closed at teardown; term and clean say how it ended.
	done     chan struct{}
	once     sync.Once
	smu      sync.Mutex
	term     error
	torn     bool
	onClosed []func()
}

// New is a Conn over nc, whose handshake is done. br, when not nil, holds
// what was read from nc past the handshake and is read first.
func New(nc net.Conn, br *bufio.Reader, opts Options) *Conn {
	if br == nil {
		br = bufio.NewReaderSize(nc, 4096)
	}
	if opts.CloseTimeout <= 0 {
		opts.CloseTimeout = DefaultCloseTimeout
	}
	c := &Conn{nc: nc, br: br, opts: opts, done: make(chan struct{})}
	if opts.PingInterval > 0 {
		go c.pinger(opts.PingInterval)
	}
	return c
}

// OnClosed runs f once the connection is torn down, or at once if it is.
func (c *Conn) OnClosed(f func()) {
	c.smu.Lock()
	if c.torn {
		c.smu.Unlock()
		f()
		return
	}
	c.onClosed = append(c.onClosed, f)
	c.smu.Unlock()
}

// Done is closed once the connection is torn down.
func (c *Conn) Done() <-chan struct{} { return c.done }

// LocalAddr and RemoteAddr are the underlying connection's.
func (c *Conn) LocalAddr() net.Addr  { return c.nc.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.nc.RemoteAddr() }

// teardown closes the connection once, remembering how it ended: a nil
// term is a clean close.
func (c *Conn) teardown(term error) {
	c.once.Do(func() {
		c.smu.Lock()
		c.term = term
		c.torn = true
		hooks := c.onClosed
		c.onClosed = nil
		c.smu.Unlock()
		_ = c.nc.Close()
		close(c.done)
		for _, f := range hooks {
			f()
		}
	})
}

// ended reports whether the connection is torn down, and how.
func (c *Conn) ended() (bool, error) {
	c.smu.Lock()
	defer c.smu.Unlock()
	return c.torn, c.term
}

// past is a deadline already gone, which stops a blocked read or write.
var past = time.Unix(1, 0)

// Receive returns the next data message. ok is false with a nil error once
// the peer has closed cleanly (1000, 1001 or no code), and after that on
// every later call. A ping is answered and a pong is dropped on the way.
// The read stops when ctx ends.
func (c *Conn) Receive(ctx context.Context) (msg Message, ok bool, err error) {
	if torn, term := c.ended(); torn {
		return Message{}, false, term
	}
	c.rmu.Lock()
	defer c.rmu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = c.nc.SetReadDeadline(past) })
	defer stop()

	var (
		assembling bool
		text       bool
		data       []byte
	)
	for {
		f, err := c.readFrame(ctx, int64(len(data)), assembling)
		if err != nil {
			return Message{}, false, c.readFailed(ctx, err)
		}
		switch f.op {
		case opPing:
			c.wmu.Lock()
			if !c.closeSent {
				_ = c.writeFrame(opPong, f.payload)
			}
			c.wmu.Unlock()
		case opPong:
		case opClose:
			return Message{}, false, c.peerClosed(f.payload)
		case opText, opBinary:
			text = f.op == opText
			data = append(data[:0], f.payload...)
			assembling = !f.fin
		case opContinuation:
			data = append(data, f.payload...)
			assembling = !f.fin
		}
		if (f.op == opText || f.op == opBinary || f.op == opContinuation) && !assembling {
			if text && !utf8.Valid(data) {
				return Message{}, false, c.fail(CloseInvalidData, "a text message that is not UTF-8")
			}
			return Message{Text: text, Data: data}, true, nil
		}
	}
}

type frame struct {
	fin     bool
	op      byte
	payload []byte
}

// readFrame reads one frame, checking what RFC 6455 section 5 requires of it.
// held is the size of the message assembled so far, for the limit.
func (c *Conn) readFrame(ctx context.Context, held int64, assembling bool) (frame, error) {
	if c.opts.IdleTimeout > 0 {
		_ = c.nc.SetReadDeadline(time.Now().Add(c.opts.IdleTimeout))
	} else {
		_ = c.nc.SetReadDeadline(time.Time{})
	}
	if ctx.Err() != nil {
		return frame{}, ctx.Err()
	}
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return frame{}, err
	}
	f := frame{fin: head[0]&0x80 != 0, op: head[0] & 0x0F}
	if head[0]&0x70 != 0 {
		return frame{}, &ProtocolError{CloseProtocolError, "reserved bits set with no extension agreed"}
	}
	masked := head[1]&0x80 != 0
	length := int64(head[1] & 0x7F)
	control := f.op&0x8 != 0
	switch f.op {
	case opContinuation, opText, opBinary, opClose, opPing, opPong:
	default:
		return frame{}, &ProtocolError{CloseProtocolError, fmt.Sprintf("unknown opcode %d", f.op)}
	}
	if control && !f.fin {
		return frame{}, &ProtocolError{CloseProtocolError, "a fragmented control frame"}
	}
	if control && length > 125 {
		return frame{}, &ProtocolError{CloseProtocolError, "a control frame over 125 bytes"}
	}
	if masked == c.opts.Client {
		if c.opts.Client {
			return frame{}, &ProtocolError{CloseProtocolError, "a masked frame from the server"}
		}
		return frame{}, &ProtocolError{CloseProtocolError, "an unmasked frame from the client"}
	}
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		n := binary.BigEndian.Uint64(ext[:])
		if n>>63 != 0 {
			return frame{}, &ProtocolError{CloseProtocolError, "a frame length with its top bit set"}
		}
		length = int64(n)
	}
	// Order matters for the codes a peer sees: a misplaced data frame is a
	// protocol error even when it is also too big.
	if !control {
		if f.op == opContinuation && !assembling {
			return frame{}, &ProtocolError{CloseProtocolError, "a continuation frame with no message to continue"}
		}
		if f.op != opContinuation && assembling {
			return frame{}, &ProtocolError{CloseProtocolError, "a new message began before the last one ended"}
		}
		if c.opts.MaxMessage > 0 && held+length > c.opts.MaxMessage {
			return frame{}, &ProtocolError{CloseTooBig, fmt.Sprintf("a message over %d bytes", c.opts.MaxMessage)}
		}
	}
	var key [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, key[:]); err != nil {
			return frame{}, err
		}
	}
	f.payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, f.payload); err != nil {
		return frame{}, err
	}
	if masked {
		mask(key, f.payload)
	}
	return f, nil
}

func mask(key [4]byte, b []byte) {
	for i := range b {
		b[i] ^= key[i&3]
	}
}

// readFailed turns a failed frame read into what Receive returns, tearing
// the connection down.
func (c *Conn) readFailed(ctx context.Context, err error) error {
	if torn, term := c.ended(); torn {
		return term
	}
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return c.fail(pe.Code, pe.Reason)
	}
	if ctx.Err() != nil {
		c.teardown(&CloseError{CloseAbnormal, "the task was stopped"})
		_, term := c.ended()
		return term
	}
	var timeout interface{ Timeout() bool }
	if c.opts.IdleTimeout > 0 && errors.As(err, &timeout) && timeout.Timeout() {
		c.sendClose(CloseGoingAway, "idle timeout")
		c.teardown(ErrIdle)
		return ErrIdle
	}
	c.teardown(&CloseError{CloseAbnormal, describe(err)})
	_, term := c.ended()
	return term
}

func describe(err error) string {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "the connection ended with no close frame"
	}
	return err.Error()
}

// fail closes the connection with code after a protocol error.
func (c *Conn) fail(code int, reason string) error {
	c.sendClose(code, reason)
	e := &ProtocolError{code, reason}
	c.teardown(e)
	if _, term := c.ended(); term != nil {
		return term
	}
	return e
}

// peerClosed handles the peer's close frame: it is checked, echoed unless a
// close was sent already, and the connection torn down.
func (c *Conn) peerClosed(payload []byte) error {
	code := CloseNoStatus
	reason := ""
	switch {
	case len(payload) == 1:
		return c.fail(CloseProtocolError, "a close frame of one byte")
	case len(payload) >= 2:
		code = int(binary.BigEndian.Uint16(payload))
		if !validReceivedCode(code) {
			return c.fail(CloseProtocolError, fmt.Sprintf("close code %d is not allowed", code))
		}
		if !utf8.Valid(payload[2:]) {
			return c.fail(CloseInvalidData, "a close reason that is not UTF-8")
		}
		reason = string(payload[2:])
	}
	c.wmu.Lock()
	if !c.closeSent {
		c.closeSent = true
		echo := []byte{}
		if code != CloseNoStatus {
			echo = payload[:2]
		}
		_ = c.writeFrame(opClose, echo)
	}
	c.wmu.Unlock()
	var term error
	if code != CloseNormal && code != CloseGoingAway && code != CloseNoStatus {
		term = &CloseError{code, reason}
	}
	c.teardown(term)
	_, term = c.ended()
	return term
}

// validReceivedCode is RFC 6455 section 7.4's codes a close frame may carry:
// the defined ones, the registered 1012 to 1014, and 3000 to 4999.
func validReceivedCode(code int) bool {
	switch {
	case code >= 1000 && code <= 1003, code >= 1007 && code <= 1014:
		return true
	case code >= 3000 && code <= 4999:
		return true
	}
	return false
}

// ValidSendCode reports whether an endpoint may send code in a close frame.
func ValidSendCode(code int) bool {
	return validReceivedCode(code) && code != 1004
}

// sendClose writes a close frame unless one was sent, bounded by a short
// write deadline, for the error paths.
func (c *Conn) sendClose(code int, reason string) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closeSent {
		return
	}
	c.closeSent = true
	_ = c.nc.SetWriteDeadline(time.Now().Add(time.Second))
	_ = c.writeFrame(opClose, closePayload(code, reason))
}

func closePayload(code int, reason string) []byte {
	if len(reason) > 123 {
		reason = reason[:123]
		for !utf8.ValidString(reason) {
			reason = reason[:len(reason)-1]
		}
	}
	p := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(p, uint16(code))
	copy(p[2:], reason)
	return p
}

// writeFrame writes one final frame; the caller holds wmu.
func (c *Conn) writeFrame(op byte, payload []byte) error {
	n := len(payload)
	head := make([]byte, 0, 14+n)
	head = append(head, 0x80|op)
	maskBit := byte(0)
	if c.opts.Client {
		maskBit = 0x80
	}
	switch {
	case n <= 125:
		head = append(head, maskBit|byte(n))
	case n <= 0xFFFF:
		head = append(head, maskBit|126, byte(n>>8), byte(n))
	default:
		head = append(head, maskBit|127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	if c.opts.Client {
		var key [4]byte
		_, _ = rand.Read(key[:])
		head = append(head, key[:]...)
		start := len(head)
		head = append(head, payload...)
		mask(key, head[start:])
	} else {
		head = append(head, payload...)
	}
	_, err := c.nc.Write(head)
	return err
}

// Send sends one message as one frame. It waits for other senders, and
// stops when ctx ends.
func (c *Conn) Send(ctx context.Context, text bool, data []byte) error {
	op := byte(opBinary)
	if text {
		if !utf8.Valid(data) {
			return errors.New("websocket: a text message must be UTF-8")
		}
		op = opText
	}
	return c.write(ctx, op, data)
}

// Ping sends a ping with payload, at most 125 bytes.
func (c *Conn) Ping(ctx context.Context, payload []byte) error {
	if len(payload) > 125 {
		return errors.New("websocket: a ping payload over 125 bytes")
	}
	return c.write(ctx, opPing, payload)
}

func (c *Conn) write(ctx context.Context, op byte, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if torn, _ := c.ended(); torn || c.closeSent {
		return ErrClosed
	}
	stop := context.AfterFunc(ctx, func() { _ = c.nc.SetWriteDeadline(past) })
	defer stop()
	_ = c.nc.SetWriteDeadline(time.Time{})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := c.writeFrame(op, data); err != nil {
		c.closeSent = true
		e := &CloseError{CloseAbnormal, describe(err)}
		c.teardown(e)
		return e
	}
	return nil
}

// Close starts the closing handshake with code and reason, waits up to the
// close timeout for the peer's close frame, and closes the connection.
// Messages that arrive meanwhile are dropped, unless another call is
// receiving, which then sees the close. A second Close does nothing.
func (c *Conn) Close(code int, reason string) error {
	if !ValidSendCode(code) {
		return fmt.Errorf("websocket: close code %d cannot be sent", code)
	}
	if len(reason) > 123 {
		return errors.New("websocket: a close reason over 123 bytes")
	}
	c.wmu.Lock()
	if torn, _ := c.ended(); torn || c.closeSent {
		c.wmu.Unlock()
		if !torn {
			c.waitDone()
		}
		return nil
	}
	c.closeSent = true
	_ = c.nc.SetWriteDeadline(time.Now().Add(c.opts.CloseTimeout))
	err := c.writeFrame(opClose, closePayload(code, reason))
	c.wmu.Unlock()
	if err != nil {
		c.teardown(&CloseError{CloseAbnormal, describe(err)})
		return nil
	}
	if c.rmu.TryLock() {
		defer c.rmu.Unlock()
		c.drain()
		return nil
	}
	c.waitDone()
	return nil
}

// waitDone waits for the reader to see the peer's close, up to the close
// timeout, then tears the connection down.
func (c *Conn) waitDone() {
	t := time.NewTimer(c.opts.CloseTimeout)
	defer t.Stop()
	select {
	case <-c.done:
	case <-t.C:
		c.teardown(nil)
	}
}

// drain reads frames until the peer's close frame or the close timeout;
// the caller holds rmu.
func (c *Conn) drain() {
	deadline := time.Now().Add(c.opts.CloseTimeout)
	for {
		if torn, _ := c.ended(); torn {
			return
		}
		_ = c.nc.SetReadDeadline(deadline)
		f, err := c.readFrameRaw()
		if err != nil {
			c.teardown(nil)
			return
		}
		if f.op == opClose {
			c.teardown(nil)
			return
		}
	}
}

// readFrameRaw reads one frame while closing, with no checks but its
// framing: what it holds is dropped.
func (c *Conn) readFrameRaw() (frame, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return frame{}, err
	}
	f := frame{fin: head[0]&0x80 != 0, op: head[0] & 0x0F}
	length := int64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]) & (1<<63 - 1))
	}
	if head[1]&0x80 != 0 {
		var key [4]byte
		if _, err := io.ReadFull(c.br, key[:]); err != nil {
			return frame{}, err
		}
	}
	if _, err := io.CopyN(io.Discard, c.br, length); err != nil {
		return frame{}, err
	}
	return f, nil
}

// GoingAway starts closing with 1001 on its own goroutine, for a program
// that is shutting down.
func (c *Conn) GoingAway() {
	go func() { _ = c.Close(CloseGoingAway, "going away") }()
}

// Abort closes the connection with no closing handshake.
func (c *Conn) Abort() {
	c.teardown(&CloseError{CloseAbnormal, "closed with no closing handshake"})
}

func (c *Conn) pinger(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.wmu.Lock()
			if c.closeSent {
				c.wmu.Unlock()
				return
			}
			err := c.writeFrame(opPing, nil)
			c.wmu.Unlock()
			if err != nil {
				c.teardown(&CloseError{CloseAbnormal, describe(err)})
				return
			}
		}
	}
}
