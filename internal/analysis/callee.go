package analysis

import (
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// CalleeKind is what a call's callee is.
type CalleeKind uint8

const (
	// CalleeUnknown is a callee the checker resolved to nothing: a call
	// it reported.
	CalleeUnknown CalleeKind = iota
	// CalleeFunction is a declared `fn`: Decl is its *ast.FuncDef.
	CalleeFunction
	// CalleeHost is a `host fn`: Decl is its *ast.ExternFunc.
	CalleeHost
	// CalleeImpl is a call that reaches one impl block's function, the
	// receiver's type known where the call is checked: Impl is the block
	// (a derive's synthesized one included) and Decl the function that
	// runs. That is the block's `fn` (*ast.FuncDef, a derive's
	// synthesized one included), its `host fn` (*ast.ExternFunc), or,
	// where the block writes none and inherits the interface's `open fn`
	// default, the interface's *ast.InterfaceMethod, whose default body
	// runs for the block's receiver (For). A generic block's Decl is its
	// template; the instance is the reference's (CallTypeArgs,
	// DispatchReceiver).
	CalleeImpl
	// CalleeInterface is an interface function dispatched where it runs,
	// on a type parameter or an interface value, with no impl known where
	// the call is checked: Decl is its *ast.InterfaceMethod, Impl is nil,
	// and the reference's CallInterface the instance it goes through.
	CalleeInterface
	// CalleeValue is a function value: a parameter, a binding, a field, a
	// `once`, an expression, or a `fn` declared inside a body, whose Decl
	// is its *ast.FuncDef. When the value is a copy of a declared
	// function (`g = parse`), Decl is that function's declaration and
	// Copy the expression the copy was made from.
	CalleeValue
	// CalleeVariant is an enum variant's constructor.
	CalleeVariant
	// CalleeConstructor is a type's constructor: a struct's call form, a
	// distinct type's, a subtype's. Decl is the type's declaration, nil
	// for `self(x)` in an interface default, which builds the
	// implementor.
	CalleeConstructor
	// CalleeKeyword is a keyword form written as a call
	// (`testing.check(c)`).
	CalleeKeyword
)

// Callee is the callee of a call, classified (ResolvedCall.Callee). It
// names declarations by the checker's symbol and node, never by a
// spelling.
type Callee struct {
	Kind CalleeKind
	// Ref is the call's own reference: the symbol at the callee's name,
	// which carries the instantiated signature (CallType, CallTypeArgs),
	// the impl the checker chose (DispatchImpl) and the interface
	// instance a dispatched call goes through (CallInterface). Nil for a
	// callee that is an expression with no name.
	Ref *Symbol
	// Decl is the declaration the call reaches, as Kind says; nil for a
	// value with no declaration behind it and for a keyword form.
	Decl ast.Node
	// File is the path of the file that declares Decl, "" for the stdlib
	// and a file with no path.
	File string
	// Copy, for a call through a local name that copies a declared
	// function, is the expression the copy was made from.
	Copy ast.Node
	// InProgram: Decl is a `fn` one of the program's own files declares,
	// not a stdlib module's.
	InProgram bool
	// Std is which std function Decl is, the zero StdFunc for a function
	// a program's file declares (StdFuncOf).
	Std StdFunc
	// Qualifier is what a call written `Q.m(...)` is qualified with, and
	// Method the name of the function it names there (qualifierOf). The
	// zero Qualifier for any other callee.
	Qualifier Qualifier
	Method    string
	// Owner is the type or interface the function the call names is
	// declared under (an impl block's receiver, an interface), in the
	// same shape as Qualifier; the zero Qualifier for a module-level
	// function. A bare `compare(b, a)` in an interface default names
	// Comparable's function, and Owner says so.
	Owner Qualifier
	// For is the type an interface's function is called for, where the
	// checker knows it at the call (`FromJson.from_json<Int>(j)` is for
	// Int; `T.from_json(j)` for the type parameter T): its `self`. In an
	// interface's default, a call on the default's own `self` is for the
	// interface's SelfParam, the type parameter `self` each instance of
	// the default binds (selfOfDefault). Nil for any other callee.
	For Type
	// Impl is the impl block a call of an interface's function reaches
	// when the receiver's type is known where the call is checked: a
	// concrete receiver of an interface-qualified call, a type-qualified
	// call, a call selected among a type's impls at several type
	// arguments, an operator interface's function called by name. Kind is
	// then CalleeImpl. Nil for a call through a type parameter, on an
	// interface value, and for any function no interface declares
	// (reachedImpl).
	Impl ast.Node
	// Form is how a call of an interface's function is answered, by the
	// rule an operator through the interface is (interfaceUse, so `a ==
	// b` and `Equatable.equal(a, b)` agree): OperatorByImpl where the call
	// reaches a block (Impl), and for a function an impl block declares;
	// OperatorStructural where the receiver's type has no block and meets
	// the interface by its shape (a tuple, an anonymous struct, a
	// collection, a function, a declared type with no Equatable,
	// Hashable or Debug block of its own); OperatorDispatched on an
	// interface value; OperatorPerInstance through a type parameter,
	// which CalleeAt answers at an instance. Zero for a callee no
	// interface declares, and where the receiver's type is unknown or
	// nothing answers (interfaceImplAt).
	Form OperatorForm
}

// RouteOwner is what a call is routed by: the qualifier it is written
// with, or for a bare call, the owner of the function it names.
func (c Callee) RouteOwner() Qualifier {
	if c.Qualifier.Kind != QualNone {
		return c.Qualifier
	}
	return c.Owner
}

// StdFunc names a function a std module declares, by its declaration: the
// module's key (`std/time`), the type it belongs to (`Time`, `Iter`; the
// interface for an interface's function), "" for a module-level function,
// the interface of the impl block that declares it, as the block's header
// writes it with its type arguments (`Add<Days, Date>`; the file's
// ImplBlockInterfaceKey), "" for a function in no interface impl, and its
// declared name. A receiver's impls of one interface at two type
// arguments (`impl Contains<String> for String`, `impl
// Contains<Codepoint> for String`) declare two functions, as an inherent
// function and an impl's of the same name do. An alias, a selective
// import or a re-export of the function names the same StdFunc.
// TestStdFuncsAreDistinct (std) holds that no two declarations of a std
// module share one.
type StdFunc struct {
	Module string
	Owner  string
	Impl   string
	Name   string
}

// IsZero reports whether s names no std function.
func (s StdFunc) IsZero() bool { return s.Module == "" }

// StdFuncOf is the std function decl is, found among the declarations of
// the std files the program reaches (fa's own, for a std file checked on
// its own, and the shared parse of a std module checked from edited text:
// Program.reachedFiles); the zero StdFunc for a declaration no std file
// makes.
func (fa *FileAnalysis) StdFuncOf(decl ast.Node) StdFunc {
	if decl == nil {
		return StdFunc{}
	}
	if IsStdlibKey(fa.Origin) {
		if s, ok := stdDeclsOf(fa)[decl]; ok {
			return s
		}
	}
	if p := fa.Program(); p != nil {
		for _, f := range p.reachedFiles() {
			if f == nil || f == fa || !IsStdlibKey(f.Origin) {
				continue
			}
			if s, ok := stdDeclsOf(f)[decl]; ok {
				return s
			}
		}
	}
	return StdFunc{}
}

// StdFuncs is every function the std file fa declares, by declaration,
// with its identity (StdFuncOf). Empty for a file that is not std's.
func (fa *FileAnalysis) StdFuncs() map[ast.Node]StdFunc {
	if fa == nil || !IsStdlibKey(fa.Origin) {
		return nil
	}
	return stdDeclsOf(fa)
}

// stdDecls caches, per std file analysis, the functions it declares
// (stdDeclsOf). A std file's declarations do not change once built.
var stdDecls sync.Map // *FileAnalysis -> map[ast.Node]StdFunc

// stdDeclsOf is every function the std file f declares, with its
// identity (StdFunc): its `fn`s and `host fn`s, at module level or in an
// impl block, and its interfaces' functions.
func stdDeclsOf(f *FileAnalysis) map[ast.Node]StdFunc {
	if m, ok := stdDecls.Load(f); ok {
		return m.(map[ast.Node]StdFunc)
	}
	m := map[ast.Node]StdFunc{}
	for _, sym := range f.Definitions {
		if sym == nil || sym.Node == nil {
			continue
		}
		switch d := sym.Node.(type) {
		case *ast.FuncDef:
			if sym.Kind == SymbolFunction {
				m[d] = StdFunc{Module: f.Origin, Owner: sym.OwningType, Impl: f.ImplBlockInterfaceKey[d], Name: sym.Name}
			}
		case *ast.ExternFunc:
			if sym.Kind == SymbolFunction {
				m[d] = StdFunc{Module: f.Origin, Owner: sym.OwningType, Impl: f.ImplBlockInterfaceKeyExtern[d], Name: sym.Name}
			}
		case *ast.InterfaceMethod:
			if sym.Kind == SymbolInterfaceMethod {
				m[sym.Node] = StdFunc{Module: f.Origin, Owner: sym.OwningInterface, Name: sym.Name}
			}
		}
	}
	if len(f.Definitions) > 0 {
		stdDecls.Store(f, m)
	}
	return m
}

// calleeInfo is what the checker knows of the function a call's callee
// expression names (calleeOf): the one answer to "which declaration is
// behind this callee, and what may a call of it name and leave out".
type calleeInfo struct {
	// sym is the declaration's symbol, when params are a declaration's.
	sym *Symbol
	// params are the parameters a call of the callee fills: a `fn`'s, a
	// `host fn`'s or an interface function's, a lambda's bound to the
	// name, or the open parameters of a partial bound to it. Nil when the
	// callee has none in view (a parameter, a field, a call's result).
	params []ast.Param
	// declared: params are a declaration's, whose defaults a call fills
	// from the declaration (CallDefaults). Otherwise they are a lambda's
	// or a partial's, which fills its own.
	declared bool
	// copied: the callee is a local name that copies the function,
	// directly or through other copies (`g = parse`, `alias = f`). A copy
	// keeps the function's defaults and has no parameter names.
	copied bool
	// copyOf is the expression a copy of a declared function was made
	// from (`parse` in `g = parse`).
	copyOf ast.Node
}

// named is the parameters a call may name: the callee's own, unless it
// reaches them through a copy.
func (ci calleeInfo) named() []ast.Param {
	if ci.copied {
		return nil
	}
	return ci.params
}

// calleeOf follows funcNode, a call's callee, back to the function it
// names: a declaration, a lambda or partial bound to a name, or either
// through local copies. It is the one place a callee is followed; the
// names a call may write (calleeParams), the defaults it may leave out
// (recordPlacement) and the callee's classification (classifyCallee) all
// read its answer.
func (c *checker) calleeOf(funcNode ast.Node) calleeInfo {
	return c.calleeOfDepth(funcNode, 0)
}

func (c *checker) calleeOfDepth(funcNode ast.Node, depth int) calleeInfo {
	var ci calleeInfo
	for ; depth < maxPartialChain; depth++ {
		sym, ok := c.callFuncSym(funcNode)
		// A parameter's symbol points at the function that declares it,
		// whose parameters are not the parameter's own.
		if !ok || sym == nil || sym.Kind == SymbolParam {
			return calleeInfo{}
		}
		switch d := sym.Node.(type) {
		case *ast.FuncDef:
			ci.sym, ci.params, ci.declared = sym, d.Params, true
			return ci
		case *ast.ExternFunc:
			ci.sym, ci.params, ci.declared = sym, d.Params, true
			return ci
		case *ast.InterfaceMethod:
			ci.sym, ci.params, ci.declared = sym, d.Params, true
			return ci
		case *ast.Binding:
			switch v := d.Value.(type) {
			case *ast.Lambda:
				ci.params = v.Params
				return ci
			case *ast.Capture:
				if call := v.PartialCall(); call != nil {
					ci.params = c.partialOpenParams(call, depth+1)
				} else {
					ci.params = captureParams(v)
				}
				if ci.params == nil {
					return calleeInfo{}
				}
				return ci
			}
			if _, isIdent := funcNode.(*ast.Ident); !isIdent || sym.Kind != SymbolBinding || d.Value == nil {
				return calleeInfo{}
			}
			// A copy: of a declared function, or of another local name.
			value := ungroupExpr(d.Value)
			if _, declared := c.namedFuncParams(value); declared {
				ci.copyOf = value
			} else if _, chained := value.(*ast.Ident); !chained {
				return calleeInfo{}
			}
			ci.copied = true
			funcNode = value
		default:
			return calleeInfo{}
		}
	}
	return calleeInfo{}
}

// maxPartialChain bounds how many bound partial applications and copies
// calleeOf follows back to a declaration.
const maxPartialChain = 64

// calleeParams is the parameters a call through funcNode may name: a
// `fn`'s, a `host fn`'s or an interface function's, a lambda's bound to a
// name, or a bound partial's open ones. Nil for a callee with none in
// view, and for a copy, which has no parameter names.
func (c *checker) calleeParams(funcNode ast.Node) []ast.Param {
	return c.calleeOf(funcNode).named()
}

// calleeCopiesADeclaration reports whether a call's callee is a local
// name that copies, directly or through other copies, a declared function
// (`g = parse`, `g = Parser.parse`) or a lambda or a partial application
// bound to a name (`alias = f` after `f = fn x = 1 -> x`). A `fn` and a
// lambda bound to a name are two ways to name a function, so a copy of
// either keeps its defaults.
func (c *checker) calleeCopiesADeclaration(funcNode ast.Node) bool {
	ci := c.calleeOf(funcNode)
	return ci.copied && ci.params != nil
}

// partialOpenParams is the parameter list of the function a partial
// application makes: the callee's parameter at each `&N`, in the order
// the placeholders are written, with its name and default. A partial is
// called by those names (`f = &connect("h", port: &1)` then `f(port: 80)`)
// and leaves a defaulted one to its default, as the IR builder lowers it
// (internal/irbuild/irpartial.go). Arguments reach slots as in any call
// (placeArgs). It is nil when the callee's parameters are unknown or an
// argument names no slot, so a call of the partial is checked as a call
// through an unnamed function type.
func (c *checker) partialOpenParams(call *ast.Call, depth int) []ast.Param {
	params := c.calleeOfDepth(call.Func, depth).named()
	if params == nil {
		return nil
	}
	names := make([]string, len(params))
	for i, p := range params {
		if p.Destructure == nil {
			names[i] = p.Name
		}
	}
	slots := placeArgs(call.Args, 0, names, make([]Type, len(params)))
	var open []ast.Param
	for i, a := range call.Args {
		slot, value := slots[i], valueOfArg(a)
		if slot < 0 || slot >= len(params) {
			return nil
		}
		if _, isOpen := value.(*ast.Placeholder); isOpen {
			open = append(open, params[slot])
		}
	}
	return open
}

// classifyCallee is the callee of call, classified from the reference the
// checker recorded at its name (FileAnalysis.CalleeOf) and what calleeOf
// finds behind it. It runs once the file's bodies are checked, when each
// reference's DispatchImpl is final (settleCallees).
func (c *checker) classifyCallee(funcNode ast.Node) Callee {
	out := c.fa.calleeAt(funcNode)
	switch out.Kind {
	case CalleeFunction:
		if fd, ok := out.Decl.(*ast.FuncDef); ok && c.nestedFuncs[fd] {
			// A `fn` declared inside a body is a function value of its
			// scope, as a lambda bound to the name is.
			out.Kind = CalleeValue
		}
	case CalleeValue:
		// One that copies a declared function names it.
		if ci := c.calleeOf(funcNode); ci.copied && ci.declared {
			out.Decl, out.File, out.Copy = ci.sym.Node, ci.sym.SourceFile, ci.copyOf
		}
	case CalleeInterface:
		// An impl chosen among several that inherits the function.
		if pos, named := calleeKey(funcNode); named {
			if b := c.selectedImplBlocks[pos]; b != nil {
				out.Impl = b
			}
		}
	}
	return c.fa.complete(funcNode, out)
}

// CalleeOf classifies funcNode, an expression naming what a call calls,
// from the reference the checker recorded at its name: the callee of a
// call the checker checked, or of one the IR builder writes over a
// function the source names (a function value's call of its function). A
// local name is a value; which declared function it copies, and whether a
// `fn` is declared inside a body, only the file's check says
// (classifyCallee).
func (fa *FileAnalysis) CalleeOf(funcNode ast.Node) Callee {
	return fa.complete(funcNode, fa.calleeAt(funcNode))
}

// calleeAt is CalleeOf before the std identity is read.
func (fa *FileAnalysis) calleeAt(funcNode ast.Node) Callee {
	if _, self := funcNode.(*ast.SelfExpr); self {
		// `self(x)` in an interface default: the implementor's
		// constructor, which each instance names.
		return Callee{Kind: CalleeConstructor}
	}
	pos, named := calleeKey(funcNode)
	var ref *Symbol
	if named {
		ref = fa.References[pos]
	}
	if ref == nil {
		// A value callee: a lambda, a call's result, a field of a value.
		return Callee{Kind: CalleeValue}
	}
	base := ref
	for base.Resolved != nil && base.Resolved != base {
		base = base.Resolved
	}
	out := Callee{Ref: ref, File: base.SourceFile}
	switch base.Kind {
	case SymbolAssertion, SymbolTestSetup, SymbolControlFlow, SymbolTryOp:
		out.Kind, out.File = CalleeKeyword, ""
		return out
	case SymbolEnumVariant:
		out.Kind, out.Decl = CalleeVariant, base.Node
		return out
	case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
		out.Kind, out.Decl = CalleeConstructor, base.Node
		return out
	case SymbolParam, SymbolBinding, SymbolField, SymbolOnce:
		// A parameter's symbol points at the function that declares it.
		return Callee{Kind: CalleeValue, Ref: ref}
	}
	// The impl function the checker chose for this call, which the name's
	// own symbol does not say where a type implements a generic interface
	// at several type arguments (`String.contains(s, Regex...)`: the
	// name is the first impl's, the call another's). A Go-bound impl
	// function is its `host fn`.
	chosen := ref.DispatchImpl
	chosenFn, _ := chosen.(*ast.FuncDef)
	switch d := base.Node.(type) {
	case *ast.FuncDef:
		if chosen != nil && chosen != ast.ImplMethodDecl(d) {
			out.Kind, out.Decl, out.InProgram = CalleeImpl, chosen, fa.inProgram(chosenFn)
			return out
		}
		out.Kind, out.Decl, out.InProgram = CalleeFunction, d, fa.inProgram(d)
		return out
	case *ast.ExternFunc:
		out.Kind, out.Decl = CalleeHost, d
		return out
	case *ast.InterfaceMethod:
		// An impl chosen at a receiver that holds the enclosing
		// declaration's type parameters (`Equatable.equal(xs, ys)` with
		// `xs: List<T>`) is each instance's: the call is the interface's
		// function, answered per instance (interfaceUse, CalleeAt), as an
		// operator over the same receiver is.
		if chosen != nil && !ContainsTypeParam(ref.DispatchReceiver) {
			out.Kind, out.Decl, out.InProgram = CalleeImpl, chosen, fa.inProgram(chosenFn)
			if p := fa.Program(); p != nil && chosenFn != nil {
				if declaring := p.ScopedFunctionFiles[chosenFn]; declaring != nil {
					out.File = declaring.FilePath
				}
			} else if declaring := fa.declaringFileOf(chosen); declaring != nil {
				out.File = declaring.FilePath
			}
			return out
		}
		out.Kind, out.Decl = CalleeInterface, d
		return out
	}
	return Callee{Kind: CalleeValue, Ref: ref}
}

// complete is out, the callee funcNode names, with, for a qualified
// callee, what it is qualified with and the method it names
// (qualifierOf), the impl block it reaches (reachedImpl) and its std
// identity (StdFuncOf).
func (fa *FileAnalysis) complete(funcNode ast.Node, out Callee) Callee {
	if acc, ok := funcNode.(*ast.FieldAccess); ok && acc.Field != nil {
		if q := fa.qualifierOf(acc.Object); q.Kind != QualNone {
			out.Qualifier, out.Method = q, acc.Field.Name
			if out.Ref != nil {
				out.Method = resolveRef(out.Ref).Name
			}
		}
	}
	switch out.Kind {
	case CalleeFunction, CalleeHost, CalleeImpl, CalleeInterface:
		if out.Ref != nil {
			base := resolveRef(out.Ref)
			out.Owner = fa.ownerOf(base)
			if out.Method == "" && out.Owner.Kind != QualNone {
				out.Method = base.Name
			}
			switch {
			case out.Qualifier.Kind == QualTypeParam:
				// `T.from_json(j)`: for the type T stands for, which an
				// instance of the body substitutes.
				out.For = out.Qualifier.Param.Type
			case out.Owner.Kind == QualInterface || out.Qualifier.Kind == QualInterface:
				out.For = selfOfDefault(out.Ref.CallTypeArgs["self"])
				if out.For == nil && out.Qualifier.Kind == QualType {
					// `Int.from_missing()`: no argument is the receiver,
					// and the type that qualifies the call is.
					out.For = fa.qualifierType(funcNode)
				}
			}
		}
	}
	return fa.withStd(fa.reachedImpl(out))
}

// ownerOf is the type or interface base, a function's symbol, is declared
// under, resolved in the file that declares the function: a name there
// means what it meant to the declaration.
func (fa *FileAnalysis) ownerOf(base *Symbol) Qualifier {
	kind, name := QualNone, ""
	switch {
	case base.Kind == SymbolInterfaceMethod && base.OwningInterface != "":
		kind, name = QualInterface, base.OwningInterface
	case base.OwningType != "":
		kind, name = QualType, base.OwningType
	default:
		return Qualifier{}
	}
	decl := fa.declaringFileOf(base.Node)
	if decl == nil || decl.ModuleScope == nil {
		return Qualifier{}
	}
	sym := decl.ModuleScope.Lookup(name)
	if sym == nil {
		return Qualifier{}
	}
	sym = resolveRef(sym)
	if sym.Kind == SymbolInterface {
		kind = QualInterface
	}
	return decl.declQualifier(kind, sym)
}

// declaringFileOf is the analysis of the file that declares node, a
// function: fa, a std file the program reaches, or one of the program's
// files.
func (fa *FileAnalysis) declaringFileOf(node ast.Node) *FileAnalysis {
	if node == nil {
		return nil
	}
	declares := func(f *FileAnalysis) bool {
		if IsStdlibKey(f.Origin) {
			_, ok := stdDeclsOf(f)[node]
			return ok
		}
		return f.declaresFunc(node)
	}
	if declares(fa) {
		return fa
	}
	if p := fa.Program(); p != nil {
		for _, f := range p.reachedFiles() {
			if f != nil && f != fa && declares(f) {
				return f
			}
		}
	}
	return nil
}

// declaresFunc reports whether f declares the function node.
func (f *FileAnalysis) declaresFunc(node ast.Node) bool {
	var at Pos
	switch d := node.(type) {
	case *ast.FuncDef:
		at = Pos{Line: d.Line, Col: d.Col}
	case *ast.ExternFunc:
		at = Pos{Line: d.Line, Col: d.Col}
	case *ast.InterfaceMethod:
		at = Pos{Line: d.Line, Col: d.Col}
	default:
		return false
	}
	sym := f.Definitions[at]
	return sym != nil && sym.Node == node
}

// resolveRef follows a reference through imports to its declaration's
// symbol.
func resolveRef(ref *Symbol) *Symbol {
	for ref.Resolved != nil && ref.Resolved != ref {
		ref = ref.Resolved
	}
	return ref
}

// QualifierKind is what a qualified call's qualifier is.
type QualifierKind uint8

const (
	// QualNone: the callee is not a name qualified by a type, an
	// interface, a module or a type parameter (a bare name, a value's
	// field).
	QualNone QualifierKind = iota
	// QualType is a struct, enum, distinct, host or built-in type.
	QualType
	// QualInterface is an interface.
	QualInterface
	// QualModule is a file or std module (`io.print`).
	QualModule
	// QualTypeParam is a type parameter (`T.from_json`).
	QualTypeParam
)

// StdType names a type, an interface or a module std declares: the
// declaring module's key and the declaration's name ("" for a module).
type StdType struct {
	Module string
	Name   string
}

// IsZero reports whether s names nothing std declares.
func (s StdType) IsZero() bool { return s.Module == "" }

// Qualifier is what the qualifier of a call written `Q.m(...)` resolves
// to (Callee.Qualifier), by the declaration the checker bound Q to: an
// import alias, a re-export, a generated owner and a subtype's refinement
// owner all give the declaration's own identity.
type Qualifier struct {
	Kind QualifierKind
	// Std is std's type, interface or module; zero for a program's.
	Std StdType
	// Decl is the declaration of a program's type or interface.
	Decl ast.Node
	// Param is a type parameter's symbol.
	Param *Symbol
}

// qualifierOf is what the qualifier obj resolves to, from the reference
// the checker recorded at it. The one place a qualifier's declaration is
// worked out.
func (fa *FileAnalysis) qualifierOf(obj ast.Node) Qualifier {
	ref := fa.qualifierRef(obj)
	if ref == nil {
		return Qualifier{}
	}
	return fa.QualifierOfSymbol(resolveRef(ref))
}

// qualifierType is the type that qualifies funcNode, a call's callee
// `Q.m`, when Q names a type with no type parameter of its own (`Int`,
// `Date`): the type a call of an interface's function no argument is the
// receiver of is for. Nil for any other callee.
func (fa *FileAnalysis) qualifierType(funcNode ast.Node) Type {
	acc, ok := funcNode.(*ast.FieldAccess)
	if !ok {
		return nil
	}
	ref := fa.qualifierRef(acc.Object)
	if ref == nil {
		return nil
	}
	t := resolveTypeVar(resolveRef(ref).Type)
	if t == nil || ContainsTypeParam(t) {
		return nil
	}
	return t
}

// qualifierRef is the reference the checker recorded at obj, a call's
// qualifier, or nil.
func (fa *FileAnalysis) qualifierRef(obj ast.Node) *Symbol {
	var pos Pos
	switch o := obj.(type) {
	case *ast.TypeIdent:
		pos = Pos{Line: o.Line, Col: o.Col}
	case *ast.Ident:
		pos = Pos{Line: o.Line, Col: o.Col}
	case *ast.FieldAccess:
		// `json.Json.from_text`: the type the module names.
		if o.Field == nil {
			return nil
		}
		pos = Pos{Line: o.Field.Line, Col: o.Field.Col}
	default:
		return nil
	}
	return fa.References[pos]
}

// QualifierOfSymbol is what a qualifier bound to base, a declaration's
// symbol, resolves to.
func (fa *FileAnalysis) QualifierOfSymbol(base *Symbol) Qualifier {
	if base == nil {
		return Qualifier{}
	}
	switch base.Kind {
	case SymbolModule:
		q := Qualifier{Kind: QualModule}
		if p := fa.Program(); p != nil && base.ModuleScope != nil {
			for name, scope := range p.StdlibModuleScopes {
				if scope == base.ModuleScope {
					q.Std = StdType{Module: "std/" + name}
				}
			}
		}
		return q
	case SymbolType:
		if _, param := base.Type.(*TypeParam_); param {
			return Qualifier{Kind: QualTypeParam, Param: base}
		}
		return fa.declQualifier(QualType, base)
	case SymbolStruct, SymbolEnum, SymbolTypeAlias:
		return fa.declQualifier(QualType, base)
	case SymbolInterface:
		return fa.declQualifier(QualInterface, base)
	}
	return Qualifier{}
}

// StdTypeQualifier is the qualifier of the type or interface a std module
// declares as name, whatever this file's scope binds the name to: the
// declaration module (`std/maps`) makes, or with module "", the one std
// module of the program that declares a type or interface of that name.
// The zero Qualifier when none does, or more than one.
func (fa *FileAnalysis) StdTypeQualifier(module, name string) Qualifier {
	p := fa.Program()
	if p == nil || name == "" {
		return Qualifier{}
	}
	var found Qualifier
	for short, scope := range p.StdlibModuleScopes {
		key := "std/" + short
		if scope == nil || module != "" && key != module {
			continue
		}
		sym := scope.LookupLocal(name)
		if sym == nil {
			continue
		}
		q := fa.QualifierOfSymbol(resolveRef(sym))
		if q.Kind != QualType && q.Kind != QualInterface || q.Std != (StdType{Module: key, Name: name}) {
			// An import of another module's declaration, or no type.
			continue
		}
		if found.Kind != QualNone && found != q {
			return Qualifier{}
		}
		found = q
	}
	return found
}

// declQualifier is the qualifier of kind k that base, a type's or an
// interface's symbol, declares: its nominal identity (origin, name) where
// its type carries one, which an alias and a subtype's owner share with
// the declaration, else the std file that declares its node (a built-in
// type, `Int`).
func (fa *FileAnalysis) declQualifier(k QualifierKind, base *Symbol) Qualifier {
	origin, name := "", ""
	switch t := base.Type.(type) {
	case *StructType:
		origin, name = t.Origin, t.Name
	case *EnumType:
		origin, name = t.Origin, t.Name
	case *DistinctType:
		origin, name = t.Origin, t.Name
	case *InterfaceType:
		origin, name = t.Origin, t.Name
	}
	if origin == "" {
		if mod, ok := fa.stdTypeModuleOf(base.Node); ok {
			origin, name = mod, base.Name
		}
	}
	if IsStdlibKey(origin) {
		return Qualifier{Kind: k, Std: StdType{Module: origin, Name: name}}
	}
	if base.Kind == SymbolTypeAlias {
		// `typealias Tiny Small`: the declaration the alias names.
		if decl := fa.programTypeDecl(origin, name); decl != nil {
			return Qualifier{Kind: k, Decl: decl}
		}
	}
	return Qualifier{Kind: k, Decl: base.Node}
}

// programTypeDecl is the declaration of the type a program's file of
// origin declares as name at its top level, or nil.
func (fa *FileAnalysis) programTypeDecl(origin, name string) ast.Node {
	if origin == "" || name == "" {
		return nil
	}
	files := []*FileAnalysis{fa}
	if p := fa.Program(); p != nil {
		files = append(files, p.Files()...)
	}
	for _, f := range files {
		if f == nil || f.Origin != origin || f.ModuleScope == nil {
			continue
		}
		if sym := f.ModuleScope.Lookup(name); sym != nil {
			if sym = resolveRef(sym); sym.Kind != SymbolTypeAlias && sym.Node != nil {
				return sym.Node
			}
		}
	}
	return nil
}

// stdTypeModuleOf is the std module whose file declares the type or
// interface node.
func (fa *FileAnalysis) stdTypeModuleOf(node ast.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	files := []*FileAnalysis{fa}
	if p := fa.Program(); p != nil {
		files = append(files, p.Files()...)
	}
	for _, f := range files {
		if f == nil || !IsStdlibKey(f.Origin) {
			continue
		}
		if stdTypeDeclsOf(f)[node] {
			return f.Origin, true
		}
	}
	return "", false
}

// stdTypeDecls caches, per std file analysis, the type and interface
// declarations it makes (stdTypeDeclsOf).
var stdTypeDecls sync.Map // *FileAnalysis -> map[ast.Node]bool

func stdTypeDeclsOf(f *FileAnalysis) map[ast.Node]bool {
	if m, ok := stdTypeDecls.Load(f); ok {
		return m.(map[ast.Node]bool)
	}
	m := map[ast.Node]bool{}
	for _, sym := range f.Definitions {
		if sym == nil || sym.Node == nil {
			continue
		}
		switch sym.Kind {
		case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
			m[sym.Node] = true
		}
	}
	if len(f.Definitions) > 0 {
		stdTypeDecls.Store(f, m)
	}
	return m
}

// settleCallees classifies the callee of every call this check recorded
// (recordCall), once its bodies are checked and the dispatch records are
// final. A record another check made is that check's: the file's own
// calls, when a test is checked against the file afterwards
// (CheckAddedTest), share their records with the file, and this check saw
// none of the `fn`s declared in those bodies (nestedFuncs).
func (c *checker) settleCallees() {
	for _, n := range c.recordedCalls {
		rc := c.fa.Calls[n]
		switch n := n.(type) {
		case *ast.Call:
			rc.Callee = c.classifyCallee(n.Func)
		case *ast.Then, *ast.Tap:
			// The stage's function, called with the piped value.
			rc.Callee = Callee{Kind: CalleeValue}
			if tap, ok := n.(*ast.Tap); ok && tap.Name != nil {
				rc.Callee = c.classifyCallee(tap.Name)
			}
		}
	}
}

// inProgram reports whether one of the program's own files, and no stdlib
// module, declares fd.
func (fa *FileAnalysis) inProgram(fd *ast.FuncDef) bool {
	p := fa.Program()
	if p == nil {
		return false
	}
	declaring := p.ScopedFunctionFiles[fd]
	return declaring != nil && !IsStdlibKey(declaring.Origin)
}
