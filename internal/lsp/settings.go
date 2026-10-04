package lsp

import (
	"encoding/json"
	"sync"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// inlayHintSettings says which inlay hints textDocument/inlayHint returns.
// Editors filter hints by kind poorly (Neovim has one switch per buffer), so
// the server filters them itself.
type inlayHintSettings struct {
	// ParameterNames is the `x:` before a positional argument.
	ParameterNames bool
	// BindingTypes is the `: Int` after an unannotated binding's name or
	// lambda parameter.
	BindingTypes bool
	// PipeTypes is the type at the end of each stage line of a multi-line
	// pipeline (pipe_hints.go).
	PipeTypes bool
}

var defaultInlayHintSettings = inlayHintSettings{
	ParameterNames: true,
	BindingTypes:   true,
	PipeTypes:      true,
}

// settingsStore holds the client's settings. Requests run concurrently with
// workspace/didChangeConfiguration, so reads take the lock.
type settingsStore struct {
	mu    sync.RWMutex
	hints *inlayHintSettings // nil until the client sets one
}

func (st *settingsStore) inlayHints() inlayHintSettings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.hints == nil {
		return defaultInlayHintSettings
	}
	return *st.hints
}

// inlayHintOptions is the JSON form of the settings. A field the client
// leaves out keeps its current value.
type inlayHintOptions struct {
	ParameterNames *bool `json:"parameterNames"`
	BindingTypes   *bool `json:"bindingTypes"`
	PipeTypes      *bool `json:"pipeTypes"`
}

type settingsOptions struct {
	InlayHints *inlayHintOptions `json:"inlayHints"`
}

// clientSettings is what a client sends, as initializationOptions or as
// workspace/didChangeConfiguration's settings: either the options
// themselves, `{"inlayHints": {...}}`, or the same under a `nomi` key,
// `{"nomi": {"inlayHints": {...}}}`. The `nomi` key wins when both are set.
type clientSettings struct {
	settingsOptions
	Nomi *settingsOptions `json:"nomi"`
}

// apply reads raw (any JSON value a client sent) and updates the settings
// it names. A value that is not an object of this shape changes nothing.
func (st *settingsStore) apply(raw any) {
	if raw == nil {
		return
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var cs clientSettings
	if json.Unmarshal(data, &cs) != nil {
		return
	}
	opts := cs.InlayHints
	if cs.Nomi != nil && cs.Nomi.InlayHints != nil {
		opts = cs.Nomi.InlayHints
	}
	if opts == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	h := defaultInlayHintSettings
	if st.hints != nil {
		h = *st.hints
	}
	if opts.ParameterNames != nil {
		h.ParameterNames = *opts.ParameterNames
	}
	if opts.BindingTypes != nil {
		h.BindingTypes = *opts.BindingTypes
	}
	if opts.PipeTypes != nil {
		h.PipeTypes = *opts.PipeTypes
	}
	st.hints = &h
}

// workspaceDidChangeConfiguration applies the pushed settings and asks the
// client to request inlay hints again, so a change shows without an edit.
func (s *Server) workspaceDidChangeConfiguration(ctx *glsp.Context, params *protocol.DidChangeConfigurationParams) error {
	s.settings.apply(params.Settings)
	if ctx != nil && ctx.Call != nil {
		// ctx.Call blocks on the client's reply; see initialized.
		go ctx.Call("workspace/inlayHint/refresh", nil, nil)
	}
	return nil
}
