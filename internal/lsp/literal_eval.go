package lsp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TYPED LITERALS THE EDITOR EVALUATES.
//
// A typed literal with no `${...}` is a constant: `Date"2026-13-04"` is the
// same Err every time the program reaches it. The language server evaluates
// each such literal's handler while the user edits, reports the double-quoted
// ones whose handler answers Err, and shows the value of the others on hover.
// Run time is unchanged: the literal still evaluates to its Err when the
// program runs. A backtick literal whose handler can fail is checked at
// compile time instead (vmhost's checkLiterals), and its failure is one of
// the lowering diagnostics (lowering.go), so it is only shown here.
//
// It runs on the engine, not beside it. The open buffer is loaded as its file
// (vmhost.LoadFileSource) with one probe function per literal appended:
//
//	fn nomi_literal_probe_0(): Result<String, String> {
//	    case Date"2026-13-04" {
//	        Ok(v) -> Ok(Debug.inspect(v))
//	        Err(e) -> Err(Debug.inspect(e))
//	    }
//	}
//
// so the front end resolves the prefix exactly as at the literal's own site,
// irbuild lowers the handler and the Debug renderings, and the VM runs each
// probe through vmhost's Program.Evaluate. That refuses a probe that can
// reach an effect before running it (vm.Machine.Effects, with its one list of
// pure crossings) and runs the rest under a step and time limit. A handler
// whose result is not a Result gets `Ok(Debug.inspect(<literal>))`, and is
// never reported, only shown.
//
// Anything that stops a literal being evaluated (the buffer does not check,
// the handler has effects, it runs out of steps, its rendering blocks) skips
// that literal silently.
//
// The work happens off the diagnostics path: a publish reports what is
// cached and starts one background evaluation of the rest, which publishes
// again when it finishes if the buffer has not changed. Results are cached by
// the handler's identity and the literal's text, for the buffer's text; a
// stdlib handler's results are kept for the process, since its source cannot
// change.

const (
	// literalSteps bounds one literal's evaluation: calls plus backward
	// branches. A valid Date literal takes under a hundred.
	literalSteps = 200_000
	// literalTime is one literal's wall-clock backstop.
	literalTime = 100 * time.Millisecond
	// literalBudget is one buffer's evaluation time, load included; the
	// literals not reached in it are skipped for this text.
	literalBudget = 2 * time.Second
)

// staticLiteral is one `<Type>"..."` with no `${...}` in an open buffer.
type staticLiteral struct {
	// start and end are the byte offsets of the tag and of the end of the
	// closing delimiter.
	start, end int
	tag        string
	// text is the literal's source, tag to closing delimiter.
	text string
	// key is the cache key: the handler's identity and text.
	key string
	// std reports a stdlib handler, whose results outlive the buffer.
	std bool
	// result reports a handler returning a Result; typ is its return type.
	result bool
	typ    string
}

// literalResult is one literal's evaluation.
type literalResult struct {
	// skipped is a literal that was not evaluated, and is not retried for
	// this text; why says what stopped it.
	skipped bool
	why     string
	// invalid reports a handler that answered Err; text is the Debug
	// rendering of the Err payload, or of the value (an Ok payload for a
	// Result handler).
	invalid bool
	text    string
}

// literalEvals caches evaluations and tracks the one running per buffer.
type literalEvals struct {
	mu sync.Mutex
	// std holds stdlib handlers' results, by key.
	std map[string]literalResult
	// docs holds the other results of each buffer's latest evaluated text.
	docs map[string]*docLiteralEvals
	// running is the background evaluation running per buffer.
	running map[string]*literalRun
}

// literalRun is one background evaluation of a buffer's text.
type literalRun struct {
	content string
	cancel  context.CancelFunc
}

// cancel stops the buffer's background evaluation, if one is running. The
// scheduler calls it for every edit (docsched.go).
func (c *literalEvals) cancel(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.running[uri]; r != nil {
		r.cancel()
		delete(c.running, uri)
	}
}

type docLiteralEvals struct {
	content string
	results map[string]literalResult
}

func (c *literalEvals) lookup(uri, content string, lit staticLiteral) (literalResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lit.std {
		r, ok := c.std[lit.key]
		return r, ok
	}
	d := c.docs[uri]
	if d == nil || d.content != content {
		return literalResult{}, false
	}
	r, ok := d.results[lit.key]
	return r, ok
}

func (c *literalEvals) store(uri, content string, lits []staticLiteral, results map[string]literalResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.std == nil {
		c.std = map[string]literalResult{}
		c.docs = map[string]*docLiteralEvals{}
	}
	d := c.docs[uri]
	if d == nil || d.content != content {
		d = &docLiteralEvals{content: content, results: map[string]literalResult{}}
		c.docs[uri] = d
	}
	for _, lit := range lits {
		r, ok := results[lit.key]
		if !ok {
			continue
		}
		if lit.std {
			c.std[lit.key] = r
		} else {
			d.results[lit.key] = r
		}
	}
}

// forget drops a closed buffer's results.
func (c *literalEvals) forget(uri string) {
	c.cancel(uri)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.docs, uri)
}

// staticLiterals finds the buffer's typed literals with no `${...}` whose
// handler the analysis resolved. It reads the tokens, where such a literal is
// one token.
func staticLiterals(content string, fa *analysis.FileAnalysis) []staticLiteral {
	if fa == nil || fa.ProjectImpls == nil || !strings.ContainsAny(content, "\"`") {
		return nil
	}
	offs := lineOffsets(content)
	var out []staticLiteral
	for _, tok := range lexer.Lex(content) {
		var delim string
		switch tok.Type {
		case token.TAGGED_STRING_LITERAL:
			delim = `"`
		case token.TAGGED_TRIPLE_STRING_LITERAL:
			delim = `"""`
		case token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL:
			delim = "`"
		default:
			continue
		}
		open := posToOffset(offs, tok.Line, tok.Col)
		start := open - len(tok.Tag)
		if start < 0 || !strings.HasPrefix(content[open:], delim) || content[start:open] != tok.Tag {
			continue
		}
		end, ok := literalEnd(content, open, delim)
		if !ok {
			continue
		}
		text := content[start:end]
		if strings.Contains(text, "\n") && strings.Contains(text, "//!") {
			// A multi-line literal in a `//!` prompt: its lines carry the
			// prompt marker, so its text is not the literal's.
			continue
		}
		lit, ok := literalHandler(fa, tok, text)
		if !ok {
			continue
		}
		lit.start, lit.end = start, end
		out = append(out, lit)
	}
	return out
}

// literalEnd is the offset just past the delimiter closing the literal opened
// at open.
func literalEnd(content string, open int, delim string) (int, bool) {
	for i := open + len(delim); i < len(content); i++ {
		c := content[i]
		if delim != "`" && c == '\\' {
			i++
			continue
		}
		if delim == `"` && c == '\n' {
			return 0, false
		}
		if strings.HasPrefix(content[i:], delim) {
			return i + len(delim), true
		}
	}
	return 0, false
}

// literalHandler fills in what the analysis knows about the literal's
// handler: which `from_fragments` the tag resolved to, the file declaring it,
// and its return type.
func literalHandler(fa *analysis.FileAnalysis, tok token.Token, text string) (staticLiteral, bool) {
	sym := fa.References[analysis.Pos{Line: tok.Line, Col: tok.Col - len(tok.Tag)}]
	if sym == nil {
		return staticLiteral{}, false
	}
	fd, ok := sym.DispatchImpl.(*ast.FuncDef)
	if !ok || fd == nil {
		return staticLiteral{}, false
	}
	home, ok := fa.ProjectImpls.ImplFiles[fd]
	if !ok {
		return staticLiteral{}, false
	}
	ft, ok := fa.ProjectImpls.ImplFuncTypes[fd].(*analysis.FuncType)
	if !ok || ft.Return == nil {
		return staticLiteral{}, false
	}
	lit := staticLiteral{
		tag:  tok.Tag,
		text: text,
		key:  home + "." + tok.Tag + "\x00" + text,
		std:  strings.HasPrefix(home, "std/"),
		typ:  ft.Return.String(),
	}
	raw := tok.Type == token.RAW_TAGGED_STRING_LITERAL || tok.Type == token.RAW_TAGGED_TRIPLE_STRING_LITERAL
	if ok, _ := analysis.RawLiteralType(raw, ft.Return); ok != nil {
		// A backtick literal checked at compile time is its handler's Ok
		// payload: the lowering check reports one that fails
		// (loweringDiagnostics), and a buffer holding an invalid one does not
		// load, so its probes are skipped.
		lit.typ = ok.String()
		return lit, true
	}
	if et, ok := analysis.ResolveTypeVar(ft.Return).(*analysis.EnumType); ok && et.Name == "Result" && len(et.TypeArgs) == 2 {
		lit.result = true
	}
	return lit, true
}

// literalProbes is the buffer with one probe function per distinct literal
// appended, and the distinct keys in buffer order; probe i is named
// nomi_literal_probe_<i>.
func literalProbes(content string, lits []staticLiteral) (string, []string) {
	var b strings.Builder
	b.WriteString(content)
	b.WriteString("\n")
	seen := map[string]bool{}
	var keys []string
	for _, lit := range lits {
		if seen[lit.key] {
			continue
		}
		seen[lit.key] = true
		name := fmt.Sprintf("nomi_literal_probe_%d", len(keys))
		keys = append(keys, lit.key)
		fmt.Fprintf(&b, "\nfn %s(): Result<String, String> {\n", name)
		if lit.result {
			fmt.Fprintf(&b, "    case %s {\n        Ok(v) -> Ok(Debug.inspect(v))\n        Err(e) -> Err(Debug.inspect(e))\n    }\n", lit.text)
		} else {
			fmt.Fprintf(&b, "    Ok(Debug.inspect(%s))\n", lit.text)
		}
		b.WriteString("}\n")
	}
	return b.String(), keys
}

// evaluateLiterals evaluates lits in the buffer at path, within
// literalBudget. Every literal gets a result; one not evaluated is skipped.
// It stops between literals when ctx ends.
func evaluateLiterals(ctx context.Context, path, content string, lits []staticLiteral) map[string]literalResult {
	results := map[string]literalResult{}
	skip := func(why string) {
		for _, lit := range lits {
			if _, done := results[lit.key]; !done {
				results[lit.key] = literalResult{skipped: true, why: why}
			}
		}
	}
	if len(lits) == 0 || path == "" {
		skip("no file")
		return results
	}
	deadline := time.Now().Add(literalBudget)
	src, keys := literalProbes(content, lits)
	p, err := vmhost.LoadFileSource(path, src, vmhost.WithUnusedBindingsAllowed(), vmhost.WithOutput(io.Discard))
	if err != nil {
		skip("load: " + err.Error())
		return results
	}
	for i, key := range keys {
		now := time.Now()
		if now.After(deadline) || ctx.Err() != nil {
			break
		}
		limit := now.Add(literalTime)
		if limit.After(deadline) {
			limit = deadline
		}
		v, err := p.Evaluate(ctx, fmt.Sprintf("nomi_literal_probe_%d", i), vmhost.EvalLimits{Steps: literalSteps, Deadline: limit})
		var effectful *vmhost.Effectful
		switch {
		case errors.Is(err, vmhost.ErrEvalLimit):
			results[key] = literalResult{skipped: true, why: "limit"}
		case errors.As(err, &effectful):
			results[key] = literalResult{skipped: true, why: "effects: " + strings.Join(effectful.Effects, "; ")}
		case err != nil:
			results[key] = literalResult{skipped: true, why: err.Error()}
		default:
			if r, ok := probeResult(v); ok {
				results[key] = r
			}
		}
	}
	skip("budget")
	return results
}

// safeEvaluateLiterals is evaluateLiterals for a background run. A panic
// records every literal as skipped, so publishing the buffer again does not
// start the evaluation again (recover.go).
func safeEvaluateLiterals(ctx context.Context, uri, path, content string, lits []staticLiteral) (results map[string]literalResult) {
	defer recoverPanic("evaluating typed literals in "+uri, func(*serverPanic) {
		results = map[string]literalResult{}
		for _, lit := range lits {
			results[lit.key] = literalResult{skipped: true, why: "internal error"}
		}
	})
	fault("literals")
	return evaluateLiterals(ctx, path, content, lits)
}

// probeResult reads a probe's Result<String, String>.
func probeResult(v vmhost.Value) (literalResult, bool) {
	rec, ok := v.(*rt.Record)
	if !ok || rec == nil || rec.Desc.Kind != rt.KindEnum || rec.NumFields() != 1 {
		return literalResult{}, false
	}
	text, ok := rec.Field(0).(string)
	if !ok {
		return literalResult{}, false
	}
	switch rec.Variant().Name {
	case "Ok":
		return literalResult{text: text}, true
	case "Err":
		return literalResult{invalid: true, text: text}, true
	}
	return literalResult{}, false
}

// literalResults answers each literal's cached result. Literals with none are
// evaluated now when wait is set, and otherwise by one background
// evaluation of the buffer, which publishes again when it finishes.
// literalEvaluation turns typed-literal evaluation on. It was off while
// nomi-lsp grew by megabytes per edit; that was every build's scopes kept
// alive under the shared stdlib scopes (Scope.MarkShared), and evaluation's
// program loads leaked through the same path.
var literalEvaluation = true

func (s *Server) literalResults(snap *analysis.DocSnapshot, lits []staticLiteral, wait bool) map[int]literalResult {
	out := map[int]literalResult{}
	if !literalEvaluation {
		return out
	}
	var missing []staticLiteral
	for i, lit := range lits {
		if r, ok := s.literals.lookup(snap.URI, snap.Content, lit); ok {
			out[i] = r
		} else {
			missing = append(missing, lit)
		}
	}
	if len(missing) == 0 {
		return out
	}
	path := uriToPath(snap.URI)
	if wait {
		s.literals.store(snap.URI, snap.Content, missing, evaluateLiterals(context.Background(), path, snap.Content, missing))
		for i, lit := range lits {
			if r, ok := s.literals.lookup(snap.URI, snap.Content, lit); ok {
				out[i] = r
			}
		}
		return out
	}
	if !snap.Open || s.notify == nil {
		return out
	}
	// One background evaluation per buffer, of the text being published.
	// The scheduler cancels it when a newer edit arrives; a run for an older
	// text still going is cancelled here. A finished run publishes again
	// through republishLiterals, which drops it unless its text is still the
	// latest analyzed and published one.
	uri, content, notify := snap.URI, snap.Content, s.notify
	s.literals.mu.Lock()
	if s.literals.running == nil {
		s.literals.running = map[string]*literalRun{}
	}
	if r := s.literals.running[uri]; r != nil {
		if r.content == content {
			s.literals.mu.Unlock()
			return out
		}
		r.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &literalRun{content: content, cancel: cancel}
	s.literals.running[uri] = run
	s.literals.mu.Unlock()
	go func() {
		defer cancel()
		results := safeEvaluateLiterals(ctx, uri, path, content, missing)
		s.literals.mu.Lock()
		if s.literals.running[uri] == run {
			delete(s.literals.running, uri)
		}
		s.literals.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		s.literals.store(uri, content, missing, results)
		s.republishLiterals(uri, content, notify)
	}()
	return out
}

// literalDiagnostics reports the buffer's static typed literals whose
// handler answers Err. Ranges are in byte columns.
func (s *Server) literalDiagnostics(snap *analysis.DocSnapshot, wait bool) []protocol.Diagnostic {
	if snap == nil || snap.Analysis == nil {
		return nil
	}
	lits := staticLiterals(snap.Content, snap.Analysis)
	if len(lits) == 0 {
		return nil
	}
	results := s.literalResults(snap, lits, wait)
	var diags []protocol.Diagnostic
	severity := protocol.DiagnosticSeverityError
	source := "nomi"
	offs := lineOffsets(snap.Content)
	for i, lit := range lits {
		r, ok := results[i]
		if !ok || r.skipped || !r.invalid {
			continue
		}
		diags = append(diags, protocol.Diagnostic{
			Range:    byteRange(offs, lit.start, lit.end),
			Severity: &severity,
			Source:   &source,
			Code:     &protocol.IntegerOrString{Value: "invalid-literal"},
			Message:  fmt.Sprintf("%s is invalid: %s", literalLabel(lit), r.text),
		})
	}
	return diags
}

// literalHover is the hover for a position inside a static typed literal's
// body: its value's Debug rendering and its type. onTag reports a position
// on the tag, where the handler's own hover leads.
func (s *Server) literalHover(snap *analysis.DocSnapshot, off int) (text string, rng protocol.Range, onTag, ok bool) {
	if snap == nil || snap.Analysis == nil {
		return "", protocol.Range{}, false, false
	}
	lits := staticLiterals(snap.Content, snap.Analysis)
	for i, lit := range lits {
		if off < lit.start || off >= lit.end {
			continue
		}
		results := s.literalResults(snap, lits[i:i+1], true)
		r, has := results[0]
		if !has || r.skipped {
			return "", protocol.Range{}, false, false
		}
		value := r.text
		if lit.result {
			if r.invalid {
				value = "Err(" + value + ")"
			} else {
				value = "Ok(" + value + ")"
			}
		}
		md := fmt.Sprintf("```nomi\n%s: %s\n```\n\n```text\n%s\n```", literalLabel(lit), lit.typ, value)
		return md, byteRange(lineOffsets(snap.Content), lit.start, lit.end), off < lit.start+len(lit.tag), true
	}
	return "", protocol.Range{}, false, false
}

// literalLabel is how a message names the literal: its text when it is one
// short line, and otherwise the tag with an elided body.
func literalLabel(lit staticLiteral) string {
	if !strings.Contains(lit.text, "\n") && len(lit.text) <= 60 {
		return lit.text
	}
	body := lit.text[len(lit.tag):]
	switch {
	case strings.HasPrefix(body, `"""`):
		return lit.tag + `"""…"""`
	case strings.HasPrefix(body, "`"):
		return lit.tag + "`…`"
	}
	return lit.tag + `"…"`
}

// byteRange is the protocol range of a byte span, in byte columns.
func byteRange(offs []int, start, end int) protocol.Range {
	sl, el := lineIndexOf(offs, start), lineIndexOf(offs, end)
	return protocol.Range{
		Start: protocol.Position{Line: uint32(sl), Character: uint32(start - offs[sl])},
		End:   protocol.Position{Line: uint32(el), Character: uint32(end - offs[el])},
	}
}
