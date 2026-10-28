package lsp

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/sourcegraph/jsonrpc2"
)

// outbox writes the server's messages to the client, in the order they
// are sent, on a goroutine of its own. Sending never waits for the client
// to read: diagnostics are published while publishMu and the view lock
// are held, and a write that waits on a client that is not reading would
// hold them too, stopping the read goroutine (didClose takes publishMu)
// and every request (they take the view lock). The message is encoded as
// it is sent, so the writer reads nothing the sender may change.
type outbox struct {
	mu    sync.Mutex
	queue []func()
	wake  chan struct{}
}

func newOutbox() *outbox {
	return &outbox{wake: make(chan struct{}, 1)}
}

// notify queues a notification.
func (o *outbox) notify(ctx context.Context, conn *jsonrpc2.Conn, method string, params any) {
	raw, err := encodeParams(params)
	if err != nil {
		return
	}
	o.send(func() { conn.Notify(ctx, method, raw) })
}

// reply queues a request's answer: its result, or e when e is not nil.
func (o *outbox) reply(ctx context.Context, conn *jsonrpc2.Conn, id jsonrpc2.ID, result any, e *jsonrpc2.Error) {
	if e != nil {
		o.send(func() { conn.ReplyWithError(ctx, id, e) })
		return
	}
	raw, err := encodeParams(result)
	if err != nil {
		o.send(func() {
			conn.ReplyWithError(ctx, id, &jsonrpc2.Error{Code: jsonrpc2.CodeInternalError, Message: err.Error()})
		})
		return
	}
	o.send(func() { conn.Reply(ctx, id, raw) })
}

// encodeParams encodes v now. nil stays nil, which jsonrpc2 writes as an
// omitted params member or a null result.
func encodeParams(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func (o *outbox) send(write func()) {
	o.mu.Lock()
	o.queue = append(o.queue, write)
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// run writes queued messages until done is closed.
func (o *outbox) run(done <-chan struct{}) {
	for {
		o.mu.Lock()
		batch := o.queue
		o.queue = nil
		o.mu.Unlock()
		for _, write := range batch {
			select {
			case <-done:
				return
			default:
			}
			write()
		}
		if len(batch) > 0 {
			continue
		}
		select {
		case <-o.wake:
		case <-done:
			return
		}
	}
}
