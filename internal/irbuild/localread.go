package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// readRoute is how identRead found the value a name holds.
type readRoute int

const (
	// readBound: a binding this body holds by the name (`bound`), a
	// closure's capture of one included.
	readBound readRoute = iota
	// readCaptured: a capture this read made, of the enclosing body's
	// value.
	readCaptured
	// readParam: a parameter of the function being lowered (`g.lookup`).
	readParam
	// readModule: a `once` or a function, nothing a body binds.
	readModule
	// readRecorded: the instance of a generic nested fn the checker's
	// record names, found from the record and not by the name.
	readRecorded
)

// localReadClass is a way a read's binding, found by its name, and the
// declaration the checker resolved the read to can differ with no
// difference in what the program does.
type localReadClass string

const (
	localReadCallSiteDefault       localReadClass = "a callee's default lowered at the call site"
	localReadRefinementLiteral     localReadClass = "a refinement rule's literal"
	localReadDestructuredParameter localReadClass = "a destructured parameter's position"
	localReadNestedSelf            localReadClass = "a nested function's own name"
)

// knownLocalReadClasses is every localReadClass verifyLocalRead lets pass,
// with its cause. verifyLocalRead fails on a difference of no listed class;
// TestIRGolden_RecordsMatchTheVM counts each class's distinct sites, and
// maxLocalReadSites holds each count to a maximum that only goes down.
var knownLocalReadClasses = map[localReadClass]string{
	// callDefaultsThrough binds the callee's parameter names to the
	// caller's argument temporaries, which the caller made or declared,
	// and the checker resolves the default's reads to the callee's
	// parameters.
	localReadCallSiteDefault: "the callee's parameters are bound to the caller's arguments",
	// refinement.go binds the value a refinement rule tests
	// (analysis.RefinementLiteralName) to the literal it checks, at a
	// synthesized line, with no Bind and no debug variable.
	localReadRefinementLiteral: "the builder binds the checked literal with no declaration of its own",
	// A lambda's destructured parameter's debug variable stands at the
	// destructuring pattern, and the checker's symbol at the name in it.
	localReadDestructuredParameter: "the debug variable stands at the pattern, not the name",
	// A recursive nested fn reads itself through the closure's own capture
	// parameter (irRecursiveFn), which declares nothing; the checker
	// resolves the read to the `fn`.
	localReadNestedSelf: "the closure's own capture declares nothing",
}

// verifyLocalRead fails, under substVerify, when the binding identRead
// found t's value in by its name (route, v) is not the declaration the
// checker resolved t to (identRef), keyed by the declaring position in the
// file being lowered, and the difference is no known localReadClass. A
// pointer would not do: the checker records per-site copies of a symbol
// (a generic call's instantiation, an interface call's instance), whose
// declaring position is the declaration's. An identifier the builder wrote
// has no record to compare (identRef) and is not checked.
func (bl *irScalarBuilder) verifyLocalRead(t *ast.Ident, v ir.Temp, route readRoute) {
	g := bl.g
	if !substVerify || route == readRecorded || g.unchecked[t] || g.srcFA() == nil {
		return
	}
	sym := g.identRef(t)
	if sym == nil {
		g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("the read of `%s` has no reference: an identifier the builder writes goes through madeIdent", t.Name))
		return
	}
	checkerLocal := bodyBinding(g.srcFA(), sym)
	var builderLocal bool
	var line, col int
	switch route {
	case readCaptured:
		if c, held := bl.captured[t.Name]; !held || c.temp != v {
			// A capture holding nothing to trace (a Unit the closure
			// makes afresh): the enclosing body's read of t, which
			// capture lowered, was held.
			return
		}
		line, col, builderLocal = bl.bindingDecl(t.Name, v)
	case readBound:
		line, col, builderLocal = bl.bindingDecl(t.Name, v)
	case readParam:
		line, col = bl.paramDecl(t.Name)
		builderLocal = true
	}
	if builderLocal == checkerLocal && (!builderLocal || declaredAt(sym, line, col)) {
		return
	}
	class, identified := bl.localReadClassOf(t, sym, builderLocal, checkerLocal)
	if _, listed := knownLocalReadClasses[class]; !identified || !listed {
		g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("the builder reads `%s` from %s, and the checker resolves it to %s",
			t.Name, describeBinding(builderLocal, line, col), describeRef(sym, checkerLocal)))
		return
	}
	localReadCensus.note(g, string(class), t.Line, t.Col)
}

// declaredAt reports whether (line, col), where the builder's binding is
// declared, is sym's declaration: its name's position, or its declaring
// node's (a binding statement, a pattern), or, for a destructured
// parameter, the function or lambda that destructures it.
func declaredAt(sym *analysis.Symbol, line, col int) bool {
	if line == sym.Pos.Line && col == sym.Pos.Col {
		return true
	}
	for _, n := range []ast.Node{sym.Node, sym.ParamOf} {
		if n == nil {
			continue
		}
		if l, c := nodePos(n); l == line && c == col {
			return true
		}
	}
	return false
}

// localReadClassOf is the class of a read whose binding and reference
// differ, identified by what the builder is lowering, and false for none.
func (bl *irScalarBuilder) localReadClassOf(t *ast.Ident, sym *analysis.Symbol, builderLocal, checkerLocal bool) (localReadClass, bool) {
	fd, isFunc := sym.Node.(*ast.FuncDef)
	switch {
	case bl.defaultScope && checkerLocal:
		return localReadCallSiteDefault, true
	case t.Name == analysis.RefinementLiteralName && checkerLocal:
		return localReadRefinementLiteral, true
	case builderLocal && checkerLocal && sym.ParamOf != nil:
		return localReadDestructuredParameter, true
	case builderLocal && checkerLocal && isFunc && fd.Name == t.Name && bl.readsOwnClosure(t.Name):
		return localReadNestedSelf, true
	}
	return "", false
}

// readsOwnClosure reports whether name is the nested function this body, or
// one enclosing it, is the body of, read through the closure's own capture.
func (bl *irScalarBuilder) readsOwnClosure(name string) bool {
	for b := bl; b != nil; b = b.parent {
		if b.nestedFn == name || b.recursive != nil && b.recursive.name == name {
			return true
		}
	}
	return false
}

// bindingDecl is where the binding of name that bl holds in v is declared,
// by the builder's own records, and whether it is a binding of a body at
// all: the debug variable naming v, the capture v is (the declaration the
// read that made it resolved to, which the enclosing body held to its
// binding; one an identifier the builder wrote made is followed into the
// enclosing body), the Bind that wrote v, or the parameter v is. Any other
// value (a function's, a `once`'s, an argument's) is none.
func (bl *irScalarBuilder) bindingDecl(name string, v ir.Temp) (line, col int, local bool) {
	vars := bl.f.Vars()
	for i := len(vars) - 1; i >= 0; i-- {
		if vars[i].Name == name && vars[i].Temp == v && vars[i].Decl.Line() != 0 {
			return vars[i].Decl.Line(), vars[i].Decl.Col(), true
		}
	}
	if c, ok := bl.captured[name]; ok && c.temp == v && bl.parent != nil {
		if c.ref != nil {
			// The read that made the capture was held to the enclosing
			// body's binding where the enclosing builder lowered it, and
			// what it reads there may be no binding's own value: an
			// arm's narrowing of it, a callee's parameter a default reads
			// as the caller's argument.
			return c.ref.Pos.Line, c.ref.Pos.Col, bodyBinding(bl.g.srcFA(), c.ref)
		}
		return bl.parent.bindingDecl(name, c.src)
	}
	switch d := bl.f.Def(v).(type) {
	case *ir.Bind:
		return d.Pos().Line(), d.Pos().Col(), true
	case *ir.Ref:
		if d.Kind() == ir.RefLocal {
			line, col := bl.paramDecl(name)
			return line, col, true
		}
	case nil:
		line, col := bl.paramDecl(name)
		return line, col, true
	}
	return 0, 0, false
}

// paramDecl is where the parameter name of the lambda or function bl
// lowers is declared, or (0, 0).
func (bl *irScalarBuilder) paramDecl(name string) (int, int) {
	var params []ast.Param
	if lam, isLambda := bl.root.(*ast.Lambda); isLambda {
		params = lam.Params
	} else if fd, isFunc := bl.sh.at.(*ast.FuncDef); isFunc {
		params = fd.Params
	}
	for _, p := range params {
		if p.Name == name {
			return p.Line, p.Col
		}
	}
	return 0, 0
}

func describeBinding(local bool, line, col int) string {
	if !local {
		return "no binding of a body"
	}
	return fmt.Sprintf("a binding declared at %d:%d", line, col)
}

func describeRef(sym *analysis.Symbol, local bool) string {
	if !local {
		return fmt.Sprintf("a declaration that is no body's binding (symbol kind %d at %d:%d)", sym.Kind, sym.Pos.Line, sym.Pos.Col)
	}
	return fmt.Sprintf("the binding declared at %d:%d", sym.Pos.Line, sym.Pos.Col)
}
