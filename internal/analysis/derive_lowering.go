package analysis

// Derive lowering: the AST->AST pass that turns source derive declarations
// into the same impl machinery manual implementations use. `*ast.ImplBlock` is
// the convergence core form: parsed directly for source
// `impl Iface for Type { ... }` blocks, and synthesized here for structural
// derives.
//
// The surface forms it lowers:
//
//	struct Point {
//	  field x: Int
//	}
//	derive Debug for Point
//
// The source implementation form `impl Iface for Type { ... }` is NOT lowered
// here — it parses directly into a complete `*ast.ImplBlock` and passes through
// this pass untouched.
//
// A `derive Iface for Type` declaration emits the same synthesized block the
// internal `@derive` marker produces
// (synthesizeDeriveFor). After lowering, derive entries are consumed, so the
// impl manifest, dispatch registration, coherence (`detectImplCollisions`),
// orphan rule, `DetectMissingImpls`, derive/universal-Debug synthesis, and the
// IR builder all operate on the lowered form unchanged. (`@derive` decorators
// on the decl remain handled separately by the later SynthesizeDerives pass.)
//
// PLACEMENT: the pass runs immediately BEFORE SynthesizeDerives at every
// SynthesizeDerives call site (internal/frontend's Checker.Prepare, analysis
// buildModule, analysis BuildProjectWithCache, internal/highlight) — lowering must come first because derive and
// universal-Debug synthesis scan top-level impl blocks to decide what to
// emit (a hand-written `Debug` implementation must suppress the auto Debug impl,
// and a hand-written `Display` implementation must collide with `derive Display`,
// exactly like their hand-written top-level equivalents).
//
// CONSUME SEMANTICS: lowering consumes source derive declarations by replacing
// them with synthesized impl blocks. Two reasons:
//
//   - Idempotency for free. Several pipelines lower the same node slice
//     more than once (the front end lowers the entry slice, then its
//     analysis reaches BuildProjectWithCache which lowers again; buildModule
//     lowers slices BuildProject may already have processed). With derived
//     derive nodes consumed, a second pass finds nothing to lower and returns the
//     input unchanged — mirroring SynthesizeDerives' idempotency contract.
//   - No double-walking. Derived impl blocks are synthesized once; leaving the
//     source derive declarations reachable would invite a
//     future pass to synthesize or validate them twice.
//
// FORMATTER ISOLATION (why consuming is safe): the formatter never sees a
// lowered AST. `format.Source` runs its own `parser.ParseFile` on the
// source text (see format/format.go), and lowering runs only inside the
// front-end pipelines listed above — there is no shared slice between
// them.
//
// ERRORS: the parser owns all shape errors (`field` in an enum body,
// functions in a type body, decorators on `derive`, ...). This pass owns the
// derive semantic checks before lowering consumes the source declarations and
// nothing downstream can see their source shape:
//
//   - duplicate derive declarations.
//   - derive: `derive X for T` for an unknown derivable protocol; conflicting
//     with a hand-written `impl X for T { ... }` block.

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// LowerDerives walks the file's top-level nodes and lowers each
// `derive Iface for Type` declaration into top-level `*ast.ImplBlock` nodes.
// Type declarations pass through unchanged; the synthesized blocks appear where
// the derive declaration was written. Declarations without derives pass through
// untouched (as do top-level impl blocks, which the parser already emits as
// complete `*ast.ImplBlock` nodes); if nothing in the slice needs lowering the
// input slice is returned as-is.
//
// Lowered shapes:
//
//   - `derive Iface for Type` → the structural block synthesizeDeriveFor
//     produces, with the interface re-pointed at the derive entry's real
//     position.
func LowerDerives(nodes []ast.Node) ([]ast.Node, []TypeError) {
	var errs []TypeError
	out, changed := lowerNodeListDerives(nodes, &errs)
	if !changed {
		return nodes, errs
	}
	return out, errs
}

func lowerNodeListDerives(nodes []ast.Node, errs *[]TypeError) ([]ast.Node, bool) {
	decls := typeDeclsInNodeList(nodes)
	manualImpls := manualImplsInNodeList(nodes)
	seenDerives := map[string]map[string]bool{}
	var out []ast.Node
	changed := false
	for i, n := range nodes {
		if conf, ok := n.(*ast.ImplConformance); ok && conf.Derive && conf.Receiver != nil {
			replacement := lowerTopLevelDerive(conf, i, decls, manualImpls, seenDerives, errs)
			if !changed {
				out = append(out, nodes[:i]...)
				changed = true
			}
			out = append(out, replacement...)
			continue
		}

		if changed {
			out = append(out, n)
		}
	}
	if !changed {
		return nodes, false
	}
	return out, true
}

// typeDeclsInNodeList maps each declared type name to its first declaration.
// The builder keeps the first and reports a later one as a redeclaration, so
// a derive lowered against a later one would build a body for a type the
// name does not denote.
func typeDeclsInNodeList(nodes []ast.Node) map[string]ast.Node {
	decls := map[string]ast.Node{}
	for _, n := range nodes {
		if name, ok := typeDeclName(n); ok && name != "" && decls[name] == nil {
			decls[name] = n
		}
	}
	return decls
}

func manualImplsInNodeList(nodes []ast.Node) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, n := range nodes {
		block, ok := n.(*ast.ImplBlock)
		if !ok || block.Interface == nil || block.Receiver == nil || block.Line >= synthLineBase {
			continue
		}
		recvName := TypeExprBaseName(block.Receiver)
		ifaceName := TypeExprBaseName(block.Interface)
		if recvName == "" || ifaceName == "" {
			continue
		}
		if out[recvName] == nil {
			out[recvName] = map[string]bool{}
		}
		out[recvName][ifaceName] = true
	}
	return out
}

func lowerTopLevelDerive(conf *ast.ImplConformance, index int, decls map[string]ast.Node, manualImpls map[string]map[string]bool, seen map[string]map[string]bool, errs *[]TypeError) []ast.Node {
	addErr := func(line, col int, format string, args ...interface{}) {
		*errs = append(*errs, TypeError{Line: line, Col: col, Message: fmt.Sprintf(format, args...)})
	}
	recvName := TypeExprBaseName(conf.Receiver)
	if recvName == "" {
		addErr(conf.Line, conf.Col, "`derive` receiver must be a named type")
		return nil
	}
	decl := decls[recvName]
	if decl == nil {
		addErr(conf.Line, conf.Col, "`derive ... for %s` requires `%s` to be declared in the same scope", conf.Receiver.TypeString(), recvName)
		return nil
	}

	var out []ast.Node
	for _, iface := range conformanceInterfaces(conf) {
		ifaceName := TypeExprBaseName(iface)
		line, col := typeExprPos(iface, conf.Line, conf.Col)
		if ifaceName == "" {
			addErr(line, col, "internal: derive declaration has an unresolvable interface name (parser should have rejected it)")
			continue
		}
		if seen[recvName] == nil {
			seen[recvName] = map[string]bool{}
		}
		if seen[recvName][ifaceName] {
			addErr(line, col, "duplicate `derive %s for %s` declaration", ifaceName, recvName)
			continue
		}
		seen[recvName][ifaceName] = true
		if !deriveSupported[ifaceName] {
			addErr(line, col, "`derive %s`: unknown derivable protocol (derive supports %s)", ifaceName, deriveSupportedDescription())
			continue
		}
		if _, optErrs := validateDeriveOptions(ifaceName, conf.Options, conf.Line, conf.Col); len(optErrs) > 0 {
			*errs = append(*errs, optErrs...)
			continue
		}
		if targetErrs := validateDeriveTarget(ifaceName, decl, conf.Line, conf.Col); len(targetErrs) > 0 {
			*errs = append(*errs, targetErrs...)
			continue
		}
		if manualImpls[recvName][ifaceName] {
			addErr(line, col, "`derive %s for %s` conflicts with a hand-written `impl %s for %s { ... }` block — a derived impl is synthesized whole, so it cannot coexist with a manual impl; drop one", ifaceName, recvName, ifaceName, recvName)
			continue
		}
		one := *conf
		one.Interface = iface
		one.Interfaces = nil
		appendDeriveDecorator(decl, &one)
		// The site is the `derive` STATEMENT (its own region, indexed by its
		// position in the pre-lowering node list), but the origin a
		// diagnostic reports is the declaration it names — that is the
		// source the programmer navigates to.
		site := synthSite{origin: synthOriginConformance, index: index, wrapping: wrappingDistincts(decls)}
		site.line, site.col = declPos(decl)
		blocks := synthesizeDeriveFor(ifaceName, decl, one.Options, site)
		for _, n := range blocks {
			if block, ok := n.(*ast.ImplBlock); ok {
				block.Receiver = conf.Receiver
				block.WhereClauses = conf.WhereClauses
				if len(conf.Generics) > 0 {
					block.Generics = inheritedTypeParams(conf.Generics)
				}
			}
		}
		out = append(out, blocks...)
	}
	return out
}

func conformanceInterfaces(conf *ast.ImplConformance) []ast.TypeExpr {
	if len(conf.Interfaces) > 0 {
		return conf.Interfaces
	}
	if conf.Interface != nil {
		return []ast.TypeExpr{conf.Interface}
	}
	return nil
}

func typeExprPos(t ast.TypeExpr, fallbackLine, fallbackCol int) (int, int) {
	if t == nil {
		return fallbackLine, fallbackCol
	}
	switch v := t.(type) {
	case *ast.SimpleType:
		return v.Line, v.Col
	case *ast.GenericType:
		return v.Line, v.Col
	case *ast.QualifiedType:
		return v.ModuleLine, v.ModuleCol
	default:
		return t.LineNum(), fallbackCol
	}
}

// appendDeriveDecorator stamps a synthetic `@derive Iface` decorator onto a
// type declaration so a `derive Iface` conformance line is handled by the
// exact same downstream machinery as a hand-written derive. NOTE: `@derive`
// is not a SOURCE form (the parser rejects it); this synthetic
// `ast.Decorator{Name:"derive"}` is the ONLY producer of derive decorators,
// created here during lowering. The decorator node + its consumers
// (walkTypeDeclDecoratorArgs, CheckDeriveBounds, processTypeDeclDeriveDecorators,
// existingDerivedImpls, SynthesizeDerives) are this desugaring's target.
// Recording the derive demands directly here, without the synthetic
// decorator, would let the internal-only `@derive` handling be deleted.
//
// It is handled by the same downstream machinery as a hand-written derive — most
// importantly CheckDeriveBounds, which records the transitive
// `(Iface, component-type)` manifest demands the synthesized body dispatches
// through. The arg is the conformance line's interface type-expr (a real source
// position), so reference-recording / unused-import / go-to-def all land on the
// `derive Iface` text. The decorator's own position mirrors the
// conformance line; only its arg drives diagnostics (deriveArgName reads the
// arg).
func appendDeriveDecorator(decl ast.Node, conf *ast.ImplConformance) {
	dec := ast.Decorator{
		Name:    "derive",
		Args:    []ast.Node{conf.Interface},
		Options: conf.Options,
		Line:    conf.Line,
		Col:     conf.Col,
	}
	switch d := decl.(type) {
	case *ast.StructDef:
		d.Decorators = append(d.Decorators, dec)
	case *ast.EnumDef:
		d.Decorators = append(d.Decorators, dec)
	case *ast.TypeDef:
		d.Decorators = append(d.Decorators, dec)
	case *ast.ExternType:
		d.Decorators = append(d.Decorators, dec)
	}
}

// inheritedTypeParams returns the derive declaration's type parameters
// for use as a synthesized block's Generics. The slice is copied (the
// TypeParam structs are values; their Bounds type-exprs are shared) so callers
// can adjust the block without aliasing the derive node's slice.
func inheritedTypeParams(tps []ast.TypeParam) []ast.TypeParam {
	if len(tps) == 0 {
		return nil
	}
	return append([]ast.TypeParam(nil), tps...)
}
