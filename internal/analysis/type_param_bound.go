package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A generic body runs once per type argument its callers pass, so what it
// needs of a type parameter must hold for every one of them. A conformance
// the body demands of its own type parameter `T` (an interface-qualified
// call or pipe, an interpolation, an ordering or arithmetic operator, a
// generic call bounding it) must be one of `T`'s bounds: the function's
// `where T: X`, the impl block's, or the type declaration's. Debug is
// universal, and `==` falls back to structural equality, so neither needs
// one. Without the rule a caller passing a type that lacks the interface
// was accepted and the body then could not run for it. This is the type
// parameter counterpart of default_self_bound.go.

// checkTypeParamDemand reports a conformance to ifaceName demanded of tp by
// rec, when tp is a type parameter of the body being checked and none of its
// bounds is ifaceName.
func (c *checker) checkTypeParamDemand(tp *TypeParam_, ifaceName string, rec Recording) {
	if tp == nil || tp.Name_ == "" || tp.Name_ == "self" || tp == c.defaultSelf {
		return
	}
	if ifaceName == "Debug" || rec.Kind == RecordingKindEqualityOperator {
		return
	}
	if c.fnTypeParams[tp.Name_] != tp {
		return
	}
	for _, b := range tp.Bounds {
		if b != nil && b.Name == ifaceName {
			return
		}
	}
	pos := rec.Pos
	subject, verb := "this value", "is"
	if arg, ok := c.demandSubject(func(t Type) bool { return resolveTypeVar(t) == Type(tp) }); ok {
		pos.Line, pos.Col = exprStartLineCol(arg)
		if text, ok := exprPathText(arg); ok {
			subject = "`" + text + "`"
		}
	} else if arg, ok := c.demandSubject(func(t Type) bool { return t != nil && mentionsTypeParam(t, tp) }); ok {
		// `"${xs}"` with xs a List<T>: the demand reaches T through xs.
		pos.Line, pos.Col = exprStartLineCol(arg)
		verb = "holds"
		if text, ok := exprPathText(arg); ok {
			subject = "`" + text + "`"
		}
	} else if sym := c.fa.References[pos]; sym != nil && (sym.Kind == SymbolParam || sym.Kind == SymbolBinding) {
		subject = "`" + sym.Name + "`"
	}
	article := "a"
	if verb == "holds" {
		article = "values of"
	}
	owner, help := c.typeParamBoundOwner(tp, ifaceName)
	msg := fmt.Sprintf("%s %s %s `%s`, and %s does not require %s of `%s`", subject, verb, article, tp.Name_, owner, ifaceName, tp.Name_)
	for _, e := range c.errors {
		if e.Line == pos.Line && e.Col == pos.Col && e.Message == msg {
			return
		}
	}
	c.report(TypeError{Line: pos.Line, Col: pos.Col, Message: msg}.WithHint(help))
}

// demandSubject finds the expression, among those a demand raised next may
// be about, whose type match accepts.
func (c *checker) demandSubject(match func(Type) bool) (ast.Node, bool) {
	for _, arg := range c.selfDemandSubjects {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if arg != nil && match(c.fa.ExprTypes[arg]) {
			return arg, true
		}
	}
	return nil, false
}

// typeParamBoundOwner names the declaration that would bound tp, and the
// hint showing the bound added there.
func (c *checker) typeParamBoundOwner(tp *TypeParam_, ifaceName string) (string, string) {
	fn := c.currentFnDecl
	if c.currentInterface != "" && c.defaultSelfFn != "" {
		iface, _ := c.reg.Lookup(c.currentInterface).(*InterfaceType)
		sig := (*MethodSig)(nil)
		if iface != nil {
			sig = interfaceMethodSig(iface, c.defaultSelfFn)
		}
		owner := "`" + c.currentInterface + "." + c.defaultSelfFn + "`"
		if sig == nil {
			return owner, fmt.Sprintf("require it: `where %s: %s`", tp.Name_, ifaceName)
		}
		return owner, fmt.Sprintf("require it in the signature: `%s`", sigWithBound("fn "+c.defaultSelfFn, sig.ParamNames, sig.Params, sig.Return, c.fnTypeParams, tp, ifaceName, func(p *TypeParam_) bool { return p.Name_ != "self" }))
	}
	if fn == nil {
		return "this function", fmt.Sprintf("require it: `where %s: %s`", tp.Name_, ifaceName)
	}
	declared := map[string]bool{}
	for _, p := range fn.TypeParams {
		declared[p.Name] = true
	}
	inherent := c.implBlock != nil && c.implBlock.Interface == nil
	if !declared[tp.Name_] && c.implBlock != nil && !inherent {
		// An interface impl's parameter is bounded on the block; an inherent
		// block has no where clause, so its functions bound it themselves.
		head := "impl " + typeExprHoverString(c.implBlock.Interface) + " for " + typeExprHoverString(c.implBlock.Receiver)
		return "`" + head + "`", fmt.Sprintf("require it on the impl block: `%s where %s`", head, boundsText(tp, ifaceName))
	}
	name := fn.Name
	if c.implBlock != nil {
		name = TypeExprBaseName(c.implBlock.Receiver) + "." + fn.Name
	}
	sym := c.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	ft, _ := symType(sym).(*FuncType)
	if ft == nil {
		return "`" + name + "`", fmt.Sprintf("require it: `where %s: %s`", tp.Name_, ifaceName)
	}
	names := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		names[i] = p.Name
	}
	head := "fn " + fn.Name
	if len(fn.TypeParams) > 0 {
		tps := make([]string, len(fn.TypeParams))
		for i, p := range fn.TypeParams {
			tps[i] = p.Name
		}
		head += "<" + strings.Join(tps, ", ") + ">"
	}
	return "`" + name + "`", fmt.Sprintf("require it in the signature: `%s`", sigWithBound(head, names, ft.Params, ft.Return, c.fnTypeParams, tp, ifaceName, func(p *TypeParam_) bool { return declared[p.Name_] || inherent }))
}

func symType(sym *Symbol) Type {
	if sym == nil {
		return nil
	}
	return sym.Type
}

// sigWithBound renders `head(params) -> Ret where ...`, the where clause
// naming each type parameter's present bounds and ifaceName added to tp's.
func sigWithBound(head string, names []string, params []Type, ret Type, inScope map[string]*TypeParam_, tp *TypeParam_, ifaceName string, own func(*TypeParam_) bool) string {
	ps := make([]string, len(params))
	for i, p := range params {
		name := "_"
		if i < len(names) && names[i] != "" {
			name = names[i]
		}
		ps[i] = name + ": " + typeString(p)
	}
	out := head + "(" + strings.Join(ps, ", ") + ")"
	if ret != nil && !isUnitLike(ret) {
		out += " -> " + typeString(ret)
	}
	var clauses []string
	order := make([]string, 0, len(inScope))
	for name := range inScope {
		order = append(order, name)
	}
	sort.Strings(order)
	for _, name := range order {
		p := inScope[name]
		if p == nil || p == tp || !own(p) || len(p.Bounds) == 0 {
			continue
		}
		clauses = append(clauses, boundsText(p, ""))
	}
	clauses = append(clauses, boundsText(tp, ifaceName))
	return out + " where " + strings.Join(clauses, ", ")
}

// boundsText renders `T: A and B`, with add appended when it is not empty.
func boundsText(tp *TypeParam_, add string) string {
	seen := map[string]bool{}
	var names []string
	for _, b := range tp.Bounds {
		if b == nil || seen[b.String()] {
			continue
		}
		seen[b.String()] = true
		names = append(names, b.String())
	}
	if add != "" {
		names = append(names, add)
	}
	return tp.Name_ + ": " + strings.Join(names, " and ")
}

// exprPathText renders a name or a field path (`b.value`).
func exprPathText(n ast.Node) (string, bool) {
	switch e := n.(type) {
	case *ast.Ident:
		return e.Name, true
	case *ast.FieldAccess:
		if e.Field == nil {
			return "", false
		}
		obj, ok := exprPathText(e.Object)
		if !ok {
			return "", false
		}
		return obj + "." + e.Field.Name, true
	}
	return "", false
}
