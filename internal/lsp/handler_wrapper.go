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

func (h *handlerWrapper) Handle(ctx *glsp.Context) (any, bool, bool, error) {
	switch ctx.Method {
	case "textDocument/inlayHint":
		var params InlayHintParams
		if err := json.Unmarshal(ctx.Params, &params); err != nil {
			return nil, true, false, err
		}
		r, err := h.server.textDocumentInlayHint(ctx, &params)
		return r, true, true, err
	default:
		return h.inner.Handle(ctx)
	}
}
