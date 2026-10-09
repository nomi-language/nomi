package lsp

import (
	"context"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/stdcache"
	"github.com/nomi-language/nomi/std"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	_ "github.com/tliron/commonlog/simple"
)

const serverName = "nomi-lsp"

var version = "0.1.0"

// Server holds the state for the Nomi language server.
type Server struct {
	handler protocol.Handler
	docs    *analysis.DocumentManager
	wrapper *handlerWrapper
	std     *std.StdLib
	// stdNav, when set, chooses where navigation into std lands in place of
	// std.FileURI's own choice: a navigator with its own source tree (or
	// none), cache root and files. Tests set it to reach the materialized
	// directory from a checkout, or to point at a fake checkout. Nil means
	// std.FileURI.
	stdNav          *stdcache.Navigator
	notify          glsp.NotifyFunc // captured for background notifications
	cancelPropagate context.CancelFunc
	propagateMu     sync.Mutex
	// exprIndex is completion's position index over the last completed
	// document's recorded expression types (completion_types.go).
	exprIndex exprIndexCache
	// completions holds the last completion list's documentation sources
	// for completionItem/resolve.
	completions completionResolveCache
	// snippetSupport is the client's completionItem.snippetSupport.
	snippetSupport bool
	// relatedInformation is the client's publishDiagnostics.relatedInformation.
	relatedInformation bool
	// literals caches the evaluations of static typed literals
	// (literal_eval.go).
	literals literalEvals
	// lowering holds the diagnostics for code the compiler cannot lower,
	// found when a document is opened, saved or edited (lowering.go).
	lowering loweringChecks
	// sched runs edited documents' analyses in the background.
	sched analysisScheduler
	// requestCtxs maps a running request's *glsp.Context to its
	// cancellable context (rpc.go).
	requestCtxs sync.Map
	// bg is the context background work (the workspace scan, closed
	// files' diagnostics) runs under; shutdown cancels it via stopBg.
	bg     context.Context
	stopBg context.CancelFunc
	// closedDiags queues closed files for a diagnostics pass
	// (workspace.go).
	closedDiags closedDiagnostics
	// settings holds the client's settings (settings.go).
	settings settingsStore
	// pipeTokens caches the token stream pipe-stage hints read
	// (pipe_hints.go).
	pipeTokens tokenCache
	// lines caches each open document's line index (position.go).
	lines lineCache
	// occurrences caches each open document's occurrence index
	// (document_highlight.go).
	occurrences occurrenceCache
}

// NewServer creates and configures a new Nomi LSP server.
func NewServer() *Server {
	s := &Server{
		docs: analysis.NewDocumentManager(),
	}
	s.startBackground()

	// The process's one stdlib analysis, the one the front end and the IR
	// builder read: the lowering check lowers a document's analysis
	// (lowering.go), and the builder knows a std declaration by its node.
	lib := std.Shared()
	s.docs.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	// Std modules a document imports are read from this server's own
	// embedded std, never from an open buffer or the disk.
	stdRoot, _ := analysis.StdlibPath()
	s.docs.SetStdlibSource(stdRoot, std.ReadFile)
	s.std = lib

	s.handler = protocol.Handler{
		Initialize:                       s.initialize,
		Initialized:                      s.initialized,
		Shutdown:                         s.shutdown,
		SetTrace:                         s.setTrace,
		TextDocumentDidOpen:              s.textDocumentDidOpen,
		TextDocumentDidChange:            s.textDocumentDidChange,
		TextDocumentDidSave:              s.textDocumentDidSave,
		TextDocumentDidClose:             s.textDocumentDidClose,
		TextDocumentDocumentSymbol:       s.textDocumentDocumentSymbol,
		TextDocumentDefinition:           s.textDocumentDefinition,
		TextDocumentImplementation:       s.textDocumentImplementation,
		TextDocumentTypeDefinition:       s.textDocumentTypeDefinition,
		TextDocumentCompletion:           s.textDocumentCompletion,
		CompletionItemResolve:            s.completionItemResolve,
		TextDocumentHover:                s.textDocumentHover,
		TextDocumentReferences:           s.textDocumentReferences,
		TextDocumentDocumentHighlight:    s.textDocumentDocumentHighlight,
		TextDocumentRename:               s.textDocumentRename,
		TextDocumentPrepareRename:        s.textDocumentPrepareRename,
		TextDocumentSemanticTokensFull:   s.textDocumentSemanticTokensFull,
		TextDocumentSignatureHelp:        s.textDocumentSignatureHelp,
		TextDocumentFormatting:           s.textDocumentFormatting,
		TextDocumentCodeAction:           s.textDocumentCodeAction,
		TextDocumentCodeLens:             s.textDocumentCodeLens,
		TextDocumentDocumentLink:         s.textDocumentDocumentLink,
		TextDocumentPrepareCallHierarchy: s.textDocumentPrepareCallHierarchy,
		CallHierarchyIncomingCalls:       s.callHierarchyIncomingCalls,
		CallHierarchyOutgoingCalls:       s.callHierarchyOutgoingCalls,
		WorkspaceDidChangeWatchedFiles:   s.workspaceDidChangeWatchedFiles,
		WorkspaceDidChangeConfiguration:  s.workspaceDidChangeConfiguration,
		WorkspaceSymbol:                  s.workspaceSymbol,
		WorkspaceWillRenameFiles:         s.workspaceWillRenameFiles,
	}

	s.wrapper = &handlerWrapper{inner: &s.handler, server: s}
	return s
}

// stdFileURI is the file go-to-definition and links open for a std module.
func (s *Server) stdFileURI(module string) string {
	if s.stdNav != nil {
		return "file://" + s.stdNav.Path(module+".nomi")
	}
	return s.std.FileURI(module)
}

func (s *Server) initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	commonlog.NewInfoMessage(0, "nomi-lsp initializing")

	capabilities := s.handler.CreateServerCapabilities()

	// Use full sync — re-parse entire file on each change
	syncKind := protocol.TextDocumentSyncKindFull
	// Save notifications start the lowering diagnostics (lowering.go); the
	// server already holds the saved text.
	capabilities.TextDocumentSync = protocol.TextDocumentSyncOptions{
		OpenClose: boolPtr(true),
		Change:    &syncKind,
		Save:      protocol.SaveOptions{IncludeText: boolPtr(false)},
	}

	capabilities.SignatureHelpProvider = &protocol.SignatureHelpOptions{
		TriggerCharacters:   []string{"(", ","},
		RetriggerCharacters: []string{",", " "},
	}

	// Trigger completion on `.`, which opens a member list (`String.`,
	// `server.`, `p.`) or a variant shorthand (`.`); on `>`, which opens
	// the stages of a pipe only when it completes `|>`; and on `{`, which
	// opens a struct literal's fields only after a type's name (`Point{`), as
	// does the `}` an auto-pair plugin adds to it (`Point{‸}`);
	// and on `"` and a backtick, which open a typed literal's documented
	// examples only after a type's name (`Date"`); and on `:`, which opens
	// the types after an annotated name (`game:`, `fn f(): `); and on `,`,
	// which opens a struct literal's or pattern's next field. The handler
	// answers any other `>`, `{`, quote, `:` or `,` with nothing. Items
	// carry no documentation until the client resolves them.
	capabilities.CompletionProvider = &protocol.CompletionOptions{
		TriggerCharacters: []string{".", ">", "{", "}", `"`, "`", ":", ","},
		ResolveProvider:   boolPtr(true),
	}
	s.snippetSupport = clientSnippetSupport(params)
	s.relatedInformation = clientRelatedInformation(params)
	s.settings.apply(params.InitializationOptions)

	// Advertise the specific code-action kinds we provide so editors can wire
	// them (e.g. run source.organizeImports on save). CreateServerCapabilities
	// already set CodeActionProvider to true because the handler is registered;
	// replace it with the kinded options form.
	capabilities.CodeActionProvider = protocol.CodeActionOptions{
		CodeActionKinds: []protocol.CodeActionKind{
			protocol.CodeActionKindQuickFix,
			protocol.CodeActionKindRefactorExtract,
			protocol.CodeActionKindRefactorInline,
			protocol.CodeActionKindRefactorRewrite,
			protocol.CodeActionKindSourceOrganizeImports,
			codeActionKindSourceFixAll,
		},
	}

	capabilities.SemanticTokensProvider = protocol.SemanticTokensOptions{
		Legend: protocol.SemanticTokensLegend{
			TokenTypes:     tokenTypes,
			TokenModifiers: tokenModifiers,
		},
		Full: true,
	}

	capabilities.RenameProvider = protocol.RenameOptions{PrepareProvider: boolPtr(true)}

	// A rename or move of a .nomi file or a folder updates its imports
	// (file_rename.go).
	capabilities.Workspace.FileOperations.WillRename.Filters = fileRenameFilters()

	// Store workspace root for scanning
	if params.RootURI != nil {
		root := strings.TrimPrefix(string(*params.RootURI), "file://")
		s.docs.SetWorkspaceRoot(root)
	}

	return initializeResult{
		Capabilities: initCapabilities{
			ServerCapabilities: capabilities,
			InlayHintProvider:  true,
		},
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    serverName,
			Version: &version,
		},
	}, nil
}

func (s *Server) initialized(ctx *glsp.Context, params *protocol.InitializedParams) error {
	// Capture notify for background goroutines
	s.notify = ctx.Notify

	// Register file watcher and scan workspace in background.
	// ctx.Call blocks waiting for a response, which deadlocks the message
	// loop (jsonrpc2 reads messages sequentially). Run it in a goroutine.
	go func() {
		ctx.Call(protocol.ServerClientRegisterCapability, &protocol.RegistrationParams{
			Registrations: []protocol.Registration{
				{
					ID:     "nomi-file-watcher",
					Method: string(protocol.MethodWorkspaceDidChangeWatchedFiles),
					RegisterOptions: protocol.DidChangeWatchedFilesRegistrationOptions{
						Watchers: []protocol.FileSystemWatcher{
							{GlobPattern: "**/*.nomi"},
							{GlobPattern: "**/*.go"},
							{GlobPattern: "**/go.mod"},
							{GlobPattern: "**/go.sum"},
						},
					},
				},
			},
		}, nil)
	}()

	go s.scanWorkspace()
	return nil
}

func (s *Server) shutdown(ctx *glsp.Context) error {
	s.stopBg()
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func (s *Server) setTrace(ctx *glsp.Context, params *protocol.SetTraceParams) error {
	protocol.SetTraceValue(params.Value)
	return nil
}

// textDocumentDidOpen and textDocumentDidChange store the text and return.
// The analysis runs in the background (docsched.go): at once for an opened
// document, after analysisDelay for an edit. Diagnostics, import edges and
// propagation to importing files follow from the finished analysis. The
// lowering check runs in the background too: at once for an opened
// document, after loweringDelay for an edit (lowering.go).
func (s *Server) textDocumentDidOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	s.docs.SetText(uri, params.TextDocument.Text)
	s.scheduleAnalysis(uri, 0, ctx.Notify)
	s.checkLowering(uri, params.TextDocument.Text)
	return nil
}

// textDocumentDidSave lowers the saved text in the background at once, for
// the diagnostics `nomi check` adds to the front end's (lowering.go),
// instead of after the last edit's delay.
func (s *Server) textDocumentDidSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	text := ""
	if params.Text != nil {
		text = *params.Text
	} else if snap := s.docs.Snapshot(uri); snap != nil {
		text = snap.Text
	} else {
		return nil
	}
	s.checkLowering(uri, text)
	return nil
}

func (s *Server) textDocumentDidChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	changed := false
	for _, change := range params.ContentChanges {
		if c, ok := change.(protocol.TextDocumentContentChangeEventWhole); ok {
			s.docs.SetText(uri, c.Text)
			changed = true
		}
	}
	if changed {
		s.scheduleAnalysis(uri, s.sched.editDelay(), ctx.Notify)
		s.scheduleLowering(uri)
	}
	return nil
}

// textDocumentDidClose drops the document's analysis. A file that exists
// on disk is indexed again and queued for a diagnostics pass of its disk
// text; any other has its diagnostics cleared.
func (s *Server) textDocumentDidClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	s.cancelAnalysis(uri)
	s.literals.forget(uri)
	s.lowering.forget(uri)
	s.pipeTokens.forget(uri)
	s.lines.forget(uri)
	s.occurrences.forget(uri)
	onDisk := s.closeDocument(uri)
	if onDisk && s.notify != nil {
		s.queueClosedDiagnostics(uri)
		return nil
	}
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         params.TextDocument.URI,
		Diagnostics: []protocol.Diagnostic{},
	})
	return nil
}

// closeDocument drops uri's analysis and reports whether the file is on
// disk. The unlock is deferred: Close may index the disk text, and a panic
// there must not leave publishMu held (recover.go).
func (s *Server) closeDocument(uri string) bool {
	s.sched.publishMu.Lock()
	defer s.sched.publishMu.Unlock()
	// A reopened document counts its versions from 1 again.
	delete(s.sched.published, uri)
	return s.docs.Close(uri)
}

func (s *Server) textDocumentDocumentSymbol(ctx *glsp.Context, params *protocol.DocumentSymbolParams) (any, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}
	syms := nodesToDocumentSymbols(doc.Nodes, doc.Analysis)
	toUTF16DocumentSymbols(s.lines.get(uri, doc.Content), syms)
	return syms, nil
}

func boolPtr(b bool) *bool { return &b }

// clientSnippetSupport reports whether the client accepts snippet
// completion items.
func clientSnippetSupport(params *protocol.InitializeParams) bool {
	td := params.Capabilities.TextDocument
	if td == nil || td.Completion == nil || td.Completion.CompletionItem == nil {
		return false
	}
	ss := td.Completion.CompletionItem.SnippetSupport
	return ss != nil && *ss
}

// initializeResult wraps protocol.InitializeResult to add LSP 3.17 fields.
type initializeResult struct {
	Capabilities initCapabilities                     `json:"capabilities"`
	ServerInfo   *protocol.InitializeResultServerInfo `json:"serverInfo,omitempty"`
}

type initCapabilities struct {
	protocol.ServerCapabilities
	InlayHintProvider bool `json:"inlayHintProvider,omitempty"`
}

func (s *Server) propagateAsync(uri string) {
	if s.notify == nil {
		return
	}

	s.propagateMu.Lock()
	if s.cancelPropagate != nil {
		s.cancelPropagate()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelPropagate = cancel
	s.propagateMu.Unlock()

	go func() {
		defer recoverPanic("propagating a change to "+uri, nil)
		// Fast-path bail: if a newer propagation has already cancelled
		// us before we even start the walk, skip the (potentially
		// expensive) PropagateChange entirely. The publish-loop check
		// below catches mid-walk cancellation; this one catches "the
		// goroutine sat in the runqueue for so long that we're
		// already stale."
		select {
		case <-ctx.Done():
			return
		default:
		}
		affected, closed := s.docs.PropagateChange(uri)
		s.queueClosedDiagnostics(closed...)
		for _, doc := range affected {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// Read errors/analysis through Snapshot — analyze() may
			// be running on this same doc in a later propagateAsync
			// goroutine that's queued behind us, and direct reads of
			// doc.Errors / doc.Analysis would race with its writes.
			snap := s.docs.Snapshot(doc.URI)
			if snap == nil || snap.Analysis == nil {
				continue
			}
			s.publishSnapshot(s.notify, snap)
		}
	}()
}

func (s *Server) workspaceDidChangeWatchedFiles(ctx *glsp.Context, params *protocol.DidChangeWatchedFilesParams) error {
	for _, event := range params.Changes {
		uri := string(event.URI)
		if isFFIInputURI(uri) {
			s.publishFFIInvalidation(ctx)
			continue
		}
		if !isNomiURI(uri) {
			continue
		}

		switch event.Type {
		case protocol.FileChangeTypeCreated:
			if s.docs.Covers(uri) && s.docs.IndexFile(uri) {
				s.queueClosedDiagnostics(uri)
			}
			// Re-analyze files that may have had unresolved imports to this file
			s.propagateNewFile(uri)

		case protocol.FileChangeTypeChanged:
			// An open document's editor text wins over the disk's.
			if s.docs.IsOpen(uri) {
				continue
			}
			if s.docs.Covers(uri) && s.docs.IndexFile(uri) {
				s.queueClosedDiagnostics(uri)
			}
			s.propagateAsync(uri)

		case protocol.FileChangeTypeDeleted:
			s.docs.RemoveWorkspaceFile(uri)
			ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
				URI:         protocol.DocumentUri(uri),
				Diagnostics: []protocol.Diagnostic{},
			})
			s.propagateAsync(uri)
		}
	}
	return nil
}

func isNomiURI(uri string) bool {
	return filepath.Ext(strings.TrimPrefix(uri, "file://")) == ".nomi"
}

func isFFIInputURI(uri string) bool {
	path := strings.TrimPrefix(uri, "file://")
	base := filepath.Base(path)
	return filepath.Ext(path) == ".go" || base == "go.mod" || base == "go.sum"
}

func (s *Server) publishFFIInvalidation(ctx *glsp.Context) {
	open, closed := s.docs.InvalidateFFIExterns()
	s.queueClosedDiagnostics(closed...)
	for _, doc := range open {
		snap := s.docs.Snapshot(doc.URI)
		if snap == nil || snap.Analysis == nil {
			continue
		}
		s.publishSnapshot(ctx.Notify, snap)
	}
}

// propagateNewFile re-analyzes the open documents, any of which may import
// the new file, and queues the closed files that import it, transitively.
func (s *Server) propagateNewFile(newURI string) {
	if s.notify == nil {
		return
	}
	go func() {
		defer recoverPanic("propagating new file "+newURI, nil)
		s.queueClosedDiagnostics(s.docs.Importers(newURI)...)
		affected := s.docs.ReanalyzeOpen(newURI)
		for _, doc := range affected {
			snap := s.docs.Snapshot(doc.URI)
			if snap == nil || snap.Analysis == nil {
				continue
			}
			s.publishSnapshot(s.notify, snap)
		}
	}()
}

func (s *Server) textDocumentInlayHint(_ *glsp.Context, params *InlayHintParams) ([]InlayHint, error) {
	uri := params.TextDocument.URI
	doc := s.docs.Snapshot(uri)
	if doc == nil || doc.Analysis == nil {
		return nil, nil
	}
	req := hintRequest{
		settings:  s.settings.inlayHints(),
		content:   doc.Content,
		tokens:    func() *lexedText { return s.pipeTokens.get(uri, doc.Content) },
		startLine: int(params.Range.Start.Line) + 1,
		endLine:   int(params.Range.End.Line) + 1,
	}
	hints := req.collect(doc.Analysis, doc.Nodes)
	toUTF16InlayHints(s.lines.get(uri, doc.Content), hints)
	return hints, nil
}
