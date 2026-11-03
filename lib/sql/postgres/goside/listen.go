//go:build !js

package goside

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
)

// Listener is a connection of its own that LISTENs on one channel. A
// notification waits on the server for the listener's next Wait.
//
// mu serializes Wait and CloseListener over the connection. state guards
// closing and the running Wait's cancel, so CloseListener can end a Wait
// that is blocked on the server before it takes mu.
type Listener struct {
	mu     sync.Mutex
	conn   *pgx.Conn
	closed bool

	state      sync.Mutex
	closing    bool
	cancelWait context.CancelFunc
	received   int64
}

// Listen opens a connection to the database at url and LISTENs on channel.
func Listen(ctx context.Context, url, channel string) (*Listener, error) {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	keepOnCancel(&config.Config)
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		conn.Close(context.Background())
		return nil, err
	}
	return &Listener{conn: conn}, nil
}

// Wait answers the next notification, waiting for one until the calling
// task's context ends.
// A CloseListener while it waits ends it with FailClosed.
func Wait(ctx context.Context, l *Listener) Notice {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Notice{Failure: Failure{Kind: FailClosed}}
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	l.state.Lock()
	if l.closing {
		l.state.Unlock()
		return Notice{Failure: Failure{Kind: FailClosed}}
	}
	l.cancelWait = cancel
	l.state.Unlock()
	n, err := l.conn.WaitForNotification(waitCtx)
	l.state.Lock()
	l.cancelWait = nil
	closing := l.closing
	if err == nil {
		l.received++
	}
	l.state.Unlock()
	if err != nil {
		if closing && ctx.Err() == nil {
			return Notice{Failure: Failure{Kind: FailClosed}}
		}
		return Notice{Failure: failure(ctx, err)}
	}
	return Notice{Channel: n.Channel, Payload: n.Payload}
}

// Waiting reports whether a Wait is blocked on the server now, for tests
// that must close a listener while it waits.
func Waiting(l *Listener) bool {
	l.state.Lock()
	defer l.state.Unlock()
	return l.cancelWait != nil
}

// Received counts the notifications Wait has answered, for tests.
func Received(l *Listener) int64 {
	l.state.Lock()
	defer l.state.Unlock()
	return l.received
}

// IsClosed reports whether CloseListener has been called.
func IsClosed(l *Listener) bool {
	l.state.Lock()
	defer l.state.Unlock()
	return l.closing
}

// CloseListener closes the listener's connection, ending a Wait that is
// running on another task first. Closing it again does nothing.
func CloseListener(l *Listener) Failure {
	l.state.Lock()
	l.closing = true
	if l.cancelWait != nil {
		l.cancelWait()
	}
	l.state.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Failure{}
	}
	l.closed = true
	if err := l.conn.Close(context.Background()); err != nil {
		return Failure{Kind: FailConnection, Message: err.Error()}
	}
	return Failure{}
}
