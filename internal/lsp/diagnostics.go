package lsp

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/testdirectives"
	"github.com/nomi-language/nomi/internal/token"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func parseErrorsToDiagnostics(errs []parser.ParseError) []protocol.Diagnostic {
	diags := make([]protocol.Diagnostic, 0, len(errs))
	severity := protocol.DiagnosticSeverityError
	source := "nomi"
	for _, e := range errs {
		line := uint32(0)
		if e.Line > 0 {
			line = uint32(e.Line - 1)
		}
		col := uint32(0)
		if e.Col > 0 {
			col = uint32(e.Col - 1)
		}
		diags = append(diags, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: line, Character: col},
				End:   protocol.Position{Line: line, Character: col},
			},
			Severity: &severity,
			Source:   &source,
			Message:  e.Message,
		})
	}
	return diags
}

func typeErrorsToDiagnostics(errs []analysis.TypeError) []protocol.Diagnostic {
	return typeErrorsToDiagnosticsIn("", errs)
}

// typeErrorsToDiagnosticsIn converts errs, found in the document at uri.
// Each diagnostic's range is the error's span (a point when it has none), its
// message the error's message with one `help:` line per hint, and its
// relatedInformation the error's related locations; a related location in
// the document itself needs uri, and is left out when uri is "".
func typeErrorsToDiagnosticsIn(uri string, errs []analysis.TypeError) []protocol.Diagnostic {
	diags := make([]protocol.Diagnostic, 0, len(errs))
	severity := protocol.DiagnosticSeverityError
	source := "nomi-type"
	for _, e := range errs {
		diag := protocol.Diagnostic{
			Range:    errorRange(e.Line, e.Col, e.EndLine, e.EndCol),
			Severity: &severity,
			Source:   &source,
			Message:  diagnosticMessage(e),
		}
		if e.Code != "" {
			diag.Code = &protocol.IntegerOrString{Value: e.Code}
		}
		for _, r := range e.Related {
			target := uri
			if r.File != "" {
				if !filepath.IsAbs(r.File) {
					continue
				}
				target = pathToURI(r.File)
			}
			if target == "" {
				continue
			}
			diag.RelatedInformation = append(diag.RelatedInformation, protocol.DiagnosticRelatedInformation{
				Location: protocol.Location{URI: protocol.DocumentUri(target), Range: errorRange(r.Line, r.Col, r.EndLine, r.EndCol)},
				Message:  r.Message,
			})
		}
		diags = append(diags, diag)
	}
	return diags
}

// diagnosticMessage is the text a diagnostic shows for e: its message, then
// one `help:` line per hint.
func diagnosticMessage(e analysis.TypeError) string {
	msg := e.Message
	for _, h := range e.Hints {
		msg += "\nhelp: " + h
	}
	return msg
}

// diagnosticHeadline is a published diagnostic's message without the hint
// and note lines diagnosticMessage and foldRelated add: the analysis error's
// own message.
func diagnosticHeadline(msg string) string {
	head, _, _ := strings.Cut(msg, "\n")
	return head
}

// errorRange is the 0-based protocol range of a 1-based span with byte
// columns; a zero end is a point.
func errorRange(line, col, endLine, endCol int) protocol.Range {
	toPos := func(l, c int) protocol.Position {
		p := protocol.Position{}
		if l > 0 {
			p.Line = uint32(l - 1)
		}
		if c > 0 {
			p.Character = uint32(c - 1)
		}
		return p
	}
	start := toPos(line, col)
	end := start
	if endLine > 0 && (endLine > line || (endLine == line && endCol > col)) {
		end = toPos(endLine, endCol)
	}
	return protocol.Range{Start: start, End: end}
}

// foldRelated moves each diagnostic's related locations into its message, as
// `note:` lines, for a client that does not read relatedInformation.
func foldRelated(diags []protocol.Diagnostic) {
	for i := range diags {
		for _, r := range diags[i].RelatedInformation {
			diags[i].Message += fmt.Sprintf("\nnote: %s (%s:%d:%d)", r.Message,
				filepath.Base(uriToPath(string(r.Location.URI))), r.Location.Range.Start.Line+1, r.Location.Range.Start.Character+1)
		}
		diags[i].RelatedInformation = nil
	}
}

func buildDiagnostics(uri string, parseErrs []parser.ParseError, typeErrs []analysis.TypeError) []protocol.Diagnostic {
	diags := parseErrorsToDiagnostics(parseErrs)
	diags = append(diags, typeErrorsToDiagnosticsIn(uri, typeErrs)...)
	return diags
}

// buildPublishedDiagnostics is uri's diagnostics for a client that reads
// relatedInformation.
func buildPublishedDiagnostics(uri string, content string, nodes []ast.Node, parseErrs []parser.ParseError, typeErrs []analysis.TypeError) []protocol.Diagnostic {
	return buildPublishedDiagnosticsFor(uri, content, nil, nodes, parseErrs, typeErrs, true)
}

// buildPublishedDiagnosticsFor is uri's diagnostics: the parser's, the
// analysis's, and the dbg and todo warnings. A point error spans the token it
// names (analysis.CompleteSpans). related reports whether the client reads
// relatedInformation; when it does not, related locations become `note:`
// lines of the message.
//
// tokens, when not nil, returns content's tokens, which the server may hold
// already; otherwise content is lexed here, at most once.
func buildPublishedDiagnosticsFor(uri string, content string, tokens func() []token.Token, nodes []ast.Node, parseErrs []parser.ParseError, typeErrs []analysis.TypeError, related bool) []protocol.Diagnostic {
	var toks []token.Token
	lexed := func() []token.Token {
		if toks == nil {
			if tokens != nil {
				toks = tokens()
			} else {
				toks = lexer.Lex(content)
			}
		}
		return toks
	}
	typeErrs = filterExpectedFixtureTypeErrors(uri, content, typeErrs)
	if content != "" {
		typeErrs = analysis.CompleteSpansFrom(lexed, typeErrs)
	}
	diags := buildDiagnostics(uri, parseErrs, typeErrs)
	if content != "" {
		widenParseErrors(lexed, diags[:len(parseErrs)])
	}
	if !related {
		foldRelated(diags)
	}
	todos, dbgs := analysis.TodosAndDbgs(nodes)
	diags = append(diags, dbgWarningDiagnostics(dbgs)...)
	diags = append(diags, todoWarningDiagnostics(todos)...)
	// Diagnostic columns are byte-based (analyzer positions); convert to the
	// UTF-16 columns the protocol expects so a squiggle after a non-ASCII
	// character on its line lands on the right span. A related location in
	// this document is converted the same way; one in another file is left
	// in bytes.
	if content != "" {
		lines := newLineIndex(content)
		for i := range diags {
			diags[i].Range = lines.utf16Range(diags[i].Range)
			for j, r := range diags[i].RelatedInformation {
				if string(r.Location.URI) == uri {
					diags[i].RelatedInformation[j].Location.Range = lines.utf16Range(r.Location.Range)
				}
			}
		}
	}
	return diags
}

// clientRelatedInformation reports whether the client reads a diagnostic's
// relatedInformation.
func clientRelatedInformation(params *protocol.InitializeParams) bool {
	td := params.Capabilities.TextDocument
	if td == nil || td.PublishDiagnostics == nil || td.PublishDiagnostics.RelatedInformation == nil {
		return false
	}
	return *td.PublishDiagnostics.RelatedInformation
}

// publish publishes uri's diagnostics from its current snapshot, if it has
// one with an analysis.
func (s *Server) publish(notify glsp.NotifyFunc, uri string) {
	if snap := s.docs.Snapshot(uri); snap != nil && snap.Analysis != nil {
		s.publishSnapshot(notify, snap)
	}
}

// publishSnapshot publishes snap's diagnostics: the parser's and the
// analysis's, then the static typed literals whose handler rejects them
// (literal_eval.go). The literals' part never waits on an evaluation: what is
// not cached yet is evaluated in the background, which publishes again.
func (s *Server) publishSnapshot(notify glsp.NotifyFunc, snap *analysis.DocSnapshot) {
	// An open document's tokens come from the server's cache, which inlay
	// hints read too; a closed file's are lexed for this publish only.
	var tokens func() []token.Token
	if snap.Open {
		tokens = func() []token.Token { return s.pipeTokens.get(snap.URI, snap.Content).toks }
	}
	diags := buildPublishedDiagnosticsFor(snap.URI, snap.Content, tokens, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors, s.relatedInformation)
	if lits := s.literalDiagnostics(snap, false); len(lits) > 0 {
		lines := newLineIndex(snap.Content)
		for _, d := range lits {
			d.Range = lines.utf16Range(d.Range)
			diags = append(diags, d)
		}
	}
	notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         protocol.DocumentUri(snap.URI),
		Diagnostics: diags,
	})
}

// dbgWarningDiagnostics reports every `dbg` as a warning: the program checks,
// runs and tests with it, and `nomi build` refuses it. The range is the
// keyword.
func dbgWarningDiagnostics(dbgs []*ast.Dbg) []protocol.Diagnostic {
	var diags []protocol.Diagnostic
	severity := protocol.DiagnosticSeverityWarning
	source := "nomi-dbg"
	code := "debug-expression"
	for _, dbg := range dbgs {
		line := uint32(0)
		if dbg.Line > 0 {
			line = uint32(dbg.Line - 1)
		}
		col := uint32(0)
		if dbg.Col > 0 {
			col = uint32(dbg.Col - 1)
		}
		diags = append(diags, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: line, Character: col},
				End:   protocol.Position{Line: line, Character: col + uint32(len("dbg"))},
			},
			Severity: &severity,
			Source:   &source,
			Code:     &protocol.IntegerOrString{Value: code},
			Message:  "`dbg` is debug-only code; remove it before production",
		})
	}
	return diags
}

// todoWarningDiagnostics reports every `todo` as a warning, as
// dbgWarningDiagnostics reports every `dbg`: the program checks and runs with
// it, `nomi build` refuses it, and the warning is the list of what is left to
// write. The range is the keyword; the message carries the reason.
func todoWarningDiagnostics(todos []*ast.Todo) []protocol.Diagnostic {
	var diags []protocol.Diagnostic
	severity := protocol.DiagnosticSeverityWarning
	source := "nomi-todo"
	code := "todo-expression"
	for _, todo := range todos {
		line := uint32(0)
		if todo.Line > 0 {
			line = uint32(todo.Line - 1)
		}
		col := uint32(0)
		if todo.Col > 0 {
			col = uint32(todo.Col - 1)
		}
		diags = append(diags, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: line, Character: col},
				End:   protocol.Position{Line: line, Character: col + uint32(len("todo"))},
			},
			Severity: &severity,
			Source:   &source,
			Code:     &protocol.IntegerOrString{Value: code},
			Message:  todoMessage(todo),
		})
	}
	return diags
}

// todoMessage is `todo "reason"`, or a note that the code is not written yet
// for a bare `todo`.
func todoMessage(todo *ast.Todo) string {
	if todo.Reason == nil {
		return "`todo`: code not written yet; `nomi build` refuses it"
	}
	return "todo " + strconv.Quote(todo.Reason.Value)
}

func filterExpectedFixtureTypeErrors(uri string, content string, errs []analysis.TypeError) []analysis.TypeError {
	if len(errs) == 0 || !isExpectedDiagnosticFixture(uri) {
		return errs
	}
	directives := testdirectives.Parse(content)
	if len(directives.TypeErrors) == 0 {
		return errs
	}

	matchedErrs := make([]bool, len(errs))
	var out []analysis.TypeError
	for _, expected := range directives.TypeErrors {
		found := false
		for i, err := range errs {
			if matchedErrs[i] {
				continue
			}
			if diagnosticMatchesExpectedTypeError(err, expected.Text) {
				matchedErrs[i] = true
				found = true
				break
			}
		}
		if !found {
			out = append(out, analysis.TypeError{
				Line:    expected.Line,
				Col:     1,
				Message: fmt.Sprintf("expected type error did not occur: %s", expected.Text),
			})
		}
	}
	for i, err := range errs {
		if !matchedErrs[i] {
			out = append(out, err)
		}
	}
	return out
}

func diagnosticMatchesExpectedTypeError(err analysis.TypeError, expected string) bool {
	return strings.Contains(err.Error(), expected) || strings.Contains(err.Message, expected)
}

// isExpectedDiagnosticFixture names the paths whose files MAY declare an
// `// expect type-error:` directive and have the matching diagnostic
// suppressed.
//
// Both entries are fixtures that are deliberately not clean, and the gate is a
// path check rather than a blanket allowance because the directive must not
// become a way to silence a diagnostic in real source. It stays OPT-IN per
// file: a fixture under one of these paths is filtered only for the errors it
// spells out, and an unmatched expectation is itself reported.
//
//   - `internal/analysis/testdata/orphan_violator*` — orphan-rule fixtures that
//     the analyzer must reject.
//   - `internal/irbuild/testdata` — IR builder fixtures. Most are valid Nomi that
//     the BUILDER refuses, which publishes nothing; but a fixture can need
//     source the ANALYZER rejects. `appnostage/reader.nomi` reads an
//     application field with no entry boot that returns its type, which the
//     analyzer rejects.
func isExpectedDiagnosticFixture(uri string) bool {
	path := strings.TrimPrefix(uri, "file://")
	path = strings.ReplaceAll(path, "\\", "/")
	return strings.Contains(path, "/internal/analysis/testdata/orphan_violator") ||
		strings.Contains(path, "/internal/irbuild/testdata/")
}

// widenParseErrors gives each parse error the extent of the token it is
// reported at, as analysis.CompleteSpans gives a type error.
func widenParseErrors(tokens func() []token.Token, diags []protocol.Diagnostic) {
	points := make([]analysis.TypeError, len(diags))
	for i, d := range diags {
		points[i] = analysis.TypeError{Line: int(d.Range.Start.Line) + 1, Col: int(d.Range.Start.Character) + 1}
	}
	for i, e := range analysis.CompleteSpansFrom(tokens, points) {
		diags[i].Range = errorRange(e.Line, e.Col, e.EndLine, e.EndCol)
	}
}
