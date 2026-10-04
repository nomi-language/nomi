package lsp

import (
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
)

// completionSentinel is spliced into the document at the cursor before the
// completion request reparses it. It is an identifier tail no one writes, so
// whatever the user had typed so far becomes one identifier ending in it
// (`Str` becomes `Strzq9cursor`), and an empty prefix becomes an identifier of
// its own. The reparsed tree then holds a node exactly where the cursor is,
// and the path from the root to that node says what the position is: a
// FieldAccess's field, a pipe's right operand, a type annotation, a struct
// literal's field label, an import path segment, and so on.
//
// The reparse is a parse of one file and nothing more. Names, scopes and
// types still come from the document's analyzed snapshot.
const completionSentinel = "zq9cursor"

// completionSentinelUpper stands in for an empty prefix where only a
// PascalCase name parses: the variant after a leading dot (`.` alone).
const completionSentinelUpper = "Zq9cursor"

// sentinelSuffixes are tried in order after the sentinel until the reparse
// keeps it. Most positions need nothing; the others complete the construct
// the cursor sits in, so its shape survives the parse: a case arm needs its
// `-> body`, a `with` needs its `= value`, an `if` condition its block, a
// pipe stage or an unclosed call its parentheses, and an unclosed struct
// pattern in a case arm its brace (and a variant payload's parenthesis).
var sentinelSuffixes = []string{"", "()", ")", " {}", " -> 0", " = 0", "} -> 0", "}) -> 0"}

// ctxKind classifies the cursor position.
type ctxKind int

const (
	ctxNone        ctxKind = iota // nothing to offer: a string, a comment, a new name
	ctxTopLevel                   // where a declaration starts
	ctxStatement                  // where a statement starts inside a block
	ctxExpr                       // an operand
	ctxType                       // a type annotation
	ctxPattern                    // a pattern outside a case arm's head
	ctxCaseArm                    // the head of a case arm
	ctxDotVariant                 // after a leading `.`
	ctxMember                     // after `X.`: an owner, a file, an interface or a value
	ctxPipe                       // the stage right of `|>`
	ctxStructField                // a field label in a struct literal
	ctxImportPath                 // a segment of an import path
	ctxImportName                 // a name in an import's selector list
	ctxTestGroup                  // a line of a `tests` group's body
	ctxTestBoot                   // the call after a group's `boot`
	ctxParamName                  // a typed parameter name in a `fn` signature
	ctxImplItem                   // a declaration in an `impl Iface for T` body
)

// completionContext is what the cursor position is, read from the reparsed
// tree, plus the nodes of that tree the candidate sources need.
type completionContext struct {
	kind ctxKind
	// prefix is the part of the word the user typed before the cursor;
	// start and end are the byte offsets, in the document as it is, of the
	// whole word an accepted item replaces (a word can continue past the
	// cursor).
	prefix     string
	start, end int
	// hit is the sentinel's position in the reparsed tree; nil when the
	// context came from the token fallback.
	hit *sentinelHit
	// object is the expression left of the dot (ctxMember), or the type
	// qualifier's module (ctxType with a qualifier).
	object ast.Node
	// accessor is the field accessor (`.na`, `.address.ci`) whose segment
	// accessorSeg is being typed (ctxDotVariant). A leading dot with a
	// lower-case word reparses as one.
	accessor    *ast.FieldAccessor
	accessorSeg int
	// typeQualifier is the module name of a qualified type being typed
	// (`io.Rea`).
	typeQualifier string
	// pipeLHS is the value piped into the stage being typed: set for
	// ctxPipe, and for a ctxMember whose access is a pipe stage
	// (`xs |> Iter.ma`).
	pipeLHS ast.Node
	// structLit is the literal whose field label is being typed.
	structLit *ast.StructLit
	// caseNode and branch are the case whose arm head is being typed.
	caseNode *ast.Case
	branch   int
	// importStmt and importSeg locate an import path segment or selector
	// name being typed.
	importStmt *ast.ImportStmt
	importSeg  int
	// groupHas lists the `tests` group keywords already written anywhere in
	// the group around the cursor (ctxTestGroup).
	groupHas map[string]bool
	// nextIsParen reports that the character after the word is `(`, so a
	// call needs no parentheses of its own.
	nextIsParen bool
	// withTarget reports a member access that is a `with` statement's
	// target, where only application fields fit.
	withTarget bool
	// groupClock reports a variant after a `tests` group's `clock`, read
	// from the tokens because the group does not parse yet.
	groupClock bool
	// paramSlot and paramName locate a parameter of a `fn` signature
	// whose name or type is being typed (completion_param_type.go).
	paramSlot paramSlot
	paramName string
	// implStub is the interface impl block whose body the cursor starts a
	// declaration in (ctxImplItem, completion_impl_stubs.go).
	implStub *implStubSite
}

// pathStep is one hop from the root of the reparsed tree toward the value
// that holds the sentinel: the value at this hop and the field of the
// previous hop it was reached through.
type pathStep struct {
	v     reflect.Value
	field string // the parent's field this value was read from ("" at a root)
	index int    // the slice index within that field, or -1
}

// iface returns the hop's Go value: an AST node pointer, or the address of a
// plain struct (*ast.CaseBranch, *ast.StructFieldVal, *ast.Param ...).
func (s pathStep) iface() any {
	if !s.v.IsValid() {
		return nil
	}
	if s.v.Kind() == reflect.Struct && s.v.CanAddr() {
		return s.v.Addr().Interface()
	}
	if s.v.CanInterface() {
		return s.v.Interface()
	}
	return nil
}

// sentinelHit is one string field of the reparsed tree that holds the
// sentinel, with the path to the value that owns the field.
type sentinelHit struct {
	path  []pathStep
	field string // the owner's string field: "Name", "Value", "Text" ...
	roots []ast.Node
}

// at returns the i-th hop above the owner of the string field (0 is the
// owner itself) as its Go value and the field it was reached through.
func (h *sentinelHit) at(i int) (any, string) {
	j := len(h.path) - 1 - i
	if j < 0 {
		return nil, ""
	}
	return h.path[j].iface(), h.path[j].field
}

// findSentinel walks every exported field reachable from roots and returns
// each string field that contains marker, with its path. Reflection, not a
// type switch, so a node kind added later is searched without anyone
// remembering to add it here.
//
// line, when positive, is the 1-based line the marker was spliced into, and
// only the roots that can hold it are walked (sentinelRoots).
func findSentinel(roots []ast.Node, marker string, line int) []sentinelHit {
	var hits []sentinelHit
	seen := map[uintptr]bool{}
	var path []pathStep
	var walk func(v reflect.Value, field string, index int)
	var walkStruct func(v reflect.Value)
	walk = func(v reflect.Value, field string, index int) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), field, index)
			}
		case reflect.Ptr:
			if v.IsNil() || seen[v.Pointer()] || v.Elem().Kind() != reflect.Struct {
				return
			}
			seen[v.Pointer()] = true
			path = append(path, pathStep{v: v, field: field, index: index})
			walkStruct(v.Elem())
			path = path[:len(path)-1]
		case reflect.Struct:
			path = append(path, pathStep{v: v, field: field, index: index})
			walkStruct(v)
			path = path[:len(path)-1]
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				walk(v.Index(i), field, i)
			}
		}
	}
	walkStruct = func(v reflect.Value) {
		t := v.Type()
		for i := range v.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			fv := v.Field(i)
			if fv.Kind() == reflect.String {
				if strings.Contains(fv.String(), marker) {
					hits = append(hits, sentinelHit{
						path:  append([]pathStep(nil), path...),
						field: f.Name,
						roots: roots,
					})
				}
				continue
			}
			walk(fv, f.Name, -1)
		}
	}
	for _, n := range sentinelRoots(roots, line) {
		walk(reflect.ValueOf(n), "", -1)
	}
	return hits
}

// sentinelRoots returns the top-level nodes of a reparse that can hold a
// marker spliced into line: those whose extent reaches line and that start
// no later than it, where a root's text is taken to start right after the
// previous root's last line, so its doc comment, attached tests and leading
// comments count. A root with no recorded extent is always kept. The roots
// of a file are in source order; walking only these instead of the whole
// file keeps a completion request's walk to the declarations at the cursor.
func sentinelRoots(roots []ast.Node, line int) []ast.Node {
	if line <= 0 {
		return roots
	}
	var out []ast.Node
	prevEnd := 0
	for _, n := range roots {
		hs, ok := n.(ast.HasSpan)
		if !ok || hs.GetSpan().IsZero() {
			out = append(out, n)
			continue
		}
		sp := hs.GetSpan()
		if prevEnd <= line && sp.EndLine >= line {
			out = append(out, n)
		}
		prevEnd = max(prevEnd, sp.EndLine)
	}
	return out
}

// insideErrorNode reports whether a hit sits in a run the parser skipped.
func (h *sentinelHit) insideErrorNode() bool {
	for _, s := range h.path {
		if _, ok := s.iface().(*ast.ErrorNode); ok {
			return true
		}
	}
	return false
}

// wordBounds returns the identifier around byte offset off: start..off is
// what was typed before the cursor and off..end the rest of the word. A
// predicate name's trailing `?` belongs to the word on either side.
func wordBounds(content string, off int) (start, end int) {
	start = off
	if start > 0 && content[start-1] == '?' {
		start--
	}
	for start > 0 && isWordByte(content[start-1]) {
		start--
	}
	end = off
	for end < len(content) && isWordByte(content[end]) {
		end++
	}
	if end < len(content) && content[end] == '?' {
		end++
	}
	return start, end
}

func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// classifyCompletion reparses content with the sentinel at byte offset off
// and returns what the position is. Each reparse is walked only at the
// cursor's declarations (sentinelRoots).
func classifyCompletion(content string, off int) completionContext {
	return classifyCompletionWalking(content, off, true)
}

// classifyCompletionWalking is classifyCompletion, walking each reparse
// whole when atCursor is false.
func classifyCompletionWalking(content string, off int, atCursor bool) completionContext {
	start, end := wordBounds(content, off)
	ctx := completionContext{start: start, end: end, prefix: content[start:off]}
	rest := strings.TrimLeft(content[end:], " \t")
	ctx.nextIsParen = strings.HasPrefix(rest, "(")
	if classifyImplItem(&ctx, content, off) {
		return ctx
	}

	// The sentinel goes where the cursor is, ahead of a typed `?`, so
	// `empty?` reparses as the one identifier `emptyzq9cursor?`.
	at := off
	if at > start && content[at-1] == '?' {
		at--
	}
	sentinels := []string{completionSentinel}
	if ctx.prefix == "" {
		sentinels = append(sentinels, completionSentinelUpper)
	}
	line := 0
	if atCursor {
		line = strings.Count(content[:at], "\n") + 1
	}
	var lastTokens []token.Token
	suffixes := sentinelSuffixes
	if c := lineClosers(content, at); c != "" {
		suffixes = append(suffixes[:len(suffixes):len(suffixes)], c)
	}
	for _, suffix := range suffixes {
		for _, sentinel := range sentinels {
			spliced := content[:at] + sentinel + suffix + content[at:]
			tokens := lexer.Lex(spliced)
			if suffix == "" && sentinel == completionSentinel {
				lastTokens = tokens
			}
			nodes, _, _ := parser.ParseResilient(tokens)
			for _, h := range findSentinel(nodes, sentinel, line) {
				if h.insideErrorNode() {
					continue
				}
				h := h
				ctx.hit = &h
				classifyHit(&ctx)
				if ctx.kind == ctxStructField && !startsField(content, start) {
					// `{..p, address: {}‸}`: the reparse reads the word
					// as a field, but no `,` or line break separates it
					// from the value before.
					ctx.kind = ctxNone
				}
				classifyParamSlot(&ctx, lastTokens)
				return ctx
			}
		}
	}
	classifyTokens(&ctx, lastTokens)
	classifyParamSlot(&ctx, lastTokens)
	return ctx
}

// startsField reports whether a word at start can begin a struct literal's
// field: it follows the literal's `{`, a `,` or a line break.
func startsField(content string, start int) bool {
	i := start
	for i > 0 && (content[i-1] == ' ' || content[i-1] == '\t') {
		i--
	}
	return i == 0 || content[i-1] == '{' || content[i-1] == ',' || content[i-1] == '\n'
}

// lineClosers closes, innermost first, the brackets the cursor's line opens
// before the cursor and does not close: `Person{address: {‸` gets `}}` and
// `[{‸` gets `}]`, what an auto-pair plugin would have typed. It is the last
// suffix tried, for a client typing with no such plugin. A string's
// brackets do not count.
func lineClosers(content string, at int) string {
	start := strings.LastIndexByte(content[:at], '\n') + 1
	var open []byte
	var quote byte
	for i := start; i < at; i++ {
		c := content[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			open = append(open, c)
		case c == ')' || c == ']' || c == '}':
			if len(open) > 0 {
				open = open[:len(open)-1]
			}
		}
	}
	closers := map[byte]byte{'(': ')', '[': ']', '{': '}'}
	var b strings.Builder
	for i := len(open) - 1; i >= 0; i-- {
		b.WriteByte(closers[open[i]])
	}
	return b.String()
}

// classifyHit reads the context off the path to the sentinel.
func classifyHit(ctx *completionContext) {
	h := ctx.hit
	if classifyPatternField(ctx) || classifyDerive(ctx) {
		return
	}
	leaf, leafField := h.at(0)
	parent, _ := h.at(1)
	grand, _ := h.at(2)
	switch leaf.(type) {
	case *ast.Ident, *ast.TypeIdent:
		if h.field != "Name" {
			ctx.kind = ctxNone
			return
		}
		if acc, ok := parent.(*ast.FieldAccessor); ok && leafField == "Path" {
			// `.na‸` and `.‸`: a field accessor, or the start of a
			// `.Variant`, whichever the expected type asks for
			// (accessorCandidates).
			ctx.kind = ctxDotVariant
			ctx.accessor = acc
			ctx.accessorSeg = h.path[len(h.path)-1].index
			return
		}
		_, parentField := h.at(1)
		classifyName(ctx, leafField, parent, parentField, grand)
	case *ast.SimpleType, *ast.GenericType:
		if h.field != "Name" {
			return
		}
		ctx.kind = ctxType
		if _, ok := parent.(*ast.EnumPattern); ok && leafField == "Variant" {
			ctx.kind = ctxPattern
		}
		if q, ok := parent.(*ast.QualifiedType); ok && leafField == "Member" {
			ctx.typeQualifier = q.Module
		}
	case *ast.QualifiedType:
		if h.field == "Module" {
			ctx.kind = ctxType
		}
	case *ast.DotVariant, *ast.DotVariantType:
		ctx.kind = ctxDotVariant
	case *ast.IdentPattern:
		ctx.kind = ctxPattern
		if _, ok := parent.(*ast.CaseBranch); ok && leafField == "Pattern" {
			if c, ok := grand.(*ast.Case); ok {
				ctx.kind = ctxCaseArm
				ctx.caseNode = c
				ctx.branch = h.path[len(h.path)-2].index
			}
		}
	case *ast.StructFieldVal:
		if h.field != "Name" {
			return
		}
		if lit, ok := parent.(*ast.StructLit); ok {
			ctx.kind = ctxStructField
			ctx.structLit = lit
		}
	default:
		ctx.kind = ctxNone
	}
}

// classifyName handles the sentinel inside an identifier: the commonest
// case, where the identifier's parent decides the position.
func classifyName(ctx *completionContext, leafField string, parent any, parentField string, grand any) {
	switch p := parent.(type) {
	case *ast.FieldAccess:
		if leafField == "Field" {
			ctx.kind = ctxMember
			ctx.object = p.Object
			// A pipe stage spelled with its owner, `xs |> Iter.ma` or
			// `xs |> Iter.ma(f)`, still takes the piped value first.
			ctx.pipeLHS = pipeLeftOf(ctx.hit, 1)
			_, isWith := grand.(*ast.With)
			ctx.withTarget = isWith && parentField == "Target"
			return
		}
	case *ast.ImportStmt:
		switch leafField {
		case "ModulePath":
			ctx.kind = ctxImportPath
			ctx.importStmt = p
			ctx.importSeg = ctx.hit.path[len(ctx.hit.path)-1].index
		case "Names":
			ctx.kind = ctxImportName
			ctx.importStmt = p
		default:
			ctx.kind = ctxNone
		}
		return
	case *ast.Binary:
		if p.Op == "|>" && leafField == "Right" {
			ctx.kind = ctxPipe
			ctx.pipeLHS = p.Left
			return
		}
	case *ast.Call:
		if leafField == "Func" {
			if lhs := pipeLeftOf(ctx.hit, 1); lhs != nil {
				ctx.kind = ctxPipe
				ctx.pipeLHS = lhs
				return
			}
			if _, ok := grand.(*ast.TestDecl); ok && parentField == "Boot" {
				ctx.kind = ctxTestBoot
				return
			}
		}
	case *ast.ExprStmt:
		if _, ok := grand.(*ast.Block); ok && parentField == "Stmts" {
			ctx.kind = ctxStatement
			return
		}
		if grand == nil {
			ctx.kind = ctxTopLevel
			return
		}
	case *ast.TestDecl:
		if leafField == "Boot" {
			ctx.kind = ctxTestBoot
			return
		}
	case *ast.TupleDestructure:
		ctx.kind = ctxNone // naming a new binding
		return
	}
	ctx.kind = ctxExpr
}

// pipeLeftOf returns the piped value when the hop i above the sentinel's
// owner is a pipe stage: the right operand of `|>` directly, or the callee of
// a call that is.
func pipeLeftOf(h *sentinelHit, i int) ast.Node {
	node, field := h.at(i)
	parent, parentField := h.at(i + 1)
	if call, ok := parent.(*ast.Call); ok && parentField != "" && field == "Func" && call != nil {
		node, field = parent, parentField
		parent, _ = h.at(i + 2)
	}
	if b, ok := parent.(*ast.Binary); ok && b.Op == "|>" && field == "Right" && node != nil {
		return b.Left
	}
	return nil
}

// classifyTokens is the fallback for a position the reparse could not keep:
// it reads the tokens just before the cursor.
func classifyTokens(ctx *completionContext, tokens []token.Token) {
	idx := -1
	for i, t := range tokens {
		if (t.Type == token.IDENT || t.Type == token.TYPE_IDENT) && strings.Contains(t.Lexeme, completionSentinel) {
			idx = i
			break
		}
	}
	if idx < 0 {
		ctx.kind = ctxNone
		return
	}
	if classifyDeriveTokens(ctx, tokens, idx) {
		return
	}
	if has, ok := enclosingTestGroup(tokens, idx); ok {
		ctx.kind = ctxTestGroup
		ctx.groupHas = has
		return
	}
	prev := prevToken(tokens, idx)
	// `boot ‸` in a group with no test yet: the group cannot parse until a
	// test asserts something, so the tree never shows the boot line.
	if prev >= 0 && tokens[prev].Type == token.IDENT && tokens[prev].Lexeme == "boot" {
		if _, ok := enclosingTestGroup(tokens, prev); ok {
			ctx.kind = ctxTestBoot
			return
		}
	}
	switch {
	case prev >= 0 && tokens[prev].Type == token.DOT:
		if obj := dottedObject(tokens, prev); obj != nil {
			ctx.kind = ctxMember
			ctx.object = obj
			return
		}
		ctx.kind = ctxDotVariant
		// `clock .‸` in a group the tree cannot show yet.
		if c := prevToken(tokens, prev); c >= 0 && tokens[c].Type == token.IDENT && tokens[c].Lexeme == "clock" {
			_, ctx.groupClock = enclosingTestGroup(tokens, c)
		}
		return
	case tokens[idx].Col == 1:
		ctx.kind = ctxTopLevel
		return
	}
	ctx.kind = ctxExpr
}

// prevToken returns the index of the token before i on the same line, or -1.
func prevToken(tokens []token.Token, i int) int {
	if i == 0 {
		return -1
	}
	p := i - 1
	if tokens[p].Line != tokens[i].Line {
		return -1
	}
	return p
}

// dottedObject rebuilds `a.b` from the name tokens ending just before the
// dot at index dot, or returns nil when the dot follows no name.
func dottedObject(tokens []token.Token, dot int) ast.Node {
	var names []token.Token
	i := dot - 1
	for i >= 0 {
		t := tokens[i]
		if t.Type != token.IDENT && t.Type != token.TYPE_IDENT {
			break
		}
		if next := tokens[i+1]; next.Col != t.Col+len(t.Lexeme) || next.Line != t.Line {
			break
		}
		names = append([]token.Token{t}, names...)
		if i == 0 || tokens[i-1].Type != token.DOT {
			break
		}
		i -= 2
	}
	if len(names) == 0 {
		return nil
	}
	var node ast.Node = nameNode(names[0])
	for _, t := range names[1:] {
		node = &ast.FieldAccess{Object: node, Field: &ast.Ident{Name: t.Lexeme, Line: t.Line, Col: t.Col}, Line: t.Line, Col: t.Col - 1}
	}
	return node
}

func nameNode(t token.Token) ast.Node {
	if t.Type == token.TYPE_IDENT {
		return &ast.TypeIdent{Name: t.Lexeme, Line: t.Line, Col: t.Col}
	}
	return &ast.Ident{Name: t.Lexeme, Line: t.Line, Col: t.Col}
}

// enclosingTestGroup reports whether token i sits directly in the body of a
// `tests "name" { ... }` group, and which of the group's keyword lines
// (clock, boot, setup, test) come before it.
func enclosingTestGroup(tokens []token.Token, i int) (map[string]bool, bool) {
	// The sentinel must start its line: `boot |` is the boot call, not a
	// new line of the group.
	if prevToken(tokens, i) >= 0 {
		return nil, false
	}
	depth := 0
	open := -1
	for j := i - 1; j >= 0; j-- {
		switch tokens[j].Type {
		case token.RBRACE:
			depth++
		case token.LBRACE:
			if depth == 0 {
				open = j
			} else {
				depth--
			}
		}
		if open >= 0 {
			break
		}
	}
	if open < 2 || tokens[open-2].Type != token.TESTS {
		return nil, false
	}
	has := map[string]bool{}
	depth = 0
	lineStart := true
	for j := open + 1; j < len(tokens); j++ {
		if j == i {
			lineStart = false
			continue
		}
		t := tokens[j]
		switch t.Type {
		case token.NEWLINE, token.SEMICOLON, token.BLANK_LINE, token.COMMENT:
			lineStart = depth == 0
			continue
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			if depth == 0 && lineStart {
				lineStart = false
			}
			depth++
			continue
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			if depth == 0 {
				return has, true
			}
			depth--
			continue
		}
		if depth == 0 && lineStart {
			switch {
			case t.Type == token.TEST:
				has["test"] = true
			case t.Type == token.IDENT && (t.Lexeme == "clock" || t.Lexeme == "boot" || t.Lexeme == "setup"):
				has[t.Lexeme] = true
			}
		}
		lineStart = false
	}
	return has, true
}
