package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
)

// declaredOrigin is the nominal-identity Origin stamped on every type a
// file declares. See analysis/nominal_identity.go.
func declaredOrigin(fa *FileAnalysis) string {
	if fa == nil {
		return OriginUnresolved
	}
	return fa.Origin
}

// BuildTypeShells pre-populates each top-level type declaration's symbol
// with a shell Type pointer (empty StructType/EnumType/etc., a final
// InterfaceType/ExternType). Other files' BuildTypes calls — running
// after this for ALL files in the project — can then look up the
// imported type's symbol via the Resolved chain and find a usable
// pointer in `.Type` rather than nil.
//
// Without this pre-pass, file order in BuildProject's Sweep C
// determines whether an imported type's `.Type` is set when its
// importer's BuildTypes runs Pass 2 (resolveTypeExpr), which silently
// drops fields whose annotation can't resolve. A struct field
// `logger: Logger` in environment.nomi disappears from the StructType if
// log.nomi's BuildTypes hasn't yet run when environment.nomi's does — and
// the missing field only surfaces when active app field validation consults
// the struct's Fields.
//
// Idempotent: calling BuildTypeShells twice for the same file leaves
// the same shell pointers in place. BuildTypes consults the shells via
// fa.ModuleScope (its existing import-symbol loop) and reuses the
// same pointers.
func BuildTypeShells(fa *FileAnalysis, nodes []ast.Node) {
	for _, node := range nodes {
		buildTypeShellInScope(fa, fa.ModuleScope, node)
	}
}

// typeDeclSymbol returns the symbol a type declaration defined, found by
// the declaration's own position, and whether that symbol is what `name`
// means in `scope`.
//
// The two can differ. A declaration of a prelude name (`enum Ordering` in a
// user file) is reported by checkReservedTypeName, and the prelude's
// synthesized import then takes the module-scope slot
// (importMayTakeScopeSlot lets a synthesized import land last). Looking the
// declaration up by NAME therefore returns the prelude's import symbol, and
// writing the declaration's type onto it made one name mean two
// declarations: a type annotation resolved through the registry to the
// local enum, while `Ordering.Equal` resolved through the scope to
// std/comparable's. So a declaration's type goes on the declaration's own
// symbol, and it is registered under its name only when that symbol owns
// the name. Everything that resolves the name then goes through the scope
// slot, which is the one lookup.
func typeDeclSymbol(fa *FileAnalysis, scope *Scope, name string, line, col int) (*Symbol, bool) {
	if fa != nil {
		if sym := fa.Definitions[Pos{Line: line, Col: col}]; sym != nil && sym.Name == name {
			return sym, scope == nil || scope.Lookup(name) == sym
		}
	}
	if scope == nil {
		return nil, false
	}
	sym := scope.Lookup(name)
	return sym, sym != nil
}

// isAutoSynthImplFor reports whether n is a universal Debug impl the
// compiler synthesized (every item AutoSynth) for a receiver named in names.
func isAutoSynthImplFor(n *ast.ImplBlock, names map[string]bool) bool {
	if len(names) == 0 || !IsSynthesizedLine(n.Line) || !names[TypeExprBaseName(n.Receiver)] {
		return false
	}
	fns := implBlockFuncDefs(n)
	if len(fns) == 0 {
		return false
	}
	for _, fn := range fns {
		if !fn.AutoSynth {
			return false
		}
	}
	return true
}

// ownRegistry is the registry Pass 2 builds a top-level declaration against.
// For a declaration that owns its name it is the file's registry. For one
// that does not (typeDeclSymbol), the file's registry maps the name to the
// declaration that owns it, often std's own type, and the per-declaration
// builders look their type up by name and fill it in; so such a declaration
// is built against a child registry that maps its name to its own type, and
// whatever it registers stays there.
func ownRegistry(fa *FileAnalysis, reg *TypeRegistry, name string, line, col int) *TypeRegistry {
	sym, owns := typeDeclSymbol(fa, fa.ModuleScope, name, line, col)
	if sym == nil || owns {
		return reg
	}
	child := NewChildTypeRegistry(reg)
	if sym.Type != nil {
		child.Register(name, sym.Type)
	}
	return child
}

func buildTypeShellInScope(fa *FileAnalysis, scope *Scope, node ast.Node) {
	if scope == nil || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.StructDef:
		if sym, _ := typeDeclSymbol(fa, scope, n.Name, n.Line, n.Col); sym != nil && sym.Type == nil {
			sym.Type = &StructType{
				Origin:           declaredOrigin(fa),
				Name:             n.Name,
				Opaque:           sym.Opaque,
				OwningSourceFile: sym.SourceFile,
			}
		}
	case *ast.EnumDef:
		if sym, _ := typeDeclSymbol(fa, scope, n.Name, n.Line, n.Col); sym != nil && sym.Type == nil {
			sym.Type = &EnumType{
				Origin:           declaredOrigin(fa),
				Name:             n.Name,
				Opaque:           sym.Opaque,
				OwningSourceFile: sym.SourceFile,
			}
		}
	case *ast.TypeDef:
		if sym, _ := typeDeclSymbol(fa, scope, n.Name, n.Line, n.Col); sym != nil && sym.Type == nil {
			sym.Type = &DistinctType{
				Origin:           declaredOrigin(fa),
				Name:             n.Name,
				Opaque:           sym.Opaque,
				OwningSourceFile: sym.SourceFile,
			}
		}
	case *ast.InterfaceDef:
		if sym, _ := typeDeclSymbol(fa, scope, n.Name, n.Line, n.Col); sym != nil && sym.Type == nil {
			sym.Type = &InterfaceType{
				Origin:     declaredOrigin(fa),
				Name:       n.Name,
				TypeParams: typeParamNames(n.TypeParams),
			}
		}
	case *ast.ExternType:
		if sym, _ := typeDeclSymbol(fa, scope, n.Name, n.Line, n.Col); sym != nil && sym.Type == nil {
			sym.Type = buildExternTypeShell(fa, n)
		}
	}
}

// buildExternTypeShell builds the Type carried on an `host type ...`
// declaration's symbol. The non-generic case is a PrimitiveType: the
// built-in singleton for a built-in name, otherwise one identified by
// (Origin, Name). The generic case (`host type Task<T>`, `host type Channel<T>`)
// produces a DistinctType shell carrying the type-param scaffolding so
// downstream annotation sites (`task: Task<T>`, `ch: Channel<Int>`) get
// instantiated forms that participate in generic-call unification.
//
// The shared TypeParam_ pointers (one per declared param) are reused
// across the declaration and every use site: ResolveTypeExpr's
// GenericType branch clones the shell with TypeArgs populated, and
// extern-fn parameter / return resolution sees the same TypeParam_
// pointer for `T` in both the declaration's annotation and the func's
// inferred params — without that shared identity, unification at the
// call site couldn't bind T (the two T's would be different unification
// variables).
//
// # ORIGIN
//
// The generic case is stamped with the declaring file's Origin, like every
// other arm of buildTypeShellInScope. Without it `pub host type Sender<T>`
// and `pub host type Task<T>` would carry OriginUnresolved, which
// sameNominalIdentity treats as matching ANY origin, so two unrelated modules'
// same-named generic host types would be one type to the checker. The Origin
// is also what lets internal/irbuild key a std generic host type on the
// (Origin, Name) rule every other std type family uses.
//
// The non-generic case is stamped too, unless the name is a built-in:
// `pub host type Bytes` resolves to the analyzer's `primitiveTypes` singleton,
// which is one object process-wide and needs no Origin. Any other name gets a
// PrimitiveType of its own carrying the Origin, so a second analysis of the
// same declaration (a stdlib module re-checked from source, an LSP rebuild)
// produces the same type rather than a stranger with the same name.
func buildExternTypeShell(fa *FileAnalysis, n *ast.ExternType) Type {
	if len(n.TypeParams) == 0 {
		if pt, ok := primitiveTypes[n.Name]; ok {
			return pt
		}
		return &PrimitiveType{Name_: n.Name, Origin: declaredOrigin(fa)}
	}
	params := make([]string, len(n.TypeParams))
	defs := make([]*TypeParam_, len(n.TypeParams))
	for i, tp := range n.TypeParams {
		params[i] = tp.Name
		defs[i] = &TypeParam_{Name_: tp.Name}
	}
	return &DistinctType{
		Origin:        declaredOrigin(fa),
		Name:          n.Name,
		Opaque:        true,
		TypeParams:    params,
		TypeParamDefs: defs,
	}
}

// BuildTypes resolves type annotations and attaches types to symbols.
// It runs two passes:
//  1. Register all user-defined type names so they can reference each other.
//  2. Resolve type details and attach to symbols.
func BuildTypes(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	reg := NewTypeRegistry()

	// Pre-populate registry with types from parent scopes (e.g. primitives types
	// like Result, Maybe) so that modules can reference them. The
	// parent scope is the prelude's ModuleScope when this is a user file —
	// most type symbols there are import aliases (Resolved → real type), so
	// follow the Resolved chain when checking for a type.
	if parent := fa.ModuleScope.Parent; parent != nil {
		for _, sym := range parent.Symbols {
			// File-local imports resolve through this project's declaration
			// graph. A preloaded prelude may hold a separate host-type instance.
			if fa.ModuleScope.Symbols[sym.Name] != nil {
				continue
			}
			real := sym
			if real.Resolved != nil {
				real = real.Resolved
			}
			if real.Type != nil && (real.Kind == SymbolType || real.Kind == SymbolEnum || real.Kind == SymbolStruct || real.Kind == SymbolTypeAlias || real.Kind == SymbolInterface) {
				reg.Register(sym.Name, real.Type)
			}
		}
	}

	// Pass 1: register user-defined type names. If BuildTypeShells has
	// already attached a shell pointer to the symbol, reuse it so other
	// files' BuildTypes runs (which copied that pointer into their own
	// registries via the imports loop) share the same instance — Pass 2
	// will mutate Fields/Methods on this same pointer.
	//
	// The type goes on the declaration's own symbol, and the name is
	// registered only when that symbol owns the name in module scope
	// (typeDeclSymbol). A declaration that lost its slot — a prelude name,
	// already reported as reserved — keeps a type for its own hover and
	// members, and the name resolves to whatever the scope says it means,
	// in a type annotation exactly as in an expression.
	//
	// unnamed holds the declarations whose name belongs to something else in
	// this file. Pass 2 builds each against a registry of its own, and skips
	// the Debug impl synthesized for it.
	var unnamed map[string]bool
	for _, node := range nodes {
		var name string
		var line, col int
		var shell func(sym *Symbol) Type
		switch n := node.(type) {
		case *ast.StructDef:
			name, line, col = n.Name, n.Line, n.Col
			shell = func(sym *Symbol) Type {
				st := &StructType{Origin: declaredOrigin(fa), Name: n.Name}
				if sym != nil {
					st.Opaque, st.OwningSourceFile = sym.Opaque, sym.SourceFile
				}
				return st
			}
		case *ast.EnumDef:
			name, line, col = n.Name, n.Line, n.Col
			shell = func(sym *Symbol) Type {
				et := &EnumType{Origin: declaredOrigin(fa), Name: n.Name}
				if sym != nil {
					et.Opaque, et.OwningSourceFile = sym.Opaque, sym.SourceFile
				}
				return et
			}
		case *ast.TypeDef:
			name, line, col = n.Name, n.Line, n.Col
			shell = func(sym *Symbol) Type {
				dt := &DistinctType{Origin: declaredOrigin(fa), Name: n.Name}
				if sym != nil {
					dt.Opaque, dt.OwningSourceFile = sym.Opaque, sym.SourceFile
				}
				return dt
			}
		case *ast.InterfaceDef:
			name, line, col = n.Name, n.Line, n.Col
			shell = func(*Symbol) Type {
				return &InterfaceType{Origin: declaredOrigin(fa), Name: n.Name, TypeParams: typeParamNames(n.TypeParams)}
			}
		case *ast.ExternType:
			name, line, col = n.Name, n.Line, n.Col
			shell = func(*Symbol) Type { return buildExternTypeShell(fa, n) }
		default:
			continue
		}
		sym, owns := typeDeclSymbol(fa, fa.ModuleScope, name, line, col)
		if sym == nil {
			reg.Register(name, shell(nil))
			continue
		}
		if sym.Type == nil {
			sym.Type = shell(sym)
		}
		if owns {
			reg.Register(name, sym.Type)
		} else {
			if unnamed == nil {
				unnamed = map[string]bool{}
			}
			unnamed[name] = true
		}
	}

	// Register imported types so local type annotations can reference them.
	for _, sym := range fa.ModuleScope.Symbols {
		real := sym
		if real.Resolved != nil {
			real = real.Resolved
		}
		if real.Type != nil && (real.Kind == SymbolStruct || real.Kind == SymbolEnum || real.Kind == SymbolType || real.Kind == SymbolTypeAlias || real.Kind == SymbolInterface) {
			if reg.Lookup(sym.Name) == nil {
				reg.Register(sym.Name, real.Type)
			}
		}
	}
	// Register module-qualified type names (e.g. `dynamic.Dynamic`) so a
	// type annotation written as `d: dynamic.Dynamic` after a bare
	// `import std/dynamic` (without destructuring) resolves through
	// ResolveTypeExpr's QualifiedType branch, which looks up by the full
	// dotted name. Without this, only the destructured-import form
	// (`import std/dynamic.{Dynamic}` — lifts `Dynamic` directly into
	// file scope) would resolve, and the qualified form would fail with
	// `unknown type "dynamic.Dynamic"`. The two import shapes are
	// supposed to be symmetric.
	registerModuleQualifiedTypes(reg, fa.ModuleScope)

	// Pass 2: resolve details and attach to symbols.
	var errs []TypeError
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.StructDef:
			errs = append(errs, buildStructType(fa, ownRegistry(fa, reg, n.Name, n.Line, n.Col), n)...)
		case *ast.EnumDef:
			errs = append(errs, buildEnumType(fa, ownRegistry(fa, reg, n.Name, n.Line, n.Col), n)...)
		case *ast.TypeDef:
			errs = append(errs, buildTypeDef(fa, ownRegistry(fa, reg, n.Name, n.Line, n.Col), n)...)
		case *ast.TypeAlias:
			errs = append(errs, buildTypeAlias(fa, ownRegistry(fa, reg, n.Name, n.Line, n.Col), n)...)
		case *ast.InterfaceDef:
			errs = append(errs, buildInterfaceMethods(fa, ownRegistry(fa, reg, n.Name, n.Line, n.Col), n)...)
		case *ast.FuncDef:
			errs = append(errs, buildFuncType(fa, reg, n)...)
		case *ast.OnceBinding:
			// Imported reads need the declared type before function bodies
			// infer lambda results, regardless of the files' checking order.
			errs = append(errs, buildOnceTypeWithBase(fa, reg, n, nil)...)
		case *ast.ExternFunc:
			errs = append(errs, buildExternFuncType(fa, reg, n)...)
		case *ast.ExternType:
			if sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]; sym != nil && sym.Type == nil {
				sym.Type = buildExternTypeShell(fa, n)
			}
			errs = append(errs, buildExternType(fa, reg, n)...)
		case *ast.ImplBlock:
			// The universal Debug impl synthesized for a declaration that does
			// not own its name would be typed against the type the name does
			// mean (std's `List`, not the local `struct List`), and report
			// errors about a declaration the user never wrote. The
			// declaration is already an error at its own site.
			if isAutoSynthImplFor(n, unnamed) {
				continue
			}
			errs = append(errs, buildImplBlockTypes(fa, reg, n)...)
		}
	}

	// Pass 2b: resolve nested fn signatures inside function bodies. Their
	// Symbols were registered by the builder; we just need to attach FuncType.
	// Also reject declarations that only make sense at module level.
	//
	// A `test` body is walked for the same reason a `fn` body is, and it was
	// added because omitting it was a SILENT hole rather than a missing
	// diagnostic. A `fn` declared inside a test body had its Symbol registered
	// and its Type left nil, so checkNestedFunc's `sym.Type.(*FuncType)` missed
	// and the body went unchecked — which meant no generic call inside it ever
	// recorded an instantiated CallType, and the IR builder refused `Some(1)`
	// inside `test "…" { fn f(): Maybe<Int> { Some(1) } }` under `generic enum
	// over a type parameter` while the identical declaration inside `fn main`
	// lowered. MEASURED at f3a00ce6, both directions.
	//
	// validateNoIllegalNestedDecls is deliberately NOT extended here. It
	// REJECTS constructs, so running it over test bodies for the first time
	// would turn programs that analyse today into errors; the signature walk
	// only ADDS a fact nothing yet had.
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.FuncDef:
			if n.Body != nil {
				errs = append(errs, buildNestedFuncTypes(fa, reg, n.Body)...)
				errs = append(errs, validateNoIllegalNestedDecls(n.Body)...)
			}
		case *ast.TestDecl:
			errs = append(errs, buildTestDeclFuncTypes(fa, reg, n)...)
		}
	}

	// Pass 3: after all impl/extend method signatures are resolved, solve the
	// interface type-argument template for each `impl Iface for T` block. The
	// unifier consults these templates to bind any generic interface's type
	// parameters from a concrete receiver — `Iter<T>` against std/iter's `Seq`
	// or a user iterator binds `T` from `each_while`'s `yield: (T) -> Bool`
	// parameter, and a user `Chooser<T>` binds `T` from whichever
	// method carries it. See recordImplTypeArgsFromBlock + the
	// general-interface-typearg-inference design.
	recordImplTypeArgsInNodes(fa, reg, nodes)

	return errs
}

func recordImplTypeArgsInNodes(fa *FileAnalysis, reg *TypeRegistry, nodes []ast.Node) {
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.ImplBlock:
			recordImplTypeArgsFromBlock(fa, reg, n)
		}
	}
}

// recordImplTypeArgsFromBlock solves the interface type-argument template for a
// block-form `impl Iface for T<...>` and records it in fa.ImplTypeArgs. The
// interface's type params are solved by unifying its declared method signatures
// (params + return, which reference the interface's TypeParam_s and SelfParam as
// unknowns) against the impl's concrete method signatures. Because unify binds
// the LEFT operand's type params first, passing the interface signature on the
// left and the impl signature on the right binds the interface's params to the
// impl's concrete types while leaving the impl type's own formal params alone —
// exactly the directional match the solve needs. The interface's element/key
// type(s) then read out of the resulting substitution.
func recordImplTypeArgsFromBlock(fa *FileAnalysis, reg *TypeRegistry, n *ast.ImplBlock) {
	if n.Interface == nil {
		return
	}
	ifaceName := TypeExprBaseName(n.Interface)
	if ifaceName == "" {
		return
	}
	implTypeName := TypeExprBaseName(n.Receiver)
	if implTypeName == "" {
		return
	}
	iface := lookupInterfaceType(fa, reg, ifaceName)
	if iface == nil {
		return
	}

	blockTP, _ := buildImplBlockTypeParams(fa, reg, n)
	var receiver Type
	if recvResolved, err := ResolveDeclaredType(n.Receiver, reg, blockTP, fa.References); err == nil {
		receiver = recvResolved
	}
	var headerArgs []Type
	if ifaceResolved, err := ResolveTypeExpr(n.Interface, reg, blockTP, fa.References); err == nil {
		if instantiated, ok := ifaceResolved.(*InterfaceType); ok {
			headerArgs = instantiated.TypeArgs
		}
	}
	if len(headerArgs) > 0 && len(headerArgs) != len(iface.TypeParamDefs) {
		fa.TypeErrors = append(fa.TypeErrors, TypeError{
			Line: n.Line,
			Col:  n.Col,
			Message: fmt.Sprintf("%s expects %d type argument(s), got %d",
				ifaceName, len(iface.TypeParamDefs), len(headerArgs)),
		})
		return
	}
	if len(iface.TypeParamDefs) == 0 && !ContainsTypeParam(receiver) {
		return // non-generic interface + concrete receiver: no template to record
	}

	subs := map[*TypeParam_]Type{}
	var typeParamDefs []*TypeParam_
	for _, item := range n.Items {
		mName, mLine, mCol := implItemNameAndPos(item)
		if mName == "" {
			continue
		}
		var sig *MethodSig
		for i := range iface.Methods {
			if iface.Methods[i].Name == mName {
				sig = &iface.Methods[i]
				break
			}
		}
		if sig == nil {
			continue
		}
		msym := fa.Definitions[Pos{Line: mLine, Col: mCol}]
		if msym == nil {
			continue
		}
		ift, ok := msym.Type.(*FuncType)
		if !ok {
			continue
		}
		// Interface declared sig on the left (its TypeParam_s + SelfParam are the
		// unknowns to solve); impl concrete sig on the right.
		for k := 0; k < len(sig.Params) && k < len(ift.Params); k++ {
			if sig.Params[k] != nil && ift.Params[k] != nil {
				_ = UnifyWith(sig.Params[k], ift.Params[k], subs)
			}
		}
		if sig.Return != nil && ift.Return != nil {
			_ = UnifyWith(sig.Return, ift.Return, subs)
		}
		if typeParamDefs == nil {
			typeParamDefs = extractImplTypeParamDefs(ift)
		}
	}

	args := make([]Type, len(iface.TypeParamDefs))
	if len(headerArgs) > 0 {
		copy(args, headerArgs)
		// The header (`Add<T, Box<T>>`) and the receiver (`Box<T>`) were
		// resolved against the same block type params, so the receiver's
		// params are the ones Args mentions. The method signature's params
		// are separate pointers; substituting those would leave the header's
		// T unbound at every use site.
		typeParamDefs = receiverTypeParamDefs(receiver)
	} else {
		for i, def := range iface.TypeParamDefs {
			if bound, ok := subs[def]; ok {
				args[i] = bound
			} else {
				args[i] = def // unconstrained by any method — stays a free param
			}
		}
	}
	if fa.ImplTypeArgs == nil {
		fa.ImplTypeArgs = make(map[string]map[string]*ImplTypeArgs)
	}
	if fa.ImplTypeArgSets == nil {
		fa.ImplTypeArgSets = make(map[string]map[string][]*ImplTypeArgs)
	}
	if fa.ImplTypeArgs[implTypeName] == nil {
		fa.ImplTypeArgs[implTypeName] = make(map[string]*ImplTypeArgs)
	}
	if fa.ImplTypeArgSets[implTypeName] == nil {
		fa.ImplTypeArgSets[implTypeName] = make(map[string][]*ImplTypeArgs)
	}
	info := &ImplTypeArgs{
		TypeParamDefs: typeParamDefs,
		Args:          args,
		Receiver:      receiver,
	}
	if fa.ImplTypeArgs[implTypeName][ifaceName] == nil {
		fa.ImplTypeArgs[implTypeName][ifaceName] = info
	}
	fa.ImplTypeArgSets[implTypeName][ifaceName] = append(fa.ImplTypeArgSets[implTypeName][ifaceName], info)
}

// lookupInterfaceType resolves an interface by name to its *InterfaceType,
// preferring the registry (which holds the fully-built MethodSigs) and falling
// back to the file's module scope.
func lookupInterfaceType(fa *FileAnalysis, reg *TypeRegistry, name string) *InterfaceType {
	if reg != nil {
		if it, ok := reg.Lookup(name).(*InterfaceType); ok {
			return it
		}
	}
	if fa != nil && fa.ModuleScope != nil {
		if sym := fa.ModuleScope.Lookup(name); sym != nil {
			real := sym
			if real.Resolved != nil {
				real = real.Resolved
			}
			if it, ok := real.Type.(*InterfaceType); ok {
				return it
			}
		}
	}
	return nil
}

// implItemNameAndPos returns the method name and definition position of an impl
// block item (a `fn` or `host fn`), or "" if the item is neither.
func implItemNameAndPos(item ast.Node) (string, int, int) {
	switch m := item.(type) {
	case *ast.FuncDef:
		return m.Name, m.Line, m.Col
	case *ast.ExternFunc:
		return m.Name, m.Line, m.Col
	}
	return "", 0, 0
}

// validateNoIllegalNestedDecls walks a function body and emits an error for
// any declaration that has module-level-only semantics (impl, extern). The
// parser accepts these inside blocks, but they
// pollute global state from a local-looking position — confusing at best,
// broken at worst (locally-declared method dispatch tables, host-binding
// shims that escape their owning scope).
//
// Nested `import` is allowed: imports bind names on the local env (cached
// underneath, so no per-call cost) and shadow module-level imports the
// same way local bindings shadow module-level ones.
func validateNoIllegalNestedDecls(node ast.Node) []TypeError {
	var errs []TypeError
	switch n := node.(type) {
	case *ast.ExternFunc:
		errs = append(errs, TypeError{
			Line: n.Line, Col: n.Col,
			Message: "extern declaration must be at the top level",
		})
	case *ast.ExternType:
		errs = append(errs, TypeError{
			Line: n.Line, Col: n.Col,
			Message: "extern declaration must be at the top level",
		})
	case *ast.FuncDef:
		if n.Body != nil {
			errs = append(errs, validateNoIllegalNestedDecls(n.Body)...)
		}
	case *ast.Block:
		for _, stmt := range n.Stmts {
			errs = append(errs, validateNoIllegalNestedDecls(stmt)...)
		}
	case *ast.Binding:
		errs = append(errs, validateNoIllegalNestedDecls(n.Value)...)
	case *ast.PatternBinding:
		errs = append(errs, validateNoIllegalNestedDecls(n.Value)...)
		for _, e := range n.ElseNodes() {
			errs = append(errs, validateNoIllegalNestedDecls(e)...)
		}
	case *ast.With:
		errs = append(errs, validateNoIllegalNestedDecls(n.Value)...)
	case *ast.GroupedExpr:
		errs = append(errs, validateNoIllegalNestedDecls(n.Expr)...)
	case *ast.If:
		errs = append(errs, validateNoIllegalNestedDecls(n.Cond)...)
		errs = append(errs, validateNoIllegalNestedDecls(n.CondPattern)...)
		if n.Then != nil {
			errs = append(errs, validateNoIllegalNestedDecls(n.Then)...)
		}
		if n.Else != nil {
			errs = append(errs, validateNoIllegalNestedDecls(n.Else)...)
		}
	case *ast.Case:
		if n.Value != nil {
			errs = append(errs, validateNoIllegalNestedDecls(n.Value)...)
		}
		for _, br := range n.Branches {
			if br.Body != nil {
				errs = append(errs, validateNoIllegalNestedDecls(br.Body)...)
			}
		}
	case *ast.Lambda:
		if n.Body != nil {
			errs = append(errs, validateNoIllegalNestedDecls(n.Body)...)
		}
	case *ast.Call:
		errs = append(errs, validateNoIllegalNestedDecls(n.Func)...)
		for _, arg := range n.Args {
			errs = append(errs, validateNoIllegalNestedDecls(arg)...)
		}
	}
	return errs
}

// buildBlockNestedTypes scans the immediate statements of a Block for type
// declarations (struct/enum/typedef/typealias/interface) and registers them
// into a fresh child registry. Returns the child registry plus any errors
// from resolving type details. The child registry chains to `reg` so
// outer/module types remain visible.
func buildBlockNestedTypes(fa *FileAnalysis, reg *TypeRegistry, block *ast.Block) (*TypeRegistry, []TypeError) {
	if block == nil {
		return reg, nil
	}
	child := NewChildTypeRegistry(reg)
	// Pass 1: register shells so types can reference each other.
	for _, stmt := range block.Stmts {
		switch n := stmt.(type) {
		case *ast.StructDef:
			child.Register(n.Name, &StructType{Name: n.Name})
		case *ast.EnumDef:
			child.Register(n.Name, &EnumType{Name: n.Name})
		case *ast.TypeDef:
			child.Register(n.Name, &DistinctType{Name: n.Name})
		case *ast.InterfaceDef:
			child.Register(n.Name, &InterfaceType{Origin: declaredOrigin(fa), Name: n.Name, TypeParams: typeParamNames(n.TypeParams)})
		}
	}
	// Pass 2: resolve details against the child registry.
	var errs []TypeError
	for _, stmt := range block.Stmts {
		switch n := stmt.(type) {
		case *ast.StructDef:
			errs = append(errs, buildStructType(fa, child, n)...)
		case *ast.EnumDef:
			errs = append(errs, buildEnumType(fa, child, n)...)
		case *ast.TypeDef:
			errs = append(errs, buildTypeDef(fa, child, n)...)
		case *ast.TypeAlias:
			errs = append(errs, buildTypeAlias(fa, child, n)...)
		case *ast.InterfaceDef:
			errs = append(errs, buildInterfaceMethods(fa, child, n)...)
		}
	}
	return child, errs
}

// buildNestedFuncTypes walks a function body looking for nested FuncDef
// declarations and resolves their signatures via buildFuncType. Recurses
// through Block/If/Case/Lambda bodies so deeply-nested fns are handled.
// When entering a Block, builds a child type registry populated with any
// type declarations at that block level so nested fns can reference them.
func buildNestedFuncTypes(fa *FileAnalysis, reg *TypeRegistry, node ast.Node) []TypeError {
	var errs []TypeError
	switch n := node.(type) {
	case *ast.FuncDef:
		errs = append(errs, buildFuncType(fa, reg, n)...)
		if n.Body != nil {
			errs = append(errs, buildNestedFuncTypes(fa, reg, n.Body)...)
		}
	case *ast.Block:
		// Build a child registry containing this block's nested type defs,
		// then process the block's statements with that registry visible.
		childReg, typeErrs := buildBlockNestedTypes(fa, reg, n)
		errs = append(errs, typeErrs...)
		for _, stmt := range n.Stmts {
			errs = append(errs, buildNestedFuncTypes(fa, childReg, stmt)...)
		}
	case *ast.Binding:
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Value)...)
	case *ast.PatternBinding:
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Value)...)
		for _, e := range n.ElseNodes() {
			errs = append(errs, buildNestedFuncTypes(fa, reg, e)...)
		}
	case *ast.With:
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Value)...)
	case *ast.GroupedExpr:
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Expr)...)
	case *ast.If:
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Cond)...)
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.CondPattern)...)
		if n.Then != nil {
			errs = append(errs, buildNestedFuncTypes(fa, reg, n.Then)...)
		}
		if n.Else != nil {
			errs = append(errs, buildNestedFuncTypes(fa, reg, n.Else)...)
		}
	case *ast.Case:
		if n.Value != nil {
			errs = append(errs, buildNestedFuncTypes(fa, reg, n.Value)...)
		}
		for _, br := range n.Branches {
			if br.Body != nil {
				errs = append(errs, buildNestedFuncTypes(fa, reg, br.Body)...)
			}
		}
	case *ast.Lambda:
		if n.Body != nil {
			errs = append(errs, buildNestedFuncTypes(fa, reg, n.Body)...)
		}
	case *ast.Call:
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Func)...)
		for _, arg := range n.Args {
			errs = append(errs, buildNestedFuncTypes(fa, reg, arg)...)
		}
	case *ast.TestDecl:
		// A `tests` group nested inside another test body. Its own body's
		// statements are the group's children; boot and setup are ordinary
		// expressions that may declare a `fn` too.
		errs = append(errs, buildTestDeclFuncTypes(fa, reg, n)...)
	}
	return errs
}

// buildTestDeclFuncTypes resolves the signatures of every `fn` declared inside
// a `test` or `tests` declaration — its body, and the `boot` and `setup`
// expressions a group carries.
//
// Split out rather than inlined into pass 2b because a `tests` group's children
// are TestDecls of their own, so the walk has to be able to re-enter itself
// through buildNestedFuncTypes.
func buildTestDeclFuncTypes(fa *FileAnalysis, reg *TypeRegistry, n *ast.TestDecl) []TypeError {
	if n == nil {
		return nil
	}
	var errs []TypeError
	if n.Boot != nil {
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Boot)...)
	}
	if n.Setup != nil {
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Setup)...)
	}
	if n.Body != nil {
		errs = append(errs, buildNestedFuncTypes(fa, reg, n.Body)...)
	}
	return errs
}

// extractImplTypeParamDefs walks the self parameter of an impl method's
// FuncType and returns the *TypeParam_ pointers in positional order. For
// `pick(s: Pair<T, U>)`, the FuncType's first param is StructType{Name:
// "Pair", TypeArgs: [TypeParam_("T"), TypeParam_("U")]}; we return those
// pointers so that substituteTypeParamDefs can identify them by identity
// when the caller supplies concrete TypeArgs.
//
// The built-in container types carry their formal param differently than a
// nominal struct/enum: a `self` typed `List<T>` resolves to a *ListType whose
// element is the param, and `Map<K, V>` to a *MapType. We mirror
// unify.go::concreteTypeArgs (List → [Elem], Map → [Key, Val]) so that
// `impl Iter for List`'s template gets TypeParamDefs=[T] (not empty) and
// the use-site substitution `T → Int` actually fires for `List<Int>`.
func extractImplTypeParamDefs(ft *FuncType) []*TypeParam_ {
	if len(ft.Params) == 0 {
		return nil
	}
	return receiverTypeParamDefs(ft.Params[0])
}

// receiverTypeParamDefs returns the formal *TypeParam_ pointers of a generic
// receiver type in positional order (`Pair<T, U>` → [T, U], `List<T>` → [T],
// `Map<K, V>` → [K, V]), or nil when the receiver is not generic or an
// argument is not a bare type parameter.
func receiverTypeParamDefs(recv Type) []*TypeParam_ {
	var args []Type
	switch p := recv.(type) {
	case *StructType:
		args = p.TypeArgs
	case *EnumType:
		args = p.TypeArgs
	case *DistinctType:
		args = p.TypeArgs
	case *ListType:
		args = []Type{p.Elem}
	case *MapType:
		args = []Type{p.Key, p.Val}
	default:
		return nil
	}
	defs := make([]*TypeParam_, 0, len(args))
	for _, a := range args {
		if tp, ok := a.(*TypeParam_); ok {
			defs = append(defs, tp)
		}
	}
	if len(defs) != len(args) {
		// Some arg wasn't a simple TypeParam_ — unsupported for now.
		return nil
	}
	return defs
}

// rejectBoundAliasInValuePosition returns a TypeError when t is a
// *BoundAliasType. Bound aliases (`typealias Name A and B`) are usable
// only in `where` bounds (and in other bound-alias definitions); using one as
// a value type is a checker error with a clear pointer at the right form.
func rejectBoundAliasInValuePosition(t Type, te ast.TypeExpr) (TypeError, bool) {
	ba, ok := t.(*BoundAliasType)
	if !ok {
		return TypeError{}, false
	}
	col := 1
	if te != nil {
		switch n := te.(type) {
		case *ast.SimpleType:
			col = n.Col
		case *ast.GenericType:
			col = n.Col
		}
	}
	line := 0
	if te != nil {
		line = te.LineNum()
	}
	return TypeError{
		Line: line, Col: col,
		Message: fmt.Sprintf("%q is an interface-bound alias and cannot be used as a value type; use it via a `where T: %s` bound instead", ba.Name_, ba.Name_),
	}, true
}

// buildTypeParamsWithFA creates a map of type parameter names to TypeParam_
// values. Internal TypeParam bounds are resolved against the supplied
// registry when present; if reg is nil, bounds are skipped (caller can
// resolve later). When fa is non-nil, the corresponding type-param Symbol
// (registered by the AST builder at the same position) gets its Type field
// set to the TypeParam_ pointer — letting hover and other consumers read the
// bound from the Symbol directly.
func buildTypeParamsWithFA(tps []ast.TypeParam, reg *TypeRegistry, fa *FileAnalysis) map[string]*TypeParam_ {
	if len(tps) == 0 {
		return nil
	}
	m := make(map[string]*TypeParam_, len(tps))
	for _, tp := range tps {
		entry := &TypeParam_{Name_: tp.Name}
		m[tp.Name] = entry
		if fa != nil {
			if sym, ok := fa.Definitions[Pos{Line: tp.Line, Col: tp.Col}]; ok && sym != nil {
				sym.Type = entry
			}
		}
	}
	for _, tp := range tps {
		entry := m[tp.Name]
		if entry == nil {
			continue
		}
		if reg != nil {
			for _, b := range tp.Bounds {
				// Record the original textual form for display before resolving.
				// `where T: ShowAndTag` keeps "ShowAndTag" even when the alias
				// expands to multiple interfaces in `Bounds`.
				entry.AsWritten = append(entry.AsWritten, b.TypeString())

				// Bounds are interface references — no anon struct types
				// expected here, so pass nil refs. The parser does not
				// populate ast.TypeParam.Bounds today (there is no
				// `<T: Iface>` surface form; bounds are `where` clauses, and
				// resolveWhereBounds is the live path), so this loop is
				// reached only by a constructed AST.
				resolved, err := ResolveDeclaredType(b, reg, m, nil)
				if err != nil {
					continue
				}
				switch t := resolved.(type) {
				case *InterfaceType:
					entry.Bounds = append(entry.Bounds, t)
				case *BoundAliasType:
					// Bound alias: splice in its expanded interfaces.
					entry.Bounds = append(entry.Bounds, t.Bounds...)
				}
			}
		}
	}
	return m
}

func buildSemanticTypeParamsWithWhere(fa *FileAnalysis, reg *TypeRegistry, tps []ast.TypeParam, clauses []ast.WhereConstraint, context string) (map[string]*TypeParam_, []TypeError) {
	source := buildTypeParamsWithFA(tps, reg, fa)
	semantic := cloneTypeParamMapDeep(source)
	if semantic == nil {
		semantic = map[string]*TypeParam_{}
	}
	errs := applyWhereBoundsToTypeParams(fa, reg, semantic, clauses, context)
	return semantic, errs
}

// typeParamNames extracts a string slice of type parameter names.
func typeParamNames(tps []ast.TypeParam) []string {
	if len(tps) == 0 {
		return nil
	}
	names := make([]string, len(tps))
	for i, tp := range tps {
		names[i] = tp.Name
	}
	return names
}

// typeParamDefsFromMap extracts the *TypeParam_ pointers from the name→ptr map
// in the order given by tps. Used alongside typeParamNames so the type retains
// pointer identity for its own type parameters (keyed by definition order).
func typeParamDefsFromMap(tps []ast.TypeParam, m map[string]*TypeParam_) []*TypeParam_ {
	if len(tps) == 0 {
		return nil
	}
	defs := make([]*TypeParam_, len(tps))
	for i, tp := range tps {
		defs[i] = m[tp.Name]
	}
	return defs
}

func buildStructType(fa *FileAnalysis, reg *TypeRegistry, n *ast.StructDef) []TypeError {
	var errs []TypeError
	tp, whereErrs := buildSemanticTypeParamsWithWhere(fa, reg, n.TypeParams, n.WhereClauses, fmt.Sprintf("struct '%s'", n.Name))
	errs = append(errs, whereErrs...)

	// Position-based lookup handles nested decls (not in module scope).
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}

	st, ok := reg.Lookup(n.Name).(*StructType)
	if !ok {
		return nil
	}

	fields := make([]FieldDef, 0, len(n.Fields))
	for _, f := range n.Fields {
		if f.TypeAnnotation == nil {
			continue
		}
		resolved, err := ResolveDeclaredType(f.TypeAnnotation, reg, tp, fa.References)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				errs = append(errs, te)
			}
			continue
		}
		if te, bad := rejectBoundAliasInValuePosition(resolved, f.TypeAnnotation); bad {
			errs = append(errs, te)
			continue
		}
		fields = append(fields, FieldDef{Name: f.Name, Type: resolved, HasDefault: f.Default != nil})

		// Attach type to field symbols in Definitions.
		attachFieldType(fa, f.Name, n.Line, resolved)
	}

	st.Fields = fields
	st.TypeParams = typeParamNames(n.TypeParams)
	st.TypeParamDefs = typeParamDefsFromMap(n.TypeParams, tp)
	sym.Type = st
	return errs
}

func buildEnumType(fa *FileAnalysis, reg *TypeRegistry, n *ast.EnumDef) []TypeError {
	var errs []TypeError
	tp, whereErrs := buildSemanticTypeParamsWithWhere(fa, reg, n.TypeParams, n.WhereClauses, fmt.Sprintf("enum '%s'", n.Name))
	errs = append(errs, whereErrs...)

	// Position-based lookup handles nested decls (not in module scope).
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}

	et, ok := reg.Lookup(n.Name).(*EnumType)
	if !ok {
		return nil
	}

	variants := make([]VariantDef, 0, len(n.Variants))
	for _, v := range n.Variants {
		vd := VariantDef{Name: v.Name}

		switch v.Kind {
		case "bare":
			vd.Kind = VariantBare
		case "positional":
			vd.Kind = VariantPositional
			if v.DataTypeExpr != nil {
				resolved, err := ResolveDeclaredType(v.DataTypeExpr, reg, tp, fa.References)
				if err != nil {
					if te, ok := err.(TypeError); ok {
						errs = append(errs, te)
					}
				} else {
					vd.DataType = resolved
				}
			} else {
				// Positional with no type expr is effectively bare.
				vd.Kind = VariantBare
			}
		case "struct":
			vd.Kind = VariantStruct
			fields := make([]FieldDef, 0, len(v.Fields))
			for _, f := range v.Fields {
				if f.TypeAnnotation == nil {
					continue
				}
				resolved, err := ResolveDeclaredType(f.TypeAnnotation, reg, tp, fa.References)
				if err != nil {
					if te, ok := err.(TypeError); ok {
						errs = append(errs, te)
					}
					continue
				}
				// HasDefault, which this loop omitted while the struct loop
				// above set it (:907). Nothing read a variant field's
				// HasDefault until the missing-required-field rule reached
				// checkStructVariantLit, so the omission was invisible: every
				// variant field read as required. It reported 14 defaulted
				// fields missing across 5 files the first time the rule ran,
				// including every field of `variant_field_defaults_test.nomi`,
				// the corpus file for this feature.
				fields = append(fields, FieldDef{Name: f.Name, Type: resolved, HasDefault: f.Default != nil})
				// Attach to the variant's field Symbol so hover shows the type.
				attachFieldType(fa, f.Name, f.Line, resolved)
			}
			vd.Fields = fields
		case "embedded":
			vd.Kind = VariantEmbedded
			if v.EmbeddedTypeExpr != nil {
				resolved, err := ResolveDeclaredType(v.EmbeddedTypeExpr, reg, tp, fa.References)
				if err != nil {
					if te, ok := err.(TypeError); ok {
						errs = append(errs, te)
					}
				} else if iface, ok := resolved.(*InterfaceType); ok {
					// `embeds` is for struct or distinct types only (spec §8):
					// it makes the embedded type a subtype of the enum, which
					// has no meaning for an interface (that would be open
					// polymorphism / dynamic interface dispatch, rejected by
					// §13.7). Without this check the line silently no-ops — declaration parses
					// but no constructor is created and no widening occurs.
					errs = append(errs, TypeError{
						Line: v.Line,
						Col:  v.Col,
						Message: "embeds requires a struct or distinct type, got interface " + iface.Name +
							"; for an interface-typed payload use a single-payload variant: `| Name " + iface.Name + "`",
					})
				} else {
					// embedded variants: for wrapping distinct types, use
					// the inner type as the variant's payload so destructure
					// binds the inner directly rather than a nested
					// DistinctVal. Subtype coercion (UserId ≤ Identifier)
					// is unaffected — `isEmbeddedTypeOf` consults the
					// variant's name (which equals the embedded type's
					// name) rather than `DataType`. Zero-sized distinct
					// embeds (Inner == nil) and struct embeds keep their
					// wrapping type as the payload.
					vd.Embedded = resolved
					if dt, ok := resolved.(*DistinctType); ok && dt.Inner != nil {
						resolved = dt.Inner
					}
					vd.DataType = resolved
				}
			}
		default:
			// Bare variants have no Kind in the parser's spelling — they show
			// up as "positional" with no DataTypeExpr or as a fall-through here.
			vd.Kind = VariantBare
		}

		variants = append(variants, vd)
	}

	et.Variants = variants
	et.TypeParams = typeParamNames(n.TypeParams)
	et.TypeParamDefs = typeParamDefsFromMap(n.TypeParams, tp)
	sym.Type = et

	// Build the enum's own type expression for variant return types.
	// For generic enums like Result<T, E>, this is EnumType with TypeArgs.
	var enumReturnTy Type = et
	if len(tp) > 0 {
		args := make([]Type, len(n.TypeParams))
		for i, p := range n.TypeParams {
			args[i] = tp[p.Name]
		}
		// The declaration's own identity, so a value a variant constructor
		// builds is `(Origin, Name)` like any other use of the enum.
		enumReturnTy = &EnumType{
			Origin:           et.Origin,
			Name:             et.Name,
			TypeArgs:         args,
			Variants:         et.Variants,
			TypeParams:       et.TypeParams,
			TypeParamDefs:    et.TypeParamDefs,
			Opaque:           et.Opaque,
			OwningSourceFile: et.OwningSourceFile,
		}
	}

	// Attach types to variant symbols. Embed variants live only in
	// `enumSym.Members` (not the module scope), so consult that map
	// before falling back to the bare-name lookup.
	for _, v := range n.Variants {
		var vsym *Symbol
		if sym != nil && sym.Members != nil {
			vsym = sym.Members[v.Name]
		}
		if vsym == nil {
			vsym = fa.ModuleScope.Lookup(v.Name)
		}
		if vsym == nil || vsym.Kind != SymbolEnumVariant {
			continue
		}
		vd := findVariant(et, v.Name)
		if vd == nil {
			continue
		}
		if vd.DataType != nil && !IsZeroSized(vd.DataType) {
			// Data-carrying variant: constructor function, e.g. Ok: (T) -> Result<T, E>
			vsym.Type = &FuncType{Params: []Type{vd.DataType}, Return: enumReturnTy}
		} else if len(vd.Fields) > 0 {
			// Struct variant: a struct has no positional form, so neither
			// does the variant (variant_construction.go). Its constructor as
			// a value takes the record call form's one argument, the record
			// of its fields: `f = Shape.Rect` then `f({w: 1.0, h: 2.0})`.
			vsym.Type = &FuncType{Params: []Type{&AnonStructType{Fields: vd.Fields}}, Return: enumReturnTy}
		} else {
			// Bare variant: the enum type itself, e.g. True: Bool
			vsym.Type = enumReturnTy
		}
	}

	return errs
}

func findVariant(et *EnumType, name string) *VariantDef {
	for i := range et.Variants {
		if et.Variants[i].Name == name {
			return &et.Variants[i]
		}
	}
	return nil
}

func buildTypeDef(fa *FileAnalysis, reg *TypeRegistry, n *ast.TypeDef) []TypeError {
	var errs []TypeError
	// Position-based lookup handles nested decls (not in module scope).
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}

	dt, ok := reg.Lookup(n.Name).(*DistinctType)
	if !ok {
		return nil
	}

	if n.InnerTypeExpr != nil {
		resolved, err := ResolveDeclaredType(n.InnerTypeExpr, reg, nil, fa.References)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				return []TypeError{te}
			}
			return nil
		}
		dt.Inner = resolved
	}
	sym.Type = dt
	return errs
}

func buildExternType(fa *FileAnalysis, reg *TypeRegistry, n *ast.ExternType) []TypeError {
	tp, errs := buildSemanticTypeParamsWithWhere(fa, reg, n.TypeParams, n.WhereClauses, fmt.Sprintf("host type '%s'", n.Name))
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}
	if sym == nil {
		return errs
	}
	dt, ok := sym.Type.(*DistinctType)
	if !ok {
		return errs
	}
	dt.TypeParams = typeParamNames(n.TypeParams)
	dt.TypeParamDefs = typeParamDefsFromMap(n.TypeParams, tp)
	return errs
}

// buildInterfaceMethods resolves each interface method's signature and stores
// it on the InterfaceType.Methods slice so the checker can consult the
// declared shape of `Interface.method(...)` calls. `self` references resolve
// to a dedicated TypeParam_ named "self" so use sites can substitute the
// actual implementor; the interface's type parameters likewise become
// TypeParam_ nodes shared with the method signature.
func buildInterfaceMethods(fa *FileAnalysis, reg *TypeRegistry, n *ast.InterfaceDef) []TypeError {
	// Position-based lookup handles nested decls (not in module scope).
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}
	if sym == nil {
		return nil
	}
	it, ok := sym.Type.(*InterfaceType)
	if !ok {
		// For nested interfaces, we registered an InterfaceType in the registry
		// but didn't yet attach to sym; do so now from the registry.
		if regIt, regOk := reg.Lookup(n.Name).(*InterfaceType); regOk {
			it = regIt
			sym.Type = it
		} else {
			return nil
		}
	}
	tp, whereErrs := buildSemanticTypeParamsWithWhere(fa, reg, n.TypeParams, n.WhereClauses, fmt.Sprintf("interface '%s'", n.Name))
	var errs []TypeError
	errs = append(errs, whereErrs...)
	// self is a fresh type param scoped to the interface.
	tp["self"] = &TypeParam_{Name_: "self"}

	methods := make([]MethodSig, 0, len(n.Methods))
	for i := range n.Methods {
		m := &n.Methods[i]
		// Per-method scope: interface params + `self`, plus the method's
		// explicit `<K>` params. Interface methods intentionally do not invent
		// implicit method-local generics from bare PascalCase names in the
		// signature; a local like `K` must be introduced by `fn name<K>`.
		mtp := make(map[string]*TypeParam_, len(tp)+len(m.TypeParams))
		for k, v := range tp {
			mtp[k] = v
		}
		for k, v := range buildTypeParamsWithFA(m.TypeParams, reg, fa) {
			mtp[k] = v
		}
		params := make([]Type, 0, len(m.Params))
		paramNames := make([]string, 0, len(m.Params))
		for _, p := range m.Params {
			paramNames = append(paramNames, p.Name)
			if p.TypeAnnotation == nil {
				params = append(params, nil)
				continue
			}
			resolved, err := ResolveDeclaredType(p.TypeAnnotation, reg, mtp, fa.References)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					errs = append(errs, te)
				}
				params = append(params, nil)
				continue
			}
			params = append(params, resolved)
			// Attach to the default-impl param Symbol (when present) so
			// hover renders e.g. `value: self` rather than bare `value`.
			attachParamType(fa, p.Name, p.Line, p.Col, resolved)
		}
		var ret Type
		if m.ReturnTypeExpr != nil {
			resolved, err := ResolveDeclaredType(m.ReturnTypeExpr, reg, mtp, fa.References)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					errs = append(errs, te)
				}
			} else {
				ret = resolved
			}
		}
		// Default parameter values. A default param behaves exactly as on a free
		// `fn` — including a non-trailing default before a trailing-lambda
		// parameter (the call-site slot-mapping binds a trailing lambda to the
		// last param and fills the skipped middle default), which is how
		// `sort_by(xs, direction = Ascending, key)` lets callers write
		// `Iter.sort_by(xs, |x| key)`. The one restriction: only a
		// Nomi-bodied default method carries the default at runtime (its
		// DefaultFn is a FuncVal that applies it). A required method dispatches
		// to an impl that never saw the interface's default, and a host
		// (`extern`) default is a fixed-arity builtin — so a default on either
		// silently wouldn't apply. Reject both at the source.
		defaultCount := 0
		for _, p := range m.Params {
			if p.Default == nil {
				continue
			}
			defaultCount++
			if m.Body == nil {
				kind := "a required function"
				if m.Extern {
					kind = "a host-backed (`extern`) default"
				}
				errs = append(errs, TypeError{
					Line: p.Line, Col: p.Col,
					Message: fmt.Sprintf("interface function '%s': a default parameter value is only allowed on a default function with a Nomi body, not on %s", m.Name, kind),
				})
			}
		}
		// Method-level `where` constraints. Each names a type variable in
		// scope — the interface's own type param or an explicit method-local,
		// both present in `mtp` — and resolves to the SAME *TypeParam_ pointer
		// that lands in the call site's substitution map (it.TypeParamDefs
		// shares the `tp` pointers; method-locals are the `mtp` additions). So
		// the checker can look the concrete binding up by pointer at each
		// dispatch. The bound is NOT written onto the TypeParam_ itself — that
		// pointer is shared across the interface's methods, so bounding it
		// would leak the constraint onto `map`/`filter`/etc.; the constraint is
		// per-method, enforced and recorded at the call site only.
		whereBounds, whereErrs := resolveWhereBounds(fa, reg, mtp, m.WhereClauses, fmt.Sprintf("interface function '%s'", m.Name))
		errs = append(errs, whereErrs...)
		methods = append(methods, MethodSig{
			Name:         m.Name,
			ParamNames:   paramNames,
			Params:       params,
			Return:       ret,
			HasDefault:   m.Body != nil,
			DefaultCount: defaultCount,
			Open:         m.Open,
			Extern:       m.Extern,
			WhereBounds:  whereBounds,
		})
	}
	it.Methods = methods

	// Resolve `field name: Type` requirements. Field types resolve in the
	// same type-param scope as method signatures — interfaces with type
	// parameters can reference them in field types (e.g.
	// `interface Container<T> { field items: List<T> }`).
	if len(n.Fields) > 0 {
		fields := make([]InterfaceFieldDef, 0, len(n.Fields))
		for _, f := range n.Fields {
			if f.TypeAnnotation == nil {
				continue
			}
			resolved, err := ResolveDeclaredType(f.TypeAnnotation, reg, tp, fa.References)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					errs = append(errs, te)
				}
				continue
			}
			fields = append(fields, InterfaceFieldDef{Name: f.Name, Type: resolved})
		}
		it.Fields = fields
	}

	it.TypeParamDefs = typeParamDefsFromMap(n.TypeParams, tp)
	it.SelfParam = tp["self"]
	return errs
}

func buildOnceTypeWithBase(fa *FileAnalysis, reg *TypeRegistry, n *ast.OnceBinding, base map[string]*TypeParam_) []TypeError {
	if n.TypeAnnotation == nil {
		return nil
	}
	resolved, err := ResolveDeclaredType(n.TypeAnnotation, reg, base, fa.References)
	if err != nil {
		if te, ok := err.(TypeError); ok {
			return []TypeError{te}
		}
		return nil
	}
	if sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]; sym != nil {
		sym.Type = resolved
	}
	return nil
}

func buildTypeAlias(fa *FileAnalysis, reg *TypeRegistry, n *ast.TypeAlias) []TypeError {
	// Position-based lookup handles nested aliases (not in module scope).
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}

	// Bound-alias form: `typealias Name A and B` — RHS is a conjunction
	// of interface bounds. Build a BoundAliasType and register it.
	if len(n.Bounds) > 0 {
		var errs []TypeError
		ifaces := make([]*InterfaceType, 0, len(n.Bounds))
		for _, b := range n.Bounds {
			// Interface-bound alias entries are interfaces — no anon struct
			// types expected, so pass nil refs.
			resolved, err := ResolveDeclaredType(b, reg, nil, nil)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					errs = append(errs, te)
				}
				continue
			}
			switch t := resolved.(type) {
			case *InterfaceType:
				ifaces = append(ifaces, t)
			case *BoundAliasType:
				// Aliasing an alias: splice the inner bounds in.
				ifaces = append(ifaces, t.Bounds...)
			default:
				errs = append(errs, TypeError{
					Line: b.LineNum(), Col: 1,
					Message: fmt.Sprintf("interface-bound alias entry must be an interface, got %s", resolved),
				})
			}
		}
		ba := &BoundAliasType{Name_: n.Name, Bounds: ifaces}
		if sym != nil {
			sym.Type = ba
		}
		reg.Register(n.Name, ba)
		return errs
	}

	if n.TargetTypeExpr == nil {
		return nil
	}
	resolved, err := ResolveDeclaredType(n.TargetTypeExpr, reg, nil, fa.References)
	if err != nil {
		if te, ok := err.(TypeError); ok {
			return []TypeError{te}
		}
		return nil
	}
	// Transparent alias: sym.Type points directly to the target type.
	if sym != nil {
		sym.Type = resolved
	}
	reg.Register(n.Name, resolved)
	return nil
}

func buildFuncType(fa *FileAnalysis, reg *TypeRegistry, n *ast.FuncDef) []TypeError {
	return buildFuncTypeWithBase(fa, reg, n, nil)
}

func buildFuncTypeWithBase(fa *FileAnalysis, reg *TypeRegistry, n *ast.FuncDef, base map[string]*TypeParam_) []TypeError {
	var errs []TypeError
	tp := cloneTypeParamMap(base)
	explicit := buildTypeParamsWithFA(n.TypeParams, reg, fa)
	for k, v := range explicit {
		tp[k] = v
	}
	// Look up by position first (handles impl/extend methods that share names),
	// then fall back to name lookup for regular functions.
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}
	if sym == nil {
		return nil
	}

	params := make([]Type, 0, len(n.Params))
	for _, p := range n.Params {
		// Destructuring param: the type comes from the annotation OR the
		// self-typing pattern head (`fn unwrap(Dur(x)): Int` derives Dur).
		// paramPatternType resolves both and reports the missing-annotation
		// error for type-less shapes. The bound inner names are typed later
		// by checkFunc via checkPattern; here we only need the slot type so
		// call-site argument checks see the right parameter type.
		if p.Destructure != nil {
			resolved, err := paramPatternType(p, reg, tp, fa.References)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					errs = append(errs, te)
				}
				params = append(params, nil)
				continue
			}
			params = append(params, resolved)
			continue
		}
		if p.TypeAnnotation == nil {
			params = append(params, nil)
			continue
		}
		resolved, err := ResolveDeclaredType(p.TypeAnnotation, reg, tp, fa.References)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				errs = append(errs, te)
			}
			params = append(params, nil)
			continue
		}
		if te, bad := rejectBoundAliasInValuePosition(resolved, p.TypeAnnotation); bad {
			errs = append(errs, te)
			params = append(params, nil)
			continue
		}
		params = append(params, resolved)

		// Attach type to param symbols in Definitions.
		attachParamType(fa, p.Name, p.Line, p.Col, resolved)
	}

	var retType Type = TypeUnit
	if n.ReturnTypeExpr != nil {
		resolved, err := ResolveDeclaredType(n.ReturnTypeExpr, reg, tp, fa.References)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				errs = append(errs, te)
			}
		} else if te, bad := rejectBoundAliasInValuePosition(resolved, n.ReturnTypeExpr); bad {
			errs = append(errs, te)
		} else {
			retType = resolved
		}
	}

	// Count every defaulted param, not just trailing ones — defaults can
	// appear anywhere in the list (spec §5 Default Arguments). Required
	// non-trailing slots are still enforced per-slot at the call site.
	defaultCount := 0
	for _, p := range n.Params {
		if p.Default != nil {
			defaultCount++
		}
	}

	whereBounds, whereErrs := resolveWhereBounds(fa, reg, tp, n.WhereClauses, fmt.Sprintf("function '%s'", n.Name))
	errs = append(errs, whereErrs...)

	ft := &FuncType{Params: params, Return: retType, DefaultCount: defaultCount, WhereBounds: whereBounds}
	sym.Type = ft
	return errs
}

func cloneTypeParamMap(base map[string]*TypeParam_) map[string]*TypeParam_ {
	out := make(map[string]*TypeParam_, len(base))
	for k, v := range base {
		out[k] = v
	}
	return out
}

func cloneTypeParamMapDeep(base map[string]*TypeParam_) map[string]*TypeParam_ {
	if len(base) == 0 {
		return nil
	}
	out := make(map[string]*TypeParam_, len(base))
	for k, v := range base {
		if v == nil {
			continue
		}
		cp := *v
		cp.Bounds = append([]*InterfaceType(nil), v.Bounds...)
		cp.AsWritten = append([]string(nil), v.AsWritten...)
		out[k] = &cp
	}
	return out
}

func buildImplBlockTypeParams(fa *FileAnalysis, reg *TypeRegistry, n *ast.ImplBlock) (map[string]*TypeParam_, []TypeError) {
	blockTP := cloneTypeParamMapDeep(buildTypeParamsWithFA(n.Generics, reg, fa))
	if blockTP == nil {
		blockTP = map[string]*TypeParam_{}
	}
	mergeReceiverTypeParams(reg, n.Receiver, blockTP)
	errs := applyWhereBoundsToTypeParams(fa, reg, blockTP, n.WhereClauses, "impl block")
	return blockTP, errs
}

func mergeReceiverTypeParams(reg *TypeRegistry, receiver ast.TypeExpr, out map[string]*TypeParam_) {
	if reg == nil || receiver == nil || out == nil {
		return
	}
	head, args := receiverGenericHead(receiver)
	if head == "" || len(args) == 0 {
		return
	}
	declDefs := typeParamDefsForType(reg.Lookup(head))
	if len(declDefs) == 0 {
		if base := TypeExprBaseName(receiver); base != "" && base != head {
			declDefs = typeParamDefsForType(reg.Lookup(base))
		}
	}
	if len(declDefs) == 0 {
		return
	}
	targets := make([]*TypeParam_, len(args))
	for i, name := range ReceiverTypeParamNames(receiver, func(n string) bool { return reg.Lookup(n) != nil }) {
		if name == "" {
			continue
		}
		tp := out[name]
		if tp == nil {
			tp = &TypeParam_{Name_: name}
			out[name] = tp
		}
		targets[i] = tp
	}
	subs := map[*TypeParam_]Type{}
	for i, target := range targets {
		if target == nil || i >= len(declDefs) || declDefs[i] == nil {
			continue
		}
		subs[declDefs[i]] = target
	}
	for i, target := range targets {
		if target == nil || i >= len(declDefs) || declDefs[i] == nil {
			continue
		}
		mergeTypeParamBounds(target, declDefs[i], subs)
	}
}

// ReceiverTypeParamNames is the type parameter each of an impl receiver's type
// ARGUMENTS contributes, BY ARGUMENT POSITION — "" where that argument is a
// concrete type rather than a parameter, so the result stays index-aligned with
// the declaration's own TypeParamDefs. nil when the receiver has no generic head.
//
// `impl Hashable for Map<K, V>` contributes ["K", "V"]; `impl ToJson for
// Map<String, V>` contributes ["", "V"]. The rule is the one this file has always
// applied and which used to live in a private `simpleReceiverTypeParamArg`, now
// folded in here so there is one copy: a bare *ast.SimpleType argument whose name
// is not a KNOWN TYPE is a parameter.
//
// EXPORTED because the backend asks the same question and had its own answer,
// which was wrong. `internal/irbuild`'s collectStdCandidates classified an impl
// block as generic from `t.Generics` and `t.WhereClauses` alone — and
// *ast.ImplBlock.Generics is POPULATED for a derive-synthesized block
// (`derive Debug for Channel<T>`) and EMPTY for a hand-written one
// (`impl Hashable for Map<K, V>`), so its test answered correctly for exactly the
// half that does not matter. 31 std declarations were consequently refused as
// `stdlib function outside the scalar subset` — a claim that a REPRESENTATION is
// missing — over a signature whose invalid position holds a type PARAMETER, which
// no representation can ever anchor. See internal/irbuild/stdrecvgeneric.go.
//
// `known` rather than a *TypeRegistry so a caller outside this package can supply
// its own resolver: the builder passes `reg.Lookup`, the backend passes the
// module scope. One implementation of the RULE, two resolvers for "is this name a
// type here" — which is the split that keeps the rule from being copied.
func ReceiverTypeParamNames(receiver ast.TypeExpr, known func(string) bool) []string {
	if receiver == nil || known == nil {
		return nil
	}
	head, args := receiverGenericHead(receiver)
	if head == "" || len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, arg := range args {
		st, simple := arg.(*ast.SimpleType)
		if !simple || st.Name == "" || known(st.Name) {
			continue
		}
		out[i] = st.Name
	}
	return out
}

func receiverGenericHead(receiver ast.TypeExpr) (string, []ast.TypeExpr) {
	switch t := receiver.(type) {
	case *ast.GenericType:
		return t.Name, t.Params
	case *ast.QualifiedType:
		head, args := receiverGenericHead(t.Member)
		if head == "" {
			return "", nil
		}
		return t.Module + "." + head, args
	default:
		return "", nil
	}
}

func typeParamDefsForType(t Type) []*TypeParam_ {
	switch ty := t.(type) {
	case *StructType:
		return ty.TypeParamDefs
	case *EnumType:
		return ty.TypeParamDefs
	case *DistinctType:
		return ty.TypeParamDefs
	case *InterfaceType:
		return ty.TypeParamDefs
	default:
		return nil
	}
}

func mergeTypeParamBounds(target, source *TypeParam_, subs map[*TypeParam_]Type) {
	if target == nil || source == nil {
		return
	}
	for _, written := range source.AsWritten {
		appendUniqueString(&target.AsWritten, written)
	}
	for _, bound := range source.Bounds {
		if bound == nil {
			continue
		}
		resolved := Substitute(bound, subs)
		if iface, ok := resolved.(*InterfaceType); ok {
			appendUniqueInterface(&target.Bounds, iface)
		}
	}
}

func appendUniqueString(items *[]string, value string) {
	if value == "" {
		return
	}
	for _, existing := range *items {
		if existing == value {
			return
		}
	}
	*items = append(*items, value)
}

func appendUniqueInterface(items *[]*InterfaceType, value *InterfaceType) {
	if value == nil {
		return
	}
	key := value.String()
	for _, existing := range *items {
		if existing != nil && existing.String() == key {
			return
		}
	}
	*items = append(*items, value)
}

// compilerKnownSynthType resolves a type name that derive SYNTHESIZED code
// may write in an item signature without the deriving file having it in
// scope. Only `Ordering` needs an entry: the other signature types
// synthesis emits (String, Int, Bool, the receiver itself) are registry
// builtins or declared in the deriving file, and `Json` and
// `Json.ShapeError` resolve through synthSupportRegistry, which
// buildImplBlockTypes layers over a synthesized block's registry. Returns nil for anything
// else — the caller then reports the ordinary resolution error, so a
// synthesizer emitting an unexpected name still fails loudly.
func compilerKnownSynthType(te ast.TypeExpr, reg *TypeRegistry, tp map[string]*TypeParam_, refs map[Pos]*Symbol) Type {
	switch t := te.(type) {
	case *ast.SimpleType:
		switch t.Name {
		case "Ordering":
			return TypeOrdering
		case "Result":
			if reg != nil {
				if ty := reg.Lookup("Result"); ty != nil {
					return ty
				}
			}
		}
	case *ast.GenericType:
		if t.Name != "Result" {
			return nil
		}
		args := make([]Type, len(t.Params))
		for i, p := range t.Params {
			resolved, err := ResolveTypeExpr(p, reg, tp, refs)
			if err != nil {
				resolved = compilerKnownSynthType(p, reg, tp, refs)
			}
			if resolved == nil {
				return nil
			}
			args[i] = resolved
		}
		return &EnumType{Name: "Result", TypeArgs: args}
	}
	return nil
}

// buildImplBlockTypes resolves the signatures of an impl block's items. Each
// item's resolved FuncType is attached to its symbol (looked up by position,
// since impl-block fns are not always in module scope). Item-local type params
// (`fn map<U>(...)`) layer on top of the block's generic params.
func buildImplBlockTypes(fa *FileAnalysis, reg *TypeRegistry, n *ast.ImplBlock) []TypeError {
	var errs []TypeError

	// Derive-/auto-synthesized blocks (position in the synth band) are
	// compiler output: their signatures may name `Ordering` (a synthesized
	// `compare` returns it) without the deriving file having it in scope.
	// Those names resolve through the compiler-known route
	// (compilerKnownSynthType) instead of erroring; hand-written blocks
	// keep ordinary scope/registry resolution.
	synthBlock := IsSynthesizedLine(n.Line)
	if synthBlock {
		reg = synthSupportRegistry(fa, reg)
	}

	// Block-level generics shared by every item signature. Use semantic clones
	// so receiver-declared and block-level `where` constraints affect checking
	// without making declaration hovers look as though the bounds were written
	// inline on the impl header.
	blockTP, blockErrs := buildImplBlockTypeParams(fa, reg, n)
	errs = append(errs, blockErrs...)

	for _, item := range n.Items {
		if once, ok := item.(*ast.OnceBinding); ok {
			errs = append(errs, buildOnceTypeWithBase(fa, reg, once, blockTP)...)
		}
	}

	for _, fn := range implBlockFuncDefs(n) {
		// Per-item type-param scope: block generics + item generics.
		tp := make(map[string]*TypeParam_, len(blockTP))
		for k, v := range blockTP {
			tp[k] = v
		}
		for k, v := range buildTypeParamsWithFA(fn.TypeParams, reg, fa) {
			tp[k] = v
		}
		sym := fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
		if sym == nil {
			continue
		}

		params := make([]Type, 0, len(fn.Params))
		for _, p := range fn.Params {
			// Destructuring param: the slot type comes from the annotation OR
			// the self-typing pattern head (`fn as_nanos(Dur(ns)): Int` derives
			// Dur). Mirror buildFuncType — without this the self-typed head was
			// ignored (only p.TypeAnnotation, nil here, was consulted), the slot
			// stayed nil, and checkPattern never typed the inner bindings. The
			// bound inner names are typed later by checkFunc via checkPattern.
			if p.Destructure != nil {
				resolved, err := paramPatternType(p, reg, tp, fa.References)
				if err != nil {
					if te, ok := err.(TypeError); ok {
						errs = append(errs, te)
					}
					params = append(params, nil)
					continue
				}
				params = append(params, resolved)
				continue
			}
			if p.TypeAnnotation == nil {
				params = append(params, nil)
				continue
			}
			te := p.TypeAnnotation
			resolved, err := ResolveDeclaredType(te, reg, tp, fa.References)
			if err != nil {
				if synthBlock {
					if known := compilerKnownSynthType(te, reg, tp, fa.References); known != nil {
						params = append(params, known)
						continue
					}
				}
				if terr, ok := err.(TypeError); ok {
					errs = append(errs, terr)
				}
				params = append(params, nil)
				continue
			}
			params = append(params, resolved)
			attachParamType(fa, p.Name, p.Line, p.Col, resolved)
		}

		var retType Type = TypeUnit
		if fn.ReturnTypeExpr != nil {
			resolved, err := ResolveDeclaredType(fn.ReturnTypeExpr, reg, tp, fa.References)
			if err != nil {
				if known := compilerKnownSynthType(fn.ReturnTypeExpr, reg, tp, fa.References); synthBlock && known != nil {
					retType = known
				} else if terr, ok := err.(TypeError); ok {
					errs = append(errs, terr)
				}
			} else {
				retType = resolved
			}
		}

		defaultCount := 0
		for _, p := range fn.Params {
			if p.Default != nil {
				defaultCount++
			}
		}
		whereBounds, whereErrs := resolveWhereBounds(fa, reg, tp, fn.WhereClauses, fmt.Sprintf("function '%s'", fn.Name))
		errs = append(errs, whereErrs...)
		sym.Type = &FuncType{Params: params, Return: retType, DefaultCount: defaultCount, WhereBounds: whereBounds}
	}

	// ExternFunc items: resolve their signatures the same way.
	for _, item := range n.Items {
		ef, ok := item.(*ast.ExternFunc)
		if !ok {
			continue
		}
		tp := make(map[string]*TypeParam_, len(blockTP))
		for k, v := range blockTP {
			tp[k] = v
		}
		for k, v := range buildTypeParamsWithFA(ef.TypeParams, reg, fa) {
			tp[k] = v
		}
		sym := fa.Definitions[Pos{Line: ef.Line, Col: ef.Col}]
		if sym == nil {
			continue
		}
		params := make([]Type, 0, len(ef.Params))
		for _, p := range ef.Params {
			if p.TypeAnnotation == nil {
				params = append(params, nil)
				continue
			}
			resolved, err := ResolveDeclaredType(p.TypeAnnotation, reg, tp, fa.References)
			if err != nil {
				if terr, ok := err.(TypeError); ok {
					errs = append(errs, terr)
				}
				params = append(params, nil)
				continue
			}
			params = append(params, resolved)
			attachParamType(fa, p.Name, p.Line, p.Col, resolved)
		}
		var retType Type = TypeUnit
		if ef.ReturnTypeExpr != nil {
			if resolved, err := ResolveDeclaredType(ef.ReturnTypeExpr, reg, tp, fa.References); err == nil {
				retType = resolved
			} else if terr, ok := err.(TypeError); ok {
				errs = append(errs, terr)
			}
		}
		whereBounds, whereErrs := resolveWhereBounds(fa, reg, tp, ef.WhereClauses, fmt.Sprintf("extern function '%s'", ef.Name))
		errs = append(errs, whereErrs...)
		// Defaults count here as they do for a module-level extern (see
		// buildExternFuncTypeWithBase). This is the path a member of
		// `host type T { ... }` takes: the body is lowered into an
		// inherent impl block, so `Supervisor.new`'s signature is built
		// right here rather than there.
		externDefaultCount := 0
		for _, p := range ef.Params {
			if p.Default != nil {
				externDefaultCount++
			}
		}
		sym.Type = &FuncType{Params: params, Return: retType, DefaultCount: externDefaultCount, WhereBounds: whereBounds}
	}
	// A synthesized block's signature errors carry the synth band too. Same
	// rule as the checker's — keep the diagnostic, move the address. See
	// checker.repointSynthDiagnostics.
	if synthBlock && n.SynthOriginLine > 0 {
		for i := range errs {
			if !IsSynthesizedLine(errs[i].Line) {
				continue
			}
			errs[i].Line = n.SynthOriginLine
			errs[i].Col = n.SynthOriginCol
		}
	}
	return errs
}

// attachParamType finds a param symbol in Definitions and sets its Type.
func attachParamType(fa *FileAnalysis, name string, line, col int, typ Type) {
	pos := Pos{Line: line, Col: col}
	if sym, ok := fa.Definitions[pos]; ok && sym.Kind == SymbolParam && sym.Name == name {
		sym.Type = typ
	}
}

// recordLambdaPatternType records the type a lambda's destructuring parameter
// was checked against, keyed by its pattern node.
func recordLambdaPatternType(fa *FileAnalysis, pat ast.Node, typ Type) {
	if fa.LambdaPatternTypes == nil {
		fa.LambdaPatternTypes = map[ast.Node]Type{}
	}
	fa.LambdaPatternTypes[pat] = typ
}

// attachFieldType finds field symbols in Definitions matching the given name
// that are on or after the given struct line.
func attachFieldType(fa *FileAnalysis, name string, structLine int, typ Type) {
	for _, sym := range fa.Definitions {
		if sym.Kind == SymbolField && sym.Name == name && sym.Pos.Line >= structLine {
			sym.Type = typ
		}
	}
}

func buildExternFuncType(fa *FileAnalysis, reg *TypeRegistry, n *ast.ExternFunc) []TypeError {
	return buildExternFuncTypeWithBase(fa, reg, n, nil)
}

func buildExternFuncTypeWithBase(fa *FileAnalysis, reg *TypeRegistry, n *ast.ExternFunc, base map[string]*TypeParam_) []TypeError {
	// Type params come from two sources:
	//   - Explicit `<T, U>` clause — captures intentionally declared
	//     function-local type parameters.
	//   - Implicit fallback: scan parameter / return annotations for
	//     PascalCase names not in the type registry and treat each as an
	//     unbound TypeParam_. Preserves the bare `fn f(x: T): T` shape
	//     that the rest of the stdlib uses.
	tp := cloneTypeParamMap(base)
	if len(n.TypeParams) > 0 {
		for k, v := range buildTypeParamsWithFA(n.TypeParams, reg, fa) {
			tp[k] = v
		}
	}
	sym := fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = fa.ModuleScope.Lookup(n.Name)
	}
	if sym == nil {
		return nil
	}

	var errs []TypeError
	params := make([]Type, 0, len(n.Params))
	for _, p := range n.Params {
		if p.TypeAnnotation == nil {
			params = append(params, nil)
			continue
		}
		resolved, err := ResolveDeclaredType(p.TypeAnnotation, reg, tp, fa.References)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				errs = append(errs, te)
			}
			params = append(params, nil)
			continue
		}
		params = append(params, resolved)
		attachParamType(fa, p.Name, p.Line, p.Col, resolved)
	}

	var retType Type = TypeUnit
	if n.ReturnTypeExpr != nil {
		resolved, err := ResolveDeclaredType(n.ReturnTypeExpr, reg, tp, fa.References)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				errs = append(errs, te)
			}
		} else {
			retType = resolved
		}
	}

	whereBounds, whereErrs := resolveWhereBounds(fa, reg, tp, n.WhereClauses, fmt.Sprintf("extern function '%s'", n.Name))
	errs = append(errs, whereErrs...)

	// An extern param may carry a default, exactly as an ordinary `fn`
	// param may. What differs is who supplies the value: the declaration
	// states the default so the analyzer can accept the shorter call and
	// so readers and the LSP can see it, while the host implementation is
	// what actually fills the slot. That split is inherent to an extern —
	// the `.nomi` is a declaration and the behaviour lives in Go.
	defaultCount := 0
	for _, p := range n.Params {
		if p.Default != nil {
			defaultCount++
		}
	}

	ft := &FuncType{Params: params, Return: retType, DefaultCount: defaultCount, WhereBounds: whereBounds}
	sym.Type = ft
	return errs
}

func resolveWhereBounds(fa *FileAnalysis, reg *TypeRegistry, typeParams map[string]*TypeParam_, clauses []ast.WhereConstraint, context string) ([]WhereBound, []TypeError) {
	if len(clauses) == 0 {
		return nil, nil
	}
	var (
		out  []WhereBound
		errs []TypeError
	)
	for _, wc := range clauses {
		param, known := typeParams[wc.Name]
		if !known {
			errs = append(errs, TypeError{
				Line: wc.Line, Col: wc.Col,
				Message: fmt.Sprintf("%s: `where` names unknown type variable `%s`", context, wc.Name),
			})
			continue
		}
		var bounds []*InterfaceType
		for _, b := range wc.Bounds {
			resolved, err := ResolveDeclaredType(b, reg, typeParams, fa.References)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					errs = append(errs, te)
				}
				continue
			}
			switch t := resolved.(type) {
			case *InterfaceType:
				bounds = append(bounds, t)
			case *BoundAliasType:
				bounds = append(bounds, t.Bounds...)
			default:
				errs = append(errs, TypeError{
					Line: wc.Line, Col: wc.Col,
					Message: fmt.Sprintf("%s: `where %s: ...` bound must be an interface, got %s", context, wc.Name, resolved),
				})
			}
		}
		if len(bounds) > 0 {
			out = append(out, WhereBound{Param: param, Bounds: bounds})
		}
	}
	return out, errs
}

func applyWhereBoundsToTypeParams(fa *FileAnalysis, reg *TypeRegistry, typeParams map[string]*TypeParam_, clauses []ast.WhereConstraint, context string) []TypeError {
	whereBounds, errs := resolveWhereBounds(fa, reg, typeParams, clauses, context)
	for _, wb := range whereBounds {
		if wb.Param == nil || len(wb.Bounds) == 0 {
			continue
		}
		wb.Param.Bounds = append(wb.Param.Bounds, wb.Bounds...)
		for _, bound := range wb.Bounds {
			if bound != nil {
				wb.Param.AsWritten = append(wb.Param.AsWritten, bound.String())
			}
		}
	}
	return errs
}

// registerModuleQualifiedTypes walks `scope`'s direct symbols looking
// for SymbolModule entries (bare `import std/dynamic` etc.) and
// registers each type member of the imported module's scope in `reg`
// under the qualified name `<alias>.<TypeName>`. This makes the
// QualifiedType branch of ResolveTypeExpr — which keys lookups on
// `n.TypeString()` — symmetric with destructured imports: a user can
// write `d: dynamic.Dynamic` after `import std/dynamic` and the type
// resolves to the same `*PrimitiveType{Name_: "Dynamic"}` the
// destructured form (`import std/dynamic.{Dynamic}` → bare-name
// `Dynamic` lookup) produces. Symmetric resolution is a prerequisite
// for symmetric impl-manifest recording downstream — the same
// `(Debug, Dynamic)` pair lands either way.
//
// Only types whose owning Symbol has a non-nil `Type` are registered
// (the same gate the bare-name imported-types loop uses). The aliased
// module symbol's Resolved pointer (if any) is followed to its
// underlying scope so re-exported / aliased imports work too.
func registerModuleQualifiedTypes(reg *TypeRegistry, scope *Scope) {
	if scope == nil {
		return
	}
	registerQualifiedTypesFromScope(reg, "", scope)
	for _, modSym := range scope.Symbols {
		real := modSym
		if real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind != SymbolModule || real.ModuleScope == nil {
			continue
		}
		alias := modSym.Name
		registerQualifiedTypesFromScope(reg, alias, real.ModuleScope)
	}
}

func registerQualifiedTypesFromScope(reg *TypeRegistry, prefix string, scope *Scope) {
	if reg == nil || scope == nil {
		return
	}
	for _, ts := range scope.Symbols {
		tReal := ts
		if tReal.Resolved != nil {
			tReal = tReal.Resolved
		}
		name := ts.Name
		if prefix != "" {
			name = prefix + "." + ts.Name
		}
		if tReal.Type != nil {
			switch tReal.Kind {
			case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
				if reg.Lookup(name) == nil {
					reg.Register(name, tReal.Type)
				}
				registerQualifiedTypeMembers(reg, name, tReal)
			}
		}
	}
}

func registerQualifiedTypeMembers(reg *TypeRegistry, prefix string, sym *Symbol) {
	if reg == nil || sym == nil || len(sym.Members) == 0 {
		return
	}
	for memberName, memberSym := range sym.Members {
		real := memberSym
		if real.Resolved != nil {
			real = real.Resolved
		}
		if real.Type == nil {
			continue
		}
		name := prefix + "." + memberName
		switch real.Kind {
		case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
			if reg.Lookup(name) == nil {
				reg.Register(name, real.Type)
			}
			registerQualifiedTypeMembers(reg, name, real)
		}
	}
}
