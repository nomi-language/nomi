package lsp

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hoverdoc"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Completion answers in three stages. classifyCompletion (completion_syntax.go)
// reparses the document with a sentinel at the cursor and reads what the
// position is from the tree. The candidate sources below turn that position
// into candidates, reading names, scopes and types from the analyzed
// snapshot only. completionItems then filters the candidates against what was
// typed, ranks them and renders the protocol items.

// candidate is one thing completion may offer, before it is matched against
// the typed prefix and rendered.
type candidate struct {
	label string
	kind  protocol.CompletionItemKind
	// sym is the symbol the candidate names, when there is one: its
	// signature is the item's detail and its hover the item's
	// documentation.
	sym *analysis.Symbol
	// locality ranks where the name comes from: 0 a local, 1 the same
	// file, 2 an import, the prelude or an owner, 3 a name that needs an
	// import.
	locality int
	// order breaks ties before the label does, for a list whose order is
	// meaningful (a test group's keywords).
	order int
	// detail and doc stand in for a symbol's signature and hover when the
	// candidate has no symbol (a field, a keyword, a path).
	detail, doc string
	// typ is the candidate's value type, for ranking against the type the
	// position expects. A symbol's own type is used when typ is nil.
	typ analysis.Type
	// insert replaces the label as the inserted text; filter replaces it
	// as the text the client matches what was typed against.
	insert, filter string
	// noCall keeps a function from being inserted as a call.
	noCall bool
	// callable marks a function candidate with no symbol (one from a
	// closed file's index entry), and callParams are its required
	// parameters' names, as callParams reads them from a symbol's node.
	callable   bool
	callParams []string
	// snippet is the candidate's text for a client with snippet support;
	// insert (or the label) is the plain-text fallback. asIs marks a
	// multi-line text already indented for its place, which the client
	// must not re-indent.
	snippet string
	asIs    bool
	// importFrom is the import accepting the candidate adds, nil when it
	// needs none.
	importFrom *analysis.MissingImport
	// first ranks the candidate above every other: the type a parameter's
	// name names.
	first bool
}

func symbolKindToCompletionKind(kind analysis.SymbolKind) protocol.CompletionItemKind {
	switch kind {
	case analysis.SymbolFunction:
		return protocol.CompletionItemKindFunction
	case analysis.SymbolStruct:
		return protocol.CompletionItemKindStruct
	case analysis.SymbolEnum:
		return protocol.CompletionItemKindEnum
	case analysis.SymbolEnumVariant:
		return protocol.CompletionItemKindEnumMember
	case analysis.SymbolType:
		return protocol.CompletionItemKindClass
	case analysis.SymbolTypeAlias:
		return protocol.CompletionItemKindClass
	case analysis.SymbolInterface:
		return protocol.CompletionItemKindInterface
	case analysis.SymbolParam:
		return protocol.CompletionItemKindVariable
	case analysis.SymbolBinding:
		return protocol.CompletionItemKindVariable
	case analysis.SymbolOnce:
		return protocol.CompletionItemKindConstant
	case analysis.SymbolField:
		return protocol.CompletionItemKindField
	case analysis.SymbolModule:
		return protocol.CompletionItemKindModule
	case analysis.SymbolInterfaceMethod:
		return protocol.CompletionItemKindMethod
	default:
		return protocol.CompletionItemKindText
	}
}

// completionRequest carries one request's inputs through the sources.
type completionRequest struct {
	s     *Server
	doc   *analysis.DocSnapshot
	fa    *analysis.FileAnalysis
	pos   analysis.Pos
	scope *analysis.Scope
	ctx   completionContext

	// doc's Content is the latest text, but its Nodes and fa may be of an
	// older text; positions maps the latest text's positions into theirs.
	positions completionPositions
	// lines indexes the lines of doc's Content.
	lines *lineIndex
	// parsed is doc's Content parsed, once a source needs it and reparsed
	// is set (textNodes).
	parsed   []ast.Node
	reparsed bool
	// expected is the type the position expects, nil when unknown.
	expected analysis.Type
	// incomplete marks the list as one the client should ask for again as
	// the word grows: a source that offers only to a typed prefix ran.
	incomplete bool
	// braceType is the struct a bare brace builds or patches, and braceHop
	// the brace's hop on the sentinel's path (completion_bare_brace.go).
	braceType analysis.Type
	braceHop  int
}

func (s *Server) textDocumentCompletion(_ *glsp.Context, params *protocol.CompletionParams) (any, error) {
	uri := string(params.TextDocument.URI)
	snap := s.docs.Snapshot(uri)
	if snap == nil {
		return nil, nil
	}
	// The request reads the latest text throughout, and names, scopes and
	// types from the latest finished analysis, which may be of an older
	// text (rpc.go's requestFreshness). positions maps between the two.
	positions := newCompletionPositions(snap.Content, snap.Text)
	view := *snap
	view.Content = snap.Text
	doc := &view
	lines := s.lines.get(uri, doc.Content)
	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  lines.byteCol(params.Position.Line, params.Position.Character),
	}
	if posInInlineGo(doc.Nodes, positions.scopePos(pos)) {
		prefix := getPrefix(doc.Content, int(params.Position.Line), int(params.Position.Character))
		return &protocol.CompletionList{Items: inlineGoHelperCompletions(prefix)}, nil
	}
	if doc.Analysis == nil {
		return nil, nil
	}
	off := posToOffset(lines.starts, pos.Line, pos.Col)
	if off > len(doc.Content) {
		off = len(doc.Content)
	}
	trigger := triggerCharacter(params)
	if !triggerMayOpen(trigger, doc.Content, off) {
		return nil, nil
	}
	if trigger == "}" {
		// An auto-pair plugin typed the `}` of `Point{}` right after the
		// user's `{`, and a client that reports only the last character
		// typed (blink.cmp) sends that instead: answer it as the `{`.
		trigger = "{"
	}
	// A literal body is read from the latest text (doc.Content here);
	// literalCompletions reads only names from the analysis.
	if lb, ok := literalBodyAt(doc.Content, off); ok {
		return &protocol.CompletionList{Items: s.literalCompletions(doc, lb)}, nil
	}
	if trigger == `"` || trigger == "`" {
		return nil, nil
	}
	r := &completionRequest{s: s, doc: doc, fa: doc.Analysis, pos: pos, positions: positions, lines: lines}
	r.scope = doc.Analysis.ScopeAt(positions.scopePos(pos))
	if r.scope == nil {
		r.scope = doc.Analysis.ModuleScope
	}
	r.ctx = classifyCompletion(doc.Content, off)
	r.promoteBareBrace()
	if (trigger == "{" || trigger == ",") && r.ctx.kind != ctxStructField && r.ctx.kind != ctxPatternField {
		return nil, nil
	}
	if trigger == ":" && r.ctx.kind != ctxType {
		return nil, nil
	}
	r.expected = r.expectedType()
	items := r.items(r.candidates())
	return &protocol.CompletionList{IsIncomplete: r.incomplete, Items: items}, nil
}

// triggerCharacter is the character that opened the request, or "" for an
// explicit or typed-word request.
func triggerCharacter(params *protocol.CompletionParams) string {
	if params.Context == nil || params.Context.TriggerKind != protocol.CompletionTriggerKindTriggerCharacter || params.Context.TriggerCharacter == nil {
		return ""
	}
	return *params.Context.TriggerCharacter
}

// triggerMayOpen decides, from the text alone, whether a trigger character
// can lead anywhere, so the common `>` of `->` or a comparison and the `{`
// of a block cost nothing: `>` only completes the `|>` of a pipe, and `{`
// only a struct literal's fields, whose `{` follows the type's name
// (`Point{`) or stands where a value goes (braceMayOpenLiteral), a quote or backtick only a typed literal's body (`Date"`),
// and `:` only an annotation, whose `:` follows the annotated name.
func triggerMayOpen(trigger, content string, off int) bool {
	switch trigger {
	case ">":
		return off >= 2 && content[off-2:off] == "|>"
	case "{":
		return off >= 2 && content[off-1] == '{' && braceMayOpenLiteral(content, off-1)
	case ",":
		// Only the next field of a struct literal or pattern; the
		// classification decides, so a comma in a call, a list or a tuple
		// opens nothing.
		return off >= 1 && content[off-1] == ','
	case "}":
		// Only the `}` an auto-pair plugin closes `Point{` or `{` with, the
		// cursor between the braces; a `}` closing a block opens nothing.
		return off >= 2 && off < len(content) && content[off-1] == '{' && content[off] == '}' && braceMayOpenLiteral(content, off-1)
	case `"`, "`":
		// A typed literal's opener follows its type's name (`Date"`).
		return off >= 2 && content[off-1] == trigger[0] && isWordByte(content[off-2])
	case ":":
		// An annotation's `:` follows the annotated name (`game:`) or a
		// signature's `)` (`fn f():`).
		return off >= 2 && content[off-1] == ':' && (isWordByte(content[off-2]) || content[off-2] == ')')
	}
	return true
}

// braceMayOpenLiteral reports, from the text alone, whether the `{` at brace
// can open a struct literal: right after the type's name (`Point{`), or where
// a value goes and a bare brace may build the struct the position expects,
// after `:`, `=`, `(`, `,`, `[`, a case arm's `->`, `return`, or at the start
// of a line (a block's tail). A function's, an if's or a case's `{` follows
// a `)`, a type or a condition and a space, so it costs nothing;
// classification decides the rest.
func braceMayOpenLiteral(content string, brace int) bool {
	if brace >= 1 && isWordByte(content[brace-1]) {
		return true
	}
	i := brace
	for i > 0 && (content[i-1] == ' ' || content[i-1] == '\t') {
		i--
	}
	if i == 0 {
		return false
	}
	switch content[i-1] {
	case ':', '=', '(', ',', '[', '\n':
		return true
	case '>':
		return i >= 2 && content[i-2] == '-'
	}
	return strings.HasSuffix(content[:i], "return") && (i == len("return") || !isWordByte(content[i-len("return")-1]))
}

// candidates dispatches on the position.
func (r *completionRequest) candidates() []candidate {
	switch r.ctx.kind {
	case ctxTopLevel:
		return r.shapedKeywordCandidates(topLevelKeywords)
	case ctxStatement:
		out := append(r.scopeCandidates(valueSymbol), r.shapedKeywordCandidates(statementKeywords)...)
		out = append(out, r.constructorCandidates()...)
		return append(out, r.autoImportCandidates(true, true)...)
	case ctxExpr:
		out := append(r.scopeCandidates(valueSymbol), r.shapedKeywordCandidates(expressionKeywords)...)
		out = append(out, r.constructorCandidates()...)
		out = append(out, r.namedArgCandidates()...)
		return append(out, r.autoImportCandidates(true, true)...)
	case ctxPipe:
		return r.pipeCandidates()
	case ctxTestBoot:
		return r.bootCandidates()
	case ctxParamName:
		return r.paramNameCandidates()
	case ctxType:
		if r.ctx.typeQualifier != "" {
			return r.moduleMemberCandidates(r.ctx.typeQualifier, typeSymbol)
		}
		var out []candidate
		if r.ctx.paramSlot == paramSlotType {
			// Ahead of the scope's own entry for the same type, which
			// the dedup in items then drops.
			if c, ok := r.paramTypeCandidate(); ok {
				out = append(out, c)
			}
		}
		out = append(out, r.scopeCandidates(func(sym *analysis.Symbol) bool {
			return typeSymbol(sym) || typeQualifierModule(sym)
		})...)
		return append(out, r.autoImportCandidates(true, false)...)
	case ctxCaseArm:
		return r.caseArmCandidates()
	case ctxStructField:
		return r.structFieldCandidates()
	case ctxPatternField:
		return r.patternFieldCandidates()
	case ctxDeriveIface:
		return r.deriveIfaceCandidates()
	case ctxDeriveType:
		return r.deriveTypeCandidates()
	case ctxPattern:
		return r.scopeCandidates(func(sym *analysis.Symbol) bool {
			return sym.Kind == analysis.SymbolEnumVariant || sym.Kind == analysis.SymbolEnum || sym.Kind == analysis.SymbolStruct
		})
	case ctxDotVariant:
		return r.variantCandidates()
	case ctxMember:
		return r.memberCandidates()
	case ctxTestGroup:
		return testGroupCandidates(r.ctx.groupHas, r.lineIndent())
	case ctxImportPath:
		return r.importPathCandidates()
	case ctxImportName:
		return r.importNameCandidates()
	case ctxImplItem:
		return r.implStubCandidates()
	}
	return nil
}

// items filters the candidates against the typed prefix, ranks them and
// renders the protocol items.
func (r *completionRequest) items(cands []candidate) []protocol.CompletionItem {
	type ranked struct {
		c     candidate
		match int
		fits  bool
	}
	var kept []ranked
	seen := map[string]bool{}
	for _, c := range cands {
		key := c.label
		if c.importFrom != nil {
			key += "\x00" + c.importFrom.ImportSpec()
		}
		if seen[key] {
			continue
		}
		name := c.label
		if c.filter != "" {
			name = c.filter
		}
		q, ok := matchQuality(r.ctx.prefix, name)
		if !ok {
			continue
		}
		seen[key] = true
		fits := r.expected != nil && c.kind != protocol.CompletionItemKindKeyword && r.typeFits(r.candidateType(c), r.expected)
		kept = append(kept, ranked{c, q, fits})
	}
	// Ranking: the type a parameter's name names first, then a candidate
	// whose type fits the expected type, then how well it matches what was
	// typed, then locals, the file's own names, imported names, and names
	// that need an import.
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.c.first != b.c.first {
			return a.c.first
		}
		if a.fits != b.fits {
			return a.fits
		}
		if a.match != b.match {
			return a.match < b.match
		}
		if a.c.locality != b.c.locality {
			return a.c.locality < b.c.locality
		}
		if a.c.order != b.c.order {
			return a.c.order < b.c.order
		}
		return a.c.label < b.c.label
	})
	gen := r.s.completions.reset()
	rng := r.replaceRange()
	items := make([]protocol.CompletionItem, 0, len(kept))
	for i, k := range kept {
		items = append(items, r.render(k.c, fmt.Sprintf("%04d", i), rng, gen))
	}
	return items
}

// replaceRange is the word an accepted item replaces, in UTF-16 columns:
// what was typed before the cursor and the rest of the identifier after it,
// a predicate's `?` included.
func (r *completionRequest) replaceRange() protocol.Range {
	return protocol.Range{Start: r.position(r.ctx.start), End: r.position(r.ctx.end)}
}

// position converts a byte offset of the document into an LSP position.
func (r *completionRequest) position(off int) protocol.Position {
	line := lineIndexOf(r.lines.starts, off)
	return protocol.Position{
		Line:      uint32(line),
		Character: r.lines.utf16Col(uint32(line), uint32(off-r.lines.starts[line])),
	}
}

// render builds the protocol item for a ranked candidate. The detail is the
// signature as hover renders it; the documentation waits for
// completionItem/resolve.
func (r *completionRequest) render(c candidate, sortText string, rng protocol.Range, gen int) protocol.CompletionItem {
	kind := c.kind
	item := protocol.CompletionItem{Label: c.label, Kind: &kind, SortText: &sortText}
	detail, hasDoc := c.detail, c.doc != ""
	if c.sym != nil {
		sig, doc := hoverdoc.SignatureAndDoc(c.sym, r.fa)
		if detail == "" {
			detail = sig
		}
		hasDoc = hasDoc || doc != ""
	}
	if c.importFrom != nil {
		if edit, ok := r.importEdit(*c.importFrom); ok {
			item.AdditionalTextEdits = []protocol.TextEdit{edit}
			if !strings.HasPrefix(detail, "import ") {
				detail = strings.TrimSpace(detail + " (import " + c.importFrom.ImportSpec() + ")")
			}
		}
	}
	if detail != "" {
		item.Detail = &detail
	}
	text, snippet := r.insertText(c)
	if (r.ctx.kind == ctxStructField || r.ctx.kind == ctxPatternField) && r.ctx.start > 0 && r.doc.Content[r.ctx.start-1] == ',' {
		// Accepted straight after the `,` that opened the list: the space
		// the formatter puts after it comes with the field.
		text = " " + text
	}
	if c.asIs {
		mode := protocol.InsertTextModeAsIs
		if snippet && strings.Contains(text, "\n") {
			// A snippet's later lines get the edit line's indentation from
			// the client: Neovim's vim.snippet.expand prepends it whatever
			// the insert mode says, and adjustIndentation asks every client
			// for the same. So they are sent relative to that line.
			text = relativeToLineIndent(text, r.lineIndent())
			mode = protocol.InsertTextModeAdjustIndentation
		}
		item.InsertTextMode = &mode
	}
	item.TextEdit = protocol.TextEdit{Range: rng, NewText: text}
	if snippet {
		format := protocol.InsertTextFormatSnippet
		item.InsertTextFormat = &format
	}
	if c.filter != "" {
		item.FilterText = &c.filter
	}
	if hasDoc {
		item.Data = r.s.completions.add(gen, resolveEntry{sym: c.sym, fa: r.fa, doc: c.doc})
	}
	return item
}

// relativeToLineIndent removes indent from the start of each line of text
// after the first, so a client that indents those lines by the edit line's
// indentation puts them back where they were written.
func relativeToLineIndent(text, indent string) string {
	if indent == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.TrimPrefix(lines[i], indent)
	}
	return strings.Join(lines, "\n")
}

// insertText is the text an accepted candidate inserts, and whether it is a
// snippet. A function in a position that calls it gets its parentheses, with
// one placeholder per parameter the call must pass: a parameter with a
// default is left out, and so is the first parameter of a pipe stage, which
// the piped value fills. Without snippet support a call with no required
// argument gets `()` and any other call gets its bare name; signature help
// takes over at the `(`.
func (r *completionRequest) insertText(c candidate) (string, bool) {
	base := c.insert
	if base == "" {
		base = c.label
	}
	if c.snippet != "" {
		if r.s.snippetSupport {
			return c.snippet, true
		}
		return base, false
	}
	params, ok := r.callParams(c)
	if !ok {
		return base, false
	}
	if !r.s.snippetSupport {
		if len(params) == 0 {
			return base + "()", false
		}
		return base, false
	}
	var b strings.Builder
	b.WriteString(snippetEscape(base))
	b.WriteString("(")
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "${%d:%s}", i+1, snippetEscape(p))
	}
	b.WriteString(")$0")
	return b.String(), true
}

// callParams returns the names of the parameters a call to c must pass at
// this position, and whether c is called here at all. It is not called in a
// position that is not an expression, when the `(` is already written, or
// when the position expects a function value (`Iter.map(xs, to_|)`).
func (r *completionRequest) callParams(c candidate) ([]string, bool) {
	switch r.ctx.kind {
	case ctxExpr, ctxStatement, ctxMember, ctxPipe, ctxTestBoot:
	default:
		return nil, false
	}
	if c.noCall || (c.sym == nil && !c.callable) || r.ctx.nextIsParen || r.expectsFunction() {
		return nil, false
	}
	var names []string
	if c.sym == nil {
		names = c.callParams
	} else {
		real := realSymbol(c.sym)
		switch real.Kind {
		case analysis.SymbolFunction, analysis.SymbolInterfaceMethod:
		default:
			return nil, false
		}
		switch n := real.Node.(type) {
		case *ast.FuncDef:
			names = requiredParamNames(n.Params)
		case *ast.ExternFunc:
			names = requiredParamNames(n.Params)
		case *ast.InterfaceMethod:
			names = requiredParamNames(n.Params)
		default:
			return nil, false
		}
	}
	if r.pipeStage() && len(names) > 0 {
		names = names[1:]
	}
	return names, true
}

// pipeStage reports whether the call being completed is a pipe stage, whose
// first argument the piped value supplies.
func (r *completionRequest) pipeStage() bool {
	return r.ctx.kind == ctxPipe || (r.ctx.kind == ctxMember && r.ctx.pipeLHS != nil)
}

// expectsFunction reports whether the position expects a function value.
func (r *completionRequest) expectsFunction() bool {
	_, ok := analysis.ResolveTypeVar(r.expected).(*analysis.FuncType)
	return ok
}

// requiredParamNames lists the parameters without a default value, each as
// its signature spells it: a destructuring parameter by its pattern
// (`Days(n)`), never by the binding slot the parser names it.
func requiredParamNames(params []ast.Param) []string {
	var out []string
	for i, p := range params {
		if p.Default != nil {
			continue
		}
		name := hoverdoc.ParamDisplayName(p)
		if name == "" {
			name = fmt.Sprintf("arg%d", i+1)
		}
		out = append(out, name)
	}
	return out
}

// snippetEscape escapes the characters a snippet gives meaning to.
func snippetEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `$`, `\$`, `}`, `\}`).Replace(s)
}

// resolveEntry is what completionItem/resolve needs to document an item.
type resolveEntry struct {
	sym *analysis.Symbol
	fa  *analysis.FileAnalysis
	doc string
}

// completionResolveCache holds the last completion list's documentation
// sources, so the list itself carries none: an item's data names its entry
// and the generation of the list it came from.
type completionResolveCache struct {
	mu      sync.Mutex
	gen     int
	entries []resolveEntry
}

func (c *completionResolveCache) reset() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.entries = c.entries[:0]
	return c.gen
}

func (c *completionResolveCache) add(gen int, e resolveEntry) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return nil
	}
	c.entries = append(c.entries, e)
	return map[string]any{"gen": gen, "id": len(c.entries) - 1}
}

func (c *completionResolveCache) lookup(data any) (resolveEntry, bool) {
	m, ok := data.(map[string]any)
	if !ok {
		return resolveEntry{}, false
	}
	gen, ok1 := jsonInt(m["gen"])
	id, ok2 := jsonInt(m["id"])
	c.mu.Lock()
	defer c.mu.Unlock()
	if !ok1 || !ok2 || gen != c.gen || id < 0 || id >= len(c.entries) {
		return resolveEntry{}, false
	}
	return c.entries[id], true
}

func jsonInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

// completionItemResolve fills in an item's documentation: the symbol's hover
// below its signature, or the candidate's own doc.
func (s *Server) completionItemResolve(_ *glsp.Context, item *protocol.CompletionItem) (*protocol.CompletionItem, error) {
	e, ok := s.completions.lookup(item.Data)
	if !ok {
		return item, nil
	}
	doc := e.doc
	if doc == "" && e.sym != nil {
		_, doc = hoverdoc.SignatureAndDoc(e.sym, e.fa)
	}
	if doc != "" {
		item.Documentation = protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: doc}
	}
	return item, nil
}

// matchQuality reports whether name matches what was typed and how well:
// 0 exactly, 1 as a prefix, 2 as a prefix ignoring case, 3 at word
// boundaries (`tl` for `to_lower`, `oA` for `orApply`), 4 as a
// case-insensitive subsequence. An empty prefix matches everything at 1.
func matchQuality(prefix, name string) (int, bool) {
	switch {
	case prefix == "":
		return 1, true
	case name == prefix:
		return 0, true
	case strings.HasPrefix(name, prefix):
		return 1, true
	case strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)):
		return 2, true
	case boundaryMatch(prefix, name):
		return 3, true
	case subsequenceMatch(strings.ToLower(prefix), strings.ToLower(name)):
		return 4, true
	}
	return 0, false
}

// boundaryMatch reports whether prefix spells name's words in order, each
// matched by a prefix of the word: `tl`, `to_l` and `tlow` all match
// `to_lower`. Words split at `_` and at a lower-to-upper case change.
func boundaryMatch(prefix, name string) bool {
	words := splitWords(name)
	p := strings.ToLower(prefix)
	var match func(p string, w int) bool
	match = func(p string, w int) bool {
		if p == "" {
			return true
		}
		for ; w < len(words); w++ {
			word := strings.ToLower(words[w])
			for n := min(len(word), len(p)); n >= 1; n-- {
				if word[:n] == p[:n] && match(p[n:], w+1) {
					return true
				}
			}
			if w == 0 {
				// The first word must start the match.
				return false
			}
		}
		return false
	}
	return match(p, 0)
}

func splitWords(name string) []string {
	var words []string
	start := 0
	for i := 1; i < len(name); i++ {
		c, prev := name[i], name[i-1]
		switch {
		case c == '_' || c == '?':
			if i > start {
				words = append(words, name[start:i])
			}
			start = i + 1
		case c >= 'A' && c <= 'Z' && prev >= 'a' && prev <= 'z':
			words = append(words, name[start:i])
			start = i
		}
	}
	if start < len(name) {
		words = append(words, strings.TrimRight(name[start:], "?"))
	}
	return words
}

func subsequenceMatch(p, s string) bool {
	i := 0
	for j := 0; j < len(s) && i < len(p); j++ {
		if s[j] == p[i] {
			i++
		}
	}
	return i == len(p)
}

// getPrefix extracts the identifier being typed at the cursor, qualifier
// included. line and character are 0-based. Only the inline-Go helper
// completion uses it; Nomi positions are classified from the reparsed tree.
func getPrefix(content string, line, character int) string {
	lines := strings.Split(content, "\n")
	if line >= len(lines) {
		return ""
	}
	lineText := lines[line]
	if character > len(lineText) {
		character = len(lineText)
	}
	end := character
	start := end
	for start > 0 && isIdentChar(lineText[start-1]) {
		start--
	}
	if start == end {
		return ""
	}
	return lineText[start:end]
}

func isIdentChar(c byte) bool {
	// `?` is the trailing predicate marker the lexer absorbs into an
	// identifier (e.g. `empty?`, `contains?`). It is trailing-only in valid
	// source, so including it in a backward char-scan cannot over-match
	// across token boundaries.
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '?'
}
