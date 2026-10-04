// Package hoverdoc renders a Nomi symbol to the markdown hover content shown by
// the editor (via the LSP) and the language tour — signatures, doc comments,
// instantiated generics, interface bounds. It is glsp-free so non-LSP callers
// (cmd/nomi-wasm's nomiHover, cmd/nomi-docgen) can use it without pulling the LSP
// framework into the binary — notably keeping it out of the tour wasm.
package hoverdoc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// At returns the hover markdown for the symbol at pos in fa, or "" when
// there's no symbol there. It is the same RENDERER the LSP's
// textDocumentHover uses, exposed for non-LSP callers — the browser tour's
// nomiHover (cmd/nomi-wasm).
//
// Callers provide project analysis so scoped field references resolve with
// the same contract and nominal identity as the editor.
func At(fa *analysis.FileAnalysis, pos analysis.Pos) string {
	if fa == nil {
		return ""
	}
	sym := fa.SymbolAt(pos)
	if sym == nil {
		return ""
	}
	return RenderWithAnalysis(sym, fa)
}

// Render returns markdown hover content for a symbol.
func Render(sym *analysis.Symbol) string {
	return RenderWithAnalysis(sym, nil)
}

// RenderWithAnalysis returns markdown hover content for a symbol, using fa for
// project-wide facts that are not stored on the symbol itself.
func RenderWithAnalysis(sym *analysis.Symbol, fa *analysis.FileAnalysis) string {
	// `self` in an impl method signature is a typealias for the block's
	// receiver type — render it as that type (`self = List<T>`). Without this
	// we'd follow sym.Resolved (the receiver type's own symbol) below, which
	// for a primitive like List/String surfaces the unhelpful "import
	// std/lists" / "import std/strings" origin instead of telling you what
	// `self` stands for.
	// Import-path `self` markers (`std/calendar.{self, Date}`) carry a
	// non-ImplBlock node, so they fall through to their module/enum hover.
	if sym.Name == "self" {
		if impl, ok := sym.Node.(*ast.ImplBlock); ok && impl.Receiver != nil {
			header := fmt.Sprintf("```nomi\nself = %s\n```", impl.Receiver.TypeString())
			// For a struct/enum receiver, append the receiver type's own
			// hover card (its body, doc comment, and opacity) so `self`
			// surfaces what the type *is*, not just its name. Skip it for
			// other receivers: a primitive like List/String resolves to an
			// unhelpful "import std/lists" origin (the reason this branch
			// exists at all), and a distinct `type` head adds nothing over
			// the header.
			if r := sym.Resolved; r != nil && isStructOrEnumDecl(r) {
				return header + "\n\n" + RenderWithAnalysis(r, fa)
			}
			return header
		}
	}

	var sig, doc string

	// Interface-qualified call (`Display.to_string(42)`) with a
	// statically concrete receiver: the checker recorded the concrete
	// impl this call dispatches to. Render that impl's signature + doc —
	// the same target go-to-def jumps to — instead of the abstract
	// interface-method contract or the param-name-less instantiated type
	// the CallType branch below would produce. Nil for generic /
	// existential receivers, which keep rendering the interface contract.
	if sym.DispatchImpl != nil {
		// The impl method is a `fn` (*FuncDef) or an `host fn` (*ExternFunc);
		// both render the same signature shape.
		var (
			name   string
			tps    []ast.TypeParam
			params []ast.Param
			ret    ast.TypeExpr
			doc    string
		)
		switch n := sym.DispatchImpl.(type) {
		case *ast.FuncDef:
			name, tps, params, ret, doc = n.Name, n.TypeParams, n.Params, n.ReturnTypeExpr, n.Doc
		case *ast.ExternFunc:
			name, tps, params, ret, doc = n.Name, n.TypeParams, n.Params, n.ReturnTypeExpr, n.Doc
		}
		if ret == nil {
			ret = &ast.SimpleType{Name: "Unit"}
		}
		result := fmt.Sprintf("```nomi\n%s\n```", RenderFuncSigResolvedSelf(name, tps, params, ret, sym.CallType, sym.DispatchReceiver, "")+renderWhereClausesForNode(sym.DispatchImpl))
		if doc != "" {
			result += "\n\n" + doc
		}
		return result
	}

	// If this is a call-site reference with an instantiated type, render from
	// the type — but only when the instantiation is CONCRETE.
	//
	// The concreteness test is new and it protects a property this branch used
	// to get for free. `displayValueType` deliberately renders an unresolved
	// `TypeParam_` as `_`, "so call-site signatures don't leak `?1` or stale
	// param names", and that was sound while every recorded CallType was fully
	// resolved: an abstract one could only be a leftover. The checker now
	// records a call whose instantiation is determined but still names the
	// ENCLOSING declaration's own type parameters — `Showable.show(value)`
	// inside `fn render<T>(value: T) where T: Showable` records `(T) -> String`
	// — and for those the erasure turns a readable contract into `fn show(_):
	// String`, which is strictly less than the interface-method rendering the
	// branches below produce.
	//
	// So the rule is "render the instantiation when it says more than the
	// declaration does". Nothing that reached this branch before reaches a
	// different arm now, because a fully resolved type contains no parameter by
	// definition.
	if sym.CallType != nil && !analysis.ContainsTypeParam(sym.CallType) {
		if ft, ok := sym.CallType.(*analysis.FuncType); ok {
			resolved := sym
			if sym.Resolved != nil {
				resolved = sym.Resolved
			}
			// Variant constructors render as `variant` rather than `fn` —
			// there's no top-level `fn Some` declaration to navigate to,
			// and labelling them as functions makes the reader hunt for
			// one that doesn't exist.
			prefix := "fn "
			if resolved.Kind == analysis.SymbolEnumVariant {
				prefix = "variant "
			}
			receiverName := ""
			if resolved.Kind != analysis.SymbolInterfaceMethod {
				receiverName = sym.CallReceiver
			}
			sig = renderFuncSigFromType(prefix, sym.Name, ft, resolved.Node, receiverName)
			doc = docFromNode(resolved.Node)
			result := fmt.Sprintf("```nomi\n%s\n```", sig)
			if doc != "" {
				result += "\n\n" + doc
			}
			return result
		}
	}

	// Follow resolved pointer for selective imports to show real definition info.
	if sym.Resolved != nil {
		sym = sym.Resolved
	}

	// Handle param, binding, and interface method by kind first, since their Node may point to the parent.
	switch sym.Kind {
	case analysis.SymbolInterfaceMethod:
		if m, ok := sym.Node.(*ast.InterfaceMethod); ok {
			// Prefix with `fn ` so the markdown snippet parses as a
			// function declaration in the hover popup's tree-sitter
			// pass — without it, `self` in the params falls back to
			// identifier coloring instead of the keyword highlight
			// it gets in the source buffer. Also matches the default
			// branch's *ast.InterfaceMethod rendering below.
			sig = "fn " + RenderInterfaceMethodSig(m)
			// Its `///`. Every sibling kind here carries its doc
			// through; this arm and the default branch's
			// *ast.InterfaceMethod case were the two that did not, so
			// a documented interface method hovered as a bare
			// signature.
			doc = m.Doc
		}
	case analysis.SymbolParam:
		sig = sym.Name
		if ta := findParamType(sym); ta != nil {
			sig = sym.Name + ": " + renderTypeExprSelfAware(ta, sym.Type, nil, sym.ReceiverDisplay)
		} else if sym.Type != nil {
			sig = sym.Name + ": " + displayValueTypeWithReceiver(sym.Type, sym.ReceiverDisplay)
		}
		if sym.Type != nil && analysis.ContainsTypeParam(sym.Type) {
			if fnSig := enclosingFuncSig(sym.Node); fnSig != "" {
				sig += "\n\n" + fnSig
			}
		}
		if st, ok := sym.Type.(*analysis.StructType); ok {
			sig += "\n\n" + renderStructFromType(st)
		}
	case analysis.SymbolBinding:
		sig = sym.Name
		if sym.Type != nil {
			sig = sym.Name + ": " + displayBindingType(sym)
		}
		if sym.Type != nil && analysis.ContainsTypeParam(sym.Type) {
			if fnSig := enclosingFuncSig(sym.Node); fnSig != "" {
				sig += "\n\n" + fnSig
			}
		}
		if st, ok := sym.Type.(*analysis.StructType); ok {
			sig += "\n\n" + renderStructFromType(st)
		}
	case analysis.SymbolOnce:
		// `once name: T = expr` — module-level lazy memoized binding.
		// Render as `once name: T = value` so the kind, type, and value
		// (when literal-renderable) are visible at hover.
		sig = "once " + sym.Name
		switch {
		case sym.Type != nil:
			sig = "once " + sym.Name + ": " + displayBindingType(sym)
		default:
			if n, ok := sym.Node.(*ast.OnceBinding); ok && n.TypeAnnotation != nil {
				sig = "once " + sym.Name + ": " + n.TypeAnnotation.TypeString()
			}
		}
		if n, ok := sym.Node.(*ast.OnceBinding); ok {
			if valStr := renderConstValue(n.Value); valStr != "" {
				sig += " = " + valStr
			}
			doc = n.Doc
		}
		if st, ok := sym.Type.(*analysis.StructType); ok {
			sig += "\n\n" + renderStructFromType(st)
		}
	case analysis.SymbolField:
		sig = sym.Name
		doc = sym.Doc
		if sym.Type != nil {
			sig = sym.Name + ": " + displayValueType(sym.Type)
		} else if f, ok := sym.Node.(*ast.InterfaceField); ok {
			sig = renderInterfaceField(f)
			doc = f.Doc
		}
	case analysis.SymbolEnumVariant:
		if enum, ok := sym.Node.(*ast.EnumDef); ok {
			if v, ok := findEnumVariant(enum, sym); ok {
				sig = renderEnumVariantDecl(v)
				doc = v.Doc
			}
		}
	case analysis.SymbolArgHint:
		// Synthetic hover-only marker for literal call args / `_`
		// placeholders. Render as `<param_name>: <type>` so a hover on
		// `100` in `divide(100, 50)` shows `x: Int`.
		if sym.Type != nil {
			sig = sym.Name + ": " + displayValueType(sym.Type)
		} else {
			sig = sym.Name
		}
	case analysis.SymbolLiteral:
		// Synthetic hover-only marker for a literal whose spelling does not
		// name its type: `'a': Codepoint`.
		sig = sym.Name + ": " + displayValueType(sym.Type)
	case analysis.SymbolAssertion:
		// Synthetic hover-only marker for assertion keywords. Like `try`,
		// this renders prose outside the code fence, so return directly.
		return renderAssertionHover(sym)
	case analysis.SymbolTestSetup:
		// Synthetic hover-only marker for `setup` in a `tests` block.
		return renderTestSetupHover(sym)
	case analysis.SymbolTestDecl:
		// Synthetic hover-only marker for `test` and `tests` declarations.
		return renderTestDeclHover(sym)
	case analysis.SymbolTryOp:
		// Synthetic hover-only marker for the `try` prefix keyword.
		// The renderer produces its own multi-line markdown (signature
		// line in a nomi fence + one prose line for the propagated
		// branch and boundary), so it returns directly instead of
		// flowing through the shared `sig`-wrapping path below — that
		// path only knows how to render a single code fence.
		return renderTryOpHover(sym)
	case analysis.SymbolControlFlow:
		// Synthetic hover-only marker for expression control-flow keywords.
		return renderControlFlowHover(sym)
	case analysis.SymbolImplKeyword:
		// Synthetic hover-only marker for `impl` conformance and
		// implementation-function keywords.
		return renderImplHover(sym)
	case analysis.SymbolModule:
		// Explicit `import std.X` symbols carry the ImportStmt node — render
		// the full path. Implicit-stdlib references (e.g. `io.inspect`
		// without an explicit import) have Node=nil; render as `module name`
		// instead of bare name.
		if imp, ok := sym.Node.(*ast.ImportStmt); ok {
			sig = "import " + renderImportPath(imp)
		} else {
			sig = "module " + sym.Name
		}
	default:
		// Type parameters (Kind=SymbolType, no AST node) — render as
		// `<T> type parameter`. Interface bounds are described in prose below
		// so the hover mirrors source syntax (`where T: Iface`) and remains
		// distinct from regular types.
		if sym.Kind == analysis.SymbolType && sym.Node == nil {
			return renderTypeParamHover(sym, fa)
		}
		switch n := sym.Node.(type) {
		case *ast.FuncDef:
			// When there's no explicit return annotation but a FuncType has
			// been attached (BuildTypes ran), synthesize a Unit return-type
			// expression and pass it down — nil Return means Unit, which is
			// also the implicit default. Lets hover show `fn helper(): Unit`
			// instead of bare `fn helper()`.
			ret := n.ReturnTypeExpr
			if ret == nil {
				if _, ok := sym.Type.(*analysis.FuncType); ok {
					ret = &ast.SimpleType{Name: "Unit"}
				}
			}
			sig = RenderFuncSigResolvedSelf(n.Name, n.TypeParams, n.Params, ret, sym.Type, nil, sym.OwningType) + renderWhereClauses(n.WhereClauses)
			doc = n.Doc
		case *ast.ExternFunc:
			sig = RenderFuncSigResolvedSelf(n.Name, n.TypeParams, n.Params, n.ReturnTypeExpr, sym.Type, nil, sym.OwningType) + renderWhereClauses(n.WhereClauses)
			doc = n.Doc
		case *ast.StructDef:
			sig = renderStructDef(n)
			doc = n.Doc
		case *ast.EnumDef:
			sig = renderEnumDef(n)
			doc = n.Doc
		case *ast.InterfaceDef:
			sig = renderInterfaceDef(n)
			doc = n.Doc
		case *ast.TypeDef:
			s := "type " + n.Name
			if n.InnerTypeExpr != nil {
				s += " " + n.InnerTypeExpr.TypeString()
			}
			sig = s
			doc = n.Doc
		// No `doc = n.Doc` here, deliberately, unlike every sibling in
		// this switch. The `switch sym.Kind` above already claims
		// SymbolInterfaceMethod and sets the doc there, and every
		// Symbol in analysis/builder.go carrying an *ast.InterfaceMethod
		// node is either that kind or SymbolParam, which also has its
		// own arm above — so no symbol was found that reaches this one.
		// Setting it here would be a change with no witness; the arm
		// stays as it was.
		case *ast.InterfaceMethod:
			sig = "fn " + RenderInterfaceMethodSig(n)
			if n.Body != nil {
				sig += " { ... }"
			}
		case *ast.InterfaceField:
			sig = renderInterfaceField(n)
			doc = n.Doc
		case *ast.TypeAlias:
			if len(n.Bounds) > 0 {
				bs := make([]string, len(n.Bounds))
				for i, b := range n.Bounds {
					bs[i] = b.TypeString()
				}
				sig = "typealias " + n.Name + " " + strings.Join(bs, " and ")
			} else {
				sig = "typealias " + n.Name + " " + n.TargetTypeExpr.TypeString()
			}
			doc = n.Doc
		case *ast.ExternType:
			sig = "type " + n.Name + renderTypeParams(n.TypeParams) + renderWhereClauses(n.WhereClauses)
			doc = n.Doc
		case *ast.ImportStmt:
			sig = "import " + renderImportPath(n)
		case *ast.AnonStructType:
			sig = displayValueType(sym.Type)
		default:
			return fmt.Sprintf("```nomi\n%s\n```", sym.Name)
		}
	}

	result := fmt.Sprintf("```nomi\n%s\n```", sig)
	// Surface opacity in hover so consumers see "this type is opaque"
	// alongside its signature. Real symbol kinds only — type-parameter
	// hovers return earlier and skip this branch. See §15.3 of the spec.
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if implements := renderImplementedInterfaces(real, fa); implements != "" {
		result += "\n\n" + implements
	}
	if role := renderFunctionImplementationRole(real); role != "" {
		result += "\n\n" + role
	}
	if real.Opaque {
		result += "\n\n*opaque — construction surface is private to its defining module*"
	}
	// For an interface method (the contract rendering — generic / existential
	// receivers, where no concrete impl was statically resolved), surface which
	// interface contracts it, so a `T.to_string` hover tells you it dispatches
	// through `Display`.
	if real.Kind == analysis.SymbolInterfaceMethod && real.OwningInterface != "" {
		result += "\n\n*interface* `" + real.OwningInterface + "`"
	}
	// An application-field read (`MyApp.logger`): name the application.
	if sym.Kind == analysis.SymbolField && sym.AppFieldOf != nil {
		result += "\n\n*app field of* `" + displayValueType(sym.AppFieldOf) + "`"
	}
	if doc != "" {
		result += "\n\n" + doc
	}
	return result
}

func renderFunctionImplementationRole(sym *analysis.Symbol) string {
	if sym == nil || sym.Kind != analysis.SymbolFunction || sym.OwningType == "" {
		return ""
	}
	if sym.ImplInterface != "" {
		return "*impl* `" + sym.ImplInterface + "." + sym.Name + "`"
	}
	return "*type function on* `" + sym.OwningType + "`"
}

func renderImplementedInterfaces(sym *analysis.Symbol, fa *analysis.FileAnalysis) string {
	names := implementedInterfaceNames(sym, fa)
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = "`" + name + "`"
	}
	return "*impl* " + strings.Join(parts, ", ")
}

func implementedInterfaceNames(sym *analysis.Symbol, fa *analysis.FileAnalysis) []string {
	if sym == nil || fa == nil || !symbolCanImplementInterfaces(sym) {
		return nil
	}
	typeName := sym.Name
	seen := make(map[string]bool)
	collect := func(ifaces map[string]bool) {
		for iface := range ifaces {
			seen[iface] = true
		}
	}
	if fa.Impls != nil {
		collect(fa.Impls[typeName])
	}
	if fa.ProjectImpls != nil && fa.ProjectImpls.Impls != nil {
		collect(fa.ProjectImpls.Impls[typeName])
	}
	if seen["Debug"] && !hasExplicitDebugImpl(typeName, fa) {
		delete(seen, "Debug")
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func symbolCanImplementInterfaces(sym *analysis.Symbol) bool {
	switch sym.Kind {
	case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType:
		return true
	default:
		return false
	}
}

func hasExplicitDebugImpl(typeName string, fa *analysis.FileAnalysis) bool {
	if fa == nil {
		return false
	}
	if idx := fa.ProjectImpls; idx != nil {
		for _, fns := range idx.IfaceMethodImpls["Debug"] {
			for _, fn := range fns {
				if idx.ReceiverOf(fn) == typeName && !fn.AutoSynth {
					return true
				}
			}
		}
		for _, exts := range idx.IfaceMethodImplExterns["Debug"] {
			for _, ext := range exts {
				if idx.ImplBlockReceiverExtern[ext] == typeName {
					return true
				}
			}
		}
	}
	for _, fns := range fa.IfaceMethodImpls["Debug"] {
		for _, fn := range fns {
			if localImplReceiverOf(fa, fn) == typeName && !fn.AutoSynth {
				return true
			}
		}
	}
	for _, exts := range fa.IfaceMethodImplExterns["Debug"] {
		for _, ext := range exts {
			if fa.ImplBlockReceiverExtern[ext] == typeName {
				return true
			}
		}
	}
	return false
}

func localImplReceiverOf(fa *analysis.FileAnalysis, fn *ast.FuncDef) string {
	if fa != nil && fa.ImplBlockReceiver != nil {
		if receiver := fa.ImplBlockReceiver[fn]; receiver != "" {
			return receiver
		}
	}
	if fn == nil || len(fn.Params) == 0 {
		return ""
	}
	return analysis.TypeExprBaseName(fn.Params[0].TypeAnnotation)
}

// displayBindingType renders a binding's type, surfacing the original
// parameter names recorded for partial-application bindings (`half =
// divide(_, 2)` → `(x: Int) -> Int`). Falls back to displayValueType when
// the binding has no recorded names.
func displayBindingType(sym *analysis.Symbol) string {
	if len(sym.ParamNames) == 0 {
		return displayValueTypeWithReceiver(sym.Type, sym.ReceiverDisplay)
	}
	ft, ok := sym.Type.(*analysis.FuncType)
	if !ok || len(ft.Params) != len(sym.ParamNames) {
		return displayValueTypeWithReceiver(sym.Type, sym.ReceiverDisplay)
	}
	parts := make([]string, len(ft.Params))
	for i, p := range ft.Params {
		ty := displayValueTypeWithReceiver(p, sym.ReceiverDisplay)
		if name := sym.ParamNames[i]; name != "" {
			parts[i] = name + ": " + ty
		} else {
			parts[i] = ty
		}
	}
	ret := "Unit"
	if ft.Return != nil {
		ret = displayValueTypeWithReceiver(ft.Return, sym.ReceiverDisplay)
	}
	return "(" + strings.Join(parts, ", ") + ") -> " + ret
}

// displayValueType renders a type for value-position hover (binding,
// param, field). Unresolved TypeParam_/TypeVar get rendered as `_`
// rather than leaking the callee's type-param name (`E`, `T`, `?1`, …)
// or showing the synthetic TypeVar ID. Function/struct/enum DECLARATIONS
// keep using Type.String() — the param names there are intentional.
func displayValueType(t analysis.Type) string {
	return displayValueTypeWithReceiver(t, "")
}

func displayValueTypeWithReceiver(t analysis.Type, receiver string) string {
	if t == nil {
		return ""
	}
	switch v := t.(type) {
	case *analysis.TypeParam_:
		if receiver != "" && v.Name_ == "self" {
			return receiver
		}
		return "_"
	case *analysis.TypeVar:
		if v.Resolved != nil {
			return displayValueTypeWithReceiver(v.Resolved, receiver)
		}
		return "_"
	case *analysis.ListType:
		return "List<" + displayValueTypeWithReceiver(v.Elem, receiver) + ">"
	case *analysis.MapType:
		return "Map<" + displayValueTypeWithReceiver(v.Key, receiver) + ", " + displayValueTypeWithReceiver(v.Val, receiver) + ">"
	case *analysis.TupleType:
		parts := make([]string, len(v.Elems))
		for i, e := range v.Elems {
			parts[i] = displayValueTypeWithReceiver(e, receiver)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *analysis.FuncType:
		params := make([]string, len(v.Params))
		for i, p := range v.Params {
			if p == nil {
				params[i] = "?"
				continue
			}
			params[i] = displayValueTypeWithReceiver(p, receiver)
		}
		ret := "Unit"
		if v.Return != nil {
			ret = displayValueTypeWithReceiver(v.Return, receiver)
		}
		return "(" + strings.Join(params, ", ") + ") -> " + ret
	case *analysis.StructType:
		if len(v.TypeArgs) > 0 {
			args := make([]string, len(v.TypeArgs))
			for i, a := range v.TypeArgs {
				args[i] = displayValueTypeWithReceiver(a, receiver)
			}
			return v.Name + "<" + strings.Join(args, ", ") + ">"
		}
		return v.Name
	case *analysis.EnumType:
		if len(v.TypeArgs) > 0 {
			args := make([]string, len(v.TypeArgs))
			for i, a := range v.TypeArgs {
				args[i] = displayValueTypeWithReceiver(a, receiver)
			}
			return v.Name + "<" + strings.Join(args, ", ") + ">"
		}
		return v.Name
	case *analysis.InterfaceType:
		if len(v.TypeArgs) > 0 {
			args := make([]string, len(v.TypeArgs))
			for i, a := range v.TypeArgs {
				args[i] = displayValueTypeWithReceiver(a, receiver)
			}
			return v.Name + "<" + strings.Join(args, ", ") + ">"
		}
		return v.Name
	case *analysis.AnonStructType:
		parts := make([]string, len(v.Fields))
		for i, f := range v.Fields {
			parts[i] = f.Name + ": " + displayValueTypeWithReceiver(f.Type, receiver)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return t.String()
	}
}

func renderTypeParams(tps []ast.TypeParam) string {
	if len(tps) == 0 {
		return ""
	}
	parts := make([]string, len(tps))
	for i, tp := range tps {
		parts[i] = tp.Name
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func renderSyntheticWhereClausesFromTypeParams(tps []ast.TypeParam) string {
	var parts []string
	for _, tp := range tps {
		if len(tp.Bounds) == 0 {
			continue
		}
		bounds := make([]string, len(tp.Bounds))
		for i, bound := range tp.Bounds {
			bounds[i] = bound.TypeString()
		}
		parts = append(parts, tp.Name+": "+strings.Join(bounds, " and "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " where " + strings.Join(parts, ", ")
}

func renderTypeParamHover(sym *analysis.Symbol, fa *analysis.FileAnalysis) string {
	bounds := typeParamBoundDisplays(sym)
	whereBounds := typeParamWhereBoundDisplays(sym, fa)
	suffix := ""
	if len(bounds) > 0 {
		suffix = ": " + strings.Join(bounds, " and ")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\n<%s%s> type parameter\n```", sym.Name, suffix)
	fmt.Fprintf(&b, "\n\n`%s` is a generic type chosen by the caller. Every `%s` in this signature means that same chosen type.", sym.Name, sym.Name)
	relations := typeParamRelations(sym, fa)
	if len(bounds) == 0 && len(whereBounds) == 0 {
		b.WriteString(" It has no interface bounds, so this signature does not require specific operations on it.")
		if len(relations) == 0 {
			return b.String()
		}
	}

	describeTypeParamBounds(&b, sym.Name, "bound", bounds)
	describeTypeParamBounds(&b, sym.Name, "`where` clause", whereBounds)
	for _, bound := range append(append([]string{}, bounds...), whereBounds...) {
		if genericBound, arg := firstGenericBound([]string{bound}); genericBound != "" {
			fmt.Fprintf(&b, "\n\nA bound with type arguments relates `%s` to the type argument shown there. For example, `%s` means the chosen `%s` must implement `%s` with `%s` as the type argument.", sym.Name, genericBound, sym.Name, genericBoundName(genericBound), arg)
			appendTypeParamBoundExamples(&b, sym.Name, "", genericBound, arg)
			break
		}
	}
	for _, rel := range relations {
		fmt.Fprintf(&b, "\n\n`%s` is used as the type argument in `%s: %s`, so this signature can choose `%s` separately from `%s`.", sym.Name, rel.Owner, rel.Bound, sym.Name, rel.Owner)
		appendTypeParamBoundExamples(&b, rel.Owner, sym.Name, rel.Bound, sym.Name)
	}
	return b.String()
}

func describeTypeParamBounds(b *strings.Builder, name, source string, bounds []string) {
	if len(bounds) == 1 {
		fmt.Fprintf(b, "\n\nThe %s means `%s` can be any concrete type that implements `%s`.", source, name, bounds[0])
	} else if len(bounds) > 1 {
		fmt.Fprintf(b, "\n\nThe %s bounds mean `%s` can be any concrete type that implements all of:", source, name)
		for _, bound := range bounds {
			fmt.Fprintf(b, "\n- `%s`", bound)
		}
	}
}

func typeParamBoundDisplays(sym *analysis.Symbol) []string {
	if tp, ok := sym.Type.(*analysis.TypeParam_); ok {
		// Prefer the original textual form for synthetic/internal type params.
		// Source-level generic bounds live in `where` clauses and are rendered
		// by typeParamWhereBoundDisplays so the hover mirrors the syntax.
		switch {
		case len(tp.AsWritten) > 0:
			return append([]string(nil), tp.AsWritten...)
		case len(tp.Bounds) > 0:
			names := make([]string, len(tp.Bounds))
			for i, b := range tp.Bounds {
				names[i] = b.Name
			}
			return names
		}
	}
	return nil
}

func typeParamWhereBoundDisplays(sym *analysis.Symbol, fa *analysis.FileAnalysis) []string {
	_, clauses := findTypeParamContext(sym, fa)
	var out []string
	for _, wc := range clauses {
		if wc.Name != sym.Name {
			continue
		}
		for _, bound := range wc.Bounds {
			out = append(out, bound.TypeString())
		}
	}
	return out
}

func firstGenericBound(bounds []string) (string, string) {
	for _, bound := range bounds {
		start := strings.Index(bound, "<")
		end := strings.LastIndex(bound, ">")
		if start >= 0 && end > start {
			return bound, bound[start+1 : end]
		}
	}
	return "", ""
}

func genericBoundName(bound string) string {
	if start := strings.Index(bound, "<"); start > 0 {
		return bound[:start]
	}
	return bound
}

type typeParamRelation struct {
	Owner string
	Bound string
}

func typeParamRelations(sym *analysis.Symbol, fa *analysis.FileAnalysis) []typeParamRelation {
	if sym == nil || fa == nil {
		return nil
	}
	tps, clauses := findTypeParamContext(sym, fa)
	if len(tps) == 0 {
		return nil
	}
	var out []typeParamRelation
	for _, tp := range tps {
		if tp.Name == sym.Name {
			continue
		}
		for _, bound := range tp.Bounds {
			if typeExprContainsSimpleName(bound, sym.Name) {
				out = append(out, typeParamRelation{
					Owner: tp.Name,
					Bound: bound.TypeString(),
				})
			}
		}
	}
	for _, wc := range clauses {
		if wc.Name == sym.Name {
			continue
		}
		for _, bound := range wc.Bounds {
			if typeExprContainsSimpleName(bound, sym.Name) {
				out = append(out, typeParamRelation{
					Owner: wc.Name,
					Bound: bound.TypeString(),
				})
			}
		}
	}
	return out
}

func findTypeParamContext(sym *analysis.Symbol, fa *analysis.FileAnalysis) ([]ast.TypeParam, []ast.WhereConstraint) {
	if sym == nil || fa == nil {
		return nil, nil
	}
	for _, def := range fa.Definitions {
		if def == nil {
			continue
		}
		var tps []ast.TypeParam
		var where []ast.WhereConstraint
		switch n := def.Node.(type) {
		case *ast.FuncDef:
			tps = n.TypeParams
			where = n.WhereClauses
		case *ast.ExternFunc:
			tps = n.TypeParams
			where = n.WhereClauses
		case *ast.StructDef:
			tps = n.TypeParams
			where = n.WhereClauses
		case *ast.EnumDef:
			tps = n.TypeParams
			where = n.WhereClauses
		case *ast.ExternType:
			tps = n.TypeParams
			where = n.WhereClauses
		case *ast.InterfaceDef:
			tps = n.TypeParams
			where = n.WhereClauses
		case *ast.ImplBlock:
			tps = n.Generics
			where = n.WhereClauses
		}
		for _, tp := range tps {
			if tp.Name == sym.Name && tp.Line == sym.Pos.Line && tp.Col == sym.Pos.Col {
				return tps, where
			}
		}
	}
	return nil, nil
}

func typeExprContainsSimpleName(expr ast.TypeExpr, name string) bool {
	switch n := expr.(type) {
	case *ast.SimpleType:
		return n.Name == name
	case *ast.QualifiedType:
		return typeExprContainsSimpleName(n.Member, name)
	case *ast.GenericType:
		for _, p := range n.Params {
			if typeExprContainsSimpleName(p, name) {
				return true
			}
		}
	case *ast.FuncType:
		for _, p := range n.Params {
			if typeExprContainsSimpleName(p, name) {
				return true
			}
		}
		return n.Return != nil && typeExprContainsSimpleName(n.Return, name)
	case *ast.AnonStructType:
		for _, f := range n.Fields {
			if typeExprContainsSimpleName(f.TypeAnnotation, name) {
				return true
			}
		}
	}
	return false
}

func appendTypeParamBoundExamples(b *strings.Builder, owner, arg, bound, relatedParam string) {
	if genericBoundName(bound) != "Steppable" {
		return
	}
	if arg == "" {
		arg = relatedParam
	}
	if arg == "" {
		return
	}
	fmt.Fprintf(b, "\n\nExamples:\n- `%s = Int`, `%s = Int`: an integer range stepped by integers.\n- `%s = Date`, `%s = Duration`: a date range stepped by durations.\n\n`%s` does not have to be the same type as `%s`; it only has to be the type used by a matching `%s` implementation.", owner, arg, owner, arg, arg, owner, bound)
}

func RenderFuncSig(name string, typeParams []ast.TypeParam, params []ast.Param, ret ast.TypeExpr) string {
	return RenderFuncSigResolvedSelf(name, typeParams, params, ret, nil, nil, "")
}

func RenderFuncSigResolvedSelf(name string, typeParams []ast.TypeParam, params []ast.Param, ret ast.TypeExpr, resolved analysis.Type, receiver analysis.Type, receiverName string) string {
	var b strings.Builder
	b.WriteString("fn ")
	b.WriteString(name)
	b.WriteString(renderTypeParams(typeParams))

	var ft *analysis.FuncType
	if resolvedFT, ok := resolved.(*analysis.FuncType); ok {
		ft = resolvedFT
	}

	b.WriteString("(")
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(ParamDisplayName(p))
		if p.TypeAnnotation != nil {
			b.WriteString(": ")
			b.WriteString(renderTypeExprSelfAware(p.TypeAnnotation, typeAt(ft, i), receiver, receiverName))
		}
		if def := renderConstValue(p.Default); def != "" {
			b.WriteString(" = ")
			b.WriteString(def)
		}
	}
	b.WriteString(")")

	if ret != nil {
		b.WriteString(": ")
		var retTy analysis.Type
		if ft != nil {
			retTy = ft.Return
		}
		b.WriteString(renderTypeExprSelfAware(ret, retTy, receiver, receiverName))
	}
	b.WriteString(renderSyntheticWhereClausesFromTypeParams(typeParams))

	return b.String()
}

func typeAt(ft *analysis.FuncType, i int) analysis.Type {
	if ft == nil || i < 0 || i >= len(ft.Params) {
		return nil
	}
	return ft.Params[i]
}

func renderTypeExprSelfAware(te ast.TypeExpr, resolved analysis.Type, receiver analysis.Type, receiverName string) string {
	if !typeExprContainsSelf(te) {
		return te.TypeString()
	}
	if receiverName != "" && typeExprIsSelf(te) {
		return receiverName
	}
	if resolved != nil {
		return displayValueTypeWithReceiver(resolved, receiverName)
	}
	if receiver != nil {
		return renderTypeExprSubstitutingSelf(te, displayValueType(receiver))
	}
	if receiverName != "" {
		return renderTypeExprSubstitutingSelf(te, receiverName)
	}
	return te.TypeString()
}

func typeExprContainsSelf(te ast.TypeExpr) bool {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name == "self"
	case *ast.SelfType:
		return true
	case *ast.QualifiedType:
		return typeExprContainsSelf(t.Member)
	case *ast.GenericType:
		for _, p := range t.Params {
			if typeExprContainsSelf(p) {
				return true
			}
		}
		return false
	case *ast.FuncType:
		for _, p := range t.Params {
			if typeExprContainsSelf(p) {
				return true
			}
		}
		return t.Return != nil && typeExprContainsSelf(t.Return)
	default:
		return false
	}
}

func typeExprIsSelf(te ast.TypeExpr) bool {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name == "self"
	case *ast.SelfType:
		return true
	default:
		return false
	}
}

func renderTypeExprSubstitutingSelf(te ast.TypeExpr, receiver string) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		if t.Name == "self" {
			return receiver
		}
		return t.TypeString()
	case *ast.SelfType:
		return receiver
	case *ast.QualifiedType:
		return t.Module + "." + renderTypeExprSubstitutingSelf(t.Member, receiver)
	case *ast.GenericType:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = renderTypeExprSubstitutingSelf(p, receiver)
		}
		return t.Name + "<" + strings.Join(parts, ", ") + ">"
	case *ast.FuncType:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = renderTypeExprSubstitutingSelf(p, receiver)
		}
		out := "(" + strings.Join(parts, ", ") + ")"
		if t.Return != nil {
			out += " -> " + renderTypeExprSubstitutingSelf(t.Return, receiver)
		}
		return out
	default:
		return te.TypeString()
	}
}

// ParamDisplayName renders a parameter's surface name for a signature: the
// destructuring pattern (`Dur(x)`, `Point{x, y}`) when the param destructures,
// otherwise the plain name. Destructure params carry a synthetic `__destr_*`
// Name (a binding slot) that must never reach hover.
func ParamDisplayName(p ast.Param) string {
	if p.Destructure != nil {
		return renderParamPattern(p.Destructure)
	}
	return p.Name
}

// renderParamPattern stringifies an irrefutable destructuring pattern back to
// its source form for hover. Params accept only the irrefutable subset (ident,
// wildcard, distinct/enum, struct, tuple); anything else renders empty.
func renderParamPattern(n ast.Node) string {
	switch p := n.(type) {
	case *ast.IdentPattern:
		return p.Name
	case *ast.WildcardPattern:
		return "_"
	case *ast.EnumPattern:
		// `Dur(x)`, `Wrapper.Only(n)`, `Foo((a, b))`. A flat tuple payload
		// (`Foo(a, b)`) renders its elements directly inside the `()` the
		// enum supplies — renderParamPattern of a flat TuplePattern omits its
		// own parens.
		s := p.Variant.TypeString()
		switch {
		case p.Payload != nil:
			s += "(" + renderParamPattern(p.Payload) + ")"
		case p.Binding != "":
			s += "(" + p.Binding + ")"
		}
		return s
	case *ast.StructPattern:
		var b strings.Builder
		if p.TypeName != nil {
			b.WriteString(p.TypeName.TypeString())
		}
		b.WriteString("{")
		for i, f := range p.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			switch {
			case f.Pattern != nil:
				b.WriteString(f.Name + ": " + renderParamPattern(f.Pattern))
			case f.Binding != "" && f.Binding != f.Name:
				b.WriteString(f.Name + ": " + f.Binding)
			default:
				b.WriteString(f.Name)
			}
		}
		b.WriteString("}")
		return b.String()
	case *ast.TuplePattern:
		parts := make([]string, len(p.Patterns))
		for i, el := range p.Patterns {
			parts[i] = renderParamPattern(el)
		}
		inner := strings.Join(parts, ", ")
		if p.Flat {
			// Caller (EnumPattern) supplies the surrounding parens.
			return inner
		}
		return "(" + inner + ")"
	default:
		return ""
	}
}

// isStructOrEnumDecl reports whether the symbol is a struct or enum
// declaration — the receiver kinds whose hover card is worth appending to
// a `self` hover (they have a body, doc, and possible opacity to show).
func isStructOrEnumDecl(sym *analysis.Symbol) bool {
	switch sym.Node.(type) {
	case *ast.StructDef, *ast.EnumDef:
		return true
	default:
		return false
	}
}

func renderStructDef(n *ast.StructDef) string {
	// Opaque structs hide their construction surface — render the
	// declaration head only (no field body), the same abstraction the
	// `*opaque*` note describes. Applies everywhere (the renderer is not
	// module-aware); an author editing the defining module sees the fields
	// in the source buffer regardless.
	if n.Opaque {
		return "struct " + n.Name + renderTypeParams(n.TypeParams) + renderWhereClauses(n.WhereClauses)
	}
	var b strings.Builder
	b.WriteString("struct ")
	b.WriteString(n.Name)
	b.WriteString(renderTypeParams(n.TypeParams))
	b.WriteString(renderWhereClauses(n.WhereClauses))
	b.WriteString(" {\n")
	for _, f := range n.Fields {
		b.WriteString("    ")
		b.WriteString(f.Name)
		if f.TypeAnnotation != nil {
			b.WriteString(": ")
			b.WriteString(f.TypeAnnotation.TypeString())
		}
		b.WriteString("\n")
	}
	b.WriteString("}")
	return b.String()
}

func renderEnumDef(n *ast.EnumDef) string {
	// Opaque enums hide their variants for the same reason opaque structs
	// hide their fields — render the declaration head only.
	if n.Opaque {
		return "enum " + n.Name + renderTypeParams(n.TypeParams) + renderWhereClauses(n.WhereClauses)
	}
	var b strings.Builder
	b.WriteString("enum ")
	b.WriteString(n.Name)
	b.WriteString(renderTypeParams(n.TypeParams))
	b.WriteString(renderWhereClauses(n.WhereClauses))
	b.WriteString(" {\n")
	for _, v := range n.Variants {
		b.WriteString("    ")
		switch v.Kind {
		case "embedded":
			b.WriteString("embeds ")
			if v.EmbeddedTypeExpr != nil {
				b.WriteString(v.EmbeddedTypeExpr.TypeString())
			} else {
				b.WriteString(v.Name)
			}
		case "positional":
			b.WriteString(v.Name)
			if v.DataTypeExpr != nil {
				b.WriteString(" ")
				b.WriteString(v.DataTypeExpr.TypeString())
			}
		case "struct":
			b.WriteString(v.Name)
			b.WriteString(" { ")
			for i, f := range v.Fields {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(f.Name)
				if f.TypeAnnotation != nil {
					b.WriteString(": ")
					b.WriteString(f.TypeAnnotation.TypeString())
				}
			}
			b.WriteString(" }")
		default:
			// bare
			b.WriteString(v.Name)
		}
		b.WriteString("\n")
	}
	b.WriteString("}")
	return b.String()
}

func findEnumVariant(enum *ast.EnumDef, sym *analysis.Symbol) (ast.EnumVariant, bool) {
	if enum == nil || sym == nil {
		return ast.EnumVariant{}, false
	}
	for _, v := range enum.Variants {
		if v.Name == sym.Name && (v.Line == sym.Pos.Line || sym.Pos.Line == 0) {
			return v, true
		}
	}
	for _, v := range enum.Variants {
		if v.Name == sym.Name {
			return v, true
		}
	}
	return ast.EnumVariant{}, false
}

func renderEnumVariantDecl(v ast.EnumVariant) string {
	var b strings.Builder
	b.WriteString("variant ")
	switch v.Kind {
	case "embedded":
		b.WriteString("embeds ")
		if v.EmbeddedTypeExpr != nil {
			b.WriteString(v.EmbeddedTypeExpr.TypeString())
		} else {
			b.WriteString(v.Name)
		}
	case "positional":
		b.WriteString(v.Name)
		if v.DataTypeExpr != nil {
			b.WriteString(" ")
			b.WriteString(v.DataTypeExpr.TypeString())
		}
	case "struct":
		b.WriteString(v.Name)
		b.WriteString(" {")
		for i, f := range v.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
			if f.TypeAnnotation != nil {
				b.WriteString(": ")
				b.WriteString(f.TypeAnnotation.TypeString())
			}
		}
		b.WriteString("}")
	default:
		b.WriteString(v.Name)
	}
	return b.String()
}

func findParamType(sym *analysis.Symbol) ast.TypeExpr {
	switch fn := sym.Node.(type) {
	case *ast.FuncDef:
		for _, p := range fn.Params {
			if p.Name == sym.Name && p.TypeAnnotation != nil {
				return p.TypeAnnotation
			}
		}
	case *ast.ExternFunc:
		for _, p := range fn.Params {
			if p.Name == sym.Name && p.TypeAnnotation != nil {
				return p.TypeAnnotation
			}
		}
	case *ast.InterfaceMethod:
		for _, p := range fn.Params {
			if p.Name == sym.Name && p.TypeAnnotation != nil {
				return p.TypeAnnotation
			}
		}
	}
	return nil
}

func RenderInterfaceMethodSig(m *ast.InterfaceMethod) string {
	var b strings.Builder
	b.WriteString(m.Name)
	b.WriteString(renderTypeParams(m.TypeParams))
	b.WriteString("(")
	for i, p := range m.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(ParamDisplayName(p))
		if p.TypeAnnotation != nil {
			b.WriteString(": ")
			b.WriteString(p.TypeAnnotation.TypeString())
		}
	}
	b.WriteString(")")
	if m.ReturnTypeExpr != nil {
		b.WriteString(": ")
		b.WriteString(m.ReturnTypeExpr.TypeString())
	}
	if len(m.WhereClauses) > 0 {
		b.WriteString(" where ")
		for i, wc := range m.WhereClauses {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(wc.Name)
			b.WriteString(": ")
			for j, bound := range wc.Bounds {
				if j > 0 {
					b.WriteString(" and ")
				}
				b.WriteString(bound.TypeString())
			}
		}
	} else {
		b.WriteString(renderSyntheticWhereClausesFromTypeParams(m.TypeParams))
	}
	return b.String()
}

// renderFuncSigFromType renders a function-shaped signature (regular fn or
// variant constructor) using instantiated types and parameter names from the
// definition AST node. `prefix` selects the keyword (`"fn "` for functions,
// `"variant "` for enum-variant constructors at call sites).
func renderFuncSigFromType(prefix, name string, ft *analysis.FuncType, defNode ast.Node, receiverName string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(name)
	b.WriteString("(")

	// Get param names from definition.
	var paramNames []string
	switch n := defNode.(type) {
	case *ast.FuncDef:
		for _, p := range n.Params {
			paramNames = append(paramNames, ParamDisplayName(p))
		}
	case *ast.ExternFunc:
		for _, p := range n.Params {
			paramNames = append(paramNames, ParamDisplayName(p))
		}
	}

	for i, p := range ft.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		if i < len(paramNames) {
			b.WriteString(paramNames[i])
			b.WriteString(": ")
		}
		// displayValueType replaces unresolved TypeParam_/TypeVar with `_`
		// so call-site signatures don't leak `?1` or stale param names.
		if typeExprContainsSelf(paramTypeExprAt(defNode, i)) && receiverName != "" {
			b.WriteString(renderTypeExprSubstitutingSelf(paramTypeExprAt(defNode, i), receiverName))
		} else {
			b.WriteString(displayValueType(p))
		}
	}
	b.WriteString(")")
	if ft.Return != nil {
		b.WriteString(": ")
		if ret := returnTypeExprOf(defNode); ret != nil && typeExprContainsSelf(ret) && receiverName != "" {
			b.WriteString(renderTypeExprSubstitutingSelf(ret, receiverName))
		} else {
			b.WriteString(displayValueType(ft.Return))
		}
	}
	b.WriteString(renderWhereClausesForCallType(defNode, ft))
	return b.String()
}

func renderWhereClausesForNode(node ast.Node) string {
	switch n := node.(type) {
	case *ast.FuncDef:
		return renderWhereClauses(n.WhereClauses)
	case *ast.ExternFunc:
		return renderWhereClauses(n.WhereClauses)
	default:
		return ""
	}
}

func renderWhereClauses(clauses []ast.WhereConstraint) string {
	if len(clauses) == 0 {
		return ""
	}
	parts := make([]string, len(clauses))
	for i, wc := range clauses {
		bounds := make([]string, len(wc.Bounds))
		for j, bound := range wc.Bounds {
			bounds[j] = bound.TypeString()
		}
		parts[i] = wc.Name + ": " + strings.Join(bounds, " and ")
	}
	return " where " + strings.Join(parts, ", ")
}

func renderWhereClausesForCallType(node ast.Node, ft *analysis.FuncType) string {
	clauses := whereClausesOfNode(node)
	if len(clauses) == 0 || ft == nil {
		return renderWhereClauses(clauses)
	}
	subs := inferCallTypeParamDisplaySubs(node, ft)
	if len(subs) == 0 {
		return renderWhereClauses(clauses)
	}
	parts := make([]string, len(clauses))
	for i, wc := range clauses {
		name := wc.Name
		if sub, ok := subs[name]; ok {
			name = sub
		}
		bounds := make([]string, len(wc.Bounds))
		for j, bound := range wc.Bounds {
			bounds[j] = renderTypeExprWithDisplaySubs(bound, subs)
		}
		parts[i] = name + ": " + strings.Join(bounds, " and ")
	}
	return " where " + strings.Join(parts, ", ")
}

func whereClausesOfNode(node ast.Node) []ast.WhereConstraint {
	switch n := node.(type) {
	case *ast.FuncDef:
		return n.WhereClauses
	case *ast.ExternFunc:
		return n.WhereClauses
	default:
		return nil
	}
}

func inferCallTypeParamDisplaySubs(node ast.Node, ft *analysis.FuncType) map[string]string {
	subs := map[string]string{}
	switch n := node.(type) {
	case *ast.FuncDef:
		typeParamNames := typeParamNameSet(n.TypeParams)
		for i, p := range n.Params {
			if i < len(ft.Params) {
				inferTypeExprDisplaySubs(p.TypeAnnotation, ft.Params[i], typeParamNames, subs)
			}
		}
		inferTypeExprDisplaySubs(n.ReturnTypeExpr, ft.Return, typeParamNames, subs)
	case *ast.ExternFunc:
		typeParamNames := typeParamNameSet(n.TypeParams)
		for i, p := range n.Params {
			if i < len(ft.Params) {
				inferTypeExprDisplaySubs(p.TypeAnnotation, ft.Params[i], typeParamNames, subs)
			}
		}
		inferTypeExprDisplaySubs(n.ReturnTypeExpr, ft.Return, typeParamNames, subs)
	}
	return subs
}

func typeParamNameSet(tps []ast.TypeParam) map[string]bool {
	if len(tps) == 0 {
		return nil
	}
	out := make(map[string]bool, len(tps))
	for _, tp := range tps {
		out[tp.Name] = true
	}
	return out
}

func inferTypeExprDisplaySubs(te ast.TypeExpr, ty analysis.Type, typeParamNames map[string]bool, subs map[string]string) {
	if te == nil || ty == nil {
		return
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		if typeParamNames[t.Name] {
			if display := displayValueType(ty); display != "" && display != "_" {
				subs[t.Name] = display
			}
		}
	case *ast.GenericType:
		typeArgs := typeArgsOfGenericExpr(t, ty)
		for i, param := range t.Params {
			if i < len(typeArgs) {
				inferTypeExprDisplaySubs(param, typeArgs[i], typeParamNames, subs)
			}
		}
	case *ast.QualifiedType:
		inferTypeExprDisplaySubs(t.Member, ty, typeParamNames, subs)
	case *ast.FuncType:
		if ft, ok := ty.(*analysis.FuncType); ok {
			for i, param := range t.Params {
				if i < len(ft.Params) {
					inferTypeExprDisplaySubs(param, ft.Params[i], typeParamNames, subs)
				}
			}
			inferTypeExprDisplaySubs(t.Return, ft.Return, typeParamNames, subs)
		}
	}
}

func typeArgsOf(ty analysis.Type) []analysis.Type {
	switch t := ty.(type) {
	case *analysis.StructType:
		return t.TypeArgs
	case *analysis.EnumType:
		return t.TypeArgs
	case *analysis.DistinctType:
		return t.TypeArgs
	case *analysis.InterfaceType:
		return t.TypeArgs
	default:
		return nil
	}
}

func typeArgsOfGenericExpr(gt *ast.GenericType, ty analysis.Type) []analysis.Type {
	switch t := ty.(type) {
	case *analysis.ListType:
		if gt.Name == "List" {
			return []analysis.Type{t.Elem}
		}
	case *analysis.MapType:
		if gt.Name == "Map" {
			return []analysis.Type{t.Key, t.Val}
		}
	}
	return typeArgsOf(ty)
}

func renderTypeExprWithDisplaySubs(te ast.TypeExpr, subs map[string]string) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		if sub, ok := subs[t.Name]; ok {
			return sub
		}
		return t.TypeString()
	case *ast.QualifiedType:
		return t.Module + "." + renderTypeExprWithDisplaySubs(t.Member, subs)
	case *ast.GenericType:
		parts := make([]string, len(t.Params))
		for i, param := range t.Params {
			parts[i] = renderTypeExprWithDisplaySubs(param, subs)
		}
		return t.Name + "<" + strings.Join(parts, ", ") + ">"
	case *ast.FuncType:
		parts := make([]string, len(t.Params))
		for i, param := range t.Params {
			parts[i] = renderTypeExprWithDisplaySubs(param, subs)
		}
		out := "(" + strings.Join(parts, ", ") + ")"
		if t.Return != nil {
			out += " -> " + renderTypeExprWithDisplaySubs(t.Return, subs)
		}
		return out
	default:
		if te == nil {
			return ""
		}
		return te.TypeString()
	}
}

func paramTypeExprAt(defNode ast.Node, i int) ast.TypeExpr {
	switch n := defNode.(type) {
	case *ast.FuncDef:
		if i >= 0 && i < len(n.Params) {
			return n.Params[i].TypeAnnotation
		}
	case *ast.ExternFunc:
		if i >= 0 && i < len(n.Params) {
			return n.Params[i].TypeAnnotation
		}
	}
	return nil
}

func returnTypeExprOf(defNode ast.Node) ast.TypeExpr {
	switch n := defNode.(type) {
	case *ast.FuncDef:
		return n.ReturnTypeExpr
	case *ast.ExternFunc:
		return n.ReturnTypeExpr
	}
	return nil
}

// docFromNode extracts the Doc string from a definition AST node.
func docFromNode(node ast.Node) string {
	switch n := node.(type) {
	case *ast.FuncDef:
		return n.Doc
	case *ast.ExternFunc:
		return n.Doc
	default:
		return ""
	}
}

func renderAssertionHover(sym *analysis.Symbol) string {
	if sym.Assertion == nil {
		return fmt.Sprintf("```nomi\n%s\n```", sym.Name)
	}
	if _, ok := sym.Node.(*ast.PatternDestructure); ok {
		return renderPatternAssertionHover(sym)
	}
	info := sym.Assertion
	inputStr := displayValueType(info.InputTy)
	resultStr := displayValueType(info.ResultTy)

	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\n%s: %s -> %s\n```", info.Keyword, inputStr, resultStr)
	if prose := assertionHoverProse(info); prose != "" {
		b.WriteString("\n\n")
		b.WriteString(prose)
	}
	return b.String()
}

func renderPatternAssertionHover(sym *analysis.Symbol) string {
	info := sym.Assertion
	inputStr := displayValueType(info.InputTy)
	resultStr := displayValueType(info.ResultTy)

	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\nassert: %s -> %s\n```", inputStr, resultStr)
	b.WriteString("\n\nMatches the value against the left-hand pattern")
	if info.Boundary != "" {
		fmt.Fprintf(&b, ". On mismatch, returns `AssertionFailure` and unwinds to `%s`.", info.Boundary)
	} else {
		b.WriteString(". On mismatch, returns `AssertionFailure`.")
	}
	return b.String()
}

func assertionHoverProse(info *analysis.AssertionInfo) string {
	success, failure := assertionBranchPhrases(info)
	failureText := assertionFailureText(info)

	if info.Keyword == "check" {
		if failure == "" {
			if success == "" {
				return "Returns `Result.Err(AssertionFailure)` on failure."
			}
			return success + "; otherwise returns `Result.Err(AssertionFailure)`."
		}
		return success + ". On `" + failure + "`, returns `Result.Err(AssertionFailure)`."
	}

	if success == "" {
		return failureText + "."
	}
	if failure == "" {
		return success + "; otherwise " + failureText + "."
	}
	return success + ". On `" + failure + "`, " + failureText + "."
}

func assertionFailureText(info *analysis.AssertionInfo) string {
	text := "returns `AssertionFailure`"
	if info.Boundary != "" {
		text += fmt.Sprintf(" and unwinds to `%s`", info.Boundary)
	}
	return text
}

func assertionBranchPhrases(info *analysis.AssertionInfo) (success, failure string) {
	if info.InputTy == nil {
		return "", ""
	}
	if isBoolType(info.InputTy) {
		if info.Keyword == "refute" {
			return "Requires `False`", "True"
		}
		return "Requires `True`", "False"
	}
	if et, ok := info.InputTy.(*analysis.EnumType); ok {
		switch et.Name {
		case "Result":
			if len(et.TypeArgs) >= 2 {
				if info.Keyword == "refute" {
					return "Requires `Err(_)`", "Ok: " + displayValueType(et.TypeArgs[0])
				}
				return "Requires `Ok(_)`", "Err: " + displayValueType(et.TypeArgs[1])
			}
		case "Maybe":
			if len(et.TypeArgs) >= 1 {
				if info.Keyword == "refute" {
					return "Requires `None`", "Some: " + displayValueType(et.TypeArgs[0])
				}
				return "Requires `Some(_)`", "None"
			}
		}
	}
	return "Requires `Assertable.failure(...)` to return `None`", "Assertable.failure(...) returns Some"
}

func isBoolType(ty analysis.Type) bool {
	if ty == nil {
		return false
	}
	if ty == analysis.TypeBool {
		return true
	}
	if p, ok := ty.(*analysis.PrimitiveType); ok {
		return p.Name_ == "Bool"
	}
	return displayValueType(ty) == "Bool"
}

func renderTestSetupHover(sym *analysis.Symbol) string {
	if sym.TestSetup == nil {
		return "```nomi\nsetup\n```"
	}
	info := sym.TestSetup
	inputStr := displayValueType(info.InputTy)
	outputStr := displayValueType(info.OutputTy)

	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\nsetup: %s -> %s\n```", inputStr, outputStr)
	b.WriteString("\n\nRuns before each test")
	if info.Group != "" {
		fmt.Fprintf(&b, " in `tests %q`", info.Group)
	}
	b.WriteString(", after the group's boot. A test binds the returned value with a pattern after its name.")
	return b.String()
}

func renderTestDeclHover(sym *analysis.Symbol) string {
	if sym.TestDecl == nil {
		return fmt.Sprintf("```nomi\n%s\n```", sym.Name)
	}
	info := sym.TestDecl
	inputStr := displayValueType(info.InputTy)
	outputStr := displayValueType(info.OutputTy)

	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\n%s: %s -> %s\n```", info.Keyword, inputStr, outputStr)
	b.WriteString("\n\n")
	if info.Group {
		fmt.Fprintf(&b, "Groups tests under `tests %q`.", info.Name)
		if info.HasSetup {
			b.WriteString(" Its setup runs before each test and returns the value a test's pattern binds.")
		}
		return b.String()
	}

	fmt.Fprintf(&b, "Runs only under `nomi test` as `test %q`.", info.Name)
	if info.HasContext {
		b.WriteString(" Binds its group's setup value through its pattern.")
	} else {
		b.WriteString(" Does not bind a setup value.")
	}
	b.WriteString(" Assertions unwind to this test.")
	return b.String()
}

func renderControlFlowHover(sym *analysis.Symbol) string {
	if sym.ControlFlow == nil {
		return fmt.Sprintf("```nomi\n%s\n```", sym.Name)
	}
	info := sym.ControlFlow
	outputStr := displayValueType(info.OutputTy)
	if outputStr == "" {
		outputStr = "T"
	}

	var inputStr string
	switch {
	case info.HasInput && info.InputTy != nil:
		inputStr = displayValueType(info.InputTy)
	case info.Keyword == "if":
		inputStr = "Bool"
	case info.Keyword == "case":
		inputStr = "patterns"
	default:
		inputStr = "value"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\n%s: %s -> %s\n```", info.Keyword, inputStr, outputStr)
	b.WriteString("\n\n")
	switch info.Keyword {
	case "if":
		if info.PatternInput {
			b.WriteString("Matches the input value against the condition pattern. On mismatch, runs `else`; without `else`, the expression returns `Unit`.")
		} else {
			b.WriteString("Branches on a `Bool`. With `else`, both branches must produce compatible values; without `else`, the expression returns `Unit`.")
		}
	case "case":
		if info.HasInput {
			b.WriteString("Matches the input value against patterns. Branch bodies must produce compatible values.")
		} else {
			b.WriteString("Checks branch guards in order. Branch bodies must produce compatible values.")
		}
	default:
		b.WriteString("Expression keyword.")
	}
	return b.String()
}

func renderImplHover(sym *analysis.Symbol) string {
	if sym.Impl == nil {
		return "```nomi\nimpl\n```"
	}
	info := sym.Impl
	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\n%s\n```", renderImplSignature(sym, info))
	if prose := renderImplProse(info); prose != "" {
		b.WriteString("\n\n")
		b.WriteString(prose)
	}
	return b.String()
}

func renderImplSignature(sym *analysis.Symbol, info *analysis.ImplInfo) string {
	if info.FunctionName != "" {
		name := info.FunctionName
		if info.SourceQualified && len(info.Interfaces) > 0 && info.Interfaces[0] != "" {
			name = info.Interfaces[0] + "." + name
		}
		switch n := sym.Node.(type) {
		case *ast.FuncDef:
			return RenderFuncSigResolvedSelf(name, n.TypeParams, n.Params, n.ReturnTypeExpr, sym.Type, nil, info.Receiver)
		case *ast.ExternFunc:
			return "extern " + RenderFuncSigResolvedSelf(name, n.TypeParams, n.Params, n.ReturnTypeExpr, sym.Type, nil, info.Receiver)
		}
		if info.Extern {
			return "host fn " + name
		}
		return "fn " + name
	}

	if info.Inherent {
		if info.Receiver != "" {
			return "impl " + info.Receiver + info.WhereClause
		}
		return "impl"
	}

	sig := "impl" + info.GenericHeader
	if len(info.Interfaces) > 0 {
		sig += " " + strings.Join(info.Interfaces, ", ")
	}
	if info.Receiver != "" {
		sig += " for " + info.Receiver
	}
	sig += info.WhereClause
	return sig
}

func renderImplProse(info *analysis.ImplInfo) string {
	if info.FunctionName != "" {
		if len(info.Interfaces) > 0 && info.Interfaces[0] != "" && info.Receiver != "" {
			return fmt.Sprintf("Implementation function for %s on `%s`. Callable through the implementing type or interface qualifier; visibility comes from %s.", markdownNameList(info.Interfaces[:1]), info.Receiver, markdownNameList(info.Interfaces[:1]))
		}
		if len(info.Interfaces) > 0 && info.Interfaces[0] != "" {
			return fmt.Sprintf("Implementation function for %s. Its visibility comes from the implemented interface.", markdownNameList(info.Interfaces[:1]))
		}
		return "Implementation function. Its visibility comes from the implemented interface."
	}

	if info.Inherent {
		if info.Receiver != "" {
			return fmt.Sprintf("Opens `%s` for type-qualified functions.", info.Receiver)
		}
		return "Opens a type for type-qualified functions."
	}

	names := markdownNameList(info.Interfaces)
	if info.SourceBlock {
		if info.Receiver != "" {
			return fmt.Sprintf("Implements %s for `%s`. Manual interface implementations use this block form.", names, info.Receiver)
		}
		return fmt.Sprintf("Implements %s in a top-level impl block.", names)
	}
	if info.Receiver != "" {
		return fmt.Sprintf("Declares that `%s` implements %s. Manual interface implementations use `impl Iface for Type { ... }` blocks; visibility comes from the interface.", info.Receiver, names)
	}
	return fmt.Sprintf("Declares interface conformance for %s.", names)
}

func markdownNameList(names []string) string {
	var quoted []string
	for _, name := range names {
		if name == "" {
			continue
		}
		quoted = append(quoted, "`"+name+"`")
	}
	switch len(quoted) {
	case 0:
		return "the interface"
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
	}
}

// renderTryOpHover formats the full markdown body for a SymbolTryOp
// marker. Layout matches the rest of the hover system: one code-fenced
// "signature" line describing the unwrap as a type arrow, plus one
// prose line naming the propagated branch (Err payload or None) and
// the boundary the `try` unwinds to. Returning the whole markdown here —
// rather than going through the caller's single-fence wrapper — keeps
// the prose outside the nomi fence so labels don't get
// syntax-highlighted as identifiers.
//
// Defensive fallback when TryOp metadata is missing: emit a bare
// fenced `try` (SymbolTryOp markers are always registered with metadata,
// but rendering shouldn't panic if that ever stops being true).
func renderTryOpHover(sym *analysis.Symbol) string {
	if sym.TryOp == nil {
		return "```nomi\ntry\n```"
	}
	inputStr := displayValueType(sym.TryOp.InputTy)
	successStr := inputStr // unrecognised input — show the raw type
	branch := ""
	if et, ok := sym.TryOp.InputTy.(*analysis.EnumType); ok {
		switch {
		case et.Name == "Result" && len(et.TypeArgs) == 2:
			successStr = displayValueType(et.TypeArgs[0])
			branch = "Err: " + displayValueType(et.TypeArgs[1])
		case et.Name == "Maybe" && len(et.TypeArgs) == 1:
			successStr = displayValueType(et.TypeArgs[0])
			branch = "None"
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\ntry: %s -> %s\n```", inputStr, successStr)
	if branch != "" || sym.TryOp.Boundary != "" {
		b.WriteString("\n\n")
		switch {
		case branch != "" && sym.TryOp.Boundary != "":
			fmt.Fprintf(&b, "On `%s`, unwinds to `%s`.", branch, sym.TryOp.Boundary)
		case branch != "":
			fmt.Fprintf(&b, "On `%s`.", branch)
		default:
			fmt.Fprintf(&b, "Unwinds to `%s`.", sym.TryOp.Boundary)
		}
	}
	return b.String()
}

// renderConstValue renders a constant's value expression as source text.
func renderConstValue(node ast.Node) string {
	switch n := node.(type) {
	case *ast.IntLit:
		return fmt.Sprintf("%d", n.Value)
	case *ast.FloatLit:
		return fmt.Sprintf("%g", n.Value)
	case *ast.DecimalLit:
		return n.Lexeme
	case *ast.CodepointLit:
		return "'" + n.Lexeme + "'"
	case *ast.StringLit:
		return fmt.Sprintf("%q", n.Value)
	case *ast.Binary:
		left := renderConstValue(n.Left)
		right := renderConstValue(n.Right)
		if left != "" && right != "" {
			return left + " " + n.Op + " " + right
		}
	}
	return ""
}

// enclosingFuncSig returns the function signature string if the node is a FuncDef
// with type parameters, providing context for generic type parameter display.
func enclosingFuncSig(node ast.Node) string {
	if fn, ok := node.(*ast.FuncDef); ok && len(fn.TypeParams) > 0 {
		return RenderFuncSig(fn.Name, fn.TypeParams, fn.Params, fn.ReturnTypeExpr)
	}
	return ""
}

// renderStructFromType renders a struct definition from the type system.
func renderStructFromType(st *analysis.StructType) string {
	var b strings.Builder
	b.WriteString("struct ")
	b.WriteString(st.String())
	b.WriteString(" {\n")
	for _, f := range st.Fields {
		b.WriteString("    ")
		b.WriteString(f.Name)
		if f.Type != nil {
			b.WriteString(": ")
			b.WriteString(displayValueType(instantiatedStructFieldType(st, f.Type)))
		}
		b.WriteString("\n")
	}
	b.WriteString("}")
	return b.String()
}

func instantiatedStructFieldType(st *analysis.StructType, ty analysis.Type) analysis.Type {
	if st == nil || ty == nil || len(st.TypeParamDefs) == 0 || len(st.TypeArgs) == 0 {
		return ty
	}
	n := len(st.TypeParamDefs)
	if len(st.TypeArgs) < n {
		n = len(st.TypeArgs)
	}
	subs := make(map[*analysis.TypeParam_]analysis.Type, n)
	for i := 0; i < n; i++ {
		subs[st.TypeParamDefs[i]] = st.TypeArgs[i]
	}
	return analysis.Substitute(ty, subs)
}

func renderInterfaceDef(n *ast.InterfaceDef) string {
	var b strings.Builder
	b.WriteString("interface ")
	b.WriteString(n.Name)
	b.WriteString(renderTypeParams(n.TypeParams))
	b.WriteString(renderWhereClauses(n.WhereClauses))
	b.WriteString(" {\n")
	for i := range n.Fields {
		b.WriteString("    ")
		b.WriteString(renderInterfaceField(&n.Fields[i]))
		b.WriteString("\n")
	}
	for _, m := range n.Methods {
		b.WriteString("    fn ")
		b.WriteString(m.Name)
		b.WriteString("(")
		for i, p := range m.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.Name)
			if p.TypeAnnotation != nil {
				b.WriteString(": ")
				b.WriteString(p.TypeAnnotation.TypeString())
			}
		}
		b.WriteString(")")
		if m.ReturnTypeExpr != nil {
			b.WriteString(": ")
			b.WriteString(m.ReturnTypeExpr.TypeString())
		}
		b.WriteString("\n")
	}
	b.WriteString("}")
	return b.String()
}

func renderInterfaceField(n *ast.InterfaceField) string {
	if n == nil {
		return "field"
	}
	s := "field " + n.Name
	if n.TypeAnnotation != nil {
		s += ": " + n.TypeAnnotation.TypeString()
	}
	return s
}

// renderImportPath formats an ImportStmt's path the way the surface
// syntax does: `/`-joined module-path segments, plus an optional
// `.<TypeIdent>` drill-through prefix when the trailing path segment is
// a type paired with selective Names. Mirrors format.emitImport's logic.
func renderImportPath(imp *ast.ImportStmt) string {
	modulePath := imp.ModulePath
	var drillThrough string
	if len(imp.Names) > 0 && len(modulePath) > 0 {
		if _, isType := modulePath[len(modulePath)-1].(*ast.TypeIdent); isType {
			drillThrough = ast.ImportNodeName(modulePath[len(modulePath)-1])
			modulePath = modulePath[:len(modulePath)-1]
		}
	}
	parts := make([]string, len(modulePath))
	for i, n := range modulePath {
		parts[i] = ast.ImportNodeName(n)
	}
	out := strings.Join(parts, "/")
	if drillThrough != "" {
		out += "." + drillThrough
	}
	return out
}

// SignatureAndDoc splits the hover for sym in two: the first line of its code
// fence (`fn trim(s: String): String`, `x: Int`, `struct Point`), which is
// what completion shows as an item's detail, and the markdown that follows
// the fence (doc comment, implemented interfaces), which completion delivers
// on resolve. Both come from RenderWithAnalysis, so a completion item and a
// hover over the same name agree.
func SignatureAndDoc(sym *analysis.Symbol, fa *analysis.FileAnalysis) (sig, doc string) {
	if sym == nil {
		return "", ""
	}
	full := RenderWithAnalysis(sym, fa)
	const open = "```nomi\n"
	if !strings.HasPrefix(full, open) {
		return "", strings.TrimSpace(full)
	}
	body := full[len(open):]
	closeAt := strings.Index(body, "\n```")
	if closeAt < 0 {
		return "", strings.TrimSpace(full)
	}
	fence := body[:closeAt]
	if nl := strings.IndexByte(fence, '\n'); nl >= 0 {
		fence = fence[:nl]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(fence), "{")), strings.TrimSpace(body[closeAt+len("\n```"):])
}
