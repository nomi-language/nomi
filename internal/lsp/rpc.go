package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The server reads messages in order on one goroutine. Notifications
// (didOpen, didChange, ...) run there, in order, and return at once: an
// edit stores its text and schedules analysis in the background
// (docsched.go). Each request runs on its own goroutine, so a request that
// waits for analysis holds up neither later edits nor other requests, and
// it can be cancelled with $/cancelRequest. initialize and shutdown run on
// the read goroutine, so nothing overtakes them.

// Error codes LSP adds to JSON-RPC's.
const (
	codeRequestCancelled int64 = -32800
	codeContentModified  int64 = -32801
)

// analysisWait bounds how long a request waits for the current analysis.
// An analysis normally takes tens of milliseconds; this only matters when
// one is pathologically slow.
const analysisWait = 2 * time.Second

// freshness says what a request does when the document's latest text is
// not yet analyzed.
type freshness int

const (
	// anyAnalysis answers from the latest finished analysis, possibly one
	// edit old. The handler reads the cursor's surroundings from the
	// latest text and maps positions into the older analysis itself.
	anyAnalysis freshness = iota
	// preferCurrent waits up to analysisWait for the current analysis,
	// then answers from the older one. For read-only answers whose ranges
	// come from the analysis: one keystroke stale is harmless.
	preferCurrent
	// requireCurrent waits up to analysisWait and fails the request with
	// ContentModified if the current analysis is not ready: its answer
	// edits the document, and edits computed against an older text could
	// corrupt it.
	requireCurrent
)

// requestFreshness is the staleness policy per request that reads a
// document's analysis. A listed request on a document whose first analysis
// has not finished is answered with null. A method not listed reads no
// document analysis (formatting edits the latest text only), or no single
// document's (completionItem/resolve, workspace requests).
//
//   - completion and signatureHelp are answered
//     while typing, where waiting is what made menus arrive too late.
//     Both read the cursor's context from the latest text; completion maps
//     positions into the older analysis (completionPositions) and reads
//     names, scopes and types from it.
//   - hover, definition, implementation, references, documentSymbol,
//     semantic tokens and inlay hints turn analysis positions into ranges.
//   - rename and codeAction return edits computed from the analysis.
var requestFreshness = map[string]freshness{
	string(protocol.MethodTextDocumentCompletion):         anyAnalysis,
	string(protocol.MethodTextDocumentSignatureHelp):      anyAnalysis,
	string(protocol.MethodTextDocumentHover):              preferCurrent,
	string(protocol.MethodTextDocumentDefinition):         preferCurrent,
	string(protocol.MethodTextDocumentImplementation):     preferCurrent,
	string(protocol.MethodTextDocumentReferences):         preferCurrent,
	string(protocol.MethodTextDocumentDocumentSymbol):     preferCurrent,
	string(protocol.MethodTextDocumentSemanticTokensFull): preferCurrent,
	"textDocument/inlayHint":                              preferCurrent,
	string(protocol.MethodTextDocumentRename):             requireCurrent,
	string(protocol.MethodTextDocumentCodeAction):         requireCurrent,
}

// rpcHandler adapts the server's glsp handler to jsonrpc2, adding
// concurrent requests, the freshness policy and cancellation, none of which
// glsp's own loop provides.
type rpcHandler struct {
	s     *Server
	inner glsp.Handler

	mu       sync.Mutex
	inflight map[jsonrpc2.ID]context.CancelFunc
}

func newRPCHandler(s *Server, inner glsp.Handler) *rpcHandler {
	return &rpcHandler{s: s, inner: inner, inflight: map[jsonrpc2.ID]context.CancelFunc{}}
}

// ServeStream serves one client connection on rw until it closes.
func (s *Server) ServeStream(rw io.ReadWriteCloser) {
	conn := jsonrpc2.NewConn(context.Background(), jsonrpc2.NewBufferedStream(rw, jsonrpc2.VSCodeObjectCodec{}), newRPCHandler(s, s.wrapper))
	<-conn.DisconnectNotify()
}

// RunStdio starts the server on stdin/stdout.
func (s *Server) RunStdio() error {
	s.ServeStream(stdio{})
	return nil
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error {
	if err := os.Stdin.Close(); err != nil {
		return err
	}
	return os.Stdout.Close()
}

func (h *rpcHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	switch req.Method {
	case string(protocol.MethodCancelRequest):
		h.cancel(req)
		return
	case "exit":
		conn.Close()
		return
	}
	gctx := h.glspContext(ctx, conn, req)
	if req.Notif {
		h.notification(gctx)
		return
	}
	switch req.Method {
	case string(protocol.MethodInitialize), string(protocol.MethodShutdown):
		result, err := h.call(gctx)
		h.reply(ctx, conn, req.ID, result, err)
		return
	}
	reqCtx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.inflight[req.ID] = cancel
	h.mu.Unlock()
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.inflight, req.ID)
			h.mu.Unlock()
			cancel()
		}()
		result, err := h.run(reqCtx, gctx, req)
		if reqCtx.Err() != nil {
			err = &jsonrpc2.Error{Code: codeRequestCancelled, Message: "request cancelled"}
		}
		h.reply(ctx, conn, req.ID, result, err)
	}()
}

// run applies the request's freshness policy, then calls its handler.
func (h *rpcHandler) run(ctx context.Context, gctx *glsp.Context, req *jsonrpc2.Request) (any, error) {
	if policy, ok := requestFreshness[req.Method]; ok {
		uri := paramsURI(gctx.Params)
		if policy != anyAnalysis && uri != "" && !h.s.awaitAnalysis(ctx, uri, h.s.sched.maxWait()) {
			if ctx.Err() != nil {
				return nil, nil
			}
			if policy == requireCurrent {
				return nil, &jsonrpc2.Error{Code: codeContentModified, Message: "the document changed; its analysis is not ready"}
			}
		}
		if snap := h.s.docs.Snapshot(uri); snap != nil && snap.Analysis == nil {
			return nil, nil
		}
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	h.s.setRequestContext(gctx, ctx)
	defer h.s.setRequestContext(gctx, nil)
	return h.call(gctx)
}

// notification runs a notification's glsp handler. A panic in it is logged
// (recover.go); a notification has no answer to carry it.
func (h *rpcHandler) notification(gctx *glsp.Context) {
	defer recoverPanic(gctx.Method, nil)
	fault(gctx.Method)
	h.inner.Handle(gctx)
}

// call runs the glsp handler and maps its outcome to JSON-RPC errors the
// way glsp's own loop does. A panic in the handler answers the request with
// an InternalError (recover.go).
func (h *rpcHandler) call(gctx *glsp.Context) (result any, err error) {
	defer recoverPanic(gctx.Method, func(p *serverPanic) {
		result, err = nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInternalError, Message: p.Error()}
	})
	fault(gctx.Method)
	result, validMethod, validParams, err := h.inner.Handle(gctx)
	switch {
	case !validMethod:
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeMethodNotFound, Message: fmt.Sprintf("method not supported: %s", gctx.Method)}
	case !validParams:
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams, Message: msg}
	case err != nil:
		if e, ok := err.(*jsonrpc2.Error); ok {
			return nil, e
		}
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidRequest, Message: err.Error()}
	}
	return result, nil
}

func (h *rpcHandler) reply(ctx context.Context, conn *jsonrpc2.Conn, id jsonrpc2.ID, result any, err error) {
	if err != nil {
		e, ok := err.(*jsonrpc2.Error)
		if !ok {
			e = &jsonrpc2.Error{Code: jsonrpc2.CodeInternalError, Message: err.Error()}
		}
		conn.ReplyWithError(ctx, id, e)
		return
	}
	conn.Reply(ctx, id, result)
}

// cancel handles $/cancelRequest: the request's context ends, a wait for
// analysis stops, and the request is answered with RequestCancelled. A
// request that already finished is not affected.
func (h *rpcHandler) cancel(req *jsonrpc2.Request) {
	if req.Params == nil {
		return
	}
	var params struct {
		ID jsonrpc2.ID `json:"id"`
	}
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return
	}
	h.mu.Lock()
	cancel := h.inflight[params.ID]
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *rpcHandler) glspContext(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) *glsp.Context {
	gctx := &glsp.Context{
		Method: req.Method,
		Notify: func(method string, params any) {
			conn.Notify(ctx, method, params)
		},
		Call: func(method string, params any, result any) {
			conn.Call(ctx, method, params, result)
		},
	}
	if req.Params != nil {
		gctx.Params = *req.Params
	}
	return gctx
}

// paramsURI is the textDocument.uri of a request's params, or "".
func paramsURI(raw json.RawMessage) string {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return p.TextDocument.URI
}

// setRequestContext records the context of the request gctx belongs to,
// or forgets it when ctx is nil.
func (s *Server) setRequestContext(gctx *glsp.Context, ctx context.Context) {
	if ctx == nil {
		s.requestCtxs.Delete(gctx)
		return
	}
	s.requestCtxs.Store(gctx, ctx)
}

// requestContext is the context of the request gctx belongs to: it ends
// when the client cancels the request. Long work in a handler checks it.
// A handler called without the RPC layer (a test) gets a context that
// never ends.
func (s *Server) requestContext(gctx *glsp.Context) context.Context {
	if gctx != nil {
		if ctx, ok := s.requestCtxs.Load(gctx); ok {
			return ctx.(context.Context)
		}
	}
	return context.Background()
}
