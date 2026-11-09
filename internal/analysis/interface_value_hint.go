package analysis

import (
	"fmt"
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
)

// InterfaceValueParamCode tags the advice that a function's type parameter
// can be an interface value (InterfaceValueHints).
const InterfaceValueParamCode = "interface-value-parameter"

// InterfaceValueHints are the advice of the file whose analysis is fa and
// whose top-level nodes are nodes: one for each function type parameter
// that style rule 52 says to write as an interface value, `fn label(value:
// Named)` for `fn label<T>(value: T) where T: Named`. Each carries a Fix
// that rewrites the function. None is an error: the program means the same
// either way.
//
// A type parameter qualifies when the rewrite checks and means the same:
//
//   - it is a type parameter of a top-level function or of a function in an
//     inherent impl block (an interface impl's function keeps the
//     interface's signature), with no default;
//   - the signature names it once, as one parameter's whole type: not in the
//     result, another parameter, another bound or inside another type;
//   - it has one bound, an interface named as itself (not a bound alias),
//     whose type arguments name no type parameter;
//   - the body names it only as the owner of that interface's functions
//     (`T.name(value)`), each taking `self` once, as a plain parameter, and
//     not returning it (spec §12 *Interface values*);
//   - the body uses the parameter only as a call's argument, a pipe's
//     subject or an interpolated value, and applies no operator to a value
//     whose type holds the type parameter;
//   - no call in the file passes the function explicit type arguments.
//
// A call in another file that passes explicit type arguments is not looked
// for: finding one would read every file of the program on each edit. Such
// a call is redundant, since the argument's type gives the type parameter,
// and after the fix it is an error at the call that deleting its type
// arguments repairs.
//
// A std file gets none, whether it is the source tree's or the copy
// go-to-definition materializes under the std cache: std's signatures are
// chosen for its lowering, and a reader cannot apply a fix there.
//
// tokens answers the file's tokens; it is called only when some type
// parameter qualifies, which the edits are placed by.
func InterfaceValueHints(fa *FileAnalysis, nodes []ast.Node, tokens func() []token.Token) []TypeError {
	if fa == nil {
		return nil
	}
	if _, isStd := stdlibModuleForPath(fa.FilePath); isStd {
		return nil
	}
	var cands []ivCandidate
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.FuncDef:
			cands = append(cands, interfaceValueCandidates(fa, v, nil)...)
		case *ast.ImplBlock:
			if v.Interface != nil {
				continue
			}
			for _, item := range v.Items {
				if f, ok := item.(*ast.FuncDef); ok {
					cands = append(cands, interfaceValueCandidates(fa, f, v)...)
				}
			}
		}
	}
	if len(cands) == 0 {
		return nil
	}
	explicit := namesCalledWithTypeArgs(nodes)
	var toks []token.Token
	var out []TypeError
	for _, c := range cands {
		if explicit[c.fn.Name] {
			continue
		}
		if toks == nil {
			toks = tokens()
		}
		edits, ok := c.edits(toks)
		if !ok {
			continue
		}
		p := c.fn.Params[c.param]
		e := TypeError{
			Line:    c.tp.Line,
			Col:     c.tp.Col,
			EndLine: c.tp.Line,
			EndCol:  c.tp.Col + len(c.tp.Name),
			Code:    InterfaceValueParamCode,
			Message: fmt.Sprintf("`%s` is used only as the type of `%s`, under one bound: write `%s: %s`", c.tp.Name, p.Name, p.Name, c.boundText),
			Fix:     &parser.Fix{Title: fmt.Sprintf("Write `%s: %s`", p.Name, c.boundText), Edits: edits},
		}
		out = append(out, e.WithHint(fmt.Sprintf("a parameter of type %s takes a value of any implementor, as `%s` does; keep `%s` when the caller's type matters past the call", c.boundText, c.tp.Name, c.tp.Name)))
	}
	return out
}

// ivCandidate is a type parameter that InterfaceValueHints may advise on:
// fn's type parameter tp, the type of fn.Params[param], bounded by bound,
// written boundText, whose functions the body calls as ownerText.f.
type ivCandidate struct {
	fn        *ast.FuncDef
	tp        ast.TypeParam
	param     int
	bound     ast.TypeExpr
	inWhere   bool
	boundText string
	ownerText string
	owners    []*ast.TypeIdent
}

// interfaceValueCandidates are fn's type parameters that qualify, except
// for the explicit type arguments of calls, which InterfaceValueHints reads
// once for the file. block is fn's inherent impl block, or nil.
func interfaceValueCandidates(fa *FileAnalysis, fn *ast.FuncDef, block *ast.ImplBlock) []ivCandidate {
	if fn.Body == nil || fn.Expansion != nil || fn.AutoSynth || fn.ImplFunction || fn.Export || len(fn.TypeParams) == 0 {
		return nil
	}
	sym := fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil {
		return nil
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || len(ft.Params) != len(fn.Params) {
		return nil
	}
	var names []string
	if block != nil {
		for _, g := range block.Generics {
			names = append(names, g.Name)
		}
	}
	for _, tp := range fn.TypeParams {
		names = append(names, tp.Name)
	}
	var out []ivCandidate
	for _, tp := range fn.TypeParams {
		if c, ok := interfaceValueCandidate(fa, fn, ft, tp, names, block == nil && fn.Public); ok {
			out = append(out, c)
		}
	}
	return out
}

// public says fn is a top-level `pub fn`, whose parameters may not name a
// private type of the file (spec §14 *Visibility consistency*), though its
// bounds may.
func interfaceValueCandidate(fa *FileAnalysis, fn *ast.FuncDef, ft *FuncType, tp ast.TypeParam, names []string, public bool) (ivCandidate, bool) {
	c := ivCandidate{fn: fn, tp: tp, param: -1}
	if tp.Default != nil {
		return c, false
	}
	shadowed := 0
	for _, n := range names {
		if n == tp.Name {
			shadowed++
		}
	}
	if shadowed != 1 {
		return c, false
	}
	// Its bounds, inline and in the where clause, and every other mention
	// of it in the signature.
	bounds := append([]ast.TypeExpr(nil), tp.Bounds...)
	uses := 0
	for _, other := range fn.TypeParams {
		for _, b := range other.Bounds {
			uses += typeExprMentions(b, tp.Name)
		}
	}
	for _, w := range fn.WhereClauses {
		if w.Name == tp.Name {
			bounds = append(bounds, w.Bounds...)
			c.inWhere = true
		}
		for _, b := range w.Bounds {
			uses += typeExprMentions(b, tp.Name)
		}
	}
	if len(bounds) != 1 || uses != 0 || typeExprMentions(fn.ReturnTypeExpr, tp.Name) != 0 {
		return c, false
	}
	c.bound = bounds[0]
	for i, p := range fn.Params {
		n := typeExprMentions(p.TypeAnnotation, tp.Name)
		if n == 0 {
			continue
		}
		if c.param >= 0 || n != 1 || !isBareTypeName(p.TypeAnnotation, tp.Name) {
			return c, false
		}
		c.param = i
	}
	if c.param < 0 {
		return c, false
	}
	p := fn.Params[c.param]
	if p.Destructure != nil || p.Default != nil || p.TypeOnly {
		return c, false
	}
	param, ok := ft.Params[c.param].(*TypeParam_)
	if !ok {
		return c, false
	}
	var ifaces []*InterfaceType
	for _, wb := range ft.WhereBounds {
		if wb.Param == param {
			ifaces = append(ifaces, wb.Bounds...)
		}
	}
	if len(ifaces) != 1 {
		return c, false
	}
	iface := ifaces[0]
	if iface == nil || iface.DefaultSelf {
		return c, false
	}
	for _, n := range names {
		if typeExprMentions(c.bound, n) != 0 {
			return c, false
		}
	}
	switch b := c.bound.(type) {
	case *ast.SimpleType:
		c.ownerText = b.Name
	case *ast.GenericType:
		c.ownerText = b.Name
	case *ast.QualifiedType:
		switch m := b.Member.(type) {
		case *ast.SimpleType:
			c.ownerText = b.Module + "." + m.Name
		case *ast.GenericType:
			c.ownerText = b.Module + "." + m.Name
		default:
			return c, false
		}
	default:
		return c, false
	}
	if base := c.ownerText[len(c.ownerText)-len(iface.Name):]; len(c.ownerText) < len(iface.Name) || base != iface.Name {
		return c, false
	}
	if public && namesPrivateType(fa, c.bound) {
		return c, false
	}
	c.boundText = c.bound.TypeString()
	owners, ok := interfaceValueBody(fa, fn.Body, p, tp.Name, param, iface)
	if !ok {
		return c, false
	}
	c.owners = owners
	return c, true
}

// interfaceValueBody reports whether fn's body uses the type parameter
// named name (tp, bounded by iface) and its parameter p only as an
// interface value can be used, and answers each `T` that owns a call.
func interfaceValueBody(fa *FileAnalysis, body *ast.Block, p ast.Param, name string, tp *TypeParam_, iface *InterfaceType) ([]*ast.TypeIdent, bool) {
	var owners []*ast.TypeIdent
	ok := true
	var walk func(n, parent ast.Node)
	walk = func(n, parent ast.Node) {
		if !ok || n == nil {
			return
		}
		switch v := n.(type) {
		case *ast.TypeIdent:
			if v.Name != name {
				break
			}
			fa, isOwner := parent.(*ast.FieldAccess)
			if !isOwner || fa.Object != ast.Node(v) || fa.Field == nil || !callableOnInterfaceValue(iface, fa.Field.Name) {
				ok = false
				return
			}
			owners = append(owners, v)
		case *ast.SimpleType:
			if v.Name == name {
				ok = false
				return
			}
		case *ast.GenericType:
			if v.Name == name {
				ok = false
				return
			}
		case *ast.FuncDef:
			for _, t := range v.TypeParams {
				if t.Name == name {
					ok = false
					return
				}
			}
		case *ast.Ident:
			if v.Name != p.Name {
				break
			}
			sym := fa.References[Pos{Line: v.Line, Col: v.Col}]
			if sym == nil || sym.Pos.Line != p.Line || sym.Pos.Col != p.Col {
				break
			}
			if !interfaceValueUse(v, parent) {
				ok = false
				return
			}
		case *ast.Binary:
			if v.Op != "|>" && (holdsTypeParam(fa.ExprTypes[v.Left], tp) || holdsTypeParam(fa.ExprTypes[v.Right], tp)) {
				ok = false
				return
			}
		case *ast.Unary:
			if holdsTypeParam(fa.ExprTypes[v.Right], tp) {
				ok = false
				return
			}
		}
		ast.Children(n, func(c ast.Node) { walk(c, n) })
	}
	walk(body, nil)
	return owners, ok
}

// interfaceValueUse reports whether a use of the parameter, id, under
// parent is one an interface value serves as the type parameter's value
// does: a call's argument, a pipe's subject, an interpolated value.
func interfaceValueUse(id *ast.Ident, parent ast.Node) bool {
	switch v := parent.(type) {
	case *ast.Call:
		return v.Func != ast.Node(id)
	case *ast.NamedArg:
		return true
	case *ast.Binary:
		return v.Op == "|>" && v.Left == ast.Node(id)
	case *ast.StringInterp:
		return true
	}
	return false
}

// callableOnInterfaceValue reports whether iface's function method can be
// called on a value of type iface: it takes `self` once, as a parameter of
// its own, and nothing else in its signature is `self` (spec §12 *Interface
// values*).
func callableOnInterfaceValue(iface *InterfaceType, method string) bool {
	sig := interfaceMethodSig(iface, method)
	if sig == nil || iface.SelfParam == nil {
		return false
	}
	self := iface.SelfParam
	selves := 0
	for _, p := range sig.Params {
		if tp, ok := p.(*TypeParam_); ok && tp == self {
			selves++
			continue
		}
		if mentionsTypeParamDeep(p, self, 0) {
			return false
		}
	}
	return selves == 1 && (sig.Return == nil || !mentionsTypeParamDeep(sig.Return, self, 0))
}

// holdsTypeParam reports whether t, following solved variables, holds tp.
func holdsTypeParam(t Type, tp *TypeParam_) bool {
	return t != nil && mentionsTypeParamDeep(t, tp, 0)
}

// typeExprMentions counts the names name in the type expression t.
func typeExprMentions(t ast.TypeExpr, name string) int {
	if t == nil {
		return 0
	}
	n := 0
	ast.Inspect(t, func(x ast.Node) bool {
		switch v := x.(type) {
		case *ast.SimpleType:
			if v.Name == name {
				n++
			}
		case *ast.GenericType:
			if v.Name == name {
				n++
			}
		case *ast.TypeIdent:
			if v.Name == name {
				n++
			}
		}
		return true
	})
	return n
}

// namesPrivateType reports whether t names a private top-level type of
// fa's file.
func namesPrivateType(fa *FileAnalysis, t ast.TypeExpr) bool {
	if fa.ModuleScope == nil {
		return false
	}
	private := false
	ast.Inspect(t, func(x ast.Node) bool {
		var name string
		switch v := x.(type) {
		case *ast.SimpleType:
			name = v.Name
		case *ast.GenericType:
			name = v.Name
		case *ast.QualifiedType:
			return false
		default:
			return true
		}
		if top := fa.ModuleScope.LookupLocal(name); top != nil && top.Resolved == nil && isTypeSymbolKind(top.Kind) && !top.Public {
			private = true
		}
		return true
	})
	return private
}

// isBareTypeName reports whether t is the type name alone.
func isBareTypeName(t ast.TypeExpr, name string) bool {
	s, ok := t.(*ast.SimpleType)
	return ok && s.Name == name
}

// namesCalledWithTypeArgs are the names of the functions some call among
// nodes passes explicit type arguments (`label<Box>(b)`, `Box.make<Int>()`).
func namesCalledWithTypeArgs(nodes []ast.Node) map[string]bool {
	out := map[string]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(x ast.Node) bool {
			call, ok := x.(*ast.Call)
			if !ok || len(call.TypeArgs) == 0 {
				return true
			}
			switch f := call.Func.(type) {
			case *ast.Ident:
				out[f.Name] = true
			case *ast.FieldAccess:
				if f.Field != nil {
					out[f.Field.Name] = true
				}
			}
			return true
		})
	}
	return out
}

// edits are the fix's text edits: the type parameter out of the list, its
// where entry out of the clause, the parameter's type written as the bound,
// and each call owner written as the interface. toks are the file's tokens.
func (c ivCandidate) edits(toks []token.Token) ([]parser.Edit, bool) {
	var edits []parser.Edit
	list, ok := c.typeParamEdit(toks)
	if !ok {
		return nil, false
	}
	edits = append(edits, list)
	if c.inWhere {
		where, ok := c.whereEdit(toks)
		if !ok {
			return nil, false
		}
		edits = append(edits, where)
	}
	ann, ok := c.fn.Params[c.param].TypeAnnotation.(*ast.SimpleType)
	if !ok || ann.Line == 0 {
		return nil, false
	}
	edits = append(edits, parser.Edit{Start: parser.Pos{Line: ann.Line, Col: ann.Col}, End: parser.Pos{Line: ann.Line, Col: ann.Col + len(ann.Name)}, Text: c.boundText})
	for _, o := range c.owners {
		edits = append(edits, parser.Edit{Start: parser.Pos{Line: o.Line, Col: o.Col}, End: parser.Pos{Line: o.Line, Col: o.Col + len(o.Name)}, Text: c.ownerText})
	}
	sort.Slice(edits, func(i, j int) bool {
		a, b := edits[i].Start, edits[j].Start
		return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
	})
	for i := 1; i < len(edits); i++ {
		prev, cur := edits[i-1].End, edits[i].Start
		if prev.Line > cur.Line || (prev.Line == cur.Line && prev.Col > cur.Col) {
			return nil, false
		}
	}
	return edits, true
}

// tokenAt is the index of the token starting at line:col, or -1.
func tokenAt(toks []token.Token, line, col int) int {
	i := sort.Search(len(toks), func(i int) bool {
		t := toks[i]
		return t.Line > line || (t.Line == line && t.Col >= col)
	})
	if i < len(toks) && toks[i].Line == line && toks[i].Col == col {
		return i
	}
	return -1
}

// trivial reports whether t is layout or a comment rather than code.
func trivial(t token.Token) bool {
	switch t.Type {
	case token.NEWLINE, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT:
		return true
	}
	return false
}

func tokStart(t token.Token) parser.Pos { return parser.Pos{Line: t.Line, Col: t.Col} }

func tokEnd(t token.Token) parser.Pos {
	if t.EndLine > 0 {
		return parser.Pos{Line: t.EndLine, Col: t.EndCol}
	}
	return parser.Pos{Line: t.Line, Col: t.Col + len(t.Lexeme)}
}

// typeParamEdit removes the type parameter from fn's `<...>` list, with its
// inline bound, or the whole list when it is the only one.
func (c ivCandidate) typeParamEdit(toks []token.Token) (parser.Edit, bool) {
	at := tokenAt(toks, c.tp.Line, c.tp.Col)
	name := tokenAt(toks, c.fn.Line, c.fn.Col)
	if at < 1 || name < 0 || toks[name].Lexeme != c.fn.Name {
		return parser.Edit{}, false
	}
	open := name + 1
	if open >= at || toks[open].Lexeme != "<" {
		return parser.Edit{}, false
	}
	// The list's top-level commas and its closing `>`.
	depth := 0
	var commas []int
	close := -1
	for i := open; i < len(toks) && close < 0; i++ {
		switch toks[i].Lexeme {
		case "<":
			depth++
		case ">":
			depth--
		case ">>":
			depth -= 2
		case ",":
			if depth == 1 {
				commas = append(commas, i)
			}
		}
		if depth <= 0 && i > open {
			if depth < 0 || toks[i].Lexeme != ">" {
				return parser.Edit{}, false
			}
			close = i
		}
	}
	if close < 0 {
		return parser.Edit{}, false
	}
	// A trailing comma separates nothing.
	if n := len(commas); n > 0 {
		next := commas[n-1] + 1
		for next < close && trivial(toks[next]) {
			next++
		}
		if next == close {
			commas = commas[:n-1]
		}
	}
	if len(commas) == 0 {
		return parser.Edit{Start: tokStart(toks[open]), End: tokEnd(toks[close])}, true
	}
	return listEntryEdit(toks, at, commas, close), true
}

// whereEdit removes the type parameter's entry from fn's where clause, or
// the whole clause when it is the only entry.
func (c ivCandidate) whereEdit(toks []token.Token) (parser.Edit, bool) {
	var w ast.WhereConstraint
	for _, x := range c.fn.WhereClauses {
		if x.Name == c.tp.Name {
			w = x
		}
	}
	at := tokenAt(toks, w.Line, w.Col)
	if at < 1 {
		return parser.Edit{}, false
	}
	kw := at - 1
	depth := 0
	for kw >= 0 {
		switch toks[kw].Lexeme {
		case ">":
			depth++
		case ">>":
			depth += 2
		case "<":
			depth--
		}
		if toks[kw].Type == token.WHERE && depth == 0 {
			break
		}
		if toks[kw].Type == token.LBRACE || toks[kw].Type == token.FN {
			return parser.Edit{}, false
		}
		kw--
	}
	if kw < 1 {
		return parser.Edit{}, false
	}
	depth = 0
	var commas []int
	end := -1
	for i := kw + 1; i < len(toks) && end < 0; i++ {
		switch toks[i].Lexeme {
		case "<", "(", "[":
			depth++
		case ">", ")", "]":
			depth--
		case ">>":
			depth -= 2
		case ",":
			if depth == 0 {
				commas = append(commas, i)
			}
		}
		if depth == 0 && toks[i].Type == token.LBRACE {
			end = i
		}
	}
	if end < 0 {
		return parser.Edit{}, false
	}
	if len(commas) == 0 {
		before := kw - 1
		for before >= 0 && trivial(toks[before]) {
			before--
		}
		if before < 0 {
			return parser.Edit{}, false
		}
		return parser.Edit{Start: tokEnd(toks[before]), End: tokStart(toks[end]), Text: " "}, true
	}
	return listEntryEdit(toks, at, commas, end), true
}

// listEntryEdit removes the entry starting at token at from a list whose
// separating commas are commas and which ends before token end: up to the
// next entry, or for the last entry from the comma before it.
func listEntryEdit(toks []token.Token, at int, commas []int, end int) parser.Edit {
	k := 0
	for k < len(commas) && commas[k] < at {
		k++
	}
	if k < len(commas) {
		next := commas[k] + 1
		for next < end && trivial(toks[next]) {
			next++
		}
		return parser.Edit{Start: tokStart(toks[at]), End: tokStart(toks[next])}
	}
	last := end - 1
	for last > at && trivial(toks[last]) {
		last--
	}
	return parser.Edit{Start: tokStart(toks[commas[k-1]]), End: tokEnd(toks[last])}
}
