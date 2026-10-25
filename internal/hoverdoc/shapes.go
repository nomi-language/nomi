package hoverdoc

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Hover on syntax no symbol covers: the punctuation of a shape-symbol
// literal or pattern, a variant's leading `.` and opening `(`, a pipe's
// `|>` (pipes.go), the `&1` of an operator or a literal that makes it a
// function (lambdas.go), an operator (operators.go), and a discard `_`
// (discards.go).
//
// An editor asks for the hover of the character under the cursor, which on
// `#Point{`, `%[`, `#.Circle{` or the `]` that ends a long literal is
// punctuation no symbol covers. SyntaxAt answers there: every position of a
// literal's head (the symbol, the name and the opening bracket) and its
// closing bracket, the `..` of a pattern's `..rest`, and the `.` and `(` of
// a variant, and both characters of a pipe's `|>`. An element, key, field
// or value inside answers as it always did, since none of those positions
// is one SyntaxAt claims.

// SyntaxAt is the hover at pos when pos is one of the positions above: the
// markdown, and the start and length of the source text it describes. ok is
// false anywhere else.
func SyntaxAt(fa *analysis.FileAnalysis, nodes []ast.Node, pos analysis.Pos) (content string, start analysis.Pos, length int, ok bool) {
	if fa == nil {
		return "", analysis.Pos{}, 0, false
	}
	s := shapeSearch{fa: fa, pos: pos, nodes: nodes}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			s.visit(n)
			return true
		})
	}
	if s.best == nil {
		return "", analysis.Pos{}, 0, false
	}
	content = s.best.render()
	if content == "" {
		return "", analysis.Pos{}, 0, false
	}
	return content, s.best.start, s.best.length, true
}

// shapeHit is one claimed position: the source text it covers and how to
// render it. size is the extent of the node that claimed it, so that of two
// nodes claiming a position the innermost answers.
type shapeHit struct {
	start  analysis.Pos
	length int
	size   int
	render func() string
}

type shapeSearch struct {
	fa   *analysis.FileAnalysis
	pos  analysis.Pos
	best *shapeHit
	// nodes are the file's top-level nodes.
	nodes []ast.Node
	// pipeOuter maps a pipe to the pipe the parser built around it for a
	// prefixed stage (pipes.go).
	pipeOuter map[*ast.Binary]*ast.Binary
	// pipedCalls are the calls that are a pipe's stage, lambdaArgs the
	// lambdas passed to a call, and lambdaStages the lambdas of a `then`
	// or `tap` stage (lambdas.go); captureArgs and captureStages are the
	// same for captures.
	pipedCalls    map[*ast.Call]bool
	lambdaArgs    map[*ast.Lambda]lambdaArg
	lambdaStages  map[*ast.Lambda]string
	captureArgs   map[*ast.Capture]lambdaArg
	captureStages map[*ast.Capture]string
	// refutable are the `_`s that stand where a match may fail, and
	// conditionArms the `_` arms of a `case` with no subject (discards.go).
	refutable     map[*ast.WildcardPattern]bool
	conditionArms map[*ast.WildcardPattern]bool
}

// claim offers the text at (line, col) of length n for the node n of extent
// size.
func (s *shapeSearch) claim(line, col, length, size int, render func() string) {
	if line != s.pos.Line || s.pos.Col < col || s.pos.Col >= col+length {
		return
	}
	if s.best != nil && s.best.size <= size {
		return
	}
	s.best = &shapeHit{start: analysis.Pos{Line: line, Col: col}, length: length, size: size, render: render}
}

// spanOf is a node's extent, or the zero span.
func spanOf(n ast.Node) ast.Span {
	if h, ok := n.(ast.HasSpan); ok && n != nil {
		return h.GetSpan()
	}
	return ast.Span{}
}

// extentOf is a node's extent on one line: its span, or, for a type name
// the parser records no span for (the name of `#Point{`, of `%Tags[`, of a
// pattern's `Some(`), the extent its position and spelling give.
func extentOf(n ast.Node) ast.Span {
	if sp := spanOf(n); !sp.IsZero() {
		return sp
	}
	one := func(line, col, n int) ast.Span {
		if line == 0 {
			return ast.Span{}
		}
		return ast.Span{StartLine: line, StartCol: col, EndLine: line, EndCol: col + n}
	}
	switch t := n.(type) {
	case *ast.SimpleType:
		return one(t.Line, t.Col, len(t.Name))
	case *ast.DotVariantType:
		return one(t.Line, t.Col, 1+len(t.Name))
	case *ast.QualifiedType:
		return one(t.ModuleLine, t.ModuleCol, len(t.TypeString()))
	case *ast.GenericType:
		return one(t.Line, t.Col, len(t.TypeString()))
	}
	return ast.Span{}
}

// spanSize orders extents: a node inside another has the smaller size.
func spanSize(sp ast.Span) int {
	return (sp.EndLine-sp.StartLine)*100000 + sp.EndCol - sp.StartCol
}

func (s *shapeSearch) visit(n ast.Node) {
	switch n := n.(type) {
	case *ast.StructLit:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.structLit(n) })
	case *ast.ListLit:
		s.shape(n, n.Line, n.Col, n.TypeName, true, func() string { return s.collectionLit(n, n.TypeName) })
	case *ast.ListSpreadLit:
		s.shape(n, n.Line, n.Col, nil, true, func() string { return s.collectionLit(n, nil) })
	case *ast.VectorLit:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.collectionLit(n, n.TypeName) })
	case *ast.SetLit:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.collectionLit(n, n.TypeName) })
	case *ast.MapLit:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.collectionLit(n, n.TypeName) })
	case *ast.TupleLit:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.collectionLit(n, n.TypeName) })
	case *ast.ListPattern:
		s.shape(n, n.Line, n.Col, n.TypeName, true, func() string { return s.pattern(n) })
		if rest, ok := n.TailSpread.(*ast.IdentPattern); ok {
			s.restDots(rest.Line, rest.Col-2, rest, n)
		}
	case *ast.VectorPattern:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.pattern(n) })
		if rest, ok := n.Rest.(*ast.IdentPattern); ok && n.Spread {
			s.restDots(n.SpreadLine, n.SpreadCol, rest, n)
		}
	case *ast.SetPattern:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.pattern(n) })
		if rest, ok := n.Rest.(*ast.IdentPattern); ok && n.Spread {
			s.restDots(n.SpreadLine, n.SpreadCol, rest, n)
		}
	case *ast.MapPattern:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.pattern(n) })
	case *ast.StructPattern:
		s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.pattern(n) })
	case *ast.TuplePattern:
		if !n.Flat {
			s.shape(n, n.Line, n.Col, n.TypeName, false, func() string { return s.pattern(n) })
		}
	case *ast.DotVariant:
		s.dotVariant(n, n.Line, n.Col, n.Name)
	case *ast.EnumPattern:
		s.variantOpen(n, n.Variant)
		if dv, ok := n.Variant.(*ast.DotVariantType); ok {
			s.dotVariant(n, dv.Line, dv.Col, dv.Name)
		}
	case *ast.Call:
		s.variantOpen(n, n.Func)
		s.noteLambdaArgs(n)
	case *ast.Binary:
		if n.Op == "|>" {
			s.pipe(n)
			s.notePipeStage(n.Right)
		} else {
			s.operator(n)
		}
	case *ast.Unary:
		s.unary(n)
	case *ast.RangeLit:
		s.rangeOp(n)
	case *ast.Lambda:
		s.lambda(n)
		s.discardParams(n.Params, func() *analysis.FuncType {
			ft, _ := analysis.ResolveTypeVar(s.fa.ExprTypes[n]).(*analysis.FuncType)
			return ft
		})
	case *ast.Capture:
		s.capture(n)
	case *ast.FuncDef:
		s.funcKeyword(n)
		s.discardParams(n.Params, func() *analysis.FuncType { return s.funcDefType(n) })
	case *ast.WildcardPattern:
		s.discard(n)
	case *ast.Binding:
		s.discardBinding(n)
	case *ast.Case, *ast.If, *ast.PatternBinding, *ast.PatternDestructure:
		s.noteRefutablePositions(n)
	}
}

// shape claims a literal's or pattern's head and closing bracket. The head
// runs from the symbol at (line, col) through the opening bracket: right
// after the type name when there is one, at col for a list (whose bracket is
// its first character), and after the one-character symbol otherwise.
func (s *shapeSearch) shape(n ast.Node, line, col int, name ast.TypeExpr, bracketFirst bool, render func() string) {
	sp := spanOf(n)
	if sp.IsZero() {
		return
	}
	size := spanSize(sp)
	open := col + 1
	if bracketFirst {
		open = col
	}
	if name != nil {
		nsp := extentOf(name)
		if nsp.IsZero() || nsp.EndLine != line {
			return
		}
		open = nsp.EndCol
		if dv, ok := name.(*ast.DotVariantType); ok {
			s.dotVariant(n, dv.Line, dv.Col, dv.Name)
		}
	}
	s.claim(line, col, open-col+1, size, render)
	s.claim(sp.EndLine, sp.EndCol-1, 1, size, render)
}

// restDots claims the `..` of `..rest` in pattern p: rest's own hover.
func (s *shapeSearch) restDots(line, col int, rest *ast.IdentPattern, p ast.Node) {
	s.claim(line, col, 2, spanSize(spanOf(p))-1, func() string {
		if sym := s.fa.Definitions[analysis.Pos{Line: rest.Line, Col: rest.Col}]; sym != nil {
			return RenderForEditor(sym, s.fa)
		}
		return ""
	})
}

// dotVariant claims the `.` and the name of `.Name` (at line, col) inside
// node n: the variant, with its enum taken from the expected type.
func (s *shapeSearch) dotVariant(n ast.Node, line, col int, name string) {
	size := spanSize(spanOf(n))
	render := func() string {
		sym := s.symbolIn(line, col+1, len(name))
		if sym == nil {
			return ""
		}
		return renderVariant(sym, true)
	}
	// Smaller than the node's size: the name inside a `#.Circle{` head
	// answers as the variant, which the head's own claim also does.
	s.claim(line, col, 1+len(name), size-1, render)
}

// variantOpen claims the `(` after a variant constructor or a variant
// pattern's name: callee is the constructor (`Some`, `.Circle`,
// `Shape.Circle`) and n the call or pattern.
func (s *shapeSearch) variantOpen(n ast.Node, callee ast.Node) {
	csp := extentOf(callee)
	sp := spanOf(n)
	if csp.IsZero() || sp.IsZero() || csp.EndLine != csp.StartLine {
		return
	}
	if sp.EndLine == csp.EndLine && sp.EndCol == csp.EndCol {
		return // no parenthesis follows
	}
	line, col := csp.EndLine, csp.EndCol
	_, dot := callee.(*ast.DotVariant)
	if _, isType := callee.(*ast.DotVariantType); isType {
		dot = true
	}
	s.claim(line, col, 1, spanSize(sp), func() string {
		sym := s.symbolIn(csp.EndLine, csp.EndCol-1, 1)
		if sym == nil || sym.Kind != analysis.SymbolEnumVariant {
			return ""
		}
		return renderVariant(sym, dot)
	})
}

// symbolIn is the symbol the analysis records over the text at (line, col)
// of length n: an enum variant there, or else the last name, so that
// `startup.Startup` answers with the type and not the file.
func (s *shapeSearch) symbolIn(line, col, n int) *analysis.Symbol {
	var found *analysis.Symbol
	for c := col; c < col+n; c++ {
		sym, _, _, ok := s.fa.TokenAt(analysis.Pos{Line: line, Col: c})
		if !ok {
			continue
		}
		if sym.Kind == analysis.SymbolEnumVariant {
			return sym
		}
		found = sym
	}
	return found
}

// nameSymbol is the symbol of a literal's or pattern's type name: the
// variant it names, or else the type.
func (s *shapeSearch) nameSymbol(name ast.TypeExpr) *analysis.Symbol {
	sp := extentOf(name)
	if sp.IsZero() || sp.EndLine != sp.StartLine {
		return nil
	}
	return s.symbolIn(sp.StartLine, sp.StartCol, sp.EndCol-sp.StartCol)
}

// notePipeStage records a pipe's call stage, or its `then` or `tap`
// lambda.
func (s *shapeSearch) notePipeStage(stage ast.Node) {
	var lam *ast.Lambda
	var capture *ast.Capture
	word := ""
	switch st := stage.(type) {
	case *ast.Call:
		if s.pipedCalls == nil {
			s.pipedCalls = map[*ast.Call]bool{}
		}
		s.pipedCalls[st] = true
		return
	case *ast.Then:
		lam, capture, word = st.Lambda, st.Capture, "then"
	case *ast.Tap:
		lam, capture, word = st.Lambda, st.Capture, "tap"
	}
	if capture != nil {
		if s.captureStages == nil {
			s.captureStages = map[*ast.Capture]string{}
		}
		s.captureStages[capture] = word
		return
	}
	if lam == nil {
		return
	}
	if s.lambdaStages == nil {
		s.lambdaStages = map[*ast.Lambda]string{}
	}
	s.lambdaStages[lam] = word
}

// shapeWord leads a literal's type with the word for its shape when the
// type is spelled in symbols only: `tuple #(Int, String)`, `anonymous
// struct #{x: Int}`. `Set<String>` and the other named collections say
// their shape already.
func shapeWord(t string) string {
	switch {
	case strings.HasPrefix(t, "#("):
		return "tuple " + t
	case strings.HasPrefix(t, "#{"):
		return "anonymous struct " + t
	}
	return t
}

// fence is code in a nomi fence.
func fence(code string) string { return "```nomi\n" + code + "\n```" }

// joinDoc appends doc below content when there is one.
func joinDoc(content, doc string) string {
	if doc == "" {
		return content
	}
	return content + "\n\n" + doc
}

// structLit is the hover on a struct literal's head: the struct it builds,
// or the variant it builds, or the anonymous struct type.
func (s *shapeSearch) structLit(n *ast.StructLit) string {
	if n.TypeName != nil {
		sym := s.nameSymbol(n.TypeName)
		if sym == nil {
			return ""
		}
		if sym.Kind == analysis.SymbolEnumVariant {
			_, dot := n.TypeName.(*ast.DotVariantType)
			return renderVariant(sym, dot)
		}
		body := RenderForEditor(sym, s.fa)
		if st, ok := analysis.ResolveTypeVar(s.fa.ExprTypes[n]).(*analysis.StructType); ok && len(st.TypeArgs) > 0 && body != "" {
			return fence(displayValueType(st)) + "\n\n---\n\n" + body
		}
		return body
	}
	if st := s.fa.TargetStructs[n]; st != nil {
		return targetStruct(s.fa, st, analysis.Pos{Line: n.Line, Col: n.Col})
	}
	ty := s.fa.ExprTypes[n]
	if ty == nil {
		return ""
	}
	return fence(shapeWord(displayValueType(ty)))
}

// targetStruct is the hover of a `#{` that builds the struct st because the
// position expected it: the struct, said to come from the expected type,
// and its doc. A generic struct leads with its instantiation.
func targetStruct(fa *analysis.FileAnalysis, st *analysis.StructType, pos analysis.Pos) string {
	from := "*from the expected type*"
	var decl *analysis.Symbol
	if scope := fa.ScopeAt(pos); scope != nil {
		if sym := scope.Lookup(st.Name); sym != nil {
			if sym.Resolved != nil {
				sym = sym.Resolved
			}
			if d, ok := sym.Type.(*analysis.StructType); ok && d.Name == st.Name && d.Origin == st.Origin {
				decl = sym
			}
		}
	}
	if decl == nil {
		return fence(renderStructFromType(st)) + "\n\n" + from
	}
	body := RenderWithAnalysis(decl, fa)
	if len(st.TypeArgs) > 0 {
		return fence(displayValueType(st)) + "\n\n" + from + "\n\n---\n\n" + body
	}
	// The struct's fence, the note, then the rest of its card (its doc).
	if head, rest, found := strings.Cut(body, "\n```"); found {
		return head + "\n```\n\n" + from + rest
	}
	return body + "\n\n" + from
}

// collectionLit is the hover on a collection or tuple literal's head: its
// type, or, for a named form, the type or variant it names.
func (s *shapeSearch) collectionLit(n ast.Node, name ast.TypeExpr) string {
	ty := s.fa.ExprTypes[n]
	if name == nil {
		if ty == nil {
			return ""
		}
		return fence(shapeWord(displayValueType(ty)))
	}
	sym := s.nameSymbol(name)
	if sym == nil {
		return ""
	}
	if sym.Kind == analysis.SymbolEnumVariant {
		_, dot := name.(*ast.DotVariantType)
		return renderVariant(sym, dot)
	}
	return s.namedShape(sym, ty)
}

// namedShape is the hover of a type built on a collection or tuple, named
// by a literal or pattern: `Tags`, the shape it is built on, and its doc.
func (s *shapeSearch) namedShape(sym *analysis.Symbol, ty analysis.Type) string {
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	var doc string
	if td, ok := real.Node.(*ast.TypeDef); ok {
		doc = td.Doc
	}
	dt, _ := analysis.ResolveTypeVar(ty).(*analysis.DistinctType)
	if dt == nil {
		dt, _ = real.Type.(*analysis.DistinctType)
	}
	if dt == nil || dt.Inner == nil {
		return RenderForEditor(sym, s.fa)
	}
	return joinDoc(fence(displayValueType(dt))+"\n\na `"+displayValueType(dt.Inner)+"`", doc)
}

// pattern is the hover on a pattern's head: what it matches and the types
// of the names it binds.
func (s *shapeSearch) pattern(n ast.Node) string {
	ty := s.fa.PatternTypes[n]
	what := "a value"
	if ty != nil {
		what = "a `" + displayValueType(ty) + "`"
	}
	var kind, matches string
	var binds []ast.Node
	switch p := n.(type) {
	case *ast.ListPattern:
		kind = "list pattern"
		matches = lengthPhrase(what, len(p.Heads), p.TailSpread != nil)
		binds = append(append(binds, p.Heads...), p.TailSpread)
	case *ast.VectorPattern:
		kind = "vector pattern"
		matches = lengthPhrase(what, len(p.Heads)+len(p.Tails), p.Spread)
		binds = append(append(append(binds, p.Heads...), p.Rest), p.Tails...)
	case *ast.SetPattern:
		kind = "set pattern"
		elems := valueList(p.Elems)
		switch {
		case !p.Spread && len(p.Elems) == 0:
			matches = "matches the empty " + strings.TrimPrefix(what, "a ")
		case !p.Spread:
			matches = "matches " + what + " holding exactly " + elems
		case len(p.Elems) == 0:
			matches = "matches any " + strings.TrimPrefix(what, "a ")
		default:
			matches = "matches " + what + " containing " + elems
		}
		binds = append(binds, p.Rest)
	case *ast.MapPattern:
		kind = "map pattern"
		keys := make([]ast.Node, len(p.Entries))
		for i, e := range p.Entries {
			keys[i] = e.Key
			binds = append(binds, e.Pattern)
		}
		if len(keys) == 0 {
			matches = "matches any " + strings.TrimPrefix(what, "a ")
		} else {
			matches = "matches " + what + " with the key"
			if len(keys) > 1 {
				matches += "s"
			}
			matches += " " + valueList(keys)
		}
	case *ast.StructPattern:
		kind = "struct pattern"
		matches = "matches " + what
		if p.TypeName != nil {
			if sym := s.nameSymbol(p.TypeName); sym != nil && sym.Kind == analysis.SymbolEnumVariant {
				matches = "matches `" + variantName(sym) + "`"
			}
		}
		for _, f := range p.Fields {
			if f.Pattern == nil {
				binds = append(binds, &ast.IdentPattern{Name: f.Binding, Line: f.BindingLine, Col: f.BindingCol})
			} else {
				binds = append(binds, f.Pattern)
			}
		}
	case *ast.TuplePattern:
		kind = "tuple pattern"
		matches = "matches " + what
		binds = p.Patterns
	default:
		return ""
	}
	text := kind + ": " + matches
	var parts []string
	for _, b := range binds {
		id, ok := b.(*ast.IdentPattern)
		if !ok || id == nil {
			continue
		}
		sym := s.fa.Definitions[analysis.Pos{Line: id.Line, Col: id.Col}]
		if sym == nil || sym.Type == nil {
			continue
		}
		parts = append(parts, "`"+id.Name+"` is `"+displayValueType(sym.Type)+"`")
	}
	if len(parts) > 0 {
		text += "; " + strings.Join(parts, ", ")
	}
	return text
}

// lengthPhrase says which sequences a list or vector pattern of n fixed
// elements matches.
func lengthPhrase(what string, n int, spread bool) string {
	switch {
	case spread && n == 0:
		return "matches any " + strings.TrimPrefix(what, "a ")
	case spread:
		return "matches " + what + " of at least " + elementCount(n)
	case n == 0:
		return "matches the empty " + strings.TrimPrefix(what, "a ")
	}
	return "matches " + what + " of exactly " + elementCount(n)
}

func elementCount(n int) string {
	if n == 1 {
		return "1 element"
	}
	return strconv.Itoa(n) + " elements"
}

// valueList is the values a set pattern holds or a map pattern's keys, as
// code: a constant as written, a name, a variant; anything else as `…`.
func valueList(values []ast.Node) string {
	parts := make([]string, len(values))
	for i, v := range values {
		text := renderConstValue(v)
		switch n := v.(type) {
		case *ast.Ident:
			text = n.Name
		case *ast.DotVariant:
			text = "." + n.Name
		case *ast.TypeIdent:
			text = n.Name
		}
		if text == "" {
			text = "…"
		}
		parts[i] = "`" + text + "`"
	}
	return strings.Join(parts, ", ")
}

// variantName is a variant spelled with its enum, `Room.Cave`.
func variantName(sym *analysis.Symbol) string {
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if enum, ok := real.Node.(*ast.EnumDef); ok {
		return enum.Name + "." + sym.Name
	}
	if ft, ok := sym.CallType.(*analysis.FuncType); ok {
		if et, ok := analysis.ResolveTypeVar(ft.Return).(*analysis.EnumType); ok {
			return et.Name + "." + sym.Name
		}
	}
	return sym.Name
}

// renderVariant is the hover of an enum variant: the variant with its enum
// and its payload (`Room.Cave`, `Shape.Circle { radius: Float }`,
// `Maybe.Some(Int) -> Maybe<Int>` at an instantiation of a generic enum),
// whether the enum came from the expected type (the `.` form), and the
// variant's doc comment or else the enum's.
func renderVariant(sym *analysis.Symbol, fromExpected bool) string {
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	enum, _ := real.Node.(*ast.EnumDef)
	var v ast.EnumVariant
	found := false
	if enum != nil {
		v, found = findEnumVariant(enum, real)
	}
	sig := variantName(sym)
	ft, _ := sym.CallType.(*analysis.FuncType)
	concrete := ft != nil && !analysis.ContainsTypeParam(ft)
	switch {
	case concrete && (!found || v.Kind == "positional"):
		parts := make([]string, len(ft.Params))
		for i, p := range ft.Params {
			parts[i] = displayValueType(p)
		}
		sig += "(" + strings.Join(parts, ", ") + ")"
	case found:
		sig += variantPayload(v)
	}
	if concrete && ft.Return != nil && (enum == nil || len(enum.TypeParams) > 0) {
		sig += " -> " + displayValueType(ft.Return)
	}
	out := fence(sig)
	if fromExpected && enum != nil {
		out += "\n\n*`" + enum.Name + "` from the expected type*"
	}
	doc := v.Doc
	if doc == "" && enum != nil {
		doc = enum.Doc
	}
	return joinDoc(out, doc)
}

// variantPayload is a declared variant's payload as it is written after the
// variant's name: `(Int)`, ` { depth: Int }`, or nothing.
func variantPayload(v ast.EnumVariant) string {
	switch v.Kind {
	case "positional":
		if v.DataTypeExpr == nil {
			return ""
		}
		t := v.DataTypeExpr.TypeString()
		if strings.HasPrefix(t, "#(") {
			return "(" + strings.TrimSuffix(strings.TrimPrefix(t, "#("), ")") + ")"
		}
		return "(" + t + ")"
	case "struct":
		fields := make([]string, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = f.Name
			if f.TypeAnnotation != nil {
				fields[i] += ": " + f.TypeAnnotation.TypeString()
			}
		}
		return " { " + strings.Join(fields, ", ") + " }"
	case "embedded":
		if v.EmbeddedTypeExpr != nil {
			return "(" + v.EmbeddedTypeExpr.TypeString() + ")"
		}
	}
	return ""
}
