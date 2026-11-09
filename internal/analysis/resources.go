package analysis

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

// The release rules (spec §9 *Releasing resources*). A value of a `Release`
// type that a body binds from an acquisition, a call whose result is a
// `Result` holding it, is the body's to give back: a `defer` among the
// statements after the binding, in the binding's own block, makes a call
// that takes it, or the body hands it on by returning it, storing it in a
// literal or a constructor, or binding another name to it. A value with no
// `defer` is handed on or released on every path that returns normally. A
// `Commit` type's
// usual release discards its work, so a body that defers that release
// calls the type's commit on every path that returns normally.
//
// Both rules read one body. A function, a lambda and a test body are each
// one; a call that takes the value is a use, whatever the callee does with
// it.

// UnreleasedResourceCode tags the error at a binding whose resource is never
// released or handed on.
const UnreleasedResourceCode = "unreleased-resource"

// UncommittedResourceCode tags the error at a normal return that leaves a
// Commit value to its deferred discard.
const UncommittedResourceCode = "uncommitted-resource"

// DiscardedResourceCode tags the error at an acquisition whose value is
// dropped: bound to `_`, or the value of a body that nothing takes.
const DiscardedResourceCode = "discarded-resource"

// EscapedReleaseCode tags the error at a `defer` that releases a value which
// then leaves the `defer`'s block.
const EscapedReleaseCode = "released-value-escapes"

// ErrorExitLeakCode tags the error at a `?` or an error `return` that leaves
// a resource unreleased: acquired before it, handed on or released only
// after it.
const ErrorExitLeakCode = "leaks-on-error"

// ReturnLeakCode tags the error at a normal exit that neither hands on nor
// releases a resource that another path hands on.
const ReturnLeakCode = "leaks-on-return"

// DeferOnErrorNoFailureCode tags the error at a `defer on error` in a
// function whose result carries no failure.
const DeferOnErrorNoFailureCode = "defer-on-error-cannot-fail"

// isResourceImpl reports whether n is an `impl Release` or `impl Commit`,
// whose functions' bodies name the type's usual release and commit.
func isResourceImpl(n *ast.ImplBlock) bool {
	if n == nil || n.Interface == nil {
		return false
	}
	switch TypeExprBaseName(n.Interface) {
	case "Release", "Commit":
		return true
	}
	return false
}

// resourceKind is what the rules need of a Release type: the call its
// `release` makes, written as a caller writes it (`Writer.discard`), and,
// for a Commit type, the call its `commit` makes (`Writer.close`). The
// names are each call's own function name, which a deferred call or a
// commit is recognized by.
type resourceKind struct {
	release   string
	releaseFn string
	commit    string
	commitFn  string
}

// resourceKindOf is ty's resourceKind, or nil when ty implements no
// Release.
func (c *checker) resourceKindOf(ty Type) *resourceKind {
	ty = resolveTypeVar(ty)
	if ty == nil || c.fa == nil {
		return nil
	}
	name := concreteTypeName(ty)
	if it, ok := ty.(*InterfaceType); ok {
		name = it.Name
	}
	if name == "" || !c.fa.HasImpl(name, "Release") {
		return nil
	}
	origin, _ := nominalOrigin(ty)
	key := origin + "\x00" + name
	if k, ok := c.resourceKinds[key]; ok {
		return k
	}
	if c.resourceKinds == nil {
		c.resourceKinds = map[string]*resourceKind{}
	}
	// The impls are known by the receiver's name, so the one found must be
	// for this declaration: std/http_host's `Conn` is not std/sql's.
	release := c.resourceImplFunc(name, origin, "Release", "release")
	if release == nil {
		c.resourceKinds[key] = nil
		return nil
	}
	k := &resourceKind{}
	k.release, k.releaseFn = c.resourceCall(release, name, origin, "release")
	if commit := c.resourceImplFunc(name, origin, "Commit", "commit"); commit != nil {
		k.commit, k.commitFn = c.resourceCall(commit, name, origin, "commit")
	}
	c.resourceKinds[key] = k
	return k
}

// resourceCall reads the call fd, a Release or Commit impl's function
// method for the type named name, makes: the body of `fn release(w: Writer)
// { Writer.discard(w) }` gives `Writer.discard` and `discard`. A function
// called by its bare name is qualified by its module, as a caller outside
// it writes it (`sql.close_quietly`). A body that is not one call gives the
// interface function itself (`Writer.release`).
func (c *checker) resourceCall(fd *ast.FuncDef, name, origin, method string) (string, string) {
	fallback := typeNameHead(name) + "." + method
	if fd.Body == nil || len(fd.Body.Stmts) != 1 {
		return fallback, method
	}
	stmt := fd.Body.Stmts[0]
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	call, ok := stmt.(*ast.Call)
	if !ok {
		return fallback, method
	}
	switch f := call.Func.(type) {
	case *ast.FieldAccess:
		if q := qualifierText(f.Object); q != "" && f.Field != nil {
			return q + "." + f.Field.Name, f.Field.Name
		}
	case *ast.Ident:
		if origin == "" || origin == OriginUnresolved || origin == OriginEntry || origin == declaredOrigin(c.fa) {
			return f.Name, f.Name
		}
		return strings.TrimSuffix(path.Base(origin), ".nomi") + "." + f.Name, f.Name
	}
	return fallback, method
}

// resourceImplFunc is the function method of the impl of iface for the type
// named name declared at origin, or nil when it has none.
func (c *checker) resourceImplFunc(name, origin, iface, method string) *ast.FuncDef {
	// The file's own impl, for a type it declares.
	if origin == "" || origin == OriginUnresolved || origin == declaredOrigin(c.fa) {
		if sym := c.fa.LookupTypeMethod(name, method); sym != nil && sym.ImplInterface == iface {
			if fd, ok := sym.Node.(*ast.FuncDef); ok {
				return fd
			}
		}
	}
	p := c.fa.Program()
	idx := p.ImplIndex
	if idx == nil {
		return nil
	}
	want := name
	if origin != "" && origin != OriginUnresolved && origin != OriginEntry && origin != p.entryOrigin() {
		want = origin + receiverOriginSep + name
	}
	for _, fn := range idx.IfaceMethodImpls[iface][method] {
		if fn == nil || idx.ReceiverOf(fn) != name || IsSynthesizedLine(fn.Line) {
			continue
		}
		if q := idx.QualifiedReceiver[fn]; q != "" && q != want {
			continue
		}
		return fn
	}
	return nil
}

// qualifierText is the text of a call's qualifier: `Writer`, `io.Writer`.
func qualifierText(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.TypeIdent:
		return v.Name
	case *ast.FieldAccess:
		if q := qualifierText(v.Object); q != "" && v.Field != nil {
			return q + "." + v.Field.Name
		}
	}
	return ""
}

// releaseBody is one body the rules read: a function's, a lambda's or a
// test's.
type releaseBody struct {
	block *ast.Block
	// tails holds the blocks whose value is the body's result: the body,
	// and the branches of an `if`, `case` or block that ends one.
	tails map[*ast.Block]bool
	// propagates says a `?` can leave the body, so a commit is written
	// `Writer.close(w)?`; in a body whose result is neither a Result nor a
	// Maybe it is written with `!`.
	propagates bool
	// dropped holds the blocks whose value nothing takes: a statement's,
	// and the body's when its result is Unit or it is a test's.
	dropped map[*ast.Block]bool
	// names are the names the body writes, which a fix's new name avoids.
	names map[string]bool
	// boot says the body is a top-level `fn boot`, an entry's boot (one
	// anywhere else is an error of its own): a `defer` written directly in
	// it runs after `main` finishes (spec §15), so the value it releases
	// may leave in the App.
	boot bool
}

// acquisition is a name a body binds to a value of a Release type from an
// acquisition.
type acquisition struct {
	sym       *Symbol
	name      string
	line, col int
	kind      *resourceKind
	// block is the block the name is in scope in, and index the binding's
	// statement in it; -1 when the name is bound by a `case` arm or an
	// `if` pattern, whose block it is in scope from the start of. block is
	// nil for an arm whose body is an expression, and expr is that body.
	block *ast.Block
	index int
	expr  ast.Node
	// stmt is the binding statement, nil for a pattern of an arm or `if`.
	stmt ast.Node
}

// checkReleases applies the release rules to body, a function's or a
// test's, whose result is result, and to each lambda in it. dropped says
// nothing takes the body's value, as for a test or a Unit callback; a body
// whose result is Unit drops it too. boot says body is a top-level
// `fn boot`'s.
func (c *checker) checkReleases(body *ast.Block, result Type, test, dropped, boot bool) {
	if body == nil || c.fa == nil {
		return
	}
	rb := &releaseBody{block: body, tails: map[*ast.Block]bool{}, propagates: test || propagatesInto(result), boot: boot}
	markTailBlocks(body, rb.tails)
	rb.markDropped(dropped || result == nil || IsUnitType(resolveTypeVar(result)))
	var acqs []*acquisition
	c.collectAcquisitions(body, rb, &acqs)
	for _, a := range acqs {
		c.checkAcquisition(rb, a)
	}
	c.checkDeferredEscapes(rb)
}

// propagatesInto reports whether a `?` can leave a body whose result is t.
func propagatesInto(t Type) bool {
	et, ok := resolveTypeVar(t).(*EnumType)
	return ok && (et.Name == "Result" || et.Name == "Maybe")
}

// markTailBlocks marks b and every block whose value is b's.
func markTailBlocks(b *ast.Block, tails map[*ast.Block]bool) {
	if b == nil {
		return
	}
	tails[b] = true
	markTailValue(tailExpr(b), tails)
}

func markTailValue(n ast.Node, tails map[*ast.Block]bool) {
	switch v := n.(type) {
	case *ast.Block:
		markTailBlocks(v, tails)
	case *ast.GroupedExpr:
		markTailValue(v.Expr, tails)
	case *ast.If:
		markTailBlocks(v.Then, tails)
		markTailValue(v.Else, tails)
	case *ast.Case:
		for _, br := range v.Branches {
			markTailValue(br.Body, tails)
		}
	}
}

// markDropped fills rb.dropped: the body itself when its value is dropped,
// and each block that is the value of a statement whose value is.
func (rb *releaseBody) markDropped(body bool) {
	rb.dropped = map[*ast.Block]bool{}
	rb.names = map[string]bool{}
	if body {
		markTailBlocks(rb.block, rb.dropped)
	}
	ast.Inspect(rb.block, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Lambda, *ast.FuncDef:
			return false
		case *ast.Ident:
			rb.names[v.Name] = true
		case *ast.Binding:
			rb.names[v.Name] = true
		case *ast.Block:
			for i, s := range v.Stmts {
				if isValueStmt(s) && (i < len(v.Stmts)-1 || rb.dropped[v]) {
					if es, ok := s.(*ast.ExprStmt); ok {
						s = es.Expr
					}
					markTailValue(s, rb.dropped)
				}
			}
		}
		return true
	})
}

// collectAcquisitions finds the acquisitions under n that belong to rb. A
// lambda is a body of its own, checked when it is met; a nested function
// is checked as a function.
func (c *checker) collectAcquisitions(n ast.Node, rb *releaseBody, acqs *[]*acquisition) {
	var visit func(ast.Node)
	visit = func(n ast.Node) {
		switch v := n.(type) {
		case nil:
			return
		case *ast.FuncDef:
			return
		case *ast.Lambda:
			var result Type
			if ft, ok := resolveTypeVar(c.fa.ExprTypes[v]).(*FuncType); ok {
				result = ft.Return
			}
			if v.Body != nil {
				// A lambda written as one expression has a body block the
				// parser made, with no braces to insert a `defer` in.
				c.checkReleases(v.Body, result, false, c.unitCallbacks[v], false)
			}
			return
		case *ast.Block:
			for i, s := range v.Stmts {
				c.bindingAcquisitions(v, i, s, acqs)
				c.checkDiscarded(rb, v, i, s)
				visit(s)
			}
			return
		case *ast.Case:
			visit(v.Value)
			acquired := v.Value != nil && c.isAcquisition(v.Value)
			for _, br := range v.Branches {
				if acquired {
					block, _ := br.Body.(*ast.Block)
					for _, a := range c.patternAcquisitions(br.Pattern) {
						a.block, a.index = block, -1
						if block == nil {
							a.expr = br.Body
						}
						*acqs = append(*acqs, a)
					}
				}
				visit(br.Guard)
				visit(br.Body)
			}
			return
		case *ast.If:
			if v.CondPattern != nil && c.isAcquisition(v.Cond) {
				for _, a := range c.patternAcquisitions(v.CondPattern) {
					a.block, a.index = v.Then, -1
					*acqs = append(*acqs, a)
				}
			}
		}
		ast.Children(n, visit)
	}
	visit(n)
}

// bindingAcquisitions records the acquisitions statement s, the i-th of
// block b, binds.
func (c *checker) bindingAcquisitions(b *ast.Block, i int, s ast.Node, acqs *[]*acquisition) {
	switch v := s.(type) {
	case *ast.Binding:
		if ast.IsDiscardName(v.Name) || IsSynthesizedLine(v.Line) {
			return
		}
		sym := c.fa.Definitions[Pos{Line: v.Line, Col: v.Col}]
		if sym == nil {
			return
		}
		kind := c.resourceKindOf(sym.Type)
		if kind == nil || !c.isAcquisition(v.Value) {
			return
		}
		*acqs = append(*acqs, &acquisition{
			sym: sym, name: v.Name, line: v.Line, col: v.Col, kind: kind,
			block: b, index: i, stmt: v,
		})
	case *ast.PatternBinding:
		if !c.isAcquisition(v.Value) {
			return
		}
		for _, a := range c.patternAcquisitions(v.Pattern) {
			a.block, a.index, a.stmt = b, i, v
			*acqs = append(*acqs, a)
		}
	}
}

// patternAcquisitions are the names pattern binds to values of a Release
// type.
func (c *checker) patternAcquisitions(pattern ast.Node) []*acquisition {
	var out []*acquisition
	add := func(name string, line, col int) {
		if ast.IsDiscardName(name) || IsSynthesizedLine(line) {
			return
		}
		sym := c.fa.Definitions[Pos{Line: line, Col: col}]
		if sym == nil {
			return
		}
		if kind := c.resourceKindOf(sym.Type); kind != nil {
			out = append(out, &acquisition{sym: sym, name: name, line: line, col: col, kind: kind})
		}
	}
	ast.Inspect(pattern, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IdentPattern:
			add(v.Name, v.Line, v.Col)
		case *ast.EnumPattern:
			if v.Binding != "" {
				add(v.Binding, v.Line, v.BindingCol)
			}
		case *ast.AsPattern:
			add(v.Name, v.NameLine, v.NameCol)
		}
		return true
	})
	return out
}

// isAcquisition reports whether e acquires: after its `?`, `!` and
// parentheses, a call (or a pipe into one) whose result is a value of a
// Release type, or a Result holding one, that names no value of a Release
// type outside a lambda. A call given one returns a view of it that the
// argument's release covers: a transaction's `Conn` from
// `Conn.begin(conn)`. A `Reader` or `Writer` returned outside a Result is
// no acquisition (isStreamType), nor is a constructor's value.
func (c *checker) isAcquisition(e ast.Node) bool {
	_, ok := c.acquired(e)
	return ok
}

// acquired is the Release type the acquisition e acquires a value of.
func (c *checker) acquired(e ast.Node) (Type, bool) {
	e = stripResultOps(e)
	switch v := e.(type) {
	case *ast.Call:
		if isConstructorCall(v) {
			return nil, false
		}
	case *ast.Binary:
		if v.Op != "|>" {
			return nil, false
		}
	default:
		return nil, false
	}
	held := resolveTypeVar(c.fa.ExprTypes[e])
	if et, ok := held.(*EnumType); ok && et.Name == "Result" {
		if len(et.TypeArgs) == 0 {
			return nil, false
		}
		held = resolveTypeVar(et.TypeArgs[0])
	} else if isStreamType(held) {
		return nil, false
	}
	if c.resourceKindOf(held) == nil {
		return nil, false
	}
	borrowed := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Lambda:
			return false
		case *ast.Ident:
			borrowed = c.resourceKindOf(c.fa.ExprTypes[v]) != nil
		case *ast.FieldAccess:
			borrowed = c.resourceKindOf(c.fa.ExprTypes[v]) != nil
		}
		return !borrowed
	})
	if borrowed {
		return nil, false
	}
	return held, true
}

// isStreamType reports whether t is std/io's Reader or Writer, which std
// makes over memory and functions (`Reader.from_text`) and borrows from
// values that are no Release type (`Body.reader`, `Connection.reader`).
// Only a Result holding one, an opening that can fail, acquires it.
func isStreamType(t Type) bool {
	p, ok := t.(*PrimitiveType)
	if !ok || (p.Name_ != "Reader" && p.Name_ != "Writer") {
		return false
	}
	return p.Origin == "std/io"
}

// stripResultOps takes the `?`, `!` and parentheses off e.
func stripResultOps(e ast.Node) ast.Node {
	for {
		switch v := e.(type) {
		case *ast.GroupedExpr:
			e = v.Expr
		case *ast.TryOp:
			if v.Expr == nil {
				return e
			}
			e = v.Expr
		case *ast.Unwrap:
			if v.Expr == nil {
				return e
			}
			e = v.Expr
		case *ast.Binary:
			// `x |> f()?` is the pipe `x |> f()` and then a `?` stage.
			switch r := v.Right.(type) {
			case *ast.TryOp:
				if r.Expr == nil && v.Op == "|>" {
					e = v.Left
					continue
				}
			case *ast.Unwrap:
				if r.Expr == nil && v.Op == "|>" {
					e = v.Left
					continue
				}
			}
			return e
		default:
			return e
		}
	}
}

// refersTo reports whether n, without its parentheses, is a reference to
// sym.
func (c *checker) refersTo(n ast.Node, sym *Symbol) bool {
	for {
		g, ok := n.(*ast.GroupedExpr)
		if !ok {
			break
		}
		n = g.Expr
	}
	id, ok := n.(*ast.Ident)
	if !ok {
		return false
	}
	for s := c.fa.References[Pos{Line: id.Line, Col: id.Col}]; s != nil; s = s.Resolved {
		if s == sym {
			return true
		}
	}
	return false
}

// callTakes reports whether call takes sym as an argument.
func (c *checker) callTakes(call *ast.Call, sym *Symbol) bool {
	for _, arg := range call.Args {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if c.refersTo(arg, sym) {
			return true
		}
	}
	return false
}

// calleeName is the name of the function call calls: `discard` for
// `Writer.discard(w)`, `Err` for `.Err(e)`.
func calleeName(call *ast.Call) string {
	switch f := call.Func.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.TypeIdent:
		return f.Name
	case *ast.FieldAccess:
		if f.Field != nil {
			return f.Field.Name
		}
	case *ast.DotVariant:
		return f.Name
	}
	return ""
}

// isConstructorCall reports whether call builds a value of a type: a
// variant (`Ok(w)`, `Body.Stream(r)`, `.Some(c)`) or a distinct type
// (`Handle(w)`), whose names are capitalized.
func isConstructorCall(call *ast.Call) bool {
	if _, ok := call.Func.(*ast.DotVariant); ok {
		return true
	}
	name := calleeName(call)
	r, _ := utf8.DecodeRuneInString(name)
	return name != "" && unicode.IsUpper(r)
}

// scopeNodes are the statements a's name is in scope over.
func (a *acquisition) scopeNodes() []ast.Node {
	switch {
	case a.block != nil:
		return a.block.Stmts[a.index+1:]
	case a.expr != nil:
		return []ast.Node{a.expr}
	}
	return nil
}

// checkAcquisition applies the rules to a. A `defer` releases a on every
// exit; a `defer on error` only on an error exit, so a value it covers must
// also be handed on.
func (c *checker) checkAcquisition(rb *releaseBody, a *acquisition) {
	deferAt, deferFn, errDeferAt := -1, "", -1
	if a.block != nil {
		for i := a.index + 1; i < len(a.block.Stmts); i++ {
			d, ok := a.block.Stmts[i].(*ast.Defer)
			if !ok {
				continue
			}
			call, ok := d.Call.(*ast.Call)
			if !ok || !c.callTakes(call, a.sym) {
				continue
			}
			if d.OnError {
				if errDeferAt < 0 {
					errDeferAt = i
				}
				continue
			}
			deferAt, deferFn = i, calleeName(call)
			break
		}
	}
	if deferAt < 0 {
		if !c.handedOn(a) {
			if errDeferAt >= 0 {
				c.report(TypeError{
					Line: a.line,
					Col:  a.col,
					Message: fmt.Sprintf("`%s` is released only if the body fails; write `defer %s(%s)` after it, or return it",
						a.name, a.kind.release, a.name),
					Code: UnreleasedResourceCode,
				})
				return
			}
			c.reportUnreleased(rb, a)
			return
		}
		if a.block != nil {
			f := &pathFlow{c: c, a: a, rb: rb, handOn: true}
			f.run(a.block, a.index+1)
			for _, m := range f.misses {
				c.report(f.leakError(m))
			}
		}
		c.checkErrorExits(rb, a, errDeferAt, false)
		return
	}
	c.checkErrorExits(rb, a, deferAt, true)
	if a.kind.commit == "" || (deferFn != a.kind.releaseFn && deferFn != "release") {
		// A deferred release that is not the discarding one (a writer's
		// `close_quietly`) commits as it releases.
		return
	}
	f := &pathFlow{c: c, a: a, rb: rb}
	f.run(a.block, deferAt+1)
	for _, m := range f.misses {
		c.report(f.missError(m))
	}
}

// checkErrorExits reports the first error exit, a `?` or a `return` of
// `Err` or `None`, that leaves the body between a's binding and the
// statement releaseAt that defers its release (-1 for none), or the
// statement that hands it on, whichever comes first. plain says the release
// at releaseAt is a `defer`, not a `defer on error`.
func (c *checker) checkErrorExits(rb *releaseBody, a *acquisition, releaseAt int, plain bool) {
	if !rb.propagates {
		return
	}
	nodes := a.scopeNodes()
	if releaseAt >= 0 && a.block != nil {
		nodes = a.block.Stmts[a.index+1 : releaseAt]
	}
	for i, n := range nodes {
		if c.handsOnIn(n, a.sym) || (i == len(nodes)-1 && releaseAt < 0 && c.yields(n, a.sym)) {
			return
		}
		line, col, what := firstErrorExit(n)
		if line == 0 {
			continue
		}
		e := TypeError{Line: line, Col: col, Code: ErrorExitLeakCode}
		release := fmt.Sprintf("defer on error %s(%s)", a.kind.release, a.name)
		switch {
		case releaseAt >= 0 && plain:
			e.Message = fmt.Sprintf("`%s` leaks if %s: its `defer` comes after it; move the `defer` up to right after acquiring `%s`",
				a.name, what, a.name)
		case releaseAt >= 0:
			e.Message = fmt.Sprintf("`%s` leaks if %s: its `defer on error` comes after it; move it up to right after acquiring `%s`",
				a.name, what, a.name)
		default:
			e.Message = fmt.Sprintf("`%s` leaks if %s: it isn't released on that path; add `%s` after acquiring it",
				a.name, what, release)
			if a.block != nil {
				if edit, ok := deferEdit(a, release); ok {
					e.Fix = &parser.Fix{Title: fmt.Sprintf("Release `%s` on error", a.name), Edits: []parser.Edit{edit}}
				}
			}
		}
		c.report(e)
		return
	}
}

// firstErrorExit is the position of the first `?` or `return` of `Err` or
// `None` in n outside a lambda or a nested function, and what it does as
// the leak message words it.
func firstErrorExit(n ast.Node) (int, int, string) {
	line, col, what := 0, 0, ""
	ast.Inspect(n, func(m ast.Node) bool {
		if line != 0 {
			return false
		}
		switch v := m.(type) {
		case *ast.Lambda, *ast.FuncDef:
			return false
		case *ast.TryOp:
			line, col, what = v.Line, v.Col, "this `?` returns an error"
		case *ast.Return:
			if isFailureValue(v) {
				line, col, what = v.Line, v.Col, "this `return` runs"
			}
		}
		return line == 0
	})
	return line, col, what
}

// handedOn reports whether a's value leaves the body some other way than
// through a release: returned, the value of its block, stored in a literal
// or a constructor, or bound to another name.
func (c *checker) handedOn(a *acquisition) bool {
	nodes := a.scopeNodes()
	if len(nodes) > 0 && c.yields(nodes[len(nodes)-1], a.sym) {
		return true
	}
	for _, n := range nodes {
		if c.handsOnIn(n, a.sym) {
			return true
		}
	}
	return false
}

// handsOnIn reports whether n hands sym's value on: returns it, binds
// another name to it, or stores it in a literal or a constructor.
func (c *checker) handsOnIn(n ast.Node, sym *Symbol) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if found {
			return false
		}
		if r, ok := m.(*ast.Return); ok {
			found = c.yields(r.Value, sym)
		} else {
			found = c.handsOnAt(m, sym)
		}
		return !found
	})
	return found
}

// handsOnAt reports whether m itself, not what it holds, hands sym's value
// on: binds another name to it, or stores it in a literal or a
// constructor.
func (c *checker) handsOnAt(m ast.Node, sym *Symbol) bool {
	switch v := m.(type) {
	case *ast.Binding:
		return c.refersTo(v.Value, sym)
	case *ast.Call:
		return isConstructorCall(v) && c.callTakes(v, sym)
	case *ast.ListLit:
		return c.anyRefers(v.Items, sym)
	case *ast.VectorLit:
		return c.anyRefers(v.Items, sym)
	case *ast.SetLit:
		return c.anyRefers(v.Items, sym)
	case *ast.ListSpreadLit:
		return c.anyRefers(v.Items, sym)
	case *ast.TupleLit:
		return c.anyRefers(v.Items, sym)
	case *ast.MapLit:
		for _, e := range v.Entries {
			if c.refersTo(e.Key, sym) || c.refersTo(e.Value, sym) {
				return true
			}
		}
	case *ast.StructLit:
		for _, f := range v.Fields {
			if c.refersTo(f.Value, sym) {
				return true
			}
		}
	}
	return false
}

func (c *checker) anyRefers(items []ast.Node, sym *Symbol) bool {
	for _, it := range items {
		if c.refersTo(it, sym) {
			return true
		}
	}
	return false
}

// yields reports whether n's value is sym's: sym itself, or the value of
// a block, `if` or `case` that ends in it.
func (c *checker) yields(n ast.Node, sym *Symbol) bool {
	switch v := n.(type) {
	case *ast.ExprStmt:
		return c.yields(v.Expr, sym)
	case *ast.GroupedExpr:
		return c.yields(v.Expr, sym)
	case *ast.Block:
		return v != nil && c.yields(tailExpr(v), sym)
	case *ast.If:
		return c.yields(v.Then, sym) || c.yields(v.Else, sym)
	case *ast.Case:
		for _, br := range v.Branches {
			if c.yields(br.Body, sym) {
				return true
			}
		}
		return false
	}
	return c.refersTo(n, sym)
}

// checkDiscarded reports s, the i-th statement of b, when it acquires a
// value and drops it: `_ = fs.open(p)?`, or an acquiring call whose value
// is a statement's that nothing takes.
func (c *checker) checkDiscarded(rb *releaseBody, b *ast.Block, i int, s ast.Node) {
	var value ast.Node
	line, col := 0, 0
	bound := false
	switch v := s.(type) {
	case *ast.Binding:
		if !ast.IsDiscardName(v.Name) || IsSynthesizedLine(v.Line) {
			return
		}
		value, line, col, bound = v.Value, v.Line, v.Col, true
	default:
		if !isValueStmt(s) || (i == len(b.Stmts)-1 && !rb.dropped[b]) {
			return
		}
		value = s
		if es, ok := s.(*ast.ExprStmt); ok {
			value = es.Expr
		}
		if span := stmtSpan(s); !span.IsZero() {
			line, col = span.StartLine, span.StartCol
		} else {
			line, col = stmtStart(s)
		}
	}
	held, ok := c.acquired(value)
	if !ok || line == 0 {
		return
	}
	kind := c.resourceKindOf(held)
	typeName := typeNameHead(resourceTypeName(held))
	name := rb.freshName(resourceVarName(typeName))
	e := TypeError{
		Line: line,
		Col:  col,
		Message: fmt.Sprintf("the `%s` this acquires is never released; bind it and write `defer %s(%s)` after it",
			typeName, kind.release, name),
		Code: DiscardedResourceCode,
	}
	// The fix binds the value to the new name, then releases it as the
	// fix for an unreleased binding does.
	a := &acquisition{name: name, kind: kind, block: b, index: i, stmt: s, line: line, col: col}
	var bind parser.Edit
	if bound {
		v := s.(*ast.Binding)
		bind = parser.Edit{Start: parser.Pos{Line: v.Line, Col: v.Col}, End: parser.Pos{Line: v.Line, Col: v.Col + len(v.Name)}, Text: name}
	} else {
		at := parser.Pos{Line: line, Col: col}
		bind = parser.Edit{Start: at, End: at, Text: name + " = "}
	}
	if edit, ok := deferEdit(a, fmt.Sprintf("defer %s(%s)", kind.release, name)); ok {
		fix := &parser.Fix{Title: fmt.Sprintf("Release the `%s`", typeName), Edits: []parser.Edit{bind, edit}}
		if kind.commit != "" {
			f := &pathFlow{c: c, a: a, rb: rb}
			f.run(b, i+1)
			for _, m := range f.misses {
				if edit, ok := f.commitEdit(m); ok {
					fix.Edits = append(fix.Edits, edit)
				}
			}
		}
		e.Fix = fix
	}
	c.report(e)
}

// resourceTypeName is the name a Release type is written by.
func resourceTypeName(t Type) string {
	if it, ok := t.(*InterfaceType); ok {
		return it.Name
	}
	return concreteTypeName(t)
}

// resourceVarName is the name a fix binds a value of the type named
// typeName to: the name in snake case (`reader`, `streamed_response`).
func resourceVarName(typeName string) string {
	if i := strings.LastIndexByte(typeName, '.'); i >= 0 {
		typeName = typeName[i+1:]
	}
	var b strings.Builder
	for i, r := range typeName {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "resource"
	}
	return b.String()
}

// freshName is name, or name with a number after it, when the body
// already writes name.
func (rb *releaseBody) freshName(name string) string {
	if !rb.names[name] {
		return name
	}
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s%d", name, n); !rb.names[c] {
			return c
		}
	}
}

// reportUnreleased reports a, with the fix that defers its release after
// the binding and, for a Commit type, commits before each normal return.
func (c *checker) reportUnreleased(rb *releaseBody, a *acquisition) {
	e := TypeError{
		Line: a.line,
		Col:  a.col,
		Message: fmt.Sprintf("`%s` is never released; write `defer %s(%s)` after it, or return it",
			a.name, a.kind.release, a.name),
		Code: UnreleasedResourceCode,
	}
	if a.block != nil {
		if edit, ok := deferEdit(a, fmt.Sprintf("defer %s(%s)", a.kind.release, a.name)); ok {
			fix := &parser.Fix{Title: fmt.Sprintf("Release `%s`", a.name), Edits: []parser.Edit{edit}}
			if a.kind.commit != "" {
				f := &pathFlow{c: c, a: a, rb: rb}
				f.run(a.block, a.index+1)
				for _, m := range f.misses {
					if edit, ok := f.commitEdit(m); ok {
						fix.Edits = append(fix.Edits, edit)
					}
				}
			}
			e.Fix = fix
		}
	}
	c.report(e)
}

// deferEdit inserts the statement text on its own line after a's binding,
// or at the start of the block a pattern of an arm or `if` binds it for.
func deferEdit(a *acquisition, text string) (parser.Edit, bool) {
	if a.stmt != nil {
		span := stmtSpan(a.stmt)
		if span.IsZero() || span.StartCol < 1 {
			return parser.Edit{}, false
		}
		at := parser.Pos{Line: span.EndLine + 1, Col: 1}
		return parser.Edit{Start: at, End: at, Text: strings.Repeat(" ", span.StartCol-1) + text + "\n"}, true
	}
	if len(a.block.Stmts) == 0 || a.block.EndLine == 0 {
		return parser.Edit{}, false
	}
	first := stmtSpan(a.block.Stmts[0])
	if first.IsZero() || first.StartLine == a.block.Line {
		return parser.Edit{}, false
	}
	at := parser.Pos{Line: first.StartLine, Col: 1}
	return parser.Edit{Start: at, End: at, Text: strings.Repeat(" ", first.StartCol-1) + text + "\n"}, true
}

// stmtSpan is s's extent, through an expression statement.
func stmtSpan(s ast.Node) ast.Span {
	if es, ok := s.(*ast.ExprStmt); ok && es.Expr != nil {
		if hs, ok := es.Expr.(ast.HasSpan); ok {
			return hs.GetSpan()
		}
	}
	if hs, ok := s.(ast.HasSpan); ok {
		return hs.GetSpan()
	}
	return ast.Span{}
}

// pathFlow follows the paths from a statement to every normal return of
// the body, and records each that the path has not settled. For the commit
// rule it starts after a deferred discarding release, and the commit or the
// release settles a path. For the hand-on rule (handOn) it starts after an
// acquisition with no `defer`, and handing the value on or releasing it
// settles a path: a binding, literal or constructor that holds it, a block
// value or return that is it, or a call of its release or commit.
type pathFlow struct {
	c       *checker
	a       *acquisition
	rb      *releaseBody
	handOn  bool
	settled bool
	dead    bool
	misses  []pathMiss
	seen    map[Pos]bool
	// in is the block whose statement the flow is at.
	in *ast.Block
}

// pathMiss is a normal return the path has not settled: stmt is the
// statement it is (a `return`, or a tail expression) when one is, and
// block the block whose closing brace it is when the path ends there. in
// is the block the flow was in.
type pathMiss struct {
	line, col int
	stmt      ast.Node
	block     *ast.Block
	in        *ast.Block
}

type flowState struct{ settled, dead bool }

func (f *pathFlow) state() flowState { return flowState{f.settled, f.dead} }
func (f *pathFlow) set(s flowState)  { f.settled, f.dead = s.settled, s.dead }
func joinFlow(a, b flowState) flowState {
	switch {
	case a.dead:
		return b
	case b.dead:
		return a
	}
	return flowState{settled: a.settled && b.settled}
}

// run follows block's statements from start. The block ending discards the
// value, so its end is a return: the body's when the block's value is the
// body's result.
func (f *pathFlow) run(block *ast.Block, start int) {
	f.stmts(block, block.Stmts[start:], f.rb.tails[block])
}

// stmts follows stmts, the rest of block, to its end. tail says the
// block's value is the body's result, so its last statement returns.
func (f *pathFlow) stmts(block *ast.Block, stmts []ast.Node, tail bool) {
	outer := f.in
	defer func() { f.in = outer }()
	for i, s := range stmts {
		if f.dead {
			return
		}
		f.in = block
		if tail && i == len(stmts)-1 && isValueStmt(s) {
			f.tail(s)
			return
		}
		f.node(s)
		f.blockValue(block, i == len(stmts)-1, s)
	}
	if !f.dead && !f.settled {
		line, col := block.EndLine, block.EndCol
		if line == 0 && len(block.Stmts) > 0 {
			line, col = stmtStart(block.Stmts[len(block.Stmts)-1])
		}
		f.miss(pathMiss{line: line, col: col, block: block})
	}
	f.dead = true
}

// isValueStmt reports whether s is an expression, whose value a block's
// last statement gives.
func isValueStmt(s ast.Node) bool {
	switch s.(type) {
	case *ast.Binding, *ast.PatternBinding, *ast.TupleDestructure, *ast.StructDestructure,
		*ast.DistinctDestructure, *ast.PatternDestructure, *ast.Defer, *ast.With, *ast.Assertion:
		return false
	}
	return true
}

// tail follows e, the body's result.
func (f *pathFlow) tail(e ast.Node) {
	stmt := e
	if es, ok := e.(*ast.ExprStmt); ok {
		e = es.Expr
	}
	switch v := e.(type) {
	case *ast.GroupedExpr:
		f.tail(v.Expr)
		return
	case *ast.Block:
		f.stmts(v, v.Stmts, true)
		return
	case *ast.If:
		f.node(v.Cond)
		before := f.state()
		f.stmts(v.Then, v.Then.Stmts, true)
		then := f.state()
		f.set(before)
		switch els := v.Else.(type) {
		case *ast.Block:
			f.stmts(els, els.Stmts, true)
		case *ast.If:
			f.tail(els)
		case nil:
			// The `if` without `else` ends the body when its condition
			// does not hold.
			f.returnAt(v, nil)
		}
		f.set(joinFlow(then, f.state()))
		return
	case *ast.Case:
		f.node(v.Value)
		before := f.state()
		after := flowState{dead: true}
		for _, br := range v.Branches {
			f.set(before)
			f.node(br.Guard)
			if b, ok := br.Body.(*ast.Block); ok {
				f.stmts(b, b.Stmts, true)
			} else {
				f.tail(br.Body)
			}
			after = joinFlow(after, f.state())
		}
		f.set(after)
		return
	}
	f.node(e)
	if f.dead {
		return
	}
	f.returnAt(e, stmt)
}

// returnAt records the normal return the expression e is unless it is a
// failure or the path has settled, and ends the path. For the hand-on
// rule, a return whose value is the name settles it.
func (f *pathFlow) returnAt(e ast.Node, stmt ast.Node) {
	if f.handOn && f.carries(e) {
		f.settled = true
	}
	if !isFailureValue(e) && !f.settled {
		line, col := stmtStart(e)
		if span := stmtSpan(e); !span.IsZero() {
			line, col = span.StartLine, span.StartCol
		}
		f.miss(pathMiss{line: line, col: col, stmt: stmt, in: f.in})
	}
	f.dead = true
}

// carries reports whether the exit e's value is the name: `r`, `return r`,
// `break r`. A constructor or literal holding it settled the path as the
// flow went through it.
func (f *pathFlow) carries(e ast.Node) bool {
	switch v := e.(type) {
	case *ast.Return:
		return f.c.yields(v.Value, f.a.sym)
	case *ast.Break:
		return f.c.yields(v.Value, f.a.sym)
	case *ast.Continue:
		return false
	}
	return f.c.yields(e, f.a.sym)
}

// blockValue settles the hand-on rule's path when s, the last statement
// of block, is the name and gives the value of a block something takes:
// the `r` of `x = if want { r } else { ... }`.
func (f *pathFlow) blockValue(block *ast.Block, last bool, s ast.Node) {
	if !f.handOn || !last || f.dead || !isValueStmt(s) || f.rb.dropped[block] {
		return
	}
	if es, ok := s.(*ast.ExprStmt); ok {
		s = es.Expr
	}
	if f.c.refersTo(s, f.a.sym) {
		f.settled = true
	}
}

func (f *pathFlow) miss(m pathMiss) {
	if m.line == 0 {
		return
	}
	if f.seen == nil {
		f.seen = map[Pos]bool{}
	}
	p := Pos{Line: m.line, Col: m.col}
	if f.seen[p] {
		return
	}
	f.seen[p] = true
	f.misses = append(f.misses, m)
}

// isFailureValue reports whether e is an `Err(...)` or a `None`, a return
// that leaves by failing.
func isFailureValue(e ast.Node) bool {
	switch v := e.(type) {
	case *ast.Return:
		return v.Value != nil && isFailureValue(v.Value)
	case *ast.ExprStmt:
		return isFailureValue(v.Expr)
	case *ast.GroupedExpr:
		return isFailureValue(v.Expr)
	case *ast.Call:
		return calleeName(v) == "Err"
	case *ast.Ident:
		return v.Name == "None"
	case *ast.TypeIdent:
		return v.Name == "None"
	case *ast.DotVariant:
		return v.Name == "None"
	case *ast.FieldAccess:
		return v.Field != nil && v.Field.Name == "None"
	}
	return false
}

// node follows n, which does not end the body unless it holds a return.
func (f *pathFlow) node(n ast.Node) {
	if f.dead {
		return
	}
	switch v := f.c.fa.AsTodo(n).(type) {
	case nil:
		return
	case *ast.FuncDef, *ast.Defer:
		return
	case *ast.Lambda:
		// A commit or a hand-on in a callback counts: the callback may run
		// it.
		if f.containsCommit(v.Body) || (f.handOn && f.c.handsOnIn(v.Body, f.a.sym)) {
			f.settled = true
		}
		return
	case *ast.Return:
		f.node(v.Value)
		if !f.dead {
			f.returnAt(v, v)
		}
		return
	case *ast.Break, *ast.Continue:
		ast.Children(n, f.node)
		if !f.dead {
			f.returnAt(n, n)
		}
		return
	case *ast.Unreachable, *ast.Todo:
		f.dead = true
		return
	case *ast.If:
		f.node(v.Cond)
		before := f.state()
		f.inner(v.Then)
		then := f.state()
		f.set(before)
		if v.Else != nil {
			f.node(v.Else)
		}
		f.set(joinFlow(then, f.state()))
		return
	case *ast.Case:
		f.node(v.Value)
		before := f.state()
		after := flowState{dead: true}
		for _, br := range v.Branches {
			f.set(before)
			f.node(br.Guard)
			f.node(br.Body)
			after = joinFlow(after, f.state())
		}
		f.set(after)
		return
	case *ast.Block:
		f.inner(v)
		return
	case *ast.PatternBinding:
		f.node(v.Value)
		before := f.state()
		for _, e := range v.ElseNodes() {
			f.node(e)
		}
		// An `else` leaves the block; the binding's path goes on.
		f.set(before)
		return
	case *ast.Call:
		ast.Children(n, f.node)
		if f.isCommit(v) || (f.handOn && f.c.handsOnAt(v, f.a.sym)) {
			f.settled = true
		}
		return
	case *ast.Binary:
		f.node(v.Left)
		f.node(v.Right)
		if f.isCommitPipe(v) {
			f.settled = true
		}
		return
	}
	ast.Children(n, f.node)
	if f.handOn && !f.dead && f.c.handsOnAt(n, f.a.sym) {
		f.settled = true
	}
}

// inner follows a block inside the path, whose end is not a return.
func (f *pathFlow) inner(b *ast.Block) {
	if b == nil {
		return
	}
	outer := f.in
	defer func() { f.in = outer }()
	for i, s := range b.Stmts {
		if f.dead {
			return
		}
		f.in = b
		f.node(s)
		f.blockValue(b, i == len(b.Stmts)-1, s)
	}
}

// isCommit reports whether call settles the value: its commit (the type's
// commit, or `commit` itself), or its release called outright, which
// discards on purpose. Either way the path has decided what to keep.
func (f *pathFlow) isCommit(call *ast.Call) bool {
	return f.settles(calleeName(call)) && f.c.callTakes(call, f.a.sym)
}

func (f *pathFlow) settles(name string) bool {
	k := f.a.kind
	return name == k.commitFn || name == "commit" || name == k.releaseFn || name == "release"
}

// isCommitPipe reports whether b is `w |> Writer.close()`, or a pipe into
// the release.
func (f *pathFlow) isCommitPipe(b *ast.Binary) bool {
	if b.Op != "|>" || !f.c.refersTo(b.Left, f.a.sym) {
		return false
	}
	call, ok := b.Right.(*ast.Call)
	if !ok {
		return false
	}
	return f.settles(calleeName(call))
}

func (f *pathFlow) containsCommit(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		switch v := m.(type) {
		case *ast.Call:
			found = found || f.isCommit(v)
		case *ast.Binary:
			found = found || f.isCommitPipe(v)
		}
		return !found
	})
	return found
}

// commitText is the commit a path is missing, as the body writes it.
func (f *pathFlow) commitText() string {
	suffix := "!"
	if f.rb.propagates {
		suffix = "?"
	}
	return fmt.Sprintf("%s(%s)%s", f.a.kind.commit, f.a.name, suffix)
}

func (f *pathFlow) missError(m pathMiss) TypeError {
	e := TypeError{
		Line: m.line,
		Col:  m.col,
		Message: fmt.Sprintf("`%s` is discarded here: this path returns without `%s`",
			f.a.name, f.commitText()),
		Code: UncommittedResourceCode,
	}
	if edit, ok := f.commitEdit(m); ok {
		e.Fix = &parser.Fix{Title: fmt.Sprintf("Commit `%s`", f.a.name), Edits: []parser.Edit{edit}}
	}
	return e
}

// leakError is the hand-on rule's error at m. Its fix calls the release on
// its own line before m's statement, when m is a statement of its block
// that nothing comes before on its line.
func (f *pathFlow) leakError(m pathMiss) TypeError {
	release := fmt.Sprintf("%s(%s)", f.a.kind.release, f.a.name)
	e := TypeError{
		Line: m.line,
		Col:  m.col,
		Message: fmt.Sprintf("`%s` leaks here: this path returns without handing it on or releasing it; call `%s` before the return",
			f.a.name, release),
		Code: ReturnLeakCode,
	}
	if m.stmt == nil || m.in == nil {
		return e
	}
	span := stmtSpan(m.stmt)
	if span.IsZero() || span.StartCol < 1 {
		return e
	}
	for i, s := range m.in.Stmts {
		if s != m.stmt {
			continue
		}
		prevEnd := m.in.Line
		if i > 0 {
			prevEnd = stmtSpan(m.in.Stmts[i-1]).EndLine
		}
		if prevEnd == 0 || prevEnd >= span.StartLine {
			return e
		}
		at := parser.Pos{Line: span.StartLine, Col: 1}
		edit := parser.Edit{Start: at, End: at, Text: strings.Repeat(" ", span.StartCol-1) + release + "\n"}
		e.Fix = &parser.Fix{Title: fmt.Sprintf("Release `%s` on this path", f.a.name), Edits: []parser.Edit{edit}}
		break
	}
	return e
}

// commitEdit inserts the commit on its own line before m's statement, or
// before the closing brace m is at.
func (f *pathFlow) commitEdit(m pathMiss) (parser.Edit, bool) {
	text := f.commitText()
	if m.stmt != nil {
		span := stmtSpan(m.stmt)
		if span.IsZero() || span.StartCol < 1 {
			return parser.Edit{}, false
		}
		at := parser.Pos{Line: span.StartLine, Col: 1}
		return parser.Edit{Start: at, End: at, Text: strings.Repeat(" ", span.StartCol-1) + text + "\n"}, true
	}
	if m.block != nil && m.block.EndLine > 0 && len(m.block.Stmts) > 0 {
		last := stmtSpan(m.block.Stmts[len(m.block.Stmts)-1])
		if last.IsZero() || last.EndLine >= m.block.EndLine {
			return parser.Edit{}, false
		}
		at := parser.Pos{Line: m.block.EndLine, Col: 1}
		return parser.Edit{Start: at, End: at, Text: strings.Repeat(" ", last.StartCol-1) + text + "\n"}, true
	}
	return parser.Edit{}, false
}
