package lsp

import (
	"encoding/json"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// handlerWrapper wraps protocol.Handler to add LSP 3.17 methods
// that glsp's protocol_3_16 doesn't support.
type handlerWrapper struct {
	inner  *protocol.Handler
	server *Server
}

// lsp317Handler runs one request of lsp317, reporting false when its
// params do not decode.
type lsp317Handler func(s *Server, ctx *glsp.Context) (result any, validParams bool, err error)

// lsp317 are the requests handlerWrapper adds, by method.
var lsp317 = map[string]lsp317Handler{
	"textDocument/inlayHint":   request317((*Server).textDocumentInlayHint),
	"textDocument/inlineValue": request317((*Server).textDocumentInlineValue),
}

// request317 decodes a request's params and calls its handler.
func request317[P, R any](handler func(*Server, *glsp.Context, *P) (R, error)) lsp317Handler {
	return func(s *Server, ctx *glsp.Context) (any, bool, error) {
		var params P
		if err := json.Unmarshal(ctx.Params, &params); err != nil {
			return nil, false, err
		}
		r, err := handler(s, ctx, &params)
		return r, true, err
	}
}

func (h *handlerWrapper) Handle(ctx *glsp.Context) (any, bool, bool, error) {
	if handle, ok := lsp317[ctx.Method]; ok {
		r, validParams, err := handle(h.server, ctx)
		return r, true, validParams, err
	}
	return h.inner.Handle(ctx)
}
