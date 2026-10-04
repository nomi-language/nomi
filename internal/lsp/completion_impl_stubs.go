package lsp

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Inside `impl Iface for T { ... }`, where a function declaration can start,
// completion offers the interface's functions the block does not define yet,
// each as a whole declaration with its signature written for T. The position
// is read from the tokens rather than the reparsed tree: an impl body admits
// only declarations, so a half-typed name there is a parse error, and the
// block may not be closed yet.

// implStubSite is an interface impl block around the cursor.
type implStubSite struct {
	// header is the block's `impl Iface for T` parsed on its own, so its
	// Interface and Receiver are there however broken the body is.
	header *ast.ImplBlock
	// items are the block's other declarations, parsed best-effort from
	// the block without the cursor's line; nil when they do not parse.
	items []ast.Node
	// written names every function the block declares, read from its
	// tokens.
	written map[string]bool
	// afterFn reports that the word follows a written `fn`, which the
	// item replaces too.
	afterFn bool
}

// classifyImplItem reports whether the word at the cursor starts a line of
// an `impl Iface for T` block's body, alone or after `fn`, and if so makes
// the context ctxImplItem. After `fn` the word an item replaces starts at
// the `fn`, so the item writes the whole declaration.
func classifyImplItem(ctx *completionContext, content string, off int) bool {
	if !strings.Contains(content[:ctx.start], "impl") {
		return false
	}
	offs := lineOffsets(content)
	tokens := lexer.Lex(content)
	offOf := func(t token.Token) int { return posToOffset(offs, t.Line, t.Col) }
	lineStart := offs[lineIndexOf(offs, ctx.start)]

	// first is the first token at or after the word; the tokens on the
	// cursor's line before it may only be `fn`.
	first := len(tokens)
	for i, t := range tokens {
		if t.Type == token.EOF || offOf(t) >= ctx.start {
			first = i
			break
		}
	}
	start := ctx.start
	var onLine []int
	for i := first - 1; i >= 0 && offOf(tokens[i]) >= lineStart; i-- {
		switch tokens[i].Type {
		case token.NEWLINE, token.BLANK_LINE:
			continue
		}
		onLine = append(onLine, i)
	}
	before := first
	switch len(onLine) {
	case 0:
	case 1:
		if tokens[onLine[0]].Type != token.FN {
			return false
		}
		before = onLine[0]
		start = offOf(tokens[onLine[0]])
	default:
		return false
	}
	// The innermost bracket open at the cursor must be the block's `{`.
	open := -1
	depth := 0
	for i := before - 1; i >= 0 && open < 0; i-- {
		switch tokens[i].Type {
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			depth++
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			if depth > 0 {
				depth--
				continue
			}
			if tokens[i].Type != token.LBRACE {
				return false
			}
			open = i
		}
	}
	if open < 0 {
		return false
	}
	impl, hasFor := -1, false
	for i := open - 1; i >= 0; i-- {
		t := tokens[i]
		if t.Type == token.LBRACE || t.Type == token.RBRACE {
			return false
		}
		if t.Type == token.FOR {
			hasFor = true
		}
		if t.Type == token.IMPL {
			if t.Col != 1 {
				return false
			}
			impl = i
			break
		}
	}
	if impl < 0 || !hasFor {
		return false
	}
	implOff, openOff := offOf(tokens[impl]), offOf(tokens[open])
	nodes, _, _ := parser.ParseResilient(lexer.Lex(content[implOff:openOff+1] + "}"))
	if len(nodes) == 0 {
		return false
	}
	header, ok := nodes[0].(*ast.ImplBlock)
	if !ok || header.Interface == nil {
		return false
	}

	// The block runs to its closing `}`, or, unclosed, to the next line
	// after the cursor's that starts at column 1.
	cursorLine := lineIndexOf(offs, ctx.start) + 1
	site := &implStubSite{header: header, written: map[string]bool{}}
	end, closed := len(content), false
	depth = 0
scan:
	for i := open + 1; i < len(tokens); i++ {
		t := tokens[i]
		if t.Type == token.EOF {
			break
		}
		if t.Line > cursorLine && t.Col == 1 && t.Type != token.RBRACE && t.Type != token.NEWLINE && t.Type != token.BLANK_LINE {
			end = offs[t.Line-1]
			break
		}
		switch t.Type {
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			if depth == 0 {
				end, closed = offOf(t)+1, true
				break scan
			}
			depth--
		case token.FN:
			if depth == 0 && t.Line != cursorLine && i+1 < len(tokens) && tokens[i+1].Type == token.IDENT {
				site.written[tokens[i+1].Lexeme] = true
			}
		}
	}
	// The other items, for the interface type arguments they pin.
	cursorEnd := len(content)
	if cursorLine < len(offs) {
		cursorEnd = offs[cursorLine]
	}
	if end >= cursorEnd {
		block := content[implOff:lineStart] + content[cursorEnd:end]
		if !closed {
			block += "\n}"
		}
		if nodes, _, _ := parser.ParseResilient(lexer.Lex(block)); len(nodes) > 0 {
			if b, ok := nodes[0].(*ast.ImplBlock); ok {
				site.items = b.Items
			}
		}
	}

	ctx.kind = ctxImplItem
	ctx.implStub = site
	site.afterFn = start != ctx.start
	ctx.start = start
	ctx.prefix = content[start:off]
	return true
}

// implStubCandidates offers the interface's functions the block does not
// define: required ones in declaration order, then the open defaults an
// impl may override, and first, when more than one required function is
// missing, one item that writes them all.
func (r *completionRequest) implStubCandidates() []candidate {
	site := r.ctx.implStub
	idef := r.interfaceDecl(site.header.Interface)
	if idef == nil {
		return nil
	}
	ifaceName := idef.Name
	var required, defaults []*ast.InterfaceMethod
	for i := range idef.Methods {
		m := &idef.Methods[i]
		if site.written[m.Name] {
			continue
		}
		switch {
		case m.Body == nil && !m.Extern:
			required = append(required, m)
		case m.Open:
			// A default that is not open is final: an impl may not
			// override it.
			defaults = append(defaults, m)
		}
	}
	if len(required)+len(defaults) == 0 {
		return nil
	}

	indent := r.lineIndent()
	lead := ""
	if indent == "" {
		// The cursor sits at column 1 inside the block: indent the item
		// as `nomi fmt` would.
		indent = strings.Repeat(" ", format.IndentWidth)
		lead = indent
	}
	g := newStubGen(idef, site)
	// What was typed is matched against `fn name` after a `fn`, or while
	// the word is still spelling `fn`, and against the name otherwise.
	withFn := site.afterFn || (r.ctx.prefix != "" && strings.HasPrefix("fn", r.ctx.prefix))
	filterOf := func(name string) string {
		if withFn {
			return "fn " + name
		}
		return name
	}

	var out []candidate
	if len(required) > 1 {
		var snippet, plain strings.Builder
		next := g.firstBodyStop()
		for i, m := range required {
			if i > 0 {
				snippet.WriteString("\n\n" + indent)
				plain.WriteString("\n\n" + indent)
			}
			snippet.WriteString(g.stub(m, indent, fmt.Sprintf("${%d}", next), true))
			plain.WriteString(g.stub(m, indent, "", false))
			next++
		}
		out = append(out, candidate{
			label:    "all missing functions",
			kind:     protocol.CompletionItemKindSnippet,
			detail:   fmt.Sprintf("%d functions of %s", len(required), ifaceName),
			insert:   lead + plain.String(),
			snippet:  lead + snippet.String(),
			filter:   filterOf(required[0].Name),
			locality: -1,
			asIs:     true,
		})
	}
	add := func(m *ast.InterfaceMethod, order int, isDefault bool) {
		detail := g.signature(m, false)
		if isDefault {
			detail = "default: " + detail
		}
		out = append(out, candidate{
			label:   m.Name,
			kind:    protocol.CompletionItemKindMethod,
			detail:  detail,
			doc:     m.Doc,
			insert:  lead + g.stub(m, indent, "", false),
			snippet: lead + g.stub(m, indent, "$0", true),
			filter:  filterOf(m.Name),
			order:   order,
			asIs:    true,
		})
	}
	for i, m := range required {
		add(m, i, false)
	}
	for i, m := range defaults {
		add(m, 1000+i, true)
	}
	return out
}

// interfaceDecl finds the declaration of the interface an impl header names:
// in scope (`Display`, an imported `Powered`) or through a file
// (`shapes.Drawable`).
func (r *completionRequest) interfaceDecl(te ast.TypeExpr) *ast.InterfaceDef {
	var sym *analysis.Symbol
	switch t := te.(type) {
	case *ast.SimpleType:
		sym = r.scope.Lookup(t.Name)
	case *ast.GenericType:
		sym = r.scope.Lookup(t.Name)
	case *ast.QualifiedType:
		if scope := r.moduleScopeOf(t.Module); scope != nil {
			sym = scope.Lookup(analysis.TypeExprBaseName(t.Member))
		}
	}
	sym = realSymbol(sym)
	if sym == nil || sym.Kind != analysis.SymbolInterface {
		return nil
	}
	idef, _ := sym.Node.(*ast.InterfaceDef)
	return idef
}

// stubGen writes the declarations of one interface's functions for one
// impl block.
type stubGen struct {
	receiver string
	selfName string
	// args are the interface's type arguments as the block spells them:
	// from the header (`Add<Days, Date>`) or pinned by a function the
	// block already defines. A parameter missing here is a linked
	// placeholder, numbered by holes.
	args  map[string]string
	holes map[string]int
}

func newStubGen(idef *ast.InterfaceDef, site *implStubSite) *stubGen {
	recv := site.header.Receiver
	g := &stubGen{
		receiver: recv.TypeString(),
		selfName: snakeCase(receiverBaseName(recv)),
		args:     map[string]string{},
		holes:    map[string]int{},
	}
	params := map[string]bool{}
	for _, tp := range idef.TypeParams {
		params[tp.Name] = true
	}
	iface := site.header.Interface
	if q, ok := iface.(*ast.QualifiedType); ok {
		iface = q.Member
	}
	if gt, ok := iface.(*ast.GenericType); ok && len(gt.Params) == len(idef.TypeParams) {
		for i, tp := range idef.TypeParams {
			g.args[tp.Name] = gt.Params[i].TypeString()
		}
	}
	for _, item := range site.items {
		fn, ok := item.(*ast.FuncDef)
		var params2 []ast.Param
		var ret ast.TypeExpr
		var name string
		switch {
		case ok:
			name, params2, ret = fn.Name, fn.Params, fn.ReturnTypeExpr
		default:
			ext, ok := item.(*ast.ExternFunc)
			if !ok {
				continue
			}
			name, params2, ret = ext.Name, ext.Params, ext.ReturnTypeExpr
		}
		for i := range idef.Methods {
			m := &idef.Methods[i]
			if m.Name != name {
				continue
			}
			local := typeParamNames(m.TypeParams)
			for k := 0; k < len(m.Params) && k < len(params2); k++ {
				pinTypeArgs(m.Params[k].TypeAnnotation, params2[k].TypeAnnotation, params, local, g.args)
			}
			pinTypeArgs(m.ReturnTypeExpr, ret, params, local, g.args)
		}
	}
	n := 1
	for _, tp := range idef.TypeParams {
		if _, ok := g.args[tp.Name]; !ok {
			g.holes[tp.Name] = n
			n++
		}
	}
	return g
}

// firstBodyStop is the first tab stop after the type placeholders.
func (g *stubGen) firstBodyStop() int {
	return len(g.holes) + 1
}

// pinTypeArgs matches an interface signature's type against the type an
// impl function wrote in its place and records what each of the interface's
// type parameters stands for.
func pinTypeArgs(iface, impl ast.TypeExpr, params, local map[string]bool, out map[string]string) {
	if iface == nil || impl == nil {
		return
	}
	switch t := iface.(type) {
	case *ast.SimpleType:
		if params[t.Name] && !local[t.Name] {
			if _, ok := out[t.Name]; !ok {
				out[t.Name] = impl.TypeString()
			}
		}
	case *ast.GenericType:
		if u, ok := impl.(*ast.GenericType); ok && u.Name == t.Name && len(u.Params) == len(t.Params) {
			for i := range t.Params {
				pinTypeArgs(t.Params[i], u.Params[i], params, local, out)
			}
		}
	case *ast.FuncType:
		if u, ok := impl.(*ast.FuncType); ok && len(u.Params) == len(t.Params) {
			for i := range t.Params {
				pinTypeArgs(t.Params[i], u.Params[i], params, local, out)
			}
			pinTypeArgs(t.Return, u.Return, params, local, out)
		}
	case *ast.QualifiedType:
		if u, ok := impl.(*ast.QualifiedType); ok && u.Module == t.Module {
			pinTypeArgs(t.Member, u.Member, params, local, out)
		}
	}
}

// stub writes m's declaration: the signature, then a body holding body (a
// tab stop, or nothing in plain text) on its own line one level in.
func (g *stubGen) stub(m *ast.InterfaceMethod, indent, body string, snippet bool) string {
	inner := indent + strings.Repeat(" ", format.IndentWidth)
	return g.signature(m, snippet) + " {\n" + inner + body + "\n" + indent + "}"
}

// signature writes m's signature for the impl: `self` becomes the receiver
// as the header spells it, the interface's type parameters become their
// arguments, and a lone self parameter is named after the receiver type.
// The other parameters keep the interface's names, which an impl must.
func (g *stubGen) signature(m *ast.InterfaceMethod, snippet bool) string {
	esc := func(s string) string {
		if snippet {
			return snippetEscape(s)
		}
		return s
	}
	local := typeParamNames(m.TypeParams)
	typ := func(te ast.TypeExpr) string { return g.renderType(te, local, snippet) }
	selfName := g.selfNameFor(m)
	var b strings.Builder
	b.WriteString("fn " + esc(m.Name))
	if len(m.TypeParams) > 0 {
		names := make([]string, len(m.TypeParams))
		for i, tp := range m.TypeParams {
			names[i] = esc(tp.Name)
		}
		b.WriteString("<" + strings.Join(names, ", ") + ">")
	}
	b.WriteString("(")
	for i, p := range m.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		name := hoverdoc.ParamDisplayName(p)
		if selfName != "" && p.Destructure == nil && isSelfTypeExpr(p.TypeAnnotation) {
			name = selfName
		}
		b.WriteString(esc(name))
		if p.TypeAnnotation != nil {
			b.WriteString(": " + typ(p.TypeAnnotation))
		}
		if p.Default != nil {
			b.WriteString(" = " + esc(format.RenderNode(p.Default)))
		}
	}
	b.WriteString(")")
	if m.ReturnTypeExpr != nil {
		b.WriteString(": " + typ(m.ReturnTypeExpr))
	}
	return b.String()
}

// selfNameFor names m's self parameter after the receiver type (`item` for
// `Item`, `set` for `Set<T>`) when m has exactly one and the name is free; ""
// keeps the interface's names.
func (g *stubGen) selfNameFor(m *ast.InterfaceMethod) string {
	selfs := 0
	for _, p := range m.Params {
		if isSelfTypeExpr(p.TypeAnnotation) {
			selfs++
		}
	}
	if selfs != 1 || !isPlainIdent(g.selfName) {
		return ""
	}
	for _, p := range m.Params {
		if !isSelfTypeExpr(p.TypeAnnotation) && p.Name == g.selfName {
			return ""
		}
	}
	return g.selfName
}

// renderType spells te in the impl: `self` as the receiver, an interface
// type parameter as its argument or its placeholder.
func (g *stubGen) renderType(te ast.TypeExpr, local map[string]bool, snippet bool) string {
	esc := func(s string) string {
		if snippet {
			return snippetEscape(s)
		}
		return s
	}
	switch t := te.(type) {
	case *ast.SelfType:
		return esc(g.receiver)
	case *ast.SimpleType:
		if t.Name == "self" {
			return esc(g.receiver)
		}
		if !local[t.Name] {
			if arg, ok := g.args[t.Name]; ok {
				return esc(arg)
			}
			if n, ok := g.holes[t.Name]; ok && snippet {
				return fmt.Sprintf("${%d:%s}", n, snippetEscape(t.Name))
			}
		}
		return esc(t.Name)
	case *ast.QualifiedType:
		return esc(t.Module+".") + g.renderType(t.Member, local, snippet)
	case *ast.GenericType:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = g.renderType(p, local, snippet)
		}
		return esc(t.Name) + "<" + strings.Join(parts, ", ") + ">"
	case *ast.FuncType:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = g.renderType(p, local, snippet)
		}
		out := "(" + strings.Join(parts, ", ") + ")"
		if t.Return != nil {
			out += " -> " + g.renderType(t.Return, local, snippet)
		}
		return out
	case *ast.AnonStructType:
		fields := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = esc(f.Name) + ": " + g.renderType(f.TypeAnnotation, local, snippet)
		}
		return "{" + strings.Join(fields, ", ") + "}"
	}
	if te == nil {
		return ""
	}
	return esc(te.TypeString())
}

func isSelfTypeExpr(te ast.TypeExpr) bool {
	switch t := te.(type) {
	case *ast.SelfType:
		return true
	case *ast.SimpleType:
		return t.Name == "self"
	}
	return false
}

func typeParamNames(tps []ast.TypeParam) map[string]bool {
	out := make(map[string]bool, len(tps))
	for _, tp := range tps {
		out[tp.Name] = true
	}
	return out
}

// receiverBaseName is the receiver type's own name, without its file or
// type arguments: `Set` for `Set<T>`, `Item` for `shop.Item`.
func receiverBaseName(te ast.TypeExpr) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	case *ast.QualifiedType:
		return receiverBaseName(t.Member)
	}
	return ""
}

// isPlainIdent reports whether s lexes as one ordinary identifier, not a
// keyword (`test`, `case`, `type`).
func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	toks := lexer.Lex(s)
	return len(toks) > 0 && toks[0].Type == token.IDENT && toks[0].Lexeme == s
}
