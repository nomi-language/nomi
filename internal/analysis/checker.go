package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffirun"
	"sort"
	"strconv"
	"strings"
)

// checker walks function bodies and validates type consistency.
type checker struct {
	// callHoles holds the `_` arguments of the calls checked so far
	// (markCallHoles). Any other `_` read as a value is an error.
	callHoles map[*ast.Placeholder]bool
	fa        *FileAnalysis
	reg       *TypeRegistry
	// blockMethodIfaces maps each FuncDef item of a file-local interface-impl
	// block (`impl Iface for T { fn m … }`) to the interface name(s) it
	// impl. Built once up front by indexFileImplBlocks so dispatch
	// resolution (implMethodIfacesFor) recognizes a block method's owning
	// interface in SINGLE-FILE analysis (LSP, BuildFileWithStdlib), where the
	// project-wide ProjectImpls index isn't populated for the file's own
	// impls. Block items don't carry the interface on the FuncDef directly,
	// so the checker indexes them here.
	blockMethodIfaces map[*ast.FuncDef][]string
	errors            []TypeError
	pipeLambdaParams  []pipeLambdaParam      // parameters of earlier bare-bodied `then` stages of the pipelines being checked; see pipe_then_stage.go
	returnTy          Type                   // expected return type of current function boundary
	iterBreakTy       Type                   // expected type of `break v` in the current iter-callback lambda
	calleeNode        ast.Node               // the callee checkCallee is checking, which a struct-shaped variant may be; see rejectStructVariantValue
	qualifierNode     ast.Node               // the object of the field access being checked, which a type name may be; see rejectTypeNameValue
	funcRefNode       ast.Node               // the name checkNodeExpecting is instantiating against an expected function type; see generic_func_ref.go
	fnTypeParams      map[string]*TypeParam_ // outer function's type params, accessible to lambda annotations
	selfTypeName      string                 // receiver type's base name while checking an impl-block body, so bare same-owner calls can resolve to the receiver type's functions. Empty outside an impl block.
	implBlock         *ast.ImplBlock         // the impl block whose items are being checked; nil outside one. checkSelfRecursion reads it for the block's own functions.
	currentInterface  string                 // interface whose default body is currently being checked, so bare sibling calls resolve inside defaults.
	currentFnName     string                 // function body currently being checked.
	// currentFnDecl is the DECLARATION NODE of the body currently being
	// checked, or nil outside one. Beside currentFnName rather than derived
	// from it, because a name is not an identity: a nested `fn helper` beside a
	// module-level `fn helper` is a different function, and two impl blocks may
	// declare one method name. Read by the inferred-bound recorder, whose key
	// has to be that identity, and by rejectKeyWithoutEquality, which reports
	// a collection type once per body.
	currentFnDecl *ast.FuncDef
	// keyWithoutEqualityReported holds the Set and Map types
	// rejectKeyWithoutEquality has reported, per body (equality_domain.go).
	keyWithoutEqualityReported map[keyWithoutEqualityReport]bool
	// ownTypeOnces is this file's owner-level `once` bindings, private ones
	// included, by receiver base name then binding name. Built on first use
	// by fileTypeOnceSymbol. An owner symbol's Members holds only the public
	// ones, because Members is what other files see.
	ownTypeOnces map[string]map[string]*Symbol
	// instEdges are the instantiations generic bodies make, checked for
	// polymorphic recursion at the end (instantiation_cycle.go).
	instEdges []instEdge
	// undetermined is the generic calls whose type arguments are checked
	// for being determined once every body is checked, and
	// undeterminedReported the bound values a binding error already called
	// not determined (undetermined.go).
	undetermined         []undeterminedCall
	undeterminedReported []ast.Node
	// undeterminedPatterns is the pattern bindings whose names are checked
	// for a determined type once every body is checked.
	undeterminedPatterns []undeterminedPattern
	// narrowed is the type each name holds inside the arm being checked
	// that matched it against an embedded variant (narrowing.go).
	narrowed          map[*Symbol]Type
	typeVarID         int                  // counter for generating fresh type variables
	fileTypeSymbols   map[string]*Symbol   // file-local type definitions (struct/enum/type/typealias/interface) by name; populated by checkVisibilityConsistency for the visibility check, bypassing the variant-shadowed scope
	fileEnumDecls     map[string][]*Symbol // every enum this file declares, block-local ones included, by name; built on first use by enumDeclSymbol
	attachedTestScope *Scope               // declaration-associated fallback scope while checking an attached test body
	// tryBoundary describes the innermost enclosing fn or lambda for
	// purposes of `try` (and `return`) unwinding. Push/pop in checkFunc
	// and checkLambda(Expecting); read in checkTryOp when registering
	// the SymbolTryOp hover marker. Empty when not inside any function
	// body (top-level expressions don't legally contain `try`).
	tryBoundary string
	// exitless is the innermost enclosing expression no function encloses (a
	// `once` initializer, a parameter or field default) while one is being
	// checked, and nil inside any boundary within it. An early exit checked
	// while it is set is an error (exitless.go).
	exitless *exitlessSite
	// testBoundaryOverride lets attached tests reuse the ordinary test-checking
	// path while presenting a declaration-oriented boundary in assertion hovers.
	testBoundaryOverride string
	// tryUnwinds accumulates one entry per `try` site within the innermost
	// `try`-boundary that INFERS its own result type — a `concurrent` block or
	// a lambda. Pushed/restored by checkBoundaryBody, appended by
	// unwrapTryResult (and by the two assertion sites, whose failure is a
	// propagating Err of its own), consumed by resolveBoundaryErr to check the
	// boundary's result and bind its error side.
	//
	// It carries the operand's FLAVOUR and not only its error type, because
	// spec §9's boundary rule has three clauses and two of them are about the
	// flavour: a `try` on a `Maybe` contributes no error type at all, so an
	// error-type-only accumulator cannot see a `Maybe` propagating out of a
	// boundary that produces neither a `Maybe` nor anything else.
	//
	// nil outside such a boundary, and that is load-bearing rather than
	// incidental: a function's `try` sites are pinned by its DECLARED return
	// type, so they neither accumulate nor need resolving — checkTryBoundary
	// checks them against the declaration instead, and reads exactly this
	// field to tell the two regimes apart. checkFunc therefore drops the
	// pointer for the duration of a body (a nested `fn` inside a lambda must
	// not feed the LAMBDA's accumulator).
	tryUnwinds *[]tryUnwind
	// returnUnwinds collects explicit return values for an inferred boundary.
	// Nested functions and boundaries isolate their exits from this accumulator.
	returnUnwinds *[]boundaryReturn
	// expectedAt / expectedEnum track the in-flight expected type for
	// dot-leading variant resolution (`.Red`, `.Obj{...}`). Set by
	// checkNodeExpecting whenever a non-nil expected is pushed in; defer-
	// restored to nest naturally. expectedAt is the raw expected type;
	// expectedEnum is a cached *EnumType view (nil when expectedAt isn't
	// an enum). The pair lets the dot-leading diagnostics distinguish:
	//   - both nil → "no determinable enum type at this position"
	//   - expectedAt set, expectedEnum nil → "resolves only to enum variants,
	//     but the expected type at this position is <NonEnumType>"
	//   - expectedEnum set → resolve against its variants
	// The fields persist through nested checkNode recursion, which is how
	// propagation through transparent constructs (case/if arms, blocks,
	// generic-fn args with returns unified to the outer expected) works
	// automatically.
	expectedAt   Type
	expectedEnum *EnumType
}

// findEnumForVariant walks the file's module-scope enum definitions for one
// whose Members include the given variant name. Returns the enum name or ""
// if no in-file enum claims the variant. Used to populate "did you mean
// Shape.Circle?" hints when bare variant construction is rejected and to
// route bare literal-attach prefixes (`Obj{"k" => v}`) to their owning
// enum in resolveVariantPrefix.
//
// Walks three sources, in this order so same-file definitions take
// precedence on a tie (matters when an imported enum happens to share a
// variant name with a local enum — keeping local wins preserves the
// previous behavior):
//
//  1. Same-file enums (`SymbolEnum` directly in ModuleScope).
//  2. Imported enums — `import std/json.{Json}` registers a
//     `SymbolBinding` whose `Resolved` chains to the real `SymbolEnum`
//     in the dep module; follow the chain and probe its Members.
//  3. Drill-through-imported variants — `import std/json.Json.{Obj}`
//     registers `Obj` as a `SymbolBinding` resolving to a
//     `SymbolEnumVariant`; the enum is recovered from the variant's
//     `Type.(*FuncType).Return.(*EnumType).Name` (bare variants carry
//     the EnumType directly as Type).
//
// Source (3) only fires when the parent enum was NOT also brought into
// scope (via `self`, or a separate `import std/json.{Json}`) — in
// that case source (2) already finds the enum and the variant lookup
// succeeds through its Members map.
func (c *checker) findEnumForVariant(variant string) string {
	if c.fa == nil || c.fa.ModuleScope == nil {
		return ""
	}
	// Pass 1: same-file enums (priority).
	for _, sym := range c.fa.ModuleScope.Symbols {
		if sym.Resolved != nil {
			continue
		}
		if sym.Kind != SymbolEnum || sym.Members == nil {
			continue
		}
		if _, ok := sym.Members[variant]; ok {
			return sym.Name
		}
	}
	// Pass 2: imported enums (binding whose Resolved chains to SymbolEnum).
	for _, sym := range c.fa.ModuleScope.Symbols {
		if sym.Resolved == nil {
			continue
		}
		real := sym
		for real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind != SymbolEnum || real.Members == nil {
			continue
		}
		if _, ok := real.Members[variant]; ok {
			return real.Name
		}
	}
	// Pass 3: drill-through-imported variant — the local binding's
	// resolved kind is SymbolEnumVariant. Recover the enclosing enum's
	// name from the variant's Type.
	if local := c.fa.ModuleScope.Lookup(variant); local != nil {
		real := local
		for real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind == SymbolEnumVariant {
			if name := enumNameFromVariantType(real.Type); name != "" {
				return name
			}
		}
	}
	return ""
}

// enumNameFromVariantType pulls the enclosing enum's name out of a
// variant symbol's Type. For data-carrying variants the Type is a
// FuncType returning the EnumType; for bare variants it's the EnumType
// directly. Returns "" if the type doesn't fit either shape (defensive
// — every variant the type-builder produces matches one of the two).
func enumNameFromVariantType(t Type) string {
	switch tt := t.(type) {
	case *EnumType:
		return tt.Name
	case *FuncType:
		if et, ok := tt.Return.(*EnumType); ok {
			return et.Name
		}
	}
	return ""
}

// enumDeclaringVariant returns the enum in scope, declared in this file
// first and then imported, whose members include variant, or nil.
func enumDeclaringVariant(scope *Scope, variant string) *Symbol {
	// Pass 1: same-file enums (priority).
	for _, sym := range scope.Symbols {
		if sym.Resolved != nil {
			continue
		}
		if sym.Kind != SymbolEnum || sym.Members == nil {
			continue
		}
		if _, ok := sym.Members[variant]; ok {
			return sym
		}
	}
	// Pass 2: imported enums.
	for _, sym := range scope.Symbols {
		if sym.Resolved == nil {
			continue
		}
		real := sym
		for real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind != SymbolEnum || real.Members == nil {
			continue
		}
		if _, ok := real.Members[variant]; ok {
			return real
		}
	}
	return nil
}

// findOwningEnumSymForVariant returns the (resolved) SymbolEnum that
// owns the variant named `variant`, walking the same three sources as
// findEnumForVariant. Returns nil when no enum in scope claims the
// variant. Distinct from findEnumForVariant in that it returns the
// real `*Symbol` directly — needed by resolveVariantPrefix's bare-
// prefix path so a drill-through-imported variant (whose owning enum
// isn't bound in the file's scope under its bare name) can still
// hand the right Symbol off to variantPayloadFromSymbol.
func (c *checker) findOwningEnumSymForVariant(variant string) *Symbol {
	if c.fa == nil || c.fa.ModuleScope == nil {
		return nil
	}
	if sym := enumDeclaringVariant(c.fa.ModuleScope, variant); sym != nil {
		return sym
	}
	// Pass 3: drill-through-imported variant — find any enum in scope
	// (same-file or imported) whose Members contain a variant whose
	// resolved symbol matches the locally bound variant. We can't just
	// trust the variant's Type to point at the right *Symbol — Type is
	// a *EnumType (the value), not the *Symbol (the scope entry).
	// Resolve the variant first, then enumerate enums in scope until
	// one of them lists this variant.
	if local := c.fa.ModuleScope.Lookup(variant); local != nil {
		real := local
		for real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind == SymbolEnumVariant {
			enumName := enumNameFromVariantType(real.Type)
			if enumName == "" {
				return nil
			}
			// The owning enum may or may not be bound in this file's
			// scope. Walk the same source as Pass 1+2 by name — if no
			// match, synthesize a minimal *Symbol from the variant's
			// EnumType so variantPayloadFromSymbol still works (we
			// already have the right Members map via the variant's
			// enclosing enum's TypeBuilder output: the variant's
			// `real` IS one of those members, but we need its
			// siblings too).
			// First try the cheap path: look the enum up by name.
			if enumSym := c.lookupEnumSymByName(enumName); enumSym != nil {
				return enumSym
			}
			// Fallback: scan the parent scope (prelude) for any
			// SymbolEnum named enumName whose Members include this
			// variant. Walks ancestor scopes so prelude / stdlib
			// enums also resolve.
			for scope := c.fa.ModuleScope; scope != nil; scope = scope.Parent {
				for _, sym := range scope.Symbols {
					cand := sym
					for cand.Resolved != nil {
						cand = cand.Resolved
					}
					if cand.Kind != SymbolEnum || cand.Name != enumName {
						continue
					}
					if cand.Members == nil {
						continue
					}
					if _, ok := cand.Members[variant]; ok {
						return cand
					}
				}
			}
		}
	}
	return nil
}

// lookupEnumSymByName resolves a name in the file's scope chain and
// returns the underlying SymbolEnum if any (following Resolved
// indirection). Returns nil for non-enum lookups or absent names.
// Centralizes the lookup-then-chase pattern used by
// findOwningEnumSymForVariant's fast path.
func (c *checker) lookupEnumSymByName(name string) *Symbol {
	sym := c.fa.ModuleScope.Lookup(name)
	if sym == nil {
		return nil
	}
	real := sym
	for real.Resolved != nil {
		real = real.Resolved
	}
	if real.Kind != SymbolEnum {
		return nil
	}
	return real
}

// resolveVariantPrefix takes a TypeExpr written at a literal-attach prefix
// position (e.g. the `Obj` in `Obj{"k" => v}`, the `Arr` in `Arr[1, 2, 3]`)
// and decides whether it names an enum variant. Returns the variant's
// enclosing EnumType, the variant's payload type (after substitution of any
// enum type-args attached to the prefix), and the variant's bare name.
// Returns (nil, nil, "") when the prefix does NOT resolve to a variant —
// callers fall back to their existing distinct-type / nominal-struct routing.
//
// Accepts both the bare (`Obj`) and qualified (`Json.Obj`) spellings:
//
//   - Bare: resolved by findOwningEnumSymForVariant, which walks
//     same-file enums plus imported enums (whose Resolved chains to
//     the real `SymbolEnum`) plus drill-through-imported variants.
//   - Qualified: looks up the enclosing enum directly by its module
//     segment; the variant lives in that enum's Members.
//
// This entry point keeps bare-prefix resolution permissive for ALL three
// sources (same-file, imported-enum, drill-through-imported). It is the
// PATTERN-position resolver — at a pattern site the scrutinee's type
// uniquely identifies the enum, so the bare prefix has no ambiguity
// hazard (callers also verify `pet.Name == scrutinee.Name`). Value-position
// callers (construction sites in checkListLit / checkMapLit) use the
// stricter resolveVariantPrefixInValuePos instead, which rejects bare
// prefixes for same-file and imported-enum variants — requiring
// `EnumName.Variant` qualification so the resolution is unambiguous and
// uniform with the existing call-form rule in checkTypeIdent.
func (c *checker) resolveVariantPrefix(typeExpr ast.TypeExpr) (et *EnumType, payload Type, variantName string) {
	if typeExpr == nil || c.fa == nil || c.fa.ModuleScope == nil {
		return nil, nil, ""
	}
	switch t := typeExpr.(type) {
	case *ast.SimpleType:
		if enumSym := c.findOwningEnumSymForVariant(t.Name); enumSym != nil {
			return variantPayloadFromSymbol(enumSym, t.Name)
		}
		// Drill-through fallback: a name like `Obj` may resolve directly
		// to a SymbolEnumVariant brought in by
		// `import std/json.Json.{Obj}`, where the enclosing enum's
		// *Symbol isn't in scope under any name. Compute the payload
		// straight from the variant symbol.
		if local := c.fa.ModuleScope.Lookup(t.Name); local != nil {
			real := local
			for real.Resolved != nil {
				real = real.Resolved
			}
			if real.Kind == SymbolEnumVariant {
				return payloadFromVariantSymbol(real, t.Name)
			}
		}
		return nil, nil, ""
	case *ast.QualifiedType:
		enumSym := c.fa.ModuleScope.Lookup(t.Module)
		if enumSym == nil {
			// A MODULE-QUALIFIED ENUM HEAD — `shapes.Bag.Items[1, 2]`, where
			// the qualifier is `shapes.Bag`. Module scope binds the module
			// (`shapes`) and the dotted-name imports (`telemetry.Probe.Reading`),
			// but never `module.Enum`, so the lookup above finds nothing. The
			// TYPE REGISTRY does hold it — `fn take(x: shapes.Bag)` resolves
			// through exactly that key — and it is where checkStructLit's
			// qualified-head branch already looks for the brace spelling.
			//
			// STRICTLY ADDITIVE: it runs only where this arm had already
			// returned "not a variant", so no resolution that worked before
			// takes a different route now.
			if et, isEnum := c.reg.Lookup(t.Module).(*EnumType); isEnum && et != nil {
				if sm, ok := t.Member.(*ast.SimpleType); ok {
					if vd := variantDefNamed(et, sm.Name); vd != nil {
						payload := vd.DataType
						if payload != nil && len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
							subs := make(map[*TypeParam_]Type, len(et.TypeParamDefs))
							for j, def := range et.TypeParamDefs {
								subs[def] = et.TypeArgs[j]
							}
							payload = Substitute(payload, subs)
						}
						return et, payload, sm.Name
					}
				}
			}
			return nil, nil, ""
		}
		real := enumSym
		if real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind != SymbolEnum {
			return nil, nil, ""
		}
		memberName := ""
		if sm, ok := t.Member.(*ast.SimpleType); ok {
			memberName = sm.Name
		}
		if memberName == "" {
			return nil, nil, ""
		}
		return variantPayloadFromSymbol(enumSym, memberName)
	}
	return nil, nil, ""
}

// resolveVariantPrefixOrDot dispatches between the regular
// scope-based variant resolution (resolveVariantPrefix) and the
// dot-leading form which resolves against a known scrutinee enum.
// Used by ListPattern / MapPattern in pattern position, where the
// scrutinee's enum (et) is already known from the case head's type.
// Returns the same triple as resolveVariantPrefix; for DotVariantType,
// the returned EnumType is et (the scrutinee's enum) when the variant
// is found, else nil.
func (c *checker) resolveVariantPrefixOrDot(typeExpr ast.TypeExpr, et *EnumType) (*EnumType, Type, string) {
	if dvt, ok := typeExpr.(*ast.DotVariantType); ok {
		if et == nil {
			return nil, nil, ""
		}
		for i := range et.Variants {
			if et.Variants[i].Name == dvt.Name {
				vd := &et.Variants[i]
				// Stash the resolved enum on the node, mirroring the
				// expression-position path so any downstream consumers
				// (the IR builder, debug printers) see a populated value.
				dvt.ResolvedEnum = et.Name
				c.recordDotVariantReference(dvt.Name, dvt.Line, dvt.Col, et)
				payload := vd.DataType
				if payload != nil && len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
					subs := make(map[*TypeParam_]Type, len(et.TypeParamDefs))
					for j, def := range et.TypeParamDefs {
						subs[def] = et.TypeArgs[j]
					}
					payload = Substitute(payload, subs)
				}
				return et, payload, dvt.Name
			}
		}
		return nil, nil, ""
	}
	return c.resolveVariantPrefix(typeExpr)
}

// resolveVariantPrefixInValuePos is the value-position counterpart to
// resolveVariantPrefix. Used by checkListLit / checkMapLit when the
// literal-attach prefix appears at a CONSTRUCTION site (`obj = Obj{...}`,
// `xs = Arr[...]`), rather than a pattern position.
//
// The rule mirrors checkTypeIdent's existing call-form rule (which
// already rejects bare same-file variant construction with a "must be
// qualified" diagnostic): bare prefixes are accepted ONLY when the
// variant is reachable in module scope directly as a SymbolEnumVariant
// AND that symbol arrived via a drill-through import (its `Resolved`
// chain ends at the real variant in another file). Same-file variants
// and bare prefixes that resolve only through an imported *enum*'s
// Members table (e.g. `import std/json.{Json}` then bare `Obj`)
// are rejected with a clear diagnostic.
//
// Qualified prefixes (`Json.Obj`) defer to resolveVariantPrefix.
//
// Returns (et, payload, name, true) on success. On rejection, emits a
// TypeError and returns (..., false). When the prefix doesn't resolve
// to a variant at all (so the caller's distinct-type fallback should
// fire), returns (nil, nil, "", true) — no error, just "not a variant."
func (c *checker) resolveVariantPrefixInValuePos(typeExpr ast.TypeExpr, line, col int) (et *EnumType, payload Type, variantName string, ok bool) {
	if typeExpr == nil || c.fa == nil || c.fa.ModuleScope == nil {
		return nil, nil, "", true
	}
	switch t := typeExpr.(type) {
	case *ast.SimpleType:
		// Drill-through-imported (and prelude — Some / None / Ok / Err /
		// True / False arrive here): bare lookup returns a Symbol whose
		// Resolved chain ends at a SymbolEnumVariant. ACCEPT.
		if local := c.fa.ModuleScope.Lookup(t.Name); local != nil {
			real := local
			for real.Resolved != nil {
				real = real.Resolved
			}
			if real.Kind == SymbolEnumVariant {
				// If the local lookup *itself* is the same-file variant
				// definition (Resolved == nil, no import chain), it's a
				// bare same-file construction — REJECT, just like
				// checkTypeIdent's call-form rule. The drill-through
				// path requires the chain to have at least one hop.
				if local.Resolved == nil {
					enumName := enumNameFromVariantType(local.Type)
					c.emitBareVariantValuePosError(t.Name, enumName, line, col)
					return nil, nil, "", false
				}
				return drillThroughResultOK(payloadFromVariantSymbol(real, t.Name))
			}
		}
		// Not drill-through-imported. If `findOwningEnumSymForVariant`
		// can match a same-file or imported enum's Members map, the
		// bare prefix is a value-position bare variant reference —
		// REJECT with the same diagnostic. The hint names the enum so
		// the user can grab the right qualified form.
		if enumSym := c.findOwningEnumSymForVariant(t.Name); enumSym != nil {
			enumName := ""
			real := enumSym
			if real.Resolved != nil {
				real = real.Resolved
			}
			enumName = real.Name
			c.emitBareVariantValuePosError(t.Name, enumName, line, col)
			return nil, nil, "", false
		}
		// Not a variant at all — caller's distinct-type fallback fires.
		return nil, nil, "", true
	case *ast.QualifiedType:
		et, payload, vname := c.resolveVariantPrefix(typeExpr)
		return et, payload, vname, true
	case *ast.DotVariantType:
		// Dot-leading literal-attach form (`.Obj{"k" => v}`, `.Arr[1,2,3]`,
		// `.Rect{w: 4.0, h: 3.0}`). Resolves against c.expectedEnum staged
		// by checkNodeExpecting. Diagnostics mirror checkDotVariant's so the
		// user sees the same message whether they wrote a bare `.Variant`
		// or a literal-attach `.Variant{...}`.
		if c.expectedEnum == nil {
			c.errors = append(c.errors, TypeError{
				Line:    t.Line,
				Col:     t.Col,
				Message: dotVariantNoEnumMessage(t.Name, c.expectedAt),
			})
			return nil, nil, "", false
		}
		et := c.expectedEnum
		for i := range et.Variants {
			if et.Variants[i].Name == t.Name {
				vd := &et.Variants[i]
				// Stash the resolved enum on the node so the IR builder can
				// construct the right variant.
				t.ResolvedEnum = et.Name
				c.recordDotVariantReference(t.Name, t.Line, t.Col, et)
				dataType := vd.DataType
				if dataType != nil && len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
					subs := make(map[*TypeParam_]Type, len(et.TypeParamDefs))
					for j, def := range et.TypeParamDefs {
						subs[def] = et.TypeArgs[j]
					}
					dataType = Substitute(dataType, subs)
				}
				return et, dataType, t.Name, true
			}
		}
		c.errors = append(c.errors, TypeError{
			Line:    t.Line,
			Col:     t.Col,
			Message: "no variant '" + t.Name + "' on enum " + et.Name,
		}.WithHint(didYouMean(t.Name, variantNames(et))))
		return nil, nil, "", false
	}
	return nil, nil, "", true
}

// drillThroughResultOK adapts variantPayloadFromSymbol /
// payloadFromVariantSymbol's three-value return into the four-value
// shape of resolveVariantPrefixInValuePos, marking the drill-through
// accepted path as a non-erroring success.
func drillThroughResultOK(et *EnumType, payload Type, vname string) (*EnumType, Type, string, bool) {
	return et, payload, vname, true
}

// isPreludeBareVariant reports whether a bare-prefix variant name is
// one of the prelude exports (Ok/Err/Some/None/True/False) and so
// remains usable as a bare prefix at expression and pattern positions.
// Per the dot-leading variant resolution design doc, these stay bare
// via the prelude's scope export — they aren't resolved by the
// variant-resolution rule and the post-migration bare-prefix
// rejection skips them.
func isPreludeBareVariant(name string) bool {
	switch name {
	case "Ok", "Err", "Some", "None", "True", "False":
		return true
	}
	return false
}

// allowsBareVariantPattern reports whether a SimpleType enum pattern may use
// its bare name. Same-file variants and variants reachable only through an
// imported enum still use `.Variant` / `Enum.Variant`; a drill-through
// selective import (`import std/comparable.Ordering.{Equal}`) creates a real
// local binding, so the bare pattern is explicit and accepted.
func (c *checker) allowsBareVariantPattern(name string, pos Pos) bool {
	return bareVariantPatternAllowed(c.fa, name, pos)
}

func bareVariantPatternAllowed(fa *FileAnalysis, name string, pos Pos) bool {
	if isPreludeBareVariant(name) {
		return true
	}
	if fa == nil {
		return false
	}
	if pos != (Pos{}) {
		if sym := fa.References[pos]; isImportedVariantSymbol(sym) {
			return true
		}
	}
	if fa.ModuleScope != nil {
		if sym := fa.ModuleScope.Lookup(name); isImportedVariantSymbol(sym) {
			return true
		}
	}
	return false
}

// bareVariantPatternMessage is the error for a variant of enumName written
// bare in a pattern where allowsBareVariantPattern refuses it.
func bareVariantPatternMessage(variant, enumName string) string {
	return fmt.Sprintf("bare variant '%s' in pattern position; use '.%s' (or '%s.%s')", variant, variant, enumName, variant)
}

func isImportedVariantSymbol(sym *Symbol) bool {
	if sym == nil || sym.Resolved == nil {
		return false
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Kind == SymbolEnumVariant
}

// recordDotVariantReference makes a dot-leading variant site reachable
// by the LSP's hover, go-to-definition, references and rename — without
// this, `.Red` is a pure expression to the analyzer and the LSP has
// nothing to look up at the position. Mirrors what the symbol-binder does
// for `EnumName.Variant` and `Variant` at parse-derived positions.
//
// The enum is found by the declaration et came from, not by its name in
// this file's scope: a `.Variant` resolves against the expected type, and
// that type's name need not be in scope at all (`Iter.sort(xs,
// .Descending)` never names `Direction`; a block-local enum is not in the
// module scope), or may name a different declaration there.
//
// Position note: `n.Col` is the column of the leading `.`. The variant
// name's first character is one column further; that's where the user's
// cursor lands when they hover the identifier, so the reference is keyed
// there.
func (c *checker) recordDotVariantReference(variantName string, line, col int, et *EnumType) {
	if c.fa == nil || et == nil || c.fa.References == nil {
		return
	}
	enumSym := c.enumDeclSymbol(et)
	if enumSym == nil {
		return
	}
	variantSym, ok := enumSym.Members[variantName]
	if !ok {
		return
	}
	c.fa.References[Pos{Line: line, Col: col + 1}] = variantSym
}

// enumDeclSymbol answers the declaring symbol of et's enum. The candidates
// are the symbol et's name binds in this file's scope, every enum this file
// declares (block-local ones included), and every enum of the reachable
// program (ProjectImplIndex.EnumDecls). The first that declared et wins:
// et is its type, or an instantiation of it, which shares its type
// parameters. Failing that, a single candidate with et's nominal identity
// (declaring file, name) answers.
func (c *checker) enumDeclSymbol(et *EnumType) *Symbol {
	var candidates []*Symbol
	if c.fa.ModuleScope != nil {
		if sym := c.fa.ModuleScope.Lookup(et.Name); sym != nil {
			for sym.Resolved != nil {
				sym = sym.Resolved
			}
			candidates = append(candidates, sym)
		}
	}
	if c.fileEnumDecls == nil {
		c.fileEnumDecls = map[string][]*Symbol{}
		for _, sym := range c.fa.Definitions {
			if sym != nil && sym.Kind == SymbolEnum {
				c.fileEnumDecls[sym.Name] = append(c.fileEnumDecls[sym.Name], sym)
			}
		}
	}
	candidates = append(candidates, c.fileEnumDecls[et.Name]...)
	if c.fa.ProjectImpls != nil {
		candidates = append(candidates, c.fa.ProjectImpls.EnumDecls[et.Name]...)
	}
	var nominal *Symbol
	ambiguous := false
	for _, sym := range candidates {
		decl, ok := sym.Type.(*EnumType)
		if sym.Kind != SymbolEnum || !ok || decl.Name != et.Name {
			continue
		}
		if decl == et || len(decl.TypeParamDefs) > 0 && len(et.TypeParamDefs) == len(decl.TypeParamDefs) && decl.TypeParamDefs[0] == et.TypeParamDefs[0] {
			return sym
		}
		if decl.Origin == et.Origin && et.Origin != "" {
			ambiguous = ambiguous || nominal != nil && nominal != sym
			nominal = sym
		}
	}
	if ambiguous {
		return nil
	}
	return nominal
}

// dotVariantNoEnumMessage formats the diagnostic for a `.Variant` that
// can't resolve because no expected enum is determinable at its
// position. Distinguishes between two cases for a more actionable
// message: "no expected type at all" (the binding/site has no anchor)
// vs "expected type isn't an enum" (the analyzer DID determine a type,
// but it's not an enum so dot-leading can't apply).
func dotVariantNoEnumMessage(variantName string, expectedAt Type) string {
	if expectedAt == nil {
		return "." + variantName + " requires a determinable enum type at this position; annotate the binding or qualify the variant"
	}
	return "." + variantName + " resolves only to enum variants, but the expected type at this position is " + expectedAt.String()
}

func enumHasVariant(et *EnumType, name string) bool {
	for _, v := range et.Variants {
		if v.Name == name {
			return true
		}
	}
	return false
}

// checkDotVariant resolves a dot-leading variant expression (`.Red`,
// `.Circle(1.0)` via Call.Func, or bare `.None`) against the in-flight
// expected enum staged on c.expectedEnum. Three outcomes:
//
//   - c.expectedEnum is nil → "no determinable enum type" diagnostic,
//     covers both "no expected type at all" and "expected type isn't
//     an enum" (checkNodeExpecting only stages when expected is enum).
//   - c.expectedEnum's Variants has the named variant → return either
//     the enum type (no-payload variant) or a FuncType (data-carrying
//     variant) so a wrapping Call site validates args naturally.
//   - c.expectedEnum is set but no such variant → "no variant 'X' on
//     enum E" diagnostic.
//
// Mirrors the resolution path qualified prefixes use (resolveVariantPrefix)
// minus the lookup-by-EnumName step; here the enum identity comes from
// the staged expected, not from the AST.
func (c *checker) checkDotVariant(n *ast.DotVariant) Type {
	if c.expectedEnum == nil {
		e := TypeError{
			Line:    n.Line,
			Col:     n.Col,
			Message: dotVariantNoEnumMessage(n.Name, c.expectedAt),
		}
		// `f: (Float) -> Shape = .Circle`: a dot-leading variant is never a
		// function value, since only an expected enum resolves it. The
		// qualified name is.
		if ft, isFunc := resolveTypeVar(c.expectedAt).(*FuncType); isFunc {
			if et, isEnum := resolveTypeVar(ft.Return).(*EnumType); isEnum && enumHasVariant(et, n.Name) {
				e = e.WithHint(fmt.Sprintf("a constructor as a function value is written qualified, `%s.%s`", et.Name, n.Name))
			}
		}
		c.errors = append(c.errors, e)
		return nil
	}
	et := c.expectedEnum
	for i := range et.Variants {
		if et.Variants[i].Name == n.Name {
			vd := &et.Variants[i]
			// Stash the resolved enum on the node so the IR builder can
			// construct the right variant without re-running the
			// expected-type derivation.
			n.ResolvedEnum = et.Name
			c.recordDotVariantReference(n.Name, n.Line, n.Col, et)
			// Substitute generic type parameters into the variant's payload
			// shape if the enum is instantiated (TypeArgs set).
			dataType := vd.DataType
			if dataType != nil && len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
				subs := make(map[*TypeParam_]Type, len(et.TypeParamDefs))
				for j, def := range et.TypeParamDefs {
					subs[def] = et.TypeArgs[j]
				}
				dataType = Substitute(dataType, subs)
			}
			// Data-carrying variant: return a FuncType so an enclosing
			// Call validates its args against the payload. No-payload
			// variant: return the enum type directly (the bare `.None`
			// form).
			if dataType != nil && !IsZeroSized(dataType) {
				ft := &FuncType{Params: []Type{dataType}, Return: et}
				// A generic enum's constructor records its instantiation at
				// the call site, as the written `Enum.Variant(x)` does after
				// solving it, so hover shows `Full(Int): Slot<Int>`.
				if len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) &&
					!c.containsForeignTypeParam(dataType) && !c.containsForeignTypeParam(et) {
					c.attachCallType(n, ft)
				}
				return ft
			}
			return et
		}
	}
	c.errors = append(c.errors, TypeError{
		Line:    n.Line,
		Col:     n.Col,
		Message: "no variant '" + n.Name + "' on enum " + et.Name,
	}.WithHint(didYouMean(n.Name, variantNames(et))))
	return nil
}

// emitBareVariantValuePosError records the standard "bare variant
// must be qualified" diagnostic for value-position literal-attach
// sites. Mirrors checkTypeIdent's wording so the error reads the
// same whether the user wrote `Obj{...}` (flat) or `Pos(1, 2)`
// (call-form).
func (c *checker) emitBareVariantValuePosError(variant, enumName string, line, col int) {
	hint := ""
	if enumName != "" {
		hint = "; use '" + enumName + "." + variant + "'"
	}
	c.errors = append(c.errors, TypeError{
		Line:    line,
		Col:     col,
		Message: "variant '" + variant + "' must be qualified through its enum" + hint,
	})
}

// variantPayloadFromSymbol pulls a variant's payload type from an enum
// Symbol's Members map. The variant's recorded Type is a FuncType
// `(Payload) -> EnumType` for data-carrying variants; bare-variant symbols
// (no payload) instead carry the EnumType directly as Type — return a nil
// payload in that case so the caller can flag the literal-attach as a shape
// error.
func variantPayloadFromSymbol(enumSym *Symbol, variantName string) (*EnumType, Type, string) {
	real := enumSym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if real.Kind != SymbolEnum || real.Members == nil {
		return nil, nil, ""
	}
	vsym, ok := real.Members[variantName]
	if !ok {
		return nil, nil, ""
	}
	rv := vsym
	if rv.Resolved != nil {
		rv = rv.Resolved
	}
	if rv.Kind != SymbolEnumVariant {
		return nil, nil, ""
	}
	var et *EnumType
	if e, ok := real.Type.(*EnumType); ok {
		et = e
	}
	if ft, ok := rv.Type.(*FuncType); ok && len(ft.Params) == 1 {
		// Substitute the enum's type params with its current type-args so
		// generic variants (e.g. `Some Maybe<T>`) resolve their payload
		// against the concrete element type when the analyzer knows it.
		payload := ft.Params[0]
		if et != nil && len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
			subs := make(map[*TypeParam_]Type, len(et.TypeParamDefs))
			for i, def := range et.TypeParamDefs {
				subs[def] = et.TypeArgs[i]
			}
			payload = Substitute(payload, subs)
		}
		return et, payload, variantName
	}
	// Bare variant (no payload). Returning a nil payload signals the
	// caller that literal-attach on this variant is a shape error.
	return et, nil, variantName
}

// payloadFromVariantSymbol is the drill-through-fallback companion to
// variantPayloadFromSymbol: it works directly from a SymbolEnumVariant
// (already chased through Resolved) when the enclosing enum's *Symbol
// isn't reachable in scope. This happens for drill-through imports
// such as `import std/json.Json.{Obj}` without `self` — `Obj`
// resolves but the parent `Json` *Symbol does not appear under
// any local name. The variant's recorded Type carries the
// `(Payload) -> EnumType` shape we need; no need to round-trip through
// the parent enum's Members map.
//
// Type-parameter substitution is intentionally skipped — drill-through
// variant imports today don't surface concrete TypeArgs at the import
// site (they share the enum's TypeParamDefs by reference but TypeArgs
// remains empty). Generic variants like `Some Maybe<T>` reached via
// drill-through fall through here with the payload type unchanged from
// its TypeParam_ form; downstream unification fills it in at the use
// site. If concrete TypeArgs ever land on drill-through variant symbols
// the substitution logic from variantPayloadFromSymbol can be lifted
// here verbatim.
func payloadFromVariantSymbol(vsym *Symbol, variantName string) (*EnumType, Type, string) {
	if vsym == nil || vsym.Kind != SymbolEnumVariant {
		return nil, nil, ""
	}
	switch t := vsym.Type.(type) {
	case *FuncType:
		if len(t.Params) != 1 {
			return nil, nil, ""
		}
		et, _ := t.Return.(*EnumType)
		return et, t.Params[0], variantName
	case *EnumType:
		// Bare variant (no payload). Nil payload signals literal-attach
		// on this variant is a shape error, same as variantPayloadFromSymbol.
		return t, nil, variantName
	}
	return nil, nil, ""
}

func (c *checker) freshTypeVar() *TypeVar {
	c.typeVarID++
	return &TypeVar{ID: c.typeVarID}
}

// implsContext returns the interface-impl tables the unifier should consult,
// with file-local impls taking precedence over the project-wide union.
// The project-level table (ProjectImpls.Impls, built by buildProjectImplIndex
// over filesByKey) is the sole non-file-local source: cache write-through
// plus the eager stdlibFAs fold in buildProjectWithCache
// ensure every reachable stdlib FA lands in filesByKey, so the union covers
// every (Type, Iface) pair the importing file could see.
//
// Single-file paths (BuildFileWithStdlib — LSP raw-document analysis, many
// analyzer-side tests) leave ProjectImpls nil and reach the checker with
// only c.fa.Impls populated; cross-file / stdlib conformances on those
// paths are deferred to runtime dispatch.
// Both tables are paired with the SAME project-wide interface-origin index:
// `fa.Impls` is itself a broadcast union of every reachable file's impls
// (mergeImpls), so the two are keyed in one space and a per-file origins map
// would be a second name for identical contents. A path that leaves
// ProjectImpls nil pairs both with a nil index and behaves exactly as it did
// before interfaces had an identity — see interface_identity.go.
func (c *checker) implsContext() []ImplTables {
	return c.fa.implsContext()
}

// implsContext is the file's impl tables in lookup order: the file's own,
// then the project's (the standard library's included).
func (fa *FileAnalysis) implsContext() []ImplTables {
	tables := make([]ImplTables, 0, 2)
	if fa == nil {
		return tables
	}
	if fa.Impls != nil {
		tables = append(tables, implTablesFor(fa.Impls, fa.ProjectImpls))
	}
	if fa.ProjectImpls != nil {
		tables = append(tables, implTablesFor(fa.ProjectImpls.Impls, fa.ProjectImpls))
	}
	return tables
}

// recPos builds a Recording.Pos for the current checker, populating
// File from the file's FilePath when it's known. Used by every
// recording site that converts a (line, col) pair from an AST node
// into a Recording's Pos; centralizing keeps the FilePath broadcast
// out of every call site and makes "unknown file path" (single-file
// BuildFileWithStdlib path) a single nil-check away.
func (c *checker) recPos(line, col int) Pos {
	file := ""
	if c != nil && c.fa != nil {
		file = c.fa.FilePath
	}
	return Pos{File: file, Line: line, Col: col}
}

// nodeLineCol returns a best-effort (line, col) for any AST node that
// carries position fields. The Node interface only guarantees
// LineNum(); column extraction is per-node-type. Returns (0, 0) for
// nodes that don't carry positions — downstream diagnostics already
// tolerate the zero value (matches slotPosition's fallback). Used by
// recording-site callers to thread positions through helpers like
// recordInterfaceConformanceFromParam without each caller hand-coding
// a type switch.
func nodeLineCol(n ast.Node) (int, int) {
	if n == nil {
		return 0, 0
	}
	switch v := n.(type) {
	case *ast.Call:
		return v.Line, v.Col
	case *ast.Ident:
		return v.Line, v.Col
	case *ast.TypeIdent:
		return v.Line, v.Col
	case *ast.PatternBinding:
		return v.Line, v.Col
	case *ast.With:
		return v.Line, v.Col
	case *ast.IntLit:
		return v.Line, v.Col
	case *ast.FloatLit:
		return v.Line, v.Col
	case *ast.DecimalLit:
		return v.Line, v.Col
	case *ast.CodepointLit:
		return v.Line, v.Col
	case *ast.StringLit:
		return v.Line, v.Col
	case *ast.Binary:
		return v.Line, v.Col
	case *ast.GroupedExpr:
		return v.Line, v.Col
	case *ast.Unary:
		return v.Line, v.Col
	case *ast.FieldAccess:
		return v.Line, v.Col
	case *ast.StructLit:
		return v.Line, v.Col
	case *ast.ListLit:
		return v.Line, v.Col
	case *ast.VectorLit:
		return v.Line, v.Col
	case *ast.SetLit:
		return v.Line, v.Col
	case *ast.MapLit:
		return v.Line, v.Col
	case *ast.TupleLit:
		return v.Line, v.Col
	case *ast.Lambda:
		return v.Line, v.Col
	case *ast.NamedArg:
		return v.Line, v.Col
	case *ast.Placeholder:
		return v.Line, v.Col
	case *ast.StringInterp:
		return v.Line, v.Col
	case *ast.TaggedString:
		return v.Line, v.Col
	case *ast.If:
		return v.Line, v.Col
	case *ast.Case:
		return v.Line, v.Col
	case *ast.Block:
		return v.Line, v.Col
	case *ast.RangeLit:
		return v.Line, v.Col
	case *ast.TryOp:
		return v.Line, v.Col
	case *ast.Dbg:
		return v.Line, v.Col
	case *ast.Then:
		return v.Line, v.Col
	case *ast.Tap:
		return v.Line, v.Col
	case *ast.Todo:
		return v.Line, v.Col
	}
	return n.LineNum(), 0
}

// implTypeArgsContext returns the interface type-argument template tables the
// unifier should consult, file-local first then project-level
// (c.fa.ProjectImpls.ImplTypeArgs, the union of every reachable FA's
// ImplTypeArgs built by buildProjectImplIndex). File-local entries take
// precedence so a user-defined impl with the same type name as a stdlib type
// resolves to the user's template. These templates bind any generic interface's
// type parameters from a concrete receiver, Iter's element type included.
func (c *checker) implTypeArgsContext() []map[string]map[string]*ImplTypeArgs {
	return c.fa.implTypeArgsContext()
}

func (fa *FileAnalysis) implTypeArgsContext() []map[string]map[string]*ImplTypeArgs {
	if fa == nil {
		return nil
	}
	tables := make([]map[string]map[string]*ImplTypeArgs, 0, 2)
	if fa.ImplTypeArgs != nil {
		tables = append(tables, fa.ImplTypeArgs)
	}
	if fa.ProjectImpls != nil && fa.ProjectImpls.ImplTypeArgs != nil {
		tables = append(tables, fa.ProjectImpls.ImplTypeArgs)
	}
	return tables
}

func (c *checker) implTypeArgSetsContext() []map[string]map[string][]*ImplTypeArgs {
	if c.fa == nil {
		return nil
	}
	tables := make([]map[string]map[string][]*ImplTypeArgs, 0, 2)
	if c.fa.ImplTypeArgSets != nil {
		tables = append(tables, c.fa.ImplTypeArgSets)
	}
	if c.fa.ProjectImpls != nil && c.fa.ProjectImpls.ImplTypeArgSets != nil {
		tables = append(tables, c.fa.ProjectImpls.ImplTypeArgSets)
	}
	return tables
}

// unify performs a type unification using the checker's impl context. It
// returns the underlying error so callers can decide whether to emit a
// diagnostic or swallow it.
func (c *checker) unify(a, b Type, subs map[*TypeParam_]Type) error {
	return UnifyWithImpls(a, b, subs, c.implsContext(), c.implTypeArgsContext())
}

// unifyInto unifies have, a value's type, into want, the type of the position
// it flows into, admitting a function value whose type is assignable to want
// (UnifyInto). Every position with a direction uses it; unify is for joins.
func (c *checker) unifyInto(want, have Type, subs map[*TypeParam_]Type) error {
	return UnifyInto(want, have, subs, c.implsContext(), c.implTypeArgsContext())
}

// newChecker answers a checker for one file: a registry of every type the
// file declares or sees, and its impl-block index.
func newChecker(fa *FileAnalysis, nodes []ast.Node) *checker {
	reg := NewTypeRegistry()

	// Register user-defined types so the checker can look them up.
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.StructDef:
			if sym := fa.ModuleScope.Lookup(n.Name); sym != nil && sym.Type != nil {
				reg.Register(n.Name, sym.Type)
			}
		case *ast.EnumDef:
			if sym := fa.ModuleScope.Lookup(n.Name); sym != nil && sym.Type != nil {
				reg.Register(n.Name, sym.Type)
			}
		case *ast.TypeDef:
			if sym := fa.ModuleScope.Lookup(n.Name); sym != nil && sym.Type != nil {
				reg.Register(n.Name, sym.Type)
			}
		case *ast.TypeAlias:
			if sym := fa.ModuleScope.Lookup(n.Name); sym != nil && sym.Type != nil {
				reg.Register(n.Name, sym.Type)
			}
		case *ast.InterfaceDef:
			if sym := fa.ModuleScope.Lookup(n.Name); sym != nil && sym.Type != nil {
				reg.Register(n.Name, sym.Type)
			}
		}
	}

	// Register imported types and primitives types (walk all visible scopes).
	for _, sym := range fa.ModuleScope.AllVisible() {
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
	// Register module-qualified type names so e.g. `dynamic.Dynamic`
	// resolves through ResolveTypeExpr after a bare `import std/dynamic`.
	// Mirrors the same call in BuildTypes — both passes build their own
	// registry, so both must register the qualified-name keys for type
	// resolution to succeed in both passes. See the helper's doc-comment
	// for the symmetry rationale with destructured-import lookups.
	registerModuleQualifiedTypes(reg, fa.ModuleScope)

	c := &checker{fa: fa, reg: reg}
	c.indexFileImplBlocks(nodes)
	return c
}

// CheckTypes walks all function bodies and validates type consistency.
func CheckTypes(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	if fa.Onces == nil {
		fa.Onces = NewOnceTable()
	}
	fa.Onces.Index(fa, nodes)
	c := newChecker(fa, nodes)

	for _, node := range nodes {
		c.checkAttachedTests(node)
		switch n := node.(type) {
		case *ast.FuncDef:
			c.checkFunc(n)
			c.checkMainParams(n)
			c.checkMainReturn(n)
		case *ast.ExternFunc:
			c.checkExternFunc(n)
		case *ast.OnceBinding:
			c.checkOnce(n)
		case *ast.ImportStmt:
			c.checkImportVisibility(n)
		case *ast.ImplBlock:
			c.checkImplBlock(n)
		case *ast.ImplConformance:
			if n.Options != nil {
				c.checkNode(n.Options)
			}
		case *ast.TestDecl:
			c.checkTestDecl(n)
		case *ast.With:
			c.addError(n.Line, n.Col, "a `with` statement belongs in a block, such as a function body: it replaces the field until that block ends")
		case *ast.Binding, *ast.TupleDestructure, *ast.StructDestructure, *ast.MapDestructure,
			*ast.PatternDestructure, *ast.DistinctDestructure, *ast.PatternBinding,
			*ast.ExprStmt, *ast.Assertion, *ast.Defer, *ast.Return:
			c.fileLevelStatement(n)
		case *ast.StructDef:
			c.checkStructDefFieldDefaults(n)
			c.checkAppTypeMembers(n.Name, n.Items)
			c.checkNamespaceItems(n.Items)
		case *ast.EnumDef:
			c.checkEnumDefFieldDefaults(n)
			c.checkNamespaceItems(n.Items)
		case *ast.TypeDef:
			c.checkNamespaceItems(n.Items)
		case *ast.ExternType:
			c.checkForeignTypeBinding(n)
			c.checkNamespaceItems(n.Items)
		case *ast.ExternPackage:
			c.checkGoImport(n)
		case *ast.InterfaceDef:
			c.checkInterfaceDefaults(n)
		}
	}

	c.rejectValueAssertions(nodes)
	c.checkGoAliases(nodes)
	c.checkVisibilityConsistency(nodes)
	c.checkQualifiedTypeVisibility(nodes)
	c.checkNamingConventions(nodes)
	c.checkImportsAtTop(nodes)
	c.checkInstantiationCycles()
	c.checkUndeterminedPatternBindings()
	c.checkUndeterminedTypeArgs()
	c.errors = append(c.errors, checkAppRoots(fa, nodes)...)
	// A boot's signature and placement are checked once per project build,
	// and reported with the file that declares the boot.
	c.errors = append(c.errors, fa.BootErrors...)

	return c.errors
}

// fileLevelStatement reports a statement written at file level. A file holds
// declarations, and the only value it binds is a `once` (spec §2); code runs
// inside a function. The parser accepts statements at file level because the
// REPL reads its inputs as files, so the rule is the checker's.
func (c *checker) fileLevelStatement(n ast.Node) {
	err := errAt(n, "")
	switch t := n.(type) {
	case *ast.Binding:
		err.Message = "a binding cannot be written at file level: a file binds a value only with `once`"
		if t.Name == "_" {
			err.Hints = []string{"move it into a function, such as `fn main`"}
		} else {
			err.Hints = []string{fmt.Sprintf(
				"write `once %s = ...` for a value the file computes once, or move the binding into a function", t.Name)}
		}
	case *ast.ExprStmt:
		err.Message = "an expression cannot be written at file level: a file holds declarations, and code runs inside a function"
		err.Hints = []string{"move it into a function, such as `fn main`"}
	case *ast.Assertion, *ast.Defer, *ast.Return:
		err.Message = "this statement cannot be written at file level: a file holds declarations, and code runs inside a function"
		err.Hints = []string{"move it into a function, such as `fn main`"}
	default:
		err.Message = "a destructuring binding cannot be written at file level: a file binds a value only with `once`"
		err.Hints = []string{"bind each value with its own `once name = ...`, or move the binding into a function"}
	}
	c.report(err)
}

func (c *checker) checkNamespaceItems(items []ast.Node) {
	for _, item := range items {
		c.checkAttachedTests(item)
		switch n := item.(type) {
		case *ast.FuncDef:
			c.checkFunc(n)
		case *ast.ExternFunc:
			c.checkExternFunc(n)
		case *ast.ExternType:
			c.checkForeignTypeBinding(n)
		case *ast.OnceBinding:
			c.checkOnce(n)
		case *ast.ImportStmt:
			c.checkImportVisibility(n)
		case *ast.ImportBlock:
			for _, entry := range n.Entries {
				c.checkImportVisibility(entry)
			}
		case *ast.ImplBlock:
			c.checkImplBlock(n)
		case *ast.TestDecl:
			c.checkTestDecl(n)
		}
	}
}

// checkMainParams rejects a top-level `fn main` that declares parameters:
// nothing calls main with arguments. A program's arguments reach it through
// its boot, as `startup.args`.
func (c *checker) checkMainParams(fn *ast.FuncDef) {
	if fn.Name != "main" || len(fn.Params) == 0 {
		return
	}
	first, last := fn.Params[0], fn.Params[len(fn.Params)-1]
	e := TypeError{Line: first.Line, Col: first.Col, Message: "`main` takes no parameters"}
	if end, ok := paramEnd(last); ok {
		e.EndLine, e.EndCol = end.EndLine, end.EndCol
	}
	e.Hints = []string{"a program reads its arguments in `fn boot(startup: Startup)` as `startup.args`, and keeps what `main` needs in the application struct"}
	c.report(e)
}

// paramEnd is the span of a parameter's last part: its default, its type
// annotation, its destructuring pattern, or its name.
func paramEnd(p ast.Param) (ast.Span, bool) {
	var parts []ast.Node
	if p.Default != nil {
		parts = append(parts, p.Default)
	}
	if p.TypeAnnotation != nil {
		parts = append(parts, p.TypeAnnotation)
	}
	if p.Destructure != nil {
		parts = append(parts, p.Destructure)
	}
	for _, n := range parts {
		if sp, ok := spanOf(n); ok {
			return sp, true
		}
	}
	if p.Line > 0 && p.Name != "" {
		return ast.Span{StartLine: p.Line, StartCol: p.Col, EndLine: p.Line, EndCol: p.Col + len(p.Name)}, true
	}
	return ast.Span{}, false
}

// mainReturnMsg rejects a `fn main` whose result a run would drop.
const mainReturnMsg = "`main` must return `Unit` or `Result<Unit, E>`"

// checkMainReturn rejects a top-level `fn main` whose declared result is
// neither Unit nor Result<Unit, E>. A run prints an `Err` and exits 1, and
// prints nothing for any other value, so `Ok("hello")` or a bare value
// returned from main would be dropped without a word.
func (c *checker) checkMainReturn(fn *ast.FuncDef) {
	if fn.Name != "main" || fn.ReturnTypeExpr == nil {
		return
	}
	sym := c.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil || sym.Node != fn {
		return
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft.Return == nil {
		return
	}
	ret := resolveTypeVar(ft.Return)
	if isUnitLike(ret) {
		return
	}
	if et, ok := ret.(*EnumType); ok && et.Name == "Result" &&
		(et.Origin == "std/results" || et.Origin == "") &&
		len(et.TypeArgs) == 2 && isUnitLike(et.TypeArgs[0]) {
		return
	}
	e := errAt(fn.ReturnTypeExpr, mainReturnMsg)
	e.Hints = []string{"to show a value, print it with `io.print` and return `Ok(Unit)`"}
	c.report(e)
}

// checkGoImport reports a `gopkg` declaration whose import path no Go module
// of the project provides: one that is not a valid Go import path, or, when
// the file is on disk, one the nearest go.mod above it does not provide.
// ffirun.GoImportProblem is the rule, the one a run's FFI validation applies;
// before this the checker accepted such a path and the IR builder declined
// every body bound through it. A file with no path (an untitled editor
// buffer, a source string) gets the import path check only.
func (c *checker) checkGoImport(n *ast.ExternPackage) {
	why := ffirun.GoImportPathProblem(n.ImportPath)
	if why == "" && c.fa.FilePath != "" {
		why = ffirun.GoImportProblem(c.fa.FilePath, n.ImportPath)
	}
	if why == "" {
		return
	}
	line, col := n.ImportPathLine, n.ImportPathCol
	if line == 0 {
		line, col = n.Line, n.Col
	}
	e := TypeError{Line: line, Col: col, Message: fmt.Sprintf("gopkg %q: %s", n.ImportPath, why)}
	if strings.HasPrefix(why, "outside ") {
		e = e.WithHint(fmt.Sprintf("add it with `go get %s`, or add a require or replace for its module to go.mod", n.ImportPath))
	}
	c.report(e)
}

// checkGoAliases reports a `go alias.Symbol` binding whose alias no `gopkg`
// declaration of the file names. A `gopkg` binds its alias for the
// declarations beside it and nothing else binds one, so such a binding names
// no Go package and nothing can run it.
func (c *checker) checkGoAliases(nodes []ast.Node) {
	declared := map[string]bool{}
	for _, n := range nodes {
		if ep, isPkg := n.(*ast.ExternPackage); isPkg {
			declared[ep.Alias] = true
		}
	}
	report := func(alias string, line, col int, name string) {
		if alias == "" || declared[alias] {
			return
		}
		e := TypeError{Line: line, Col: col, Message: fmt.Sprintf(
			"`go %s.%s`: no `gopkg` in this file declares the alias %s", alias, name, alias)}
		c.report(e.WithHint(fmt.Sprintf("declare the package with `gopkg \"<import path>\" as %s`", alias)))
	}
	var walk func(items []ast.Node)
	walk = func(items []ast.Node) {
		for _, n := range items {
			switch d := n.(type) {
			case *ast.ExternFunc:
				report(d.ForeignAlias, d.ForeignAliasLine, d.ForeignAliasCol, d.ForeignName)
			case *ast.ExternType:
				report(d.ForeignAlias, d.ForeignAliasLine, d.ForeignAliasCol, d.ForeignName)
				walk(d.Items)
			case *ast.ImplBlock:
				walk(d.Items)
			}
		}
	}
	walk(nodes)
}

func (c *checker) checkForeignTypeBinding(n *ast.ExternType) {
	if n.ForeignAlias == "" {
		return
	}
	if !n.Opaque {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"Go type binding %s must be declared with `opaque type`",
			n.Name))
	}
}

// checkVisibilityConsistency walks public top-level declarations and reports
// any reference to a private (file-local, non-`pub`) type. Spec §3:
// public functions/structs/enums/interfaces/aliases must not expose private
// types. Primitive types, stdlib types, and types imported from other
// modules are by definition public — only locally-declared
// without-`pub` types count as private.
func (c *checker) checkVisibilityConsistency(nodes []ast.Node) {
	// Pre-build a name→symbol map of file-local type definitions. We
	// can't rely on fa.References for this — embedded enum variants
	// share their name with the type they embed (`embeds Secret`
	// defines a variant named Secret), and variant Define() shadows the
	// type symbol in the scope, which can cause References to point at
	// the variant rather than the type. fa.Definitions is keyed by Pos
	// so every definition survives independently.
	c.fileTypeSymbols = make(map[string]*Symbol)
	for _, sym := range c.fa.Definitions {
		// Only the type-defining symbols themselves count — not enum
		// variants (which share the embedded form's name and would
		// otherwise mask the underlying struct), not type parameters
		// (which carry SymbolType but have nil Node), and not imports
		// (which carry someone else's symbol kind but their own Node).
		// Filter on BOTH: the AST node must be one of the type-defining
		// forms AND the symbol's kind must be the matching type kind.
		var typeKind bool
		switch sym.Kind {
		case SymbolStruct, SymbolEnum, SymbolTypeAlias, SymbolInterface, SymbolType:
			typeKind = true
		}
		if !typeKind {
			continue
		}
		switch sym.Node.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias, *ast.InterfaceDef:
			// fall through
		default:
			continue
		}
		existing, exists := c.fileTypeSymbols[sym.Name]
		if !exists || (sym.Public && !existing.Public) {
			c.fileTypeSymbols[sym.Name] = sym
		}
	}
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.FuncDef:
			if !n.Public {
				continue
			}
			for _, p := range n.Params {
				c.checkPublicTypeExpr(p.TypeAnnotation, "function parameter")
			}
			c.checkPublicTypeExpr(n.ReturnTypeExpr, "function return")
		case *ast.ExternFunc:
			if !n.Public {
				continue
			}
			for _, p := range n.Params {
				c.checkPublicTypeExpr(p.TypeAnnotation, "extern function parameter")
			}
			c.checkPublicTypeExpr(n.ReturnTypeExpr, "extern function return")
		case *ast.StructDef:
			if !n.Public {
				continue
			}
			// Opaque types may have private internals — outside callers
			// can't reach the representation, so private wrapped types
			// don't leak. See §3 Visibility Consistency relaxation.
			if c.symbolIsOpaque(n.Name) {
				continue
			}
			for _, f := range n.Fields {
				c.checkPublicTypeExpr(f.TypeAnnotation, "public struct field")
			}
		case *ast.EnumDef:
			if !n.Public {
				continue
			}
			if c.symbolIsOpaque(n.Name) {
				continue
			}
			for _, v := range n.Variants {
				c.checkPublicTypeExpr(v.EmbeddedTypeExpr, "public enum embed")
				c.checkPublicTypeExpr(v.DataTypeExpr, "public enum variant data")
				for _, f := range v.Fields {
					c.checkPublicTypeExpr(f.TypeAnnotation, "public enum variant field")
				}
			}
		case *ast.InterfaceDef:
			if !n.Public {
				continue
			}
			for _, m := range n.Methods {
				for _, p := range m.Params {
					c.checkPublicTypeExpr(p.TypeAnnotation, "public interface function parameter")
				}
				c.checkPublicTypeExpr(m.ReturnTypeExpr, "public interface function return")
			}
		case *ast.TypeAlias:
			if !n.Public {
				continue
			}
			c.checkPublicTypeExpr(n.TargetTypeExpr, "public type alias")
		case *ast.TypeDef:
			if !n.Public {
				continue
			}
			if c.symbolIsOpaque(n.Name) {
				continue
			}
			c.checkPublicTypeExpr(n.InnerTypeExpr, "public distinct type inner")
		case *ast.OnceBinding:
			if !n.Public {
				continue
			}
			// TypeAnnotation is optional on `once` bindings — when
			// omitted there's no public type-name to leak through.
			c.checkPublicTypeExpr(n.TypeAnnotation, "public once binding")
		}
	}
}

// symbolIsOpaque returns true if the named top-level symbol in the
// current file's module scope is exported as opaque. Used by
// checkVisibilityConsistency to skip the private-type leak check for
// opaque distinct types — outside callers can't reach the
// representation, so a private wrapped type doesn't leak.
func (c *checker) symbolIsOpaque(name string) bool {
	if c.fa == nil || c.fa.ModuleScope == nil {
		return false
	}
	sym := c.fa.ModuleScope.Lookup(name)
	if sym == nil {
		return false
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Opaque
}

// checkPublicTypeExpr walks a type expression and reports each reference to
// a file-local private type. Recurses into composite type forms (generic
// arguments, function parameters/returns, qualified types) so a `Secret`
// hiding inside `List<Maybe<Secret>>` is still caught.
func (c *checker) checkPublicTypeExpr(te ast.TypeExpr, context string) {
	if te == nil {
		return
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		c.flagIfPrivate(Pos{Line: t.Line, Col: t.Col}, t.Name, context)
	case *ast.GenericType:
		c.flagIfPrivate(Pos{Line: t.Line, Col: t.Col}, t.Name, context)
		for _, p := range t.Params {
			c.checkPublicTypeExpr(p, context)
		}
	case *ast.QualifiedType:
		// A module-qualified type (other.Module.SomeType) is reachable only
		// because the imported module exposed it, so it's by definition not
		// a file-local privacy leak. Recurse into a generic-type member's
		// args though — those can name local types.
		if g, ok := t.Member.(*ast.GenericType); ok {
			for _, p := range g.Params {
				c.checkPublicTypeExpr(p, context)
			}
		}
	case *ast.FuncType:
		for _, p := range t.Params {
			c.checkPublicTypeExpr(p, context)
		}
		c.checkPublicTypeExpr(t.Return, context)
	}
}

// checkNamingConventions enforces spec §4: types and enum variants are
// PascalCase; modules, functions, struct fields, parameters, bindings, and
// constants are snake_case; type parameters are PascalCase. Walks top-level
// decls. Binding-level names that only exist inside function bodies
// (let-bindings, destructure bindings, nested constants) are validated by
// the body walker via validateBindingLikeName, not here.
func (c *checker) checkNamingConventions(nodes []ast.Node) {
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.FuncDef:
			c.requireSnakeCase(n.Name, n.Line, n.Col, "function name")
			for _, p := range n.Params {
				c.requireSnakeCase(p.Name, p.Line, p.Col, "parameter name")
			}
			for _, tp := range n.TypeParams {
				c.requireTypeParamName(tp.Name, tp.Line, tp.Col)
			}
		case *ast.ExternFunc:
			c.requireSnakeCase(n.Name, n.Line, n.Col, "extern function name")
			for _, p := range n.Params {
				c.requireSnakeCase(p.Name, p.Line, p.Col, "parameter name")
			}
		case *ast.StructDef:
			c.requirePascalCase(n.Name, n.Line, n.Col, "struct name")
			for _, f := range n.Fields {
				c.requireSnakeCase(f.Name, f.Line, f.Col, "struct field name")
			}
			for _, tp := range n.TypeParams {
				c.requireTypeParamName(tp.Name, tp.Line, tp.Col)
			}
		case *ast.EnumDef:
			c.requirePascalCase(n.Name, n.Line, n.Col, "enum name")
			for _, v := range n.Variants {
				if v.Kind == "embedded" {
					// Variant Name is the embedded type's name — the
					// PascalCase requirement falls on the type itself,
					// which is checked at its own declaration site.
					continue
				}
				c.requirePascalCase(v.Name, v.Line, v.Col, "enum variant name")
				for _, f := range v.Fields {
					c.requireSnakeCase(f.Name, f.Line, f.Col, "enum variant field name")
				}
			}
			for _, tp := range n.TypeParams {
				c.requireTypeParamName(tp.Name, tp.Line, tp.Col)
			}
		case *ast.InterfaceDef:
			c.requirePascalCase(n.Name, n.Line, n.Col, "interface name")
			for _, m := range n.Methods {
				c.requireSnakeCase(m.Name, m.Line, m.Col, "interface function name")
				for _, p := range m.Params {
					c.requireSnakeCase(p.Name, p.Line, p.Col, "interface function parameter name")
				}
			}
			for _, tp := range n.TypeParams {
				c.requireTypeParamName(tp.Name, tp.Line, tp.Col)
			}
		case *ast.TypeAlias:
			c.requirePascalCase(n.Name, n.Line, n.Col, "type alias name")
		case *ast.TypeDef:
			c.requirePascalCase(n.Name, n.Line, n.Col, "distinct type name")
		case *ast.OnceBinding:
			c.requireSnakeCase(n.Name, n.Line, n.Col, "once binding name")
		case *ast.ImportStmt:
			c.checkImportNaming(n)
		case *ast.ImportBlock:
			for _, entry := range n.Entries {
				c.checkImportNaming(entry)
			}
		}
	}
}

// checkImportNaming validates each path segment, imported name, and alias in
// an import statement. Path segments and original imported names follow their
// lexical shape. Aliases follow the thing being named: module/value aliases are
// strict snake_case, while type-ish aliases and variants are PascalCase.
func (c *checker) checkImportNaming(n *ast.ImportStmt) {
	for _, seg := range n.ModulePath {
		c.validateImportNode(seg, "import path segment")
	}
	for _, name := range n.Names {
		c.validateImportNode(name, "imported name")
	}
	for i, alias := range n.Aliases {
		if alias == nil {
			continue
		}
		c.validateImportAliasNode(alias, c.importAliasTargetKind(n, i, alias), "import alias")
	}
	if n.ModuleAlias != nil {
		line, col := nodeLineCol(n.ModuleAlias)
		c.requireStrictSnakeCase(ast.ImportNodeName(n.ModuleAlias), line, col, "import alias")
	}
	for i, alias := range n.ExportAliases {
		if alias == nil {
			continue
		}
		c.validateImportAliasNode(alias, c.importAliasTargetKind(n, i, alias), "export alias")
	}
}

// validateImportNode applies the case rule appropriate to the lexer's
// classification of the import-position node: *ast.Ident must be
// snake_case, *ast.TypeIdent must be PascalCase. Other node kinds (or
// nil) are silently ignored — the parser shouldn't produce them here.
func (c *checker) validateImportNode(n ast.Node, kind string) {
	switch v := n.(type) {
	case *ast.Ident:
		c.requireSnakeCase(v.Name, v.Line, v.Col, kind)
	case *ast.TypeIdent:
		c.requirePascalCase(v.Name, v.Line, v.Col, kind)
	}
}

func (c *checker) validateImportAliasNode(n ast.Node, target SymbolKind, kind string) {
	name := ast.ImportNodeName(n)
	line, col := nodeLineCol(n)
	if importAliasTargetWantsPascal(target) {
		c.requirePascalCase(name, line, col, kind)
		return
	}
	c.requireStrictSnakeCase(name, line, col, kind)
}

func (c *checker) importAliasTargetKind(stmt *ast.ImportStmt, index int, alias ast.Node) SymbolKind {
	if c.fa == nil {
		return SymbolBinding
	}
	line, col := nodeLineCol(alias)
	if sym := c.fa.Definitions[Pos{Line: line, Col: col}]; sym != nil {
		return resolvedSymbolKind(sym)
	}
	if index < len(stmt.Names) {
		name := ast.ImportNodeName(stmt.Names[index])
		if sym := c.fa.ModuleScope.Lookup(name); sym != nil {
			return resolvedSymbolKind(sym)
		}
	}
	return SymbolBinding
}

func resolvedSymbolKind(sym *Symbol) SymbolKind {
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil {
		return SymbolBinding
	}
	return sym.Kind
}

func importAliasTargetWantsPascal(kind SymbolKind) bool {
	switch kind {
	case SymbolStruct, SymbolEnum, SymbolEnumVariant, SymbolType, SymbolTypeAlias, SymbolInterface:
		return true
	default:
		return false
	}
}

// validateBindingLikeName routes a binding-style name (let, destructure,
// nested const) through the snake_case rule. Wildcards are accepted by
// isSnakeCase. Empty names are no-ops (used for elided struct-pattern
// bindings like `{x: _}` where Pattern handles the slot).
func (c *checker) validateBindingLikeName(name string, line, col int, kind string) {
	if name == "" {
		return
	}
	c.requireSnakeCase(name, line, col, kind)
}

func (c *checker) requirePascalCase(name string, line, col int, kind string) {
	if name == "" {
		return
	}
	if strings.Contains(name, ".") {
		for _, part := range strings.Split(name, ".") {
			if part == "" || !isPascalCase(part) {
				c.addError(line, col, fmt.Sprintf("%s %q must use PascalCase segments", kind, name))
				return
			}
		}
		return
	}
	if isPascalCase(name) {
		return
	}
	c.addError(line, col, fmt.Sprintf("%s %q must be PascalCase", kind, name))
}

func (c *checker) requireTypeParamName(name string, line, col int) {
	if isTypeParamName(name) {
		return
	}
	c.addError(line, col, fmt.Sprintf("type parameter %q must be PascalCase", name))
}

func (c *checker) requireSnakeCase(name string, line, col int, kind string) {
	if name == "" || isSnakeCase(name) {
		return
	}
	c.addError(line, col, fmt.Sprintf("%s %q must be snake_case", kind, name))
}

func (c *checker) requireStrictSnakeCase(name string, line, col int, kind string) {
	if name == "" || isStrictSnakeCase(name) {
		return
	}
	c.addError(line, col, fmt.Sprintf("%s %q must be snake_case", kind, name))
}

// isPascalCase reports whether s is `[A-Z][A-Za-z0-9]*` — first char
// uppercase, then letters/digits, no underscores.
func isPascalCase(s string) bool {
	if s == "" {
		return false
	}
	if !(s[0] >= 'A' && s[0] <= 'Z') {
		return false
	}
	for _, r := range s {
		if r == '_' {
			return false
		}
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func isTypeParamName(s string) bool {
	return isPascalCase(s)
}

// isSnakeCase reports whether s consists of lowercase letters, digits, and
// underscores. The lone wildcard `_` is allowed; otherwise s must have at
// least one alphabetic character so digits-only and underscore-only names
// (apart from `_`) are rejected.
func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	if s == "_" {
		return true
	}
	// A single trailing `?` (predicate convention, e.g. `empty?`) is allowed
	// on any identifier; the lexer guarantees the `?` only ever appears at the
	// end, so stripping one before the body check is sufficient.
	s = strings.TrimSuffix(s, "?")
	if s == "" {
		return false // a bare `?` is not a valid name
	}
	hasAlpha := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			hasAlpha = true
		case r >= '0' && r <= '9':
			// digits are fine, just don't count as alpha
		case r == '_':
			// underscores are fine, don't count as alpha
		default:
			return false
		}
	}
	return hasAlpha
}

func isStrictSnakeCase(s string) bool {
	return !strings.Contains(s, "?") && isSnakeCase(s)
}

// flagIfPrivate reports a private-type leak when the type named at pos is
// declared as a non-`pub` symbol in this file. Resolves directly via the
// fileTypeSymbols name map rather than fa.References, so embedded enum
// variants don't mask the underlying private struct/enum/etc.
func (c *checker) flagIfPrivate(pos Pos, name, context string) {
	sym, ok := c.fileTypeSymbols[name]
	if !ok || sym == nil {
		return // imported / primitive / unknown — not file-local.
	}
	if sym.Public {
		return
	}
	c.addError(pos.Line, pos.Col, fmt.Sprintf(
		"%s exposes private type %s", context, name))
}

func (c *checker) addError(line, col int, msg string) {
	c.errors = append(c.errors, TypeError{Line: line, Col: col, Message: msg})
}

// addErrorAt reports msg over n's extent.
func (c *checker) addErrorAt(n ast.Node, msg string) {
	c.report(errAt(n, msg))
}

// report records e.
func (c *checker) report(e TypeError) {
	c.errors = append(c.errors, e)
}

// checkNestedFunc checks a `fn` declared inside a body.
//
// CheckTypes's walk reaches module-level declarations, namespace items and
// impl-block items. A `fn` written inside a block or a test body is none of
// those, and `checkNode` had no arm for one — so until this existed a nested
// `fn`'s BODY was never type-checked at all:
// `fn bad(): Int { "nope" }` at file scope is `return type mismatch: expected
// Int, got String`, and the identical declaration nested inside `main` drew
// SILENCE from the analyzer.
//
// The consequence that made this worth finding is not the missing diagnostic
// but a missing FACT. checkGenericCall attaches the instantiated signature to
// the call-site symbol (Symbol.CallType), and that is how a prelude
// constructor learns its type arguments: `Some(1)` in a function returning
// `Maybe<Int>` records `(Int) -> Maybe<Int>`. Inside a nested `fn` nothing ran,
// so nothing was recorded, and the IR builder refused `Some(1)` there under
// `generic enum over a type parameter` — a key naming monomorphization for what
// was really an unvisited body. The identical text at file scope lowered.
//
// Position-keyed, and the guard is the point rather than defensiveness.
// checkFunc falls back to `ModuleScope.Lookup(fn.Name)` when a declaration's
// position carries no symbol, which is right for a file-scope `fn` and a
// BARE-NAME COLLAPSE for a nested one: a nested `fn helper` beside a
// module-level `fn helper` is a different function (internal/irbuild/nestedfn.go
// pins the answer 101 rather than 1), so checking the nested body against
// the module signature would report the module's return type at the nested
// body's position. A nested `fn` the resolver did not record is therefore left
// unchecked rather than checked against somebody else's declaration.
func (c *checker) checkNestedFunc(fn *ast.FuncDef) {
	if c.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}] == nil {
		return
	}
	c.checkFunc(fn)
}

func (c *checker) checkFunc(fn *ast.FuncDef) {
	// Look up by position first (handles impl/extend methods that share names),
	// then fall back to name lookup for regular functions.
	sym := c.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil {
		sym = c.fa.ModuleScope.Lookup(fn.Name)
	}
	if sym == nil {
		return
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok {
		return
	}

	// For generic function definitions, check the body with type params as opaque.
	// Operations on type params that require concrete types will correctly error.
	// Return type checking uses TypesEqual which treats TypeParam_ comparisons nominally.

	prev := c.returnTy
	c.returnTy = ft.Return
	defer func() { c.returnTy = prev }()

	prevFnName := c.currentFnName
	c.currentFnName = fn.Name
	defer func() { c.currentFnName = prevFnName }()
	prevFnDecl := c.currentFnDecl
	c.currentFnDecl = fn
	defer func() { c.currentFnDecl = prevFnDecl }()

	// `try` (and `return`) inside this body unwind to the function itself.
	// Push the boundary so checkTryOp can label the SymbolTryOp marker.
	prevBoundary := c.tryBoundary
	c.tryBoundary = "fn " + fn.Name
	defer func() { c.tryBoundary = prevBoundary }()
	defer c.enterBoundaryExits()()

	// A declared return type PINS this body's `try` sites, so they neither
	// accumulate nor need resolving — and a nested `fn` inside a lambda must
	// not contribute its own `try` error types to the LAMBDA's accumulator,
	// which is what happens if the pointer is left live across the boundary.
	// Dropping it is also what makes `c.tryUnwinds == nil` mean exactly "the
	// innermost boundary declares its own result", which checkTryBoundary
	// reads.
	prevTryUnwinds := c.tryUnwinds
	c.tryUnwinds = nil
	defer func() { c.tryUnwinds = prevTryUnwinds }()
	prevReturns := c.returnUnwinds
	c.returnUnwinds = nil
	defer func() { c.returnUnwinds = prevReturns }()

	// Make the function's TypeParam_s visible to lambda annotations inside
	// the body — `|x: List<T>| …` should resolve T to the same TypeParam_
	// the outer signature uses — and to a `T.member` qualifier, which needs
	// every name the declaration writes and not only the ones a parameter or
	// the return mentions.
	prevTP := c.fnTypeParams
	c.fnTypeParams = declaredTypeParamsByName(ft, fn.TypeParams)
	defer func() { c.fnTypeParams = prevTP }()

	restoreWhereBounds := c.pushWhereBounds(ft.WhereBounds)
	defer restoreWhereBounds()

	// Destructuring parameters: reuse the slot types buildFuncType already
	// derived (ft.Params is index-aligned with fn.Params, including
	// destructure slots), reject refutable patterns, then run checkPattern to
	// type the bound inner names so the body's references resolve. buildFuncType
	// is the sole reporter of the missing-annotation / generic-head derivation
	// error — checkDestructureParams must not re-derive (that double-reported,
	// see fix I-1). Mirrors the lambda param paths, which never go through
	// buildFuncType and so derive (and report once) inline.
	c.checkDestructureParams(fn.Params, ft)
	if len(ft.Params) == len(fn.Params) {
		for i, p := range fn.Params {
			c.checkParamDefault(p, ft.Params[i])
		}
	}

	// If the body's trailing expression is a list literal and the declared
	// return type is a list, swap that expression's check to the
	// expected-type variant ahead of checkBlock — otherwise checkListLit
	// runs first and reports a spurious mismatch (locking the element type
	// to the first item) before subtype coercion gets a chance. We do this
	// by checking earlier statements normally, then the last with
	// `checkNodeExpecting`.
	// A trailing lambda gets the declared FuncType here, which infers its
	// unannotated parameters (`fn make_adder(n: Int): (Int) -> Int { |x| n + x }`).
	bodyTy := c.checkBlockExpectingReturn(fn.Body)

	// Check implicit return: body type must match declared return type.
	// TypesEqual is the fast path. When the body type carries TypeVars
	// (from a nested generic call whose unsolved type params became
	// fresh inference vars), fall back to unify so the TypeVars bind to
	// the declared return — `Some(Err("boom"))` returning
	// `Maybe<Result<?α, String>>` should accept a declared
	// `Maybe<Result<Int, String>>` once ?α binds to Int.
	if bodyTy != nil && c.returnTy != nil {
		if !TypeAssignable(c.returnTy, bodyTy) {
			matched := false
			if isBoolVariantVsBool(bodyTy, c.returnTy) {
				matched = true
			}
			if !matched && containsTypeVar(bodyTy) {
				if err := c.unifyInto(c.returnTy, bodyTy, nil); err == nil {
					matched = true
				}
			}
			// Interface subtyping at the return-type boundary: a
			// concrete struct that `impl`s the declared interface type
			// satisfies the declaration, including the interface's type
			// arguments. Most prominent use: `fn boot(): App {
			// AppEnv{...} }` — AppEnv is concrete, App is the
			// interface, and an `impl App for AppEnv` block makes
			// the substitution legal. This is narrow on purpose — we
			// don't allow arbitrary concrete-where-interface elsewhere,
			// only at function return positions.
			if !matched {
				if c.argMatchesParam(bodyTy, c.returnTy, c.recPos(fn.Line, fn.Col), RecordingKindInterfaceTypedParam) {
					matched = true
				}
			}
			if !matched {
				c.report(returnMismatchError(fn, c.typef(
					"return type mismatch: expected %s, got %s", c.returnTy, bodyTy)).WithHint(c.wholeResultHint(c.returnTy, bodyTy, tailExpr(fn.Body))).WithHint(embedsDowncastHint(c.returnTy, bodyTy)))
			}
		}
	}

	var impl *ast.ImplBlock
	if c.implBlock != nil {
		for _, item := range c.implBlock.Items {
			if item == ast.Node(fn) {
				impl = c.implBlock
			}
		}
	}
	c.checkSelfRecursion(fn, impl)
}

// checkDestructureParams type-checks every destructuring parameter in a
// function's param list, reusing the slot types buildFuncType already derived
// into ft.Params (index-aligned with params, including destructure slots): for
// each slot it rejects refutable patterns and runs checkPattern so the bound
// inner names are typed. It does NOT re-derive the slot type — buildFuncType is
// the sole reporter of the missing-annotation / generic-head derivation error
// (fix I-1; re-deriving here double-reported it). A nil slot means buildFuncType
// already errored on (and reported) that param's derivation, so we walk the
// pattern with nil to keep inner-binding symbols from dangling and append no
// error. Plain (non-destructure) params are untouched — their types come from
// the builder / type_builder. The lambda param paths derive types inline (they
// never go through buildFuncType) and so don't use this helper.
func (c *checker) checkDestructureParams(params []ast.Param, ft *FuncType) {
	for i, p := range params {
		if p.Destructure == nil {
			continue
		}
		// ft.Params is built one-to-one with params by buildFuncType; guard
		// the index defensively in case a caller ever passes a mismatched ft.
		var ty Type
		if ft != nil && i < len(ft.Params) {
			ty = ft.Params[i]
		}
		if ty == nil {
			// Derivation already failed and was reported in buildFuncType.
			// Walk the pattern with nil so inner-binding symbols don't dangle,
			// but skip refutability (it needs a resolved type) and add no error.
			c.checkPattern(p.Destructure, nil)
			continue
		}
		checkParamPatternRefutable(p.Destructure, ty, c.reg, c.addError)
		c.checkPattern(p.Destructure, ty)
	}
}

// checkAppFieldRead types `MyApp.logger`, an application-field read, and
// records it: AppReads for the IR builder, and a reference at the field name
// for the editor. The type name keeps the builder's reference to the struct;
// the field name refers to the field's declaration, typed as the field.
func (c *checker) checkAppFieldRead(n *ast.FieldAccess, read AppRead) Type {
	if c.fa.AppReads == nil {
		c.fa.AppReads = map[ast.Node]AppRead{}
		c.fa.AppReadsByPos = map[Pos]AppRead{}
	}
	fieldPos := Pos{Line: n.Field.Line, Col: n.Field.Col}
	c.fa.AppReads[n] = read
	c.fa.AppReadsByPos[fieldPos] = read
	ref := Symbol{Name: n.Field.Name, Kind: SymbolField, Pos: fieldPos}
	if _, structSym := appTypeNamedBy(c.fa, n.Object); structSym != nil {
		if member := structSym.Members[n.Field.Name]; member != nil && member.Kind == SymbolField {
			ref = *member
		}
	}
	ref.Type = read.Field.Type
	ref.AppFieldOf = read.App
	c.fa.References[fieldPos] = &ref
	return read.Field.Type
}

// checkWith checks the statement `with MyApp.logger = value`. The target
// must be an application field, and the value is checked against the field's
// declared type, which a `.Variant` value is resolved from. A `with` is a
// statement and has no value.
func (c *checker) checkWith(n *ast.With) Type {
	read, ok := appReadOf(c.fa, n.Target)
	if !ok {
		pos := Pos{Line: n.Line, Col: n.Col}
		if n.Target != nil && n.Target.Field != nil {
			pos = Pos{Line: n.Target.Field.Line, Col: n.Target.Field.Col}
		}
		c.addError(pos.Line, pos.Col, fmt.Sprintf("`with` replaces an application field, such as `MyApp.logger`; %s is not one", withTargetText(n.Target)))
		if n.Value != nil {
			c.checkNode(n.Value)
		}
		return TypeUnit
	}
	ty := c.checkAppFieldRead(n.Target, read)
	if n.Value == nil {
		return TypeUnit
	}
	valTy := c.checkNodeExpecting(n.Value, ty)
	if ty != nil && valTy != nil {
		if !c.argMatchesParam(valTy, ty, c.recPos(n.Line, n.Col), RecordingKindInterfaceTypedParam) {
			c.addError(n.Line, n.Col, c.typef("%s replacement expects %s, got %s", withTargetText(n.Target), ty, valTy))
		}
	}
	return TypeUnit
}

// withTargetText spells a `with` target as written, in backticks.
func withTargetText(target *ast.FieldAccess) string {
	if target == nil {
		return "`?`"
	}
	var spell func(ast.Node) string
	spell = func(n ast.Node) string {
		switch v := n.(type) {
		case *ast.TypeIdent:
			return v.Name
		case *ast.Ident:
			return v.Name
		case *ast.FieldAccess:
			if v.Field == nil {
				return spell(v.Object)
			}
			return spell(v.Object) + "." + v.Field.Name
		}
		return "?"
	}
	return "`" + spell(target) + "`"
}

// checkAppTypeMembers rejects an inherent member (a function or a `once`)
// of an application type that has a field's name: `MyApp.logger` always
// names the field, so such a member could never be reached.
func (c *checker) checkAppTypeMembers(typeName string, items []ast.Node) {
	if c.fa.ModuleScope == nil || typeName == "" {
		return
	}
	st := appStructOfSymbol(c.fa, c.fa.ModuleScope.Lookup(typeName))
	if st == nil {
		return
	}
	for _, item := range items {
		var name string
		var line, col int
		switch v := item.(type) {
		case *ast.FuncDef:
			name, line, col = v.Name, v.Line, v.Col
		case *ast.OnceBinding:
			name, line, col = v.Name, v.Line, v.Col
		default:
			continue
		}
		if _, isField := scopedField(st, name); isField {
			c.addError(line, col, fmt.Sprintf(
				"`%s` is an application type, so `%s.%s` reads its field `%s`; rename this member",
				st.Name, st.Name, name, name))
		}
	}
}

// calleeFuncDef resolves a Call.Func node to the *ast.FuncDef it ultimately
// names, or nil if the callee isn't a directly-resolvable function (e.g.
// it's a lambda, a parameter, a builtin, or a non-function symbol). Walks
// through import-proxy Resolved chains.
func (c *checker) calleeFuncDef(node ast.Node) *ast.FuncDef {
	var pos Pos
	switch n := node.(type) {
	case *ast.Ident:
		pos = Pos{Line: n.Line, Col: n.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: n.Line, Col: n.Col}
	default:
		return nil
	}
	sym, ok := c.fa.References[pos]
	if !ok {
		sym, ok = c.fa.Definitions[pos]
	}
	if !ok || sym == nil {
		return nil
	}
	real := sym
	for real.Resolved != nil {
		real = real.Resolved
	}
	if fn, ok := real.Node.(*ast.FuncDef); ok {
		return fn
	}
	return nil
}

// rejectBareNameDispatch reports a bare-name call whose name resolves to an
// interface method — `speak(dog)` where `speak` is an `impl Speech` block
// method or an interface default method — unless the checker has already
// resolved it as a same-owner implementation call. Interface-impl methods are
// reached through the interface or implementing type; bare names otherwise
// resolve only to ordinary module functions and locals.
//
// bareNameDispatchInterface returns "" — and this is a no-op — when the
// callee doesn't resolve to an interface method, including the case where
// a local `fn`/lambda shadows an impl-method name (that call is an ordinary
// call, not dispatch). Replaces the prior bare-name-dispatch *resolution*
// (and its ambiguity diagnostic): naming a qualifier subsumes the old
// cross-interface-ambiguity case entirely.
func (c *checker) rejectBareNameDispatch(ident *ast.Ident) bool {
	ifaceName := c.bareNameDispatchInterface(ident)
	if ifaceName == "" {
		return false
	}
	// Bare-name dispatch is rejected. Interface-impl methods are called through
	// a qualifier: `Iface.method(x)` or `Type.method(x)`. A bare `method(x)`
	// that resolves to an interface method is an error outside the receiver
	// type's own body — bare names otherwise resolve to ordinary module
	// functions / locals.
	c.addError(ident.Line, ident.Col, fmt.Sprintf(
		"bare-name dispatch is not supported: %q is an interface function (of %s); qualify the call as %s.%s(...) or Type.%s(...)",
		ident.Name, ifaceName, ifaceName, ident.Name, ident.Name))
	return true
}

// isModuleObject reports whether the object of a field access resolves to
// a module symbol — i.e. `X.member` is module-member access rather than
// field access on a value.
func (c *checker) isModuleObject(obj ast.Node) bool {
	var pos Pos
	switch o := obj.(type) {
	case *ast.Ident:
		pos = Pos{Line: o.Line, Col: o.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: o.Line, Col: o.Col}
	default:
		return false
	}
	sym := c.fa.References[pos]
	return sym != nil && sym.Kind == SymbolModule
}

func (c *checker) checkModuleMemberVisibility(obj ast.Node, member *ast.Ident, sym *Symbol) {
	if c == nil || c.fa == nil || member == nil || sym == nil || !c.isModuleObject(obj) {
		return
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if real == nil || real.Public {
		return
	}
	if real.SourceFile == "" || real.SourceFile == c.fa.FilePath {
		return
	}
	moduleName := moduleObjectName(obj)
	c.addError(member.Line, member.Col, fmt.Sprintf(
		"file '%s' has no member '%s'", moduleName, member.Name))
}

// reportMissingFileMember reports `file.member` when the object is a file API
// object and the file declares no root-level `member`. Returns false, and
// reports nothing, when the object is not a file API object or the member
// exists.
//
// A function or `once` declared in an `impl` block is not a file member: it is
// reached through its owner (`Duration.seconds`, `Int.to_string`), and the
// file's scope never holds it, so `duration.seconds` lands here like any other
// missing name. When the file declares a type that owns the name, the
// diagnostic says so and gives the owner spelling.
func (c *checker) reportMissingFileMember(n *ast.FieldAccess) bool {
	if c == nil || c.fa == nil || n == nil || n.Field == nil {
		return false
	}
	modScope := c.fileObjectScope(n.Object)
	if modScope == nil || modScope.LookupLocal(n.Field.Name) != nil {
		return false
	}
	e := errAt(n.Field, fmt.Sprintf("file '%s' has no member '%s'", moduleObjectName(n.Object), n.Field.Name))
	if hint := c.typeOwnedMemberHint(modScope, n.Field.Name); hint != "" {
		e = e.WithHint(hint)
	} else {
		e = e.WithHint(didYouMean(n.Field.Name, publicNames(modScope)))
	}
	c.report(e)
	return true
}

// fileObjectScope returns the member scope of the file API object a field
// access's object names (`duration` in `duration.seconds`), or nil when the
// object is not a file API object.
func (c *checker) fileObjectScope(obj ast.Node) *Scope {
	var pos Pos
	switch o := obj.(type) {
	case *ast.Ident:
		pos = Pos{Line: o.Line, Col: o.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: o.Line, Col: o.Col}
	default:
		return nil
	}
	return moduleScopeOfSym(c.fa.References[pos])
}

// typeOwnedMemberHint names the owner of `name` when a type or interface the
// file declares owns a function or `once` by that name, with the spelling that
// reaches it. Empty when no declared type owns it. Owners are tried in name
// order so the hint is the same on every run.
func (c *checker) typeOwnedMemberHint(modScope *Scope, name string) string {
	owners := make([]string, 0, len(modScope.Symbols))
	for ownerName, sym := range modScope.Symbols {
		if sym == nil || sym.Resolved != nil {
			continue
		}
		switch sym.Kind {
		case SymbolStruct, SymbolEnum, SymbolType, SymbolInterface:
			owners = append(owners, ownerName)
		}
	}
	sort.Strings(owners)
	for _, owner := range owners {
		sym := modScope.Symbols[owner]
		if member := sym.Members[name]; member != nil && member.Kind == SymbolOnce {
			return fmt.Sprintf("it belongs to %s, so read it as %s.%s", owner, owner, name)
		}
		origin, _ := nominalOrigin(sym.Type)
		method, _ := c.resolveTypeMethodSymbol(owner, name, origin)
		for method != nil && method.Resolved != nil {
			method = method.Resolved
		}
		if method == nil || method.OwningType != owner {
			continue
		}
		if method.IsImplMethod && method.ImplInterface != "" {
			return fmt.Sprintf("it implements %s for %s, so call it as %s.%s(...) or %s.%s(...)",
				method.ImplInterface, owner, owner, name, method.ImplInterface, name)
		}
		return fmt.Sprintf("it belongs to %s, so call it as %s.%s(...)", owner, owner, name)
	}
	return ""
}

func moduleObjectName(obj ast.Node) string {
	switch o := obj.(type) {
	case *ast.Ident:
		return o.Name
	case *ast.TypeIdent:
		return o.Name
	}
	return "<module>"
}

// implMethodIfacesFor returns the interface names that claim `fn` as an impl
// method — block-form impls only. It
// consults the file-local impl-block index first (the only index available in
// single-file analysis), then the project impl index — the
// (iface -> method -> []FuncDef) map IndexImplBlockFuncDefs populates from each
// `impl Iface for T { ... }` block's HEADER (cross-file + stdlib). This is the
// dispatch-resolution lookup for every impl method (stdlib, @derive/auto-Debug
// synthesis output, and hand-written blocks alike), whose FuncDef items carry
// no decorator.
func (c *checker) implMethodIfacesFor(fn *ast.FuncDef) []string {
	if fn == nil {
		return nil
	}
	// File-local impl blocks (built up front by indexFileImplBlocks) — the
	// only index available in single-file analysis for the file's own impls.
	if names := c.blockMethodIfaces[fn]; len(names) > 0 {
		out := append([]string(nil), names...)
		sort.Strings(out)
		return out
	}
	// Project-wide index — cross-file block impls (and stdlib) in BuildProject.
	if c.fa == nil || c.fa.ProjectImpls == nil {
		return nil
	}
	var out []string
	for iface, byMethod := range c.fa.ProjectImpls.IfaceMethodImpls {
		for _, f := range byMethod[fn.Name] {
			if f == fn {
				out = append(out, iface)
				break
			}
		}
	}
	sort.Strings(out) // deterministic order (map iteration is random)
	return out
}

// indexFileImplBlocks builds c.blockMethodIfaces from the file's interface-impl
// blocks, including those nested inside modules, mapping each method FuncDef to
// the interface(s) it impls. This is the per-file analogue of the project impl
// index; it lets dispatch resolution recognize a block method's owning
// interface in single-file analysis. A method appearing in multiple
// `impl Iface for T` blocks accumulates all owning interfaces.
func (c *checker) indexFileImplBlocks(nodes []ast.Node) {
	for _, n := range nodes {
		blk, ok := n.(*ast.ImplBlock)
		if !ok || blk.Interface == nil {
			continue
		}
		ifaceName := TypeExprBaseName(blk.Interface)
		if ifaceName == "" {
			continue
		}
		for _, item := range blk.Items {
			fn, ok := item.(*ast.FuncDef)
			if !ok {
				continue
			}
			if c.blockMethodIfaces == nil {
				c.blockMethodIfaces = make(map[*ast.FuncDef][]string)
			}
			c.blockMethodIfaces[fn] = append(c.blockMethodIfaces[fn], ifaceName)
		}
	}
}

// bareNameDispatchInterface resolves the interface name a bare-name
// dispatch callee belongs to. Two callee shapes:
//
//  1. impl-method callee — calleeFuncDef returns the *ast.FuncDef; its
//     owning `impl Iface for T` block names the interface.
//  2. default-method callee — calleeFuncDef returns nil (default methods
//     aren't impl-block items, they live inside the `interface` block).
//     The callee Ident resolves to a SymbolInterfaceMethod; its owning
//     interface is the SymbolInterface in module scope whose Members map
//     contains that exact method symbol.
//
// Returns "" when the callee resolves to a plain local `fn` (a shadowing
// definition) or can't be resolved at all.
func (c *checker) bareNameDispatchInterface(ident *ast.Ident) string {
	// Shape 1: impl-method FuncDef callee. implMethodIfacesFor recognizes the
	// method as a block-form `impl Iface for T { fn … }` item
	// (via the project impl index), so a bare call to a block-impl method is
	// rejected.
	if fn := c.calleeFuncDef(ident); fn != nil {
		if names := c.implMethodIfacesFor(fn); len(names) > 0 {
			return names[0]
		}
		// Resolved to a FuncDef that is not an impl method: a plain local
		// fn shadowing the impl method. Leave it alone.
		return ""
	}

	// Shape 2: default-method callee — resolve the Ident's symbol and,
	// if it's an interface method, find its owning interface. The call
	// site's Ident isn't always in fa.References (default-method
	// symbols are defined directly into module scope by
	// registerImplDefaults, and the builder doesn't always register
	// a back-reference for the bare call); fall back to a module-scope
	// lookup by name. The isDispatch gate already keyed on
	// DispatchNames[ident.Name], so a module-scope hit here is the same
	// symbol that gate consulted.
	pos := Pos{Line: ident.Line, Col: ident.Col}
	sym, ok := c.fa.References[pos]
	if !ok {
		sym, ok = c.fa.Definitions[pos]
	}
	if (!ok || sym == nil) && c.fa.ModuleScope != nil {
		sym = c.fa.ModuleScope.Lookup(ident.Name)
	}
	if sym == nil {
		return ""
	}
	real := sym
	for real.Resolved != nil {
		real = real.Resolved
	}
	if real.Kind != SymbolInterfaceMethod {
		return ""
	}
	if c.fa.ModuleScope == nil {
		return ""
	}
	for _, candidate := range c.fa.ModuleScope.Symbols {
		ic := candidate
		for ic.Resolved != nil {
			ic = ic.Resolved
		}
		if ic.Kind != SymbolInterface {
			continue
		}
		// Pointer identity, not name match: registerImplDefaults in
		// builder.go defines the interface's own *Symbol straight into
		// module scope, so the owning interface is the one whose Members
		// entry IS this exact symbol.
		if ic.Members[real.Name] == real {
			return ic.Name
		}
	}
	return ""
}

func (c *checker) checkImportVisibility(n *ast.ImportStmt) {
	// Only check selective imports (import models.{User, helper})
	if len(n.Names) == 0 {
		return
	}
	for i, nameNode := range n.Names {
		origName := ast.ImportNodeName(nameNode)
		// The binding lives in scope under its alias (if any), so look up by
		// the alias name but report the original name in the error.
		lookupName := origName
		if i < len(n.Aliases) && n.Aliases[i] != nil {
			lookupName = ast.ImportNodeName(n.Aliases[i])
		}
		bindNode := nameNode
		if i < len(n.Aliases) && n.Aliases[i] != nil {
			bindNode = n.Aliases[i]
		}
		line, col := nodeLineCol(bindNode)
		sym := c.fa.Definitions[Pos{Line: line, Col: col}]
		if sym == nil {
			sym = c.fa.ModuleScope.Lookup(lookupName)
		}
		if sym == nil {
			continue
		}
		real := sym
		if real.Resolved != nil {
			real = real.Resolved
		}
		// A name the file does not declare is bound to a placeholder, which
		// the builder already reported as missing; it is not private.
		if sym.Resolved == nil && sym.Kind == SymbolBinding {
			continue
		}
		// Another file's own import of a file is not an item of it: a file
		// publishes declarations and the items it re-exports, never a file.
		if real.Kind == SymbolModule {
			fileSegs := n.ModulePath
			if n.FileSegments > 0 && n.FileSegments <= len(fileSegs) {
				fileSegs = fileSegs[:n.FileSegments]
			}
			parts := make([]string, len(fileSegs))
			for j, seg := range fileSegs {
				parts[j] = ast.ImportNodeName(seg)
			}
			nameLine, nameCol := nodeLineCol(nameNode)
			c.addError(nameLine, nameCol, fmt.Sprintf(
				"`%s` is a file that `%s` imports, not an item it exports; import `%s` directly",
				origName, strings.Join(parts, "/"), origName))
			continue
		}
		if !real.Public {
			c.addError(nameNode.LineNum(), 1, fmt.Sprintf(
				"'%s' is private and cannot be imported", origName))
		}
	}
}

func (c *checker) checkOnce(n *ast.OnceBinding) {
	if n.Value == nil {
		return
	}
	// Position-based lookup handles nested once bindings (not in module scope).
	sym := c.fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = c.fa.ModuleScope.Lookup(n.Name)
	}
	// An annotated binding's symbol carries its declared type from the build
	// phase. The value is checked against it, as a local binding's is, so the
	// annotation supplies the expected type `.Variant` shorthand and empty
	// literals resolve against, and a value of another type is an error here
	// rather than a program the IR builder cannot lower.
	var declared Type
	if n.TypeAnnotation != nil && sym != nil {
		declared = sym.Type
	}
	check := func() Type {
		return c.checkExitless(onceExitless(n.Name), func() Type {
			if declared != nil {
				return c.checkNodeExpecting(n.Value, declared)
			}
			return c.checkNode(n.Value)
		})
	}
	var valTy Type
	if t := c.fa.Onces; t != nil {
		if _, ok := t.sites[n]; ok {
			t.checking = append(t.checking, n)
			valTy = check()
			t.checking = t.checking[:len(t.checking)-1]
			t.checked[n] = true
			if cycle := t.cycles[n]; cycle != nil {
				c.addError(n.Line, n.Col, onceCycleMessage(n.Name, cycle))
				return
			}
		} else {
			valTy = check()
		}
	} else {
		valTy = check()
	}
	if valTy == nil {
		return
	}
	if declared != nil {
		if err := c.unifyInto(declared, valTy, map[*TypeParam_]Type{}); err != nil {
			c.report(errAt(n.Value, c.typef(
				"type mismatch: expected %s, got %s", declared, valTy)).WithHint(defaultedFuncValueHint(declared, valTy, n.Value)).WithHint(embedsDowncastHint(declared, valTy)))
		}
		return
	}
	if n.TypeAnnotation == nil {
		c.checkLocallyDetermined(n.Name, n.Value, valTy, nil, n.Line, n.Col)
	}
	if sym != nil && sym.Type == nil {
		sym.Type = valTy
	}
}

func (c *checker) checkBlock(block *ast.Block) Type {
	if block == nil {
		return TypeUnit
	}
	return c.checkBlockStmts(block)
}

func (c *checker) checkBlockStmts(block *ast.Block) Type {
	if block == nil {
		return TypeUnit
	}
	defer c.enterBlockImports(block)()
	var last Type = TypeUnit
	for i, stmt := range block.Stmts {
		last = c.checkNode(stmt)
		c.checkNonFinalExprStmt(stmt, last, i == len(block.Stmts)-1)
	}
	return last
}

// enterBlockImports checks the rest of block under blockImportRegistry's
// registry, when it answers one, and returns what restores the enclosing one.
func (c *checker) enterBlockImports(block *ast.Block) func() {
	prev := c.reg
	if reg := c.blockImportRegistry(block); reg != nil {
		c.reg = reg
	}
	if reg := c.blockTypeRegistry(block); reg != nil {
		c.reg = reg
	}
	return func() { c.reg = prev }
}

// blockTypeRegistry answers the registry a block that declares types is
// checked under: the enclosing one plus each struct, enum, distinct type,
// alias and interface the block declares, at the type the type builder gave
// its symbol. Without it a block-local type was unknown to every annotation
// the checker resolves (`m: Meters = Meters(3)`, a lambda parameter's
// `d: Meters`, a turbofish), though its constructor and its nested fns'
// signatures saw it. It answers nil for a block that declares no type.
func (c *checker) blockTypeRegistry(block *ast.Block) *TypeRegistry {
	if c.fa == nil || block == nil {
		return nil
	}
	var reg *TypeRegistry
	for _, stmt := range block.Stmts {
		var name string
		var line, col int
		switch n := stmt.(type) {
		case *ast.StructDef:
			name, line, col = n.Name, n.Line, n.Col
		case *ast.EnumDef:
			name, line, col = n.Name, n.Line, n.Col
		case *ast.TypeDef:
			name, line, col = n.Name, n.Line, n.Col
		case *ast.TypeAlias:
			name, line, col = n.Name, n.Line, n.Col
		case *ast.InterfaceDef:
			name, line, col = n.Name, n.Line, n.Col
		default:
			continue
		}
		sym := c.fa.Definitions[Pos{Line: line, Col: col}]
		if sym == nil || sym.Type == nil {
			continue
		}
		if reg == nil {
			reg = NewChildTypeRegistry(c.reg)
		}
		reg.Register(name, sym.Type)
	}
	return reg
}

// blockImportRegistry answers the registry a block with `import` statements
// is checked under: the enclosing one plus every type those imports bind,
// registered as newChecker registers a file-level import's. Without it a
// block-imported type is unknown to type annotations and to the
// type-qualified member check, so `Json.Number(1.5)` passed with no such
// variant. It answers nil for a block that imports nothing.
func (c *checker) blockImportRegistry(block *ast.Block) *TypeRegistry {
	if c.fa == nil || len(c.fa.BlockImportScopes) == 0 {
		return nil
	}
	var reg *TypeRegistry
	for _, stmt := range block.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok {
			continue
		}
		scope := c.fa.BlockImportScopes[imp]
		if scope == nil {
			continue
		}
		if reg == nil {
			reg = NewChildTypeRegistry(c.reg)
		}
		// Sorted, so a name two imports bind registers the same declaration
		// on every run.
		names := make([]string, 0, len(scope.Symbols))
		for name, sym := range scope.Symbols {
			if sym.Node == ast.Node(imp) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			sym := scope.Symbols[name]
			real := sym
			for real.Resolved != nil {
				real = real.Resolved
			}
			switch real.Kind {
			case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
				if real.Type != nil {
					reg.Register(name, real.Type)
					registerQualifiedTypeMembers(reg, name, real)
				}
			case SymbolModule:
				registerQualifiedTypesFromScope(reg, name, moduleScopeOfSym(sym))
			}
		}
	}
	return reg
}

func (c *checker) checkNonFinalExprStmt(stmt ast.Node, ty Type, isTail bool) {
	if isTail || ty == nil || isUnitLike(ty) || isInfallibleLike(ty) {
		return
	}
	es, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return
	}
	if isIgnorableStatementExpr(es.Expr) {
		return
	}
	line, col := es.Line, es.Col
	if col == 0 {
		line, col = exprStartLineCol(es.Expr)
	}
	c.addError(line, col, fmt.Sprintf(
		"non-final expression has type %s, but only Unit results can be ignored — write `_ = ...` if intentional, use `dbg ...` to inspect it, or move it to the end to return it",
		ty))
}

func isInfallibleLike(t Type) bool {
	return resolveTypeVar(t) == TypeInfallible
}

func isIgnorableStatementExpr(expr ast.Node) bool {
	switch e := expr.(type) {
	case *ast.Dbg:
		return true
	case *ast.Assertion:
		return !e.Check
	case *ast.With:
		return true
	case *ast.Binary:
		if e.Op != "|>" {
			return false
		}
		switch right := ungroupExpr(e.Right).(type) {
		case *ast.Dbg:
			return right.Expr == nil
		case *ast.Assertion:
			return !right.Check
		default:
			return false
		}
	default:
		return false
	}
}

func exprStartLineCol(n ast.Node) (int, int) {
	switch e := n.(type) {
	case *ast.Call:
		return exprStartLineCol(e.Func)
	case *ast.Binary:
		return exprStartLineCol(e.Left)
	case *ast.Unary:
		return e.Line, e.Col
	case *ast.FieldAccess:
		return exprStartLineCol(e.Object)
	default:
		return nodeLineCol(n)
	}
}

// checkConcurrentBlock type-checks `concurrent { body }` — the
// structured-concurrency scope from spec §20. Semantically it's a
// lambda-shaped value: the body is a fresh expression scope whose
// last statement supplies the block's type, and `return`/`try`
// unwind to the block boundary (not the enclosing fn). The runtime
// behaviour (task spawning, cancellation cascade, safe-point checks)
// is layered on by the VM and rt; layer 1 only needs the type-check
// path to reach end-to-end.
//
// Structural rules — Rule 1 (spawn only inside concurrent), Rule 2
// (Task must be awaited), Rule 3 (Task cannot escape) — are enforced
// by analysis/concurrent_scope.go and analysis/task_lifetime.go,
// which run as separate passes after CheckTypes.
func (c *checker) checkConcurrentBlock(n *ast.ConcurrentBlock) Type {
	// `try` and `return` inside a concurrent block unwind to the block
	// boundary, not the enclosing fn, the same rule lambdas follow (Nomi
	// has no non-local returns). Push the boundary
	// before walking the body, restore on the way out.
	prevBoundary := c.tryBoundary
	c.tryBoundary = "concurrent"
	defer func() { c.tryBoundary = prevBoundary }()
	defer c.enterBoundaryExits()()

	// Clear `c.returnTy` for the duration of the body. The enclosing fn's
	// return type must not bleed into generic-call inference inside the
	// block: a `concurrent` block's tail expression flows up via its
	// binding's RHS, not via a `return` to the fn. Without this, an inner
	// `Task.await(...)` (whose return type is a generic `T`) gets `T` unified
	// against the fn's return type by checkGenericCall's "fall back to
	// enclosing return" fixup, which is the wrong direction here — the
	// block is a value, the fn return is the consumer of that value.
	prevReturnTy := c.returnTy
	c.returnTy = nil
	defer func() { c.returnTy = prevReturnTy }()

	if n.Body == nil {
		return TypeUnit
	}
	bodyTy := c.checkBoundaryBody(n.Body, nil, n.Line, n.Col)
	if bodyTy == nil {
		return TypeUnit
	}
	return bodyTy
}

// tryUnwind is one early-exit site's contribution to an INFERRING
// `try`-boundary (a `concurrent` block or a lambda), which is the only kind of
// boundary whose result type is not written down anywhere.
//
// Flavour is the operand's prelude enum name — a member of
// TryOperandTypeNames — and Err is the `Result`'s error type, nil for a
// `Maybe`. Line/Col locate the site so a diagnostic lands on it rather than on
// the block.
//
// Assertion marks the entries that are NOT `try` sites: an `assert` inside a
// `concurrent` block also propagates an `Err`, of `AssertionFailure`, and has
// contributed to this accumulator since it existed. It is flagged because the
// flavour/result check (checkInferredBoundaryFlavour) must speak only about
// `try`: a diagnostic reading "`try` cannot propagate…" pointed at an `assert`
// names a keyword the user did not write.
//
// An assertion inside a `concurrent` block exits the block as
// `Err(AssertionFailure)`, so checkInferredBoundaryFlavour requires the
// block's value to be a `Result`, and resolveBoundaryErr unifies its error
// side with AssertionFailure, as checkAssertionBoundaryAt does for a `fn`.
type tryUnwind struct {
	Flavour   string
	Err       Type
	Assertion bool
	Line, Col int
}

// checkBoundaryBody checks a concurrent block or lambda with fresh return and
// try accumulators. Explicit returns and fallthrough determine its result;
// try propagation must fit that result. Nested boundaries isolate both kinds
// of exit. line/col anchor the conflict diagnostic.
func (c *checker) checkBoundaryBody(body *ast.Block, expected Type, line, col int) Type {
	prev := c.tryUnwinds
	prevReturns := c.returnUnwinds
	acc := []tryUnwind{}
	returns := []boundaryReturn{}
	c.tryUnwinds = &acc
	c.returnUnwinds = &returns
	bodyTy := c.checkBlockExpecting(body, expected)
	c.tryUnwinds = prev
	c.returnUnwinds = prevReturns
	bodyTy = c.resolveBoundaryReturns(bodyTy, returns, line, col)
	return c.resolveBoundaryErr(bodyTy, acc, line, col)
}

// checkLambdaBody checks a lambda's body as a boundary, its tail against
// expected (lambdaTailExpected). The type staged for the LAMBDA's own
// position, a function type, is not the body's: a `.Variant` in the body
// resolves against expected or reports that nothing names its enum, never
// against the callback type around it.
func (c *checker) checkLambdaBody(n *ast.Lambda, expected Type) Type {
	prevAt, prevEnum := c.expectedAt, c.expectedEnum
	c.expectedAt, c.expectedEnum = nil, nil
	defer func() { c.expectedAt, c.expectedEnum = prevAt, prevEnum }()
	return c.checkBoundaryBody(n.Body, expected, n.Line, n.Col)
}

// lambdaTailExpected is the type a callback's body tail is checked against:
// the callee's declared result when it is concrete, so `|a, b| .Less` passed
// as a `(T, T) -> Ordering` resolves its dot variant as a fn body ending in
// `.Less` does. A result still holding a type parameter or an unsolved
// variable is nil: the body is what solves it, and checkLambdaExpecting
// unifies the body's type against it afterwards. A Unit result is nil too: a
// callback for `Iter.each` may end in any expression and discard it.
func lambdaTailExpected(ret Type) Type {
	if ret == nil || ContainsTypeParam(ret) || containsTypeVar(ret) || isUnitLike(ret) {
		return nil
	}
	return ret
}

type boundaryReturn struct {
	ty        Type
	line, col int
}

// A return statement diverges from its block, but its operand is a result of
// the enclosing boundary. Reconcile those values with the fallthrough result
// before checking the boundary's try propagation.
func (c *checker) resolveBoundaryReturns(bodyTy Type, returns []boundaryReturn, line, col int) Type {
	result := bodyTy
	declared := false
	// A concrete contextual result is the common target, including an interface
	// implemented by different return values. Generic contexts still infer from
	// the values; their unsolved parameters are not a concrete result choice.
	if len(returns) > 0 && c.returnTy != nil && !ContainsTypeParam(c.returnTy) && !containsTypeVar(c.returnTy) {
		result = c.returnTy
		declared = true
		returns = append([]boundaryReturn{{bodyTy, line, col}}, returns...)
	}
	for _, ret := range returns {
		if ret.ty == nil || isInfallibleLike(ret.ty) {
			continue
		}
		if result == nil || isInfallibleLike(result) {
			result = ret.ty
			continue
		}
		if err := c.unify(result, ret.ty, nil); err != nil && !TypesEqual(result, ret.ty) {
			c.addError(ret.line, ret.col, c.typef(
				"return type mismatch: boundary returns %s, got %s", result, ret.ty))
		} else if !declared {
			result = embedsJoin(result, ret.ty)
		}
	}
	return result
}

func (c *checker) recordBoundaryReturn(n *ast.Return, ty Type) {
	if c.returnUnwinds != nil {
		*c.returnUnwinds = append(*c.returnUnwinds, boundaryReturn{ty, n.Line, n.Col})
	}
}

// resolveBoundaryErr applies spec §9's boundary rule to a boundary that
// INFERS its own result, which is the half checkTryBoundary cannot answer: a
// `concurrent` block and a lambda declare no result type, so the rule is
// checked against the result the body produced.
//
// bodyTy is the boundary's body/result type; unwinds are its `try` sites.
//
//   - Clause 1 and 2 (the result must be a `Result`/`Maybe`, and the flavours
//     must agree) are reported at the offending `try`. Without them a lambda
//     whose result is `Int` could carry a propagating `try` and hand an
//     `Err`/`None` to a caller expecting an `Int` — the same soundness hole
//     checkTryBoundary closes for a `fn`, reached through the boundary that has
//     no declaration to check against.
//   - Clause 3 (the error types) is the ORIGINAL behaviour, unchanged: the
//     accumulated error types are unified together, then bound into the
//     result's error side when it is still an unresolved variable, or
//     validated against it when it is already concrete. This direction is why
//     `Ok(try f())` in a `concurrent` block needs no annotation.
//
// A boundary with no `try` sites, and a nil bodyTy, pass through unchanged.
func (c *checker) resolveBoundaryErr(bodyTy Type, unwinds []tryUnwind, line, col int) Type {
	if len(unwinds) == 0 || bodyTy == nil {
		return bodyTy
	}
	rt, resultIsEnum := resolveTypeVar(bodyTy).(*EnumType)
	if !c.checkInferredBoundaryFlavour(bodyTy, rt, resultIsEnum, unwinds) {
		return bodyTy
	}
	// Only a `Result` boundary has an error side to resolve. A `Maybe` one
	// accumulated no error types, and a non-enum result reaches here only when
	// every accumulated entry was an ASSERTION (which the flavour check waves
	// through by design), so `rt` may legitimately be nil.
	if !resultIsEnum || rt.Name != "Result" || len(rt.TypeArgs) != 2 {
		return bodyTy
	}
	errTypes := make([]Type, 0, len(unwinds))
	for _, u := range unwinds {
		if u.Err != nil {
			errTypes = append(errTypes, u.Err)
		}
	}
	if len(errTypes) == 0 {
		return bodyTy
	}
	// Unify the accumulated `try` error types together; a conflict means the
	// boundary's error type would be ambiguous, which is ill-typed.
	merged := errTypes[0]
	for _, e := range errTypes[1:] {
		if c.unify(merged, e, nil) != nil {
			c.addError(line, col, c.typef(
				"inconsistent error types in try expressions: %s vs %s", merged, e))
			return bodyTy
		}
	}
	// Bind the result's error side, or validate it if already concrete.
	errSide := resolveTypeVar(rt.TypeArgs[1])
	if _, isVar := errSide.(*TypeVar); isVar {
		_ = c.unify(errSide, merged, nil)
		// Collapse the bound variable to its concrete type in place, so
		// downstream uses see `Result<T, String>` rather than
		// `Result<T, ?var→String>` — matching what an explicit annotation
		// would have produced. (Some operand checks compare types without
		// following the TypeVar resolve chain.)
		rt.TypeArgs[1] = resolveTypeVar(rt.TypeArgs[1])
	} else if c.unify(errSide, merged, nil) != nil {
		c.addError(line, col, c.typef(
			"inconsistent error types in try expressions: %s vs %s", errSide, merged))
	}
	return bodyTy
}

// checkInferredBoundaryFlavour reports whether an inferring boundary's result
// can hold what its `try` sites propagate, emitting a diagnostic at the
// offending site when it cannot. Returns false when the caller must stop.
//
// Deliberately silent on two shapes rather than guessing:
//
//   - a result that is still an unresolved inference variable or a type
//     parameter — nothing has pinned it yet, and reporting here would beat
//     the position that will;
//   - `TypeInfallible`, which is what a body that only ever diverges produces.
//     Such a body has no result to disagree with. Explicit return values have
//     already contributed to the result through resolveBoundaryReturns.
//
// The Infallible test is POINTER IDENTITY and not TypesEqual, and that is the
// bug this function shipped with for one build: `TypesEqual` implements
// Infallible as a BOTTOM type (types.go: "Infallible is compatible with
// everything"), so `TypesEqual(anything, TypeInfallible)` is true and the
// guard swallowed every boundary it was meant to wave through only the
// divergent ones. Found because it silenced
// TestTryErrInference_ConflictingTryErrTypes_Rejected.
func (c *checker) checkInferredBoundaryFlavour(bodyTy Type, rt *EnumType, resultIsEnum bool, unwinds []tryUnwind) bool {
	resolved := resolveTypeVar(bodyTy)
	switch resolved.(type) {
	case *TypeVar, *TypeParam_:
		return false
	}
	if resolved == Type(TypeInfallible) {
		return false
	}
	// An assertion exits its boundary as `Err(AssertionFailure)`, like a
	// `try`, so a `concurrent` block holding one must produce a `Result`.
	// Its error side is unified with AssertionFailure below.
	if !resultIsEnum || rt.Name != "Result" {
		for _, u := range unwinds {
			if u.Assertion {
				c.addError(u.Line, u.Col, fmt.Sprintf(
					"an assertion inside a concurrent block exits the block with Err(AssertionFailure), "+
						"but the block's value is %s: end the block with a Result<_, AssertionFailure> "+
						"(`Ok(...)`), or move the assertion out of the block", bodyTy))
				return false
			}
		}
	}
	// Only `try` sites are policed below.
	tries := make([]tryUnwind, 0, len(unwinds))
	for _, u := range unwinds {
		if !u.Assertion {
			tries = append(tries, u)
		}
	}
	if len(tries) == 0 {
		return true
	}
	kind := "lambda"
	if c.tryBoundary == "concurrent" {
		kind = "concurrent block"
	}
	if !resultIsEnum || !isTryOperandName(rt.Name) {
		u := tries[0]
		c.addError(u.Line, u.Col, fmt.Sprintf(
			"`try` cannot propagate a %s out of a %s whose result is %s: the %s must produce a Result or a Maybe",
			u.Flavour, kind, bodyTy, kind))
		return false
	}
	for _, u := range tries {
		if u.Flavour == rt.Name {
			continue
		}
		c.addError(u.Line, u.Col, fmt.Sprintf(
			"`try` on a %s cannot propagate out of a %s whose result is %s: the %s must produce a %s",
			u.Flavour, kind, bodyTy, kind, u.Flavour))
		return false
	}
	return true
}

// checkDefer type-checks `defer call(...)`.
func (c *checker) checkDefer(n *ast.Defer) Type {
	call, ok := n.Call.(*ast.Call)
	if !ok {
		c.addError(n.Line, n.Col, "`defer` requires a function call")
		if n.Call != nil {
			c.checkNode(n.Call)
		}
		return TypeUnit
	}
	callTy := c.checkCall(call, nil)
	c.fa.recordExprType(call, callTy)
	if callTy != nil && !TypesEqual(resolveTypeVar(callTy), TypeUnit) {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"`defer` call must return Unit, got %s", callTy))
	}
	return TypeUnit
}

func (c *checker) checkAssertion(n *ast.Assertion) Type {
	if n.Expr == nil {
		kw := assertionKeyword(n)
		c.addError(n.Line, n.Col, fmt.Sprintf("`%s` requires an expression", kw))
		return nil
	}
	ty := c.checkNode(n.Expr)
	return c.checkAssertionSubject(n, ty)
}

func (c *checker) checkAssertionSubject(n *ast.Assertion, ty Type) Type {
	kw := assertionKeyword(n)
	successTy, ok := c.assertionSuccessType(ty, n.Refute, n.Line, n.Col)
	if ty != nil && !ok {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"`%s` expects Bool, Result, Maybe, or Assertable, got %s", kw, ty))
	}
	if n.Check {
		resultTy := c.resultType(successTy, c.assertionFailureType())
		c.registerAssertionHoverAt(n.Line, n.Col, n, kw, ty, successTy, resultTy)
		return resultTy
	}
	c.registerAssertionHoverAt(n.Line, n.Col, n, kw, ty, successTy, successTy)
	if c.tryUnwinds != nil && c.tryBoundary != "lambda" {
		*c.tryUnwinds = append(*c.tryUnwinds, tryUnwind{
			Flavour: "Result", Err: c.assertionFailureType(), Assertion: true,
			Line: n.Line, Col: n.Col,
		})
	} else {
		c.checkAssertionBoundary(n, kw)
	}
	return successTy
}

func assertionKeyword(n *ast.Assertion) string {
	if n.Check {
		return "check"
	}
	if n.Refute {
		return "refute"
	}
	return "assert"
}

func (c *checker) assertionSuccessType(ty Type, refute bool, line, col int) (Type, bool) {
	if ty == nil {
		return TypeUnit, true
	}
	if TypesEqual(ty, TypeBool) {
		return TypeBool, true
	}
	if et, ok := resolveTypeVar(ty).(*EnumType); ok {
		switch et.Name {
		case "Result":
			if len(et.TypeArgs) >= 2 {
				return ty, true
			}
		case "Maybe":
			if len(et.TypeArgs) >= 1 {
				return ty, true
			}
		}
	}
	if c.typeImplementsInterface(ty, "Assertable", c.recPos(line, col), RecordingKindInterfaceTypedParam) {
		return ty, true
	}
	return TypeUnit, false
}

func (c *checker) registerAssertionHoverAt(line, col int, node ast.Node, kw string, inputTy, successTy, resultTy Type) {
	if line <= 0 || inputTy == nil {
		return
	}
	c.fa.References[Pos{Line: line, Col: col}] = &Symbol{
		Name: kw,
		Kind: SymbolAssertion,
		Pos:  Pos{Line: line, Col: col},
		Node: node,
		Span: len(kw),
		Assertion: &AssertionInfo{
			Keyword:   kw,
			InputTy:   inputTy,
			SuccessTy: successTy,
			ResultTy:  resultTy,
			Boundary:  c.tryBoundary,
		},
	}
}

func (c *checker) checkDbg(n *ast.Dbg) Type {
	if n.Expr == nil {
		c.addError(n.Line, n.Col, "`dbg` requires an expression unless it is used as a pipe stage")
		return nil
	}
	return c.checkNode(n.Expr)
}

func (c *checker) checkDbgExpecting(n *ast.Dbg, expected Type) Type {
	if n.Expr == nil {
		c.addError(n.Line, n.Col, "`dbg` requires an expression unless it is used as a pipe stage")
		return nil
	}
	return c.checkNodeExpecting(n.Expr, expected)
}

func (c *checker) checkAssertionBoundary(n *ast.Assertion, kw string) {
	c.checkAssertionBoundaryAt(n.Line, n.Col, kw)
}

func (c *checker) checkAssertionBoundaryAt(line, col int, kw string) {
	if c.rejectExitlessExit(kw, line, col) {
		return
	}
	// A lambda is a boundary of its own — `assert` unwinds to it exactly as
	// `return` does — so the failure becomes the lambda's *value*, and
	// whatever invoked the lambda decides what happens to it. For
	// `Iter.each` and friends that decision is to discard it, and since a
	// callback typed `-> Unit` accepts any result, nothing downstream
	// objects either. The assertion then passes no matter what it says,
	// which is worse than not writing it at all.
	//
	// Checked ahead of the return-type rule because a lambda's return type
	// is often inferred as something plausible; the problem is the boundary,
	// not the type.
	if c.tryBoundary == "lambda" {
		c.addError(line, col, fmt.Sprintf(
			"`%s` inside a lambda becomes the lambda's return value instead of failing "+
				"the test, and the caller discards it — move it out of the lambda, or "+
				"have the lambda return what you want to assert about", kw))
		return
	}
	returnTy := c.returnTy
	if returnTy == nil && c.currentFnName != "" {
		returnTy = TypeUnit
	}
	if returnTy == nil {
		return
	}
	errTy := c.assertionFailureType()
	rt, ok := resolveTypeVar(returnTy).(*EnumType)
	if !ok || rt.Name != "Result" || len(rt.TypeArgs) != 2 {
		c.addError(line, col, fmt.Sprintf(
			"`%s` can return AssertionFailure, but the enclosing function returns %s", kw, returnTy))
		return
	}
	if c.unify(rt.TypeArgs[1], errTy, nil) != nil {
		c.addError(line, col, fmt.Sprintf(
			"`%s` can return AssertionFailure, but the enclosing function returns Result<_, %s>",
			kw, rt.TypeArgs[1]))
	}
}

func (c *checker) resultType(okTy, errTy Type) Type {
	if c.reg != nil {
		if et, ok := c.reg.Lookup("Result").(*EnumType); ok {
			return &EnumType{
				Origin:           et.Origin,
				Name:             et.Name,
				Variants:         et.Variants,
				TypeParams:       et.TypeParams,
				TypeParamDefs:    et.TypeParamDefs,
				TypeArgs:         []Type{okTy, errTy},
				Opaque:           et.Opaque,
				OwningSourceFile: et.OwningSourceFile,
			}
		}
	}
	if c.fa != nil && c.fa.ModuleScope != nil {
		if sym := c.lookupEnumSymByName("Result"); sym != nil {
			if et, ok := sym.Type.(*EnumType); ok {
				return &EnumType{
					Origin:           et.Origin,
					Name:             et.Name,
					Variants:         et.Variants,
					TypeParams:       et.TypeParams,
					TypeParamDefs:    et.TypeParamDefs,
					TypeArgs:         []Type{okTy, errTy},
					Opaque:           et.Opaque,
					OwningSourceFile: et.OwningSourceFile,
				}
			}
		}
	}
	return &EnumType{Name: "Result", TypeArgs: []Type{okTy, errTy}}
}

// assertionFailureType is the ERROR side of an assertion's Result: what
// `testing.check` returns as `Err`, and what a `try`-propagated `assert`
// carries.
//
// The CANONICAL declaration wins over every scope, and that is the fix rather
// than a preference. std/testing declares
// `check<T>(subject: T): Result<T, AssertionFailure>` and that name is resolved
// in std/testing's scope, which imports std/assertions.AssertionFailure — the
// calling file's scope is not asked at the declaration and must not be asked
// here. Consulting it made a caller's own `struct AssertionFailure` retype
// check's error, so a field read on it type-checked and then died at run time;
// see analysis.TypeAssertionFailure.
//
// The two scope lookups are retained BELOW the canonical one for the builds
// that have no std — unit tests over a hand-built registry, where
// TypeAssertionFailure is still the fallback PrimitiveType and a registry entry
// is the only real answer available.
func (c *checker) assertionFailureType() Type {
	if _, unresolved := TypeAssertionFailure.(*PrimitiveType); !unresolved {
		return TypeAssertionFailure
	}
	if c.reg != nil {
		if ty := c.reg.Lookup("AssertionFailure"); ty != nil {
			return ty
		}
	}
	if c.fa != nil && c.fa.ModuleScope != nil {
		if sym := c.fa.ModuleScope.Lookup("AssertionFailure"); sym != nil && sym.Type != nil {
			return sym.Type
		}
	}
	return TypeAssertionFailure
}

func (c *checker) checkTestDecl(n *ast.TestDecl) Type {
	return c.checkTestDeclWithContext(n, TypeUnit)
}

func (c *checker) checkAttachedTests(n ast.Node) {
	for _, t := range attachedTestsOf(n) {
		if t.Body == nil {
			continue
		}
		prevAttachedTestScope := c.attachedTestScope
		prevBoundaryOverride := c.testBoundaryOverride
		line, col := nodeLineCol(n)
		if c.fa != nil {
			c.attachedTestScope = c.fa.ScopeAt(Pos{Line: line, Col: col})
		}
		c.testBoundaryOverride = "attached test for " + attachedTestBoundaryOwnerName(n)
		name := "//! test"
		c.checkTestDecl(&ast.TestDecl{
			Name: name,
			Body: t.Body,
			Line: t.Line,
			Col:  t.Col,
		})
		c.attachedTestScope = prevAttachedTestScope
		c.testBoundaryOverride = prevBoundaryOverride
	}
}

func attachedTestBoundaryOwnerName(n ast.Node) string {
	switch v := n.(type) {
	case *ast.FuncDef:
		return v.Name
	case *ast.ExternFunc:
		return v.Name
	case *ast.StructDef:
		return v.Name
	case *ast.EnumDef:
		return v.Name
	case *ast.TypeDef:
		return v.Name
	case *ast.ExternType:
		return v.Name
	case *ast.TypeAlias:
		return v.Name
	case *ast.InterfaceDef:
		return v.Name
	case *ast.ImplBlock:
		return "impl"
	case *ast.ImplConformance:
		return "impl"
	case *ast.OnceBinding:
		return v.Name
	default:
		return "declaration"
	}
}

func attachedTestsOf(n ast.Node) []ast.AttachedTest { return ast.AttachedTestsOf(n) }

// checkTestClock type-checks a `clock …` declaration against
// `testing.Clock`.
//
// Staging the expected enum is what lets the `.Virtual` shorthand resolve
// without naming the type, and it is also what makes every other
// spelling behave like ordinary Nomi: `Clock.Virtual` needs std/testing
// imported, a misspelled variant is an unknown-variant error, and a
// non-clock expression is a type mismatch. The alternative considered was
// resolving the variant syntactically in the test runner, which would
// have accepted `Clock.Virtual` with no import at all — a reference that
// only looked like one.
func (c *checker) checkTestClock(n *ast.TestDecl) {
	if n.Clock == nil {
		return
	}
	et, ok := c.lookupClockEnum()
	if !ok {
		// The enum is only in the registry once std/testing is imported.
		// Skipping the check when it is absent would make `clock` the one
		// place in Nomi where a name resolves out of thin air, so require
		// the import instead.
		c.addError(n.ClockLine, n.ClockCol,
			"clock needs the Clock type in scope — add `std/testing.Clock` to your imports")
		return
	}
	ty := c.checkNodeExpecting(n.Clock, et)
	if ty == nil {
		return // the expression already reported its own error
	}
	if got, ok := ty.(*EnumType); !ok || !TypesEqual(got, et) {
		c.addError(n.Clock.LineNum(), n.ClockCol, fmt.Sprintf(
			"clock must be a %s, got %s — write `clock Clock.Virtual` or `clock Clock.System`",
			et.Name, typeString(ty)))
	}
}

func (c *checker) lookupClockEnum() (*EnumType, bool) {
	if et, ok := c.reg.Lookup("Clock").(*EnumType); ok {
		return et, true
	}
	if et, ok := c.reg.Lookup("testing.Clock").(*EnumType); ok {
		return et, true
	}
	return nil, false
}

func (c *checker) checkTestDeclWithContext(n *ast.TestDecl, contextTy Type) Type {
	if n.NameLine > 0 && strings.TrimSpace(n.Name) == "" {
		// A test report names each case, and a group's name prefixes its
		// cases', so a blank name would report nothing a reader can find.
		msg := "a test name must not be empty; name the test for what it checks, such as `test \"parses a date\"`"
		if n.Group {
			msg = "a `tests` group name must not be empty; name the group for what its tests share, such as `tests \"dates\"`"
		}
		c.addError(n.NameLine, n.NameCol, msg)
	}
	if n.Group {
		c.checkTestClock(n)
		if n.Boot != nil {
			c.checkTestBoot(n)
		}
		var groupContextTy Type = TypeUnit
		if n.Setup != nil {
			// A setup body runs inside each case it serves, so a `try` or an
			// assertion in it ends that case, as one in the case's body does.
			// A `return value` in it ends the setup with that value, as a
			// lambda's does, so its result joins the tail with every return.
			prevBoundary := c.tryBoundary
			prevReturns, prevReturnTy := c.returnUnwinds, c.returnTy
			returns := []boundaryReturn{}
			c.tryBoundary = fmt.Sprintf("setup of tests %q", n.Name)
			c.returnUnwinds, c.returnTy = &returns, nil
			groupContextTy = c.checkNode(n.Setup)
			c.tryBoundary = prevBoundary
			c.returnUnwinds, c.returnTy = prevReturns, prevReturnTy
			line, col := n.SetupLine, n.SetupCol
			if line <= 0 {
				line, col = nodeLineCol(n.Setup)
			}
			groupContextTy = c.resolveBoundaryReturns(groupContextTy, returns, line, col)
			if groupContextTy == nil {
				groupContextTy = TypeUnit
			}
			c.registerTestSetupHover(n, TypeUnit, groupContextTy)
		}
		c.registerTestDeclHover(n, TypeUnit, groupContextTy)
		if n.Body != nil {
			for _, stmt := range n.Body.Stmts {
				if child, ok := stmt.(*ast.TestDecl); ok {
					c.checkTestDeclWithContext(child, groupContextTy)
				}
			}
		}
		return TypeUnit
	}
	var testInputTy Type = TypeUnit
	if n.ContextPattern != nil {
		// The pattern binds the group's setup value, which has one shape,
		// so it must match whatever that value is.
		checkParamPatternRefutable(n.ContextPattern, contextTy, c.reg, func(line, col int, _ string) {
			c.addError(line, col, refutableTestPatternMsg)
		})
		c.checkPattern(n.ContextPattern, contextTy)
		testInputTy = contextTy
	}
	if c.testBoundaryOverride == "" {
		c.registerTestDeclHover(n, testInputTy, TypeUnit)
	}
	prevBoundary := c.tryBoundary
	c.tryBoundary = fmt.Sprintf("test %q", n.Name)
	if c.testBoundaryOverride != "" {
		c.tryBoundary = c.testBoundaryOverride
	}
	defer func() { c.tryBoundary = prevBoundary }()
	c.checkBlock(n.Body)
	return TypeUnit
}

// refutableTestPatternMsg rejects a test pattern that may not match its
// group's setup value.
const refutableTestPatternMsg = "a test's pattern must match every setup value; bind it with a name or an irrefutable pattern and match it in the body"

// checkTestBoot checks a group's `boot` line: a call to an entry file's boot.
// The call is checked as an ordinary call against the boot's signature, so
// `boot server.boot()` needs a boot with no parameter or a defaulted one, and
// `boot server.boot(startup)` one whose parameter takes a Startup.
func (c *checker) checkTestBoot(n *ast.TestDecl) {
	call, ok := n.Boot.(*ast.Call)
	if !ok {
		c.addError(n.BootLine, n.BootCol, "`boot` in a `tests` group calls an entry's boot, such as `boot server.boot(startup)`")
		c.checkNode(n.Boot)
		return
	}
	c.checkNode(call)
	if TestGroupBoot(c.fa, n) == nil {
		c.addError(n.BootLine, n.BootCol, fmt.Sprintf(
			"`boot` in a `tests` group calls an entry's boot, the `fn boot` of a file that defines `fn main`, such as `boot server.boot(startup)`; %s is not one",
			"`"+calleeText(call.Func)+"`"))
	}
}

// calleeText spells a call's callee as written, for a diagnostic.
func calleeText(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.TypeIdent:
		return v.Name
	case *ast.FieldAccess:
		if v.Field == nil {
			return calleeText(v.Object)
		}
		return calleeText(v.Object) + "." + v.Field.Name
	}
	return "?"
}

func (c *checker) registerTestDeclHover(n *ast.TestDecl, inputTy, outputTy Type) {
	if n.Line <= 0 {
		return
	}
	kw := "test"
	if n.Group {
		kw = "tests"
	}
	c.fa.References[Pos{Line: n.Line, Col: n.Col}] = &Symbol{
		Name: kw,
		Kind: SymbolTestDecl,
		Pos:  Pos{Line: n.Line, Col: n.Col},
		Span: len(kw),
		TestDecl: &TestDeclInfo{
			Keyword:    kw,
			Name:       n.Name,
			InputTy:    inputTy,
			OutputTy:   outputTy,
			Group:      n.Group,
			HasSetup:   n.Setup != nil,
			HasContext: n.ContextPattern != nil,
		},
	}
}

// TestGroupSetupType is the type the checker gave group t's `setup`: the join
// of its tail value and its `return` values. It is nil when t has no setup or
// the checker recorded none.
func TestGroupSetupType(fa *FileAnalysis, t *ast.TestDecl) Type {
	if fa == nil || t == nil || t.Setup == nil || t.SetupLine <= 0 {
		return nil
	}
	sym := fa.References[Pos{Line: t.SetupLine, Col: t.SetupCol}]
	if sym == nil || sym.TestSetup == nil {
		return nil
	}
	return sym.TestSetup.OutputTy
}

func (c *checker) registerTestSetupHover(n *ast.TestDecl, inputTy, outputTy Type) {
	if n.SetupLine <= 0 {
		return
	}
	c.fa.References[Pos{Line: n.SetupLine, Col: n.SetupCol}] = &Symbol{
		Name: "setup",
		Kind: SymbolTestSetup,
		Pos:  Pos{Line: n.SetupLine, Col: n.SetupCol},
		Span: len("setup"),
		TestSetup: &TestSetupInfo{
			InputTy:  inputTy,
			OutputTy: outputTy,
			Group:    n.Name,
		},
	}
}

// checkBlockExpectingReturn checks a function body block, pushing the
// declared return type (c.returnTy) into the trailing expression so
// expected-type-aware nodes (list literals with `embeds` subtype coercion,
// lambdas needing param inference) see the context. Earlier statements are
// checked normally — they aren't the value the function returns.
func (c *checker) checkBlockExpectingReturn(block *ast.Block) Type {
	if block == nil {
		return TypeUnit
	}
	if c.returnTy == nil || len(block.Stmts) == 0 {
		return c.checkBlock(block)
	}
	defer c.enterBlockImports(block)()
	var last Type = TypeUnit
	for i, stmt := range block.Stmts {
		if i == len(block.Stmts)-1 {
			if es, ok := stmt.(*ast.ExprStmt); ok {
				if isUnitLike(c.returnTy) && EndsInDbg(es.Expr) {
					// A Unit body may end in a `dbg` observation: the value
					// dbg passes through is discarded, as it is for a dbg
					// statement before the end. Nothing expects its type,
					// so the operand is checked on its own.
					c.checkNode(es.Expr)
					last = TypeUnit
					continue
				}
				last = c.checkNodeExpecting(es.Expr, c.returnTy)
				continue
			}
		}
		last = c.checkNode(stmt)
		c.checkNonFinalExprStmt(stmt, last, false)
	}
	return last
}

// EndsInDbg reports whether expr is a `dbg` observation: `dbg x`, or a pipe
// whose last stage is a bare `dbg` (`x |> f() |> dbg`). A Unit body may end
// in one, and its value is discarded.
func EndsInDbg(expr ast.Node) bool {
	switch e := expr.(type) {
	case *ast.Dbg:
		return e.Expr != nil
	case *ast.Binary:
		if e.Op != "|>" {
			return false
		}
		dbg, ok := ungroupExpr(e.Right).(*ast.Dbg)
		return ok && dbg.Expr == nil
	}
	return false
}

// checkBlockExpecting checks a block, pushing `expected` into its trailing
// expression — the mirror of checkBlockExpectingReturn for an arbitrary expected
// type rather than the function return. Used for case/if arm bodies so a generic
// constructor in the tail (`{ ...; Ok(.Guess(n)) }`) sees the expected type and
// can resolve a dot-leading variant.
func (c *checker) checkBlockExpecting(block *ast.Block, expected Type) Type {
	if block == nil {
		return TypeUnit
	}
	if expected == nil || len(block.Stmts) == 0 {
		return c.checkBlock(block)
	}
	defer c.enterBlockImports(block)()
	var last Type = TypeUnit
	for i, stmt := range block.Stmts {
		if i == len(block.Stmts)-1 {
			if es, ok := stmt.(*ast.ExprStmt); ok {
				last = c.checkNodeExpecting(es.Expr, expected)
				continue
			}
		}
		last = c.checkNode(stmt)
		c.checkNonFinalExprStmt(stmt, last, false)
	}
	return last
}

// checkNodeExpecting checks a node with an expected type pushed down.
// For most nodes this delegates to checkNode; the key case is *ast.Lambda
// where the expected FuncType supplies parameter types for unannotated params.
//
// Dot-leading variant resolution piggy-backs on this hook: when the
// expected type is an *EnumType, we stage it on c.expectedEnum (defer-
// restored) so any `.Variant` / `.Variant{...}` / `.Variant(...)` inside
// the subtree can read it. The field persists through nested checkNode
// recursion, which is how propagation through transparent constructs
// (case/if arms, blocks, generic-fn args with returns unified to the
// outer expected) works automatically — those constructs call checkNode
// on inner expressions without re-pushing an expected type, so the
// outer expectedEnum remains visible at the leaf.
func (c *checker) checkNodeExpecting(node ast.Node, expected Type) Type {
	if expected != nil {
		c.fa.recordExpectedType(node, expected)
	}
	ty := c.rejectKeyWithoutEquality(node, c.checkNodeExpectingType(node, expected))
	c.fa.recordExprType(node, ty)
	return ty
}

// checkNodeExpectingType is checkNodeExpecting before the Set and Map key
// check.
func (c *checker) checkNodeExpectingType(node ast.Node, expected Type) Type {
	if node == nil {
		return nil
	}
	if expected == nil {
		return c.checkNode(node)
	}
	if ty, ok := c.checkTypeWitnessExpr(node, expected); ok {
		return ty
	}

	// Stage expected type for dot-leading variant resolution. Always push
	// when expected is non-nil (the fast-path above handled nil); the
	// expectedEnum cache is set only when expected is an EnumType.
	prevAt := c.expectedAt
	prevEnum := c.expectedEnum
	c.expectedAt = expected
	c.expectedEnum, _ = expected.(*EnumType)
	defer func() {
		c.expectedAt = prevAt
		c.expectedEnum = prevEnum
	}()

	// Dot-leading variant at expression position resolves via the
	// staged expected enum. Handled before the generic switch so the
	// resolution sees the freshly-staged c.expectedEnum (which may have
	// just been set above, when expected is *EnumType).
	if dv, ok := node.(*ast.DotVariant); ok {
		ty := c.checkDotVariant(dv)
		return c.rejectStructVariantValue(dv, ty)
	}
	// A field accessor `.name` is typed from the expected function type's
	// parameter (field_accessor.go).
	if acc, ok := node.(*ast.FieldAccessor); ok {
		return c.checkFieldAccessor(acc, expected)
	}

	switch n := node.(type) {
	case *ast.Ident, *ast.TypeIdent, *ast.FieldAccess:
		// A generic function or constructor named as a value is
		// instantiated against the expected function type
		// (generic_func_ref.go).
		prevRef := c.funcRefNode
		c.funcRefNode = n
		ty := c.checkNode(n)
		c.funcRefNode = prevRef
		return c.instantiateFuncRef(n, ty, expected)
	case *ast.GroupedExpr:
		return c.checkNodeExpecting(n.Expr, expected)
	case *ast.With:
		return c.checkWith(n)
	case *ast.Call:
		// Forward the expected type so a generic constructor's argument slots see
		// the concrete payload type (e.g. `.Quit` in `Result.Ok(.Quit)`).
		return c.checkCall(n, expected)
	case *ast.Binary:
		// Forward the expected type so a generic pipe tail (`n |> Result.Ok()`)
		// can pin a return param the piped arg leaves free. Non-pipe binaries
		// ignore it.
		return c.checkBinary(n, expected)
	case *ast.Case:
		// A case in expected position pushes that expectation into each arm body,
		// so `case … { … -> Ok(.Quit) }` resolves the construction per arm.
		return c.checkCase(n, expected)
	case *ast.TryOp:
		return c.checkTryOpExpecting(n, expected)
	case *ast.Block:
		// A block in expected position pushes it into the trailing expression.
		return c.checkBlockExpecting(n, expected)
	case *ast.If:
		// An if in expected position pushes it into each branch.
		return c.checkIf(n, expected)
	case *ast.Dbg:
		return c.checkDbgExpecting(n, expected)
	case *ast.Todo:
		return TypeInfallible
	case *ast.Lambda:
		expectedFT, ok := expected.(*FuncType)
		if !ok {
			// A type expected here that is not a function's (`f: Int =
			// |x| ...`) is the mismatch the position reports; an unknown
			// parameter type is not a second error.
			switch expected.(type) {
			case nil, *TypeVar, *TypeParam_:
				return c.checkLambda(n, true)
			}
			return c.checkLambda(n, false)
		}
		return c.checkLambdaExpecting(n, expectedFT)
	case *ast.NamedArg:
		// Named args wrap the underlying value; push expected through
		// so e.g. `transform(..., f: |x| x + 1)` infers x's type from
		// f's declared signature.
		return c.checkNodeExpecting(n.Value, expected)
	case *ast.ListLit:
		// Heterogeneous list literals need the expected element type pushed
		// down so `embeds` subtype coercion works at element granularity:
		// `[circle, rectangle, Shape.Point]: List<Shape>` should accept each
		// element where Shape is the unifying enum, rather than locking the
		// element type to whichever item appears first.
		if lt, ok := expected.(*ListType); ok {
			return c.checkListLitExpecting(n, lt.Elem)
		}
		// A collection literal is an `Iter` of its element type, so an
		// expected `Iter<T>` pushes T into the items as `List<T>` would:
		// `Iter.to_map([(.North, .Cave)])` expected to be
		// `Map<Direction, Place>` checks each tuple against
		// `(Direction, Place)`.
		if elemTy, ok := iterElemType(expected); ok {
			return c.checkListLitExpecting(n, elemTy)
		}
		return c.checkNode(n)
	case *ast.VectorLit:
		if elemTy, ok := vectorElemType(expected); ok {
			return c.checkVectorLitExpecting(n, elemTy)
		}
		if elemTy, ok := iterElemType(expected); ok {
			return c.checkVectorLitExpecting(n, elemTy)
		}
		return c.checkNode(n)
	case *ast.SetLit:
		if elemTy, ok := setElemType(expected); ok {
			return c.checkSetLitExpecting(n, elemTy)
		}
		if elemTy, ok := iterElemType(expected); ok {
			return c.checkSetLitExpecting(n, elemTy)
		}
		return c.checkNode(n)
	case *ast.TupleLit:
		// A tuple literal against a tuple type of its arity pushes each element
		// type into its item, so `(.North, .Cave)` checked against
		// `(Direction, Place)` resolves both variants.
		if tt, ok := resolveTypeVar(expected).(*TupleType); ok && len(tt.Elems) == len(n.Items) {
			return c.checkTupleLitExpecting(n, tt.Elems)
		}
		return c.checkNode(n)
	case *ast.StructLit:
		// A brace literal with no type name and no spread builds the
		// expected nominal struct (checkTargetTypedStructLit).
		if n.TypeName == nil && n.Spread == nil {
			if st, ok := resolveTypeVar(expected).(*StructType); ok {
				return c.checkTargetTypedStructLit(n, st)
			}
		}
		// An anonymous literal checked against an anonymous struct type
		// points its labels at that type's fields. Checking is unchanged.
		ty := c.checkNode(n)
		if lit := anonStructLitOf(n); lit != nil {
			if anon, ok := resolveTypeVar(expected).(*AnonStructType); ok {
				c.recordAnonLitFieldLabels(lit, anon)
			}
		}
		return ty
	case *ast.MapLit:
		// Annotated map literal — push expected K/V down to each entry so
		// dot-leading variant resolution sees the right expected enum at
		// value positions (`themes: Map<String, Color> = {"a" => .Red}`).
		// Skip the literal-attach path (n.TypeName != nil); that's the
		// variant/distinct construction site and routes through checkMapLit
		// independently.
		if mt, ok := expected.(*MapType); ok && n.TypeName == nil {
			return c.checkMapLitExpecting(n, mt.Key, mt.Val)
		}
		return c.checkNode(n)
	default:
		return c.checkNode(n)
	}
}

func (c *checker) checkTypeWitnessExpr(node ast.Node, expected Type) (Type, bool) {
	if !isTypeWitnessType(expected) {
		return nil, false
	}
	witnessExpr := typeExprFromWitnessExpr(node)
	if witnessExpr == nil {
		return nil, false
	}
	ty := &ast.GenericType{
		Name:   "Type",
		Params: []ast.TypeExpr{witnessExpr},
	}
	resolved, err := ResolveTypeExpr(ty, c.reg, c.fnTypeParams, c.fa.References)
	if err != nil {
		return c.scopedTypeWitness(node, expected)
	}
	return resolved, true
}

// scopedTypeWitness is the `Type<T>` witness for a type name the registry
// ResolveTypeExpr reads does not hold: a type declared in a block or an
// attached test (`//! type TraceId String`). Its symbol says it names a
// type; checkTypeIdent records the reference and gives its type, which is
// nil where the checker does not type the declaration. A name that is not
// a type is not a witness, and the caller checks it as a value.
func (c *checker) scopedTypeWitness(node ast.Node, expected Type) (Type, bool) {
	ti, ok := node.(*ast.TypeIdent)
	if !ok {
		return nil, false
	}
	pos := Pos{Line: ti.Line, Col: ti.Col}
	sym := c.fa.References[pos]
	if sym == nil {
		sym = c.fa.Definitions[pos]
	}
	if sym == nil && c.attachedTestScope != nil {
		sym = c.attachedTestScope.Lookup(ti.Name)
	}
	if sym == nil {
		return nil, false
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch sym.Kind {
	case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias:
	default:
		return nil, false
	}
	named := c.checkTypeIdent(ti)
	if named == nil {
		return nil, true
	}
	witness := *expected.(*DistinctType)
	witness.TypeArgs = []Type{named}
	return &witness, true
}

func isTypeWitnessType(ty Type) bool {
	dt, ok := ty.(*DistinctType)
	return ok && dt.Name == "Type" && len(dt.TypeArgs) == 1
}

func typeExprFromWitnessExpr(node ast.Node) ast.TypeExpr {
	switch n := node.(type) {
	case *ast.Ident:
		return &ast.SimpleType{Name: n.Name, Line: n.Line, Col: n.Col}
	case *ast.TypeIdent:
		return &ast.SimpleType{Name: n.Name, Line: n.Line, Col: n.Col}
	case *ast.FieldAccess:
		member := typeExprFromWitnessExpr(n.Field)
		if member == nil {
			return nil
		}
		switch o := n.Object.(type) {
		case *ast.Ident:
			return &ast.QualifiedType{
				Module:     o.Name,
				ModuleLine: o.Line,
				ModuleCol:  o.Col,
				Member:     member,
			}
		case *ast.TypeIdent:
			return &ast.QualifiedType{
				Module:     o.Name,
				ModuleLine: o.Line,
				ModuleCol:  o.Col,
				Member:     member,
			}
		case *ast.FieldAccess:
			prefix := typeWitnessNameFromFieldAccess(o)
			if prefix == "" {
				return nil
			}
			return &ast.QualifiedType{
				Module:     prefix,
				ModuleLine: n.Line,
				ModuleCol:  n.Col,
				Member:     member,
			}
		default:
			return nil
		}
	default:
		return nil
	}
}

func typeWitnessNameFromFieldAccess(n *ast.FieldAccess) string {
	if n == nil || n.Field == nil {
		return ""
	}
	var left string
	switch o := n.Object.(type) {
	case *ast.Ident:
		left = o.Name
	case *ast.TypeIdent:
		left = o.Name
	case *ast.FieldAccess:
		left = typeWitnessNameFromFieldAccess(o)
	default:
		return ""
	}
	if left == "" {
		return ""
	}
	return left + "." + n.Field.Name
}

// checkMapLitExpecting checks each map entry's key against the expected
// key type and value against the expected value type. Mirror of
// checkListLitExpecting for maps; same fallback to checkMapLit when
// the expected types are unresolved (TypeParam or TypeVar).
func (c *checker) checkMapLitExpecting(n *ast.MapLit, keyTy, valTy Type) Type {
	if keyTy == nil || valTy == nil || ContainsTypeParam(keyTy) || ContainsTypeParam(valTy) || containsTypeVar(keyTy) || containsTypeVar(valTy) {
		return c.checkMapLit(n)
	}
	for _, entry := range n.Entries {
		kt := c.checkNodeExpecting(entry.Key, keyTy)
		if kt != nil && !TypeAssignable(keyTy, kt) {
			c.addMismatch(n.Line, 1, keyTy, kt, c.typef("map key type mismatch: expected %s, got %s", keyTy, kt))
		}
		vt := c.checkNodeExpecting(entry.Value, valTy)
		if vt != nil && !TypeAssignable(valTy, vt) {
			c.addMismatch(n.Line, 1, valTy, vt, c.typef("map value type mismatch: expected %s, got %s", valTy, vt))
		}
	}
	return &MapType{Key: keyTy, Val: valTy}
}

func (c *checker) checkNode(node ast.Node) Type {
	ty := c.rejectKeyWithoutEquality(node, c.checkNodeType(node))
	c.fa.recordExprType(node, ty)
	return ty
}

// checkNodeType is checkNode before the Set and Map key check.
func (c *checker) checkNodeType(node ast.Node) Type {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *ast.IntLit:
		return TypeInt

	case *ast.FloatLit:
		return TypeFloat

	case *ast.DecimalLit:
		return TypeDecimal

	case *ast.CodepointLit:
		c.registerCodepointHover(n)
		return TypeCodepoint

	case *ast.RangeLit:
		return c.checkRangeLit(n)

	case *ast.StringLit:
		return TypeString

	case *ast.DotVariant:
		// Reached when a `.Variant` lands in a position that didn't push an
		// expected type through checkNodeExpecting (statement position,
		// unannotated binding RHS, etc.). The expected-enum path is handled
		// directly in checkNodeExpecting; this case emits the no-context
		// diagnostic when there's no staged enum to resolve against.
		ty := c.checkDotVariant(n)
		return c.rejectStructVariantValue(n, ty)

	case *ast.FieldAccessor:
		// Reached where no expected type was pushed: the no-function
		// diagnostic.
		return c.checkFieldAccessor(n, nil)

	case *ast.StringInterp:
		// All interpolation parts are checked, but result is always String.
		// The runtime stringifies each interpolated value via
		// Display.to_string, so record (T, Display) for each part with a
		// concrete static type. Existential parts (interface, type param,
		// type variable) defer to the runtime — same exclusion the other
		// conformance recorders use.
		for _, part := range n.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				partTy := c.checkNode(se.Expr)
				line, col := slotPosition(se)
				c.recordConformanceIfConcrete(partTy, "Display", c.recPos(line, col), RecordingKindCallSite)
			}
		}
		return TypeString

	case *ast.TaggedString:
		return c.checkTaggedString(n)

	case *ast.Ident:
		ty := c.checkIdent(n)
		if node != c.qualifierNode && c.isModuleObject(n) {
			// A file qualifier names the functions and types a file
			// declares; on its own (`io`, `x = io`) it has no value.
			c.report(errAt(n, fmt.Sprintf("`%s` is a file, not a value", n.Name)).
				WithHint(fmt.Sprintf("name one of its members, as in `%s.function(x)`", n.Name)))
			return nil
		}
		if node == c.funcRefNode {
			return ty
		}
		return c.rejectUninstantiatedFuncRef(n, ty)

	case *ast.TypeIdent:
		ty := c.checkTypeIdent(n)
		if node == c.calleeNode || node == c.qualifierNode {
			return ty
		}
		if ft, isCtor := c.distinctCtorValue(n); isCtor {
			return ft
		}
		ty = c.rejectTypeNameValue(n, ty)
		if node == c.funcRefNode {
			return ty
		}
		return c.rejectUninstantiatedFuncRef(n, ty)

	case *ast.Binary:
		return c.checkBinary(n, nil)

	case *ast.GroupedExpr:
		return c.checkNode(n.Expr)

	case *ast.Unary:
		return c.checkUnary(n)

	case *ast.Call:
		return c.checkCall(n, nil)

	case *ast.Binding:
		return c.checkBinding(n)

	case *ast.TupleDestructure:
		return c.checkTupleDestructure(n)

	case *ast.StructDestructure:
		return c.checkStructDestructure(n)

	case *ast.MapDestructure:
		return c.checkMapDestructure(n)

	case *ast.PatternDestructure:
		return c.checkPatternDestructure(n)

	case *ast.PatternBinding:
		return c.checkPatternBinding(n)

	case *ast.DistinctDestructure:
		return c.checkDistinctDestructure(n)

	case *ast.Block:
		return c.checkBlock(n)

	case *ast.ConcurrentBlock:
		return c.checkConcurrentBlock(n)

	case *ast.With:
		return c.checkWith(n)

	case *ast.Defer:
		return c.checkDefer(n)

	case *ast.Assertion:
		return c.checkAssertion(n)

	case *ast.Placeholder:
		// `_` is a value only as a call's argument, where it leaves that
		// argument open (partial application, or the slot a pipe fills).
		if !c.callHoles[n] {
			c.report(errAt(n, placeholderValueMsg).WithHint(placeholderValueHint))
		}
		return nil

	case *ast.Dbg:
		return c.checkDbg(n)

	case *ast.Todo:
		// `todo` never produces a value, so it is the bottom type: it fits
		// whatever its position expects (checkNodeExpecting records that
		// expectation for the IR builder) and leaves a join to its other arms.
		return TypeInfallible

	case *ast.TestDecl:
		return c.checkTestDecl(n)

	case *ast.If:
		return c.checkIf(n, nil)

	case *ast.FieldAccess:
		ty := c.rejectStructVariantValue(n, c.checkFieldAccess(n))
		if node != c.calleeNode && node != c.qualifierNode {
			if ft, isCtor := c.distinctCtorValue(n); isCtor {
				return ft
			}
			ty = c.rejectTypeNameValue(n, ty)
		}
		if node == c.funcRefNode {
			return ty
		}
		return c.rejectUninstantiatedFuncRef(n, ty)

	case *ast.Return:
		return c.checkReturn(n)

	case *ast.ListLit:
		return c.checkListLit(n)

	case *ast.VectorLit:
		return c.checkVectorLit(n)

	case *ast.SetLit:
		return c.checkSetLit(n)

	case *ast.ListSpreadLit:
		return c.checkListSpreadLit(n)

	case *ast.TupleLit:
		return c.checkTupleLit(n)

	case *ast.MapLit:
		return c.checkMapLit(n)

	case *ast.Case:
		return c.checkCase(n, nil)

	case *ast.Lambda:
		return c.checkLambda(n, true)

	case *ast.TryOp:
		return c.checkTryOp(n)

	case *ast.ExprStmt:
		return c.checkNode(n.Expr)

	case *ast.OnceBinding:
		// Nested once binding inside a function body — same as top-level
		// checkOnce: infer the value type and attach to the symbol so hover
		// can show it. Top-level OnceBindings have their name validated by
		// checkNamingConventions; nested ones never reach that walk.
		c.validateBindingLikeName(n.Name, n.Line, n.Col, "once binding name")
		c.checkOnce(n)
		return TypeUnit

	case *ast.FuncDef:
		// A `fn` declared inside a body. Beside the OnceBinding arm above and
		// for the same reason: CheckTypes's walk never reaches it, so this is
		// the only place its body can be checked. See checkNestedFunc.
		c.checkNestedFunc(n)
		return TypeUnit

	case *ast.StructDef:
		// A `struct` declared inside a body. Beside the FuncDef arm above and
		// for the same reason: CheckTypes's walk sees only top-level nodes, so
		// a block-local declaration's field defaults reach the rule only here.
		// Without this arm `fn f() { struct Cfg { port: Int = "x" } ... }` is
		// the bypass for the whole check.
		c.checkStructDefFieldDefaults(n)
		// nil, not TypeUnit: these arms previously fell to the switch's
		// `default: return nil`, and a block whose final statement is a type
		// declaration would otherwise change its inferred result type.
		return nil

	case *ast.EnumDef:
		c.checkEnumDefFieldDefaults(n)
		return nil

	case *ast.StructLit:
		return c.checkStructLit(n)

	case *ast.Break:
		return c.checkBreak(n)

	case *ast.Continue:
		if c.rejectExitlessExit("continue", n.Line, n.Col) {
			if n.Value != nil {
				c.checkNode(n.Value)
			}
			return TypeInfallible
		}
		if n.Value != nil {
			c.checkNode(n.Value)
			c.addError(n.Line, n.Col, continueValueMessage)
		}
		return TypeInfallible

	default:
		return nil
	}
}

// unboundNameError is the error for an identifier bound nowhere in scope.
func (c *checker) unboundNameError(n *ast.Ident, testScope *Scope) TypeError {
	if e, ok := c.pipeLambdaBoundaryError(n); ok {
		return e
	}
	return undefinedVariableError(n, c.valueNamesAt(n.Line, n.Col, testScope))
}

func (c *checker) checkIdent(n *ast.Ident) Type {
	pos := Pos{Line: n.Line, Col: n.Col}

	var ty Type
	// Check references first (identifier usages). An item imported through
	// a facade that re-exports it (`import facade.{make_box}`) is a proxy
	// onto the facade's own proxy, so the chain is followed until it
	// reaches a typed symbol.
	if sym, ok := c.fa.References[pos]; ok {
		real := typedResolvedSymbol(sym)
		c.ensureOnceType(real)
		ty = real.Type
		if narrowed, isNarrowed := c.narrowed[sym]; isNarrowed {
			ty = narrowed
		}
	} else if sym, ok := c.fa.Definitions[pos]; ok {
		ty = typedResolvedSymbol(sym).Type
	} else if c.attachedTestScope != nil {
		if sym := c.attachedTestScope.Lookup(n.Name); sym != nil {
			c.fa.References[pos] = sym
			real := sym
			if real.Resolved != nil {
				real = real.Resolved
			}
			ty = real.Type
		}
		if ty == nil {
			c.report(c.unboundNameError(n, c.attachedTestScope))
		}
	} else {
		c.report(c.unboundNameError(n, nil))
	}

	// Still has unsolved (non-caller-scope) TypeParam_s — instantiate them
	// as fresh inference vars so downstream uses can resolve them. `None`
	// becomes `Maybe<?α>` rather than `Maybe<TypeParam_>`, which lets a
	// `case acc { Some(v) -> …; None -> … }` pattern resolve α from the
	// branch types.
	if _, ok := ty.(*EnumType); ok {
		ty = instantiateUnboundCalleeParams(ty, c.fnTypeParams, c)
	}
	if c.currentInterface != "" {
		if tp, ok := ty.(*TypeParam_); ok && tp.Name_ == "self" {
			if iface, ok := c.reg.Lookup(c.currentInterface).(*InterfaceType); ok {
				return interfaceSelfType(iface)
			}
		}
	}

	return ty
}

// typedResolvedSymbol is the symbol an import proxy stands for: sym's
// Resolved, then further along the chain while the symbol reached has no
// type, as a re-exported item's proxy has none.
func typedResolvedSymbol(sym *Symbol) *Symbol {
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	for real.Type == nil && real.Resolved != nil {
		real = real.Resolved
	}
	return real
}

func (c *checker) checkTypeIdent(n *ast.TypeIdent) Type {
	pos := Pos{Line: n.Line, Col: n.Col}

	var ty Type
	if sym, ok := c.fa.References[pos]; ok {
		// Reject bare construction of a same-file enum variant. Variants
		// must come through their enum (`Shape.Circle(5.0)`) or via a
		// drill-through import (`import shape.Shape.{Circle}`). The
		// same-file rule prevents silent collisions when two enums share
		// variant names. Locally lifted aliases (`sym.Resolved != nil`)
		// and prelude variants (`sym.Pos` not in this file's
		// `Definitions`) remain valid bare.
		if sym.Resolved == nil && sym.Kind == SymbolEnumVariant {
			if def, hasDef := c.fa.Definitions[sym.Pos]; hasDef && def == sym {
				enumName := c.findEnumForVariant(sym.Name)
				hint := ""
				if enumName != "" {
					hint = "; use '" + enumName + "." + sym.Name + "'"
				}
				c.errors = append(c.errors, TypeError{
					Line:    n.Line,
					Col:     n.Col,
					Message: "variant '" + sym.Name + "' must be qualified through its enum" + hint,
				})
			}
		}
		real := sym
		if real.Resolved != nil {
			real = real.Resolved
		}
		// Opaque-types: bare-variant reference (`Active` for a nullary
		// variant, used at value position) outside its owning module
		// exposes the variant shape. The non-nullary case is caught at
		// the call site by checkOpaqueEnumVariantCall; this branch
		// handles the bare-variant value case where the expression
		// itself produces the enum value.
		if real.Kind == SymbolEnumVariant {
			if et, ok := real.Type.(*EnumType); ok && et != nil && et.Opaque {
				if c.fa == nil || c.fa.FilePath != et.OwningSourceFile {
					c.addError(n.Line, n.Col, fmt.Sprintf(
						"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", et.Name))
				}
			}
		}
		ty = real.Type
	} else if sym, ok := c.fa.Definitions[pos]; ok {
		real := sym
		if real.Resolved != nil {
			real = real.Resolved
		}
		ty = real.Type
	} else if c.attachedTestScope != nil {
		if sym := c.attachedTestScope.Lookup(n.Name); sym != nil {
			c.fa.References[pos] = sym
			real := sym
			if real.Resolved != nil {
				real = real.Resolved
			}
			ty = real.Type
		}
		if ty == nil {
			if IsSynthesizedLine(n.Line) && synthSupportNames[n.Name] {
				return nil
			}
			c.errors = append(c.errors, TypeError{
				Line:    n.Line,
				Col:     n.Col,
				Message: "undefined type or variant '" + n.Name + "'",
			}.WithHint(didYouMean(n.Name, c.typeNamesAt(n.Line, n.Col))))
		}
	} else {
		// derive Synthesized code (position in the synth band) referencing
		// a name from the closed compiler-known support set — protocol
		// interfaces, Ordering and its variants, the Bool singletons —
		// resolves through the compiler-known route, not the deriving
		// file's imports: stay silent and let the body check proceed
		// untyped (the checker is nil-lenient). Any OTHER unresolved name
		// at a synthesized position still errors, so a synthesizer bug
		// fails loudly.
		if IsSynthesizedLine(n.Line) && synthSupportNames[n.Name] {
			return nil
		}
		c.errors = append(c.errors, TypeError{
			Line:    n.Line,
			Col:     n.Col,
			Message: "undefined type or variant '" + n.Name + "'",
		}.WithHint(didYouMean(n.Name, c.typeNamesAt(n.Line, n.Col))))
	}

	// Still has unsolved (non-caller-scope) TypeParam_s — instantiate them
	// as fresh inference vars so downstream uses can resolve them.
	if _, ok := ty.(*EnumType); ok {
		ty = instantiateUnboundCalleeParams(ty, c.fnTypeParams, c)
	}

	return ty
}

// interfaceObjectType returns the interface type referred to by the `Object`
// of a field access, covering both the case where the checked type is
// already an *InterfaceType and the case where it's an imported TypeIdent
// whose proxy sym has Type=nil (requiring a Resolved lookup).
func interfaceObjectType(c *checker, obj ast.Node, objTy Type) (*InterfaceType, bool) {
	if it, ok := objTy.(*InterfaceType); ok {
		return it, true
	}
	var pos Pos
	switch o := obj.(type) {
	case *ast.Ident:
		pos = Pos{Line: o.Line, Col: o.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: o.Line, Col: o.Col}
	default:
		return nil, false
	}
	sym, ok := c.fa.References[pos]
	if !ok {
		sym, ok = c.fa.Definitions[pos]
	}
	if !ok || sym == nil {
		return nil, false
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if it, ok := real.Type.(*InterfaceType); ok {
		return it, true
	}
	return nil, false
}

// specializeInterfaceMethod returns the func type with the interface's own
// type parameters substituted from the argument's InterfaceType's TypeArgs,
// when the callee is an interface method access whose first arg is itself
// typed as that same interface. Non-interface-method calls pass through
// unchanged.
func (c *checker) specializeInterfaceMethod(fn ast.Node, ft *FuncType, firstArg ast.Node) *FuncType {
	fa, ok := fn.(*ast.FieldAccess)
	if !ok {
		return ft
	}
	iface, ok := interfaceObjectType(c, fa.Object, nil)
	if !ok {
		return ft
	}
	if len(iface.TypeParamDefs) == 0 {
		return ft
	}
	// A collection literal is never typed as the interface itself, so it
	// specialises nothing. Checking it here, before its parameter's expected
	// type is known, would also check it a second time against whatever
	// expected type encloses the call: `.North` inside
	// `Iter.to_map([(.North, .Cave)])` would be reported against the call's
	// `Map<Direction, Place>`.
	switch firstArg.(type) {
	case *ast.ListLit, *ast.VectorLit, *ast.SetLit, *ast.ListSpreadLit, *ast.MapLit, *ast.TupleLit:
		return ft
	}
	// This check only reads the argument's type. checkCall checks every
	// argument again against its parameter, and that check reports; keeping
	// this one's diagnostics would report each error in the argument twice
	// (an undefined name inside `Iter.count(Iter.filter(xs, |x| ...))`).
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	argTy := c.checkNode(firstArg)
	c.errors = c.errors[:mark]
	c.undetermined = c.undetermined[:undeterminedMark]
	argIface, ok := argTy.(*InterfaceType)
	if !ok || argIface.Name != iface.Name || len(argIface.TypeArgs) != len(iface.TypeParamDefs) {
		return ft
	}
	subs := make(map[*TypeParam_]Type, len(iface.TypeParamDefs))
	for i, def := range iface.TypeParamDefs {
		subs[def] = argIface.TypeArgs[i]
	}
	params := make([]Type, len(ft.Params))
	for i, p := range ft.Params {
		params[i] = Substitute(p, subs)
	}
	return &FuncType{Params: params, Return: Substitute(ft.Return, subs), DefaultCount: ft.DefaultCount, WhereBounds: ft.WhereBounds}
}

// resolveBodyType resolves a binding's inline annotation — `held: Box = …`.
// That is a declaration position, so a generic must be fully applied here.
func (c *checker) resolveBodyType(te ast.TypeExpr) (Type, error) {
	return ResolveDeclaredType(te, c.reg, c.fnTypeParams, c.fa.References)
}

func (c *checker) registerImplBlockHover(n *ast.ImplBlock) {
	if n == nil || n.Line <= 0 || n.Col <= 0 {
		return
	}
	if n.Interface == nil && (n.Receiver == nil || IsSynthesizedLine(n.Receiver.LineNum())) {
		return
	}
	if n.Interface != nil && n.Receiver != nil && IsSynthesizedLine(n.Receiver.LineNum()) && !n.InferInterfaceMethods {
		return
	}

	info := &ImplInfo{
		Receiver:      typeExprHoverString(n.Receiver),
		GenericHeader: typeParamsHoverString(n.Generics),
		WhereClause:   whereClausesHoverString(n.WhereClauses),
		SourceBlock:   n.Interface == nil || (n.Receiver != nil && !IsSynthesizedLine(n.Receiver.LineNum())),
		Inherent:      n.Interface == nil,
	}
	if n.Interface != nil {
		info.Interfaces = []string{typeExprHoverString(n.Interface)}
	}
	c.registerImplKeywordHover(Pos{Line: n.Line, Col: n.Col}, n, info)
}

func (c *checker) registerImplFunctionHover(item ast.Node, block *ast.ImplBlock) {
	var (
		line            int
		col             int
		name            string
		extern          bool
		sourceQualified bool
		iface           ast.TypeExpr
		typ             Type
	)

	switch it := item.(type) {
	case *ast.FuncDef:
		if !it.ImplFunction || it.ImplLine <= 0 || it.ImplCol <= 0 {
			return
		}
		line, col = it.ImplLine, it.ImplCol
		name, iface = it.Name, it.ImplIface
		sourceQualified = it.ImplIfaceSourceQualified
		if sym := c.fa.Definitions[Pos{Line: it.Line, Col: it.Col}]; sym != nil {
			typ = sym.Type
		}
	case *ast.ExternFunc:
		if !it.ImplFunction || it.ImplLine <= 0 || it.ImplCol <= 0 {
			return
		}
		line, col = it.ImplLine, it.ImplCol
		name, iface, extern = it.Name, it.ImplIface, true
		sourceQualified = it.ImplIfaceSourceQualified
		if sym := c.fa.Definitions[Pos{Line: it.Line, Col: it.Col}]; sym != nil {
			typ = sym.Type
		}
	default:
		return
	}
	if iface == nil && block != nil {
		iface = block.Interface
	}

	info := &ImplInfo{
		FunctionName:    name,
		Extern:          extern,
		SourceQualified: sourceQualified,
	}
	if block != nil {
		info.Receiver = typeExprHoverString(block.Receiver)
	}
	if iface != nil {
		info.Interfaces = []string{typeExprHoverString(iface)}
	}
	c.registerImplKeywordHover(Pos{Line: line, Col: col}, item, info, typ)
}

func (c *checker) registerImplKeywordHover(pos Pos, node ast.Node, info *ImplInfo, typ ...Type) {
	if pos.Line <= 0 || pos.Col <= 0 || info == nil {
		return
	}
	if existing := c.fa.References[pos]; existing != nil && existing.Kind == SymbolImplKeyword && existing.Impl != nil && existing.Impl.FunctionName == "" && info.FunctionName == "" {
		existing.Impl.Interfaces = mergeImplHoverInterfaces(existing.Impl.Interfaces, info.Interfaces)
		if existing.Impl.Receiver == "" {
			existing.Impl.Receiver = info.Receiver
		}
		if existing.Impl.GenericHeader == "" {
			existing.Impl.GenericHeader = info.GenericHeader
		}
		existing.Impl.SourceBlock = existing.Impl.SourceBlock || info.SourceBlock
		return
	}

	sym := &Symbol{
		Name: "impl",
		Kind: SymbolImplKeyword,
		Pos:  pos,
		Span: 4,
		Node: node,
		Impl: info,
	}
	if len(typ) > 0 {
		sym.Type = typ[0]
	}
	c.fa.References[pos] = sym
}

func mergeImplHoverInterfaces(existing, next []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range append(existing, next...) {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func typeExprHoverString(te ast.TypeExpr) string {
	if te == nil {
		return ""
	}
	return te.TypeString()
}

func typeParamsHoverString(params []ast.TypeParam) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		part := param.Name
		if len(param.Bounds) > 0 {
			bounds := make([]string, 0, len(param.Bounds))
			for _, bound := range param.Bounds {
				bounds = append(bounds, typeExprHoverString(bound))
			}
			part += ": " + strings.Join(bounds, " and ")
		}
		parts = append(parts, part)
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func whereClausesHoverString(clauses []ast.WhereConstraint) string {
	if len(clauses) == 0 {
		return ""
	}
	parts := make([]string, 0, len(clauses))
	for _, wc := range clauses {
		bounds := make([]string, 0, len(wc.Bounds))
		for _, bound := range wc.Bounds {
			if s := typeExprHoverString(bound); s != "" {
				bounds = append(bounds, s)
			}
		}
		if len(bounds) == 0 {
			continue
		}
		parts = append(parts, wc.Name+": "+strings.Join(bounds, " and "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " where " + strings.Join(parts, ", ")
}

// checkImplReceiverDeclared reports an impl block whose receiver names no
// type, at the name: `impl Greeter for Peon` with no `Peon` declared. Its
// functions were otherwise registered for a type nothing can hold, and a
// same-owner call into them reached the IR builder.
func (c *checker) checkImplReceiverDeclared(n *ast.ImplBlock) {
	if n.Receiver == nil {
		return
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, n)
	_, err := ResolveTypeExpr(n.Receiver, c.reg, blockTP, nil)
	if te, isTypeErr := err.(TypeError); isTypeErr && strings.HasPrefix(te.Message, "unknown type ") {
		c.report(te)
	}
}

// checkImplInterfaceArgsResolve reports a type argument of an impl header's
// interface that does not resolve, at the argument: the `Dbys` of
// `impl Add<Dbys, Day> for Day`. checkImplBlock resolves the whole header
// and, when that fails, skips every conformance check, and the builder
// reports only an interface name that is not declared. So the block was
// accepted and its functions registered under an interface instantiation no
// one could name, which a `+` on the receiver then reached.
func (c *checker) checkImplInterfaceArgsResolve(n *ast.ImplBlock) {
	te := n.Interface
	if q, ok := te.(*ast.QualifiedType); ok {
		te = q.Member
	}
	g, ok := te.(*ast.GenericType)
	if !ok {
		return
	}
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, n)
	for _, arg := range g.Params {
		if _, err := ResolveTypeExpr(arg, c.reg, blockTP, nil); err != nil {
			if typeErr, isTypeErr := err.(TypeError); isTypeErr {
				c.report(typeErr)
			}
		}
	}
}

func (c *checker) checkImplBlock(n *ast.ImplBlock) {
	// Every diagnostic this function and its callees produce is re-pointed
	// off the synth band on the way out. See repointSynthDiagnostics.
	defer c.repointSynthDiagnostics(n, len(c.errors))
	c.registerImplBlockHover(n)
	if IsSynthesizedLine(n.Line) {
		prevReg := c.reg
		c.reg = synthSupportRegistry(c.fa, c.reg)
		defer func() { c.reg = prevReg }()
	} else {
		c.checkImplReceiverDeclared(n)
		if n.Interface != nil {
			c.checkImplInterfaceArgsResolve(n)
		}
	}

	if n.Interface == nil {
		c.checkAppTypeMembers(TypeExprBaseName(n.Receiver), n.Items)
	}
	// Track the receiver type while checking implementation bodies.
	prevSelf := c.selfTypeName
	c.selfTypeName = TypeExprBaseName(n.Receiver)
	// Check each item's body.
	prevImpl := c.implBlock
	c.implBlock = n
	ifaceName := TypeExprBaseName(n.Interface)
	for _, item := range n.Items {
		if n.InferInterfaceMethods && n.Interface != nil && !implItemClaimsInterface(item, ifaceName) {
			continue
		}
		c.registerImplFunctionHover(item, n)
		c.checkAttachedTests(item)
		switch it := item.(type) {
		case *ast.FuncDef:
			c.checkFunc(it)
		case *ast.ExternFunc:
			c.checkExternFunc(it)
		case *ast.OnceBinding:
			c.checkOnce(it)
		}
	}
	c.selfTypeName = prevSelf
	c.implBlock = prevImpl

	if n.Interface == nil {
		return // inherent block — no interface conformance to assert
	}

	// Resolve the interface and the receiver's structural surface, then assert
	// field conformance (the universal-conformance-assertion semantics of an
	// interface-impl block; structural-only interfaces are satisfied by an
	// empty block).
	// The header's interface type arguments may name the block's own type
	// parameters (`impl Store<String, T> for Cache<T>`), so resolve it with
	// them in scope; resolved without, `T` is undefined and every check below
	// was skipped.
	blockTP, _ := buildImplBlockTypeParams(c.fa, c.reg, n)
	ifaceTy, err := ResolveTypeExpr(n.Interface, c.reg, blockTP, c.fa.References)
	if err != nil || ifaceTy == nil {
		return
	}
	iface, ok := ifaceTy.(*InterfaceType)
	if !ok {
		return // not an interface (builder already errored)
	}
	recvName := TypeExprBaseName(n.Receiver)
	if recvName == "" {
		return
	}

	// Method-signature conformance: every `fn` / `host fn` item must match
	// the interface method's parameter and return types (with `self` → the
	// receiver). The dispatch path never validates it, so without this check
	// a mistyped impl method would slip through. Reuse the same implementation-signature comparator over
	// the items' already-resolved FuncTypes.
	c.validateImplBlockMethodSignatures(n, iface, blockTP)
}

// interfaceReqSubs is the interface's own type-argument substitution, so an
// impl of `Add<Days, Day>` compares against the interface's signatures with
// `Days` and `Day` in place of its parameters. `self` is not in here, because
// the right binding differs per caller: the method validator takes it from the
// impl's own self-position parameter (the applied receiver), and
// requiredImplSignature binds it to the receiver.
func interfaceReqSubs(iface *InterfaceType) map[*TypeParam_]Type {
	subs := make(map[*TypeParam_]Type, len(iface.TypeParamDefs)+1)
	for i, def := range iface.TypeParamDefs {
		if i < len(iface.TypeArgs) {
			subs[def] = iface.TypeArgs[i]
		}
	}
	return subs
}

// interfaceReqSubsWithSelf is interfaceReqSubs plus `self` → the receiver.
func interfaceReqSubsWithSelf(iface *InterfaceType, recvTy Type) map[*TypeParam_]Type {
	subs := interfaceReqSubs(iface)
	if iface.SelfParam != nil && recvTy != nil {
		subs[iface.SelfParam] = recvTy
	}
	return subs
}

// implDiagPos answers where a diagnostic about `n`'s contents should point.
//
// A block SYNTHESIZED for a declaration (`@derive`, or the universal-default
// Debug) carries positions in the synth band, and those name a line no
// programmer can navigate to and no editor can resolve — a real one reached a
// user as `line 1077657600, col 1: impl function 'inspect': return type Bool
// does not match interface 'Debug' return type String`, which is a real
// mis-attribution wearing a fabricated address. The programmer wrote the
// DECLARATION, so that is what the error names.
//
// Hand-written blocks record no origin and keep their own position.
func implDiagPos(n *ast.ImplBlock, fallback Pos) Pos {
	if n != nil && n.SynthOriginLine > 0 && IsSynthesizedLine(fallback.Line) {
		return Pos{Line: n.SynthOriginLine, Col: n.SynthOriginCol}
	}
	return fallback
}

// repointSynthDiagnostics moves every diagnostic recorded from `from` onward
// off the synthesized-position band and onto the declaration `n` was
// synthesized for.
//
// implDiagPos above covers the diagnostics checkImplBlock raises ITSELF, one
// call site at a time. It does not cover the ones its callees raise while
// checking a synthesized BODY, and those reach users too. Two examples,
// both from `derive FromJson for P` / `derive ToJson for P` in a
// file that does not also name `Json`:
//
//	line 1432010752, col 84: unknown type "Json.ShapeError"
//	line 1431994368, col 7:  undefined type Json.Obj
//
// 1432010752 decodes as synthSlot(synthOriginConformance, 2, synthKindFromJson)
// — a legitimate slot, not an uninitialized field. What is wrong is that it
// escaped into a message, so the fix is a position rewrite rather than a
// position repair.
//
// THE DIAGNOSTIC IS KEPT, NOT DROPPED. synthSupportNames' rule is that an
// unresolved name in a synthesized body "still errors loudly, keeping the
// synthesizer-bug net intact", and a synthesizer bug the user cannot see is
// worse than one reported at the `derive` line.
//
// ONLY THE TypeError IS TOUCHED. fa.Definitions and fa.References key the
// impl item's resolved signature BY POSITION, and re-pointing a NODE instead
// of the error would make that lookup miss and silently delete every signature
// mismatch on every synthesized impl. This function has no access to either
// map, which is the point.
func (c *checker) repointSynthDiagnostics(n *ast.ImplBlock, from int) {
	if from < 0 || from > len(c.errors) {
		return
	}
	repointSynthErrors(n, c.errors[from:])
}

// repointSynthErrors moves each error in errs whose position lies in the
// synthesized band onto the declaration the block n was synthesized for.
// Every pass that reports on an impl block (the builder, the type builder,
// the checker) runs its new errors through it. A hand-written block records
// no origin and its errors keep their own position.
func repointSynthErrors(n *ast.ImplBlock, errs []TypeError) {
	if n == nil || n.SynthOriginLine <= 0 {
		return
	}
	for i := range errs {
		if !IsSynthesizedLine(errs[i].Line) {
			continue
		}
		errs[i].Line = n.SynthOriginLine
		errs[i].Col = n.SynthOriginCol
		errs[i].EndLine, errs[i].EndCol = 0, 0
	}
}

// validateImplBlockMethodSignatures checks each item of an interface-impl
// block against the interface's MethodSig: the method must be declared on the
// interface, take the same number of parameters, and match each parameter
// type and the return type after the header's type arguments replace the
// interface's parameters. Covers `fn` and `host fn` items alike (both carry a
// resolved FuncType from buildImplBlockTypes). The comparator is
// implSigMatcher: the block's own type parameters (blockTP) are rigid.
func (c *checker) validateImplBlockMethodSignatures(n *ast.ImplBlock, iface *InterfaceType, blockTP map[string]*TypeParam_) {
	recvName := TypeExprBaseName(n.Receiver)
	matcher := newImplSigMatcher(iface, blockTP)
	recvTy, err := ResolveTypeExpr(n.Receiver, c.reg, blockTP, nil)
	if err != nil {
		recvTy = nil
	}
	for _, item := range n.Items {
		var name string
		var pos Pos
		var params []ast.Param
		var typeParams []ast.TypeParam
		switch it := item.(type) {
		case *ast.FuncDef:
			name, pos, params, typeParams = it.Name, Pos{Line: it.Line, Col: it.Col}, it.Params, it.TypeParams
		case *ast.ExternFunc:
			name, pos, params, typeParams = it.Name, Pos{Line: it.Line, Col: it.Col}, it.Params, it.TypeParams
		default:
			continue
		}
		// pos KEYS the Definitions lookup below and must stay the item's own
		// position; dpos is only where a diagnostic points.
		dpos := implDiagPos(n, pos)
		if n.InferInterfaceMethods && !implItemClaimsInterface(item, iface.Name) {
			continue
		}
		var sig *MethodSig
		for i := range iface.Methods {
			if iface.Methods[i].Name == name {
				sig = &iface.Methods[i]
				break
			}
		}
		if sig == nil {
			c.addError(dpos.Line, dpos.Col, fmt.Sprintf("impl '%s' for '%s': function '%s' is not part of the interface", iface.Name, recvName, name))
			continue
		}
		// Final-default rule: a non-`open` interface default is final, so an
		// impl may not override it. A host-backed default (`host fn`) is final
		// the same way unless declared `open extern`.
		if (sig.HasDefault || sig.Extern) && !sig.Open {
			c.addError(dpos.Line, dpos.Col, fmt.Sprintf("impl function '%s': '%s' is a final default on interface '%s'; mark it `open` on the interface to allow overriding", name, name, iface.Name))
			continue
		}
		sym := c.fa.Definitions[pos]
		if sym == nil {
			continue
		}
		ft, ok := sym.Type.(*FuncType)
		if !ok || ft == nil {
			continue // unresolved signature — buildImplBlockTypes already errored
		}
		required := c.requiredImplSignature(iface, sig, recvTy)
		mismatch := func(msg string) {
			c.report(TypeError{Line: dpos.Line, Col: dpos.Col, Message: msg}.WithHint(required))
		}
		if len(ft.Params) != len(sig.Params) {
			mismatch(fmt.Sprintf("impl function '%s' takes %d parameters, but interface '%s' declares %d", name, len(ft.Params), iface.Name, len(sig.Params)))
			continue
		}
		// `self` substitutes to the impl's OWN resolved self-position parameter
		// — the applied receiver (e.g. `List<T>`), so `self` inside the return
		// type (`Maybe<(T, self)>`) compares correctly. reg.Lookup(recvName)
		// gives the bare, unapplied generic (`List`), which wouldn't unify with
		// `List<T>`. An interface function whose `self` appears only in other
		// positions (`fn make(): self`, `FromJson.from_json(json: Json):
		// Result<self, E>`) has no self-position parameter, and there `self` is
		// the receiver as the header resolves it (recvTy, applied, with the
		// block's own type parameters rigid). Left unsubstituted, `self` bound
		// to whatever the impl wrote, so `impl Make for Day { fn make(): Int }`
		// was accepted.
		subs := interfaceReqSubs(iface)
		if iface.SelfParam != nil {
			for i := range sig.Params {
				if isSelfPositionParam(sig.Params[i], iface) && i < len(ft.Params) && ft.Params[i] != nil {
					subs[iface.SelfParam] = ft.Params[i]
					break
				}
			}
			if _, seeded := subs[iface.SelfParam]; !seeded && recvTy != nil {
				subs[iface.SelfParam] = recvTy
			}
		}
		match := matcher.function(typeParams)
		for i := range ft.Params {
			// Parameter-name conformance (Swift-style, matching the decorator
			// path): a non-self-position parameter's name must match the
			// interface declaration. The self/receiver param name is free, and a
			// destructured parameter has no user-facing slot name to compare.
			if i < len(params) && i < len(sig.ParamNames) && sig.ParamNames[i] != "" &&
				!isSelfPositionParam(sig.Params[i], iface) && params[i].Destructure == nil && params[i].Name != sig.ParamNames[i] {
				p := params[i]
				pp := implDiagPos(n, Pos{Line: p.Line, Col: p.Col})
				e := TypeError{Line: pp.Line, Col: pp.Col, Message: fmt.Sprintf("impl function '%s': parameter name '%s' does not match interface declaration '%s'", name, p.Name, sig.ParamNames[i])}
				if pp.Line == p.Line {
					e.EndLine, e.EndCol = p.Line, p.Col+len(p.Name)
				}
				if r, ok := c.interfaceFunctionRelated(n, name, "interface function '"+name+"' is declared here"); ok {
					e = e.WithRelated(r)
				}
				c.report(e.WithHint(fmt.Sprintf("rename it '%s'", sig.ParamNames[i])))
			}
			// A self-position parameter is the receiver: `self` stands for it on
			// the interface side, so the impl must write the receiver there.
			// `impl Greeter for Peon { fn greet(p: Person) }` registered a
			// Person function under Peon, which a same-owner call from
			// another Person impl then reached.
			if isSelfPositionParam(sig.Params[i], iface) {
				if recvTy != nil && ft.Params[i] != nil && !typesEqualIn(recvTy, ft.Params[i], eqStrict) {
					mismatch(c.typef("impl function '%s': parameter %d has type %s, but it stands for `self`, which this block implements for %s", name, i+1, ft.Params[i], recvTy))
				}
				continue
			}
			expected := Substitute(sig.Params[i], subs)
			actual := ft.Params[i]
			if expected == nil || actual == nil {
				continue
			}
			if !match.match(expected, actual) {
				mismatch(c.typef("impl function '%s': parameter %d has type %s, but interface '%s' declares %s", name, i+1, actual, iface.Name, expected))
			} else if et, emb, ok := embedsDowncast(actual, expected); ok {
				mismatch(c.typef("impl function '%s': parameter %d has type %s, but interface '%s' declares %s, and %s may hold a variant other than %s", name, i+1, actual, iface.Name, expected, et, emb))
			}
		}
		// A return written on neither side is Unit, so an interface function
		// with no return type requires an impl with none.
		expectedRet := normalizeReturn(sig.Return)
		if sig.Return != nil {
			expectedRet = normalizeReturn(Substitute(sig.Return, subs))
		}
		if actualRet := normalizeReturn(ft.Return); !match.match(expectedRet, actualRet) {
			mismatch(c.typef("impl function '%s': return type %s does not match interface '%s' return type %s", name, actualRet, iface.Name, expectedRet))
		} else if et, emb, ok := embedsDowncast(expectedRet, actualRet); ok {
			mismatch(c.typef("impl function '%s': return type %s does not match interface '%s' return type %s, and %s may hold a variant other than %s", name, actualRet, iface.Name, expectedRet, et, emb))
		}
		match.consistent(true)
		for _, extra := range match.extraWhereBounds(sig, ft) {
			msg := fmt.Sprintf("impl function '%s': `where %s: %s` is not required by interface '%s', and a call through the interface does not check it", name, extra.param, extra.iface, iface.Name)
			if extra.implParam {
				msg += fmt.Sprintf("; put it on the impl block (`impl ... where %s: %s`)", extra.param, extra.iface)
			}
			c.addError(dpos.Line, dpos.Col, msg)
		}
	}

	// Completeness: every interface method without a default body must be
	// supplied by the block. An interface-impl block that omits a required
	// method must error here (otherwise the omission only surfaces as a runtime
	// dispatch miss, or silently never, when the method is never called).
	// Methods with a default body are optional; a marker interface has no
	// methods, so an empty block satisfies it. One block per (Iface, T) is
	// guaranteed by the coherence check, so every required method must live in
	// THIS block.
	supplied := make(map[string]bool, len(n.Items))
	for _, item := range n.Items {
		switch it := item.(type) {
		case *ast.FuncDef:
			if !n.InferInterfaceMethods || implItemClaimsInterface(item, iface.Name) {
				supplied[it.Name] = true
			}
		case *ast.ExternFunc:
			if !n.InferInterfaceMethods || implItemClaimsInterface(item, iface.Name) {
				supplied[it.Name] = true
			}
		}
	}
	for i := range iface.Methods {
		m := &iface.Methods[i]
		// HasDefault (Nomi default) and Extern (host-backed default) are both
		// provided to implementors, so neither is required.
		if m.HasDefault || m.Extern || supplied[m.Name] {
			continue
		}
		// Common slip: the type has an inherent method of the same name. Point
		// at it instead of reporting the method as absent.
		if c.hasUntaggedMethod(recvName, m.Name) {
			c.addError(n.Line, n.Col, fmt.Sprintf("`fn %s` on '%s' is a type function, not an implementation function; move it into `impl %s for %s { ... }` to satisfy the interface", m.Name, recvName, iface.Name, recvName))
			continue
		}
		e := implHeaderError(n, fmt.Sprintf("impl '%s' for '%s': missing function '%s' required by interface '%s'", iface.Name, recvName, m.Name, iface.Name))
		if r, ok := c.interfaceFunctionRelated(n, m.Name, "'"+m.Name+"' is declared here"); ok {
			e = e.WithRelated(r)
		}
		c.report(e)
	}
}

// hasUntaggedMethod reports whether the receiver type has an inherent
// method of the given name. Used to turn a bare "missing method" diagnostic
// into a "put this method in the interface implementation block" hint.
func (c *checker) hasUntaggedMethod(recvName, methodName string) bool {
	if c.fa == nil || c.fa.TypeMethods == nil {
		return false
	}
	sym := c.fa.TypeMethods[recvName][methodName]
	return sym != nil && !sym.IsImplMethod
}

// typeString renders a Type for error messages, falling back to
// "<unknown>" when the type is nil. Trivial wrapper around .String() so
// the field-validation error path doesn't have to nil-check inline.
func typeString(t Type) string {
	if t == nil {
		return "<unknown>"
	}
	return t.String()
}

// isSelfPositionParam reports whether a parameter type IS the self type-param
// itself (so the impl is free to rename the corresponding param). Composite
// types containing self (like `Maybe<self>`) are not considered self-position.
func isSelfPositionParam(t Type, iface *InterfaceType) bool {
	if iface.SelfParam == nil {
		return false
	}
	tp, ok := t.(*TypeParam_)
	return ok && tp == iface.SelfParam
}

// requiredImplSignature is the hint on an impl function whose arity,
// parameter types or return type differ from the interface's: the function
// the impl must write, with the header's type arguments in place of the
// interface's parameters and the receiver in place of `self`.
// `impl Add<Days, Day> for Day` requires `fn add(lhs: Day, rhs: Days): Day`.
// An interface parameter the header gives no argument for keeps its name.
func (c *checker) requiredImplSignature(iface *InterfaceType, sig *MethodSig, recvTy Type) string {
	subs := interfaceReqSubsWithSelf(iface, recvTy)
	params := make([]string, len(sig.Params))
	for i, p := range sig.Params {
		ty := typeString(Substitute(p, subs))
		if i < len(sig.ParamNames) && sig.ParamNames[i] != "" {
			params[i] = sig.ParamNames[i] + ": " + ty
		} else {
			params[i] = ty
		}
	}
	ret := ""
	if sig.Return != nil && !isUnitLike(sig.Return) {
		ret = ": " + typeString(Substitute(sig.Return, subs))
	}
	return fmt.Sprintf("interface '%s' requires `fn %s(%s)%s`", iface, sig.Name, strings.Join(params, ", "), ret)
}

// checkInterfaceDefaults type-checks each Nomi-bodied default method in an
// interface. Interface default bodies are not top-level FuncDefs, but they
// should obey the same body/return rules and may call sibling interface
// functions by bare name.
func (c *checker) checkInterfaceDefaults(n *ast.InterfaceDef) {
	c.checkInterfaceDefaultParamDefaults(n)

	it, ok := c.reg.Lookup(n.Name).(*InterfaceType)
	if !ok || it == nil {
		return
	}

	prevIface := c.currentInterface
	c.currentInterface = n.Name
	defer func() { c.currentInterface = prevIface }()

	for i := range n.Methods {
		m := &n.Methods[i]
		if m.Body == nil {
			continue
		}
		ft := interfaceDefaultMethodFuncType(it, m.Name)
		if ft == nil {
			continue
		}

		prevReturn := c.returnTy
		c.returnTy = ft.Return
		prevFn := c.currentFnName
		c.currentFnName = m.Name
		prevBoundary := c.tryBoundary
		c.tryBoundary = "fn " + m.Name
		restoreExits := c.enterBoundaryExits()
		prevTP := c.fnTypeParams
		c.fnTypeParams = collectTypeParamsByName(ft)
		restoreWhereBounds := c.pushWhereBounds(ft.WhereBounds)

		var bodyTy Type
		if block, ok := m.Body.(*ast.Block); ok {
			bodyTy = c.checkBlockExpectingReturn(block)
		} else {
			bodyTy = c.checkNode(m.Body)
		}

		restoreWhereBounds()
		c.fnTypeParams = prevTP
		restoreExits()
		c.tryBoundary = prevBoundary
		c.currentFnName = prevFn
		c.returnTy = prevReturn

		if bodyTy != nil && ft.Return != nil && !TypeAssignable(ft.Return, bodyTy) {
			matched := false
			if isBoolVariantVsBool(bodyTy, ft.Return) {
				matched = true
			}
			if !matched && containsTypeVar(bodyTy) {
				if err := c.unifyInto(ft.Return, bodyTy, nil); err == nil {
					matched = true
				}
			}
			if !matched && c.argMatchesParam(bodyTy, ft.Return, c.recPos(m.Line, m.Col), RecordingKindInterfaceTypedParam) {
				matched = true
			}
			if !matched {
				c.addMismatch(m.Line, m.Col, ft.Return, bodyTy, c.typef("return type mismatch: expected %s, got %s", ft.Return, bodyTy))
			}
		}
	}
}

// checkInterfaceDefaultParamDefaults type-checks the default value of each
// parameter of an interface DEFAULT method against that parameter's declared
// type. The main checker loop doesn't otherwise visit interface bodies with
// expected-type context, so without this an interface default's param default
// is never type-checked — a mismatched `count: Int = "nope"` would slip through
// to a runtime surprise. Resolved param types come from the already-built
// MethodSig (index-aligned with the AST params). Only default methods
// (Body != nil) carry param defaults that apply — required and host-backed
// methods reject defaults at parse time. (Dot-shorthand defaults are already
// resolved by the parser from the annotation; this validates them too.)
func (c *checker) checkInterfaceDefaultParamDefaults(n *ast.InterfaceDef) {
	it, ok := c.reg.Lookup(n.Name).(*InterfaceType)
	if !ok {
		return
	}
	for mi := range n.Methods {
		m := &n.Methods[mi]
		if m.Body == nil {
			continue
		}
		var sig *MethodSig
		for si := range it.Methods {
			if it.Methods[si].Name == m.Name {
				sig = &it.Methods[si]
				break
			}
		}
		if sig == nil {
			continue
		}
		for pi := range m.Params {
			p := m.Params[pi]
			if p.Default == nil || pi >= len(sig.Params) {
				continue
			}
			paramTy := sig.Params[pi]
			if paramTy == nil {
				continue
			}
			defTy := c.checkExitless(paramDefaultExitless(p.Name), func() Type {
				return c.checkNodeExpecting(p.Default, paramTy)
			})
			if defTy != nil && !c.argMatchesParam(defTy, paramTy, c.recPos(p.Line, p.Col), RecordingKindGenericBoundCheck) {
				c.addError(p.Line, p.Col, c.typef(
					"interface function '%s': default value for parameter '%s' is %s, expected %s",
					m.Name, p.Name, defTy, paramTy))
			}
		}
	}
}

// pushWhereBounds makes the current function's `where` constraints visible
// while checking its body. It feeds the same bound paths used by
// `T.member(...)`, bounded field access, and generic return conformance. The
// mutation is scoped to the current body check and restored immediately
// afterward, so declaration rendering still reflects the
// source syntax.
func (c *checker) pushWhereBounds(bounds []WhereBound) func() {
	if len(bounds) == 0 {
		return func() {}
	}
	originals := map[*TypeParam_][]*InterfaceType{}
	for _, wb := range bounds {
		if wb.Param == nil || len(wb.Bounds) == 0 {
			continue
		}
		if _, ok := originals[wb.Param]; !ok {
			originals[wb.Param] = wb.Param.Bounds
		}
		wb.Param.Bounds = append(wb.Param.Bounds, wb.Bounds...)
	}
	return func() {
		for tp, original := range originals {
			tp.Bounds = original
		}
	}
}

// recordWhereBoundDemands enforces and records function-level `where` bounds
// at a call site. The callee FuncType carries the WhereBounds; for each, the
// constrained type variable's concrete binding is read straight from `subs`
// (the WhereBound.Param pointer is the exact one the receiver/args solving
// bound) and then checked + recorded like any generic interface constraint.
// Shared by the direct-call (checkGenericCall) and pipe (checkPipe) paths so
// neither drifts.
func (c *checker) recordWhereBoundDemands(ft *FuncType, subs map[*TypeParam_]Type, line, col, errCol int) {
	for _, wb := range ft.WhereBounds {
		concrete, ok := subs[wb.Param]
		if !ok {
			continue
		}
		for _, bound := range wb.Bounds {
			if !typeImplementsInterface(c, concrete, bound, c.recPos(line, col), RecordingKindGenericBoundCheck) {
				c.boundError(line, errCol, concrete, bound, wb.Param.Name_)
				continue
			}
			if fits, have := c.boundArgsFit(concrete, bound, subs); !fits {
				c.boundArgsError(line, errCol, concrete, bound, wb.Param.Name_, subs, have)
				continue
			}
			c.recordBoundConformance(concrete, bound.Name, c.recPos(line, col), RecordingKindGenericBoundCheck)
		}
	}
}

// solveFromWhereImpls solves the type parameters a generic `where` bound's
// arguments name from the impl the bound's solved subject has: with `L`
// solved to Day and `impl Add<Days, Day> for Day` the only impl of Add for
// Day whose arguments unify with the bound's `Add<R, Out>` under what subs
// already holds, R and Out are Days and Day. A bound whose subject is not
// concrete, or that two impls would answer differently, solves nothing. It
// reports whether it solved anything.
func (c *checker) solveFromWhereImpls(ft *FuncType, subs map[*TypeParam_]Type) bool {
	if c.fa == nil {
		return false
	}
	solvedAny := false
	for range ft.WhereBounds {
		progress := false
		for _, wb := range ft.WhereBounds {
			subject, ok := subs[wb.Param]
			if !ok {
				continue
			}
			concrete := resolveTypeVar(subject)
			if ContainsTypeParam(concrete) || containsTypeVar(concrete) {
				continue
			}
			for _, bound := range wb.Bounds {
				if bound == nil || len(bound.TypeArgs) == 0 || !anyTypeArgOpen(bound.TypeArgs, subs) {
					continue
				}
				var answer map[*TypeParam_]Type
				var answerArgs []Type
				ambiguous := false
				for _, args := range c.implArgLists(concrete, bound.Name) {
					if len(args) != len(bound.TypeArgs) {
						continue
					}
					trial := make(map[*TypeParam_]Type, len(subs))
					for k, v := range subs {
						trial[k] = v
					}
					fits := true
					for i, a := range bound.TypeArgs {
						if c.unify(a, args[i], trial) != nil {
							fits = false
							break
						}
					}
					if !fits {
						continue
					}
					if answer != nil && !typesAllEqual(answerArgs, args) {
						ambiguous = true
						break
					}
					answer, answerArgs = trial, args
				}
				if answer == nil || ambiguous {
					continue
				}
				for k, v := range answer {
					if _, had := subs[k]; !had {
						subs[k] = v
						progress = true
					}
				}
			}
		}
		if !progress {
			break
		}
		solvedAny = true
	}
	return solvedAny
}

// anyTypeArgOpen reports whether a type parameter in args is not in subs.
func anyTypeArgOpen(args []Type, subs map[*TypeParam_]Type) bool {
	for _, a := range args {
		if ContainsTypeParam(Substitute(a, subs)) {
			return true
		}
	}
	return false
}

// typesAllEqual reports whether two type lists are pairwise equal.
func typesAllEqual(a, b []Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !TypesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// checkPartialParamStructs enforces that every `Partial<T>` parameter of a
// generic callee resolves to a struct once T is solved. `Partial<T>` is a deep
// partial of a struct's *fields*, so a non-struct inner has nothing to patch:
// `Struct.update(5, 5)` solves the `updates: Partial<T>` parameter to
// `Partial<Int>`, which the runtime would reject only at execution time
// ("first argument must be a struct"). Surfacing it here turns that runtime
// trap into a compile-time error — the call-site complement to the type
// resolver's concrete-inner check (which handles a literal `Partial<Int>`
// annotation). A param whose inner is still generic (T unsolved) is skipped;
// only a fully-solved non-struct inner is an error. Shared by the direct-call
// (checkGenericCall) and pipe (checkPipe) paths so neither drifts.
func (c *checker) checkPartialParamStructs(ft *FuncType, subs map[*TypeParam_]Type, line, col int) {
	for _, p := range ft.Params {
		pt, ok := p.(*PartialType)
		if !ok {
			continue
		}
		inner := Substitute(pt.Inner, subs)
		if isSolvedParam(inner) && !isStructShaped(inner) {
			c.addError(line, col, fmt.Sprintf(
				"Partial<%s> is invalid: `%s` is not a struct, so it has no fields to patch", inner, inner))
		}
	}
}

// enforceStructConformance rejects an interface-qualified call whose receiver
// solved to a non-struct when the interface is the universal `Struct`. Struct
// conformance is structural and missing-impl-exempt, so this call-site check is
// the sole enforcement that `Struct.method(x)` is handed an actual struct — and
// it produces the principled "X does not implement Struct" message rather than
// the incidental `Partial<self>` one. Returns true when it emitted an error, so
// the caller can suppress the now-redundant checkPartialParamStructs diagnostic
// for the same call. A still-generic self (type param / inference var) is
// skipped — only a fully-solved non-struct is an error.
func (c *checker) enforceStructConformance(ifaceName string, concrete Type, method string, line, col int) bool {
	if ifaceName != "Struct" || concrete == nil {
		return false
	}
	if ContainsTypeParam(concrete) || containsTypeVar(concrete) {
		return false
	}
	if isStructShaped(concrete) {
		return false
	}
	c.addError(line, col, fmt.Sprintf(
		"%s does not implement Struct (required by Struct.%s — only structs, named or anonymous, satisfy Struct)",
		concrete, method))
	return true
}

// interfaceMethodFuncType constructs a FuncType for `iface.method` from the
// interface's stored MethodSig. Returns nil when the method isn't declared on
// the interface or its return type isn't resolvable.
func interfaceMethodFuncType(iface *InterfaceType, name string) *FuncType {
	if iface == nil {
		return nil
	}
	for _, m := range iface.Methods {
		if m.Name == name {
			params := make([]Type, len(m.Params))
			copy(params, m.Params)
			// A method with no declared return type is Unit (e.g.
			// `each(source: self, f: (T) -> Unit)`); still build a FuncType so
			// its callback args get generic inference rather than dropping it.
			ret := m.Return
			if ret == nil {
				ret = TypeUnit
			}
			// Resolve `self` to the interface applied to its own type params
			// (e.g. `Iter<T>`) so that unifying a concrete receiver at the
			// call site binds the interface's type params — the same generic
			// inference the impl-method path gets. Without it, `self` stays a
			// bare param: unify binds `self` but never `T`, so a default like
			// `find(s: self, f: (T) -> Bool)` leaves the lambda param as `T`.
			// Non-generic interfaces keep the bare `self`; interface default
			// bodies apply the interface-shaped `self` only while checking
			// those bodies.
			if iface.SelfParam != nil && len(iface.TypeParamDefs) > 0 {
				args := make([]Type, len(iface.TypeParamDefs))
				for i, d := range iface.TypeParamDefs {
					args[i] = d
				}
				selfTy := &InterfaceType{
					Origin:        iface.Origin,
					Name:          iface.Name,
					Methods:       iface.Methods,
					TypeParams:    iface.TypeParams,
					TypeParamDefs: iface.TypeParamDefs,
					TypeArgs:      args,
					SelfParam:     iface.SelfParam,
				}
				subs := map[*TypeParam_]Type{iface.SelfParam: selfTy}
				for i := range params {
					params[i] = Substitute(params[i], subs)
				}
				ret = Substitute(ret, subs)
			}
			return &FuncType{Params: params, Return: ret, DefaultCount: m.DefaultCount, WhereBounds: m.WhereBounds}
		}
	}
	return nil
}

// callFnTypeFromResolved returns the callee's type via the Resolved indirection
// that import proxy symbols carry. Used as a narrow fallback in checkCall when
// the direct symbol lookup returned nil, so that calls to imported variant
// constructors and functions resolve to the real signature without changing
// the broader behaviour of checkIdent/checkTypeIdent. Walks the full Resolved
// chain (not just one hop) so multi-hop re-export chains — A re-exports from
// B which re-exports from C — reach C's original symbol whose `.Type` is set.
// Returns nil when the node is not an ident/type-ident, or no Resolved chain
// reaches a typed symbol.
func (c *checker) callFnTypeFromResolved(node ast.Node) Type {
	var pos Pos
	switch n := node.(type) {
	case *ast.Ident:
		pos = Pos{Line: n.Line, Col: n.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: n.Line, Col: n.Col}
	default:
		return nil
	}
	sym, ok := c.fa.References[pos]
	if !ok {
		sym, ok = c.fa.Definitions[pos]
	}
	if !ok || sym == nil {
		return nil
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Type
}

// checkRangeLit checks a `..` / `..=` range literal. Operands must have the
// same Comparable type. Result type is the stdlib `Range<T>` struct.
func (c *checker) checkRangeLit(n *ast.RangeLit) Type {
	checkOperand := func(expr ast.Node) Type {
		if expr == nil {
			return nil
		}
		got := c.checkNode(expr)
		if got == nil {
			return nil
		}
		return got
	}

	startTy := checkOperand(n.Start)
	endTy := checkOperand(n.End)
	elemTy := startTy
	if elemTy == nil {
		elemTy = endTy
	}
	if startTy != nil && endTy != nil && !TypesEqual(startTy, endTy) {
		c.addError(n.Line, n.Col, c.typef("range endpoints must have the same type, got %s and %s", startTy, endTy))
	}
	if elemTy == nil {
		elemTy = TypeInt
	}

	if iface := lookupInterfaceType(c.fa, c.reg, "Comparable"); iface != nil {
		if !typeImplementsInterface(c, elemTy, iface, c.recPos(n.Line, n.Col), RecordingKindGenericBoundCheck) {
			c.addError(n.Line, n.Col, fmt.Sprintf("range endpoint type %s does not implement Comparable", elemTy))
		} else {
			c.recordBoundConformance(elemTy, "Comparable", c.recPos(n.Line, n.Col), RecordingKindGenericBoundCheck)
		}
	}
	return c.instantiateRangeType(elemTy)
}

// instantiateRangeType is std's Range<elem>. It reads std's declaration
// (TypeRange), never the file's scope: a std file that does not import
// std/ranges has no `Range` in scope, and reading the name there typed its
// range literals as nothing, which left `1..=3 |> Iter.to_vector()`'s T
// unsolved. Without std there is no Range, and the literal has no type.
func (c *checker) instantiateRangeType(elem Type) Type {
	st, ok := TypeRange.(*StructType)
	if !ok {
		return nil
	}
	return &StructType{
		Origin:           st.Origin,
		Name:             st.Name,
		Fields:           st.Fields,
		TypeParams:       st.TypeParams,
		TypeParamDefs:    st.TypeParamDefs,
		TypeArgs:         []Type{elem},
		Opaque:           st.Opaque,
		OwningSourceFile: st.OwningSourceFile,
	}
}

// rejectAssertionOperand reports an `assert` or `refute` written as the
// operand of op. A `check`, whose value is the point, is not one. An assertion checks its subject and ends the test or function
// when the check fails; its value is the subject it passed, so `!assert x` is
// `False` whenever it gets that far and never the negated check it reads as.
// An assertion stands as its own statement, as a binding's value, or as a pipe
// stage.
func (c *checker) rejectAssertionOperand(a *ast.Assertion, op string) {
	keyword := assertionKeyword(a)
	e := TypeError{
		Line:    a.Line,
		Col:     a.Col,
		Message: fmt.Sprintf("`%s` cannot be an operand of `%s`", keyword, op),
	}
	if op == "!" {
		e = e.WithHint("to check that a condition is false, write `refute condition` or `assert !condition`")
	} else {
		e = e.WithHint("write the assertion as its own statement, or bind its value first: `ok = " + keyword + " condition`")
	}
	c.errors = append(c.errors, e)
}

func (c *checker) checkBinary(n *ast.Binary, expected Type) Type {
	if n.Op != "|>" {
		for _, side := range []ast.Node{n.Left, n.Right} {
			if a, isAssert := ungroupExpr(side).(*ast.Assertion); isAssert && !a.Check {
				c.rejectAssertionOperand(a, n.Op)
			}
		}
	}
	// A pipe into a generic call in a position with a known expected type
	// checks its head against the call's first parameter as the expected type
	// solves it, as checkGenericCall does for a written-out first argument:
	// `[(.North, .Cave)] |> Iter.to_map()` expected to be
	// `Map<Direction, Place>` checks the list against
	// `Iter<(Direction, Place)>`. The callee is resolved once, here, and reused
	// below.
	var left Type
	leftChecked := false
	var earlyCallee Type
	earlyCall := false
	if n.Op == "|>" && needsExpectedType(n.Left) && concreteExpected(expected) {
		if call, ok := n.Right.(*ast.Call); ok && !callHasPlaceholder(call.Args) {
			earlyCallee = c.pipeCalleeType(call)
			earlyCall = true
			// A struct-shaped variant's head is a record, not the struct
			// its constructor type names: `{r: 6} |> Shape.Circle()`
			// expected to be a `Shape<Int>` must not type `{r: 6}` as a
			// Circle, which checkPipedVariantCtor rejects as already built.
			if c.structShapedVariantCallee(call.Func, earlyCallee) {
				// checked below with no expected type, as the record it is
			} else if headTy := c.pipeHeadExpected(earlyCallee, expected); headTy != nil {
				left = c.checkNodeExpecting(n.Left, headTy)
				leftChecked = true
			}
		}
	}
	var right Type
	comparison := isComparisonOp(n.Op)
	if comparison {
		left, right = c.checkComparisonOperands(n)
	} else if !leftChecked {
		left = c.checkNode(n.Left)
	}
	if n.Op == "|>" {
		defer c.pushPipeLambdaParams(n.Left)()
	}

	// For pipes, collect the function type and explicit args separately.
	// Bare keyword stages (`x |> dbg`, `x |> try`) consume the already-computed
	// left value directly; keyword-with-operand forms on the RHS are rejected so
	// each pipe stage has one obvious input.
	var pipeCall *ast.Call
	pipeDbg := false
	var pipeTry *ast.TryOp
	var pipeAssertion *ast.Assertion
	var pipeIf *ast.If
	var pipeCase *ast.Case
	var pipeLambda *ast.Lambda
	var pipeTap *ast.Tap
	if n.Op == "|>" {
		rhs := n.Right
		if dbg, ok := rhs.(*ast.Dbg); ok && dbg.Expr == nil {
			pipeDbg = true
			right = left
		}
		if try, ok := ungroupExpr(rhs).(*ast.TryOp); ok {
			pipeTry = try
			if try.Expr != nil {
				c.addError(try.Line, try.Col, "right side of `|>` must be bare `try`; pipe into the fallible call first")
			} else {
				c.registerTryOpHover(try, left)
				right = c.unwrapTryResult(left, try, n.Left)
			}
		}
		if pipeTry == nil {
			if assertion, ok := ungroupExpr(rhs).(*ast.Assertion); ok {
				pipeAssertion = assertion
				kw := assertionKeyword(assertion)
				c.registerAssertionHoverAt(assertion.Line, assertion.Col, assertion, kw, left, left, left)
				c.addError(assertion.Line, assertion.Col, fmt.Sprintf(
					"`%s` must be placed at the head of the pipeline", kw))
			}
		}
		if pipeTry == nil && pipeAssertion == nil && !pipeDbg {
			if ifExpr, ok := ungroupExpr(rhs).(*ast.If); ok && ifExpr.Cond == nil {
				pipeIf = ifExpr
				right = c.checkIfWithConditionType(ifExpr, left, expected)
			}
		}
		if pipeTry == nil && pipeAssertion == nil && !pipeDbg && pipeIf == nil {
			if caseExpr, ok := ungroupExpr(rhs).(*ast.Case); ok && caseExpr.Value == nil {
				pipeCase = caseExpr
				right = c.checkCaseWithScrutineeType(caseExpr, left, expected)
			}
		}
		if pipeTry == nil && pipeAssertion == nil && !pipeDbg && pipeIf == nil && pipeCase == nil {
			if tap, ok := rhs.(*ast.Tap); ok {
				pipeTap = tap
				right = c.checkTapStage(tap, left)
			} else if then, ok := rhs.(*ast.Then); ok {
				pipeLambda = then.Lambda
				c.checkPipeLambdaArity(then.Lambda, "then")
			} else if lambda, ok := ungroupExpr(rhs).(*ast.Lambda); ok {
				// `x |> |v| ...` is not a stage (pipe_stage_call.go). The
				// lambda is still checked against the piped value, so the
				// rest of the pipeline is typed as the `then` it should be.
				pipeLambda = lambda
				c.reportLambdaPipeStage(lambda)
			}
			if pipeLambda != nil {
				expectedFT := &FuncType{Params: []Type{left}, Return: expected}
				if ft, ok := c.checkLambdaExpecting(pipeLambda, expectedFT).(*FuncType); ok {
					right = ft.Return
				}
			}
			if then, ok := rhs.(*ast.Then); ok {
				c.registerControlFlowHover(then.Line, then.Col, "then", left, right, left != nil, false)
			}
		}
		if pipeDbg {
			// Already handled: bare `dbg` as a pipe stage is transparent.
		} else if pipeTry != nil {
			// Already handled: bare `try` as a pipe stage unwraps the piped
			// Result/Maybe.
		} else if pipeAssertion != nil {
			// Already diagnosed: assert/refute own a pipeline from the head.
		} else if pipeIf != nil {
			// Already handled: bare if as a pipe stage branches on the piped Bool.
		} else if pipeCase != nil {
			// Already handled: bare case as a pipe stage matches the piped value.
		} else if pipeLambda != nil {
			// Already handled: a `then` stage calls its lambda with the
			// piped value.
		} else if pipeTap != nil {
			// Already handled: a `tap` stage runs its lambda and passes
			// the piped value on.
		} else if acc, ok := ungroupExpr(rhs).(*ast.FieldAccessor); ok {
			// `user |> .name`: the read is `user.name`.
			c.addError(acc.Line, acc.Col, fieldAccessorPipeStageMessage(acc))
		} else if call, ok := rhs.(*ast.Call); ok {
			pipeCall = call
			c.markCallHoles(call)
			if earlyCall {
				right = earlyCallee
			} else {
				right = c.pipeCalleeType(call)
			}
		} else if isPipeKeywordStage(rhs) {
			// `x |> todo` and the rejected `x |> dbg expr`, both judged
			// below.
			right = c.checkNode(n.Right)
		} else {
			// A stage that is not a call: `x |> f`, `x |> io.print`,
			// `x |> Ok`. A name without parentheses is a function
			// reference, never a call (pipe_stage_call.go). The stage is
			// still checked, as a callee, so an undefined name is reported
			// and hover and go-to-definition see it.
			c.reportBarePipeStage(rhs)
			prevCallee := c.calleeNode
			c.calleeNode = ungroupExpr(rhs)
			c.checkNode(n.Right)
			c.calleeNode = prevCallee
			return nil
		}
	} else if !comparison {
		right = c.checkNode(n.Right)
	}

	switch n.Op {
	case "+", "-", "*", "/":
		if left == nil || right == nil {
			return left // best effort
		}
		return c.checkOperatorBinary(n, left, right)

	case "%":
		if left == nil || right == nil {
			return left // best effort
		}
		// Try unify first so a fresh TypeVar on either side (e.g. `prev`
		// destructured from `Map.get(acc, k)` when acc is `Map<?α, ?β>`)
		// binds to the concrete operand type. Fall back to TypesEqual.
		mismatch := false
		if err := c.unify(left, right, nil); err != nil {
			if !TypesEqual(left, right) {
				mismatch = true
			}
		}
		if mismatch {
			c.addError(n.Line, n.Col, c.typef(
				"binary %s type mismatch: %s vs %s", n.Op, left, right))
			return left
		}
		// Resolve TypeVars before the numeric check — `prev + v` binding
		// `prev`'s TypeVar to Int via the unify above leaves left as the
		// (now-resolved) TypeVar, not the TypeInt singleton.
		leftResolved := left
		if tv, ok := left.(*TypeVar); ok && tv.Resolved != nil {
			leftResolved = tv.Resolved
		}
		if n.Op == "%" {
			// Modulo is defined only on Int/Float — never on Decimal (no `%`
			// by design, like Float-only modulo). The mixed-operand check
			// above already rejected Int-vs-Decimal etc.; this rejects a
			// Decimal % Decimal explicitly.
			if leftResolved != TypeInt && leftResolved != TypeFloat {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"modulo is not defined on %s; %% operands must be Int or Float", left))
			}
		} else if leftResolved != TypeInt && leftResolved != TypeFloat && leftResolved != TypeDecimal {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"binary %s operands must be Int, Float, or Decimal, got %s", n.Op, left))
		}
		return left

	case "==", "!=":
		// Operands must be the same type. Use unify first so a fresh TypeVar
		// on either side binds (generic code, `x == None`); fall back to
		// TypesEqual, which also accepts embed-subtyping (T ≤ E). Mixed
		// Int/Float and unrelated types (e.g. String vs Int) are rejected
		// rather than silently comparing unequal at runtime.
		if left != nil && right != nil {
			if err := c.unify(left, right, nil); err != nil && !TypesEqual(left, right) && !isBoolVariantPair(left, right) {
				c.addError(n.Line, n.Col, c.typef(
					"equality type mismatch: %s vs %s", left, right))
			} else if msg := c.noEqualityMessage(left, n.Op); msg != "" {
				// A function value or an Iter, directly or inside the
				// operand (equality_domain.go).
				c.addError(n.Line, n.Col, msg)
			}
		}
		// Record (T, Equatable) demand so the manifest carries the
		// dispatch entry `==` needs for struct types with a custom
		// `impl Equatable` (e.g. anchored `DateTime`, where `==` is
		// instant-only). The recorder skips interfaces / type-params /
		// type-vars; primitives (Int / Float / Decimal / String / Bool)
		// take the direct comparison path and never dispatch, so
		// their `impl Equatable` bodies (`fn equal?(a: Int, b: Int):
		// Bool { a == b }`) can't recurse. The recording is *weak* —
		// see RecordingKindEqualityOperator — so a `==` on a distinct
		// type without an `impl Equatable` (e.g. `pub type Id Int`)
		// doesn't trigger a missing-impl diagnostic; equality
		// falls back to structural `rt.Equal`.
		c.recordConformanceRecording(left, "Equatable",
			Recording{Pos: c.recPos(n.Line, n.Col), Kind: RecordingKindEqualityOperator, Op: n.Op})
		return TypeBool

	case "<", ">", "<=", ">=":
		if left != nil && right != nil && !TypesEqual(left, right) {
			c.addError(n.Line, n.Col, c.typef(
				"comparison type mismatch: %s vs %s", left, right))
		} else if kind, unorderable := orderingUnsupportedKind(left); unorderable {
			// Nameless structural aggregates (anonymous structs, tuples)
			// can never carry an `impl Comparable` — there is no
			// declaration to hang the impl on, and they have no nominal
			// name for `recordConformanceRecording` to demand, so the
			// strong-demand route below would silently drop them and the
			// comparison would reach evalComparison's "cannot compare"
			// trap at runtime (documented there as unreachable for a
			// type-checked program). Surface it as the compile-time
			// missing-impl error a named struct already gets.
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"no impl of `Comparable` for `%s` (required via ordering operator `%s`); %s types cannot implement `Comparable`",
				left, n.Op, kind))
		} else {
			// Record (T, Comparable) demand. Unlike Equatable above this
			// recording is *strong* — see RecordingKindOrderingOperator —
			// because ordering has no structural fallback: `<` on a
			// concrete type with no declared `impl Comparable` is a
			// compile-time missing-impl error. When the impl exists,
			// evalComparison routes StructVal comparisons through
			// `impl Comparable fn compare` so `<` / `>` agree with `==`
			// for types like `DateTime` that override equality semantics.
			// Recursing (recordBoundConformanceRecording, not
			// recordConformanceRecording): a container's `impl
			// Comparable` body dispatches Comparable on its ELEMENTS,
			// so `#["a"] < #["b"]` needs (Comparable, String) as well
			// as (Comparable, Vector). Without the recursion it
			// recorded only the container and faulted at dispatch —
			// the same shape as the type-qualified call site in
			// impl_call_demand.go, reached by a different route.
			c.recordBoundConformanceRecording(left, "Comparable",
				Recording{Pos: c.recPos(n.Line, n.Col), Kind: RecordingKindOrderingOperator, Op: n.Op})
		}
		return TypeBool

	case "and", "or":
		if left != nil && !TypesEqual(left, TypeBool) {
			at := operandPos(n.Left, n.Line, n.Col)
			c.addError(at.Line, at.Col, fmt.Sprintf(
				"'%s' operands must be Bool, got %s", n.Op, left))
		}
		if right != nil && !TypesEqual(right, TypeBool) {
			at := operandPos(n.Right, n.Line, n.Col)
			c.addError(at.Line, at.Col, fmt.Sprintf(
				"'%s' operands must be Bool, got %s", n.Op, right))
		}
		return TypeBool

	case "|>":
		if pipeDbg {
			return left
		}
		if todo, ok := ungroupExpr(n.Right).(*ast.Todo); ok {
			// `x |> todo`: the piped value is computed and the stage traps,
			// so the pipeline fits whatever its position expects.
			if expected != nil {
				c.fa.recordExpectedType(todo, expected)
			}
			return TypeInfallible
		}
		if pipeTry != nil {
			return right
		}
		if pipeAssertion != nil {
			return nil
		}
		if pipeIf != nil {
			return right
		}
		if pipeCase != nil {
			return right
		}
		if pipeLambda != nil {
			return right
		}
		if pipeTap != nil {
			return right
		}
		if dbg, ok := n.Right.(*ast.Dbg); ok && dbg.Expr != nil {
			c.addError(dbg.Line, dbg.Col, "right side of `|>` must be bare `dbg`, not `dbg expr`")
			return nil
		}
		if pipeCall != nil {
			if form, ok := c.variantCtorCallee(pipeCall.Func, right); ok {
				if !callHasPlaceholder(pipeCall.Args) {
					return c.openVariantEnum(c.checkPipedVariantCtor(pipeCall, n.Left, left, form))
				}
			}
			if dt, ok := right.(*DistinctType); ok && isTypeConstructorCallee(pipeCall.Func) && !callHasPlaceholder(pipeCall.Args) {
				if ty := c.checkPipedDistinctCtor(pipeCall, n.Left, left, dt); ty != nil {
					return ty
				}
			}
			if ty, handled := c.checkPipedTypeNameCall(pipeCall, n.Left, left, right); handled {
				return ty
			}
		}
		return c.checkPipe(n, left, right, pipeCall, expected)

	default:
		return nil
	}
}

type operatorInterface struct {
	Op        string
	Interface string
	Method    string
	Verb      string
}

var operatorInterfaces = []operatorInterface{
	{Op: "+", Interface: "Add", Method: "add", Verb: "add"},
	{Op: "-", Interface: "Subtract", Method: "subtract", Verb: "subtract"},
	{Op: "*", Interface: "Multiply", Method: "multiply", Verb: "multiply"},
	{Op: "/", Interface: "Divide", Method: "divide", Verb: "divide"},
}

func operatorInterfaceForOp(op string) (operatorInterface, bool) {
	for _, iface := range operatorInterfaces {
		if iface.Op == op {
			return iface, true
		}
	}
	return operatorInterface{}, false
}

func operatorInterfaceByName(name string) (operatorInterface, bool) {
	for _, iface := range operatorInterfaces {
		if iface.Interface == name {
			return iface, true
		}
	}
	return operatorInterface{}, false
}

// Operator diagnostics point at the operator token, which is where the
// parser puts ast.Binary's and ast.Unary's Line and Col: a mismatch between
// the operands, a missing or unmatched operator impl, and an operand type the
// operator is not defined on all belong to the operator. A diagnostic that
// blames one operand of two (`and` given a non-Bool, a right operand an
// impl does not take) points at that operand's first token instead. The
// owner-call form of an operator interface (`Subtract.subtract(a, b)`) has
// no operator token, so its missing impl points at the call's first token
// and its argument mismatch at the argument.
func operandPos(operand ast.Node, opLine, opCol int) Pos {
	if line, col := exprStartLineCol(operand); line > 0 && col > 0 {
		return Pos{Line: line, Col: col}
	}
	return Pos{Line: opLine, Col: opCol}
}

func (c *checker) checkOperatorBinary(n *ast.Binary, left, right Type) Type {
	opIface, ok := operatorInterfaceForOp(n.Op)
	if !ok {
		return left
	}
	if result, ok := c.checkOperatorInterface(n, opIface, left, right); ok {
		return result
	}
	if result, ok := c.checkBuiltinOperator(n, left, right); ok {
		return result
	}
	if kind, unsupported := operatorUnsupportedKind(left); unsupported {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"no impl of `%s` for `%s` (required via %s operator `%s`); %s types cannot implement `%s`",
			opIface.Interface, left, opIface.Verb, opIface.Op, kind, opIface.Interface))
		return left
	}
	if name := concreteTypeName(resolveTypeVar(left)); name != "" {
		c.recordConformanceRecording(left, opIface.Interface,
			Recording{Pos: c.recPos(n.Line, n.Col), Kind: RecordingKindOperator, Op: n.Op})
		return left
	}
	c.addError(n.Line, n.Col, fmt.Sprintf(
		"binary %s operands must use a left-hand type that implements %s, got %s", n.Op, opIface.Interface, left))
	return left
}

func (c *checker) checkOperatorInterface(n *ast.Binary, opIface operatorInterface, left, right Type) (Type, bool) {
	leftResolved := resolveTypeVar(left)
	switch lt := leftResolved.(type) {
	case *TypeParam_:
		for _, bound := range lt.Bounds {
			if bound == nil || bound.Name != opIface.Interface {
				continue
			}
			if len(bound.TypeArgs) != 2 {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"%s bound on %s must specify right-hand and output types", opIface.Interface, lt))
				return left, true
			}
			return c.checkOperatorRhs(n, opIface, right, bound.TypeArgs[0], bound.TypeArgs[1])
		}
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"type parameter %s cannot use `%s` without %s %s bound", lt, n.Op, articleFor(opIface.Interface), opIface.Interface))
		return left, true
	case *InterfaceType:
		if lt.Name == opIface.Interface && len(lt.TypeArgs) == 2 {
			return c.checkOperatorRhs(n, opIface, right, lt.TypeArgs[0], lt.TypeArgs[1])
		}
		return nil, false
	}

	name := interfaceImplName(leftResolved)
	// OriginUnresolved: an operator interface is named by the OPERATOR
	// (`+` → `Add`), not by a resolved type expression, so there is no
	// declaration here to read an Origin off. It needs none — the operator
	// interfaces are prelude-injected and therefore unshadowable
	// (checkReservedTypeName), so the name has exactly one declaration.
	if name == "" || !interfaceImplemented(name, opIface.Interface, OriginUnresolved, c.implsContext()) {
		return nil, false
	}
	rhsExpected, result, matched := c.lookupOperatorImplTypeArgs(opIface, leftResolved, right)
	if !matched {
		if isBuiltinOperatorCandidate(n.Op, leftResolved) {
			return nil, false
		}
		if rhsExpected != nil {
			// One impl for this receiver, so the right operand is what is
			// wrong: name the type that impl takes.
			return c.checkOperatorRhs(n, opIface, right, rhsExpected, result)
		}
		c.addError(n.Line, n.Col, c.typef(
			"no matching %s impl for %s %s %s", opIface.Interface, left, n.Op, right))
		return left, true
	}
	c.fa.RecordManifest(name, opIface.Interface,
		Recording{Pos: c.recPos(n.Line, n.Col), Kind: RecordingKindOperator, Op: n.Op})
	c.recordImplReceiverBoundConformances(leftResolved, opIface.Interface, c.recPos(n.Line, n.Col), RecordingKindOperator)
	return c.checkOperatorRhs(n, opIface, right, rhsExpected, result)
}

// lookupOperatorImplTypeArgs selects the operator impl for `left op right` and
// returns its right-hand and output types at left's type arguments
// (`Box<Int>`'s `impl Add<T, Box<T>>` takes Int and gives Box<Int>). On a
// miss it still returns the one impl's types when exactly one impl's
// receiver matches, so the caller can report the right operand instead of a
// missing impl; matched is false either way.
func (c *checker) lookupOperatorImplTypeArgs(opIface operatorInterface, left, right Type) (Type, Type, bool) {
	if c == nil || c.fa == nil {
		return nil, nil, false
	}
	concrete := resolveTypeVar(left)
	name := concreteTypeName(concrete)
	if name == "" {
		return nil, nil, false
	}
	cargs := concreteTypeArgs(concrete)
	candidates := lookupImplTypeArgSets(name, opIface.Interface, c.implTypeArgSetsContext())
	if len(candidates) == 0 {
		if info := lookupImplTypeArgs(name, opIface.Interface, c.implTypeArgsContext()); info != nil {
			candidates = []*ImplTypeArgs{info}
		}
	}
	// The right-hand and output types of every impl whose receiver matches.
	// When there is exactly one, a miss against it is a right-operand
	// mismatch rather than a missing impl.
	var rhsSeen, outSeen []Type
	for _, info := range candidates {
		if info == nil || len(info.Args) != 2 {
			continue
		}
		if !implReceiverMatches(info, concrete, c.implsContext(), c.implTypeArgsContext()) {
			continue
		}
		rhs := substituteTypeParamDefs(info.TypeParamDefs, cargs, info.Args[0])
		out := substituteTypeParamDefs(info.TypeParamDefs, cargs, info.Args[1])
		if c.unify(rhs, right, nil) == nil || TypesEqual(rhs, right) {
			return rhs, out, true
		}
		// The same block can reach this loop from the file's table and the
		// project's, so keep distinct right-hand types only.
		seen := false
		for _, prior := range rhsSeen {
			if TypesEqual(prior, rhs) {
				seen = true
				break
			}
		}
		if !seen {
			rhsSeen, outSeen = append(rhsSeen, rhs), append(outSeen, out)
		}
	}
	if len(rhsSeen) == 1 {
		return rhsSeen[0], outSeen[0], false
	}
	return nil, nil, false
}

func (c *checker) checkOperatorRhs(n *ast.Binary, opIface operatorInterface, right, rhsExpected, result Type) (Type, bool) {
	if err := c.unify(rhsExpected, right, nil); err != nil && !TypesEqual(rhsExpected, right) {
		at := operandPos(n.Right, n.Line, n.Col)
		c.addError(at.Line, at.Col, c.typef(
			"binary %s right operand mismatch: %s expects %s, got %s", n.Op, opIface.Interface, rhsExpected, right))
	}
	return result, true
}

func (c *checker) checkBuiltinOperator(n *ast.Binary, left, right Type) (Type, bool) {
	if !isBuiltinOperatorCandidate(n.Op, left) && !isBuiltinOperatorCandidate(n.Op, right) {
		return nil, false
	}
	mismatch := false
	if err := c.unify(left, right, nil); err != nil {
		if !TypesEqual(left, right) {
			mismatch = true
		}
	}
	if mismatch {
		c.addError(n.Line, n.Col, c.typef(
			"binary %s type mismatch: %s vs %s", n.Op, left, right))
		return left, true
	}
	leftResolved := resolveTypeVar(left)
	if n.Op == "+" && leftResolved == TypeString {
		return TypeString, true
	}
	if n.Op == "+" {
		if _, ok := resolveTypeVar(leftResolved).(*ListType); ok {
			return left, true
		}
		if _, ok := vectorElemType(leftResolved); ok {
			return left, true
		}
	}
	if leftResolved == TypeInt || leftResolved == TypeFloat || leftResolved == TypeDecimal {
		return left, true
	}
	return nil, false
}

func isBuiltinOperatorCandidate(op string, t Type) bool {
	switch rt := resolveTypeVar(t).(type) {
	case *TypeVar:
		return true
	case *ListType:
		return op == "+"
	default:
		if op == "+" {
			if _, ok := vectorElemType(rt); ok {
				return true
			}
		}
		if op == "+" && rt == TypeString {
			return true
		}
		return rt == TypeInt || rt == TypeFloat || rt == TypeDecimal
	}
}

func operatorUnsupportedKind(t Type) (string, bool) {
	if t == nil {
		return "", false
	}
	switch resolveTypeVar(t).(type) {
	case *AnonStructType:
		return "anonymous struct", true
	case *TupleType:
		return "tuple", true
	}
	return "", false
}

func ungroupExpr(n ast.Node) ast.Node {
	for {
		grouped, ok := n.(*ast.GroupedExpr)
		if !ok {
			return n
		}
		n = grouped.Expr
	}
}

// pipeCalleeType is the type of a pipe stage's callee (`Iter.to_map` in
// `xs |> Iter.to_map()`).
func (c *checker) pipeCalleeType(call *ast.Call) Type {
	if ident, ok := call.Func.(*ast.Ident); ok {
		if ty, sameOwner := c.sameOwnerBareCallType(ident); sameOwner {
			c.fa.recordExprType(ident, ty)
			return ty
		}
	}
	return c.checkCallee(call.Func)
}

// concreteExpected reports an expected type that can seed a generic call's
// type parameters: present, with no type parameter and no unsolved inference
// variable in it.
func concreteExpected(expected Type) bool {
	return expected != nil && !ContainsTypeParam(expected) && !containsTypeVar(expected)
}

// pipeHeadExpected is the type a pipe's head is expected to have when the
// stage calls the generic function calleeTy and the whole pipe is expected to
// be `expected`: the first parameter under the substitution that unifying
// the result with `expected` solves. nil when the callee is not generic in
// its result, the result does not unify, or the parameter stays unsolved.
func (c *checker) pipeHeadExpected(calleeTy, expected Type) Type {
	ft, ok := calleeTy.(*FuncType)
	if !ok || len(ft.Params) == 0 || !ContainsTypeParam(ft.Return) {
		return nil
	}
	subs := map[*TypeParam_]Type{}
	if c.unifyInto(expected, ft.Return, subs) != nil {
		return nil
	}
	head := Substitute(ft.Params[0], subs)
	if ContainsTypeParam(head) {
		return nil
	}
	return head
}

func (c *checker) checkPipe(n *ast.Binary, argTy, fnTy Type, pipeCall *ast.Call, expected Type) Type {
	if fnTy == nil {
		return nil
	}
	ft, ok := fnTy.(*FuncType)
	if !ok {
		return nil
	}

	// Pipe semantics: when the call has an explicit `_` placeholder, the
	// piped value substitutes for the placeholder in place — so arg index
	// maps directly to param index. Without a placeholder, the piped value
	// is prepended as the first arg, so explicit args fill slots 1..N.
	pipedSlot := 0
	argShift := 1
	if pipeCall != nil {
		for i, arg := range pipeCall.Args {
			if _, isPh := arg.(*ast.Placeholder); isPh {
				pipedSlot = i
				argShift = 0
				break
			}
		}
	}

	// Pre-compute the slot mapping for explicit args. Mirrors
	// computePositionalSlots from the direct-call path: identity mapping
	// (positional i → slot i+argShift), with trailing-lambda routing
	// applied when the last positional is a lambda that would otherwise
	// land in a non-last slot. Without this, `xs |> f(|y| ...)` with a
	// defaulted middle param misroutes the lambda to the middle slot,
	// dropping bidirectional inference into the lambda body.
	var pipeSlots []int
	if pipeCall != nil {
		// Too many arguments, counting the piped value, as the direct call
		// counts them. Too few is checkRequiredSlotsFilled's below.
		if got := len(pipeCall.Args) + argShift; got > len(ft.Params) {
			if ft.DefaultCount > 0 {
				c.addError(pipeCall.Line, 1, fmt.Sprintf(
					"expected %d to %d arguments, got %d (counting the piped value)", len(ft.Params)-ft.DefaultCount, len(ft.Params), got))
			} else {
				c.addError(pipeCall.Line, 1, fmt.Sprintf(
					"expected %d arguments, got %d (counting the piped value)", len(ft.Params), got))
			}
		}
		pipeSlots = computePipeArgSlots(pipeCall.Args, argShift, c.callDefParams(pipeCall.Func), ft.Params)
		for _, na := range positionalNamedConflicts(pipeCall.Args, pipeSlots, c.callDefParams(pipeCall.Func), argShift) {
			c.addError(na.Line, 1, fmt.Sprintf("parameter '%s' already has a value", na.Name))
		}
		if argShift == 1 {
			c.checkRequiredSlotsFilled(pipeCall, pipeCall.Args, pipeSlots, pipedSlot, ft.Params)
		} else {
			c.checkRequiredSlotsFilled(pipeCall, pipeCall.Args, pipeSlots, -1, ft.Params)
		}
	}

	// Check if the function is generic.
	isGeneric := ContainsTypeParam(ft.Return)
	for _, p := range ft.Params {
		if ContainsTypeParam(p) {
			isGeneric = true
			break
		}
	}

	if isGeneric {
		subs := map[*TypeParam_]Type{}
		errMark := len(c.errors)
		slotArgs := map[int]ast.Node{}

		// Unify piped-in arg against the slot it fills (param[pipedSlot]).
		if pipedSlot < len(ft.Params) && argTy != nil {
			slotArgs[pipedSlot] = n.Left
			argTy = coerceMapToList(argTy, ft.Params[pipedSlot])
			argTy = coerceRangeToList(argTy, ft.Params[pipedSlot])
			if err := c.unifyInto(ft.Params[pipedSlot], argTy, subs); err != nil {
				c.reportGenericArgMismatch(ft.Params[pipedSlot], argTy, subs, n.Left, positionalArgLabel(pipedSlot), n.Line, n.Col)
			}
			// Mirror the recordInterfaceConformanceFromParam call in
			// checkGenericCall: a piped concrete arg flowing into an
			// interface-typed param (e.g. `xs |> Iter.to_list()` where
			// to_list's param is `Iter<T>`) needs (concrete, iface)
			// recorded in the impl manifest.
			c.recordInterfaceConformanceFromParam(argTy, ft.Params[pipedSlot], c.recPos(n.Line, n.Col), RecordingKindInterfaceTypedParam)
		}

		// Check explicit args (from the call syntax) against their routed slots.
		if pipeCall != nil {
			defParams := c.callDefParams(pipeCall.Func)
			for i, arg := range pipeCall.Args {
				paramIdx := pipeSlots[i]
				// computePipeArgSlots returns -1 for named args, since they
				// route by name rather than position. Resolving that name to
				// its slot here is what lets a named argument contribute to
				// inference: without it a type param that only appears in
				// that parameter never binds, and
				// `xs |> Task.spawn_all(max_running: 2, f: label)` reported
				// `List<U>` where the same call with `label` positional
				// inferred `List<String>` — the label alone changed the type.
				if na, ok := arg.(*ast.NamedArg); ok {
					slot := namedArgSlot(na.Name, defParams)
					if slot >= 0 && slot < len(ft.Params) {
						expectedTy := Substitute(ft.Params[slot], subs)
						valTy := c.checkNodeExpecting(na.Value, expectedTy)
						if valTy != nil {
							slotArgs[slot] = na.Value
							argLine, argCol := nodeLineCol(na.Value)
							if err := c.unifyInto(ft.Params[slot], valTy, subs); err != nil {
								c.reportGenericArgMismatch(ft.Params[slot], valTy, subs, na.Value, namedArgLabel(na.Name), argLine, argCol)
							}
							c.recordInterfaceConformanceFromParam(valTy, ft.Params[slot], c.recPos(argLine, argCol), RecordingKindInterfaceTypedParam)
						}
						continue
					}
					if slot < 0 {
						c.reportUnknownNamedArg(na, defParams)
					}
				}
				if paramIdx >= 0 && paramIdx < len(ft.Params) {
					expectedTy := Substitute(ft.Params[paramIdx], subs)
					// `_` placeholder in a pipe call gets a hover Symbol
					// carrying the slot's expected type.
					if ph, ok := arg.(*ast.Placeholder); ok && expectedTy != nil {
						c.fa.References[Pos{Line: ph.Line, Col: ph.Col}] = &Symbol{
							Name: "_",
							Kind: SymbolBinding,
							Pos:  Pos{Line: ph.Line, Col: ph.Col},
							Type: expectedTy,
						}
					}
					explicitArgTy := c.checkNodeExpecting(arg, expectedTy)
					if explicitArgTy != nil {
						slotArgs[paramIdx] = arg
						argLine, argCol := nodeLineCol(arg)
						if err := c.unifyInto(ft.Params[paramIdx], explicitArgTy, subs); err != nil {
							c.reportGenericArgMismatch(ft.Params[paramIdx], explicitArgTy, subs, arg, positionalArgLabel(paramIdx), argLine, argCol)
						}
						c.recordInterfaceConformanceFromParam(explicitArgTy, ft.Params[paramIdx], c.recPos(argLine, argCol), RecordingKindInterfaceTypedParam)
					}
				} else {
					c.checkNode(arg)
				}
			}
		}

		// Interface-qualified dispatch via pipe (`s |> Display.to_string()`):
		// record the (receiver, iface) conformance and the dispatch target for
		// the interface method's self-parameter binding, mirroring
		// checkGenericCall's direct-call path. Without this the manifest bridge
		// never pre-loads the receiver's impl and the runtime fails with
		// "Display.to_string: no implementation for type ...". (The List case
		// happens to work without it only because (Display, List) is recorded
		// elsewhere in the always-loaded stdlib; Set has no such backstop.)
		structConformanceErrored := false
		if pipeCall != nil {
			if fa, ok := pipeCall.Func.(*ast.FieldAccess); ok {
				if iface, ok := interfaceObjectType(c, fa.Object, nil); ok && iface.SelfParam != nil {
					if concrete, ok := subs[iface.SelfParam]; ok {
						structConformanceErrored = c.enforceStructConformance(iface.Name, concrete, fa.Field.Name, n.Line, n.Col)
						c.recordBoundConformance(concrete, iface.Name, c.recPos(n.Line, n.Col), RecordingKindCallSite)
						// The bound `T` is USED at, which nothing recorded
						// because a Debug bound can reject nothing. This is the
						// PIPE spelling; the direct-call spelling below is the
						// sibling, and both are needed — a table-driven guard
						// over one of them would report success for the other.
						c.recordInferredBoundOnTypeParam(concrete, iface.Name)

						c.attachDispatchImpl(fa, iface.Name, concrete)
					}
				}
			}
		}

		retTy := Substitute(ft.Return, subs)

		// A result parameter only a `where` bound names, from the subject's
		// impl of it, as checkGenericCall solves it.
		if ContainsTypeParam(retTy) && c.solveFromWhereImpls(ft, subs) {
			retTy = Substitute(ft.Return, subs)
		}

		// Back-stop: solve any still-free result param from a known concrete
		// expected type (mirror of checkGenericCall's post-argument unification).
		// `n |> Result.Ok()` leaves E free after the piped arg pins T = Int; an
		// expected `Result<Int, String>` pins E = String, so the result doesn't
		// escape as `Result<Int, E>`. Post-argument + concrete-only: an ordinary
		// pipe mismatch still unifies against the un-seeded params first and reports
		// the normal message, and a non-matching expected fails cleanly here.
		if expected != nil && !ContainsTypeParam(expected) && ContainsTypeParam(retTy) {
			_ = c.unifyInto(expected, retTy, subs)
			retTy = Substitute(ft.Return, subs)
		}

		// A callee parameter nothing solved becomes a fresh inference
		// variable, as checkGenericCall's result does; the enclosing
		// function's own parameters stay. Returning it as is made the stage
		// `Iter<T>` with Iter.filter's own T whenever the piped value had no
		// type, and the next stage's lambda then saw that rigid T.
		if ContainsTypeParam(retTy) {
			retTy = instantiateUnboundCalleeParams(retTy, c.fnTypeParams, c)
		}

		// Interface-bound enforcement (mirror of checkGenericCall's loop): each
		// solved `T → Concrete` mapping must satisfy every interface in
		// T.Bounds. Records (concrete, bound) — and recurses into TypeArgs
		// for generic containers — so the manifest names the impls
		// dispatch will need. Without this, a piped generic call
		// (`xs |> show()`) records nothing for interface bounds, even though
		// the direct-call form already does.
		for tp, concrete := range subs {
			for _, bound := range tp.Bounds {
				if !typeImplementsInterface(c, concrete, bound, c.recPos(n.Line, n.Col), RecordingKindGenericBoundCheck) {
					c.boundError(n.Line, pipeBoundCol(n, pipeCall), concrete, bound, tp.Name_)
					continue
				}
				c.recordBoundConformance(concrete, bound.Name, c.recPos(n.Line, n.Col), RecordingKindGenericBoundCheck)
			}
		}

		// Method-level `where`-clause enforcement (constrained interface
		// defaults) — same as checkGenericCall, so a piped `xs |> Iter.sort_by(...)`
		// records the (key, Comparable) demand the direct-call form does.
		c.recordWhereBoundDemands(ft, subs, n.Line, n.Col, pipeBoundCol(n, pipeCall))

		// A solved `Partial<T>` parameter must resolve to a struct (Struct.update).
		// Skipped when a `Struct.method` conformance error already fired (the
		// conformance message subsumes the redundant Partial<self> one).
		if !structConformanceErrored {
			c.checkPartialParamStructs(ft, subs, n.Line, 1)
		}

		// Store instantiated function type on the call-site reference symbol.
		if len(subs) > 0 {
			instParams := make([]Type, len(ft.Params))
			for i, p := range ft.Params {
				instParams[i] = Substitute(p, subs)
			}
			instFT := &FuncType{Params: instParams, Return: retTy}
			// For pipes, the function node is either pipeCall.Func or n.Right.
			if pipeCall != nil {
				c.attachCallType(pipeCall.Func, instFT)
			} else {
				c.attachCallType(n.Right, instFT)
			}
		}

		if pipeCall != nil {
			c.noteUndeterminedCall(pipeCall.Func, n, ft, subs, slotArgs, errMark)
		} else {
			c.noteUndeterminedCall(n.Right, n, ft, subs, slotArgs, errMark)
		}
		return retTy
	}

	// Non-generic: simple type check. Uses argMatchesParam so concrete
	// implementers of interface params (e.g. std/iter's Seq<String> where
	// Iter<String> is expected) are accepted via the impl registry.
	//
	if pipedSlot < len(ft.Params) && argTy != nil && ft.Params[pipedSlot] != nil {
		if !c.argMatchesParam(argTy, ft.Params[pipedSlot], c.recPos(n.Line, n.Col), RecordingKindInterfaceTypedParam) {
			c.addMismatch(n.Line, 1, ft.Params[pipedSlot], argTy, c.typef("pipe argument type mismatch: expected %s, got %s", ft.Params[pipedSlot], argTy))
		}
	}

	// Check explicit args against their routed slots, a named argument
	// against the parameter it names, as the direct call does.
	if pipeCall != nil {
		defParams := c.callDefParams(pipeCall.Func)
		for i, arg := range pipeCall.Args {
			paramIdx := pipeSlots[i]
			label := positionalArgLabel(paramIdx)
			value := arg
			if na, ok := arg.(*ast.NamedArg); ok {
				paramIdx = namedArgSlot(na.Name, defParams)
				label = namedArgLabel(na.Name)
				value = na.Value
				if paramIdx < 0 {
					c.reportUnknownNamedArg(na, defParams)
				}
			}
			if paramIdx >= 0 && paramIdx < len(ft.Params) {
				expectedTy := ft.Params[paramIdx]
				c.registerArgSlotHint(arg, pipeCall.Func, paramIdx, expectedTy)
				valTy := c.checkNodeExpecting(value, expectedTy)
				if _, isPh := value.(*ast.Placeholder); !isPh && valTy != nil && expectedTy != nil {
					argLine, argCol := nodeLineCol(value)
					if !c.argMatchesParam(valTy, expectedTy, c.recPos(argLine, argCol), RecordingKindInterfaceTypedParam) {
						c.report(errAt(value, c.typef("%s: expected %s, got %s", label, expectedTy, valTy)).WithHint(defaultedFuncValueHint(expectedTy, valTy, value)).WithHint(embedsDowncastHint(expectedTy, valTy)))
					}
				}
			} else {
				c.checkNode(arg)
			}
		}
	}

	return ft.Return
}

// computePipeArgSlots returns the param slot each pipeCall.Args[i] targets.
// argShift is 1 when the piped value is prepended (no placeholder) and 0
// when the piped value substitutes for an explicit `_` placeholder in args.
// Named args claim their named param; remaining positional args fill slots
// left-to-right starting at argShift, skipping claimed slots. If the last
// positional arg is a lambda and the last param slot is unclaimed but not
// the lambda's identity-mapped slot, the lambda is re-routed to the last
// slot — matching the runtime's trailing-lambda routing in resolveArgs.
// Returns -1 for NamedArg entries (those are routed by name elsewhere).
func computePipeArgSlots(args []ast.Node, argShift int, paramNames []string, params []Type) []int {
	out := make([]int, len(args))
	claimed := make([]bool, len(params))
	for _, arg := range args {
		na, ok := arg.(*ast.NamedArg)
		if !ok {
			continue
		}
		for j, pn := range paramNames {
			if pn == na.Name {
				if j < len(claimed) {
					claimed[j] = true
				}
				break
			}
		}
	}
	nextSlot := argShift
	advance := func() int {
		for nextSlot < len(params) && claimed[nextSlot] {
			nextSlot++
		}
		s := nextSlot
		nextSlot++
		return s
	}
	lastPosIdx := -1
	seenNamed := false
	for i, arg := range args {
		if _, isNamed := arg.(*ast.NamedArg); isNamed {
			out[i] = -1
			seenNamed = true
			continue
		}
		// A positional before any named argument keeps its slot, as in
		// computePositionalSlots.
		if seenNamed {
			out[i] = advance()
		} else {
			out[i] = nextSlot
			nextSlot++
		}
		lastPosIdx = i
	}
	if lastPosIdx < 0 {
		return out
	}
	// A trailing named function routes like a trailing lambda, as in
	// computePositionalSlots.
	if !isLambdaArg(args[lastPosIdx]) && !routesAsTrailingFunc(args[lastPosIdx], params, out[lastPosIdx]) {
		return out
	}
	lastParam := len(params) - 1
	if lastParam < 0 || claimed[lastParam] {
		return out
	}
	if out[lastPosIdx] == lastParam {
		return out
	}
	out[lastPosIdx] = lastParam
	return out
}

func (c *checker) checkUnary(n *ast.Unary) Type {
	if a, isAssert := ungroupExpr(n.Right).(*ast.Assertion); isAssert && !a.Check {
		c.rejectAssertionOperand(a, n.Op)
	}
	operand := c.checkNode(n.Right)
	if operand == nil {
		return nil
	}

	switch n.Op {
	case "-":
		if operand != TypeInt && operand != TypeFloat && operand != TypeDecimal {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"unary - operand must be numeric, got %s", operand))
		}
		return operand

	case "!":
		if !TypesEqual(operand, TypeBool) {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"unary ! operand must be Bool, got %s", operand))
		}
		return TypeBool

	default:
		return nil
	}
}

// checkOpaqueConstructor reports an error if `dt` is an opaque distinct
// type whose owning module differs from the current file. The
// construction surface (the type name as a callable) is private to the
// defining module (spec §15, *Opaque distinct types*).
//
// Both unqualified (`PositiveInt(5)` — *ast.TypeIdent) and
// module-qualified (`positive_int.PositiveInt(5)` — *ast.FieldAccess
// whose Field carries the type name) forms are handled; the callee node
// is used only to position the error.
// checkOpaqueUnwrap reports an error when an unwrap call (e.g. `Int(p)`
// where `p: PositiveInt` and PositiveInt is opaque) crosses the type's
// module boundary. Same-module unwrap is the owner's right; outside
// callers must go through exported accessor functions.
func (c *checker) checkOpaqueUnwrap(arg ast.Node, argTy Type) {
	dt, ok := argTy.(*DistinctType)
	if !ok || dt == nil || !dt.Opaque {
		return
	}
	// Same-module unwrap is the owner's right.
	if c.fa != nil && c.fa.FilePath == dt.OwningSourceFile {
		return
	}
	c.addError(arg.LineNum(), 1, fmt.Sprintf(
		"cannot unwrap opaque type '%s' — its representation is private to its defining module; use an exported accessor", dt.Name))
}

// checkOpaqueEnumVariantCall reports an error when calling a variant
// constructor of an inline-body opaque enum from outside its owning
// module. Variant calls (`Active(...)` / `Status.Active(...)`) resolve
// to a FuncType whose Return is the enum type; this function inspects
// the return and the callee's References-table symbol to decide whether
// the call is a variant constructor that needs blocking.
func (c *checker) checkOpaqueEnumVariantCall(n *ast.Call, fnTy Type) {
	ft, ok := fnTy.(*FuncType)
	if !ok || ft == nil {
		return
	}
	et, ok := ft.Return.(*EnumType)
	if !ok || et == nil || !et.Opaque {
		return
	}
	// Same-module construction is the owner's right.
	if c.fa != nil && c.fa.FilePath == et.OwningSourceFile {
		return
	}
	// Confirm the callee is a variant constructor (not a top-level
	// function that happens to return the enum). Resolve via the
	// References table the builder populates for the callee identifier.
	var pos Pos
	switch fn := n.Func.(type) {
	case *ast.Ident:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.FieldAccess:
		if fn.Field != nil {
			pos = Pos{Line: fn.Field.Line, Col: fn.Field.Col}
		}
	}
	if pos == (Pos{}) || c.fa == nil {
		return
	}
	sym, found := c.fa.References[pos]
	if !found {
		return
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if real.Kind != SymbolEnumVariant {
		return
	}
	c.addError(pos.Line, pos.Col, fmt.Sprintf(
		"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", et.Name))
}

func (c *checker) checkOpaqueConstructor(callee ast.Node, dt *DistinctType) {
	if dt == nil || !dt.Opaque {
		return
	}
	// Same-module access is allowed. The current file's path on the
	// FileAnalysis matches the type's OwningSourceFile inside the
	// owning module; outside callers see different paths (or, for the
	// project entry's own opaque types, both paths are empty).
	if c.fa != nil && c.fa.FilePath == dt.OwningSourceFile {
		return
	}
	var line, col int
	switch fn := callee.(type) {
	case *ast.TypeIdent:
		line, col = fn.Line, fn.Col
	case *ast.FieldAccess:
		if fn.Field != nil {
			line, col = fn.Field.Line, fn.Field.Col
		} else {
			line, col = fn.Line, fn.Col
		}
	default:
		return
	}
	c.addError(line, col, fmt.Sprintf(
		"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", dt.Name))
}

// checkTypeParamQualifiedCall resolves a call whose qualifier is a generic
// type parameter — `T.method(x)` where `T: SomeIface`. It is the
// type-parameter analogue of an interface-qualified call: at runtime the call
// dispatches on the argument's type, and here we *type* it by the bound interface's method
// signature — the same FuncType an `Iface.method(x)` callee resolves to — so
// arguments and the return are checked identically.
//
// Returns (funcType, true) when the callee is type-parameter-qualified;
// `handled` is true even on error (a diagnostic is emitted and the type is
// nil) so checkCall does not fall back to permissive handling. Returns
// (nil, false) when the callee is not a type-parameter qualifier.
//
// The `T` occurrence keeps its type-parameter symbol — only the dispatch
// interface is derived, never written back — so hover / go-to-def on `T`
// stay truthful (the whole point of resolving here rather than rewriting the
// qualifier to the interface name).
func (c *checker) checkTypeParamQualifiedCall(n *ast.Call) (Type, bool) {
	fa, ok := n.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil {
		return nil, false
	}
	tp, providers, ok := c.typeParamQualifiedMember(fa)
	if !ok {
		return nil, false
	}
	if len(providers) == 1 {
		return typeParamQualifiedMethodFuncType(providers[0], fa.Field.Name, tp), true
	}
	// Zero providers, or more than one, is the same mistake whether the member
	// is called or taken bare, so the diagnostic is checkFieldAccess's — the
	// terminal that sees both positions. checkCall types this callee through
	// checkNode(n.Func) before reaching here, so the error has already been
	// reported at exactly this position; returning handled-with-no-type keeps
	// checkCall off its permissive fallback without reporting twice.
	return nil, true
}

// typeParamQualifiedMember resolves the qualifier of `T.member` to a type
// parameter of the enclosing function and reports which of its interface
// bounds declare `member`. `ok` is false when the qualifier is not a type
// parameter in scope — the member then resolves through the ordinary
// concrete-type and interface paths.
//
// The two positions a qualified member appears in share this: a call callee
// (checkTypeParamQualifiedCall) and a bare value (checkFieldAccess's
// terminal). Only the call path had it, so a bare `T.member` was typed
// permissively nil for ANY member name and the program died at run time on
// `unknown builtin 'T.member'`.
func (c *checker) typeParamQualifiedMember(fa *ast.FieldAccess) (*TypeParam_, []*InterfaceType, bool) {
	if fa == nil || fa.Field == nil {
		return nil, nil, false
	}
	var objName string
	switch o := fa.Object.(type) {
	case *ast.TypeIdent:
		objName = o.Name
	case *ast.Ident:
		objName = o.Name
	default:
		return nil, nil, false
	}
	tp, ok := c.fnTypeParams[objName]
	if !ok {
		return nil, nil, false
	}
	var providers []*InterfaceType
	for _, b := range tp.Bounds {
		if interfaceMethodFuncType(b, fa.Field.Name) != nil {
			providers = append(providers, b)
		}
	}
	return tp, providers, true
}

// reportTypeParamQualifiedMember is the diagnostic for a `T.member` whose
// bounds do not resolve it to exactly one interface. Called from
// checkFieldAccess's terminal for both a call callee and a bare value, so the
// wording is position-neutral.
func (c *checker) reportTypeParamQualifiedMember(fa *ast.FieldAccess, tp *TypeParam_, providers []*InterfaceType) {
	objName, method := tp.Name_, fa.Field.Name
	switch {
	case len(providers) > 1:
		c.addError(fa.Field.Line, fa.Field.Col, fmt.Sprintf(
			"'%s.%s' is ambiguous: bounds %s all declare '%s' — qualify by the interface instead (e.g. `%s.%s(...)`)",
			objName, method, interfaceBoundNames(providers), method, providers[0].Name, method))
	case len(tp.Bounds) == 0:
		c.addError(fa.Field.Line, fa.Field.Col, fmt.Sprintf(
			"type parameter '%s' has no interface bound, so '%s.%s' has nothing to dispatch through; add a `where %s: SomeInterface` bound whose interface declares '%s'",
			objName, objName, method, objName, method))
	default:
		c.addError(fa.Field.Line, fa.Field.Col, fmt.Sprintf(
			"no interface bound of type parameter '%s' (%s) declares function '%s'",
			objName, interfaceBoundNames(tp.Bounds), method))
	}
}

// qualifiedCallSpellings renders the qualified calls that replace `value.fn`,
// one per owner: "`HasName.name(h)`", or "`A.f(x)` or `B.f(x)`". A receiver
// that is not a plain name or field path is spelled `value`.
func qualifiedCallSpellings(owners []string, fn string, receiver ast.Node) string {
	arg := calleeText(unparen(receiver))
	if strings.Contains(arg, "?") {
		arg = "value"
	}
	calls := make([]string, len(owners))
	for i, owner := range owners {
		calls[i] = fmt.Sprintf("`%s.%s(%s)`", owner, fn, arg)
	}
	return strings.Join(calls, " or ")
}

// typeParamFunctionNotFieldHint is the help for `x.f` on a value of a type
// parameter. When a bound declares `fn f`, it names the qualified call;
// otherwise it says how to declare one.
func (c *checker) typeParamFunctionNotFieldHint(tp *TypeParam_, field string, receiver ast.Node) string {
	bounds := tp.Bounds
	if declared, ok := c.fnTypeParams[tp.Name_]; ok && len(bounds) == 0 {
		bounds = declared.Bounds
	}
	var providers []string
	for _, b := range bounds {
		if interfaceMethodFuncType(b, field) != nil {
			providers = append(providers, b.Name)
		}
	}
	switch len(providers) {
	case 0:
		return fmt.Sprintf(
			"a type parameter has no fields; declare `fn %s(value: self): ...` in its bound and call `%s.%s(value)`",
			field, tp.Name_, field)
	case 1:
		return fmt.Sprintf("`%s` declares `fn %s`; call it as %s",
			providers[0], field, qualifiedCallSpellings([]string{tp.Name_}, field, receiver))
	default:
		return fmt.Sprintf("bounds %s each declare `fn %s`; call it as %s",
			strings.Join(backquoted(providers), " and "), field, qualifiedCallSpellings(providers, field, receiver))
	}
}

// structFunctionNotFieldHint is the help for `p.f` on a struct value with no
// field `f`. When the struct's type has a function `f`, inherent or from an
// impl, it names the qualified call; otherwise it suggests a similar field.
func (c *checker) structFunctionNotFieldHint(st *StructType, field string, receiver ast.Node) string {
	providers := c.interfacesWritingMethod(st.Name, field)
	origin, _ := nominalOrigin(st)
	sym, _ := c.resolveTypeMethodSymbol(st.Name, field, origin)
	if sym == nil && len(providers) == 0 {
		return didYouMean(field, fieldNames(st.Fields))
	}
	var owners []string
	if len(providers) < 2 {
		owners = append(owners, st.Name)
	}
	owners = append(owners, providers...)
	return fmt.Sprintf("`%s` is a function of `%s`, not a field; call it as %s",
		field, st.Name, qualifiedCallSpellings(owners, field, receiver))
}

func backquoted(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "`" + n + "`"
	}
	return out
}

func typeParamQualifiedMethodFuncType(iface *InterfaceType, name string, self Type) *FuncType {
	if iface == nil {
		return nil
	}
	for _, m := range iface.Methods {
		if m.Name != name {
			continue
		}
		params := make([]Type, len(m.Params))
		copy(params, m.Params)
		ret := m.Return
		if ret == nil {
			ret = TypeUnit
		}
		subs := make(map[*TypeParam_]Type, len(iface.TypeParamDefs)+1)
		for i, def := range iface.TypeParamDefs {
			if i < len(iface.TypeArgs) {
				subs[def] = iface.TypeArgs[i]
			}
		}
		if iface.SelfParam != nil && self != nil {
			subs[iface.SelfParam] = self
		}
		if len(subs) > 0 {
			for i, p := range params {
				params[i] = Substitute(p, subs)
			}
			ret = Substitute(ret, subs)
		}
		return &FuncType{Params: params, Return: ret, DefaultCount: m.DefaultCount, WhereBounds: m.WhereBounds}
	}
	return nil
}

// interfaceBoundNames joins interface bound names for diagnostics.
func interfaceBoundNames(bounds []*InterfaceType) string {
	names := make([]string, len(bounds))
	for i, b := range bounds {
		names[i] = b.Name
	}
	return strings.Join(names, ", ")
}

func (c *checker) checkCall(n *ast.Call, expected Type) Type {
	c.markCallHoles(n)
	var fnTy Type
	sameOwnerBareCall := false
	if ident, ok := n.Func.(*ast.Ident); ok {
		fnTy, sameOwnerBareCall = c.sameOwnerBareCallType(ident)
		if sameOwnerBareCall {
			c.fa.recordExprType(ident, fnTy)
		}
	}
	if !sameOwnerBareCall {
		fnTy = c.checkCallee(n.Func)
	}
	if c.isTestingCheckCall(n) {
		return c.checkTestingCheckCall(n)
	}
	// When the callee is an identifier/type-identifier that resolves to an
	// import proxy symbol (Type=nil, Resolved=real), fall back to the real
	// symbol's type. This covers cases like imported variant constructors
	// (`Ok`, `Some`) and top-level imported functions — calling them as a
	// function still needs the real FuncType even though broader checkIdent
	// resolution intentionally stays nil-preserving to avoid regressions in
	// places where Type=nil is load-bearing (e.g. interface method dispatch).
	if fnTy == nil {
		fnTy = c.callFnTypeFromResolved(n.Func)
	}
	// Type-parameter-qualified dispatch: `T.method(x)` where T is a generic
	// type parameter bounded by an interface declaring `method`. Resolve and
	// validate it against T's bounds, typing the call by the bound interface's
	// method signature. `handled` true means the callee was type-param-
	// qualified (override fnTy with the resolved/`nil`-on-error type); leaves
	// fnTy untouched for ordinary callees.
	if ty, handled := c.checkTypeParamQualifiedCall(n); handled {
		fnTy = ty
	}
	// When the callee is an interface method (`Interface.method(...)`), pre-
	// specialise the interface's own type parameters from the first argument's
	// type args so the method's return type is pinned to the actual element
	// type. Without this, `Iter.each_while(x: Iter<T>, …)` returns a FuncType
	// with an unsolved interface-T, which checkGenericCall later tries to
	// solve from the enclosing function's return type — mis-binding it to the
	// outer return's element and producing nonsense scrutinee types.
	skipReturnInfer := false
	if ft, ok := fnTy.(*FuncType); ok && len(n.Args) > 0 {
		specialized := c.specializeInterfaceMethod(n.Func, ft, n.Args[0])
		if specialized != ft {
			fnTy = specialized
			// After interface-method specialisation, any remaining TypeParam_s
			// in the return belong to the caller's scope (e.g. the impl's T),
			// not callee unknowns. Don't unify the return against the enclosing
			// function's return type — that would mis-bind the caller's own T
			// to whatever element shape the outer return expects.
			skipReturnInfer = true
		}
	}

	// Opaque enum variant constructor: `Active(...)` or
	// `Status.Active(...)` where Status is an inline-body opaque enum.
	// The variant constructor exposes the variant shape, so block it
	// outside the owning module. Mirrors checkOpaqueConstructor for
	// distinct types and the StructLit/StructCallForm branches for
	// opaque structs. Same-module construction is the owner's right.
	c.checkOpaqueEnumVariantCall(n, fnTy)

	// A struct-shaped or embedded variant takes its shape's construction
	// forms (`Shape.Rect({...})`, `Shape.Id(5)`); see variant_construction.go.
	if form, ok := c.variantCtorCallee(n.Func, fnTy); ok {
		if ty, handled := c.checkVariantCtorCall(n, form); handled {
			return c.openVariantEnum(ty)
		}
	}

	// Distinct type constructor: Id(42) where Id is a DistinctType.
	// Only treat as constructor when called via TypeIdent (e.g. Callback(...)),
	// not when calling a variable of distinct type (e.g. handler(...)).
	if dt, ok := fnTy.(*DistinctType); ok {
		// Opacity check fires for both unqualified (TypeIdent) and
		// module-qualified (FieldAccess) constructor forms — the rule
		// blocks construction from outside the owning module regardless
		// of how the type name was written.
		c.checkOpaqueConstructor(n.Func, dt)
		if isTypeConstructorCallee(n.Func) {
			// Tuple-distinct (inner is a TupleType of arity N>=2) accepts
			// either a single tuple-typed arg (call-form: `Pair((1, "x"))`)
			// or N positional args matching the inner element types
			// (literal-attach: `Pair(1, "x")`). Other shapes are arity errors.
			if innerTup, ok := dt.Inner.(*TupleType); ok && len(innerTup.Elems) >= 2 {
				c.checkTupleDistinctArgs(n, dt, innerTup)
				return dt
			}
			if callHasPlaceholder(n.Args) {
				for _, arg := range n.Args {
					c.checkNode(arg)
				}
				return dt
			}
			// The argument is checked against the wrapped type, which is
			// also pushed as its expected type: a lambda literal passed to
			// `Callback(|s| ...)` (Callback wraps `(String) -> String`) gets
			// its parameters inferred. See distinct_construction.go.
			return c.checkDistinctCtorCall(n, dt)
		}
		// Calling a variable of function-wrapping distinct type — unwrap to inner.
		if innerFt, ok := dt.Inner.(*FuncType); ok {
			fnTy = innerFt
		}
	}

	// A type name called as a function: a struct's record call form
	// (`Point({x: 1})`) or a distinct's unwrap (`Int(id)`); anything else
	// (`Int("4")`, `String(4)`) is an error (type_name_value.go). fnTy may
	// be nil (no symbol Type attached) or the named type itself (the symbol
	// for `Int` carries PrimitiveType Int, not a FuncType).
	if ref, resolved, ok := c.calleeNamedType(n.Func, fnTy); ok {
		// Nominal struct call-form: Foo({a: 1, b: "x"}) and
		// Foo(defaults()) both coerce an anonymous struct into the
		// named struct value. Parallels the literal-attach form
		// Foo{a: 1, b: "x"}. An argument that is not a record is an
		// error — the coercion is keyed on the CALL and nothing
		// wider.
		if st, ok := resolved.(*StructType); ok {
			return c.checkStructCallForm(n, st)
		}
		argTys := make([]Type, len(n.Args))
		for i, arg := range n.Args {
			argTys[i] = c.checkNode(arg)
		}
		return c.checkTypeNameCall(ref, resolved, n.Args, argTys)
	}

	// Operator-interface functions (`add`, `divide`, etc.) are selected from
	// their full operand shape. Run that selection before the older bare-name
	// dispatch rejection so `divide(10, nz)` can resolve to
	// `impl Divide<NonZeroInt, Int> for Int`.
	if ty, handled := c.checkOperatorInterfaceCall(n); handled {
		return ty
	}

	// Dispatch methods (from impl/extend blocks) are runtime-dispatched,
	// so skip argument type-checking — the checker only sees one impl's
	// signature. Detect this before the `ft == nil` guard and the
	// generic-call fork: a bare-name call to an impl-block method or a
	// default method must be marked with its resolved interface
	// regardless of whether the method's callee type resolved to a
	// FuncType here (interface-method symbols may not carry one) and
	// regardless of whether the method's type contains a TypeParam
	// (default methods and `self`-parametered impl methods are generic
	// and would otherwise return via checkGenericCall before the
	// marking runs).
	isDispatch := false
	if ident, ok := n.Func.(*ast.Ident); ok && c.fa.DispatchNames[ident.Name] && !sameOwnerBareCall {
		// Bare-name dispatch is rejected. A bare call whose name resolves to an
		// interface method is an error (qualify it as `Iface.method` or
		// `Type.method`); a name that resolves to a local
		// `fn`/lambda shadowing an impl method is left alone (handled inside
		// rejectBareNameDispatch via bareNameDispatchInterface returning "").
		isDispatch = c.rejectBareNameDispatch(ident)
	}
	ft, _ := fnTy.(*FuncType)

	if ft == nil {
		// Type-qualified call to a missing member is reported at
		// checkFieldAccess's terminal (which sees both call callees and bare
		// value access), not here — so the diagnostic is uniform and never
		// double-fires. Still check arguments so nested calls get validated.
		for _, arg := range n.Args {
			c.checkNode(arg)
		}
		// A variant constructor's callee can carry its enum's type (an
		// embedded host-type payload, `Wrap.One(#[1])`); it is a function.
		if notFn := notCallable(fnTy); notFn != nil && !calleeIsVariantConstructor(c, n.Func) {
			c.report(errAt(n.Func, c.typef("`%s` is not a function", notFn)))
		}
		return nil
	}

	// Determine if this is a generic call (params or return contain TypeParam_).
	isGeneric := ContainsTypeParam(ft.Return)
	for _, p := range ft.Params {
		if ContainsTypeParam(p) {
			isGeneric = true
			break
		}
	}

	if isGeneric {
		return c.checkGenericCall(n, ft, skipReturnInfer, expected)
	}

	// Variant tuple-payload literal-attach: a call to a 1-arg variant
	// constructor whose param is a tuple of arity N, where the call
	// supplied N positional args. Validates each arg against the
	// corresponding tuple element and short-circuits the generic
	// argument-count / per-slot machinery below.
	if !isDispatch && c.checkVariantTuplePayloadFlatCall(n, ft) {
		return Substitute(ft.Return, nil)
	}

	// Check argument count (skip for dispatch methods). A partial
	// application's `_` is an argument like any other: `add(_, 1, 2)` gives a
	// two-parameter add three, and `add(_)` leaves b with nothing.
	minArgs := len(ft.Params) - ft.DefaultCount
	if !isDispatch && (len(n.Args) < minArgs || len(n.Args) > len(ft.Params)) {
		if ft.DefaultCount > 0 {
			c.addError(n.Line, 1, fmt.Sprintf(
				"expected %d to %d arguments, got %d", minArgs, len(ft.Params), len(n.Args)))
		} else {
			c.addError(n.Line, 1, fmt.Sprintf(
				"expected %d arguments, got %d", len(ft.Params), len(n.Args)))
		}
	}

	// Pre-compute the positional → param slot mapping. The base mapping
	// is identity (positional i → param i), with the runtime's
	// trailing-lambda routing applied: if the last positional is a lambda
	// and the last param slot would be empty under straight positional
	// fill (and isn't claimed by a named arg), route the lambda to the
	// last slot so its callback type drives lambda inference.
	posSlots := computePositionalSlots(n.Args, ft.Params, c.callDefParams(n.Func))
	if !isDispatch {
		for _, na := range positionalNamedConflicts(n.Args, posSlots, c.callDefParams(n.Func), 0) {
			c.addError(na.Line, 1, fmt.Sprintf("parameter '%s' already has a value", na.Name))
		}
		if len(n.Args) >= minArgs && len(n.Args) <= len(ft.Params) {
			c.checkRequiredSlotsFilled(n, n.Args, posSlots, -1, ft.Params)
		}
	}

	// Resolve each call-site arg to its target param slot, tracking
	// placeholders separately so partial-application can rebuild a
	// FuncType whose params match the placeholder slots in encounter order.
	// remainingDefaults counts how many of those placeholder slots were
	// defaulted in the original fn — the partial inherits those defaults
	// at runtime (callFuncWithSlots fills nil slots from the original's
	// Defaults), so the partial's FuncType reflects them as DefaultCount.
	var remainingParams []Type
	remainingDefaults := 0
	calleeDefaults := c.callDefParamHasDefault(n.Func)
	for i, arg := range n.Args {
		switch a := arg.(type) {
		case *ast.NamedArg:
			defParams := c.callDefParams(n.Func)
			slot := namedArgSlot(a.Name, defParams)
			if slot < 0 || slot >= len(ft.Params) {
				if slot < 0 && !isDispatch {
					c.reportUnknownNamedArg(a, defParams)
				}
				c.checkNode(a.Value)
				continue
			}
			if _, isPh := a.Value.(*ast.Placeholder); isPh {
				remainingParams = append(remainingParams, ft.Params[slot])
				if slot < len(calleeDefaults) && calleeDefaults[slot] {
					remainingDefaults++
				}
				c.registerArgSlotHint(a.Value, n.Func, slot, ft.Params[slot])
				continue
			}
			c.registerArgSlotHint(arg, n.Func, slot, ft.Params[slot])
			expectedParamTy := ft.Params[slot]
			argTy := c.checkNodeExpecting(a.Value, expectedParamTy)
			if !isDispatch && expectedParamTy != nil && argTy != nil {
				argLine, argCol := nodeLineCol(a.Value)
				if !c.argMatchesParam(argTy, expectedParamTy, c.recPos(argLine, argCol), RecordingKindInterfaceTypedParam) {
					c.report(errAt(a.Value, c.typef(
						"argument '%s': expected %s, got %s", a.Name, expectedParamTy, argTy)).WithHint(defaultedFuncValueHint(expectedParamTy, argTy, a.Value)).WithHint(c.wholeResultHint(expectedParamTy, argTy, a.Value)).WithHint(embedsDowncastHint(expectedParamTy, argTy)))
				}
			}
		case *ast.Placeholder:
			slot := posSlots[i]
			if slot >= 0 && slot < len(ft.Params) {
				remainingParams = append(remainingParams, ft.Params[slot])
				if slot < len(calleeDefaults) && calleeDefaults[slot] {
					remainingDefaults++
				}
				c.registerArgSlotHint(arg, n.Func, slot, ft.Params[slot])
			}
		default:
			slot := posSlots[i]
			if slot >= 0 && slot < len(ft.Params) {
				c.registerArgSlotHint(arg, n.Func, slot, ft.Params[slot])
			}
			var expectedParamTy Type
			if !isDispatch && slot >= 0 && slot < len(ft.Params) {
				expectedParamTy = ft.Params[slot]
			}
			argTy := c.checkNodeExpecting(arg, expectedParamTy)
			if !isDispatch && slot >= 0 && slot < len(ft.Params) && ft.Params[slot] != nil && argTy != nil {
				argLine, argCol := nodeLineCol(arg)
				if !c.argMatchesParam(argTy, ft.Params[slot], c.recPos(argLine, argCol), RecordingKindInterfaceTypedParam) {
					c.report(errAt(arg, c.typef(
						"argument %d: expected %s, got %s", i+1, ft.Params[slot], argTy)).WithHint(defaultedFuncValueHint(ft.Params[slot], argTy, arg)).WithHint(c.wholeResultHint(ft.Params[slot], argTy, arg)).WithHint(embedsDowncastHint(ft.Params[slot], argTy)))
				}
			}
		}
	}

	// Partial application: return a function type with remaining params.
	// Defaults from the original fn for the unbound slots are preserved
	// at runtime, so reflect them in the partial's FuncType.
	if len(remainingParams) > 0 {
		return &FuncType{Params: remainingParams, Return: ft.Return, DefaultCount: remainingDefaults}
	}

	return ft.Return
}

// notCallable is a callee's type when it is a data type no call can apply
// (`1()`, `""()`, a struct value called), or nil. A callee with no type, or
// one still open, is left to whatever reported it.
func notCallable(t Type) Type {
	switch v := resolveTypeVar(t).(type) {
	case *PrimitiveType, *StructType, *EnumType, *TupleType, *ListType, *MapType, *AnonStructType:
		return v
	case *DistinctType:
		if _, isFn := v.Inner.(*FuncType); !isFn {
			return v
		}
	}
	return nil
}

func (c *checker) implMethodFuncType(impl ast.ImplMethodDecl) *FuncType {
	if c == nil || c.fa == nil || impl == nil {
		return nil
	}
	if fn, ok := impl.(*ast.FuncDef); ok && c.fa.ProjectImpls != nil {
		if ft, ok := c.fa.ProjectImpls.ImplFuncTypes[fn].(*FuncType); ok {
			return ft
		}
	}
	pos := Pos{Line: impl.LineNum(), Col: implColumn(impl)}
	if sym := c.fa.Definitions[pos]; sym != nil {
		if ft, ok := sym.Type.(*FuncType); ok {
			return ft
		}
	}
	if recv := implReceiverForLookup(c.fa, impl); recv != "" && implName(impl) != "" {
		if sym := c.fa.LookupTypeMethod(recv, implName(impl)); sym != nil {
			if ft, ok := sym.Type.(*FuncType); ok {
				return ft
			}
		}
		if c.fa.ProjectImpls != nil {
			if sym := c.fa.ProjectImpls.TypeMethods[recv][implName(impl)]; sym != nil {
				if ft, ok := sym.Type.(*FuncType); ok {
					return ft
				}
			}
		}
	}
	return nil
}

func implColumn(impl ast.ImplMethodDecl) int {
	switch v := impl.(type) {
	case *ast.FuncDef:
		return v.Col
	case *ast.ExternFunc:
		return v.Col
	default:
		return 0
	}
}

func implName(impl ast.ImplMethodDecl) string {
	switch v := impl.(type) {
	case *ast.FuncDef:
		return v.Name
	case *ast.ExternFunc:
		return v.Name
	default:
		return ""
	}
}

func implReceiverForLookup(fa *FileAnalysis, impl ast.ImplMethodDecl) string {
	switch v := impl.(type) {
	case *ast.FuncDef:
		if fa != nil && fa.ProjectImpls != nil {
			if r := fa.ProjectImpls.ReceiverOf(v); r != "" {
				return r
			}
		}
		if fa != nil && fa.ImplBlockReceiver != nil {
			if r := fa.ImplBlockReceiver[v]; r != "" {
				return r
			}
		}
		return funcDefReceiverBaseName(v)
	case *ast.ExternFunc:
		if fa != nil && fa.ImplBlockReceiverExtern != nil {
			return fa.ImplBlockReceiverExtern[v]
		}
		if len(v.Params) > 0 && v.Params[0].TypeAnnotation != nil {
			return TypeExprBaseName(v.Params[0].TypeAnnotation)
		}
	}
	return ""
}

func isTypeConstructorCallee(n ast.Node) bool {
	switch n.(type) {
	case *ast.TypeIdent, *ast.FieldAccess:
		return true
	default:
		return false
	}
}

func (c *checker) checkOperatorInterfaceCall(n *ast.Call) (Type, bool) {
	if len(n.Args) != 2 {
		return nil, false
	}
	opIface, ok := c.operatorInterfaceForCall(n.Func)
	if !ok {
		return nil, false
	}
	left := c.checkNode(n.Args[0])
	right := c.checkNode(n.Args[1])
	if left == nil || right == nil {
		return nil, true
	}
	rhsExpected, result, matched := c.lookupOperatorImplTypeArgs(opIface, left, right)
	if !matched {
		if rhsExpected != nil {
			// One impl for this receiver: argument 2 is what is wrong.
			at := operandPos(n.Args[1], n.Line, n.Col)
			c.addMismatch(at.Line, at.Col, rhsExpected, right, c.typef("argument 2: expected %s, got %s", rhsExpected, right))
			return result, true
		}
		at := operandPos(n, n.Line, n.Col)
		c.addError(at.Line, at.Col, c.typef(
			"no matching %s impl for %s %s %s", opIface.Interface, left, opIface.Op, right))
		return nil, true
	}
	if err := c.unify(rhsExpected, right, nil); err != nil && !TypesEqual(rhsExpected, right) {
		at := operandPos(n.Args[1], n.Line, n.Col)
		c.addMismatch(at.Line, at.Col, rhsExpected, right, c.typef("argument 2: expected %s, got %s", rhsExpected, right))
	}
	if name := interfaceImplName(resolveTypeVar(left)); name != "" {
		c.fa.RecordManifest(name, opIface.Interface,
			Recording{Pos: c.recPos(n.Line, n.Col), Kind: RecordingKindCallSite})
		c.recordImplReceiverBoundConformances(left, opIface.Interface, c.recPos(n.Line, n.Col), RecordingKindCallSite)
	}
	if fa, ok := n.Func.(*ast.FieldAccess); ok {
		c.attachDispatchImplForInterfaceKey(fa, opIface.Interface, fmt.Sprintf("%s<%s, %s>", opIface.Interface, rhsExpected, result), left)
	} else if ident, ok := n.Func.(*ast.Ident); ok {
		c.attachBareDispatchImplForInterfaceKey(ident, opIface.Interface, fmt.Sprintf("%s<%s, %s>", opIface.Interface, rhsExpected, result), left)
	}
	return result, true
}

func (c *checker) operatorInterfaceForCall(fn ast.Node) (operatorInterface, bool) {
	switch f := fn.(type) {
	case *ast.FieldAccess:
		if f.Field == nil {
			return operatorInterface{}, false
		}
		opIface, ok := operatorInterfaceByMethod(f.Field.Name)
		if !ok {
			return operatorInterface{}, false
		}
		if iface, ok := interfaceObjectType(c, f.Object, nil); ok && iface.Name == opIface.Interface {
			return opIface, true
		}
		recvName := c.typeQualifierName(f.Object)
		if recvName == "" {
			return operatorInterface{}, false
		}
		for _, provider := range c.interfacesDeclaringMethod(recvName, opIface.Method) {
			if provider == opIface.Interface {
				return opIface, true
			}
		}
		return operatorInterface{}, false
	case *ast.Ident:
		opIface, ok := operatorInterfaceByMethod(f.Name)
		if !ok {
			return operatorInterface{}, false
		}
		if iface := c.bareNameDispatchInterface(f); iface != "" {
			if iface == opIface.Interface {
				return opIface, true
			}
			return operatorInterface{}, false
		}
		if c.selfTypeName != "" {
			for _, provider := range c.interfacesDeclaringMethod(c.selfTypeName, f.Name) {
				if provider == opIface.Interface {
					return opIface, true
				}
			}
		}
		return operatorInterface{}, false
	default:
		return operatorInterface{}, false
	}
}

func operatorInterfaceByMethod(method string) (operatorInterface, bool) {
	for _, iface := range operatorInterfaces {
		if iface.Method == method {
			return iface, true
		}
	}
	return operatorInterface{}, false
}

func articleFor(name string) string {
	if name == "" {
		return "a"
	}
	switch name[0] {
	case 'A', 'E', 'I', 'O', 'U':
		return "an"
	default:
		return "a"
	}
}

func (c *checker) typeQualifierName(n ast.Node) string {
	switch o := n.(type) {
	case *ast.TypeIdent:
		return o.Name
	case *ast.FieldAccess:
		return c.namespaceQualifiedTypeName(o)
	default:
		return ""
	}
}

func (c *checker) isTestingCheckCall(n *ast.Call) bool {
	fa, ok := n.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "check" {
		return false
	}
	switch obj := fa.Object.(type) {
	case *ast.Ident:
		return obj.Name == "testing"
	case *ast.TypeIdent:
		return obj.Name == "testing"
	default:
		return false
	}
}

func (c *checker) checkTestingCheckCall(n *ast.Call) Type {
	line, col := testingCheckKeywordPos(n)
	if len(n.Args) != 1 {
		c.addError(n.Line, 1, fmt.Sprintf("testing.check expects 1 argument, got %d", len(n.Args)))
		return c.resultType(TypeUnit, c.assertionFailureType())
	}
	if _, ok := n.Args[0].(*ast.NamedArg); ok {
		c.addError(n.Line, 1, "testing.check expects a positional argument")
		return c.resultType(TypeUnit, c.assertionFailureType())
	}

	inputTy := c.checkNode(n.Args[0])
	successTy, ok := c.assertionSuccessType(inputTy, false, line, col)
	if !ok {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"testing.check expects Bool, Result, Maybe, or Assertable, got %s", inputTy))
	}
	resultTy := c.resultType(successTy, c.assertionFailureType())
	c.registerAssertionHoverAt(line, col, n, "check", inputTy, successTy, resultTy)
	return resultTy
}

func testingCheckKeywordPos(n *ast.Call) (int, int) {
	if fa, ok := n.Func.(*ast.FieldAccess); ok && fa.Field != nil {
		return fa.Field.Line, fa.Field.Col
	}
	return n.Line, n.Col
}

// callDefParamHasDefault returns a parallel-to-Params bool slice telling
// whether each slot of the function being called has a default value.
// Returns nil for callees we can't resolve to a FuncDef/ExternFunc.
func (c *checker) callDefParamHasDefault(funcNode ast.Node) []bool {
	params := c.calleeParams(funcNode)
	if params == nil {
		return nil
	}
	out := make([]bool, len(params))
	for i, p := range params {
		out[i] = p.Default != nil
	}
	return out
}

// checkRequiredSlotsFilled reports the first parameter without a default that
// a call leaves empty. The argument count alone cannot see it: for
// `fn middle(a: Int, b: Int = 10, c: Int)`, `middle(1, 2)` has as many
// arguments as required parameters, but positional arguments fill slots in
// order, so 2 lands in `b` and `c` gets nothing. slots is the positional
// routing (computePositionalSlots or computePipeArgSlots); pipedSlot is the
// slot a pipe fills, or -1. A final positional function reference routes to
// a trailing callback parameter as a lambda does (routesAsTrailingFunc),
// which computePipeArgSlots does not apply, so it is applied here.
func (c *checker) checkRequiredSlotsFilled(call *ast.Call, args []ast.Node, slots []int, pipedSlot int, params []Type) {
	nParams := len(params)
	names := c.callDefParams(call.Func)
	defaults := c.callDefParamHasDefault(call.Func)
	if nParams == 0 || len(names) != nParams || len(defaults) != nParams {
		return
	}
	filled := make([]bool, nParams)
	positional := make([]bool, nParams)
	if pipedSlot >= 0 && pipedSlot < nParams {
		filled[pipedSlot] = true
	}
	lastPos := -1
	for i, arg := range args {
		if na, ok := arg.(*ast.NamedArg); ok {
			for j, name := range names {
				if name == na.Name {
					filled[j] = true
					break
				}
			}
			continue
		}
		lastPos = i
	}
	for i, arg := range args {
		if _, ok := arg.(*ast.NamedArg); ok || i >= len(slots) {
			continue
		}
		slot := slots[i]
		if i == lastPos && !filled[nParams-1] && routesAsTrailingFunc(arg, params, slot) {
			slot = nParams - 1
		}
		if slot >= 0 && slot < nParams {
			filled[slot] = true
			positional[slot] = true
		}
	}
	for j := 0; j < nParams; j++ {
		if filled[j] || defaults[j] {
			continue
		}
		msg := fmt.Sprintf("missing argument for parameter '%s'", names[j])
		for k := 0; k < j; k++ {
			if defaults[k] && positional[k] {
				msg += fmt.Sprintf("; positional arguments fill parameters in order and cannot skip "+
					"the defaulted '%s', so pass this one by name (%s: ...)", names[k], names[j])
				break
			}
		}
		line, col := stmtStart(call)
		c.addError(line, col, msg)
		return
	}
}

// computePositionalSlots returns a slice mapping the i-th positional arg
// in `args` to a param slot index in `params`. The base mapping is identity
// (positional i → param i). When the runtime's trailing-lambda routing
// applies — last positional is a lambda, the last param slot is empty under
// straight positional fill, and no named arg targets that slot — the last
// positional's slot is changed to the last param index so the lambda's
// expected callback type drives lambda inference. Slots claimed by named
// args are skipped over (so positional slots stay consecutive among
// non-named slots).
//
// For non-positional args (NamedArg, etc.) the corresponding entry is -1
// (unused); callers index this by their own positional counter.
func computePositionalSlots(args []ast.Node, params []Type, paramNames []string) []int {
	out := make([]int, len(args))
	// Track which slots named args claim so positional fill skips them.
	claimed := make([]bool, len(params))
	for _, arg := range args {
		na, ok := arg.(*ast.NamedArg)
		if !ok {
			continue
		}
		for j, pn := range paramNames {
			if pn == na.Name {
				if j < len(claimed) {
					claimed[j] = true
				}
				break
			}
		}
	}
	// Fill positional slots left-to-right, skipping claimed ones.
	nextSlot := 0
	advance := func() int {
		for nextSlot < len(params) && claimed[nextSlot] {
			nextSlot++
		}
		s := nextSlot
		nextSlot++
		return s
	}
	// A positional written before any named argument means the slot it
	// sits in: `add(1, a: 2)` puts 1 in
	// slot 0, which is the duplicate positionalNamedConflicts reports. Only
	// one written after a named argument skips the slots names claimed.
	posIdx := 0
	seenNamed := false
	for i, arg := range args {
		if _, isNamed := arg.(*ast.NamedArg); isNamed {
			out[i] = -1
			seenNamed = true
			continue
		}
		if seenNamed {
			out[i] = advance()
		} else {
			out[i] = nextSlot
			nextSlot++
		}
		posIdx++
	}
	// Trailing-lambda routing: if the last positional is a lambda, its
	// assigned slot isn't the last param slot, and the last param slot
	// isn't claimed, route the lambda to the last slot.
	if posIdx == 0 || len(params) == 0 {
		return out
	}
	lastPosArgIdx := -1
	for i := len(args) - 1; i >= 0; i-- {
		if _, isNamed := args[i].(*ast.NamedArg); !isNamed {
			lastPosArgIdx = i
			break
		}
	}
	if lastPosArgIdx < 0 {
		return out
	}
	lastParam := len(params) - 1
	if !isLambdaArg(args[lastPosArgIdx]) &&
		!routesAsTrailingFunc(args[lastPosArgIdx], params, out[lastPosArgIdx]) {
		return out
	}
	if claimed[lastParam] {
		return out
	}
	if out[lastPosArgIdx] == lastParam {
		return out
	}
	out[lastPosArgIdx] = lastParam
	return out
}

// positionalNamedConflicts reports each named argument whose parameter a
// positional argument already fills: `sum(1, a: 2)`, where the 1 was written
// before any named argument and so means slot 0. Such a call gives one
// parameter two values, so it is rejected here.
// slots is the positional routing (computePositionalSlots or
// computePipeArgSlots, -1 for a named argument) and offset the slots filled
// ahead of args, a piped value's.
func positionalNamedConflicts(args []ast.Node, slots []int, paramNames []string, offset int) []*ast.NamedArg {
	filled := map[int]bool{}
	for i := 0; i < offset; i++ {
		filled[i] = true
	}
	for i, arg := range args {
		if _, isNamed := arg.(*ast.NamedArg); isNamed || i >= len(slots) || slots[i] < 0 {
			continue
		}
		filled[slots[i]] = true
	}
	var out []*ast.NamedArg
	for _, arg := range args {
		na, isNamed := arg.(*ast.NamedArg)
		if !isNamed {
			continue
		}
		for j, pn := range paramNames {
			if pn == na.Name {
				if filled[j] {
					out = append(out, na)
				}
				break
			}
		}
	}
	return out
}

// routesAsTrailingFunc extends trailing-lambda routing to a function passed
// by name. `Iter.map(xs, |n| n * 2)` and `Iter.map(xs, double)` mean the same
// thing, so routing that depends on which one you wrote is a rule about
// syntax where the reader expects a rule about arguments.
//
// A bare name is not self-evidently a function the way `|n| ...` is, and
// inferring its type here would be circular for the lambda case. The
// parameter shapes settle it instead: route only when the last parameter
// takes a function and the slot the name would otherwise fill does not.
// Where both take functions, there is a real choice between them, and
// left-to-right order is the one the call already states.
//
// The runtime applies the same rule in resolveArgs. The two must agree —
// when they disagree, the analyzer checks the argument against one
// parameter and the call passes it to another.
func routesAsTrailingFunc(arg ast.Node, params []Type, currentSlot int) bool {
	if _, ok := arg.(*ast.Ident); !ok {
		return false
	}
	if len(params) == 0 {
		return false
	}
	if _, ok := params[len(params)-1].(*FuncType); !ok {
		return false
	}
	if currentSlot < 0 || currentSlot >= len(params) {
		return false
	}
	_, currentTakesFunc := params[currentSlot].(*FuncType)
	return !currentTakesFunc
}

// isLambdaArg reports whether a call-site arg is a lambda (or block, used
// as an implicit-`it` lambda in some forms). A field accessor `.name` is a
// function written in place, and routes as a lambda does
// (`Iter.sort_by(users, .name)`).
func isLambdaArg(n ast.Node) bool {
	switch n.(type) {
	case *ast.Lambda, *ast.FieldAccessor:
		return true
	case *ast.Block:
		return true
	}
	return false
}

// markCallHoles records each `_` argument of call, positional or named
// (`port: _`), as a partial-application hole: the one place `_` is a value.
// Every path that checks the arguments may then visit the `_` without
// knowing it is one.
func (c *checker) markCallHoles(call *ast.Call) {
	for _, arg := range call.Args {
		if na, ok := arg.(*ast.NamedArg); ok {
			arg = na.Value
		}
		if ph, ok := arg.(*ast.Placeholder); ok {
			if c.callHoles == nil {
				c.callHoles = map[*ast.Placeholder]bool{}
			}
			c.callHoles[ph] = true
		}
	}
}

// placeholderValueMsg is the error for `_` read as a value outside a call's
// argument list, and placeholderValueHint says where `_` does stand for one.
const (
	placeholderValueMsg  = "`_` has no value here"
	placeholderValueHint = "`_` stands for a missing argument only in a call, as in add(1, _)"
)

// callHasPlaceholder reports whether any call-site arg is a `_` placeholder,
// either positionally or as a named-arg value (`f(port: _)`).
func callHasPlaceholder(args []ast.Node) bool {
	for _, arg := range args {
		switch a := arg.(type) {
		case *ast.Placeholder:
			return true
		case *ast.NamedArg:
			if _, ok := a.Value.(*ast.Placeholder); ok {
				return true
			}
		}
	}
	return false
}

// callDefParams returns the param-name list of the function being called via
// funcNode, by following the funcNode's reference symbol back to its
// declaration. Returns nil for callees we can't resolve to a known
// param-bearing definition (FuncDef, ExternFunc, or a binding to a Lambda).
func (c *checker) callDefParams(funcNode ast.Node) []string {
	params := c.calleeParams(funcNode)
	if params == nil {
		return nil
	}
	out := make([]string, len(params))
	for i, p := range params {
		out[i] = p.Name
	}
	return out
}

// argMatchesParam reports whether argTy is acceptable where paramTy is expected.
// For interface-typed params it consults the impl registry so concrete
// implementers are accepted (e.g. List<Int> where Iter<Int> is expected).
// recPos / recKind are forwarded to the underlying recordConformanceIfConcrete
// when an interface-typed param triggers a recording. Callers pass the
// argument or call expression's source position so the demand site is
// preserved in the manifest.
func (c *checker) argMatchesParam(argTy, paramTy Type, recPos Pos, recKind RecordingKind) bool {
	if TypeAssignable(paramTy, argTy) {
		return true
	}
	// A function value enters a function-typed position when its type is
	// assignable to the position's: contravariant parameters, covariant result
	// (UnifyInto).
	if _, isFunc := resolveTypeVar(paramTy).(*FuncType); isFunc {
		if _, isFunc := resolveTypeVar(argTy).(*FuncType); isFunc {
			return c.unifyInto(paramTy, argTy, nil) == nil
		}
	}
	// Anonymous records admit each field at its declared type, including
	// interface fields whose values are concrete implementations.
	if expected, ok := paramTy.(*AnonStructType); ok {
		actual, ok := argTy.(*AnonStructType)
		if !ok || len(actual.Fields) != len(expected.Fields) {
			return false
		}
		for _, field := range expected.Fields {
			value, found := scopedField(actual, field.Name)
			if !found || !c.argMatchesParam(value.Type, field.Type, recPos, recKind) {
				return false
			}
		}
		return true
	}
	// A `Partial<T>` parameter (Struct.update, or a user function taking a
	// patch) accepts a deep partial of T. Struct.update is generic so it flows
	// through checkGenericCall; this branch handles a non-generic function with a
	// `Partial<ConcreteType>` parameter, keeping the built-in coherent wherever
	// it's written.
	if pt, ok := paramTy.(*PartialType); ok {
		return c.partialPatchOK(argTy, pt.Inner, recPos.Line, recPos.Col)
	}
	if iface, ok := paramTy.(*InterfaceType); ok {
		return c.ifaceParamAdmits(argTy, iface, recPos, recKind)
	}
	if _, ok := argTy.(*InterfaceType); ok {
		return c.unify(argTy, paramTy, nil) == nil
	}
	// Bare-variant constructors like `None` start as `Maybe<T>` and get
	// instantiated to `Maybe<?α>` by checkIdent. At a monomorphic call site
	// (`maybe_value(None)` where the param is concrete `Maybe<Int>`), the
	// fresh TypeVar needs to bind through unification — otherwise TypesEqual
	// rejects the structural mismatch even though the types are compatible.
	// The parameter can hold one too: a partial application of a generic
	// function (`f = Iter.map(xs, _)`, `((Int) -> ?1) -> Iter<?1>`) leaves
	// the hole its argument fills.
	if containsTypeVar(argTy) || containsTypeVar(paramTy) {
		return c.unifyInto(paramTy, argTy, nil) == nil
	}
	return false
}

// ifaceParamAdmits reports whether `argTy` is admissible where the interface
// type `iface` is expected, and records the (concrete, iface) conformance when
// it is. This is THE interface-impl admission rule: every position that expects
// an interface and accepts a concrete implementer must come through here, so
// there is one implementation of "does this type satisfy this interface at a
// value position" rather than one per syntactic form.
//
// Two callers today, deliberately:
//
//   - `argMatchesParam` — an interface-typed parameter at a call site, which is
//     also how a struct FIELD is checked in the constructor-call record forms
//     (`checkStructLitAgainstStruct` / `checkAnonStructTypeAgainstStruct`).
//   - `deepPartialMatches` — an interface-typed field inside a `Partial<T>`
//     patch, i.e. `Struct.update(cfg, {clock: FakeClock{...}})`. That position
//     was refused before this helper existed, because the deep-partial walk
//     tested each field with bare `Unify` and interface admission lives here
//     rather than in the unifier. See partial.go's header.
//
// The recording is the part a second copy of this rule would most plausibly
// drop: `recordConformanceIfConcrete` is what puts a (Type, Iface) pair in the
// impl manifest, and a reimplementation that
// returned the right verdict and skipped the recording would type check clean
// with a manifest entry missing.
//
// What that costs is NOT established. A mutant of exactly that shape — a
// hand-written `c.unify(iface, arg, nil) == nil` inside `deepPartialMatches`,
// no recording — left the manifest entry out (the guard in
// partial_iface_field_test.go catches it), and no run of
// tests/11-interfaces-and-impls/interface_field_update_test.nomi has
// been shown to fail under it. So the missing entry is unobserved, not shown
// to be harmless. Do not cite this comment as evidence that a dropped recording
// breaks dispatch — cite it as the reason to share the rule rather than to
// re-derive when each caller's manifest obligations are load-bearing.
//
// Callers pass the argument or patch position so the manifest keeps the
// demand site.
func (c *checker) ifaceParamAdmits(argTy Type, iface *InterfaceType, recPos Pos, recKind RecordingKind) bool {
	// Universal struct interface: a `Struct`-typed (existential) parameter
	// accepts any struct — named or anonymous — with no impl-registry entry
	// to unify against. Existential args (interface / type param / type var)
	// defer to true; a concrete non-struct is rejected.
	if iface.Name == "Struct" {
		switch resolveTypeVar(argTy).(type) {
		case *InterfaceType, *TypeParam_, *TypeVar:
			return true
		}
		return isStructShaped(argTy)
	}
	if c.unify(iface, argTy, nil) != nil {
		return false
	}
	// Record (concrete, iface) when a concrete arg flows into an
	// interface param. Existential args (interface, type param, type
	// var) are skipped — concrete type isn't statically known.
	c.recordConformanceIfConcrete(argTy, iface.Name, recPos, recKind)
	return true
}

// containsTypeVar walks a Type and reports whether any *TypeVar appears
// (after following Resolved chains). Used to decide when argMatchesParam
// should attempt unification rather than relying on TypesEqual.
func containsTypeVar(t Type) bool {
	if t == nil {
		return false
	}
	switch tt := resolveTypeVar(t).(type) {
	case *TypeVar:
		return true
	case *ListType:
		return containsTypeVar(tt.Elem)
	case *MapType:
		return containsTypeVar(tt.Key) || containsTypeVar(tt.Val)
	case *TupleType:
		for _, e := range tt.Elems {
			if containsTypeVar(e) {
				return true
			}
		}
		return false
	case *AnonStructType:
		for _, f := range tt.Fields {
			if containsTypeVar(f.Type) {
				return true
			}
		}
		return false
	case *FuncType:
		for _, p := range tt.Params {
			if containsTypeVar(p) {
				return true
			}
		}
		return containsTypeVar(tt.Return)
	case *EnumType:
		for _, a := range tt.TypeArgs {
			if containsTypeVar(a) {
				return true
			}
		}
		return false
	case *StructType:
		for _, a := range tt.TypeArgs {
			if containsTypeVar(a) {
				return true
			}
		}
		return false
	case *InterfaceType:
		for _, a := range tt.TypeArgs {
			if containsTypeVar(a) {
				return true
			}
		}
		return false
	case *DistinctType:
		// Generic opaque externs (Channel<T>, Task<T>) and generic distinct
		// types carry params in TypeArgs; primitive wrappers carry an Inner.
		for _, a := range tt.TypeArgs {
			if containsTypeVar(a) {
				return true
			}
		}
		return containsTypeVar(tt.Inner)
	case *PartialType:
		return containsTypeVar(tt.Inner)
	}
	return false
}

// shouldReportUnifyError decides whether a failed unification in a generic
// call should surface as a user-visible type error. We report when the
// parameter's shape is statically fixed by the signature — interface-typed
// parameters with fixed type-arg shapes (e.g. Iter<(K, V)>) and
// anon-struct parameters whose fields commit to a constructed shape (e.g.
// {tail: List<T>}). Other unification failures — partially-solved lambda
// bodies, bare-TypeParam parameters, etc. — are left for downstream checks
// to surface so we don't regress historically permissive inference paths.
func shouldReportUnifyError(paramTy, argTy Type) bool {
	switch p := paramTy.(type) {
	case *InterfaceType:
		if len(p.TypeArgs) == 0 {
			return false
		}
		for _, a := range p.TypeArgs {
			switch a.(type) {
			case *TupleType, *PrimitiveType, *ListType, *MapType, *StructType, *EnumType, *DistinctType:
				return true
			}
		}
		return false
	case *AnonStructType:
		// *AnonStructType in the inner switch covers nested anon-struct
		// fields (e.g. {pair: {a: Int, b: Int}}); the InterfaceType branch
		// omits it because anon-struct interface type-args are unused today.
		for _, f := range p.Fields {
			switch f.Type.(type) {
			case *TupleType, *PrimitiveType, *ListType, *MapType, *StructType, *EnumType, *DistinctType, *AnonStructType:
				return true
			}
		}
		return false
	}
	return false
}

// isSolvedParam reports whether a parameter type, after substituting the
// type params solved by earlier arguments, has become fully concrete (no
// unsolved type param and no inference var). When it has, a later argument
// that fails to unify is a genuine multi-occurrence conflict — the type var
// was pinned by an earlier argument — and must be enforced, rather than left
// to the permissive `shouldReportUnifyError` fallback.
func isSolvedParam(substituted Type) bool {
	return substituted != nil && !ContainsTypeParam(substituted) && !containsTypeVar(substituted)
}

// argSatisfiesConcreteParam reports whether argTy is an acceptable argument for
// `concreteParam`, a parameter whose type var was solved by an earlier argument.
// It admits exactly the relations the runtime honors:
//   - structural / embeds / bool-variant / interface-impl compatibility (unify);
//   - a deep-partial patch against a `Partial<T>` parameter (Struct.update).
//
// Anything else — `g(1, "two")`, `Map.get(Map<Int, _>, "wrong")` — is rejected.
// `line`/`col` position any diagnostic the `Partial<T>` opacity gate emits.
func (c *checker) argSatisfiesConcreteParam(argTy, concreteParam Type, line, col int) bool {
	if TypeAssignable(concreteParam, argTy) {
		return true
	}
	if pt, ok := concreteParam.(*PartialType); ok {
		return c.partialPatchOK(argTy, pt.Inner, line, col)
	}
	// A callback parameter declared to return Unit — e.g. Iter.each's
	// `(T) -> ()` — discards the callback's return value, so a lambda returning
	// any type is acceptable (a side-effect callback). Match the parameters but
	// ignore the return by unifying against a Unit-returning rewrite.
	if pf, ok := concreteParam.(*FuncType); ok && isUnitLike(pf.Return) {
		if af, ok := argTy.(*FuncType); ok {
			rewritten := &FuncType{Params: af.Params, Return: TypeUnit, DefaultCount: af.DefaultCount}
			return c.unifyInto(concreteParam, rewritten, nil) == nil
		}
	}
	if _, _, down := embedsDowncast(concreteParam, argTy); down {
		return false
	}
	return c.unify(concreteParam, argTy, nil) == nil
}

// reportGenericArgMismatch applies the shared generic-call argument-check policy
// after a failed unify of `argTy` against `param` (the original, unsubstituted
// parameter) with bindings `subs`. When an earlier argument has solved param's
// type var (so the substituted param is concrete), it enforces this argument
// against that concrete type; otherwise it falls back to the permissive
// shape-only check. `argIndex` is 0-based. Shared by the direct-call
// (checkGenericCall) and pipe (checkPipe) paths so both reject the same
// multi-occurrence conflicts.
func (c *checker) reportGenericArgMismatch(param, argTy Type, subs map[*TypeParam_]Type, arg ast.Node, label string, line, col int) {
	if iface, ok := param.(*InterfaceType); ok {
		if !c.typeImplementsInterface(argTy, iface.Name, c.recPos(line, col), RecordingKindInterfaceTypedParam) {
			c.addError(line, col, fmt.Sprintf(
				"%s: %s does not implement %s", label, argTy, iface.Name))
			return
		}
	}
	substituted := Substitute(param, subs)
	if isSolvedParam(substituted) {
		if !c.argSatisfiesConcreteParam(argTy, substituted, line, col) {
			hint := defaultedFuncValueHint(substituted, argTy, arg)
			if hint == "" {
				hint = c.functionJoinHint(param, substituted, argTy)
			}
			c.report(TypeError{Line: line, Col: col, Message: c.typef(
				"%s: expected %s, got %s", label, substituted, argTy)}.Spanning(arg).WithHint(hint).WithHint(c.wholeResultHint(substituted, argTy, arg)).WithHint(embedsDowncastHint(substituted, argTy)))
		}
		return
	}
	// A function value with the wrong number of parameters never unifies,
	// whatever the type variables solve to. The permissive fallback below
	// let `Maybe.map(m, parse)` through for a `parse` with a defaulted second
	// parameter, and the program was BLOCKED at run time.
	if funcArityDiffers(substituted, argTy) {
		c.report(TypeError{Line: line, Col: col, Message: c.typef(
			"%s: expected %s, got %s", label, substituted, argTy)}.Spanning(arg).WithHint(defaultedFuncValueHint(substituted, argTy, arg)).WithHint(embedsDowncastHint(substituted, argTy)))
		return
	}
	// A function value is never a non-function, and the reverse: no
	// substitution turns `(String) -> Maybe<Int>` into a `Maybe<T>`. The
	// permissive fallback below let `Maybe.with_default(f, 0)` through, and
	// the program was BLOCKED at run time (func_value_shape.go).
	if funcShapeDiffers(substituted, argTy) {
		c.report(TypeError{Line: line, Col: col, Message: c.typef(
			"%s: expected %s, got %s", label, substituted, argTy)}.Spanning(arg))
		return
	}
	// The same holds for any two different type constructors: no
	// substitution makes a `Set<T>` of Unit or a `List<T>` of a String. The
	// fallback below let `Set.size({})` through, and the program could not
	// run.
	if headDiffers(substituted, argTy) {
		c.report(TypeError{Line: line, Col: col, Message: c.typef(
			"%s: expected %s, got %s", label, substituted, argTy)}.Spanning(arg))
		return
	}
	if shouldReportUnifyError(param, argTy) {
		c.addMismatch(line, col, substituted, argTy, c.typef("%s: expected %s, got %s", label, substituted, argTy))
	}
}

// functionJoinHint explains a rejected argument whose function type would be
// assignable to the one an earlier argument bound the type parameter to:
// `pick(c, float_str, shown)` with `fn pick<T>(c: Bool, a: T, b: T): T`. A type
// parameter holds one function type, and a second function joins it only when
// their types are the same.
func (c *checker) functionJoinHint(param, solved, argTy Type) string {
	tp, isParam := param.(*TypeParam_)
	if !isParam {
		return ""
	}
	if _, isFunc := resolveTypeVar(solved).(*FuncType); !isFunc {
		return ""
	}
	if _, isFunc := resolveTypeVar(argTy).(*FuncType); !isFunc || c.unifyInto(solved, argTy, nil) != nil {
		return ""
	}
	return c.typef("%s is already %s from an earlier argument, and a type parameter joins two function types only when they are the same; pass a lambda of that type instead", tp.Name_, solved)
}

// positionalArgLabel names the argument at 0-based call position i in a
// mismatch message; namedArgLabel names a named argument by its parameter.
func positionalArgLabel(i int) string { return fmt.Sprintf("argument %d", i+1) }

func namedArgLabel(name string) string { return "argument '" + name + "'" }

// reportUnknownNamedArg reports a named argument whose name is none of the
// callee's parameters. paramNames nil means the callee's names are not known
// (a function value), and nothing is reported.
func (c *checker) reportUnknownNamedArg(na *ast.NamedArg, paramNames []string) {
	if paramNames == nil {
		return
	}
	c.report(TypeError{Line: na.Line, Col: na.Col, Message: "no parameter named '" + na.Name + "'"}.WithHint(didYouMean(na.Name, paramNames)))
}

// namedArgSlot is the parameter slot a named argument names, or -1.
func namedArgSlot(name string, paramNames []string) int {
	for j, pn := range paramNames {
		if pn == name {
			return j
		}
	}
	return -1
}

// callArgsNeedExpectedType reports whether any argument has a part whose type
// only the call's expected type can supply: a dot-leading variant (`.Quit`,
// `.Guess(x)`), which resolves against the expected enum, or an empty list,
// vector, set or map literal, whose element type nothing in it fixes. Either
// may sit directly in the argument or inside a list, vector, set, tuple or map
// literal or a nested call's arguments (`[(.North, .Cave)]`, `Some(.North)`).
// Those arguments cannot be checked against an un-instantiated type param, so
// the expected type has to be seeded into the substitution first. NamedArg
// wrappers are unwrapped. Lambda bodies and blocks are not searched: what
// they hold is typed by the lambda's own return or a binding.
func callArgsNeedExpectedType(args []ast.Node) bool {
	for _, a := range args {
		if needsExpectedType(a) {
			return true
		}
	}
	return false
}

func needsExpectedType(node ast.Node) bool {
	switch v := node.(type) {
	case *ast.DotVariant:
		return true
	case *ast.TypeIdent:
		// A constructor named as a value (`Ok`, `Err`): a type parameter
		// its argument does not fix is fixed only by the call's result,
		// `E` in `rs: List<Result<Int, String>> = Iter.map(xs, Ok) |> ...`.
		return true
	case *ast.FieldAccess:
		// `Result.Ok`, `Tree.Node`: the qualified spelling of the same.
		_, typeOwner := v.Object.(*ast.TypeIdent)
		return typeOwner && v.Field != nil && v.Field.Name != "" && v.Field.Name[0] >= 'A' && v.Field.Name[0] <= 'Z'
	case *ast.NamedArg:
		return needsExpectedType(v.Value)
	case *ast.GroupedExpr:
		return needsExpectedType(v.Expr)
	case *ast.Lambda:
		// A parameter with no annotation or default takes its type from
		// the function type the lambda is checked against; where only the
		// call's result fixes that type (`f: (Int) -> Int = ident(|x| x + 1)`),
		// the lambda has none without the seed.
		for _, p := range v.Params {
			if p.Destructure == nil && p.TypeAnnotation == nil && p.Default == nil {
				return true
			}
		}
		return false
	case *ast.Call:
		if _, ok := v.Func.(*ast.DotVariant); ok {
			return true
		}
		return callArgsNeedExpectedType(v.Args)
	case *ast.ListLit:
		return v.TypeName == nil && (len(v.Items) == 0 || callArgsNeedExpectedType(v.Items))
	case *ast.VectorLit:
		return len(v.Items) == 0 || callArgsNeedExpectedType(v.Items)
	case *ast.SetLit:
		return len(v.Items) == 0 || callArgsNeedExpectedType(v.Items)
	case *ast.TupleLit:
		return callArgsNeedExpectedType(v.Items)
	case *ast.StructLit:
		// A brace literal builds the nominal struct its slot expects
		// (`Some({street: …, city: …})` under `Maybe<Address>`), and only
		// the call's expected type can say which.
		return v.TypeName == nil && v.Spread == nil
	case *ast.ListSpreadLit:
		return callArgsNeedExpectedType(v.Heads) || (v.TailSpread != nil && needsExpectedType(v.TailSpread))
	case *ast.MapLit:
		if v.TypeName != nil {
			return false
		}
		if len(v.Entries) == 0 {
			return true
		}
		for _, e := range v.Entries {
			if needsExpectedType(e.Key) || needsExpectedType(e.Value) {
				return true
			}
		}
	}
	return false
}

// checkGenericCall handles calls to generic functions by inferring type params.
// When skipReturnInfer is true, the post-argument unification of the return
// type against the enclosing function's return type is skipped — used for
// interface-method calls that have already been specialised from argument
// type args, so any remaining TypeParam_s belong to the caller's scope and
// should stay nominal.
func (c *checker) checkGenericCall(n *ast.Call, ft *FuncType, skipReturnInfer bool, expected Type) Type {
	errMark := len(c.errors)
	// Variant tuple-payload literal-attach for generic variant
	// constructors. Mirrors the non-generic path in checkCall — accept
	// the flat-call shape `Variant(a, b, ...)` when the variant's 1-arg
	// payload is a tuple of arity N >= 2 and the call supplied exactly
	// N positional args. Suppresses the generic argument-count diagnostic
	// below so the per-arg type-check is the sole source of feedback.
	suppressArgCount := c.checkVariantTuplePayloadFlatCall(n, ft)

	// Check argument count.
	minArgs := len(ft.Params) - ft.DefaultCount
	if !suppressArgCount && (len(n.Args) < minArgs || len(n.Args) > len(ft.Params)) {
		if ft.DefaultCount > 0 {
			c.addError(n.Line, 1, fmt.Sprintf(
				"expected %d to %d arguments, got %d", minArgs, len(ft.Params), len(n.Args)))
		} else {
			c.addError(n.Line, 1, fmt.Sprintf(
				"expected %d arguments, got %d", len(ft.Params), len(n.Args)))
		}
	}

	subs := map[*TypeParam_]Type{}

	// Turbofish: bind the callee's type params from explicit type args before
	// solving from arguments. Params are taken in first-appearance order across
	// the signature (params then return), which equals declaration order for
	// all but exotic out-of-order signatures (`fn f<B, A>(x: A): B`).
	if n.TypeArgs != nil {
		tps := collectOrderedTypeParams(ft)
		if len(n.TypeArgs) != len(tps) {
			c.addError(n.Line, 1, fmt.Sprintf(
				"expected %d type argument(s), got %d", len(tps), len(n.TypeArgs)))
		}
		// A type argument that names no type is the error an annotation
		// naming it gives (`unknown type "Nope"`); ignoring it let
		// `ident<Nope>(1)` run as `ident(1)`.
		for i, ta := range n.TypeArgs {
			resolved, err := ResolveTypeExpr(ta, c.reg, c.fnTypeParams, c.fa.References)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					c.report(te)
				}
				continue
			}
			if resolved != nil && len(n.TypeArgs) == len(tps) {
				subs[tps[i]] = resolved
			}
		}
	}

	// Bidirectional seed: when the result type is generic and the call sits in a
	// position with a known concrete expected type (return tail, annotated
	// binding, or an argument slot — supplied via `expected`, nil otherwise),
	// solve the result params from it NOW, before checking arguments, so a
	// dot-leading-variant argument sees the concrete payload type rather than a
	// bare type param. `Result.Ok(.Quit)` expected to be `Result<Input, String>`
	// then sees `.Quit`'s expected type as `Input`, and `Iter.to_map([(.North,
	// .Cave)])` expected to be `Map<Direction, Place>` checks its list against
	// `Iter<(Direction, Place)>`.
	//
	// Scoped deliberately tight:
	//   - `expected`, not c.returnTy — c.returnTy is the ambient enclosing return,
	//     which for a statement-position call would unify params against an
	//     unrelated type (e.g. `Unit`) and corrupt inference; `expected` is non-nil
	//     only where the result genuinely flows to a known type.
	//   - concrete `expected` only (`!ContainsTypeParam`) — seeding from an
	//     expected type that itself holds caller-scope params (a generic body
	//     returning `Maybe<U>`) mixes param scopes and mis-infers.
	//   - only when an argument holds a dot variant or an empty literal
	//     (callArgsNeedExpectedType) — the argument shapes that cannot be checked
	//     against an un-instantiated param and truly need the seed. An empty
	//     literal left to post-hoc unification types the call with inference
	//     variables, so no instantiation is recorded for the IR builder and the
	//     program BLOCKs. Ordinary arguments unify post-hoc, so seeding them would
	//     only perturb error reporting (surfacing an element mismatch before the
	//     binding-level one) without fixing anything. Direct-call free params
	//     (e.g. E in `Result.Ok(n)`) are still solved by the post-argument
	//     back-stop below.
	//   - `!skipReturnInfer` — shared with that back-stop (interface-method
	//     specialisation leaves caller-scope params in the return).
	//   - tentative — the seed is unified into a copy and kept only when the
	//     whole result type unifies with `expected`. An expected type the result
	//     does not match (an interface slot, a mismatch the caller reports)
	//     leaves the substitution as the arguments alone would build it. An
	//     expected type holding an unsolved inference variable is skipped too:
	//     unify binds the variable itself, which a discarded trial cannot undo.
	//   - a partial application's result is a function of its open slots,
	//     so that function, not the callee's result, meets `expected`:
	//     `f: (Int) -> Int = pick(True, _, 4)` seeded T as `(Int) -> Int`
	//     and then rejected the 4.
	resultTy := ft.Return
	if callHasPlaceholder(n.Args) {
		resultTy = c.partialCallShape(n, ft)
	}
	if concreteExpected(expected) && !skipReturnInfer &&
		ContainsTypeParam(resultTy) && callArgsNeedExpectedType(n.Args) {
		trial := make(map[*TypeParam_]Type, len(subs))
		for k, v := range subs {
			trial[k] = v
		}
		if c.unifyInto(expected, resultTy, trial) == nil {
			subs = trial
		}
	}

	// Process arguments left to right. After each argument, apply partial
	// substitution so solved type params flow into subsequent arguments
	// (enabling lambda parameter inference from earlier args).
	//
	// Arg → param-slot mapping goes through computePositionalSlots — the
	// same routing the non-generic path uses — so trailing-lambda routing
	// applies to generic callees too (a final lambda skips a defaulted
	// middle param into the last slot: `Iter.sort_by(users, |u| u.name)`).
	// NamedArg entries come back as -1 and route by name to the parameter
	// they name, as in the non-generic path and the pipe path: checking one
	// against the parameter at its written position rejected
	// `spread(1, twice: True)` as "argument 2: expected List<Int>, got Bool".
	defParams := c.callDefParams(n.Func)
	posSlots := computePositionalSlots(n.Args, ft.Params, defParams)
	for _, na := range positionalNamedConflicts(n.Args, posSlots, c.callDefParams(n.Func), 0) {
		c.addError(na.Line, 1, fmt.Sprintf("parameter '%s' already has a value", na.Name))
	}
	if !suppressArgCount &&
		len(n.Args) >= minArgs && len(n.Args) <= len(ft.Params) {
		c.checkRequiredSlotsFilled(n, n.Args, posSlots, -1, ft.Params)
	}
	// instArgs pairs each parameter type with its argument's type, for the
	// polymorphic-recursion check (recordInstantiation).
	var instArgs [][2]Type
	var openSlots []int
	slotArgs := map[int]ast.Node{}
	for i, arg := range n.Args {
		slot := i
		label := positionalArgLabel(i)
		errLine, errCol := n.Line, 1
		if na, ok := arg.(*ast.NamedArg); ok {
			slot = namedArgSlot(na.Name, defParams)
			label = namedArgLabel(na.Name)
			errLine, errCol = nodeLineCol(na.Value)
			if slot < 0 {
				c.reportUnknownNamedArg(na, defParams)
				c.checkNode(na.Value)
				continue
			}
		} else if i < len(posSlots) && posSlots[i] >= 0 {
			slot = posSlots[i]
		}
		if slot >= len(ft.Params) {
			c.checkNode(arg)
			continue
		}

		// A `_`, positional or named, leaves the slot open: the call is a
		// partial application, typed below once the written arguments have
		// solved what they can.
		if isPlaceholderArg(arg) {
			openSlots = append(openSlots, slot)
			continue
		}

		// Substitute already-solved params into the expected type.
		expectedParamTy := Substitute(ft.Params[slot], subs)

		c.registerArgSlotHint(arg, n.Func, slot, expectedParamTy)

		argTy := c.checkNodeExpecting(arg, expectedParamTy)
		if argTy == nil {
			continue
		}
		slotArgs[slot] = arg

		// Coerce Map<K,V> to List<(K,V)> when param expects List<T>,
		// matching the runtime's iterNext which yields (key, value) tuples.
		argTy = coerceMapToList(argTy, ft.Params[slot])

		// Coerce Range to List<Int> when param expects List<T>,
		// matching the runtime's iterNext which yields Int values.
		argTy = coerceRangeToList(argTy, ft.Params[slot])

		instArgs = append(instArgs, [2]Type{ft.Params[slot], argTy})

		// Unify argument type against the (original) parameter type to solve more params.
		// When the parameter shape constrains the argument (e.g. Iter<(K, V)>
		// vs a List of non-tuple values), surface the mismatch as a type error.
		if err := c.unifyInto(ft.Params[slot], argTy, subs); err != nil {
			c.reportGenericArgMismatch(ft.Params[slot], argTy, subs, arg, label, errLine, errCol)
		}

		// Conformance recording for concrete-arg-into-interface-param. The
		// non-generic argMatchesParam path records this at line ~2858 when
		// paramTy is *InterfaceType; the generic path bypasses argMatchesParam
		// and goes straight to unify, so the recording has to happen here too.
		// Without it, calls like `Iter.reduce(s: String, ...)` where reduce's
		// first param is `Iter<T>` leave (Iter, String) out of the
		// manifest, and DetectMissingImpls never checks the pair.
		argLine, argCol := nodeLineCol(arg)
		c.recordInterfaceConformanceFromParam(argTy, ft.Params[slot], c.recPos(argLine, argCol), RecordingKindInterfaceTypedParam)
	}

	// Interface-method dispatch (`Iface.method(arg, ...)`): record
	// (concrete, iface) only for the interface's `self` TypeParam_ — the
	// receiver-position binding is the actual conformance fact A2's other
	// recorders miss for this call shape. Other TypeParamDefs (e.g. T in
	// `Pusher<T> { fn push(c: self, v: T): self }`) are unrelated to
	// conformance: T binding to Int says nothing about Int implementing
	// Pusher. Existential self bindings (interface, type param, type var)
	// are skipped.
	//
	// Use recordBoundConformance (not recordConformanceIfConcrete) so the
	// recursion into TypeArgs picks up nested concrete types. A `@derive
	// Debug pub type Kvs Map<String, Int>` synth body calls
	// `Debug.inspect(inner)` where inner: Map<String, Int>; the Map's
	// own Debug body iterates and calls `Debug.inspect` on each key
	// and value, so (Debug, String) and (Debug, Int) need manifest
	// entries. Without the recursion the bridge force-loads std/maps but
	// leaves dispatch for the nested types unset, and the runtime
	// fails with "Debug.inspect: no implementation for type 'String'".
	structConformanceErrored := false
	// returnOnlySelf is the interface whose `self` no argument solved: a
	// method that declares self only in its RETURN (`FromJson.from_json(json:
	// Json): Result<self, Json.ShapeError>`) called without a type argument.
	// Its self is solved from the result below, and recorded there.
	var returnOnlySelf *InterfaceType
	if fa, ok := n.Func.(*ast.FieldAccess); ok {
		if iface, ok := interfaceObjectType(c, fa.Object, nil); ok && iface.SelfParam != nil {
			if concrete, ok := subs[iface.SelfParam]; ok {
				structConformanceErrored = c.recordInterfaceSelf(n, fa, iface, concrete)
			} else {
				returnOnlySelf = iface
			}
		} else {
			// The TYPE-qualified spelling of the same call
			// (`Vector.compare(...)` for `Comparable.compare(...)`)
			// resolves through the type-method symbol, where there is
			// no SelfParam to read, and recorded nothing at all. See
			// impl_call_demand.go.
			c.recordTypeQualifiedImplDemand(n, fa, ft, subs)
		}
	}

	retTy := Substitute(ft.Return, subs)

	// Nullary calls to a generic function (e.g. `Map.empty()`) have no args
	// to drive type-param solving. Instantiate any callee TypeParam_s left
	// in retTy as fresh inference vars so downstream uses can resolve them
	// — `Map.empty()` ends up as `Map<?α, ?β>` instead of `Map<K, V>`.
	// Caller-scope TypeParam_s (c.fnTypeParams) are preserved.
	//
	// Run this BEFORE the c.returnTy fallback below: that fallback would
	// otherwise resolve callee TypeParam_s against the enclosing function's
	// return type, making them look like caller-scope params we'd want to
	// preserve. For nullary calls we'd rather get fresh holes the body's
	// usage can resolve directly.
	if len(n.Args) == 0 {
		retTy = instantiateUnboundCalleeParams(retTy, c.fnTypeParams, c)
	}

	// A return-only self is solved by where the result GOES: the expected
	// type at this position (an annotated binding, `try` under one, an
	// argument slot) when there is one. The enclosing function's return type
	// below is the ambient fallback and says nothing about a call that is not
	// in tail position: `notes: List<Note> = try FromJson.from_json(json)`
	// inside a function returning `Result<Int, E>` solved self as Int.
	if returnOnlySelf != nil && expected != nil && !ContainsTypeParam(expected) {
		trial := make(map[*TypeParam_]Type, len(subs))
		for k, v := range subs {
			trial[k] = v
		}
		if c.unifyInto(expected, ft.Return, trial) == nil {
			subs = trial
			retTy = Substitute(ft.Return, subs)
		}
	}

	// A type parameter named only in a `where` bound's arguments and the
	// result (`Out` in `fn plus<L, R, Out>(lhs: L, rhs: R): Out where L:
	// Add<R, Out>`) is the one the solved subject's impl of the bound
	// gives, as `lhs + rhs` itself is typed by that impl. Solved before the
	// fallback below, which would otherwise read it off the enclosing
	// function's return type wherever the call sits.
	if ContainsTypeParam(retTy) && c.solveFromWhereImpls(ft, subs) {
		retTy = Substitute(ft.Return, subs)
	}

	// The return-only self, now solved, gets the records an argument-solved
	// self gets above: its conformance demand and the dispatch it selects,
	// which is what tells the IR builder which impl the call names.
	if returnOnlySelf != nil {
		if concrete, ok := subs[returnOnlySelf.SelfParam]; ok && !ContainsTypeParam(concrete) {
			if fa, ok := n.Func.(*ast.FieldAccess); ok {
				structConformanceErrored = c.recordInterfaceSelf(n, fa, returnOnlySelf, concrete)
				c.attachReturnSelf(fa, concrete)
			}
		}
	}

	// Any remaining callee-scope TypeParam_s — leftovers from args that
	// didn't constrain them and a return-type fallback that didn't bind —
	// become fresh TypeVars. This lets a nested generic call's result
	// (`Ok(99)` returning `Result<Int, E>`) flow into an outer monomorphic
	// param check (`unwrap_or(Some(Ok(99)))` expects
	// `Maybe<Result<Int, String>>`): the surviving E gets a TypeVar that
	// argMatchesParam's unify can bind to String. Caller-scope TypeParam_s
	// (c.fnTypeParams) are preserved as nominal so a generic body's own
	// params don't get accidentally fresh-vared.
	if ContainsTypeParam(retTy) {
		retTy = instantiateUnboundCalleeParams(retTy, c.fnTypeParams, c)
	}

	// Interface-bound enforcement: each TypeParam_ carries zero or more
	// Bounds. For each `T → Concrete` in subs, verify Concrete implements
	// every interface in T.Bounds. Bounds are conjunctive (AND); a
	// missing impl for any one fails the call (with that interface named
	// in the error so the user knows which interface they're missing).
	for tp, concrete := range subs {
		for _, bound := range tp.Bounds {
			if !typeImplementsInterface(c, concrete, bound, c.recPos(n.Line, n.Col), RecordingKindGenericBoundCheck) {
				c.boundError(n.Line, callCol(n), concrete, bound, tp.Name_, n.Func)
				continue
			}
			c.recordBoundConformance(concrete, bound.Name, c.recPos(n.Line, n.Col), RecordingKindGenericBoundCheck)
		}
	}

	// Method-level `where`-clause enforcement (constrained interface functions).
	c.recordWhereBoundDemands(ft, subs, n.Line, n.Col, callCol(n))

	// A solved `Partial<T>` parameter must resolve to a struct (Struct.update).
	// Skipped when a `Struct.method` conformance error already fired for this
	// call — the conformance message subsumes the redundant Partial<self> one.
	if !structConformanceErrored {
		c.checkPartialParamStructs(ft, subs, n.Line, 1)
	}

	// Store instantiated function type on the call-site reference symbol,
	// but only when all type params were fully resolved. Partial resolution
	// (e.g. from a type-mismatched argument) produces misleading hover info.
	//
	// "Fully resolved" excludes a CALLER-SCOPE type parameter, which is an
	// answer and not a hole. Thirty-nine lines above, this function has
	// already drawn exactly that line for the other consumer of the same
	// distinction: callee-scope leftovers become fresh TypeVars while
	// "caller-scope TypeParam_s (c.fnTypeParams) are preserved as nominal so
	// a generic body's own params don't get accidentally fresh-vared". A bare
	// ContainsTypeParam cannot see that difference, so `Ok(x)` inside
	// `fn ok<T>(x: T): Result<T, String>` — whose instantiation IS determined,
	// as `Result<T, String>` with the enclosing declaration's own T — recorded
	// nothing, while the identical call in a non-generic twin recorded
	// `(Int) -> Result<Int, String>`.
	//
	// MEASURED consequence of the gap, which is why this is a correctness fix
	// and not hover polish. With no CallType, irbuild's preludeArgs falls back to
	// the CONSTRUCTOR'S OWN DECLARED return — `Some: (T) -> Maybe<T>`,
	// `Ok: (T) -> Result<T, E>` — and looks those names up in the IR builder's
	// substitution frame BY SPELLING. That is an unrelated declaration's type
	// parameters resolved against the enclosing function's frame:
	// `fn wrap<T>(x: T): Maybe<T>` lowered only because both spell it `T`, and
	// `fn wrap<A>(x: A): Maybe<A>` refused. Recording the instantiation here
	// removes the fallback's reason to exist for this population, and what it
	// records carries the ENCLOSING function's parameters, which the frame is
	// keyed on.
	//
	// The original guard's population is untouched: an UNSOLVED inference
	// variable and a CALLEE-scope parameter both still block the record, so a
	// type-mismatched argument produces no misleading hover exactly as before.
	if len(subs) > 0 {
		instParams := make([]Type, len(ft.Params))
		for i, p := range ft.Params {
			instParams[i] = Substitute(p, subs)
		}
		instFT := &FuncType{Params: instParams, Return: retTy}
		fullyResolved := !c.containsForeignTypeParam(retTy)
		for _, p := range instParams {
			if c.containsForeignTypeParam(p) {
				fullyResolved = false
				break
			}
		}
		if fullyResolved {
			c.attachCallType(n.Func, instFT)
		}
	}
	c.recordInstantiation(n, subs, instArgs)

	// Partial application: a function of the open slots, in the order the
	// placeholders are written, at the types the written arguments solved,
	// as checkCall's non-generic path builds it. Returning the callee's result
	// here typed `Console.write_line(c, _)` as Unit.
	if len(openSlots) > 0 {
		calleeDefaults := c.callDefParamHasDefault(n.Func)
		params := make([]Type, len(openSlots))
		defaults := 0
		for i, slot := range openSlots {
			params[i] = Substitute(ft.Params[slot], subs)
			if slot < len(calleeDefaults) && calleeDefaults[slot] {
				defaults++
			}
		}
		// The open slots and the result share the callee parameters nothing
		// solved: `Iter.map(xs, _)` is `((Int) -> ?1) -> Iter<?1>`.
		pf := &FuncType{Params: params, Return: Substitute(ft.Return, subs), DefaultCount: defaults}
		return instantiateUnboundCalleeParams(pf, c.fnTypeParams, c)
	}

	c.noteUndeterminedCall(n.Func, n, ft, subs, slotArgs, errMark)
	return retTy
}

// partialCallShape is the function type a partial application of generic
// ft builds before any argument is checked: the parameters at its `_` slots,
// in the order they are written, returning ft's result. checkGenericCall
// types the partial the same way once its arguments have solved what they
// can.
func (c *checker) partialCallShape(n *ast.Call, ft *FuncType) Type {
	defParams := c.callDefParams(n.Func)
	posSlots := computePositionalSlots(n.Args, ft.Params, defParams)
	var params []Type
	for i, arg := range n.Args {
		if !isPlaceholderArg(arg) {
			continue
		}
		slot := i
		if na, ok := arg.(*ast.NamedArg); ok {
			slot = namedArgSlot(na.Name, defParams)
		} else if i < len(posSlots) && posSlots[i] >= 0 {
			slot = posSlots[i]
		}
		if slot < 0 || slot >= len(ft.Params) {
			return ft.Return
		}
		params = append(params, ft.Params[slot])
	}
	return &FuncType{Params: params, Return: ft.Return}
}

// isPlaceholderArg reports a `_` argument, positional or named (`port: _`).
func isPlaceholderArg(arg ast.Node) bool {
	if na, ok := arg.(*ast.NamedArg); ok {
		arg = na.Value
	}
	_, ok := arg.(*ast.Placeholder)
	return ok
}

// recordInterfaceSelf records what an interface-qualified call's solved `self`
// implies: the struct-conformance check, the (concrete, interface)
// conformance demand, the bound it places on a type parameter, and the impl
// the call dispatches to. It reports whether struct conformance errored.
func (c *checker) recordInterfaceSelf(n *ast.Call, fa *ast.FieldAccess, iface *InterfaceType, concrete Type) bool {
	errored := c.enforceStructConformance(iface.Name, concrete, fa.Field.Name, n.Line, 1)
	c.recordBoundConformance(concrete, iface.Name, c.recPos(n.Line, n.Col), RecordingKindCallSite)
	c.recordInferredBoundOnTypeParam(concrete, iface.Name)
	c.attachDispatchImpl(fa, iface.Name, concrete)
	return errored
}

// attachReturnSelf records a return-only self's solved type on a per-site copy
// of the method reference. See Symbol.ReturnSelf.
func (c *checker) attachReturnSelf(fa *ast.FieldAccess, concrete Type) {
	if fa.Field == nil {
		return
	}
	pos := Pos{Line: fa.Field.Line, Col: fa.Field.Col}
	existing := c.fa.References[pos]
	if existing == nil {
		return
	}
	ref := *existing
	ref.ReturnSelf = concrete
	c.fa.References[pos] = &ref
}

// containsForeignTypeParam is ContainsTypeParam with the ONE type parameter that
// is an answer rather than a hole excluded: the enclosing declaration's own.
//
// The two are the same question asked from two positions. ContainsTypeParam is
// a pure function of a type and cannot know which declaration is being checked,
// so it is right for every caller that has no scope. This one has `c`, and
// `c.fnTypeParams` is the map checkFunc built from the signature currently being
// checked — the same map `instantiateUnboundCalleeParams` is handed to decide
// which surviving parameters must NOT be turned into fresh inference variables.
// So the two uses agree on scope by construction rather than by a second copy of
// the rule.
//
// IDENTITY, NOT SPELLING. The membership test is pointer equality against the
// entry under the same name, which is `recordInferredBoundOnTypeParam`'s own
// rule one function below ("the identity check is pointer equality against
// c.fnTypeParams"). A name test would readmit exactly the confusion this fixes:
// a callee's `T` and an enclosing `T` are two declarations' parameters and share
// nothing but a letter.
func (c *checker) containsForeignTypeParam(t Type) bool {
	return containsTypeParamExcept(t, c.fnTypeParams)
}

// containsTypeParamExcept mirrors ContainsTypeParam's traversal with an
// exemption set. It lives beside its caller rather than in types.go because the
// exemption is a CHECKER-scope notion and types.go has no scope.
func containsTypeParamExcept(t Type, own map[string]*TypeParam_) bool {
	if t == nil {
		return false
	}
	if tp, isParam := t.(*TypeParam_); isParam {
		return own[tp.Name_] != tp
	}
	// Every other shape is ContainsTypeParam's own recursion, reached through
	// it so the traversal has one encoding. A type that holds no parameter at
	// all is answered by ContainsTypeParam directly; one that does is taken
	// apart here.
	if !ContainsTypeParam(t) {
		return false
	}
	switch t := t.(type) {
	case *ListType:
		return containsTypeParamExcept(t.Elem, own)
	case *MapType:
		return containsTypeParamExcept(t.Key, own) || containsTypeParamExcept(t.Val, own)
	case *FuncType:
		for _, p := range t.Params {
			if containsTypeParamExcept(p, own) {
				return true
			}
		}
		return containsTypeParamExcept(t.Return, own)
	case *TupleType:
		return anyContainsTypeParamExcept(t.Elems, own)
	case *EnumType:
		return anyContainsTypeParamExcept(t.TypeArgs, own)
	case *StructType:
		return anyContainsTypeParamExcept(t.TypeArgs, own)
	case *InterfaceType:
		return anyContainsTypeParamExcept(t.TypeArgs, own)
	case *DistinctType:
		return anyContainsTypeParamExcept(t.TypeArgs, own)
	case *AnonStructType:
		for _, f := range t.Fields {
			if containsTypeParamExcept(f.Type, own) {
				return true
			}
		}
		return false
	}
	// A shape ContainsTypeParam says holds a parameter and this switch has no
	// arm for. Answering TRUE keeps the original guard's behaviour, so a new
	// Type that grows type arguments cannot silently start recording a
	// partially resolved instantiation.
	return true
}

func anyContainsTypeParamExcept(ts []Type, own map[string]*TypeParam_) bool {
	for _, t := range ts {
		if containsTypeParamExcept(t, own) {
			return true
		}
	}
	return false
}

// recordInferredBoundOnTypeParam notes that the ENCLOSING generic function's own
// type parameter was used at `ifaceName`'s bound, for a consumer that compiles
// the body rather than walking it.
//
// # `Debug` only, and the restriction is the soundness argument
//
// Inferring a bound nobody wrote is sound exactly when it can reject nothing
// that would otherwise be accepted. `Debug` has that property and no other
// interface here does: `SynthesizeUniversalDebug` writes an `impl Debug for T`
// for every declared type, and `typeImplementsInterface` above answers TRUE for
// `Debug` at every concrete type and every bare type variable, so a `Debug`
// bound constrains nothing. Inferring `Display` from `Display.to_string(x)`
// would be the opposite: it would turn `fn f<T>(x: T)` called at a type with no
// `impl Display` from a call-site error into a DECLARATION error, moving a
// diagnostic a programmer can act on.
//
// `Struct` is deliberately excluded even though it is also predicate-decided:
// it is satisfied by every struct and by NOTHING ELSE, so a `Struct` bound does
// reject things and inferring one would change what checks.
//
// # It records rather than enforces, and those are separate fields
//
// Nothing here touches `ft.WhereBounds`, which is where a bound failure would
// come from, and nothing in this package reads what this writes. So an entry
// cannot make a program fail to check — which is the property that lets this run
// on every body rather than behind a flag.
//
// The type parameter must be the ENCLOSING function's own. `subs[SelfParam]` is
// whatever `self` SOLVED to, and inside a generic body that may be a callee's
// parameter as easily as this function's; keying a callee's `T` to this
// declaration node would attribute a bound to the wrong function. The identity
// check is pointer equality against `c.fnTypeParams`, which is exactly the map
// `checkFunc` built from this body's own signature.
func (c *checker) recordInferredBoundOnTypeParam(concrete Type, ifaceName string) {
	if ifaceName != "Debug" || c.currentFnDecl == nil || len(c.currentFnDecl.TypeParams) == 0 {
		return
	}
	tp, isParam := concrete.(*TypeParam_)
	if !isParam || tp.Name_ == "" || tp.Name_ == "self" {
		return
	}
	if c.fnTypeParams[tp.Name_] != tp {
		return
	}
	c.fa.RecordInferredTypeParamBound(c.currentFnDecl, tp.Name_, ifaceName)
}

// typeImplementsInterface reports whether `concrete` has an `impl iface for X`
// declaration recorded in any of the impls tables the checker consults
// (file-local + stdlib). Skips the check when concrete still has TypeParams
// or TypeVars — leftover inference bits get a free pass and downstream
// checks surface the real mismatch.
func typeImplementsInterface(c *checker, concrete Type, iface *InterfaceType, recPos Pos, recKind RecordingKind) bool {
	if iface == nil {
		return true
	}
	// Universal default Debug: every type is Debug-inspectable. The eager
	// auto-synthesis pass (SynthesizeUniversalDebug) materializes a
	// structural (or name-only, for opaque) Debug impl for every declared
	// type that lacks an explicit one, so any concrete type genuinely has a
	// Debug impl at runtime. Treat the interface-bound / conformance check as
	// always satisfied — for concrete types AND bare type variables —
	// without recording a manifest demand (the eager registration covers
	// Debug independent of the demanded set; see RegisterImplsFromManifest).
	// Every other interface stays demand-driven and enforced.
	if iface.Name == "Debug" {
		return true
	}
	if ContainsTypeParam(concrete) {
		return true
	}
	// Universal struct interface: `Struct` is satisfied structurally by every
	// struct — named or anonymous — and by nothing else. Like Debug, conformance
	// is decided by a predicate rather than a registered impl, so no manifest
	// demand is recorded (the host-backed `update` default covers every struct
	// receiver at runtime via dispatchShim's default fallback). A non-struct
	// here yields the bound's "X does not implement Struct" error.
	if iface.Name == "Struct" {
		return isStructShaped(concrete)
	}
	concreteName := interfaceImplName(concrete)
	if concreteName == "" {
		return true
	}
	for _, table := range c.implsContext() {
		names, ok := table.Impls[concreteName]
		if !ok || !names[iface.Name] {
			continue
		}
		// The recorded impl must name the SAME interface declaration the
		// bound does; a same-named interface from another file is another
		// interface. See interface_identity.go.
		if !sameNominalIdentity(iface.Origin, iface.Name, table.IfaceOrigins[concreteName][iface.Name], iface.Name) {
			continue
		}
		if c != nil && !c.implReceiverMatches(concrete, iface.Name) {
			return false
		}
		if c != nil && c.fa != nil {
			c.fa.RecordManifest(concreteName, iface.Name, Recording{Pos: recPos, Kind: recKind})
		}
		return true
	}
	return false
}

func (c *checker) implReceiverMatches(concrete Type, ifaceName string) bool {
	if c == nil {
		return true
	}
	name := interfaceImplName(concrete)
	if name == "" {
		return true
	}
	info := lookupImplTypeArgs(name, ifaceName, c.implTypeArgsContext())
	if info == nil {
		return true
	}
	return implReceiverMatches(info, concrete, c.implsContext(), c.implTypeArgsContext())
}

// registerArgSlotHint registers a synthetic Symbol at an argument's
// position so hover reveals which call parameter it's filling. Used for
// literals and placeholders — anything that wouldn't otherwise produce
// useful hover info. Identifiers and complex expressions are skipped
// (their own hover is more relevant).
//
// The Span field is set to the literal's source-text length so hover
// fires across the whole token (e.g. all three columns of `100`), not
// just the first character matching the synthetic symbol's Name.
func (c *checker) registerArgSlotHint(arg ast.Node, funcNode ast.Node, paramIdx int, paramTy Type) {
	// NamedArg key: register a Reference at the `name:` token's position
	// using the param's declared type looked up by name (not by paramIdx,
	// since named args may be out of positional order). Then recurse into
	// the inner Value to handle a literal or placeholder there too.
	if na, ok := arg.(*ast.NamedArg); ok {
		keyIdx := c.callParamIndex(funcNode, na.Name)
		keyTy := paramTy
		if keyIdx >= 0 {
			if sym, ok := c.callFuncSym(funcNode); ok {
				if ft, ok := sym.Type.(*FuncType); ok && keyIdx < len(ft.Params) {
					keyTy = ft.Params[keyIdx]
				}
			}
		}
		if keyTy != nil && na.Line > 0 {
			c.fa.References[Pos{Line: na.Line, Col: na.Col}] = &Symbol{
				Name: na.Name,
				Kind: SymbolArgHint,
				Pos:  Pos{Line: na.Line, Col: na.Col},
				Type: keyTy,
				Span: len(na.Name),
			}
		}
		// Recurse into the value with the looked-up slot so a literal
		// (e.g. `10` in `timeout: 10`) hovers as the named param, not
		// the positional slot the named arg happens to occupy.
		innerIdx := keyIdx
		if innerIdx < 0 {
			innerIdx = paramIdx
		}
		c.registerArgSlotHint(na.Value, funcNode, innerIdx, keyTy)
		return
	}
	if paramTy == nil {
		return
	}
	var line, col, span int
	switch a := arg.(type) {
	case *ast.Placeholder:
		line, col, span = a.Line, a.Col, 1
	case *ast.IntLit:
		line, col, span = a.Line, a.Col, len(fmt.Sprintf("%d", a.Value))
	case *ast.FloatLit:
		line, col, span = a.Line, a.Col, len(fmt.Sprintf("%g", a.Value))
	case *ast.DecimalLit:
		line, col, span = a.Line, a.Col, len(a.Lexeme)
	case *ast.CodepointLit:
		line, col, span = a.Line, a.Col, len(a.Lexeme)+2 // include quotes
	case *ast.StringLit:
		line, col, span = a.Line, a.Col, len(a.Value)+2 // include quotes
	default:
		return
	}
	c.fa.References[Pos{Line: line, Col: col}] = &Symbol{
		Name: c.callParamName(funcNode, paramIdx),
		Kind: SymbolArgHint,
		Pos:  Pos{Line: line, Col: col},
		Type: paramTy,
		Span: span,
	}
}

// callFuncSym returns the Reference symbol for a call's function node.
func (c *checker) callFuncSym(funcNode ast.Node) (*Symbol, bool) {
	var pos Pos
	switch fn := funcNode.(type) {
	case *ast.Ident:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.FieldAccess:
		if fn.Field != nil {
			pos = Pos{Line: fn.Field.Line, Col: fn.Field.Col}
		}
	default:
		return nil, false
	}
	sym, ok := c.fa.References[pos]
	if !ok {
		return nil, false
	}
	if sym.Resolved != nil {
		return sym.Resolved, true
	}
	return sym, true
}

// calleeParams returns the parameter list of the callable that funcNode
// resolves to, supporting:
//   - direct fn references (`add(...)` → *ast.FuncDef)
//   - host fn references (`io.print(...)` → *ast.ExternFunc)
//   - lambdas bound to a name (`f = |x| ...; f(...)` → *ast.Binding whose
//     Value is *ast.Lambda)
//
// Returns nil when the callee can't be resolved or isn't a callable with
// known params (e.g. a higher-order arg, an interface-method dispatch).
func (c *checker) calleeParams(funcNode ast.Node) []ast.Param {
	sym, ok := c.callFuncSym(funcNode)
	if !ok {
		return nil
	}
	switch def := sym.Node.(type) {
	case *ast.FuncDef:
		return def.Params
	case *ast.ExternFunc:
		return def.Params
	case *ast.Binding:
		if lam, ok := def.Value.(*ast.Lambda); ok {
			return lam.Params
		}
	}
	return nil
}

// callParamIndex returns the slot index of the parameter named `name` in
// the function being called via funcNode. Returns -1 when not found.
func (c *checker) callParamIndex(funcNode ast.Node, name string) int {
	for i, p := range c.calleeParams(funcNode) {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// callParamName returns the parameter name at slot `paramIdx` for the
// function being called via `funcNode` (an Ident, TypeIdent, or FieldAccess).
// Returns "_" when no name is available.
func (c *checker) callParamName(funcNode ast.Node, paramIdx int) string {
	params := c.calleeParams(funcNode)
	if paramIdx >= 0 && paramIdx < len(params) {
		return params[paramIdx].Name
	}
	return "_"
}

// checkTupleDistinctArgs validates a call to a tuple-distinct constructor.
// Two construction forms are accepted:
//   - call-form:      Pair((1, "x"))   — a single argument whose type unifies
//     with the inner tuple type.
//   - literal-attach: Pair(1, "x")     — N positional args matching the
//     inner tuple's element types.
//
// Mismatches surface targeted error messages:
//   - wrong arity:    "Pair takes 2 arguments, got N; expected Pair(Int, String)"
//   - wrong arg type: "argument I of Pair: expected T, got U"
func (c *checker) checkTupleDistinctArgs(n *ast.Call, dt *DistinctType, inner *TupleType) {
	arity := len(inner.Elems)

	// Call-form: exactly one arg whose type matches the inner tuple type.
	if len(n.Args) == 1 {
		argTy := c.checkNodeExpecting(n.Args[0], inner)
		argLine, argCol := nodeLineCol(n.Args[0])
		if argTy != nil && c.argMatchesParam(argTy, inner, c.recPos(argLine, argCol), RecordingKindCallSite) {
			return
		}
		// If the user passed a tuple-typed arg, they're in call-form: surface
		// the unify error so the diagnostic points at the actual element-type
		// mismatch rather than misleadingly blaming arity (the user did pass
		// exactly one argument).
		if _, isTuple := resolveTypeVar(argTy).(*TupleType); isTuple {
			if err := c.unify(inner, argTy, nil); err != nil {
				c.addError(n.Line, 1, err.Error())
			}
			return
		}
		// Single non-tuple arg — likely an attempted literal-attach with the
		// wrong arity. Flag as arity mismatch pointing at the expected shape.
		c.addError(n.Line, 1, fmt.Sprintf(
			"%s takes %d arguments, got 1; expected %s%s",
			dt.Name, arity, dt.Name, inner.String()))
		return
	}

	// Literal-attach: N args, each matching the corresponding inner element.
	if len(n.Args) == arity {
		for i, arg := range n.Args {
			expected := inner.Elems[i]
			argTy := c.checkNodeExpecting(arg, expected)
			if argTy == nil {
				continue
			}
			argLine, argCol := nodeLineCol(arg)
			if !c.argMatchesParam(argTy, expected, c.recPos(argLine, argCol), RecordingKindCallSite) {
				c.addMismatch(n.Line, 1, expected, argTy, c.typef("argument %d of %s: expected %s, got %s", i+1, dt.Name, expected, argTy))
			}
		}
		return
	}

	// Wrong arity — still descend into args so nested calls get checked.
	for _, arg := range n.Args {
		c.checkNode(arg)
	}
	c.addError(n.Line, 1, fmt.Sprintf(
		"%s takes %d arguments, got %d; expected %s%s",
		dt.Name, arity, len(n.Args), dt.Name, inner.String()))
}

// checkStructCallForm validates a call to a nominal struct constructor
// (`Foo({a: 1, b: "x"})`), the parallel to the literal-attach form
// `Foo{a: 1, b: "x"}`. The single argument is either an anonymous struct
// LITERAL or an expression whose inferred type is an anonymous struct —
// `Foo(defaults())`, `Foo(m)` — and either way its fields must shape-match
// the nominal struct: every field name must exist on the struct, every
// supplied type must match the declared field type, and every required
// (non-defaulted) field must be supplied. Anything else — wrong arg count,
// an argument that is not a record at all, unknown field, missing required
// field, mismatched field type — is a type error.
//
// This is the only place anon-struct -> nominal-struct coercion happens,
// and it is keyed on the CALL. `let f: Foo = {a: 1, b: "x"}` does NOT
// auto-coerce; an annotation is not a construction and the broader form
// stays out of scope.
//
// For generic structs (`type Box<T> { v: T }`), type parameters are
// inferred by unifying declared field types against the supplied values'
// types, mirroring how `Box{v: 1}` (literal-attach) infers `Box<Int>`.
// The returned StructType carries the inferred TypeArgs.
func (c *checker) checkStructCallForm(n *ast.Call, st *StructType) Type {
	c.checkOpaqueStructCall(n, st)
	if len(n.Args) != 1 {
		// Still walk args so nested expressions get checked.
		for _, arg := range n.Args {
			c.checkNode(arg)
		}
		c.addError(n.Line, 1, fmt.Sprintf(
			"%s takes 1 argument, got %d; expected %s({...}) with the struct's fields",
			st.Name, len(n.Args), st.Name))
		return st
	}

	// The single argument must be a struct literal, OR a value whose type
	// is an AnonStructType. Both shapes flow through checkNode, but only an
	// anonymous-struct literal (StructLit with no TypeName) gives us the
	// per-field source positions we want for diagnostics.
	arg := n.Args[0]
	lit, isLit := arg.(*ast.StructLit)
	if isLit && lit.TypeName == nil {
		ty := c.checkStructLitAgainstStruct(lit, st, false)
		c.fa.recordExprType(lit, ty)
		return ty
	}
	// Empty `{}` parses as a zero-statement Block (typed Unit), not as a
	// StructLit. Treat it as an empty anon-struct literal so that
	// `Empty({})` and `AllDefaults({})` type-check — every field is either
	// absent and defaulted, or absent and triggers a missing-required-field
	// error. Without this branch the call falls into the non-literal arm
	// below and rejects empty/all-defaulted structs.
	if blk, isBlk := arg.(*ast.Block); isBlk && len(blk.Stmts) == 0 {
		empty := &ast.StructLit{TypeName: nil, Fields: nil, Line: blk.Line, Col: blk.Col}
		return c.checkStructLitAgainstStruct(empty, st, false)
	}

	// Non-literal argument — accepted when its inferred type is an
	// AnonStructType whose shape matches, which is what makes
	// `MyApp(defaults())` work alongside `MyApp({...})`. Construction
	// reads the argument's fields by name, fills declared defaults and
	// brands the result with the module-qualified name, so field ORDER is
	// irrelevant and the nominal identity is the declared type's.
	//
	// What it gives up is per-field source positions: an `*AnonStructType`
	// carries field names and types but no `Pos`, so every diagnostic
	// below points at the ARGUMENT rather than at the offending field.
	// The field NAME stays in every message, which is the part a reader
	// acts on; the column is the part that degrades.
	argTy := c.checkNode(arg)
	if anon, ok := resolveTypeVar(argTy).(*AnonStructType); ok {
		return c.checkAnonStructTypeAgainstStruct(arg, anon, st)
	}
	c.addError(arg.LineNum(), 1, fmt.Sprintf(
		"%s expects an anonymous struct literal {...} matching its fields, got %s",
		st.Name, formatTypeForError(argTy)))
	return st
}

// checkOpaqueStructCall rejects the call form of an opaque struct
// (`Foo({...})`, `{...} |> Foo()`) outside its owning module: construction
// there would expose the field representation, so the owner module exports
// a constructor function instead. Mirrors the StructLit branch in
// checkStructLit and checkOpaqueConstructor for distinct types.
func (c *checker) checkOpaqueStructCall(n *ast.Call, st *StructType) {
	if !st.Opaque || (c.fa != nil && c.fa.FilePath == st.OwningSourceFile) {
		return
	}
	line, col := n.Line, 1
	if ti, ok := n.Func.(*ast.TypeIdent); ok {
		line, col = ti.Line, ti.Col
	}
	c.addError(line, col, fmt.Sprintf(
		"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", st.Name))
}

// checkPipedStructCallForm is checkStructCallForm for a pipe stage,
// `{x: 1} |> Point()`: the piped value is the record, and the call takes no
// argument of its own. The head was already checked (pipedTy), without the
// struct as its expected type, so the record's fields are matched by type,
// as for a non-literal argument.
func (c *checker) checkPipedStructCallForm(call *ast.Call, piped ast.Node, pipedTy Type, st *StructType) Type {
	c.checkOpaqueStructCall(call, st)
	if len(call.Args) != 0 {
		for _, arg := range call.Args {
			c.checkNode(arg)
		}
		at := operandPos(call.Args[0], call.Line, call.Col)
		c.addError(at.Line, at.Col, fmt.Sprintf(
			"%s takes the piped record as its only argument, got %d more",
			st.Name, len(call.Args)))
		return nil
	}
	if pipedTy == nil {
		return nil
	}
	if anon, ok := resolveTypeVar(pipedTy).(*AnonStructType); ok {
		return c.checkAnonStructTypeAgainstStruct(piped, anon, st)
	}
	line, col := nodeLineCol(piped)
	if line == 0 {
		line, col = piped.LineNum(), 1
	}
	c.addError(line, col, fmt.Sprintf(
		"%s expects an anonymous struct literal {...} matching its fields, got %s",
		st.Name, formatTypeForError(pipedTy)))
	return nil
}

// checkAnonStructTypeAgainstStruct validates a NON-LITERAL argument whose
// inferred type is an anonymous struct against the target nominal struct's
// field shape. It is checkStructLitAgainstStruct's twin, and the rules
// it applies are deliberately the same ones: every supplied field must
// exist on the struct, every supplied type must satisfy the declared type,
// and every field the argument omits must carry a declared default.
//
// The differences from the literal twin are exactly two, and both follow
// from having a TYPE rather than syntax:
//
//   - No positions. `*AnonStructType` has field names and field types and
//     no `Pos`, so `at` — the argument expression — is the site of every
//     diagnostic. Field names are still named.
//   - No expected-type push and no hover symbols. There is no field VALUE
//     node here to check against the declared type or to register, because
//     the fields were checked wherever the value was built. Lambda field
//     params are inferred at that site, not this one.
//
// Generic inference mirrors the twin: declared field types unify against
// the supplied field types and the returned StructType carries the solved
// TypeArgs, so `Box(recordOfInt)` is `Box<Int>` the way `Box({v: 1})` is.
func (c *checker) checkAnonStructTypeAgainstStruct(at ast.Node, anon *AnonStructType, st *StructType) Type {
	subs := map[*TypeParam_]Type{}
	canInferArgs := len(st.TypeParamDefs) > 0 && len(st.TypeArgs) == 0

	// nodeLineCol gives the argument's own column where it knows one; the
	// non-literal arm's existing refusal used `arg.LineNum()` with column 1
	// and that stays the fallback for a node shape it has no case for.
	line, col := nodeLineCol(at)
	if line == 0 {
		line, col = at.LineNum(), 1
	}

	provided := make(map[string]bool, len(anon.Fields))
	for _, f := range anon.Fields {
		declared := findFieldType(st.Fields, f.Name)
		if declared == nil {
			c.addError(line, col, fmt.Sprintf(
				"%s has no field '%s'", st.Name, f.Name))
			continue
		}
		provided[f.Name] = true
		if f.Type == nil {
			continue
		}
		if canInferArgs {
			_ = c.unifyInto(declared, f.Type, subs)
			continue
		}
		if !c.argMatchesParam(f.Type, declared, c.recPos(line, col), RecordingKindInterfaceTypedParam) {
			c.addMismatch(line, col, declared, f.Type, c.typef("field '%s' of %s: expected %s, got %s", f.Name, st.Name, declared, f.Type))
		}
	}

	for _, sf := range st.Fields {
		if provided[sf.Name] || sf.HasDefault {
			continue
		}
		c.addError(line, col, fmt.Sprintf(
			"missing field '%s' of %s", sf.Name, st.Name))
	}

	if canInferArgs && len(subs) > 0 {
		args := make([]Type, len(st.TypeParamDefs))
		for i, def := range st.TypeParamDefs {
			if t, ok := subs[def]; ok {
				args[i] = t
			} else {
				args[i] = def
			}
		}
		return &StructType{
			Origin:        st.Origin,
			Name:          st.Name,
			TypeArgs:      args,
			Fields:        st.Fields,
			TypeParams:    st.TypeParams,
			TypeParamDefs: st.TypeParamDefs,
		}
	}
	return st
}

// checkStructLitAgainstStruct IS THE VALIDATOR for a struct literal checked
// against a nominal struct, for both spellings that reach one: the
// literal-attach form `Cfg{port: 80}` and the constructor-call record form
// `Cfg({port: 80})`. It validates the field NAMES, checks each field VALUE
// through `argMatchesParam`, requires every field that carries no default,
// infers the struct's type arguments when the site supplies none, and returns
// the struct instantiated with whatever it solved.
//
// Routing both spellings through one validator is what keeps them in
// agreement. Without it, the literal-attach form would accept
// `Point{x: "s", y: 1}` where `x: Int`, an unknown field, or a missing field,
// while the constructor-call form refused all of them.
//
// `argMatchesParam` is why this stays lenient: it is the same rule struct
// updates, constructor calls and field defaults use, reaching
// `ifaceParamAdmits` for an interface-typed field. It admits a `Debug` or
// `Display` field holding an Int, an `Iter<Int>` field holding a list, a
// universal `Struct` field, an untyped empty list at `List<Int>`, a lambda at
// a function-typed field, `None` at `Maybe<Int>`, an anonymous-struct field,
// and a generic `T` field. A bare `TypesEqual` would reject most of them.
// It refuses `f: Float` given an Int (`expected Float, got Int`), as every
// other position in the language does.
func (c *checker) checkStructLitAgainstStruct(lit *ast.StructLit, st *StructType, patch bool) Type {
	// Mirror checkStructLit's generic inference machinery: when the struct
	// declares type parameters and the call site doesn't supply explicit
	// TypeArgs (it never does in call-form), unify declared field types
	// against value types to solve the type-param substitution.
	subs := map[*TypeParam_]Type{}
	canInferArgs := len(st.TypeParamDefs) > 0 && len(st.TypeArgs) == 0
	// The type the literal is expected to have, when it is this struct with
	// its arguments (`f: Box<(Int) -> Int> = Box{v: |x| x + 1}`), says what
	// a field whose type the values have not yet solved should be checked
	// against, so a lambda there gets its parameter types. It only steers
	// the field's check: the values still decide the arguments, and the
	// caller compares the result with what it expected.
	hints := c.expectedStructArgs(st)

	provided := make(map[string]bool, len(lit.Fields))
	for _, f := range lit.Fields {
		declared := findFieldType(st.Fields, f.Name)
		if declared == nil {
			// Still check the value to surface nested errors, but flag the unknown field.
			c.checkNode(f.Value)
			line, col := f.Line, f.Col
			if line == 0 {
				line, col = lit.Line, lit.Col
			}
			c.addError(line, col, fmt.Sprintf(
				"%s has no field '%s'", st.Name, f.Name))
			continue
		}
		provided[f.Name] = true
		// A BARE BRACE AT A STRUCT-TYPED FIELD OF A PATCH IS ITSELF A
		// PATCH, applied recursively. That is the whole of the deep rule,
		// and the recursion is this same validator rather than a second
		// field-checker: the field-name rule, the `argMatchesParam`
		// admission, the field-label hover and the opacity guard all stay
		// in one place, one level down.
		//
		// `patch` is why this cannot leak into ordinary construction.
		// `Outer{inner: {a: 9}}` has no base for `inner.b` to come from
		// and stays the type error it is today; the deep arm exists only
		// underneath a spread, exactly as `Partial<T>`'s depth exists only
		// underneath a `Partial<T>`.
		//
		// THREE SHAPES ARE EXCLUDED HERE AND EACH IS A VALUE, not a patch:
		// a nominal literal (`Inner{a: 9, b: 8}`, TypeName != nil), a
		// nested spread (`{..o.inner, a: 9}`, Spread != nil — already a
		// complete value of the field's type), and anything that is not a
		// brace literal at all. `patchTargetStruct` excludes the fourth,
		// an interface-typed field, by returning nil for it: you cannot
		// patch through an interface that does not name its fields, which
		// is the same place `deepPartialMatches` stops.
		if patch {
			if nested, isLit := f.Value.(*ast.StructLit); isLit && nested.TypeName == nil && nested.Spread == nil {
				if inner := c.patchTargetStruct(declared); inner != nil {
					if !c.opaqueUpdateBlocked(inner, nested, f.Line, f.Col) {
						c.checkStructLitAgainstStruct(nested, inner, true)
					}
					continue
				}
			}
		}
		// Push the (possibly partially-substituted) declared type as the
		// expected type so e.g. lambda field params get inferred.
		expected := declared
		if canInferArgs {
			expected = Substitute(Substitute(declared, subs), hints)
		}
		valTy := c.checkNodeExpecting(f.Value, expected)
		if valTy == nil {
			continue
		}
		if canInferArgs {
			_ = c.unifyInto(declared, valTy, subs)
		} else {
			valLine, valCol := nodeLineCol(f.Value)
			if valLine == 0 {
				valLine, valCol = lit.Line, lit.Col
			}
			if !c.argMatchesParam(valTy, declared, c.recPos(valLine, valCol), RecordingKindInterfaceTypedParam) {
				c.addMismatch(valLine, valCol, declared, valTy, c.typef("field '%s' of %s: expected %s, got %s", f.Name, st.Name, declared, valTy))
			}
		}
	}

	// Every required (non-defaulted) field must be supplied — UNLESS this
	// literal is a PATCH. `{..base, x: 9}` supplies every field from
	// `base`, and `Fields` holds only the overrides, so a field absent
	// here is not absent from the value. The same is true one level down:
	// `{..o, inner: {a: 9}}` takes `inner.b` from `o.inner`. See the note
	// on checkStructUpdateLit.
	//
	// Keyed on the PATCH parameter and not on `lit.Spread`, because the
	// nested patch has no spread of its own and needs the same leniency.
	// One concept rather than two: the only caller that passes true is
	// checkStructUpdateLit and its own recursion.
	if !patch {
		for _, sf := range st.Fields {
			if provided[sf.Name] || sf.HasDefault {
				continue
			}
			c.addError(lit.Line, lit.Col, fmt.Sprintf(
				"missing field '%s' of %s", sf.Name, st.Name))
		}
	}

	// THE FIELD LABEL'S HOVER, once the substitution exists. This has to run
	// after the loop, not inside it, because the loop is what solves `subs` —
	// a symbol minted at field 1 of `Pair{first: 1, second: "hi"}` would carry
	// an unsolved parameter. That is the defect the literal-attach form fixed
	// for itself and the call form did not: it registered `Type: declared`
	// inline, so `Box({v: 1})` hovered `v: T` where `Box{v: 1}` hovered
	// `v: Int`. One call site now, so they agree.
	c.recordLitFieldLabelTypes(lit, st.Fields, subs)

	// For generic structs, return the substituted instance carrying the
	// inferred TypeArgs (e.g. `Box<Int>` instead of bare `Box`). Mirrors
	// the shape checkStructLit returns for the literal-attach form.
	if canInferArgs && len(subs) > 0 {
		args := make([]Type, len(st.TypeParamDefs))
		for i, def := range st.TypeParamDefs {
			if t, ok := subs[def]; ok {
				args[i] = t
			} else {
				args[i] = def
			}
		}
		return &StructType{
			Origin:        st.Origin,
			Name:          st.Name,
			TypeArgs:      args,
			Fields:        st.Fields,
			TypeParams:    st.TypeParams,
			TypeParamDefs: st.TypeParamDefs,
		}
	}
	return st
}

// expectedStructArgs maps st's type parameters to the arguments of the type
// the expression being checked is expected to have, when that is st's own
// declaration instantiated, as in `f: Box<(Int) -> Int> = Box{...}`. It is
// nil otherwise, and leaves out an argument that is itself unsolved.
func (c *checker) expectedStructArgs(st *StructType) map[*TypeParam_]Type {
	if len(st.TypeParamDefs) == 0 || len(st.TypeArgs) != 0 {
		return nil
	}
	want, ok := resolveTypeVar(c.expectedAt).(*StructType)
	if !ok || want.Name != st.Name || want.Origin != st.Origin ||
		len(want.TypeArgs) != len(st.TypeParamDefs) || len(want.TypeParamDefs) != len(st.TypeParamDefs) {
		return nil
	}
	var hints map[*TypeParam_]Type
	for i, def := range st.TypeParamDefs {
		if want.TypeParamDefs[i] != def {
			return nil
		}
		arg := resolveTypeVar(want.TypeArgs[i])
		if arg == nil {
			continue
		}
		if _, open := arg.(*TypeVar); open {
			continue
		}
		if hints == nil {
			hints = map[*TypeParam_]Type{}
		}
		hints[def] = arg
	}
	return hints
}

// checkTargetTypedStructLit checks a brace literal with no type name and no
// spread at a position that expects the nominal struct `want`:
//
//	a: Address = {street: "1 Main", city: "Bath"}
//	Person{name: "Ada", address: {street: "1 Main", city: "Bath"}}
//
// The literal builds `want`, exactly as `Address{...}` would: the same
// validator (checkStructLitAgainstStruct, not as a patch, so a field it omits
// without a default is missing), the same opacity guard as checkStructLit's
// named head, and the IR builder lowers it as the named literal through
// FileAnalysis.TargetStructs. Only a *StructType reaches here: an interface,
// a type parameter, an enum or an anonymous struct type keeps the literal
// anonymous, because none of them names one struct to build.
//
// A generic `want` whose arguments are all known (`b: Box<Int> = {item: 3}`)
// checks the fields against the substituted declaration and returns `want`.
// One whose arguments still hold a type parameter or an unsolved variable
// (a generic callee's `Box<T>`) infers them from the fields, as `Box{...}`
// does, and the caller unifies the result.
func (c *checker) checkTargetTypedStructLit(lit *ast.StructLit, want *StructType) Type {
	canon := canonicalStructForFieldAccess(c, want)
	if canon == nil || len(canon.Fields) == 0 {
		return c.checkNode(lit)
	}
	if canon.Opaque && (c.fa == nil || c.fa.FilePath != canon.OwningSourceFile) {
		c.addError(lit.Line, lit.Col, fmt.Sprintf(
			"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", canon.Name))
	}
	var result Type
	if len(canon.TypeArgs) == 0 {
		result = c.checkStructLitAgainstStruct(lit, canon, false)
	} else if typeArgsKnown(canon.TypeArgs) && len(canon.TypeParamDefs) == len(canon.TypeArgs) {
		subs := make(map[*TypeParam_]Type, len(canon.TypeArgs))
		for i, tp := range canon.TypeParamDefs {
			subs[tp] = canon.TypeArgs[i]
		}
		fields := make([]FieldDef, len(canon.Fields))
		for i, f := range canon.Fields {
			fields[i] = f
			fields[i].Type = Substitute(f.Type, subs)
		}
		view := &StructType{
			Origin:           canon.Origin,
			Name:             canon.Name,
			Fields:           fields,
			Opaque:           canon.Opaque,
			OwningSourceFile: canon.OwningSourceFile,
		}
		c.checkStructLitAgainstStruct(lit, view, false)
		result = want
	} else {
		decl := *canon
		decl.TypeArgs = nil
		result = c.checkStructLitAgainstStruct(lit, &decl, false)
	}
	if st, ok := result.(*StructType); ok && c.fa != nil {
		if c.fa.TargetStructs == nil {
			c.fa.TargetStructs = map[*ast.StructLit]*StructType{}
		}
		c.fa.TargetStructs[lit] = st
	}
	return result
}

// typeArgsKnown reports whether no type argument holds a type parameter or an
// unsolved type variable.
func typeArgsKnown(args []Type) bool {
	for _, a := range args {
		if ContainsTypeParam(a) || containsTypeVar(a) {
			return false
		}
	}
	return true
}

// checkStructUpdateLit checks `{..base, field: value}`, struct update by
// spread.
//
// IT ADDS NO VALIDATOR. The head is checked as an ordinary expression, and
// the remaining fields go through `checkStructLitAgainstStruct`, which is
// already the one place a struct literal's field names are validated and its
// field values admitted (through `argMatchesParam`, so an interface-typed or
// type-parameter field stays exactly as lenient here as in `Cfg{...}`).
// Two rules differ and both live inside that validator, keyed on its
// `patch` parameter: a field the literal omits is not missing, and a bare
// brace at a struct-typed field is a PATCH rather than a value.
//
// # THE PATCH IS DEEP
//
// A bare brace at a struct-typed field is checked as a patch of that field,
// not as a whole value of the declared field type. So
//
//	Struct.update(user, {address: {city: "NYC"}})
//	{..user, address: {city: "NYC"}}
//
// are the same operation. `Struct.update` remains the form that takes a
// COMPUTED patch — a variable holding one — and the form reachable by name.
// The nested-spread spelling `{..user, address: {..user.address, city: …}}`
// stays legal and stays equivalent.
//
// A `Partial<self>` patch and a spread admit the same shapes, and the
// admission is one rule written once. It is NOT `deepPartialMatches`, and
// that is a choice with a reason rather than a duplication: that walk takes
// TYPES and returns a bool. Its own header says a nested field "has no node
// of its own here (only types reach this walk)", so it cannot name the
// nested type in a diagnostic or point at the offending nested field. The
// spread has an `*ast.StructLit` per field, so its recursion is the
// AST-level validator calling itself, which gets `unknown field 'z' on
// struct Inner` and `field 'a' of Inner: expected Int, got String` by
// construction rather than by a second message-building walk. The two are
// pinned to agree by TestSpreadDeep_AgreesWithStructUpdate rather than by
// sharing a function they cannot share.
//
// THE RESULT TYPE IS THE HEAD'S TYPE, which is also what
// `Struct.update(original, updates): self` returns. Nominal in,
// nominal out; anonymous in, anonymous out.
//
// An ANONYMOUS head reaches the same validator through a StructType built
// from the head's own field shape. That is an adapter, not a second
// implementation: the field-name rule, the `argMatchesParam` admission and
// the field-label hover are all the validator's, and the type handed back is
// the head's `AnonStructType` rather than the adapter.
func (c *checker) checkStructUpdateLit(n *ast.StructLit) Type {
	// A spread that changes no field is its base value. Values are immutable
	// and have no identity, so there is nothing a copy could do that the
	// value itself does not; the form only gives a reader something to work
	// out.
	if len(n.Fields) == 0 {
		line, col := n.SpreadLine, n.SpreadCol
		if line == 0 {
			line, col = n.Line, n.Col
		}
		base := "the value itself"
		if id, ok := n.Spread.(*ast.Ident); ok {
			base = "`" + id.Name + "`"
		}
		c.addError(line, col, "a struct spread with no fields is its base value; write "+base)
	}
	head := resolveTypeVar(c.checkNode(n.Spread))
	if head == nil {
		for _, f := range n.Fields {
			c.checkNode(f.Value)
		}
		return nil
	}
	switch h := head.(type) {
	case *StructType:
		st := h
		// THE HEAD'S TYPE ARGUMENTS ARE ALREADY SOLVED, and the field
		// shape has to be read through them. `bx: Box<Int>` gives a
		// StructType with TypeArgs [Int] but Fields still declared as
		// `item: T`, and the validator's own inference is off precisely
		// because the arguments are present — so without this,
		// `{..bx, item: 4}` reported `field 'item' of Box: expected T,
		// got Int`. `partialTargetFields` is the substitution
		// `Struct.update`'s patch walk already uses, and it also resolves
		// a thin name-only reference through the registry.
		if shape := partialTargetFields(st, c.reg); shape != nil {
			st = &StructType{
				Origin:           st.Origin,
				Name:             st.Name,
				Fields:           shape,
				Opaque:           st.Opaque,
				OwningSourceFile: st.OwningSourceFile,
			}
		}
		// NAMING A PRIVATE FIELD IS THE EXPOSURE, wherever the name
		// appears. `{..c, value: 9}` on an opaque struct outside its
		// defining file is refused with the same wording as
		// `Struct.update(c, {value: -999})`. `{..c}` alone names no field
		// and stays legal, exactly as whole-value replacement does.
		//
		// One implementation, because the deep patch reaches an opaque
		// struct one level down too — `{..box, inner: {value: -999}}`
		// where `Box.inner: Counter`. `opaquePatchField`'s header names
		// exactly that shape as the reason its walk is keyed on the patch
		// rather than on the receiver, so a nested arm that did not ask
		// would have made the spread a way around opacity.
		if c.opaqueUpdateBlocked(st, n, n.Line, n.Col) {
			return h
		}
		c.checkStructLitAgainstStruct(n, st, true)
		return h
	case *AnonStructType:
		c.checkStructLitAgainstStruct(n, &StructType{Name: h.String(), Fields: h.Fields}, true)
		return h
	default:
		line, col := n.SpreadLine, n.SpreadCol
		if line == 0 {
			line, col = n.Line, n.Col
		}
		c.addError(line, col, fmt.Sprintf(
			"struct spread `..` requires a struct, got %s", formatTypeForError(head)))
		for _, f := range n.Fields {
			c.checkNode(f.Value)
		}
		return nil
	}
}

// patchTargetStruct returns the struct a nested patch would apply to, or nil
// when the position takes a VALUE and not a patch.
//
// It answers for exactly the positions `deepPartialMatches` recurses into: a
// named struct and an anonymous record. Its field shape comes from
// `partialTargetFields`, which is `Struct.update`'s own resolution — generic
// arguments substituted in, and a thin name-only `StructType` resolved through
// the registry — so the two forms cannot disagree about what fields the nested
// target has.
//
// NIL FOR AN INTERFACE-TYPED FIELD, and that is the rule rather than an
// omission: a `Partial<T>` stops at an interface for the same reason, because
// an interface does not name its fields and there is nothing to patch through.
// The field still takes a value, admitted by `argMatchesParam` reaching
// `ifaceParamAdmits`, so `{..stage, who: Quiet{n: 2}}` is unaffected.
//
// Nil for a type parameter as well, which is the same answer read off a
// different absence: an UNSOLVED `T` is not struct-shaped. A SOLVED one is
// already substituted by `partialTargetFields` before it gets here, so
// `{..bx, item: {a: 9}}` at `Box<Inner>` patches and `Box<T>` does not.
func (c *checker) patchTargetStruct(declared Type) *StructType {
	switch t := resolveTypeVar(declared).(type) {
	case *AnonStructType:
		if len(t.Fields) == 0 {
			return nil
		}
		return &StructType{Name: t.String(), Fields: t.Fields}
	case *StructType:
		fields := partialTargetFields(t, c.reg)
		if len(fields) == 0 {
			return nil
		}
		return &StructType{
			Origin:           t.Origin,
			Name:             t.Name,
			Fields:           fields,
			Opaque:           t.Opaque,
			OwningSourceFile: t.OwningSourceFile,
		}
	}
	return nil
}

// opaqueUpdateBlocked reports, and returns true, when `lit` patches a field of
// an opaque struct declared outside the file being checked.
//
// Two callers: the spread's head (`{..c, value: 9}`) and a nested patch one or
// more levels down (`{..box, inner: {value: 9}}`). They were two copies of
// this check for as long as there was one of them; the nested arm is what made
// the second copy a fork waiting to happen.
//
// `fbLine`/`fbCol` position the diagnostic when the offending field carries no
// position of its own. Returns false for a field-less `{..c}`, which names
// nothing and is as legal as whole-value replacement is in `opaquePatchField`.
//
// The values are still checked when it blocks, so a second error inside the
// patch is not swallowed by the first.
func (c *checker) opaqueUpdateBlocked(st *StructType, lit *ast.StructLit, fbLine, fbCol int) bool {
	if len(lit.Fields) == 0 {
		return false
	}
	canon := canonicalStructForFieldAccess(c, st)
	if canon == nil || !canon.Opaque || (c.fa != nil && c.fa.FilePath == canon.OwningSourceFile) {
		return false
	}
	line, col := lit.Fields[0].Line, lit.Fields[0].Col
	if line == 0 {
		line, col = fbLine, fbCol
	}
	c.addError(line, col, fmt.Sprintf(
		"cannot update field '%s' of opaque type '%s' outside its defining module — use an exported constructor function",
		lit.Fields[0].Name, canon.Name))
	for _, f := range lit.Fields {
		c.checkNode(f.Value)
	}
	return true
}

// formatTypeForError returns a human-readable type string, falling back to
// "<unknown>" when t is nil so error messages remain readable.
func formatTypeForError(t Type) string {
	if t == nil {
		return "<unknown>"
	}
	return t.String()
}

// checkVariantTuplePayloadFlatCall validates the literal-attach flat call
// `Variant(a, b, ...)` for a variant constructor whose 1-arg payload is a
// tuple of arity N >= 2 (and the call supplied exactly N positional args).
// This is the construction-side parallel to the flat tuple-destructure
// pattern `Variant(a, b)` accepted in checkPattern's EnumPattern branch
// and to the tuple-distinct literal-attach `Pair(1, "x")` in
// checkTupleDistinctArgs. Each positional arg is type-checked against the
// matching tuple element; mismatches surface a per-arg diagnostic.
// Returns true when the flat-call shape applies (caller suppresses its
// generic argument-count check), false otherwise.
func (c *checker) checkVariantTuplePayloadFlatCall(n *ast.Call, ft *FuncType) bool {
	if len(ft.Params) != 1 {
		return false
	}
	tup, ok := ft.Params[0].(*TupleType)
	if !ok || len(tup.Elems) < 2 {
		return false
	}
	if len(n.Args) != len(tup.Elems) {
		return false
	}
	for _, arg := range n.Args {
		if _, named := arg.(*ast.NamedArg); named {
			return false
		}
		if _, ph := arg.(*ast.Placeholder); ph {
			return false
		}
	}
	if !calleeIsVariantConstructor(c, n.Func) {
		return false
	}
	name := variantConstructorName(n.Func)
	for i, arg := range n.Args {
		expected := tup.Elems[i]
		argTy := c.checkNodeExpecting(arg, expected)
		if argTy == nil {
			continue
		}
		argLine, argCol := nodeLineCol(arg)
		if !c.argMatchesParam(argTy, expected, c.recPos(argLine, argCol), RecordingKindCallSite) {
			c.addMismatch(n.Line, 1, expected, argTy, c.typef("argument %d of %s: expected %s, got %s", i+1, name, expected, argTy))
		}
	}
	return true
}

// variantConstructorName extracts the variant's surface name from a callee
// node for use in error messages. Falls back to a generic placeholder.
func variantConstructorName(funcNode ast.Node) string {
	switch fn := funcNode.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.TypeIdent:
		return fn.Name
	case *ast.FieldAccess:
		if fn.Field != nil {
			return fn.Field.Name
		}
	}
	return "<variant>"
}

// calleeIsVariantConstructor reports whether the call's func node ultimately
// resolves to a Symbol of kind SymbolEnumVariant.
func calleeIsVariantConstructor(c *checker, funcNode ast.Node) bool {
	var pos Pos
	switch fn := funcNode.(type) {
	case *ast.Ident:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.FieldAccess:
		if fn.Field != nil {
			pos = Pos{Line: fn.Field.Line, Col: fn.Field.Col}
		}
	default:
		return false
	}
	sym, ok := c.fa.References[pos]
	if !ok || sym == nil {
		return false
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Kind == SymbolEnumVariant
}

// attachCallType sets CallType on the reference symbol for a call's function node.
func (c *checker) attachCallType(funcNode ast.Node, ct Type) {
	var pos Pos
	switch fn := funcNode.(type) {
	case *ast.Ident:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.FieldAccess:
		if fn.Field != nil {
			pos = Pos{Line: fn.Field.Line, Col: fn.Field.Col}
		}
	case *ast.DotVariant:
		// Keyed past the dot, as recordDotVariantReference keys it.
		pos = Pos{Line: fn.Line, Col: fn.Col + 1}
	default:
		return
	}
	if sym, ok := c.fa.References[pos]; ok {
		// Create a per-call-site copy so that each generic call site
		// gets its own CallType without overwriting other sites that
		// share the same definition symbol.
		ref := *sym
		ref.CallType = ct
		if fa, ok := funcNode.(*ast.FieldAccess); ok {
			if owner, ok := fa.Object.(*ast.TypeIdent); ok {
				ref.CallReceiver = owner.Name
			}
		}
		c.fa.References[pos] = &ref
	}
}

// checkLocallyDetermined enforces that a value binding's type is readable
// from its own line. It rejects a binding whose type carries *no* concrete
// information — a "wholly unresolved" type: a bare inference var, or a
// generic whose every direct type argument is an unbound var (e.g.
// `ch = Channel.buffered(4)` → Channel<?>, `m = Map.empty()` →
// Map<?, ?>, `xs = []` → List<?>, `x = None` → Maybe<?>). The fix is an LHS
// annotation or an RHS turbofish.
//
// A generic with any concrete argument is considered locally readable and is
// exempt — notably the idiomatic variant constructions where a *secondary*
// type param is harmlessly open (`x = Ok(42)` → Result<Int, ?>,
// `Some(Ok(42))` → Maybe<Result<Int, ?>>): the value content is determined,
// only the absent variant's type is free.
//
// Annotated bindings (declared != nil) are exempt: the annotation is the
// local determination. Type parameters in scope (a function's own <T>) are
// not inference vars, so generic-function-local bindings are unaffected.
func (c *checker) checkLocallyDetermined(name string, value ast.Node, valTy, declared Type, line, col int) {
	if declared != nil || valTy == nil {
		return
	}
	if isWhollyUnresolved(valTy) {
		c.markUndeterminedReported(value)
		c.addError(line, col, fmt.Sprintf(
			"binding '%s' type is not locally determined: it has unsolved type "+
				"parameter(s) — add an annotation (`%s: T = …`) or a type argument (`f<T>(…)`)",
			name, name))
	}
}

// patternSite is one name a pattern or destructure binds, at the position
// the builder defined its symbol.
type patternSite struct {
	name string
	pos  Pos
}

// patternSites is every name pattern binds, in source order, mirroring
// builder.definePatternOwned.
func patternSites(pattern ast.Node) []patternSite {
	var out []patternSite
	var walk func(ast.Node)
	walk = func(node ast.Node) {
		switch n := node.(type) {
		case *ast.IdentPattern:
			out = append(out, patternSite{n.Name, Pos{Line: n.Line, Col: n.Col}})
		case *ast.AsPattern:
			walk(n.Pattern)
			out = append(out, patternSite{n.Name, Pos{Line: n.NameLine, Col: n.NameCol}})
		case *ast.Binary:
			walk(n.Left)
			walk(n.Right)
		case *ast.EnumPattern:
			if n.Payload != nil {
				walk(n.Payload)
			} else if n.Binding != "" {
				out = append(out, patternSite{n.Binding, Pos{Line: n.Line, Col: n.BindingCol}})
			}
		case *ast.StructPattern:
			for _, f := range n.Fields {
				if f.Pattern != nil {
					walk(f.Pattern)
				} else if f.Binding != "" {
					out = append(out, patternSite{f.Binding, Pos{Line: structPatternFieldBindingLine(f, n.Line), Col: f.BindingCol}})
				}
			}
		case *ast.TuplePattern:
			for _, p := range n.Patterns {
				walk(p)
			}
		case *ast.ListPattern:
			for _, h := range n.Heads {
				walk(h)
			}
			if n.TailSpread != nil {
				walk(n.TailSpread)
			}
		case *ast.MapPattern:
			for _, e := range n.Entries {
				walk(e.Pattern)
			}
		}
	}
	walk(pattern)
	return out
}

// notePatternBindings queues the names a pattern or destructure binds for
// checkUndeterminedPatternBindings. value is the matched expression.
func (c *checker) notePatternBindings(sites []patternSite, value ast.Node) {
	if len(sites) > 0 {
		c.undeterminedPatterns = append(c.undeterminedPatterns, undeterminedPattern{sites: sites, value: value})
	}
}

// undeterminedPattern is one pattern or destructure whose bound names are
// checked at the end of the file.
type undeterminedPattern struct {
	sites []patternSite
	value ast.Node
}

// checkUndeterminedPatternBindings reports each name a pattern bound whose
// type is still an unsolved variable once every body in the file is checked.
// `Ok(z) = Maybe.to_result(None, "none") else { ... }` binds z to the T of
// `Maybe<T>`, which nothing fixes, so z is a value of no type and no use of
// it can be compiled. A name whose type only holds an unsolved variable
// (`a` in `(a, b) = (None, 1)`, a Maybe<T> that can only be None) is a
// value like any other and is not reported.
//
// A pattern's names are checked at the end, not where they are bound as
// `name = value` is (checkLocallyDetermined), because the matched value is
// often a lambda parameter that later lines of the body determine
// (`|state = ([], []), item| { (ts, fs) = state ... }` in std/iter). A
// matched value whose call is reported here is not reported again by
// checkUndeterminedTypeArgs.
func (c *checker) checkUndeterminedPatternBindings() {
	for _, u := range c.undeterminedPatterns {
		for _, s := range u.sites {
			if ast.IsDiscardName(s.name) {
				continue
			}
			sym := c.fa.Definitions[s.pos]
			if sym == nil || sym.Type == nil || !isUnresolvedVar(sym.Type) {
				continue
			}
			c.markUndeterminedReported(u.value)
			c.addError(s.pos.Line, s.pos.Col, fmt.Sprintf(
				"the type of '%s' is not determined: nothing fixes the type the pattern binds it to — "+
					"annotate the matched value (`v: T = …`) or give a call a type argument (`f<T>(…)`)",
				s.name))
		}
	}
}

// isUnresolvedVar reports whether t resolves to an unbound inference var.
func isUnresolvedVar(t Type) bool {
	_, ok := resolveTypeVar(t).(*TypeVar)
	return ok
}

// allUnresolvedVars reports whether args is non-empty and every element is an
// unbound inference var.
func allUnresolvedVars(args []Type) bool {
	if len(args) == 0 {
		return false
	}
	for _, a := range args {
		if !isUnresolvedVar(a) {
			return false
		}
	}
	return true
}

// isWhollyUnresolved reports whether a binding type carries no concrete
// information: a bare unbound var, or a generic whose direct type arguments
// are all unbound vars. A generic with any concrete argument (Result<Int, ?>,
// Maybe<Result<…>>) is locally readable and not wholly unresolved.
func isWhollyUnresolved(t Type) bool {
	switch tt := resolveTypeVar(t).(type) {
	case *TypeVar:
		return true
	case *ListType:
		return isUnresolvedVar(tt.Elem)
	case *MapType:
		return isUnresolvedVar(tt.Key) && isUnresolvedVar(tt.Val)
	case *TupleType:
		return allUnresolvedVars(tt.Elems)
	case *EnumType:
		return allUnresolvedVars(tt.TypeArgs)
	case *StructType:
		return allUnresolvedVars(tt.TypeArgs)
	case *InterfaceType:
		return allUnresolvedVars(tt.TypeArgs)
	case *DistinctType:
		return allUnresolvedVars(tt.TypeArgs)
	}
	return false
}

func (c *checker) checkBinding(n *ast.Binding) Type {
	c.validateBindingLikeName(n.Name, n.Line, n.Col, "binding name")
	// Resolve an inline annotation, if present. The enclosing fn's type-param
	// map is in scope so e.g. `acc: List<T> = …` inside `fn foo<T>(...)` binds
	// the same TypeParam_ used in the outer signature. Resolution failures
	// surface as type errors and the annotation is treated as absent for
	// inference. We resolve before the unannotated-param guard so a function-
	// typed annotation can suppress the guard (it will drive each param's
	// type via bidirectional inference into checkLambdaExpecting).
	var declared Type
	if n.TypeAnnotation != nil {
		resolved, err := c.resolveBodyType(n.TypeAnnotation)
		if err != nil {
			if te, ok := err.(TypeError); ok {
				c.errors = append(c.errors, te)
			} else {
				c.addError(n.Line, n.Col, err.Error())
			}
		} else {
			declared = resolved
		}
	}

	// A lambda bound to a local name with no annotation has no function type
	// to take its parameters' types from; checkNode reports each one it
	// cannot infer (checkLambda). For a non-function
	// annotation (`f: Int = |x| ...`) the unify check below reports the
	// mismatch, with no per-parameter errors beside it.

	var valTy Type
	if declared != nil {
		// Push the declared type into the RHS so polymorphic returns
		// (e.g. `Map.empty(): Map<K, V>`) can resolve to a concrete type
		// instead of leaving fresh TypeVars unbound.
		valTy = c.checkNodeExpecting(n.Value, declared)
		// Validate: the RHS type must unify with the declared type. The
		// unify binds any TypeVars on the RHS side to the concrete pieces
		// of `declared`; the local subs map captures TypeParam_ bindings
		// (e.g. `Ok(42)` returns `Result<Int, E>` where the variant
		// constructor's free `E` only resolves once the annotation is
		// known) so we can substitute them out of valTy.
		if valTy != nil {
			subs := map[*TypeParam_]Type{}
			if err := c.unifyInto(declared, valTy, subs); err != nil {
				c.report(errAt(n.Value, c.typef(
					"type mismatch: expected %s, got %s", declared, valTy)).WithHint(defaultedFuncValueHint(declared, valTy, n.Value)).WithHint(embedsDowncastHint(declared, valTy)))
			} else if len(subs) > 0 {
				valTy = Substitute(valTy, subs)
			}
		}
	} else {
		valTy = c.checkNode(n.Value)
		if isTodoValue(n.Value) && !ast.IsDiscardName(n.Name) {
			// `todo` takes the type its position expects, and an unannotated
			// binding expects none, so nothing says what the name holds.
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"cannot infer a type for '%s' from `todo`: annotate the binding (`%s: T = todo`)",
				n.Name, n.Name))
			return TypeUnit
		}
	}

	if !ast.IsDiscardName(n.Name) {
		c.checkLocallyDetermined(n.Name, n.Value, valTy, declared, n.Line, n.Col)
	}

	// Attach the type to the binding's symbol. When an annotation was
	// supplied, prefer it over the inferred RHS type so hover renders what
	// the user wrote (`Map<String, Int>`) rather than the equivalent
	// inferred shape with resolved TypeVars. They unify, so this is
	// presentation, not soundness.
	pos := Pos{Line: n.Line, Col: n.Col}
	if sym, ok := c.fa.Definitions[pos]; ok && sym.Type != nil && declared == nil && valTy != nil {
		// The binding is checked a second time (specializeInterfaceMethod
		// reads the type of `Iter.count({ q = f(_, x); q(y) })`'s block
		// before checkCall checks it against its parameter). Its reads
		// still see the symbol's first type, so the inference variables
		// this check made for the value are solved through it; otherwise
		// what they record (a partial's instantiated signature) stays
		// open.
		_ = c.unify(sym.Type, valTy, nil)
	}
	if sym, ok := c.fa.Definitions[pos]; ok && sym.Type == nil {
		switch {
		case declared != nil:
			sym.Type = declared
		case valTy != nil:
			sym.Type = valTy
		}
		// Surface the original param names so hover renders
		// `(x: Int) -> Int` instead of the nameless `(Int) -> Int`. Two
		// shapes apply here: a direct lambda (`f = |x, y| ...`) and a
		// partial application (`g = f(_, 2)`).
		switch v := n.Value.(type) {
		case *ast.Lambda:
			names := make([]string, len(v.Params))
			for i, p := range v.Params {
				names[i] = p.Name
			}
			sym.ParamNames = names
		case *ast.Call:
			if names := c.partialAppParamNames(v); len(names) > 0 {
				sym.ParamNames = names
			}
		}
	}

	return TypeUnit
}

// partialAppParamNames returns the original parameter names of the unbound
// slots in a partial-application call (one with `_` placeholders), parallel
// to the FuncType returned by checkCall. Empty entries mark slots whose name
// couldn't be resolved (callee not a FuncDef/ExternFunc, or arg index past
// the param list). Returns nil when the call has no placeholders or when no
// names are recoverable.
func (c *checker) partialAppParamNames(call *ast.Call) []string {
	var names []string
	any := false
	posIdx := 0
	for _, arg := range call.Args {
		switch a := arg.(type) {
		case *ast.NamedArg:
			if _, isPh := a.Value.(*ast.Placeholder); !isPh {
				continue
			}
			if a.Name == "" || a.Name == "_" {
				names = append(names, "")
			} else {
				names = append(names, a.Name)
				any = true
			}
		case *ast.Placeholder:
			name := c.callParamName(call.Func, posIdx)
			if name == "" || name == "_" {
				names = append(names, "")
			} else {
				names = append(names, name)
				any = true
			}
			posIdx++
		default:
			posIdx++
		}
	}
	if !any {
		return nil
	}
	return names
}

func (c *checker) checkTupleDestructure(n *ast.TupleDestructure) Type {
	for _, b := range n.Bindings {
		if b == nil {
			continue
		}
		c.validateBindingLikeName(b.Name, b.Line, b.Col, "binding name")
	}
	valTy := c.checkNode(n.Value)

	if valTy == nil {
		return TypeUnit
	}

	tt, ok := valTy.(*TupleType)
	if !ok {
		c.addError(n.Line, n.Col, fmt.Sprintf("tuple destructure requires a tuple type, got %s", valTy))
		return TypeUnit
	}

	if len(n.Bindings) != len(tt.Elems) {
		c.addError(n.Line, n.Col, fmt.Sprintf("tuple destructure has %d bindings but tuple has %d elements", len(n.Bindings), len(tt.Elems)))
		return TypeUnit
	}

	for i, binding := range n.Bindings {
		if binding == nil {
			continue // wildcard _
		}
		sym := c.fa.Definitions[Pos{Line: binding.Line, Col: binding.Col}]
		if sym != nil {
			sym.Type = tt.Elems[i]
		}
	}
	var sites []patternSite
	for _, b := range n.Bindings {
		if b != nil {
			sites = append(sites, patternSite{b.Name, Pos{Line: b.Line, Col: b.Col}})
		}
	}
	c.notePatternBindings(sites, n.Value)

	return TypeUnit
}

func (c *checker) checkStructDestructure(n *ast.StructDestructure) Type {
	for _, f := range n.Fields {
		if f.Binding == "" {
			continue
		}
		line := structPatternFieldBindingLine(f, n.Line)
		c.validateBindingLikeName(f.Binding, line, f.BindingCol, "binding name")
	}
	valTy := c.checkNode(n.Value)

	if valTy == nil {
		return TypeUnit
	}

	var fields []FieldDef
	var structName string
	var subs map[*TypeParam_]Type

	switch st := valTy.(type) {
	case *StructType:
		fields = st.Fields
		structName = st.Name
		if len(st.TypeParamDefs) > 0 && len(st.TypeArgs) == len(st.TypeParamDefs) {
			subs = make(map[*TypeParam_]Type, len(st.TypeParamDefs))
			for i, def := range st.TypeParamDefs {
				subs[def] = st.TypeArgs[i]
			}
		}
	case *AnonStructType:
		fields = st.Fields
		structName = st.String()
	default:
		c.addError(n.Line, n.Col, fmt.Sprintf("struct destructure requires a struct type, got %s", valTy))
		return TypeUnit
	}

	for _, f := range n.Fields {
		var fieldType Type
		found := false
		for _, sf := range fields {
			if sf.Name == f.Name {
				fieldType = sf.Type
				found = true
				break
			}
		}
		if !found {
			c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("field %s not found in type %s", f.Name, structName)}.WithHint(didYouMean(f.Name, fieldNames(fields))))
			continue
		}
		if fieldType != nil && subs != nil {
			fieldType = Substitute(fieldType, subs)
		}
		if f.Binding != "" {
			line := structPatternFieldBindingLine(f, n.Line)
			sym := c.fa.Definitions[Pos{Line: line, Col: f.BindingCol}]
			if sym != nil {
				sym.Type = fieldType
			}
		}
		// Hover on the field-name key (`x` in `{x: a} = expr`) shows the
		// field's type from the struct definition. Skip when the field is
		// a punning binding (`{x} = expr`, `Name == Binding` at the same
		// position) — the SymbolBinding registered by the builder already
		// occupies that position, and registering a second symbol at the
		// same position fights the binding for semantic-token coloring.
		bindingLine := structPatternFieldBindingLine(f, n.Line)
		punning := f.Binding == f.Name && f.NameLine == bindingLine && f.NameCol == f.BindingCol
		if f.NameLine > 0 && fieldType != nil && !punning {
			c.fa.References[Pos{Line: f.NameLine, Col: f.NameCol}] = &Symbol{
				Name: f.Name,
				Kind: SymbolField,
				Pos:  Pos{Line: f.NameLine, Col: f.NameCol},
				Type: fieldType,
			}
		}
	}
	var sites []patternSite
	for _, f := range n.Fields {
		if f.Binding != "" {
			sites = append(sites, patternSite{f.Binding, Pos{Line: structPatternFieldBindingLine(f, n.Line), Col: f.BindingCol}})
		}
	}
	c.notePatternBindings(sites, n.Value)

	return TypeUnit
}

// checkMapPatternEntries type-checks one map pattern's entries: the KEY as an
// ordinary expression, then the value PATTERN against the map's V.
//
// # The key was never checked at all, and the map LITERAL path is the control
//
// Every one of the six places that walked `[]ast.MapPatternEntry` — this
// function's caller and five arms of checkPattern's *ast.MapPattern case —
// visited `entry.Pattern` and skipped `entry.Key`. The builder DOES walk the
// key (builder.go, the MapPattern arm) so names inside it resolve and
// unused-binding analysis sees it, which is why the omission never presented as
// an unknown-identifier bug and stayed invisible for the checker's whole life.
//
// MEASURED, as a paired positive/negative rather than an absence:
// `{Int.to_string(1, 2, 3) => "x"}` — a map LITERAL — is a front-end error
// ("expected 1 arguments, got 3"), because checkMapLit calls
// checkNodeExpecting on its keys. The SAME expression as a map PATTERN key,
// `case m { {Int.to_string(1, 2, 3) => v} -> v }`, was ACCEPTED. One expression,
// two answers, and the literal path is the one that is right. So this is a
// known-present check that was simply not wired to a second position, not a
// check nobody had written.
//
// # Why NO expected type, which is the whole design and is forced by the corpus
//
// The obvious spelling is checkNodeExpecting(entry.Key, mt.Key), matching the
// literal path exactly. It is WRONG here, and 08-pattern-matching/
// map_patterns_test.nomi:108-121 is the counterexample: it writes
// `{Color.Blue => v}` against a `Map<Bool, String>` and asserts the arm does
// NOT match, falling through to `_` with the value "fallthrough". A map
// pattern's key is an EXPRESSION evaluated against the map and then looked up,
// so a key of an unrelated type is not a type error — it is a guaranteed miss,
// and Nomi's structurally-keyed Map makes that meaningful rather than
// accidental. Imposing mt.Key would turn that passing program into a front-end
// error.
//
// So the key is checked the way a standalone expression is checked: for its own
// internal consistency, and to record the types the checker solves inside it.
// It is an ordinary expression position at run time too: the key expression
// is evaluated, and its own faults propagate, before the lookup, so a key can
// legally have any type and can still trap on its own terms.
//
// # The consequence that motivated finding it
//
// A prelude constructor in key position — `{Some(2) => v}` — records no
// instantiated signature when nothing checks it, so `Symbol.CallType` is nil
// and internal/irbuild's preludeArgs refuses it under `generic enum over a type
// parameter | Maybe.Some at Maybe<T>`. That reads as a missing SOURCE of type
// arguments and is not: `2` determines `Maybe<Int>` with no expected type, no
// substitution frame and no dictionary, exactly as `x = Some(2)` already does.
// It is one unvisited position, and it is the same shape as the divergence
// inferred.go's TypeParam_ arm was added for — one type resolved concretely
// through one door and abstractly through another.
func (c *checker) checkMapPatternEntries(entries []ast.MapPatternEntry, valTy Type) {
	for _, entry := range entries {
		if entry.Key != nil {
			c.checkNode(entry.Key)
		}
		c.checkPattern(entry.Pattern, valTy)
	}
}

// mapPatternSites is every name a map pattern's entries bind.
func mapPatternSites(entries []ast.MapPatternEntry) []patternSite {
	var out []patternSite
	for _, e := range entries {
		out = append(out, patternSites(e.Pattern)...)
	}
	return out
}

func (c *checker) checkMapDestructure(n *ast.MapDestructure) Type {
	valTy := c.checkNode(n.Value)

	if valTy == nil {
		return TypeUnit
	}

	mt, ok := valTy.(*MapType)
	if !ok {
		c.addError(n.Line, n.Col, fmt.Sprintf("map destructure requires a map type, got %s", valTy))
		return TypeUnit
	}

	// The two early returns above deliberately still do not descend, which is
	// pre-existing: they report one error about the destructured VALUE and a
	// second error from inside a key would be noise about a construct whose
	// type is already wrong. Only the loop that already ran is rerouted.
	c.checkMapPatternEntries(n.Entries, mt.Val)
	c.notePatternBindings(mapPatternSites(n.Entries), n.Value)

	return TypeUnit
}

func (c *checker) checkPatternDestructure(n *ast.PatternDestructure) Type {
	valTy := c.checkNode(n.Value)
	c.checkPattern(n.Pattern, valTy)
	c.notePatternBindings(patternSites(n.Pattern), n.Value)
	c.registerAssertionHoverAt(n.AssertLine, n.AssertCol, n, "assert", valTy, TypeUnit, TypeUnit)
	if c.tryUnwinds != nil && c.tryBoundary != "lambda" {
		*c.tryUnwinds = append(*c.tryUnwinds, tryUnwind{
			Flavour: "Result", Err: c.assertionFailureType(), Assertion: true,
			Line: n.AssertLine, Col: n.AssertCol,
		})
	} else {
		c.checkAssertionBoundaryAt(n.Line, n.Col, "assert")
	}
	return TypeUnit
}

// refutableBindingMessage is the error for a binding whose pattern can fail
// to match a value of type t and that has no `else`.
func refutableBindingMessage(t Type) string {
	return fmt.Sprintf("this pattern can fail to match a value of type %s; "+
		"add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr`", t)
}

// tryUnwrapHint is the help on a refutable binding `Ok(x) = r` or
// `Some(x) = m`, whose author usually wants `try`: it unwraps the value and
// returns early on `Err` or `None`. binding is the name the payload binds.
// It is empty for any other variant or enum. A pipe gets the `|> try` stage.
func tryUnwrapHint(et *EnumType, variant, binding string, value ast.Node) string {
	var missed, other string
	switch {
	case et.Name == "Result" && variant == "Ok":
		missed, other = "an `Err`", "`Err`"
	case et.Name == "Maybe" && variant == "Some":
		missed, other = "`None`", "`None`"
	default:
		return ""
	}
	fix := fmt.Sprintf("write `%s = try ...`", binding)
	if b, ok := ungroupExpr(value).(*ast.Binary); ok && b.Op == "|>" {
		fix = fmt.Sprintf("write `%s = ...` and end the pipe with `|> try`", binding)
	}
	return fmt.Sprintf("`%s(%s)` does not match %s. To unwrap the `%s` and return early on %s, %s",
		variant, binding, missed, et.Name, other, fix)
}

// variantNameOfDotted is the last segment of a written name:
// `Maybe.Some` is "Some".
func variantNameOfDotted(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// noFallbackPayloadMessage is the error for an `else` path that produces a
// value when the binding's pattern has no payload the value could stand in for.
const noFallbackPayloadMessage = "this pattern has no single payload a fallback could stand in for; " +
	"every path through `else` must return, break or continue"

// checkPatternBinding checks `Pattern = value [else { ... }]`.
//
// Without `else` the pattern must always match. With one it must be able to
// fail, and every path through the else either leaves (its type is
// Infallible: return, break, continue, or a branch whose paths all do) or
// produces a fallback. A fallback is allowed only when the pattern is one
// variant with a payload whose own pattern always matches (`Some(email)`,
// `Ok((w, h))`, `.Valid{addr, score}`): the fallback stands in for that
// payload, and the inner pattern binds from it as it would on a match. In
// the arms form the arms match the value that failed, and the binding's
// pattern plus the arms must be exhaustive by case's rules.
func (c *checker) checkPatternBinding(n *ast.PatternBinding) Type {
	valTy := c.checkNode(n.Value)
	c.checkPattern(n.Pattern, valTy)
	// Checked once the else is: a fallback (`Some(z) = None else { 0 }`)
	// is part of the binding and determines the payload's type.
	defer c.notePatternBindings(patternSites(n.Pattern), n.Value)
	irrefutable := valTy == nil || c.patternsExhaustiveOver(valTy, []ast.Node{n.Pattern})
	if n.Else == nil {
		if !irrefutable {
			e := TypeError{Line: n.Line, Col: n.Col, Message: refutableBindingMessage(valTy)}
			if p, ok := n.Pattern.(*ast.EnumPattern); ok && p.Binding != "" {
				if et, ok := valTy.(*EnumType); ok {
					if hint := tryUnwrapHint(et, c.variantNameOfPattern(p), p.Binding, n.Value); hint != "" {
						e.Hints = []string{hint}
					}
				}
			}
			c.report(e)
		}
		return TypeUnit
	}
	if irrefutable && valTy != nil {
		c.addError(n.Else.Line, n.Else.Col, "this pattern always matches; remove the else")
	}
	payloadTy, variant, canFallback := c.bindingFallbackPayload(n.Pattern, valTy)
	var expected Type
	if canFallback {
		expected = payloadTy
	}
	// checkElsePath checks one path's type: Infallible leaves; anything
	// else is a fallback for the pattern's payload.
	checkElsePath := func(ty Type, line, col int) {
		if ty == nil || isInfallibleLike(ty) {
			return
		}
		if !canFallback {
			c.addError(line, col, noFallbackPayloadMessage)
			return
		}
		_, _, down := embedsDowncast(payloadTy, ty)
		if err := c.unify(payloadTy, ty, nil); down || (err != nil && !TypesEqual(payloadTy, ty)) {
			c.addMismatch(line, col, payloadTy, ty, c.typef(
				"the fallback stands in for %s's payload of type %s, got %s", variant, payloadTy, ty))
		}
	}
	if block := n.Else.Block; block != nil {
		ty := c.checkBlockExpecting(block, expected)
		line, col := block.Line, block.Col
		if len(block.Stmts) > 0 {
			last := block.Stmts[len(block.Stmts)-1]
			if es, ok := last.(*ast.ExprStmt); ok {
				line, col = exprStartLineCol(es.Expr)
			} else {
				line, col = nodeLineCol(last)
			}
		}
		checkElsePath(ty, line, col)
		return TypeUnit
	}
	patterns := []ast.Node{n.Pattern}
	for _, arm := range n.Else.Arms {
		if arm.Guard == nil && arm.Pattern != nil {
			patterns = append(patterns, arm.Pattern)
		}
	}
	c.checkPatternCoverage(n.Else.Line, n.Else.Col, nil, "`else`", patterns, valTy)
	for _, arm := range n.Else.Arms {
		if arm.Pattern != nil && valTy != nil {
			c.checkPattern(arm.Pattern, valTy)
			c.notePatternBindings(patternSites(arm.Pattern), n.Value)
		}
		if arm.Guard != nil {
			c.checkCondition(arm.Guard, "`when` guard")
		}
		ty := c.checkNodeExpecting(arm.Body, expected)
		line, col := arm.Line, arm.Col
		if arm.Body != nil {
			line, col = exprStartLineCol(arm.Body)
		}
		checkElsePath(ty, line, col)
	}
	return TypeUnit
}

// bindingFallbackPayload reports the type a binding's `else` may fall back
// to: the payload of the one variant the pattern names, when the pattern's
// own payload pattern always matches. variant is that variant's name.
//
// `Some(email)` over Maybe<String> answers String; `Ok((w, h))` answers the
// tuple; `.Valid{addr, score}` and `.Valid(v)` over a struct variant answer
// the anonymous struct of its fields. A pattern with no single payload to
// stand in for (`[first, ..rest]`, `Ok(Some(x))`, a literal) answers false.
func (c *checker) bindingFallbackPayload(pattern ast.Node, t Type) (Type, string, bool) {
	et, ok := t.(*EnumType)
	if !ok {
		return nil, "", false
	}
	var name string
	var inner ast.Node
	// An `as` around the whole pattern (`Some(n) as m`) binds the whole
	// value, which a fallback for the payload cannot supply, so it takes the
	// default arm: no fallback.
	switch p := pattern.(type) {
	case *ast.EnumPattern:
		if p.Payload == nil && p.Binding == "" {
			return nil, "", false
		}
		name, inner = c.variantNameOfPattern(p), payloadOfVariantPattern(p)
	case *ast.StructPattern:
		if p.TypeName == nil {
			return nil, "", false
		}
		fields := *p
		fields.TypeName = nil
		name, inner = variantNameOf(p.TypeName), &fields
	default:
		return nil, "", false
	}
	v := findVariant(et, name)
	if v == nil {
		return nil, "", false
	}
	subs := enumTypeArgSubs(et)
	var payload Type
	switch {
	case v.DataType != nil:
		payload = v.DataType
		if subs != nil {
			payload = Substitute(payload, subs)
		}
	case len(v.Fields) > 0:
		fields := make([]FieldDef, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = f
			if subs != nil && f.Type != nil {
				fields[i].Type = Substitute(f.Type, subs)
			}
		}
		payload = &AnonStructType{Fields: fields}
	default:
		return nil, "", false
	}
	if !c.patternsExhaustiveOver(payload, []ast.Node{inner}) {
		return nil, "", false
	}
	return payload, name, true
}

func (c *checker) checkDistinctDestructure(n *ast.DistinctDestructure) Type {
	if n.Binding != nil {
		c.validateBindingLikeName(n.Binding.Name, n.Binding.Line, n.Binding.Col, "binding name")
	}
	valTy := c.checkNode(n.Value)

	if valTy == nil {
		return TypeUnit
	}

	dt, ok := valTy.(*DistinctType)
	if !ok {
		if et, isEnum := valTy.(*EnumType); isEnum && len(et.Variants) > 1 {
			// `Some(x) = maybe`: a variant pattern, which can fail.
			binding := "_"
			if n.Binding != nil {
				binding = n.Binding.Name
			}
			e := TypeError{Line: n.Line, Col: n.Col, Message: refutableBindingMessage(valTy)}
			if hint := tryUnwrapHint(et, variantNameOfDotted(n.TypeName), binding, n.Value); hint != "" {
				e.Hints = []string{hint}
			}
			c.report(e)
			return TypeUnit
		}
		c.addError(n.Line, n.Col, fmt.Sprintf("distinct type destructure requires a distinct type, got %s", valTy))
		return TypeUnit
	}

	if !distinctTypeNameMatches(n.TypeName, dt.Name) {
		c.addError(n.Line, n.Col, fmt.Sprintf("distinct type destructure expected %s, got %s", n.TypeName, dt.Name))
		return TypeUnit
	}

	if n.Binding != nil {
		sym := c.fa.Definitions[Pos{Line: n.Binding.Line, Col: n.Binding.Col}]
		if sym != nil && dt.Inner != nil {
			sym.Type = dt.Inner
		}
		c.notePatternBindings([]patternSite{{n.Binding.Name, Pos{Line: n.Binding.Line, Col: n.Binding.Col}}}, n.Value)
	}

	return TypeUnit
}

func distinctTypeNameMatches(patternName, actualName string) bool {
	return patternName == actualName ||
		strings.HasSuffix(actualName, "."+patternName) ||
		strings.HasSuffix(patternName, "."+actualName)
}

func (c *checker) checkIf(n *ast.If, expected Type) Type {
	if n.Cond == nil {
		c.addError(n.Line, n.Col, "`if` as a pipe stage requires a piped Bool condition")
		return TypeUnit
	}
	condTy := c.checkNode(n.Cond)
	if n.CondPattern != nil {
		c.checkPattern(n.CondPattern, condTy)
		c.notePatternBindings(patternSites(n.CondPattern), n.Cond)
		if condTy != nil && c.patternsExhaustiveOver(condTy, []ast.Node{n.CondPattern}) {
			c.reportIrrefutableIfPattern(n, condTy)
		}
		narrowSym, narrowTy := c.narrowingFor(n.Cond, condTy, n.CondPattern)
		return c.checkIfBranches(n, condTy, expected, true, narrowSym, narrowTy)
	}
	return c.checkIfWithConditionType(n, condTy, expected)
}

// reportIrrefutableIfPattern rejects `if Pattern = value` whose pattern
// matches every value of the value's type: the `if` tests nothing, its first
// branch always runs and an `else` never does. A bare name is the common
// case, and is usually `if x = 0` written for `if x == 0`; when a binding of
// that name and the value's type is already in scope, the hint says so.
func (c *checker) reportIrrefutableIfPattern(n *ast.If, condTy Type) {
	line, col := nodePos(n.CondPattern)
	if col == 0 {
		line, col = n.Line, n.Col
	}
	e := TypeError{Line: line, Col: col, Message: fmt.Sprintf(
		"this pattern always matches a value of type %s, so the `if` has nothing to test; bind it on its own line with `pattern = value`",
		condTy)}
	if p, ok := n.CondPattern.(*ast.IdentPattern); ok {
		if outer := c.outerValueNamed(n, p); outer != nil && outer.Type != nil && TypesEqual(outer.Type, condTy) {
			e.Hints = []string{fmt.Sprintf(
				"`if %s = ...` binds a new `%s`; to compare the existing `%s` with the value, write `if %s == ...`",
				p.Name, p.Name, p.Name, p.Name)}
		}
	}
	c.report(e)
}

// outerValueNamed is the parameter, binding or `once` that p's name refers
// to at n, other than the one p itself binds, or nil.
func (c *checker) outerValueNamed(n *ast.If, p *ast.IdentPattern) *Symbol {
	own := Pos{Line: p.Line, Col: p.Col}
	for scope := c.fa.ScopeAt(Pos{Line: n.Line, Col: n.Col}); scope != nil; scope = scope.Parent {
		sym := scope.LookupLocal(p.Name)
		if sym == nil || sym.Pos == own {
			continue
		}
		before := sym.Pos.Line < n.Line || (sym.Pos.Line == n.Line && sym.Pos.Col < n.Col)
		switch sym.Kind {
		case SymbolParam, SymbolBinding:
			if before {
				return sym
			}
		case SymbolOnce:
			return sym
		}
		return nil
	}
	return nil
}

func (c *checker) checkIfWithConditionType(n *ast.If, condTy Type, expected Type) Type {
	if condTy != nil && !TypesEqual(condTy, TypeBool) {
		c.addError(n.Line, 1, fmt.Sprintf(
			"if condition must be Bool, got %s", condTy))
	}
	return c.checkIfBranches(n, condTy, expected, false, nil, nil)
}

// narrowSym and narrowTy are the name the first branch narrows and its type
// there (narrowing.go), or nil.
func (c *checker) checkIfBranches(n *ast.If, condTy Type, expected Type, patternInput bool, narrowSym *Symbol, narrowTy Type) Type {
	checkThen := func(want Type) (ty Type) {
		c.withNarrowing(narrowSym, narrowTy, func() { ty = c.checkBlockExpecting(n.Then, want) })
		return ty
	}
	if n.Else == nil {
		thenTy := checkThen(TypeUnit)
		if thenTy != nil && !isUnitLike(thenTy) && !isInfallibleLike(thenTy) {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"if without else returns Unit, so then branch result of type %s is ignored — bind it to `_` inside the branch if intentional, or add an else branch to return a value",
				thenTy))
		}
		c.registerControlFlowHover(n.Line, n.Col, "if", condTy, TypeUnit, true, patternInput)
		return TypeUnit
	}

	// Push `expected` into each branch so a generic constructor in a branch tail
	// (`if … { Ok(.Quit) } else { … }`) resolves its dot-leading variant.
	thenTy := checkThen(expected)

	var elseTy Type
	switch e := n.Else.(type) {
	case *ast.Block:
		elseTy = c.checkBlockExpecting(e, expected)
	case *ast.If:
		elseTy = c.checkIf(e, expected)
	default:
		elseTy = c.checkNodeExpecting(n.Else, expected)
	}

	// If one branch diverges (Infallible), use the other branch's type.
	if thenTy == TypeInfallible {
		c.settleTodoTail(n.Then, elseTy)
		return elseTy
	}
	if elseTy == TypeInfallible {
		c.settleTodoTail(n.Else, thenTy)
		return thenTy
	}

	if thenTy != nil && elseTy != nil {
		// Try unify first so fresh TypeVars in either branch (e.g. `None`
		// widening to `Maybe<?α>`) bind to the concrete type from the
		// other branch. Fall back to TypesEqual's leniency around
		// unparameterized struct/enum names to avoid regressing
		// pre-existing stdlib shapes.
		if err := c.unify(thenTy, elseTy, nil); err != nil {
			if !TypesEqual(thenTy, elseTy) {
				c.addError(n.Line, 1, c.typef(
					"if/else branch type mismatch: then is %s, else is %s", thenTy, elseTy))
			}
		} else {
			thenTy = branchJoin(thenTy, elseTy)
		}
	}
	c.registerControlFlowHover(n.Line, n.Col, "if", condTy, thenTy, true, patternInput)
	return thenTy
}

// branchJoin answers the type of an `if` or `case` whose branches so far have
// type have and whose next branch has type next, the two having unified. When
// one is an interface the other implements (a `List<Int>` branch beside an
// `Iter<Int>` one, as a generic call does when its expected type is the
// interface), the expression's type is the interface: the concrete branch's
// value enters it, and the interface branch's value cannot become the
// concrete type. The same holds for an enum beside a type it embeds: the
// expression's type is the enum (embedsJoin).
func branchJoin(have, next Type) Type {
	_, haveIface := resolveTV(have).(*InterfaceType)
	_, nextIface := resolveTV(next).(*InterfaceType)
	if nextIface && !haveIface {
		return next
	}
	return embedsJoin(have, next)
}

// registerCodepointHover records the hover marker for a codepoint literal,
// `'a': Codepoint`, unless an argument-slot hint already holds the position.
func (c *checker) registerCodepointHover(n *ast.CodepointLit) {
	pos := Pos{Line: n.Line, Col: n.Col}
	if _, taken := c.fa.References[pos]; taken {
		return
	}
	c.fa.References[pos] = &Symbol{
		Name: "'" + n.Lexeme + "'",
		Kind: SymbolLiteral,
		Pos:  pos,
		Type: TypeCodepoint,
		Span: len(n.Lexeme) + 2,
	}
}

func (c *checker) registerControlFlowHover(line, col int, keyword string, inputTy, outputTy Type, hasInput bool, patternInput bool) {
	if line <= 0 {
		return
	}
	c.fa.References[Pos{Line: line, Col: col}] = &Symbol{
		Name: keyword,
		Kind: SymbolControlFlow,
		Pos:  Pos{Line: line, Col: col},
		Span: len(keyword),
		ControlFlow: &ControlFlowInfo{
			Keyword:      keyword,
			InputTy:      inputTy,
			OutputTy:     outputTy,
			HasInput:     hasInput,
			PatternInput: patternInput,
		},
	}
}

func (c *checker) checkFieldAccess(n *ast.FieldAccess) Type {
	fieldPos := Pos{Line: n.Field.Line, Col: n.Field.Col}

	if id, ok := n.Object.(*ast.Ident); ok && id.Name == "self" {
		c.addError(id.Line, id.Col, "`self` is an interface type placeholder, not an expression qualifier; use a bare same-owner call or qualify with the concrete type")
		return nil
	}

	// `MyApp.logger`: a field of an application type is an application-field
	// read, never an owner-qualified member (checkAppTypeMembers).
	if read, ok := appReadOf(c.fa, n); ok {
		return c.checkAppFieldRead(n, read)
	}

	// A dotted type name is one name in expression position too. The
	// qualifier in `Probe.Reading.Steady` is the whole `Probe.Reading`, so
	// resolve it as a name before treating it as a traversal — otherwise the
	// leading segment gets looked up on its own, and an importer that bound
	// `Probe.Reading` never bound `Probe`.
	dottedOwner, dottedSym := c.dottedTypeQualifier(n.Object)

	// Members of a dotted owner — its variants, and the functions it
	// declares — reach the checker through the References table, which the
	// builder fills for the qualifiers it saw declared. A name that arrived
	// by import has no entry, and neither does a variant of a dotted enum in
	// any file. Record it here so Step 2 below resolves both the same way it
	// resolves `Maybe.Some`.
	if dottedSym != nil {
		if member := dottedTypeMember(dottedSym, n.Field.Name); member != nil {
			if _, seen := c.fa.References[fieldPos]; !seen {
				c.fa.References[fieldPos] = member
			}
		}
	}

	var objTy Type
	if dottedOwner != "" && c.qualifierNeedsWholeName(n.Object) {
		// Checking the object would walk into segments that are spelling
		// rather than structure. The name is already resolved; record it so
		// hover and go-to-def see the declaration from either half.
		c.recordDottedQualifier(n.Object, dottedSym)
	} else {
		prevQualifier := c.qualifierNode
		c.qualifierNode = n.Object
		objTy = c.checkNode(n.Object)
		c.qualifierNode = prevQualifier
	}

	// A variant of an enum named through a whole-file import
	// (`tree.Tree.Leaf`). The qualifier is not one name in this file's scope,
	// so the block above left the variant unrecorded, and the registry
	// fallback below would type it as the declaration itself, its type
	// parameters uninstantiated. Record the variant so Step 2 types it as it
	// types `Tree.Leaf`.
	if owner, isFA := n.Object.(*ast.FieldAccess); isFA && dottedSym == nil && owner.Field != nil {
		if _, seen := c.fa.References[fieldPos]; !seen && c.namespaceQualifiedTypeName(owner) != "" {
			ownerSym := c.fa.References[Pos{Line: owner.Field.Line, Col: owner.Field.Col}]
			if member := dottedTypeMember(ownerSym, n.Field.Name); member != nil && member.Kind == SymbolEnumVariant {
				c.fa.References[fieldPos] = member
			}
		}
	}

	// Step 1: type-qualified method access (`Type.method`).
	// When the object NAMES a type and the field is one of that type's
	// impl-block methods (inherent or
	// interface-impl), resolve to the impl method's symbol and RECORD the
	// reference at the method-name position (powers hover + go-to-def).
	//
	// This runs BEFORE the value-field resolution below: for an OPAQUE type,
	// fieldTypeFromObject would otherwise reject a method name as an
	// inaccessible field (`Set.insert` on the opaque `Set`). Enum-variant
	// access (`Maybe.Some`, `Direction.North`) is not a type method, so it
	// falls through to Step 2's References-table dispatch.
	var tqTypeName string
	switch o := n.Object.(type) {
	case *ast.TypeIdent:
		// A variant spelled bare (`None`, `Some`, an imported variant) is a
		// value of its enum, not a type qualifier: `None.foo` is a field
		// access on an `Option` and fails as one below.
		if !c.typeIdentNamesVariant(o) {
			tqTypeName = o.Name
		}
	case *ast.FieldAccess:
		tqTypeName = c.namespaceQualifiedTypeName(o)
		if tqTypeName == "" {
			tqTypeName = dottedOwner
		}
	}
	// A file-qualified owner (`calendar.Date`) names WHICH declaration,
	// but the member tables — type-once bindings, type methods, the
	// interface-provider index — are keyed by base name. Try the
	// qualified spelling first so a shadowed type still resolves to the
	// right declaration, then the base name for the tables that only
	// ever knew it by that.
	tqBaseName := tqTypeName
	if i := strings.LastIndex(tqBaseName, "."); i >= 0 {
		tqBaseName = tqBaseName[i+1:]
	}
	if tqTypeName != "" {
		// Selective import aliases name the same nominal receiver. Restrict
		// this fallback to an imported type symbol: generated module qualifiers
		// also use TypeIdent nodes and must retain their module spelling.
		if ident, ok := n.Object.(*ast.TypeIdent); ok {
			if alias := c.fa.ModuleScope.Lookup(ident.Name); alias != nil && alias.Resolved != nil {
				real := alias
				for real.Resolved != nil {
					real = real.Resolved
				}
				switch real.Kind {
				case SymbolStruct, SymbolType, SymbolEnum:
					if canonical := concreteTypeName(real.Type); canonical != "" {
						tqBaseName = canonical
					}
				}
			}
		}
		sym := c.lookupTypeOnceSymbol(tqTypeName, n.Field.Name)
		if sym == nil && tqBaseName != tqTypeName {
			sym = c.lookupTypeOnceSymbol(tqBaseName, n.Field.Name)
		}
		if sym == nil {
			sym = c.ownerOnceSymbol(n.Object, n.Field.Name)
		}
		if sym != nil {
			c.fa.References[fieldPos] = sym
			c.ensureOnceType(sym)
			if sym.Type != nil {
				return sym.Type
			}
		}
		// Spec §13, *Function Name Collisions*: a method name a type implements under two interfaces can't be
		// disambiguated by the type qualifier — interface-qualification is
		// required. (The type's own impl methods collapse into one TypeMethods
		// slot, so the count must come from the interface set, not the slot.)
		providers := c.interfacesWritingMethod(tqBaseName, n.Field.Name)
		if len(providers) >= 2 {
			quoted := make([]string, len(providers))
			for i, p := range providers {
				quoted[i] = "'" + p + "'"
			}
			c.addError(fieldPos.Line, fieldPos.Col, fmt.Sprintf(
				"type '%s' impl %s, which each declare '%s' — type-qualified '%s.%s(...)' is ambiguous; qualify by interface, e.g. '%s.%s(...)'",
				tqTypeName, strings.Join(quoted, " and "), n.Field.Name,
				tqTypeName, n.Field.Name, providers[0], n.Field.Name))
			if it, ok := c.reg.Lookup(providers[0]).(*InterfaceType); ok {
				if ft := interfaceMethodFuncType(it, n.Field.Name); ft != nil {
					return ft
				}
			}
			return nil
		}
		// One origin for both spellings: the qualified form and the base form
		// name the SAME declaration (`calendar.Error` and, under a selective
		// import, `Error`), and only the registry resolves both.
		recvOrigin := c.receiverOriginForReceiver(n.Object, tqTypeName, tqBaseName)
		sym = c.lookupTypeMethodSymbol(tqTypeName, n.Field.Name, recvOrigin)
		if sym == nil && tqBaseName != tqTypeName {
			sym = c.lookupTypeMethodSymbol(tqBaseName, n.Field.Name, recvOrigin)
		}
		if sym != nil {
			c.fa.References[fieldPos] = sym
			if sym.Type != nil {
				return sym.Type
			}
		}
		// Spec §13, default implementations: a method the type doesn't write itself but inherits as a
		// default from the single interface that declares it is reachable
		// type-qualified, typed from that interface's signature.
		if len(providers) == 1 {
			if it, ok := c.reg.Lookup(providers[0]).(*InterfaceType); ok {
				if ft := interfaceMethodFuncType(it, n.Field.Name); ft != nil {
					return ft
				}
			}
		}
	}

	// Step 1.5: field-based resolution against the object's type. Wins for
	// struct/enum field access. When the object is a module or enum reference
	// (no value-type, no fields), this is a no-op and Step 2 dispatches via
	// the References table.
	if objTy != nil && tqTypeName == "" {
		if ty, ok := fieldTypeFromObject(c, objTy, n.Field.Name, fieldPos); ok {
			return ty
		}
	}

	// When the object is an interface type, prefer the interface's declared
	// method signature over any single impl's concrete type. Picking one
	// impl's signature mis-binds the call's scrutinee/return types (e.g.
	// `Iter.each_while(x, y)` must take `y: (T) -> Bool`, not whichever impl
	// the builder happened to register), and blowing up to a proper dispatch
	// mechanism isn't needed here — the interface's own MethodSig is enough
	// to type the call.
	//
	// Only an interface NAME qualifies a function (`Display.to_string`,
	// tqTypeName set). A VALUE of interface type (a parameter, binding or
	// field) has no members besides fields, and an interface has no fields:
	// `h.name` falls through to the terminal's "no field" error, whose hint
	// names the qualified call.
	if ifaceTy, ok := interfaceObjectType(c, n.Object, objTy); ok && tqTypeName != "" {
		if ft := interfaceMethodFuncType(ifaceTy, n.Field.Name); ft != nil {
			return ft
		}
		// Interface exists but method isn't found on it — fall through to
		// Step 2's References-table dispatch, which at worst returns nil
		// and lets checkCall validate arguments permissively.
	}

	// Step 2: dispatch qualified-name access via the References table.
	// This is the primary path for module-qualified calls (`io.print`,
	// `Map.empty`) and enum-qualified variant access (`Maybe.None`,
	// `Direction.North`) — the builder pre-populates References[fieldPos]
	// with the resolved member symbol whenever `n.Object` is a known
	// module or enum. Under the collision-error rule, an identifier
	// can't be both a local value-binding and an imported module, so
	// this path is unambiguous: if Step 1's field access didn't apply,
	// the object is a module/enum reference and References gives the
	// answer.
	if sym, ok := c.fa.References[fieldPos]; ok {
		real := sym
		for real.Resolved != nil {
			real = real.Resolved
		}
		c.ensureOnceType(real)
	}
	if sym, ok := c.fa.References[fieldPos]; ok && sym.Type != nil {
		c.checkModuleMemberVisibility(n.Object, n.Field, sym)
		// Nullary variant qualified access (`Maybe.None`, `Color.Red`)
		// carries the enum's original TypeParam_ pointers in its Type. At
		// each use site, instantiate those callee-scope params as fresh
		// TypeVars so unrelated `case` branches that share the same
		// nullary variant don't conflict (`Some(v) -> Maybe.Some(v) | None
		// -> Maybe.None` would otherwise read as Maybe<U> | Maybe<T>).
		// Data-carrying variants (FuncType) are left raw — checkCall
		// routes them through checkGenericCall, which handles per-call
		// substitution itself; instantiating here would strip the
		// TypeParam_ markers checkGenericCall keys on. checkTypeIdent
		// applies the same EnumType-only rule on the bare-name path.
		real := sym
		if real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind == SymbolEnumVariant {
			// Opaque-types: qualified bare-variant access (`Status.Active`)
			// outside the owning module exposes the variant. Block it.
			// Mirrors the bare-name TypeIdent path in checkTypeIdent and
			// the call-site path in checkOpaqueEnumVariantCall.
			if et, ok := sym.Type.(*EnumType); ok && et != nil && et.Opaque {
				if c.fa == nil || c.fa.FilePath != et.OwningSourceFile {
					c.addError(fieldPos.Line, fieldPos.Col, fmt.Sprintf(
						"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", et.Name))
				}
			}
			if _, isEnum := sym.Type.(*EnumType); isEnum {
				inst := instantiateUnboundCalleeParams(sym.Type, c.fnTypeParams, c)
				if et, isInst := inst.(*EnumType); isInst && len(et.TypeArgs) > 0 {
					// Record this site's instantiation, so the IR builder
					// can build the value where no expected type names
					// the instance (a list element, a call argument).
					ref := *sym
					ref.VariantType = inst
					c.fa.References[fieldPos] = &ref
				}
				return inst
			}
		}
		return sym.Type
	}

	// A file API object names the file's root-level declarations and nothing
	// else. The builder records `file.member` against the imported file's
	// scope, so no reference means the file declares no such member: report it
	// here rather than fall through to the permissive `return nil`, which let
	// `io.bogus(1)` check clean and left the IR builder to decline the call.
	if tqTypeName == "" && c.reportMissingFileMember(n) {
		return nil
	}

	if tqTypeName != "" {
		if et, ok := c.reg.Lookup(tqTypeName).(*EnumType); ok && et != nil {
			if ty := qualifiedEnumVariantAccessType(et, n.Field.Name); ty != nil {
				return ty
			}
		}
	}

	// Fail-loud field-access guard. Reaching here means the field resolved to
	// no field (step 1.5), no method (step 1.6), and no module/enum member
	// (step 2). For a *resolved, concrete* receiver the field genuinely does
	// not exist — report it at compile time instead of returning a permissive
	// nil that traps at runtime ("struct '' has no field 'x'"). The guard is
	// structural: an unresolved inference var (`*TypeVar`), a bare type param
	// (`*TypeParam_`), or a nil object matches no case below and falls through
	// to `return nil` with NO error — "I can't resolve this field yet, but
	// that isn't a mistake" (keeps inferred-lambda field access working; bare
	// type-param field access is a separate increment).
	//
	// Type-qualified access (`Type.member`, tqTypeName set) that
	// resolved no type method (Step 1), interface method (Step 1.6), or member /
	// enum variant (Step 2) means the member does not exist on the type. Report
	// it — covering both a call callee (`List.empty()`) and a bare value access
	// (`Maybe.Bogus`). Scoped to a qualifier naming a real type, since an
	// undefined type already errored.
	//
	// A TYPE-PARAMETER qualifier is resolved here too, ahead of that scope. It
	// is not in the registry, and checkTypeParamQualifiedCall covers only a
	// CALL: that function runs from checkCall alone. Without this, a bare
	// `T.member` would fall through to `return nil` for ANY member name,
	// including one no type in the program declares, and the program would die
	// at run time on `unknown builtin 'T.member'`. It is resolved here for the
	// same reason as the concrete guard above: this terminal is the one point
	// that sees a call callee and a bare value, so one rule covers both and
	// neither double-reports.
	if tp, providers, ok := c.typeParamQualifiedMember(n); ok {
		if len(providers) == 1 {
			return typeParamQualifiedMethodFuncType(providers[0], n.Field.Name, tp)
		}
		c.reportTypeParamQualifiedMember(n, tp, providers)
		return nil
	}
	if tqTypeName != "" {
		if c.reg.Lookup(tqTypeName) != nil || c.reg.Lookup(tqBaseName) != nil {
			if st, isStruct := c.reg.Lookup(tqTypeName).(*StructType); isStruct {
				if _, isField := scopedField(st, n.Field.Name); isField {
					// `Settings.tag` names a field: it is an application
					// read once some entry boot returns Settings, and none
					// in this project does.
					c.addError(fieldPos.Line, fieldPos.Col, fmt.Sprintf(
						"`%s.%s` reads an application field only when an entry boot returns `%s`, and no `fn boot` in this project does; check the entry file that boots it",
						tqTypeName, n.Field.Name, tqTypeName))
					return nil
				}
			}
			c.report(TypeError{Line: fieldPos.Line, Col: fieldPos.Col, Message: fmt.Sprintf(
				"type '%s' has no member '%s'", tqTypeName, n.Field.Name)}.WithHint(didYouMean(n.Field.Name, c.typeMemberNames(tqTypeName))))
		}
		return nil
	}
	if tqTypeName == "" {
		// A value of interface type (an existential) has no fields: an
		// interface declares functions only.
		if it, ok := interfaceObjectType(c, n.Object, objTy); ok {
			hint := fmt.Sprintf(
				"an interface declares functions only; declare `fn %s(value: self): ...` in '%s' and call `%s.%s(value)`",
				n.Field.Name, it.Name, it.Name, n.Field.Name)
			if interfaceMethodFuncType(it, n.Field.Name) != nil {
				hint = fmt.Sprintf("`%s` declares `fn %s`; call it as %s",
					it.Name, n.Field.Name, qualifiedCallSpellings([]string{it.Name}, n.Field.Name, n.Object))
			}
			c.report(TypeError{Line: fieldPos.Line, Col: fieldPos.Col, Message: fmt.Sprintf(
				"interface '%s' has no field '%s'", it.Name, n.Field.Name)}.WithHint(hint))
			return nil
		}
		switch rt := resolveTypeVar(objTy).(type) {
		case *StructType:
			c.report(TypeError{Line: fieldPos.Line, Col: fieldPos.Col, Message: fmt.Sprintf(
				"struct '%s' has no field '%s'", rt.Name, n.Field.Name)}.WithHint(c.structFunctionNotFieldHint(rt, n.Field.Name, n.Object)))
		case *EnumType:
			c.addError(fieldPos.Line, fieldPos.Col, fmt.Sprintf(
				"enum '%s' has no field '%s' (match on its variants instead)", rt.Name, n.Field.Name))
		case *DistinctType:
			c.addError(fieldPos.Line, fieldPos.Col, fmt.Sprintf(
				"'%s' has no field '%s'", rt.Name, n.Field.Name))
		case *TypeParam_:
			// A type parameter has no fields, bounded or not: its bounds
			// declare functions only.
			c.report(TypeError{Line: fieldPos.Line, Col: fieldPos.Col, Message: fmt.Sprintf(
				"type parameter `%s` has no field '%s'", rt.Name_, n.Field.Name)}.WithHint(c.typeParamFunctionNotFieldHint(rt, n.Field.Name, n.Object)))
		case *AnonStructType, *TupleType, *PrimitiveType, *ListType, *MapType, *FuncType:
			c.addError(fieldPos.Line, fieldPos.Col, fmt.Sprintf(
				"%s has no field '%s'", rt, n.Field.Name))
		}
	}
	return nil
}

// typeIdentNamesVariant reports whether a capitalized name in expression
// position denotes an enum variant and no type of that name exists, so that
// `X.y` reads a field of the variant's value rather than a member of a type.
func (c *checker) typeIdentNamesVariant(id *ast.TypeIdent) bool {
	sym, ok := c.fa.References[Pos{Line: id.Line, Col: id.Col}]
	if !ok {
		return false
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Kind == SymbolEnumVariant && c.reg.Lookup(id.Name) == nil
}

// dottedTypeQualifier resolves an expression's object as a dotted type name —
// `Probe.Reading` in `Probe.Reading.Steady` — and returns that name with the
// symbol it denotes.
//
// The lookup is by the whole name, because that is the only name there is: a
// dotted declaration puts one symbol in scope and the dot in it is spelling.
// Anything that isn't a type resolves to "" and leaves the ordinary
// value-traversal path alone, so `config.server.port` is untouched.
func (c *checker) dottedTypeQualifier(obj ast.Node) (string, *Symbol) {
	fa, ok := obj.(*ast.FieldAccess)
	if !ok || c.fa == nil || c.fa.ModuleScope == nil {
		return "", nil
	}
	path := fieldAccessPath(fa)
	if len(path) < 2 {
		return "", nil
	}
	name := strings.Join(path, ".")
	sym := c.fa.ModuleScope.Lookup(name)
	if sym == nil {
		return "", nil
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	switch real.Kind {
	case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
		// The binding, not what it resolves to: naming the qualifier is a use
		// of the import that bound it, and that is judged by symbol identity.
		return name, sym
	}
	return "", nil
}

// qualifierNeedsWholeName reports whether checking the object as an expression
// would walk into a segment that is not a name on its own. True exactly when
// the References table doesn't already identify the qualifier — where it does,
// the existing path resolved the name and its halves already have their
// references recorded.
func (c *checker) qualifierNeedsWholeName(obj ast.Node) bool {
	fa, ok := obj.(*ast.FieldAccess)
	if !ok {
		return false
	}
	return c.namespaceQualifiedTypeName(fa) == ""
}

// qualifiedEnumVariantAccessType types a variant reached through a
// file-qualified enum path (`calendar.Error.InvalidFormat`). The qualifier only
// selects which declaration `et` is; the resulting type is that
// declaration, unrenamed. Identity lives in (Origin, Name), so a copy
// whose Name carried the qualifier would be a *different* type from the
// one the enum's own signatures name — `expected Error, got
// calendar.Error`.
func qualifiedEnumVariantAccessType(et *EnumType, variantName string) Type {
	if et == nil || variantName == "" {
		return nil
	}
	for _, variant := range et.Variants {
		if variant.Name != variantName {
			continue
		}
		if variant.DataType == nil {
			return et
		}
		return &FuncType{Params: []Type{variant.DataType}, Return: et}
	}
	return nil
}

// dottedTypeMember finds a member of a dotted owner by name — a variant of a
// dotted enum, or a function the declaration carries.
func dottedTypeMember(owner *Symbol, name string) *Symbol {
	if owner == nil {
		return nil
	}
	// An imported binding carries no members of its own; the declaration it
	// resolves to is where they live.
	real := owner
	if real.Resolved != nil {
		real = real.Resolved
	}
	if real.Members == nil {
		return nil
	}
	member, ok := real.Members[name]
	if !ok || member == nil {
		return nil
	}
	return member
}

// recordDottedQualifier points both halves of a dotted qualifier at the
// declaration, so hovering either one describes the name they spell together
// — the same answer the type position gives.
func (c *checker) recordDottedQualifier(obj ast.Node, sym *Symbol) {
	fa, ok := obj.(*ast.FieldAccess)
	if !ok || sym == nil || c.fa == nil {
		return
	}
	if fa.Field != nil {
		c.fa.References[Pos{Line: fa.Field.Line, Col: fa.Field.Col}] = sym
	}
	switch base := fa.Object.(type) {
	case *ast.TypeIdent:
		c.fa.References[Pos{Line: base.Line, Col: base.Col}] = sym
	case *ast.Ident:
		c.fa.References[Pos{Line: base.Line, Col: base.Col}] = sym
	}
}

func (c *checker) namespaceQualifiedTypeName(n *ast.FieldAccess) string {
	if c == nil || c.fa == nil || n == nil || n.Field == nil {
		return ""
	}
	pos := Pos{Line: n.Field.Line, Col: n.Field.Col}
	sym := c.fa.References[pos]
	if sym == nil {
		return ""
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	switch real.Kind {
	case SymbolStruct, SymbolEnum, SymbolType, SymbolTypeAlias, SymbolInterface:
		// A file-qualified path (`calendar.Date`) selects WHICH
		// declaration, which matters when a local type shadows the base
		// name. Return the dotted form when the registry knows it so
		// that selection survives; callers that key member tables by
		// base name fall back via typeMemberLookupNames.
		if strings.Contains(sym.Name, ".") {
			return sym.Name
		}
		if path := fieldAccessPath(n); len(path) >= 2 {
			qualified := strings.Join(path, ".")
			if c.reg != nil && c.reg.Lookup(qualified) != nil {
				return qualified
			}
		}
		return real.Name
	default:
		return ""
	}
}

func fieldAccessPath(n *ast.FieldAccess) []string {
	if n == nil || n.Field == nil {
		return nil
	}
	var prefix []string
	switch o := n.Object.(type) {
	case *ast.Ident:
		prefix = []string{o.Name}
	case *ast.TypeIdent:
		prefix = []string{o.Name}
	case *ast.FieldAccess:
		prefix = fieldAccessPath(o)
	default:
		return nil
	}
	if len(prefix) == 0 {
		return nil
	}
	return append(prefix, n.Field.Name)
}

// structSubs returns the TypeParam_ → TypeArg substitution map for a
// generic struct instantiation (e.g. Pair<Int, String> with TypeParamDefs
// [A, B] yields {A: Int, B: String}). Returns nil when the struct isn't
// generic or has no TypeArgs at this site.
func structSubs(st *StructType) map[*TypeParam_]Type {
	if len(st.TypeParamDefs) == 0 || len(st.TypeArgs) == 0 {
		return nil
	}
	subs := make(map[*TypeParam_]Type, len(st.TypeParamDefs))
	for i, def := range st.TypeParamDefs {
		if i < len(st.TypeArgs) {
			subs[def] = st.TypeArgs[i]
		}
	}
	return subs
}

// fieldTypeFromObject resolves X.Y as a direct field access on X's type, and
// (on success) also updates References[fieldPos] so hover/go-to-def land on
// the field symbol rather than any module export the builder may have
// optimistically recorded. Returns (nil, false) when the object type is not
// struct-like or has no matching field.
func fieldTypeFromObject(c *checker, objTy Type, fieldName string, fieldPos Pos) (Type, bool) {
	// A solved inference variable is its solution: `v.0` where v came back
	// from a generic call whose argument fixed it.
	switch ty := resolveTypeVar(objTy).(type) {
	case *StructType:
		ty = canonicalStructForFieldAccess(c, ty)
		// Opaque-types: field access on an opaque struct (inline-body
		// opaque) from outside its owning module exposes the field
		// representation. Block it; same-module field access is the
		// owner's right. Mirrors the destructuring boundary check.
		if ty.Opaque && (c.fa == nil || c.fa.FilePath != ty.OwningSourceFile) {
			c.addError(fieldPos.Line, fieldPos.Col, fmt.Sprintf(
				"cannot access field '%s' of opaque type '%s' outside its defining module — use an exported accessor",
				fieldName, ty.Name))
			return nil, true
		}
		// For generic structs (e.g. Pair<Int, String>), substitute the
		// instantiated TypeArgs into the declared field type so hover and
		// downstream checks see `Int` rather than the raw `A`.
		subs := structSubs(ty)
		for _, f := range ty.Fields {
			if f.Name == fieldName {
				resolvedTy := f.Type
				if subs != nil {
					resolvedTy = Substitute(resolvedTy, subs)
				}
				// Resolve the struct's defining Symbol. ModuleScope covers
				// top-level structs; nested structs (declared inside a fn
				// or block) aren't in ModuleScope, so fall back to scanning
				// Definitions for a SymbolStruct of the same name.
				structSym := lookupSymbolDeep(c.fa.ModuleScope, ty.Name)
				if structSym == nil {
					for _, defSym := range c.fa.Definitions {
						if defSym.Name == ty.Name && defSym.Kind == SymbolStruct {
							structSym = defSym
							break
						}
					}
				}
				if structSym != nil {
					real := structSym
					if real.Resolved != nil {
						real = real.Resolved
					}
					if fieldSym, ok := real.Members[f.Name]; ok {
						// Don't mutate the shared declared-symbol's Type
						// (that's the source of truth for the raw decl).
						// Stash the instantiated type on a fresh proxy so
						// hover at this Pos reads the concrete type.
						proxy := *fieldSym
						proxy.Type = resolvedTy
						c.fa.References[fieldPos] = &proxy
					}
				}
				if c.fa != nil {
					if _, ok := c.fa.References[fieldPos]; !ok {
						c.fa.References[fieldPos] = &Symbol{
							Name: f.Name,
							Kind: SymbolField,
							Pos:  fieldPos,
							Type: resolvedTy,
						}
					}
				}
				return resolvedTy, true
			}
		}
	case *AnonStructType:
		for _, f := range ty.Fields {
			if f.Name == fieldName {
				// The field the anonymous type's annotation declares, when
				// it is in this file; otherwise a field Symbol at the access
				// position, so hover still renders `x: Int`.
				if decl := c.anonFieldDecl(f); decl != nil {
					c.fa.References[fieldPos] = decl
					return f.Type, true
				}
				c.fa.References[fieldPos] = &Symbol{
					Name: f.Name,
					Kind: SymbolField,
					Pos:  fieldPos,
					Type: f.Type,
				}
				return f.Type, true
			}
		}
	case *EnumType:
		for _, v := range ty.Variants {
			for _, f := range v.Fields {
				if f.Name == fieldName {
					if variantSym := c.fa.ModuleScope.Lookup(v.Name); variantSym != nil {
						if variantSym.Resolved != nil {
							variantSym = variantSym.Resolved
						}
						if fieldSym, ok := variantSym.Members[f.Name]; ok {
							c.fa.References[fieldPos] = fieldSym
						}
					}
					return f.Type, true
				}
			}
			if v.DataType != nil {
				if st, ok := v.DataType.(*StructType); ok {
					for _, f := range st.Fields {
						if f.Name == fieldName {
							for _, defSym := range c.fa.Definitions {
								if defSym.Name == st.Name && defSym.Kind == SymbolStruct {
									if fieldSym, ok := defSym.Members[f.Name]; ok {
										c.fa.References[fieldPos] = fieldSym
									}
									break
								}
							}
							return f.Type, true
						}
					}
				}
			}
		}
	case *TupleType:
		// Tuple field access uses numeric "names" like "0", "1"; resolve
		// to the elem at that index so `pair.0` hovers as `0: <type>`.
		i, err := strconv.Atoi(fieldName)
		if err == nil && i >= 0 && i < len(ty.Elems) {
			c.fa.References[fieldPos] = &Symbol{
				Name: fieldName,
				Kind: SymbolField,
				Pos:  fieldPos,
				Type: ty.Elems[i],
			}
			return ty.Elems[i], true
		}
	}
	return nil, false
}

func canonicalStructForFieldAccess(c *checker, ty *StructType) *StructType {
	if c == nil || ty == nil || len(ty.Fields) > 0 {
		return ty
	}
	withTypeArgs := func(st *StructType) *StructType {
		if st == nil || len(st.Fields) == 0 {
			return ty
		}
		if len(ty.TypeArgs) == 0 {
			return st
		}
		copy := *st
		copy.TypeArgs = ty.TypeArgs
		return &copy
	}
	if c.reg != nil {
		if st, ok := c.reg.Lookup(ty.Name).(*StructType); ok {
			if resolved := withTypeArgs(st); len(resolved.Fields) > 0 {
				return resolved
			}
		}
	}
	if c.fa != nil && c.fa.ModuleScope != nil {
		if sym := lookupSymbolDeep(c.fa.ModuleScope, ty.Name); sym != nil {
			real := sym
			if real.Resolved != nil {
				real = real.Resolved
			}
			if st, ok := real.Type.(*StructType); ok {
				return withTypeArgs(st)
			}
		}
	}
	return ty
}

func (c *checker) checkTryOp(n *ast.TryOp) Type {
	if n.Expr == nil {
		c.addError(n.Line, n.Col, "`try` requires an expression outside a pipe")
		return nil
	}
	inputTy := c.checkNode(n.Expr)
	c.registerTryOpHover(n, inputTy)
	return c.unwrapTryResult(inputTy, n, n.Expr)
}

func (c *checker) checkTryOpExpecting(n *ast.TryOp, expected Type) Type {
	if n.Expr == nil || expected == nil {
		return c.checkTryOp(n)
	}
	inputTy := c.checkNodeExpecting(n.Expr, &EnumType{
		Name:     "Result",
		TypeArgs: []Type{expected, c.freshTypeVar()},
	})
	c.registerTryOpHover(n, inputTy)
	return c.unwrapTryResult(inputTy, n, n.Expr)
}

// registerTryOpHover stores a SymbolTryOp marker at the `try` keyword's
// position. n.Line / n.Col come from the `try` token in the parser, so the
// marker covers exactly the three-column `try`. The renderer uses InputTy
// to derive the success and Err / None branches and Boundary to label where
// the `try` unwinds to. Skipped when the input type wasn't resolved (the
// checker would already have reported the failure).
//
// Called from checkTryOp for the prefix form (`try r`) and from the pipe
// branch in checkBinary for the pipe-stage form (`x |> r() |> try`) — both
// paths must register, otherwise hover misses on one of the two spellings.
func (c *checker) registerTryOpHover(n *ast.TryOp, inputTy Type) {
	if n.Line <= 0 || inputTy == nil {
		return
	}
	c.fa.References[Pos{Line: n.Line, Col: n.Col}] = &Symbol{
		Name: "try",
		Kind: SymbolTryOp,
		Pos:  Pos{Line: n.Line, Col: n.Col},
		Span: 3,
		TryOp: &TryOpInfo{
			InputTy:  inputTy,
			Boundary: c.tryBoundary,
		},
	}
}

// TryOperandTypeNames is the closed set of enum names `try` accepts as an
// operand.
//
// Named rather than left inline in unwrapTryResult's condition because
// internal/irbuild carries the SAME fact on its own side — a `try` operand is a
// prelude enum whose spec sets `tryOperand` — and two encodings of one fact is
// this project's signature bug class. internal/irbuild's
// TestTryOperandSetsAgreeWithTheChecker holds the two sets equal in both
// directions, so a third admitted operand here fails loudly instead of being
// silently refused by the IR builder, and a builder flag set by mistake fails
// instead of lowering something this checker never sanctioned.
//
// It is the builder side that cannot be derived: `Fragment<T>` is also a
// two-variant prelude enum whose first variant carries one payload, so the
// builder's shape test stopped being able to tell a `try` operand from a
// non-operand the moment std/literals.Fragment got a representation.
var TryOperandTypeNames = []string{"Maybe", "Result"}

// isTryOperandName reports whether an enum name may be a `try` operand.
func isTryOperandName(name string) bool {
	for _, n := range TryOperandTypeNames {
		if n == name {
			return true
		}
	}
	return false
}

// unwrapTryResult applies one layer of `try` to ty: strips Result<T, E> or
// Maybe<T> to T. Used by checkTryOp and by the pipe-stage form
// (`x |> f(a) |> try`). Reports a diagnostic on `at` if ty is not
// Result/Maybe.
//
// operand is the expression `try` applies to, for the hint on a backtick
// typed literal, whose type is already the handler's success type.
func (c *checker) unwrapTryResult(ty Type, at, operand ast.Node) Type {
	if ty == nil {
		return nil
	}
	et, ok := ty.(*EnumType)
	if !ok || !isTryOperandName(et.Name) {
		line, col := nodeLineCol(at)
		e := TypeError{Line: line, Col: col, Message: fmt.Sprintf("try requires a Result or Maybe, got %s", ty)}
		if lit, raw := ungroupExpr(operand).(*ast.TaggedString); raw && lit.Raw {
			e.Hints = []string{fmt.Sprintf("a backtick typed literal is checked at compile time, so this one is already a %s: remove the `try`", ty)}
		}
		c.report(e)
		return nil
	}
	if len(et.TypeArgs) == 0 {
		return nil
	}
	if line, col := nodeLineCol(at); c.rejectExitlessExit("try", line, col) {
		return et.TypeArgs[0]
	}
	// A `try` contributes an early exit to the enclosing `try`-boundary. Record
	// it so an INFERRING boundary (concurrent block, lambda) can check its
	// result against what the site propagates and resolve its error side. The
	// FLAVOUR is recorded for every operand — a `try` on a Maybe carries no
	// error type but still constrains the boundary — and the error type only
	// for a Result.
	if c.tryUnwinds != nil {
		line, col := nodeLineCol(at)
		u := tryUnwind{Flavour: et.Name, Line: line, Col: col}
		if et.Name == "Result" && len(et.TypeArgs) >= 2 {
			u.Err = et.TypeArgs[1]
		}
		*c.tryUnwinds = append(*c.tryUnwinds, u)
	}
	c.checkTryBoundary(et, at)
	return et.TypeArgs[0]
}

// checkTryBoundary enforces spec §9's boundary rule at a `try` site whose
// boundary is a `fn` with a DECLARED result type: the propagated value must
// inhabit that declaration. Three ways it can fail, all reported here and all
// naming both sides:
//
//  1. the boundary is not a `Result` or a `Maybe` at all (`fn f(): Int`, or a
//     `fn` with no declared return type, whose result is Unit);
//  2. the flavours disagree — `try` on a `Maybe` inside a `Result`-returning
//     function, or the reverse;
//  3. both are `Result`s and the ERROR TYPES differ.
//
// (1) and (2) are spec §9's stated rule ("the nearest function boundary's
// return type must match"); (3) is the soundness rule. Without it,
// `try Json.decode(s)` inside `fn f(s: String): Result<Int, String>` would be
// accepted, and the Err path would carry a `Json.DecodeError` out of a
// function promising `String`: `try` propagates the operand's variant value
// unchanged, so nothing would notice until a caller rendered it and died in
// `Display.to_string`.
//
// Equality is required rather than convertibility. Nomi has no
// error-conversion story at the `try` boundary (Rust's `From`/`?` is the
// obvious prior art and is a separate feature with its own design questions),
// and the language already answers this: spec §9 gained `Result.map_err`
// precisely so a caller converts AT THE SITE. The final tolerance is
// `argMatchesParam`, so an INTERFACE-typed declared error side still accepts
// an implementer: the propagated value's type must inhabit the boundary's
// declared type.
//
// Boundaries this deliberately does not police:
//
//   - an INFERRING boundary (`concurrent` block, lambda), signalled by a live
//     `c.tryUnwinds` accumulator. resolveBoundaryErr owns those and applies
//     the SAME three clauses from the other direction — against the result the
//     body produced, because there is no declaration to check against.
//   - a TEST BODY, which declares no result at all — a propagating `try` there
//     ends the case with `test returned early` and is not a type error.
//     checkAttachedTests runs at declaration-list level, so an attached test's
//     body sees the ambient (top-level) `returnTy`/`currentFnName` and is
//     exempt by the same condition, not by a special case.
//
// The fn-boundary test is `c.returnTy` plus `c.currentFnName`, exactly as
// checkAssertionBoundaryAt spells it for `assert` — the same question about the
// same boundary, so it must not grow a second encoding.
func (c *checker) checkTryBoundary(operand *EnumType, at ast.Node) {
	if c.currentFnDecl != nil && c.fa.BootFunctions[c.currentFnDecl] && c.tryBoundary == "fn boot" {
		return
	}
	if c.tryUnwinds != nil {
		return
	}
	boundary := c.returnTy
	if boundary == nil && c.currentFnName != "" {
		boundary = TypeUnit
	}
	if boundary == nil {
		return
	}
	line, col := nodeLineCol(at)
	bt, ok := resolveTypeVar(boundary).(*EnumType)
	if !ok || !isTryOperandName(bt.Name) {
		c.addError(line, col, fmt.Sprintf(
			"`try` cannot propagate a %s out of a function returning %s: the enclosing function must return a Result or a Maybe",
			operand.Name, boundary))
		return
	}
	if bt.Name != operand.Name {
		c.addError(line, col, fmt.Sprintf(
			"`try` on a %s cannot propagate out of a function returning %s: the enclosing function must return a %s",
			operand.Name, boundary, operand.Name))
		return
	}
	if operand.Name != "Result" || len(operand.TypeArgs) < 2 || len(bt.TypeArgs) < 2 {
		return
	}
	got := resolveTypeVar(operand.TypeArgs[1])
	want := resolveTypeVar(bt.TypeArgs[1])
	if got == nil || want == nil || c.tryErrTypesAgree(want, got, line, col) {
		return
	}
	c.addMismatch(line, col, want, got, c.typef("try error type mismatch: expected %s, got %s (the enclosing function returns %s); convert the error at the `try`, for example with Result.map_err", want, got, boundary))
}

// tryErrTypesAgree reports whether a `try` operand's error type `got` inhabits
// the boundary's declared error type `want`.
//
// The ladder is TypeAssignable, then unify, then argMatchesParam, and each
// rung exists for a shape the previous one cannot answer. An enum where one
// of its embedded types is wanted is refused before any of them
// (embedsDowncast).
//
//   - TypeAssignable is the whole rule for the concrete case and the fast path.
//   - unify admits a side still carrying an unresolved inference variable, and
//     a generic boundary whose error side is the function's own type parameter
//     (`fn f<E>(…): Result<T, E>` propagating a `Result<_, E>`). A PROTECTED
//     type parameter is not unified away — mirroring checkReturn — so a
//     generic boundary is not silently satisfied by a concrete error type.
//   - argMatchesParam is the interface rung: an INTERFACE-typed declared error
//     side accepts a concrete implementer, consulting the impl registry the
//     same way a parameter position does.
func (c *checker) tryErrTypesAgree(want, got Type, line, col int) bool {
	if TypeAssignable(want, got) {
		return true
	}
	if _, _, down := embedsDowncast(want, got); down {
		return false
	}
	if !containsProtectedTypeParam(want, c.fnTypeParams) {
		if c.unify(want, got, map[*TypeParam_]Type{}) == nil {
			return true
		}
	}
	return c.argMatchesParam(got, want, c.recPos(line, col), RecordingKindInterfaceTypedParam)
}

func (c *checker) checkReturn(n *ast.Return) Type {
	if c.rejectExitlessExit("return", n.Line, n.Col) {
		if n.Value != nil {
			c.checkNode(n.Value)
		}
		return TypeInfallible
	}
	if n.Value == nil {
		c.recordBoundaryReturn(n, TypeUnit)
		if c.returnTy != nil && !TypesEqual(c.returnTy, TypeUnit) {
			subs := map[*TypeParam_]Type{}
			if containsProtectedTypeParam(c.returnTy, c.fnTypeParams) || c.unify(c.returnTy, TypeUnit, subs) != nil {
				c.addErrorAt(n, fmt.Sprintf(
					"return type mismatch: expected %s, got Unit", c.returnTy))
			}
		}
		return TypeInfallible
	}

	// Check the returned value against the declared return type, so bidirectional
	// inference flows in — same as the block's trailing expression. This lets a
	// generic constructor in a `return` resolve a dot-leading variant
	// (`return Ok(.Quit)`) the way the tail-expression form already does.
	valTy := c.checkNodeExpecting(n.Value, c.returnTy)
	c.recordBoundaryReturn(n, valTy)
	if valTy != nil && c.returnTy != nil && !TypeAssignable(c.returnTy, valTy) {
		subs := map[*TypeParam_]Type{}
		if !containsProtectedTypeParam(c.returnTy, c.fnTypeParams) && c.unifyInto(c.returnTy, valTy, subs) == nil {
			return TypeInfallible
		}
		if !c.argMatchesParam(valTy, c.returnTy, c.recPos(n.Line, n.Col), RecordingKindInterfaceTypedParam) {
			c.report(errAt(n.Value, c.typef(
				"return type mismatch: expected %s, got %s", c.returnTy, valTy)).WithHint(c.wholeResultHint(c.returnTy, valTy, n.Value)).WithHint(embedsDowncastHint(c.returnTy, valTy)))
		}
	}
	return TypeInfallible
}

func (c *checker) checkBreak(n *ast.Break) Type {
	if c.rejectExitlessExit("break", n.Line, n.Col) {
		if n.Value != nil {
			c.checkNode(n.Value)
		}
		return TypeInfallible
	}
	if n.Value != nil {
		vTy := c.checkNode(n.Value)
		if vTy != nil && c.iterBreakTy != nil {
			// When the break-target type is still an unresolved inference var
			// — a stateless `loop(|| … break v)` inferring its state type from
			// its breaks — bind it directly with nil subs so the TypeVar takes
			// the value's type (including when the value is itself a type
			// param). A non-nil subs map would route a TypeParam_ value into
			// the discarded subs instead of binding the var. A later break of a
			// different type finds the var resolved and goes through the normal
			// mismatch check below.
			if _, isVar := resolveTypeVar(c.iterBreakTy).(*TypeVar); isVar {
				_ = c.unify(c.iterBreakTy, vTy, nil)
				return TypeInfallible
			}
			// Unify against the enclosing iter-callback's declared return
			// type. For `loop(|n = 5| …)` the expected type is S, already
			// substituted to Int (from the default), so `break "hello"`
			// surfaces as Int vs String.
			subs := map[*TypeParam_]Type{}
			if err := c.unify(c.iterBreakTy, vTy, subs); err != nil {
				_ = err
				c.addMismatch(n.Line, n.Col, c.iterBreakTy, vTy, c.typef("break value type mismatch: expected %s, got %s", c.iterBreakTy, vTy))
			}
		}
	}
	return TypeInfallible
}

func (c *checker) checkListLit(n *ast.ListLit) Type {
	// Type-prefixed list literals (`Arr[1, 2, 3]`) are the literal-attach
	// construction form for list-payload enum variants and list-distinct
	// types. Route based on what TypeName resolves to.
	if n.TypeName != nil {
		typeName := n.TypeName.TypeString()
		// Variant literal-attach (value position). Enforces qualification
		// for bare same-file / imported-enum variants — only drill-through-
		// imported (incl. prelude `Some`/`None`/`Ok`/`Err`/`True`/`False`)
		// and qualified `EnumName.Variant` are accepted bare here.
		et, payload, _, posOK := c.resolveVariantPrefixInValuePos(n.TypeName, n.Line, n.Col)
		if !posOK {
			// Diagnostic already emitted by resolveVariantPrefixInValuePos.
			// Walk items so nested errors still surface.
			for _, item := range n.Items {
				c.checkNode(item)
			}
			return nil
		}
		if et != nil {
			lt, ok := payload.(*ListType)
			if !ok {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"variant %s does not take a list payload; type-prefixed list literal requires a variant whose payload is `List<T>`",
					typeName))
				for _, item := range n.Items {
					c.checkNode(item)
				}
				return et
			}
			for _, item := range n.Items {
				ty := c.checkNodeExpecting(item, lt.Elem)
				if ty == nil {
					continue
				}
				itemLine, itemCol := nodeLineCol(item)
				if !c.argMatchesParam(ty, lt.Elem, c.recPos(itemLine, itemCol), RecordingKindCallSite) {
					c.addMismatch(item.LineNum(), 1, lt.Elem, ty, c.typef("%s element type: expected %s, got %s", typeName, lt.Elem, ty))
				}
			}
			return et
		}
		// Distinct list-type literal-attach.
		named := c.reg.Lookup(typeName)
		if named == nil {
			c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("undefined type %s", typeName)}.WithHint(didYouMean(typeName, c.typeNamesAt(n.Line, n.Col))))
			for _, item := range n.Items {
				c.checkNode(item)
			}
			return nil
		}
		dt, ok := named.(*DistinctType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"%s is not a list-distinct type; type-prefixed list literal requires `type %s List<T>`",
				typeName, typeName))
			for _, item := range n.Items {
				c.checkNode(item)
			}
			return nil
		}
		lt, ok := dt.Inner.(*ListType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"%s wraps %s, not a list; type-prefixed list literal requires `type %s List<T>`",
				typeName, dt.Inner, typeName))
			for _, item := range n.Items {
				c.checkNode(item)
			}
			return dt
		}
		for _, item := range n.Items {
			ty := c.checkNodeExpecting(item, lt.Elem)
			if ty == nil {
				continue
			}
			itemLine, itemCol := nodeLineCol(item)
			if !c.argMatchesParam(ty, lt.Elem, c.recPos(itemLine, itemCol), RecordingKindCallSite) {
				c.addMismatch(item.LineNum(), 1, lt.Elem, ty, c.typef("%s element type: expected %s, got %s", typeName, lt.Elem, ty))
			}
		}
		return dt
	}

	if len(n.Items) == 0 {
		// Empty literal — element type is a fresh inference variable so
		// downstream unification can fill it in (e.g. `acc = []` later
		// used as `[item, ..acc]` resolves the element type to `T`).
		return &ListType{Elem: c.freshTypeVar()}
	}

	var elemTy Type
	itemTys := make([]Type, 0, len(n.Items))
	for _, item := range n.Items {
		ty := c.checkNode(item)
		if ty == nil {
			continue
		}
		itemTys = append(itemTys, ty)
		if elemTy == nil {
			elemTy = ty
		} else if !TypesEqual(elemTy, ty) {
			// An element still open in a type variable (`None` beside
			// `Some(1)`) takes the other elements' type.
			if (containsTypeVar(elemTy) || containsTypeVar(ty)) && c.unify(elemTy, ty, nil) == nil {
				continue
			}
			c.report(errAt(item, c.typef("list element type mismatch: expected %s, got %s", elemTy, ty)).WithHint(embedsDowncastHint(elemTy, ty)))
		}
	}

	if elemTy == nil {
		elemTy = c.freshTypeVar()
	}
	elemTy = widestEmbedsElem(elemTy, itemTys)
	markEmbedsWidening(n, elemTy, itemTys)
	return &ListType{Elem: elemTy}
}

// widestEmbedsElem is a list's element type when its elements mix an enum with
// values of types it `embeds`: the enum, whichever element came first. Every
// such element widens into it (`[c, Shape.Dot]` is a List<Shape>), and naming
// the first element's type instead typed the list by one embedded member.
func widestEmbedsElem(elemTy Type, itemTys []Type) Type {
	if embeddableTypeName(resolveTypeVar(elemTy)) == "" {
		return elemTy
	}
	for _, ty := range itemTys {
		if et, ok := resolveTypeVar(ty).(*EnumType); ok && isEmbeddedTypeOf(resolveTypeVar(elemTy), et) {
			return ty
		}
	}
	return elemTy
}

// markEmbedsWidening records on a list literal the enum its embedded elements
// widen into, for the IR builder (ast.ListLit.EmbedsEnum).
func markEmbedsWidening(n *ast.ListLit, elemTy Type, itemTys []Type) {
	et, ok := resolveTypeVar(elemTy).(*EnumType)
	if !ok {
		return
	}
	for _, ty := range itemTys {
		if isEmbeddedTypeOf(resolveTypeVar(ty), et) {
			n.EmbedsEnum = et.Name
			return
		}
	}
}

// checkListLitExpecting checks each list item against the expected element
// type. Used when context provides one (e.g. `xs: List<Shape> = [c, r, p]`),
// so heterogeneous embedded values widen to the enum at element granularity
// rather than each element having to match the first. Falls back to the
// unannotated `checkListLit` when the expected element type is itself an
// unbound type parameter or contains TypeVars — those cases are better
// handled by the call-site's own unification, which already knows how to
// bind generic params to concrete element types.
func (c *checker) checkListLitExpecting(n *ast.ListLit, elemTy Type) Type {
	// A type-prefixed literal (`Arr[1, 2]`, `.Arr[1, 2]`) constructs the
	// variant or list-distinct type it names, whatever list is expected.
	// Typing it as a plain list here left an unknown prefix unreported.
	if n.TypeName != nil || elemTy == nil || ContainsTypeParam(elemTy) || containsTypeVar(elemTy) {
		return c.checkListLit(n)
	}
	itemTys := make([]Type, 0, len(n.Items))
	for _, item := range n.Items {
		ty := c.checkNodeExpecting(item, elemTy)
		if ty == nil {
			continue
		}
		itemTys = append(itemTys, ty)
		// argMatchesParam (a superset of TypesEqual) accepts an element that
		// *conforms* to an interface element type — so `List<Struct>` admits any
		// struct, `List<Display>` any Display impl — not just exact-type matches.
		itemLine, itemCol := nodeLineCol(item)
		if !TypeAssignable(elemTy, ty) && !c.argMatchesParam(ty, elemTy, c.recPos(itemLine, itemCol), RecordingKindCallSite) {
			c.report(errAt(item, c.typef("list element type mismatch: expected %s, got %s", elemTy, ty)).WithHint(embedsDowncastHint(elemTy, ty)))
		}
	}
	markEmbedsWidening(n, elemTy, itemTys)
	return &ListType{Elem: elemTy}
}

// iterElemType is T when ty is std's `Iter<T>`.
func iterElemType(ty Type) (Type, bool) {
	it, ok := resolveTypeVar(ty).(*InterfaceType)
	if !ok || !isStdIterType(it) || len(it.TypeArgs) != 1 {
		return nil, false
	}
	return it.TypeArgs[0], true
}

func vectorElemType(ty Type) (Type, bool) {
	dt, ok := resolveTypeVar(ty).(*DistinctType)
	if !ok || dt.Name != "Vector" || len(dt.TypeArgs) != 1 {
		return nil, false
	}
	return dt.TypeArgs[0], true
}

func makeVectorType(elem Type, reg *TypeRegistry) Type {
	if elem == nil {
		elem = TypeAny
	}
	if reg != nil {
		if dt, ok := reg.Lookup("Vector").(*DistinctType); ok {
			return &DistinctType{
				Origin:           dt.Origin,
				Name:             dt.Name,
				Inner:            dt.Inner,
				Opaque:           dt.Opaque,
				OwningSourceFile: dt.OwningSourceFile,
				TypeParams:       dt.TypeParams,
				TypeParamDefs:    dt.TypeParamDefs,
				TypeArgs:         []Type{elem},
			}
		}
	}
	return &DistinctType{Name: "Vector", Opaque: true, TypeArgs: []Type{elem}}
}

func setElemType(ty Type) (Type, bool) {
	st, ok := resolveTypeVar(ty).(*StructType)
	if !ok || st.Name != "Set" || len(st.TypeArgs) != 1 {
		return nil, false
	}
	return st.TypeArgs[0], true
}

func makeSetType(elem Type, reg *TypeRegistry) Type {
	if elem == nil {
		elem = TypeAny
	}
	if reg != nil {
		if st, ok := reg.Lookup("Set").(*StructType); ok {
			return &StructType{
				Origin:           st.Origin,
				Name:             st.Name,
				Fields:           st.Fields,
				Opaque:           st.Opaque,
				OwningSourceFile: st.OwningSourceFile,
				TypeParams:       st.TypeParams,
				TypeParamDefs:    st.TypeParamDefs,
				TypeArgs:         []Type{elem},
			}
		}
	}
	return &StructType{Name: "Set", Opaque: true, TypeArgs: []Type{elem}}
}

func (c *checker) checkVectorLit(n *ast.VectorLit) Type {
	if len(n.Items) == 0 {
		return makeVectorType(c.freshTypeVar(), c.reg)
	}

	var elemTy Type
	for _, item := range n.Items {
		ty := c.checkNode(item)
		if ty == nil {
			continue
		}
		if elemTy == nil {
			elemTy = ty
		} else if !TypesEqual(elemTy, ty) {
			// An element still open in a type variable (`None` beside
			// `Some(1)`) takes the other elements' type, as in a list.
			if (containsTypeVar(elemTy) || containsTypeVar(ty)) && c.unify(elemTy, ty, nil) == nil {
				continue
			}
			c.report(errAt(item, c.typef("vector element type mismatch: expected %s, got %s", elemTy, ty)).WithHint(embedsDowncastHint(elemTy, ty)))
		} else {
			elemTy = embedsJoin(elemTy, ty)
		}
	}
	if elemTy == nil {
		elemTy = c.freshTypeVar()
	}
	return makeVectorType(elemTy, c.reg)
}

func (c *checker) checkVectorLitExpecting(n *ast.VectorLit, elemTy Type) Type {
	if elemTy == nil || ContainsTypeParam(elemTy) || containsTypeVar(elemTy) {
		return c.checkVectorLit(n)
	}
	for _, item := range n.Items {
		ty := c.checkNodeExpecting(item, elemTy)
		if ty == nil {
			continue
		}
		itemLine, itemCol := nodeLineCol(item)
		if !TypeAssignable(elemTy, ty) && !c.argMatchesParam(ty, elemTy, c.recPos(itemLine, itemCol), RecordingKindCallSite) {
			c.report(errAt(item, c.typef("vector element type mismatch: expected %s, got %s", elemTy, ty)).WithHint(embedsDowncastHint(elemTy, ty)))
		}
	}
	return makeVectorType(elemTy, c.reg)
}

func (c *checker) checkSetLit(n *ast.SetLit) Type {
	if len(n.Items) == 0 {
		return makeSetType(c.freshTypeVar(), c.reg)
	}

	var elemTy Type
	for _, item := range n.Items {
		ty := c.checkNode(item)
		if ty == nil {
			continue
		}
		if elemTy == nil {
			elemTy = ty
		} else if !TypesEqual(elemTy, ty) {
			// An element still open in a type variable (`None` beside
			// `Some(1)`) takes the other elements' type, as in a list.
			if (containsTypeVar(elemTy) || containsTypeVar(ty)) && c.unify(elemTy, ty, nil) == nil {
				continue
			}
			c.report(errAt(item, c.typef("set element type mismatch: expected %s, got %s", elemTy, ty)).WithHint(embedsDowncastHint(elemTy, ty)))
		} else {
			elemTy = embedsJoin(elemTy, ty)
		}
	}
	if elemTy == nil {
		elemTy = c.freshTypeVar()
	}
	return makeSetType(elemTy, c.reg)
}

func (c *checker) checkSetLitExpecting(n *ast.SetLit, elemTy Type) Type {
	if elemTy == nil || ContainsTypeParam(elemTy) || containsTypeVar(elemTy) {
		return c.checkSetLit(n)
	}
	for _, item := range n.Items {
		ty := c.checkNodeExpecting(item, elemTy)
		if ty == nil {
			continue
		}
		itemLine, itemCol := nodeLineCol(item)
		if !TypeAssignable(elemTy, ty) && !c.argMatchesParam(ty, elemTy, c.recPos(itemLine, itemCol), RecordingKindCallSite) {
			c.report(errAt(item, c.typef("set element type mismatch: expected %s, got %s", elemTy, ty)).WithHint(embedsDowncastHint(elemTy, ty)))
		}
	}
	return makeSetType(elemTy, c.reg)
}

// checkListSpreadLit types `[h1, h2, ..., ..tail]`. Element type unifies across
// all heads and the tail's element type. Uses nil subs so TypeVar binding
// (`tv.Resolved = b`) takes precedence over TypeParam_-into-subs, which is
// what we want: an empty-list tail's `List<TypeVar>` gets resolved against
// the head's element type rather than the head's TypeParam_ being captured
// into a discarded local subs map.
func (c *checker) checkListSpreadLit(n *ast.ListSpreadLit) Type {
	var elemTy Type
	for _, h := range n.Heads {
		ht := c.checkNode(h)
		if ht == nil {
			continue
		}
		if elemTy == nil {
			elemTy = ht
		} else if err := UnifyWithImpls(elemTy, ht, nil, c.implsContext(), c.implTypeArgsContext()); err != nil {
			c.addError(n.Line, 1, c.typef(
				"list element type mismatch: %s vs %s", elemTy, ht))
		}
	}
	if n.TailSpread != nil {
		tailTy := c.checkNode(n.TailSpread)
		if lt, ok := tailTy.(*ListType); ok {
			if elemTy == nil {
				elemTy = lt.Elem
			} else if err := UnifyWithImpls(elemTy, lt.Elem, nil, c.implsContext(), c.implTypeArgsContext()); err != nil {
				c.addError(n.Line, 1, c.typef(
					"list spread tail element type mismatch: %s vs %s", elemTy, lt.Elem))
			}
		}
	}
	if elemTy == nil {
		elemTy = c.freshTypeVar()
	}
	return &ListType{Elem: elemTy}
}

func (c *checker) checkTupleLit(n *ast.TupleLit) Type {
	return c.checkTupleLitExpecting(n, nil)
}

// checkTupleLitExpecting checks each item against its expected element type,
// when expected (one per item) is non-nil.
func (c *checker) checkTupleLitExpecting(n *ast.TupleLit, expected []Type) Type {
	elems := make([]Type, len(n.Items))
	unknown := false
	for i, item := range n.Items {
		errsBefore := len(c.errors)
		var ty Type
		if expected != nil {
			ty = c.checkNodeExpecting(item, expected[i])
		} else {
			ty = c.checkNode(item)
		}
		if ty == nil {
			// An element with an error has no type, and neither does the
			// tuple: a Unit in its place draws a follow-on mismatch.
			unknown = unknown || len(c.errors) > errsBefore
			ty = TypeUnit
		}
		elems[i] = ty
	}
	if unknown {
		return nil
	}
	return &TupleType{Elems: elems}
}

func (c *checker) checkMapLit(n *ast.MapLit) Type {
	// Type-prefixed map literals (`Kvs{"a" => 1}`) are the literal-attach
	// construction form for map-distinct types. The named type must be a
	// DistinctType wrapping a MapType — check entries against the inner
	// K/V and return the named distinct type. Anything else (struct,
	// non-map distinct, etc.) is a type error.
	if n.TypeName != nil {
		typeName := n.TypeName.TypeString()
		// Variant literal-attach (value position): `Obj{"k" => v}` where
		// `Obj` is an enum variant with a Map payload. Enforces
		// qualification for bare same-file / imported-enum variants —
		// only drill-through-imported and qualified `EnumName.Variant`
		// are accepted bare here. Check entries against the variant's
		// inner Map<K, V> and return the enclosing enum type — mirrors
		// the DistinctType branch below but routes through the variant.
		et, payload, _, posOK := c.resolveVariantPrefixInValuePos(n.TypeName, n.Line, n.Col)
		if !posOK {
			// Diagnostic already emitted by resolveVariantPrefixInValuePos.
			// Walk entries so nested errors still surface.
			for _, entry := range n.Entries {
				c.checkNode(entry.Key)
				c.checkNode(entry.Value)
			}
			return nil
		}
		if et != nil {
			mt, ok := payload.(*MapType)
			if !ok {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"variant %s does not take a map payload; type-prefixed map literal requires a variant whose payload is `Map<K, V>`",
					typeName))
				for _, entry := range n.Entries {
					c.checkNode(entry.Key)
					c.checkNode(entry.Value)
				}
				return et
			}
			for _, entry := range n.Entries {
				kt := c.checkNodeExpecting(entry.Key, mt.Key)
				keyLine, keyCol := nodeLineCol(entry.Key)
				if kt != nil && !c.argMatchesParam(kt, mt.Key, c.recPos(keyLine, keyCol), RecordingKindCallSite) {
					c.addMismatch(entry.Key.LineNum(), 1, mt.Key, kt, c.typef("%s key type: expected %s, got %s", typeName, mt.Key, kt))
				}
				vt := c.checkNodeExpecting(entry.Value, mt.Val)
				valLine, valCol := nodeLineCol(entry.Value)
				if vt != nil && !c.argMatchesParam(vt, mt.Val, c.recPos(valLine, valCol), RecordingKindCallSite) {
					c.addMismatch(entry.Value.LineNum(), 1, mt.Val, vt, c.typef("%s value type: expected %s, got %s", typeName, mt.Val, vt))
				}
			}
			return et
		}
		named := c.reg.Lookup(typeName)
		if named == nil {
			c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("undefined type %s", typeName)}.WithHint(didYouMean(typeName, c.typeNamesAt(n.Line, n.Col))))
			// Still descend into entries so nested errors surface.
			for _, entry := range n.Entries {
				c.checkNode(entry.Key)
				c.checkNode(entry.Value)
			}
			return nil
		}
		dt, ok := named.(*DistinctType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"%s is not a map-distinct type; type-prefixed map literal requires `type %s Map<K, V>`",
				typeName, typeName))
			for _, entry := range n.Entries {
				c.checkNode(entry.Key)
				c.checkNode(entry.Value)
			}
			return nil
		}
		mt, ok := dt.Inner.(*MapType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"%s wraps %s, not a map; type-prefixed map literal requires `type %s Map<K, V>`",
				typeName, dt.Inner, typeName))
			for _, entry := range n.Entries {
				c.checkNode(entry.Key)
				c.checkNode(entry.Value)
			}
			return dt
		}
		// Check each entry's key against mt.Key and value against mt.Val.
		for _, entry := range n.Entries {
			kt := c.checkNodeExpecting(entry.Key, mt.Key)
			keyLine, keyCol := nodeLineCol(entry.Key)
			if kt != nil && !c.argMatchesParam(kt, mt.Key, c.recPos(keyLine, keyCol), RecordingKindCallSite) {
				c.addMismatch(entry.Key.LineNum(), 1, mt.Key, kt, c.typef("%s key type: expected %s, got %s", typeName, mt.Key, kt))
			}
			vt := c.checkNodeExpecting(entry.Value, mt.Val)
			valLine, valCol := nodeLineCol(entry.Value)
			if vt != nil && !c.argMatchesParam(vt, mt.Val, c.recPos(valLine, valCol), RecordingKindCallSite) {
				c.addMismatch(entry.Value.LineNum(), 1, mt.Val, vt, c.typef("%s value type: expected %s, got %s", typeName, mt.Val, vt))
			}
		}
		return dt
	}

	var keyTy, valTy Type
	for _, entry := range n.Entries {
		kt := c.checkNode(entry.Key)
		vt := c.checkNode(entry.Value)
		if kt != nil {
			if keyTy == nil {
				keyTy = kt
			} else if !TypesEqual(keyTy, kt) {
				c.addMismatch(n.Line, 1, keyTy, kt, c.typef("map key type mismatch: expected %s, got %s", keyTy, kt))
			} else {
				keyTy = embedsJoin(keyTy, kt)
			}
		}
		if vt != nil {
			if valTy == nil {
				valTy = vt
			} else if !TypesEqual(valTy, vt) {
				c.addMismatch(n.Line, 1, valTy, vt, c.typef("map value type mismatch: expected %s, got %s", valTy, vt))
			} else {
				valTy = embedsJoin(valTy, vt)
			}
		}
	}
	if keyTy == nil {
		keyTy = c.freshTypeVar()
	}
	if valTy == nil {
		valTy = c.freshTypeVar()
	}
	return &MapType{Key: keyTy, Val: valTy}
}

// checkPatternQualifier checks the qualifier of a qualified variant pattern
// (`Color` in `Color.Blue`, `ticket.Status` in `ticket.Status.Open`)
// against the scrutinee's type, and reports false after an error. The
// variant is looked up by its member name alone, so without this a
// misspelled `C6lor.Blue`, or another enum's `Mood.Blue`, matched
// `Color.Blue`, and the IR builder, which does read the qualifier, declined
// the `case`.
//
// A qualifier names something when the registry holds it (a module-level or
// block-local type, or a module-qualified one) or the builder resolved it
// to a symbol (an import, an alias, a module); anything else is an unknown
// type. Against an enum, a qualifier that names any other type (another
// enum, a struct, a distinct, an interface, a built-in such as `String`) is
// an error too: enums do not embed enums, so only the enum itself owns its
// variants.
func (c *checker) checkPatternQualifier(qt *ast.QualifiedType, expectedTy Type) bool {
	if qt.Module == "" || IsSynthesizedLine(qt.ModuleLine) {
		return true
	}
	named := c.reg.Lookup(qt.Module)
	if named == nil {
		if c.fa.References[Pos{Line: qt.ModuleLine, Col: qt.ModuleCol}] != nil {
			return true
		}
		c.report(unknownTypeError(c.reg, qt.ModuleLine, qt.ModuleCol, qt.Module))
		return false
	}
	et, isEnum := resolveTypeVar(expectedTy).(*EnumType)
	if !isEnum {
		return true
	}
	other := ""
	switch q := named.(type) {
	case *EnumType:
		if sameNominalIdentity(q.Origin, q.Name, et.Origin, et.Name) {
			return true
		}
		other = "enum " + q.Name
	case *StructType:
		other = "struct " + q.Name
	case *DistinctType:
		other = "type " + q.Name
	case *InterfaceType:
		other = "interface " + q.Name
	default:
		// A built-in (`String.Cat`), a `typealias` of a non-enum, a bound
		// alias: none of them is the enum.
		other = "type " + qt.Module
	}
	e := TypeError{Line: qt.ModuleLine, Col: qt.ModuleCol, EndLine: qt.ModuleLine, EndCol: qt.ModuleCol + len(qt.Module),
		Message: fmt.Sprintf("pattern `%s` is qualified by %s, but the value matched is %s %s",
			qt.TypeString(), other, articleFor(et.Name), et.Name)}
	member := TypeExprBaseName(qt.Member)
	for _, v := range et.Variants {
		if v.Name == member {
			e = e.WithHint(fmt.Sprintf("write `%s.%s`, or `.%s`", et.Name, member, member))
			break
		}
	}
	c.report(e)
	return false
}

// checkAsPattern checks `P as name`: P against the value, and name bound to
// the whole value, with the type of the position the pattern sits in. The
// name is not narrowed by P: `.Circle{r} as s` over a Shape binds s as a
// Shape.
//
// An `as` whose pattern is itself a name or `_` binds nothing a plain name
// would not, so it is an error: `y as x` binds one value twice, and `_ as x`
// is the pattern `x`.
func (c *checker) checkAsPattern(n *ast.AsPattern, expectedTy Type) {
	c.validateBindingLikeName(n.Name, n.NameLine, n.NameCol, "binding name")
	switch inner := n.Pattern.(type) {
	case *ast.IdentPattern:
		c.report(TypeError{Line: n.NameLine, Col: n.NameCol, EndLine: n.NameLine, EndCol: n.NameCol + len(n.Name),
			Message: fmt.Sprintf("`%s as %s` binds the same value twice; use one name", inner.Name, n.Name)})
	case *ast.AsPattern:
		c.report(TypeError{Line: n.NameLine, Col: n.NameCol, EndLine: n.NameLine, EndCol: n.NameCol + len(n.Name),
			Message: fmt.Sprintf("`as %s as %s` binds the same value twice; use one name", inner.Name, n.Name)})
	case *ast.WildcardPattern:
		c.report(TypeError{Line: n.NameLine, Col: n.NameCol, EndLine: n.NameLine, EndCol: n.NameCol + len(n.Name),
			Message: fmt.Sprintf("`_ as %s` matches every value, as `%s` does; write `%s`", n.Name, n.Name, n.Name)})
	}
	c.checkPattern(n.Pattern, expectedTy)
	if expectedTy != nil {
		if sym := c.fa.Definitions[Pos{Line: n.NameLine, Col: n.NameCol}]; sym != nil {
			sym.Type = expectedTy
		}
	}
}

// checkPattern assigns expectedTy to the symbols introduced by a pattern.
// It handles Wildcard, Ident, Tuple, Enum, Struct, List, and Map patterns.
func (c *checker) checkPattern(pattern ast.Node, expectedTy Type) {
	// A payload type reached through a generic call can be a TypeVar bound
	// to the concrete type (`pick<T>(...)` returning `Opt<T>`, T bound to a
	// tuple). Every arm below type-switches on the expected type, so follow
	// the binding once here rather than in each arm.
	expectedTy = resolveTypeVar(expectedTy)
	switch n := pattern.(type) {
	case *ast.WildcardPattern:
		// Wildcard binds nothing, but the position has a known type from
		// the case scrutinee / destructure source. Register a synthetic
		// Symbol so hover renders e.g. `_: Int` instead of nothing.
		if expectedTy != nil {
			c.fa.References[Pos{Line: n.Line, Col: n.Col}] = &Symbol{
				Name: "_",
				Kind: SymbolBinding,
				Pos:  Pos{Line: n.Line, Col: n.Col},
				Type: expectedTy,
			}
		}
	case *ast.IdentPattern:
		c.validateBindingLikeName(n.Name, n.Line, n.Col, "binding name")
		if expectedTy != nil {
			sym := c.fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
			if sym != nil {
				sym.Type = expectedTy
			}
		}
	case *ast.AsPattern:
		c.checkAsPattern(n, expectedTy)
	case *ast.CodepointLit:
		c.registerCodepointHover(n)
		c.checkLiteralPattern("'"+n.Lexeme+"'", n.Line, n.Col, TypeCodepoint, expectedTy)
	case *ast.IntLit:
		c.checkLiteralPattern(n.Lexeme, n.Line, n.Col, TypeInt, expectedTy)
	case *ast.FloatLit:
		c.checkLiteralPattern(n.Lexeme, n.Line, n.Col, TypeFloat, expectedTy)
	case *ast.DecimalLit:
		c.checkLiteralPattern(n.Lexeme, n.Line, n.Col, TypeDecimal, expectedTy)
	case *ast.StringLit:
		c.checkLiteralPattern(stringPatternText(n), n.Line, n.Col, TypeString, expectedTy)
	case *ast.Binary:
		if n.Op != "+" {
			c.addError(n.Line, n.Col, fmt.Sprintf("unsupported binary pattern operator %s", n.Op))
			return
		}
		if _, ok := n.Left.(*ast.StringLit); !ok {
			c.addError(n.Line, n.Col, "string prefix pattern must start with a string literal")
			return
		}
		binding, ok := n.Right.(*ast.IdentPattern)
		if !ok {
			c.addError(n.Line, n.Col, "string prefix pattern must bind the remaining string to a name")
			return
		}
		if expectedTy != nil && !TypesEqual(resolveTypeVar(expectedTy), TypeString) {
			c.addError(n.Line, n.Col, fmt.Sprintf("string prefix pattern requires a String scrutinee, got %s", expectedTy))
		}
		c.checkPattern(binding, TypeString)
	case *ast.TuplePattern:
		if expectedTy == nil {
			for _, sub := range n.Patterns {
				c.checkPattern(sub, nil)
			}
			return
		}
		tt, ok := expectedTy.(*TupleType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf("tuple pattern requires a tuple type, got %s", expectedTy))
			for _, sub := range n.Patterns {
				c.checkPattern(sub, nil)
			}
			return
		}
		if len(n.Patterns) != len(tt.Elems) {
			c.addError(n.Line, n.Col, fmt.Sprintf("tuple pattern has %d elements but expected %d", len(n.Patterns), len(tt.Elems)))
			for _, sub := range n.Patterns {
				c.checkPattern(sub, nil)
			}
			return
		}
		for i, sub := range n.Patterns {
			c.checkPattern(sub, tt.Elems[i])
		}

	case *ast.EnumPattern:
		if expectedTy == nil {
			return
		}
		// Extract variant/type name from the TypeExpr.
		variantName := ""
		var variantPos Pos
		switch v := n.Variant.(type) {
		case *ast.SimpleType:
			variantName = v.Name
			variantPos = Pos{Line: v.Line, Col: v.Col}
		case *ast.QualifiedType:
			if !c.checkPatternQualifier(v, expectedTy) {
				return
			}
			variantName = v.Member.TypeString()
			if sm, ok := v.Member.(*ast.SimpleType); ok {
				variantPos = Pos{Line: sm.Line, Col: sm.Col}
			}
		case *ast.DotVariantType:
			// Dot-leading variant pattern (`case x { .Obj{...} -> ... }`).
			// The scrutinee's type IS the type-determined context here, so
			// the lookup proceeds without c.expectedEnum — the expectedTy
			// parameter already carries the scrutinee's enum. Record a
			// reference at the identifier position (one past the leading
			// dot) so hover and go-to-def work; the variantPos below uses
			// the same key for downstream alias/CallType-proxy lookups.
			variantName = v.Name
			variantPos = Pos{Line: v.Line, Col: v.Col + 1}
			if et, ok := expectedTy.(*EnumType); ok {
				v.ResolvedEnum = et.Name
				c.recordDotVariantReference(v.Name, v.Line, v.Col, et)
			}
		default:
			variantName = n.Variant.TypeString()
		}
		// If the name is an aliased import (e.g. `Some as Just`), follow
		// Resolved to recover the real variant name used in the enum definition.
		if variantPos != (Pos{}) {
			if sym, ok := c.fa.References[variantPos]; ok && sym.Resolved != nil && sym.Resolved.Name != "" {
				variantName = sym.Resolved.Name
			}
		}
		// Handle distinct types matched with TypeName(binding) or bare TypeName.
		if dt, ok := expectedTy.(*DistinctType); ok {
			patternName := variantName
			if _, ok := n.Variant.(*ast.QualifiedType); ok {
				patternName = n.Variant.TypeString()
			}
			if !distinctTypeNameMatches(patternName, dt.Name) {
				c.addError(n.Line, n.Col, fmt.Sprintf("distinct type pattern %s does not match type %s", patternName, dt.Name))
				return
			}
			// Opaque-types: destructuring an opaque distinct type from
			// outside its owning module exposes the wrapped value. Block
			// it. Same-module destructuring is fine.
			if dt.Opaque && (c.fa == nil || c.fa.FilePath != dt.OwningSourceFile) {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"cannot destructure opaque type '%s' outside its defining module", dt.Name))
				return
			}
			if n.Payload != nil {
				c.checkPattern(n.Payload, dt.Inner)
			} else if n.Binding != "" {
				sym := c.fa.Definitions[Pos{Line: n.Line, Col: n.BindingCol}]
				if sym != nil && dt.Inner != nil {
					sym.Type = dt.Inner
				}
			}
			return
		}
		et, ok := expectedTy.(*EnumType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf("enum pattern requires an enum type, got %s", expectedTy))
			return
		}
		// Opaque-types: destructuring an opaque enum (inline-body opaque)
		// from outside its owning module exposes the variant shape and
		// payloads. Block it; same-module destructuring is the owner's
		// right. Mirrors the DistinctType branch above.
		if et.Opaque && (c.fa == nil || c.fa.FilePath != et.OwningSourceFile) {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"cannot destructure opaque type '%s' outside its defining module", et.Name))
			return
		}
		// Look up variant by name.
		var found *VariantDef
		for i := range et.Variants {
			if et.Variants[i].Name == variantName {
				found = &et.Variants[i]
				break
			}
		}
		if found == nil {
			c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("variant %s not found in enum %s", variantName, et.Name)}.WithHint(didYouMean(variantName, variantNames(et))))
			return
		}
		// Bare-prefix variant patterns are accepted only when the bare
		// name is an actual variant binding in scope: prelude variants or
		// drill-through selective imports such as
		// `import std/comparable.Ordering.{Equal}`. Same-file variants and
		// variants reachable only through an imported enum still use the
		// type-directed `.Variant` form or the qualified `Enum.Variant`
		// spelling.
		if st, ok := n.Variant.(*ast.SimpleType); ok && !c.allowsBareVariantPattern(st.Name, variantPos) {
			c.addError(st.Line, st.Col, bareVariantPatternMessage(st.Name, et.Name))
		}
		// Determine data type, substituting generics if needed.
		dataType := found.DataType
		if dataType != nil && len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
			subs := make(map[*TypeParam_]Type, len(et.TypeParamDefs))
			for i, def := range et.TypeParamDefs {
				subs[def] = et.TypeArgs[i]
			}
			dataType = Substitute(dataType, subs)
		}
		// Data-carrying variants in pattern position must spell their payload
		// — `Err(_)` or `Err(s)`, never bare `Err`. Bare-variant patterns are
		// reserved for variants that genuinely have no data (`None`, `True`,
		// or `embeds <ZeroSizedType>` variants). Without this check, the
		// formatter rewriting `Err(_)` → `Err` silently strips a binding the
		// reader needs.
		if dataType != nil && !IsZeroSized(dataType) && n.Payload == nil && n.Binding == "" {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"variant %s carries data; pattern needs an explicit payload (e.g. %s(_) or %s(name))",
				variantName, variantName, variantName))
		}
		// The inverse: a variant with no data (`None`, a plain bare variant)
		// admits no payload or binding — `Plain(x)` would bind a value that
		// doesn't exist. Zero-sized embeds variants are NOT data-less (their
		// payload is the zero-sized distinct itself, spec §8 *Embedded Types*, so
		// `Switch.Off(x)` binds x: Off) and stay accepted; the IR builder
		// relies on this rejection to know a binding-with-no-payload-value
		// match can only be a zero-sized embeds variant.
		if dataType == nil && len(found.Fields) == 0 && found.Kind != VariantEmbedded &&
			(n.Payload != nil || (n.Binding != "" && !ast.IsDiscardName(n.Binding))) {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"variant %s carries no data; use the bare pattern %s",
				variantName, variantName))
		}
		// Mirror the call-site CallType machinery so hover at this pattern
		// position shows `variant Ok(Int): Result<Int, String>` rather than
		// the raw `type Result<T, E>` definition. Per-position copy avoids
		// clobbering across pattern sites that share the same variant Symbol.
		if dataType != nil && variantPos.Line > 0 {
			callTy := &FuncType{Params: []Type{dataType}, Return: et}
			if refSym, ok := c.fa.References[variantPos]; ok && refSym != nil {
				proxy := *refSym
				proxy.CallType = callTy
				c.fa.References[variantPos] = &proxy
			}
		}
		// Bind the inner value if present.
		if n.Payload != nil {
			// Flat-destructure shorthand `Variant(a, b, ...)` is now
			// accepted for variants whose payload is a tuple of arity ≥ 2,
			// mirroring the tuple-distinct literal-attach pattern form
			// (`Pair(a, b)`). The flat TuplePattern's arity / element
			// types fall through to the regular tuple-pattern check below,
			// which produces the same diagnostics either way.
			c.checkPattern(n.Payload, dataType)
		} else if n.Binding != "" {
			sym := c.fa.Definitions[Pos{Line: n.Line, Col: n.BindingCol}]
			if sym != nil && dataType != nil {
				sym.Type = dataType
			}
		}

	case *ast.StructPattern:
		if expectedTy == nil {
			for _, f := range n.Fields {
				if f.Pattern != nil {
					c.checkPattern(f.Pattern, nil)
				}
			}
			return
		}
		var fields []FieldDef
		var structName string
		var subs map[*TypeParam_]Type
		switch st := expectedTy.(type) {
		case *StructType:
			// Opaque-types: destructuring an opaque struct (inline-body
			// opaque) from outside its owning module exposes the field
			// representation. Block it; same-module destructuring is the
			// owner's right.
			if st.Opaque && (c.fa == nil || c.fa.FilePath != st.OwningSourceFile) {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"cannot destructure opaque type '%s' outside its defining module", st.Name))
				return
			}
			if !c.structPatternNames(n, st) {
				c.checkPatternFieldsUntyped(n)
				return
			}
			fields = st.Fields
			structName = st.Name
			if len(st.TypeParamDefs) > 0 && len(st.TypeArgs) == len(st.TypeParamDefs) {
				subs = make(map[*TypeParam_]Type, len(st.TypeParamDefs))
				for i, def := range st.TypeParamDefs {
					subs[def] = st.TypeArgs[i]
				}
			}
		case *AnonStructType:
			if n.TypeName != nil {
				// `Person{name} = {name: "Ada"}`: an anonymous struct is no
				// declared struct's value (no width or nominal subtyping),
				// so the named pattern never describes it.
				if c.structPatternTypeKnown(n) {
					c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf(
						"struct pattern names %s, but the value is the anonymous struct %s",
						n.TypeName.TypeString(), st)}.WithHint(
						"drop the type name to destructure an anonymous struct"))
				}
				c.checkPatternFieldsUntyped(n)
				return
			}
			fields = st.Fields
			structName = st.String()
		case *EnumType:
			// Struct variant pattern: Error.HttpError{status, message}
			// Look up the variant from the pattern's TypeName.
			variantName := ""
			if n.TypeName != nil {
				switch v := n.TypeName.(type) {
				case *ast.QualifiedType:
					if !c.checkPatternQualifier(v, st) {
						for _, f := range n.Fields {
							if f.Pattern != nil {
								c.checkPattern(f.Pattern, nil)
							}
						}
						return
					}
					variantName = v.Member.TypeString()
				case *ast.SimpleType:
					variantName = v.Name
				case *ast.DotVariantType:
					// Dot-leading struct-variant pattern (`.Rect{w, h}`).
					// The scrutinee's type pins the enum; record a hover/
					// go-to-def reference at the identifier position so the
					// LSP can resolve the variant the way it does for the
					// qualified form.
					variantName = v.Name
					v.ResolvedEnum = st.Name
					c.recordDotVariantReference(v.Name, v.Line, v.Col, st)
				}
			}
			vd := findVariant(st, variantName)
			if vd == nil {
				c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("variant %s not found in enum %s", variantName, st.Name)}.WithHint(didYouMean(variantName, variantNames(st))))
				for _, f := range n.Fields {
					if f.Pattern != nil {
						c.checkPattern(f.Pattern, nil)
					}
				}
				return
			}
			// Bare-prefix struct-variant pattern (`Rect{w, h}` against an
			// enum scrutinee) is rejected post-migration; same rule as
			// EnumPattern's bare-prefix rejection above.
			if sst, ok := n.TypeName.(*ast.SimpleType); ok && !isPreludeBareVariant(sst.Name) {
				c.addError(sst.Line, sst.Col, bareVariantPatternMessage(sst.Name, st.Name))
			}
			fields = vd.Fields
			structName = vd.Name
			// Embed variants carry their fields on the underlying struct's
			// DataType, not on `vd.Fields` (which is empty). Mirror what
			// `fieldTypeFromObject` does for `*EnumType` field access so
			// `case s { Shape.Rectangle{width, height} -> ... }` can
			// destructure through to the embedded struct.
			if len(fields) == 0 && vd.DataType != nil {
				if est, ok := vd.DataType.(*StructType); ok {
					fields = est.Fields
					if structName == "" {
						structName = est.Name
					}
				}
			}
			if len(st.TypeParamDefs) > 0 && len(st.TypeArgs) == len(st.TypeParamDefs) {
				subs = make(map[*TypeParam_]Type, len(st.TypeParamDefs))
				for i, def := range st.TypeParamDefs {
					subs[def] = st.TypeArgs[i]
				}
			}
		default:
			c.addError(n.Line, n.Col, fmt.Sprintf("struct pattern requires a struct type, got %s", expectedTy))
			for _, f := range n.Fields {
				if f.Pattern != nil {
					c.checkPattern(f.Pattern, nil)
				}
			}
			return
		}
		for _, f := range n.Fields {
			var fieldType Type
			found := false
			for _, sf := range fields {
				if sf.Name == f.Name {
					fieldType = sf.Type
					found = true
					break
				}
			}
			if !found {
				c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("field %s not found in type %s", f.Name, structName)}.WithHint(didYouMean(f.Name, fieldNames(fields))))
				if f.Pattern != nil {
					c.checkPattern(f.Pattern, nil)
				}
				continue
			}
			if fieldType != nil && subs != nil {
				fieldType = Substitute(fieldType, subs)
			}
			if f.Pattern != nil {
				c.checkPattern(f.Pattern, fieldType)
			} else if f.Binding != "" {
				line := structPatternFieldBindingLine(f, n.Line)
				sym := c.fa.Definitions[Pos{Line: line, Col: f.BindingCol}]
				if sym != nil {
					sym.Type = fieldType
				}
			}
			// Hover on the field-name key (`x` in `case p { {x: a} -> ...}`).
			// Skip punning bindings to avoid colliding with the binding's
			// SymbolBinding at the same position.
			bindingLine := structPatternFieldBindingLine(f, n.Line)
			punning := f.Binding == f.Name && f.NameLine == bindingLine && f.NameCol == f.BindingCol
			if f.NameLine > 0 && fieldType != nil && !punning {
				c.fa.References[Pos{Line: f.NameLine, Col: f.NameCol}] = &Symbol{
					Name: f.Name,
					Kind: SymbolField,
					Pos:  Pos{Line: f.NameLine, Col: f.NameCol},
					Type: fieldType,
				}
			}
		}

	case *ast.ListPattern:
		// Variant literal-attach destructure: `Arr[a, b, ..rest]` against
		// an EnumType. Resolve the prefix to the matching variant and
		// destructure the variant's List payload.
		if et, ok := expectedTy.(*EnumType); ok && n.TypeName != nil {
			pet, payload, vname := c.resolveVariantPrefixOrDot(n.TypeName, et)
			if pet == nil || vname == "" {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"list pattern with type prefix %s requires a list-payload variant or list-distinct scrutinee, got %s",
					n.TypeName.TypeString(), expectedTy))
				for _, head := range n.Heads {
					c.checkPattern(head, nil)
				}
				if n.TailSpread != nil {
					c.checkPattern(n.TailSpread, nil)
				}
				return
			}
			// Bare-prefix list-variant pattern (`Arr[a, b]` against an enum)
			// is rejected post-migration; same rule as the EnumPattern and
			// StructPattern paths above.
			if st, ok := n.TypeName.(*ast.SimpleType); ok && !isPreludeBareVariant(st.Name) {
				c.addError(st.Line, st.Col, bareVariantPatternMessage(st.Name, et.Name))
			}
			if pet.Name != et.Name {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"variant %s belongs to %s, not scrutinee type %s",
					vname, pet.Name, et.Name))
			}
			lt, ok := payload.(*ListType)
			if !ok {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"variant %s does not take a list payload; type-prefixed list pattern requires a variant whose payload is `List<T>`",
					vname))
				for _, head := range n.Heads {
					c.checkPattern(head, nil)
				}
				if n.TailSpread != nil {
					c.checkPattern(n.TailSpread, nil)
				}
				return
			}
			for _, head := range n.Heads {
				c.checkPattern(head, lt.Elem)
			}
			if n.TailSpread != nil {
				c.checkPattern(n.TailSpread, lt)
			}
			return
		}
		if expectedTy == nil {
			for _, head := range n.Heads {
				c.checkPattern(head, nil)
			}
			if n.TailSpread != nil {
				c.checkPattern(n.TailSpread, nil)
			}
			return
		}
		// Unwrap a list-distinct DistinctType to its inner ListType so the
		// pattern can match the underlying list. Type-prefixed list
		// patterns (`Items[a, b]`) additionally verify the prefix agrees
		// with the scrutinee's distinct type.
		actualTy := expectedTy
		if dt, ok := expectedTy.(*DistinctType); ok {
			if _, isList := dt.Inner.(*ListType); isList {
				if n.TypeName != nil && n.TypeName.TypeString() != dt.Name {
					c.addError(n.Line, n.Col, fmt.Sprintf(
						"list pattern prefix %s does not match scrutinee type %s",
						n.TypeName.TypeString(), dt.Name))
				}
				actualTy = dt.Inner
			}
		} else if n.TypeName != nil {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"list pattern with type prefix %s requires a list-distinct scrutinee, got %s",
				n.TypeName.TypeString(), expectedTy))
		}
		lt, ok := actualTy.(*ListType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf("list pattern requires a list type, got %s", expectedTy))
			for _, head := range n.Heads {
				c.checkPattern(head, nil)
			}
			if n.TailSpread != nil {
				c.checkPattern(n.TailSpread, nil)
			}
			return
		}
		for _, head := range n.Heads {
			c.checkPattern(head, lt.Elem)
		}
		if n.TailSpread != nil {
			c.checkPattern(n.TailSpread, actualTy)
		}

	case *ast.MapPattern:
		// Unwrap a map-distinct DistinctType to its inner MapType so the
		// pattern can match the underlying map. Type-prefixed map patterns
		// (`Kvs{"a" => v}`) additionally verify the prefix name agrees
		// with the scrutinee's distinct type.
		actualTy := expectedTy
		// Variant literal-attach destructure: `Obj{"k" => v}` against an
		// EnumType. Resolve the prefix to the matching variant and
		// destructure the variant's Map payload — the variant must agree
		// with the scrutinee's enum.
		if et, ok := expectedTy.(*EnumType); ok && n.TypeName != nil {
			pet, payload, vname := c.resolveVariantPrefixOrDot(n.TypeName, et)
			if pet == nil || vname == "" {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"map pattern with type prefix %s requires a map-payload variant or map-distinct scrutinee, got %s",
					n.TypeName.TypeString(), expectedTy))
				c.checkMapPatternEntries(n.Entries, nil)
				return
			}
			// Bare-prefix map-variant pattern (`Obj{"k" => v}` against an
			// enum) is rejected post-migration; same rule as the other
			// pattern-position paths.
			if st, ok := n.TypeName.(*ast.SimpleType); ok && !isPreludeBareVariant(st.Name) {
				c.addError(st.Line, st.Col, bareVariantPatternMessage(st.Name, et.Name))
			}
			if pet.Name != et.Name {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"variant %s belongs to %s, not scrutinee type %s",
					vname, pet.Name, et.Name))
			}
			mt, ok := payload.(*MapType)
			if !ok {
				c.addError(n.Line, n.Col, fmt.Sprintf(
					"variant %s does not take a map payload; type-prefixed map pattern requires a variant whose payload is `Map<K, V>`",
					vname))
				c.checkMapPatternEntries(n.Entries, nil)
				return
			}
			c.checkMapPatternEntries(n.Entries, mt.Val)
			return
		}
		if dt, ok := expectedTy.(*DistinctType); ok {
			if _, isMap := dt.Inner.(*MapType); isMap {
				if n.TypeName != nil && n.TypeName.TypeString() != dt.Name {
					c.addError(n.Line, n.Col, fmt.Sprintf(
						"map pattern prefix %s does not match scrutinee type %s",
						n.TypeName.TypeString(), dt.Name))
				}
				actualTy = dt.Inner
			}
		} else if n.TypeName != nil {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"map pattern with type prefix %s requires a map-distinct scrutinee, got %s",
				n.TypeName.TypeString(), expectedTy))
		}
		if actualTy == nil {
			c.checkMapPatternEntries(n.Entries, nil)
			return
		}
		mt, ok := actualTy.(*MapType)
		if !ok {
			c.addError(n.Line, n.Col, fmt.Sprintf("map pattern requires a map type, got %s", expectedTy))
			c.checkMapPatternEntries(n.Entries, nil)
			return
		}
		c.checkMapPatternEntries(n.Entries, mt.Val)
	}
}

// checkLiteralPattern requires a literal pattern's type to be the type of
// the value at its position. Literals are monomorphic (`3` is an Int, `"a"` a
// String, `'a'` a Codepoint), so the value's type must equal the literal's:
// `case n { "a" -> ... }` over an Int can never match and is an error. An
// unsolved inference variable at the position is solved to the literal's
// type. text is the literal as the message shows it.
func (c *checker) checkLiteralPattern(text string, line, col int, litTy, expectedTy Type) {
	if expectedTy == nil {
		return
	}
	actual := resolveTypeVar(expectedTy)
	if tv, ok := actual.(*TypeVar); ok {
		tv.Resolved = litTy
		return
	}
	if TypesEqual(actual, litTy) {
		return
	}
	c.addError(line, col, c.typef("pattern %s is %s %s, but the value is %s %s",
		text, articleFor(litTy.String()), litTy, articleFor(actual.String()), actual))
}

// stringPatternText renders a string literal pattern for a diagnostic.
func stringPatternText(n *ast.StringLit) string {
	if n.Raw {
		return "`" + n.Value + "`"
	}
	return strconv.Quote(n.Value)
}

// checkCaseExhaustiveness verifies that a `case x { ... }` covers every value
// of its scrutinee's type, by the constructor-matrix rules in exhaustive.go:
//
//   - Wildcard (`_`) and bare-identifier patterns cover everything.
//   - An enum is covered when every variant is, and a variant when the
//     payload patterns of the arms naming it cover its payload type, so
//     `Some(Ok(_))` + `Some(Err(_))` + `None` covers `Maybe<Result<T, E>>`.
//   - A tuple, struct or distinct value is covered component by component:
//     `(Some(_), _)` + `(None, Some(_))` + `(None, None)` covers
//     `(Maybe<A>, Maybe<B>)`.
//   - A list is covered by length: `[]` + `[h, ..t]` covers every list.
//   - A literal, string prefix or map pattern never covers
//     its type (Int, String, Codepoint, Map, ...); a catch-all must.
//   - Branches with a `when` guard never count toward coverage.
//
// A subject-less case is checked by checkCaseHasCatchAll.
func (c *checker) checkCaseExhaustiveness(n *ast.Case, scrutineeTy Type) {
	if n == nil {
		return
	}
	var patterns []ast.Node
	for _, branch := range n.Branches {
		if branch.Guard != nil || branch.Pattern == nil {
			continue
		}
		patterns = append(patterns, branch.Pattern)
	}
	c.checkPatternCoverage(n.Line, n.Col, n.Value, "case", patterns, scrutineeTy)
}

// checkPatternCoverage reports a pattern set that does not cover every value
// of t, by checkCaseExhaustiveness's rules. what names the construct in the
// message: "case", or "`else`" for a binding's else arms, whose set is the
// binding's own pattern plus the arms'. Guarded arms are left out of patterns
// by the caller: a guard never counts toward coverage.
func (c *checker) checkPatternCoverage(line, col int, subject ast.Node, what string, patterns []ast.Node, t Type) {
	if t == nil {
		return
	}
	// The error spans the construct's head through its subject, `case c`,
	// when the subject ends on the line the construct starts.
	at := func(msg string) TypeError {
		e := TypeError{Line: line, Col: col, Message: msg}
		if sp, ok := spanOf(subject); ok && sp.EndLine == line && col > 0 {
			e.EndLine, e.EndCol = sp.EndLine, sp.EndCol
		}
		return e
	}
	if isScalarPrimitive(t) {
		for _, p := range patterns {
			if isUnconstrainedPattern(p) {
				return
			}
		}
		c.report(at(fmt.Sprintf("non-exhaustive %s: add a `_` arm", what)))
		return
	}
	if TypesEqual(t, TypeCodepoint) {
		// Codepoint literal arms cover 128 of its 1,112,064 values, so a
		// set that tests one needs a catch-all as an Int's does.
		literal := false
		for _, p := range patterns {
			if isUnconstrainedPattern(p) {
				return
			}
			if _, ok := ast.WithoutAs(p).(*ast.CodepointLit); ok {
				literal = true
			}
		}
		if literal {
			c.report(at(fmt.Sprintf("non-exhaustive %s: add a `_` arm", what)))
		}
		return
	}
	t = resolveTypeVar(t)
	enumTy, ok := t.(*EnumType)
	if !ok || len(enumTy.Variants) == 0 {
		w, missing := c.uncoveredValue([]Type{t}, patternRows(patterns))
		if !missing {
			return
		}
		if w[0] == "_" {
			c.report(at(fmt.Sprintf("non-exhaustive %s: add a `_` arm", what)))
			return
		}
		c.report(at(fmt.Sprintf(
			"non-exhaustive %s on %s: missing %s",
			what, t, w[0])).WithHint("add an arm for the missing value, or a `_` arm"))
		return
	}
	if c.patternsExhaustiveOver(enumTy, patterns) {
		return
	}
	subs := enumTypeArgSubs(enumTy)
	var missing []string
	for _, v := range enumTy.Variants {
		if !c.variantCovered(v, subs, patterns) {
			missing = append(missing, v.Name)
		}
	}
	if len(missing) == 0 {
		return
	}
	c.report(at(fmt.Sprintf(
		"non-exhaustive %s on %s: missing %s",
		what, enumTy.Name, strings.Join(missing, ", "))).WithHint("add an arm for each missing variant, or a `_` arm"))
}

// patternRows makes a one-column pattern matrix, one row per pattern.
func patternRows(patterns []ast.Node) [][]ast.Node {
	rows := make([][]ast.Node, len(patterns))
	for i, p := range patterns {
		rows[i] = []ast.Node{p}
	}
	return rows
}

// checkCaseHasCatchAll requires an unguarded catch-all arm where the checker
// cannot enumerate the cases: a subject-less `case` (its conditions are
// arbitrary Bools) and a `case` over a scalar primitive (Int, String, ...),
// whose literal and prefix patterns never cover the type. A binding pattern
// (`n -> ...`) covers everything as `_` does.
func (c *checker) checkCaseHasCatchAll(n *ast.Case, adHoc bool) {
	for _, branch := range n.Branches {
		if branch.Guard != nil {
			continue
		}
		if adHoc {
			if isAdHocCatchAll(branch.Pattern) {
				return
			}
			continue
		}
		if branch.Pattern != nil && isUnconstrainedPattern(branch.Pattern) {
			return
		}
	}
	c.addError(n.Line, n.Col, "non-exhaustive case: add a `_` arm")
}

// isAdHocCatchAll reports whether a subject-less case arm's condition is the
// `_` catch-all.
func isAdHocCatchAll(p ast.Node) bool {
	switch v := p.(type) {
	case *ast.WildcardPattern, *ast.Placeholder:
		return true
	case *ast.Ident:
		return v.Name == "_"
	}
	return false
}

// isScalarPrimitive reports whether t is a primitive whose values no finite
// set of literal patterns covers.
func isScalarPrimitive(t Type) bool {
	switch t {
	case TypeInt, TypeFloat, TypeDecimal, TypeString, TypeByte:
		return true
	}
	return false
}

// patternsExhaustiveOver reports whether the given pattern set covers every
// possible value of type t (see checkCaseExhaustiveness for the rules).
func (c *checker) patternsExhaustiveOver(t Type, patterns []ast.Node) bool {
	_, missing := c.uncoveredValue([]Type{t}, patternRows(patterns))
	return !missing
}

// enumTypeArgSubs builds a TypeParam_ → TypeArg substitution for the enum so
// each variant's DataType is concretized to the use-site instantiation
// (Maybe<Result<Int, String>>'s Some.DataType = T → Result<Int, String>).
func enumTypeArgSubs(enumTy *EnumType) map[*TypeParam_]Type {
	if len(enumTy.TypeParamDefs) == 0 || len(enumTy.TypeArgs) != len(enumTy.TypeParamDefs) {
		return nil
	}
	subs := make(map[*TypeParam_]Type, len(enumTy.TypeParamDefs))
	for i, def := range enumTy.TypeParamDefs {
		subs[def] = enumTy.TypeArgs[i]
	}
	return subs
}

// variantCovered reports whether the patterns naming variant v cover every
// value of it: some pattern names it, and their payload patterns are
// exhaustive over its payload. A struct variant's payload is its fields,
// covered by a pattern that constrains none of them.
func (c *checker) variantCovered(v VariantDef, subs map[*TypeParam_]Type, patterns []ast.Node) bool {
	var sub []ast.Node
	for _, p := range patterns {
		if payload, ok := c.variantPatternPayload(p, v.Name); ok {
			sub = append(sub, payload)
		}
	}
	if len(sub) == 0 {
		return false
	}
	if v.DataType == nil {
		if len(v.Fields) == 0 {
			// Bare variants (no payload type) — any match suffices.
			return true
		}
		for _, p := range sub {
			if isUnconstrainedPattern(p) {
				return true
			}
		}
		return false
	}
	dataType := v.DataType
	if subs != nil {
		dataType = Substitute(dataType, subs)
	}
	return c.patternsExhaustiveOver(dataType, sub)
}

// structPatternNames reports whether the struct pattern n's type name, when
// it writes one, names st, the struct its value has: by the declared name
// (`Point{x, y}`, `geo.Point{x, y}`, `Box{v}` over a `Box<Int>`), or through
// the type the name resolves to (an alias). Otherwise it reports the
// mismatch, or the unknown name, at the pattern. A name nothing declares
// (`Invalissert{name, age}`) was accepted, and the IR builder declined the
// binding.
func (c *checker) structPatternNames(n *ast.StructPattern, st *StructType) bool {
	if n.TypeName == nil {
		return true
	}
	base := structPatternBaseName(n.TypeName)
	if base == "" || base == st.Name {
		return true
	}
	resolved, ok := c.structPatternType(n)
	if !ok {
		return false
	}
	if rs, isStruct := resolveTypeVar(resolved).(*StructType); isStruct && sameNominalIdentity(rs.Origin, rs.Name, st.Origin, st.Name) {
		return true
	}
	c.addError(n.Line, n.Col, fmt.Sprintf("struct pattern names %s, but the value is a %s", n.TypeName.TypeString(), st))
	return false
}

// structPatternTypeKnown reports whether the struct pattern n's type name
// resolves, reporting it at the pattern when it does not.
func (c *checker) structPatternTypeKnown(n *ast.StructPattern) bool {
	_, ok := c.structPatternType(n)
	return ok
}

// structPatternType is the type the struct pattern n's type name resolves
// to. A name that does not resolve is reported at the pattern.
func (c *checker) structPatternType(n *ast.StructPattern) (Type, bool) {
	resolved, err := ResolveTypeExpr(n.TypeName, c.reg, c.fnTypeParams, c.fa.References)
	if err == nil {
		return resolved, true
	}
	if te, isType := err.(TypeError); isType {
		c.report(te)
	} else {
		c.addError(n.Line, n.Col, fmt.Sprintf("unknown type %q", n.TypeName.TypeString()))
	}
	return nil, false
}

// structPatternBaseName is the declared name a struct pattern's type name
// writes, without a module qualifier or type arguments.
func structPatternBaseName(te ast.TypeExpr) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	case *ast.QualifiedType:
		if t.Member != nil {
			return structPatternBaseName(t.Member)
		}
	}
	return ""
}

// checkPatternFieldsUntyped checks the field sub-patterns of a struct
// pattern whose type did not check, so the names they bind are still typed
// (as unknown) and reported once.
func (c *checker) checkPatternFieldsUntyped(n *ast.StructPattern) {
	for _, f := range n.Fields {
		if f.Pattern != nil {
			c.checkPattern(f.Pattern, nil)
		}
	}
}

// variantPatternPayload returns the pattern p matches variant `variant`'s
// payload against, when p names that variant: `.V(x)`'s payload pattern, or a
// struct-variant pattern `.V{a, b}`'s fields as an anonymous struct pattern.
func (c *checker) variantPatternPayload(p ast.Node, variant string) (ast.Node, bool) {
	switch v := ast.WithoutAs(p).(type) {
	case *ast.EnumPattern:
		if c.variantNameOfPattern(v) != variant {
			return nil, false
		}
		return payloadOfVariantPattern(v), true
	case *ast.StructPattern:
		if v.TypeName == nil || variantNameOf(v.TypeName) != variant {
			return nil, false
		}
		fields := *v
		fields.TypeName = nil
		return &fields, true
	case *ast.ListPattern:
		// `.Items[h, ..t]` matches a list-payload variant's list.
		if v.TypeName == nil || variantNameOf(v.TypeName) != variant {
			return nil, false
		}
		list := *v
		list.TypeName = nil
		return &list, true
	case *ast.MapPattern:
		if v.TypeName == nil || variantNameOf(v.TypeName) != variant {
			return nil, false
		}
		entries := *v
		entries.TypeName = nil
		return &entries, true
	}
	return nil, false
}

// variantNameOfPattern returns the canonical (post-alias) variant name for
// an EnumPattern. Resolves through the file's References map and walks the
// `Resolved` chain so that a `Some as Just` import still classifies a `Just(n)`
// pattern as covering the `Some` variant on `Maybe`. Falls back to the bare
// AST name when no resolution is recorded.
func (c *checker) variantNameOfPattern(p *ast.EnumPattern) string {
	var pos Pos
	switch v := p.Variant.(type) {
	case *ast.SimpleType:
		pos = Pos{Line: v.Line, Col: v.Col}
	case *ast.QualifiedType:
		if inner, ok := v.Member.(*ast.SimpleType); ok {
			pos = Pos{Line: inner.Line, Col: inner.Col}
		}
	}
	if pos.Line > 0 && c.fa != nil {
		if sym, ok := c.fa.References[pos]; ok && sym != nil {
			for sym.Resolved != nil {
				sym = sym.Resolved
			}
			if sym.Kind == SymbolEnumVariant {
				return sym.Name
			}
		}
	}
	return variantNameOf(p.Variant)
}

// payloadOfVariantPattern returns the pattern matched against the variant's
// payload value. Bare variants and binding-only payloads are conceptually
// unconstrained — they're modelled as a wildcard so they recurse cleanly.
func payloadOfVariantPattern(p *ast.EnumPattern) ast.Node {
	if p.Binding != "" || p.Payload == nil {
		return &ast.WildcardPattern{Line: p.Line, Col: p.Col}
	}
	return p.Payload
}

// variantNameOf extracts the variant name from a pattern's variant TypeExpr.
// `Some` → "Some"; `Maybe.Some` / `Shape.Circle` → "Some" / "Circle".
func variantNameOf(te ast.TypeExpr) string {
	switch v := te.(type) {
	case *ast.SimpleType:
		return v.Name
	case *ast.QualifiedType:
		if inner, ok := v.Member.(*ast.SimpleType); ok {
			return inner.Name
		}
	case *ast.DotVariantType:
		return v.Name
	}
	return ""
}

// isUnconstrainedPattern reports whether a pattern node imposes no
// constraint on the value being matched — i.e. whether it would also
// match any other value of the same type. Used recursively to decide
// whether a destructuring payload covers its outer variant.
func isUnconstrainedPattern(p ast.Node) bool {
	switch v := ast.WithoutAs(p).(type) {
	case nil:
		return true
	case *ast.WildcardPattern, *ast.IdentPattern:
		return true
	case *ast.TuplePattern:
		for _, elem := range v.Patterns {
			if !isUnconstrainedPattern(elem) {
				return false
			}
		}
		return true
	case *ast.StructPattern:
		// Anonymous struct destructuring with no per-field literal
		// constraints — typed-shape struct patterns also fall through
		// here; the only thing that constrains is a sub-pattern.
		for _, f := range v.Fields {
			if f.Pattern != nil && !isUnconstrainedPattern(f.Pattern) {
				return false
			}
		}
		return true
	}
	// EnumPattern, literal nodes, ListPattern, MapPattern — all constrained.
	return false
}

func (c *checker) checkCase(n *ast.Case, expected Type) Type {
	var scrutineeTy Type
	if n.Value != nil {
		scrutineeTy = c.checkNode(n.Value)
	} else {
		// A subject-less case written as a value, not the right side of a
		// pipe (which checkPipe checks with the piped type): its arms are
		// conditions.
		c.checkCaseHasCatchAll(n, true)
		for _, branch := range n.Branches {
			if branch.Pattern != nil && !isAdHocCatchAll(branch.Pattern) {
				c.checkCondition(branch.Pattern, "case condition")
			}
		}
	}
	return c.checkCaseWithScrutineeType(n, scrutineeTy, expected)
}

// checkCondition checks cond, a subject-less case arm's condition or a
// `when` guard, as a Bool. what names it in the message.
func (c *checker) checkCondition(cond ast.Node, what string) {
	ty := c.checkNodeExpecting(cond, TypeBool)
	if ty != nil && !TypesEqual(ty, TypeBool) && !isInfallibleLike(ty) {
		c.addErrorAt(cond, fmt.Sprintf("%s must be Bool, got %s", what, ty))
	}
}

func (c *checker) checkCaseWithScrutineeType(n *ast.Case, scrutineeTy Type, expected Type) Type {
	c.checkCaseExhaustiveness(n, scrutineeTy)

	var resultTy Type
	for _, branch := range n.Branches {
		if branch.Pattern != nil && scrutineeTy != nil {
			c.checkPattern(branch.Pattern, scrutineeTy)
			c.notePatternBindings(patternSites(branch.Pattern), n.Value)
		}
		var bodyTy Type
		narrowSym, narrowTy := c.narrowingFor(n.Value, scrutineeTy, branch.Pattern)
		c.withNarrowing(narrowSym, narrowTy, func() {
			if branch.Guard != nil {
				c.checkCondition(branch.Guard, "`when` guard")
			}
			bodyTy = c.checkNodeExpecting(branch.Body, expected)
		})
		if bodyTy == nil {
			continue
		}
		if resultTy == nil || isInfallibleLike(resultTy) {
			// A diverging arm (`return`, `todo`) says nothing about the
			// case's type, so the first arm that produces a value does.
			resultTy = bodyTy
			continue
		}
		if isInfallibleLike(bodyTy) {
			continue
		}
		// A branch whose body type is a bare TypeParam_ (e.g. binding from a
		// pattern against an underconstrained scrutinee type) is compatible
		// with any concrete type the other branches produce — pick the more
		// informative result.
		if _, ok := resultTy.(*TypeParam_); ok {
			resultTy = bodyTy
			continue
		}
		if _, ok := bodyTy.(*TypeParam_); ok {
			continue
		}
		// Try unify first so fresh TypeVars bind across sibling branches
		// (e.g. `Some(v) -> Some(v)` typed as `Maybe<?α>` resolves α from a
		// sibling's `Maybe<Int>`). Fall back to TypesEqual's leniency around
		// unparameterized struct/enum names.
		if err := c.unify(resultTy, bodyTy, nil); err != nil {
			if !TypesEqual(resultTy, bodyTy) {
				// An earlier arm's `Ok(v)` leaves the error type open
				// (`Result<Tx, ?4>`); the case's expected type closes it, so
				// the message names the type the author declared.
				if expected != nil && containsTypeVar(resultTy) {
					_ = c.unify(expected, resultTy, nil)
				}
				c.report(errAt(valueExpr(branch.Body), c.typef(
					"case branch type mismatch: expected %s, got %s", resultTy, bodyTy)).WithHint(c.wholeResultHint(resultTy, bodyTy, valueExpr(branch.Body))).WithHint(embedsDowncastHint(resultTy, bodyTy)))
			}
			continue
		}
		resultTy = branchJoin(resultTy, bodyTy)
	}
	if resultTy != nil && !isInfallibleLike(resultTy) {
		for _, branch := range n.Branches {
			c.settleTodoTail(branch.Body, resultTy)
		}
	}
	c.registerControlFlowHover(n.Line, n.Col, "case", scrutineeTy, resultTy, scrutineeTy != nil, false)
	return resultTy
}

// settleTodoTail records ty as the type expected of a `todo` that is the value
// of body, an arm of an `if` or `case` whose type its other arms decided. The
// arm was checked before that type was known, so the `todo` had nothing to
// fit; the IR builder reads this record to give the trap's destination the
// arm's kind. A `todo` already checked against a type keeps it.
func (c *checker) settleTodoTail(body ast.Node, ty Type) {
	if ty == nil || isInfallibleLike(ty) {
		return
	}
	switch n := body.(type) {
	case *ast.Todo:
		if _, known := c.fa.ExpectedTypes[n]; !known {
			c.fa.recordExpectedType(n, ty)
		}
	case *ast.GroupedExpr:
		c.settleTodoTail(n.Expr, ty)
	case *ast.ExprStmt:
		c.settleTodoTail(n.Expr, ty)
	case *ast.Block:
		if len(n.Stmts) > 0 {
			c.settleTodoTail(n.Stmts[len(n.Stmts)-1], ty)
		}
	case *ast.If:
		c.settleTodoTail(n.Then, ty)
		if n.Else != nil {
			c.settleTodoTail(n.Else, ty)
		}
	case *ast.Case:
		for _, branch := range n.Branches {
			c.settleTodoTail(branch.Body, ty)
		}
	}
}

// instantiateUnboundCalleeParams replaces each type parameter in t that is
// not one of `keep` (the caller's own type params, matched by declaration,
// not by name) with a fresh TypeVar, one per parameter: `Pair<T, T>` becomes
// `Pair<?1, ?1>`. Used at generic-call sites: callee TypeParam_s left unbound
// after argument-driven unification become holes that downstream context can
// resolve. A callee's `T` and the caller's own `T` are different parameters,
// so only the caller's survives.
func instantiateUnboundCalleeParams(t Type, keep map[string]*TypeParam_, c *checker) Type {
	if t == nil {
		return nil
	}
	fresh := map[*TypeParam_]Type{}
	for _, tp := range collectOrderedTypeParams(&FuncType{Return: t}) {
		if existing, ok := keep[tp.Name_]; ok && existing == tp {
			continue
		}
		fresh[tp] = c.freshTypeVar()
	}
	return Substitute(t, fresh)
}

// openUnsolvedCalleeParams replaces each unbounded type parameter in t that
// is not the enclosing function's own with an inference variable, reusing the
// one open already holds for it.
func (c *checker) openUnsolvedCalleeParams(t Type, open map[*TypeParam_]Type) Type {
	if t == nil || !c.containsForeignTypeParam(t) {
		return t
	}
	for _, tp := range collectOrderedTypeParams(&FuncType{Return: t}) {
		if _, ok := open[tp]; ok || len(tp.Bounds) > 0 {
			continue
		}
		if existing, ok := c.fnTypeParams[tp.Name_]; ok && existing == tp {
			continue
		}
		open[tp] = c.freshTypeVar()
	}
	return Substitute(t, open)
}

func containsProtectedTypeParam(t Type, protected map[string]*TypeParam_) bool {
	if t == nil || len(protected) == 0 {
		return false
	}
	switch tt := t.(type) {
	case *TypeParam_:
		if p, ok := protected[tt.Name_]; ok && p == tt {
			return true
		}
	case *ListType:
		return containsProtectedTypeParam(tt.Elem, protected)
	case *MapType:
		return containsProtectedTypeParam(tt.Key, protected) || containsProtectedTypeParam(tt.Val, protected)
	case *TupleType:
		for _, elem := range tt.Elems {
			if containsProtectedTypeParam(elem, protected) {
				return true
			}
		}
	case *FuncType:
		for _, param := range tt.Params {
			if containsProtectedTypeParam(param, protected) {
				return true
			}
		}
		return containsProtectedTypeParam(tt.Return, protected)
	case *StructType:
		for _, arg := range tt.TypeArgs {
			if containsProtectedTypeParam(arg, protected) {
				return true
			}
		}
	case *EnumType:
		for _, arg := range tt.TypeArgs {
			if containsProtectedTypeParam(arg, protected) {
				return true
			}
		}
	case *InterfaceType:
		for _, arg := range tt.TypeArgs {
			if containsProtectedTypeParam(arg, protected) {
				return true
			}
		}
	case *DistinctType:
		if containsProtectedTypeParam(tt.Inner, protected) {
			return true
		}
		for _, arg := range tt.TypeArgs {
			if containsProtectedTypeParam(arg, protected) {
				return true
			}
		}
	case *PartialType:
		return containsProtectedTypeParam(tt.Inner, protected)
	}
	return false
}

// collectTypeParamsByName walks a FuncType and returns a name → TypeParam_
// map of every TypeParam_ the signature declares — used to make those
// TypeParam_s available when resolving annotations inside the function body
// (e.g. on lambda params) and to resolve a `T.member` qualifier.
//
// The params and the return are the usual sources. The WHERE BOUNDS are a
// third, and omitting them was a soundness hole rather than a nicety: a
// parameter mentioned in neither a param nor the return is still declared and
// still dispatchable, so `fn f<T>(): String where T: Machine { T.nope() }`
// found no `T` to resolve against, both validators returned "not a type
// parameter", `nomi check` said ok and the program died on
// `unknown builtin 'T.nope'`, while the same function with
// `T` in a parameter position was rejected at compile time. The bound's
// `Param` pointer is the same declaration the rest of the map carries.
func collectTypeParamsByName(ft *FuncType) map[string]*TypeParam_ {
	out := map[string]*TypeParam_{}
	var walk func(t Type)
	walk = func(t Type) {
		switch tt := t.(type) {
		case *TypeParam_:
			if _, exists := out[tt.Name_]; !exists {
				out[tt.Name_] = tt
			}
		case *ListType:
			walk(tt.Elem)
		case *MapType:
			walk(tt.Key)
			walk(tt.Val)
		case *TupleType:
			for _, e := range tt.Elems {
				walk(e)
			}
		case *FuncType:
			for _, p := range tt.Params {
				walk(p)
			}
			walk(tt.Return)
		case *StructType:
			for _, a := range tt.TypeArgs {
				walk(a)
			}
		case *EnumType:
			for _, a := range tt.TypeArgs {
				walk(a)
			}
		case *InterfaceType:
			for _, a := range tt.TypeArgs {
				walk(a)
			}
		case *DistinctType:
			// A generic opaque type (Vector<T>, Channel<T>) carries its
			// parameter in TypeArgs, as collectOrderedTypeParams reads it.
			for _, a := range tt.TypeArgs {
				walk(a)
			}
			if tt.Inner != nil {
				walk(tt.Inner)
			}
		}
	}
	for _, p := range ft.Params {
		walk(p)
	}
	walk(ft.Return)
	for _, wb := range ft.WhereBounds {
		walk(wb.Param)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// declaredTypeParamsByName is collectTypeParamsByName plus the names the
// DECLARATION writes that the signature never mentions — `fn f<T>(): String`,
// where `T` appears in no parameter, no return and no `where` clause, so
// nothing ever minted a TypeParam_ for it.
//
// Such a parameter is unusable except through a turbofish, and it was the
// third shape of one soundness hole: `fn f<T>(): String { T.nope() }` passed
// `nomi check` and died on `unknown builtin 'T.nope'`, because
// both `T.member` validators start by asking whether the qualifier is a type
// parameter in scope and the answer was no. The minted parameter is
// unbounded, which is exactly what the declaration says, and the qualifier
// then gets the has-no-interface-bound diagnostic the referenced shapes get.
func declaredTypeParamsByName(ft *FuncType, declared []ast.TypeParam) map[string]*TypeParam_ {
	out := collectTypeParamsByName(ft)
	for _, tp := range declared {
		if tp.Name == "" {
			continue
		}
		if _, exists := out[tp.Name]; exists {
			continue
		}
		if out == nil {
			out = map[string]*TypeParam_{}
		}
		out[tp.Name] = &TypeParam_{Name_: tp.Name}
	}
	return out
}

// collectOrderedTypeParams returns the function's type parameters in
// first-appearance order across the signature (each parameter type left to
// right, then the return type). This equals declaration order for every
// signature whose params appear in the same order they're declared — the
// common case, including return-only params like `new<T>(): Channel<T>`. The
// only divergence is exotic out-of-order declarations (`fn f<B, A>(x: A): B`),
// which turbofish would bind by appearance rather than declaration. Used to map
// turbofish type args onto the callee's type params.
func collectOrderedTypeParams(ft *FuncType) []*TypeParam_ {
	seen := map[*TypeParam_]bool{}
	var out []*TypeParam_
	var walk func(t Type)
	walk = func(t Type) {
		switch tt := t.(type) {
		case *TypeParam_:
			if !seen[tt] {
				seen[tt] = true
				out = append(out, tt)
			}
		case *ListType:
			walk(tt.Elem)
		case *MapType:
			walk(tt.Key)
			walk(tt.Val)
		case *TupleType:
			for _, e := range tt.Elems {
				walk(e)
			}
		case *FuncType:
			for _, p := range tt.Params {
				walk(p)
			}
			walk(tt.Return)
		case *StructType:
			for _, a := range tt.TypeArgs {
				walk(a)
			}
		case *EnumType:
			for _, a := range tt.TypeArgs {
				walk(a)
			}
		case *InterfaceType:
			for _, a := range tt.TypeArgs {
				walk(a)
			}
		case *DistinctType:
			// Generic opaque externs (Channel<T>, Task<T>) carry their params
			// in TypeArgs; primitive distinct wrappers carry an Inner.
			for _, a := range tt.TypeArgs {
				walk(a)
			}
			if tt.Inner != nil {
				walk(tt.Inner)
			}
		case *AnonStructType:
			for _, f := range tt.Fields {
				walk(f.Type)
			}
		}
	}
	for _, p := range ft.Params {
		walk(p)
	}
	walk(ft.Return)
	return out
}

// widenForDefault widens narrow singleton primitives to their enclosing enum
// when used as a default value seeding a type parameter. Specifically,
// `True`/`False` widen to `Bool` — a default of `False` for an accumulator
// means "starts at False," not "must always be False," so the body is
// allowed to assign True via `break True` or the fall-through path.
func widenForDefault(t Type) Type {
	if t == TypeTrue || t == TypeFalse {
		return TypeBool
	}
	return t
}

// canPinFromDefault reports whether the default's inferred type is
// "concrete enough" to pin a caller's TypeParam_. Concrete here means no
// `Unit` placeholder and no `TypeParam_` lurking inside — both signal that
// the default is shape-only (`[]` → `List<Unit>`, `None` → `Maybe<T>`)
// and the body's references will determine the real type.
//
// Examples:
//   - `5` → Int — concrete, pin.
//   - `(0, 1)` → (Int, Int) — concrete, pin (loop tuple state).
//   - `[]` → List<Unit> — Unit placeholder, don't pin.
//   - `None` → Maybe<T> — TypeParam_ inside, don't pin.
//   - `(0, [])` → (Int, List<Unit>) — Unit inside, don't pin.
func canPinFromDefault(t Type) bool {
	if t == nil {
		return false
	}
	if ContainsTypeParam(t) {
		return false
	}
	return !containsUnit(t)
}

// containsUnit walks t looking for `Unit` as a leaf — which the checker uses
// as a placeholder when an element type is unknown (empty list, empty map).
func containsUnit(t Type) bool {
	switch tt := t.(type) {
	case *PrimitiveType:
		return tt == TypeUnit
	case *ListType:
		return containsUnit(tt.Elem)
	case *MapType:
		return containsUnit(tt.Key) || containsUnit(tt.Val)
	case *TupleType:
		for _, e := range tt.Elems {
			if containsUnit(e) {
				return true
			}
		}
		return false
	case *FuncType:
		for _, p := range tt.Params {
			if containsUnit(p) {
				return true
			}
		}
		return containsUnit(tt.Return)
	case *StructType:
		for _, a := range tt.TypeArgs {
			if containsUnit(a) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, a := range tt.TypeArgs {
			if containsUnit(a) {
				return true
			}
		}
		return false
	}
	return false
}

// checkLambda checks a lambda no function type is expected for. With
// undetermined set, an unannotated parameter with no default is an error:
// nothing gives it a type.
func (c *checker) checkLambda(n *ast.Lambda, undeterminedErr bool) Type {
	undetermined := false
	// `try` and `return` inside a lambda unwind to the lambda boundary,
	// not the enclosing fn. Nomi has no non-local returns.
	prevBoundary := c.tryBoundary
	c.tryBoundary = "lambda"
	defer func() { c.tryBoundary = prevBoundary }()
	defer c.enterBoundaryExits()()

	params := make([]Type, len(n.Params))
	for i, p := range n.Params {
		if p.Destructure != nil {
			// Destructuring lambda param with no contextual type: derive from
			// the annotation or self-typing head, reject refutable patterns,
			// then type the inner bindings. (When the lambda's type IS driven
			// by context, checkLambdaExpecting handles it instead.)
			ty, err := paramPatternType(p, c.reg, c.fnTypeParams, c.fa)
			if err != nil {
				if te, ok := err.(TypeError); ok {
					c.errors = append(c.errors, te)
				}
				c.checkPattern(p.Destructure, nil)
				continue
			}
			params[i] = ty
			recordLambdaPatternType(c.fa, p.Destructure, ty)
			checkParamPatternRefutable(p.Destructure, ty, c.reg, c.addError)
			c.checkPattern(p.Destructure, ty)
			c.checkParamDefault(p, ty)
			continue
		}
		if p.TypeAnnotation != nil {
			resolved, err := ResolveTypeExpr(p.TypeAnnotation, c.reg, c.fnTypeParams, c.fa.References)
			if err == nil {
				params[i] = resolved
				// Attach type to param symbol.
				attachParamType(c.fa, p.Name, p.Line, p.Col, resolved)
				c.checkParamDefault(p, resolved)
			} else if te, ok := err.(TypeError); ok {
				// An annotation naming no type is an error, not a
				// parameter of unknown type that takes any argument.
				c.errors = append(c.errors, te)
			}
		} else if p.Default != nil {
			// No annotation — infer the param's type from the default value,
			// but only when the default's shape is "concrete enough" — see
			// canPinFromDefault. Defaults like `[]` and `None` carry placeholder
			// types (List<Unit>, Maybe<TypeParam_>) and would over-narrow the
			// param; defaults like `5`, `(0, 1)`, `False` are fine.
			if defTy := c.checkParamDefaultValue(p); canPinFromDefault(defTy) {
				defTy = widenForDefault(defTy)
				params[i] = defTy
				attachParamType(c.fa, p.Name, p.Line, p.Col, defTy)
			}
		} else if undeterminedErr {
			// No annotation, no default, and no function type expected
			// here: nothing determines the parameter's type, so neither its
			// uses nor the lambda's own type can be checked. A later use of
			// the lambda (`case Some(|x| ...) { Some(h) -> f(h) }`) does
			// not fix it; the checker has no variable for it to solve.
			c.addError(p.Line, p.Col, fmt.Sprintf("cannot infer type for parameter %s", p.Name))
			undetermined = true
		}
	}

	prevReturnTy := c.returnTy
	c.returnTy = nil
	errsBefore := len(c.errors)
	bodyTy := c.checkLambdaBody(n, nil)
	c.returnTy = prevReturnTy
	if bodyTy == nil {
		if len(c.errors) > errsBefore {
			// The body has no type because it has an error: the lambda's
			// type is unknown too, not a Unit-returning function, which
			// would draw a second, follow-on mismatch.
			return nil
		}
		bodyTy = TypeUnit
	}

	if undetermined {
		// Reported at the parameter. The result, computed from a parameter
		// of no type, is unknown too, so a use of the lambda draws no
		// follow-on mismatch over it.
		bodyTy = nil
	}
	return &FuncType{Params: params, Return: bodyTy, DefaultCount: lambdaDefaultCount(n.Params)}
}

// checkParamDefault checks a parameter's default value against the
// parameter's type, which an annotation or the expected callback type fixed
// before the default was looked at. Checking it is what records the
// default's references (`c: Count = Count.zero` names an owner-level `once`),
// and what rejects a default of the wrong type, which would otherwise reach
// the body as a value of a type its parameter does not have.
// checkExternFunc checks a `host fn` or `go` binding, which has no body:
// each parameter's default is checked against the parameter's type, as a
// `fn`'s is.
func (c *checker) checkExternFunc(n *ast.ExternFunc) {
	sym := c.fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
	if sym == nil {
		sym = c.fa.ModuleScope.Lookup(n.Name)
	}
	if sym == nil {
		return
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || len(ft.Params) != len(n.Params) {
		return
	}
	prevTP := c.fnTypeParams
	c.fnTypeParams = declaredTypeParamsByName(ft, n.TypeParams)
	defer func() { c.fnTypeParams = prevTP }()
	for i, p := range n.Params {
		c.checkParamDefault(p, ft.Params[i])
	}
}

// checkParamDefaultValue checks a parameter's default with no expected type.
func (c *checker) checkParamDefaultValue(p ast.Param) Type {
	return c.checkExitless(paramDefaultExitless(p.Name), func() Type {
		return c.checkNode(p.Default)
	})
}

func (c *checker) checkParamDefault(p ast.Param, declared Type) {
	if p.Default == nil || declared == nil {
		return
	}
	defTy := c.checkExitless(paramDefaultExitless(p.Name), func() Type {
		return c.checkNodeExpecting(p.Default, declared)
	})
	if defTy == nil || c.argMatchesParam(defTy, declared, c.recPos(p.Line, p.Col), RecordingKindInterfaceTypedParam) {
		return
	}
	c.addError(p.Line, p.Col, c.typef(
		"default value for parameter '%s' is %s, expected %s", p.Name, defTy, declared))
}

// lambdaDefaultCount returns the total number of params with a default
// value across `params`. Lambdas, like fns, can carry defaults on any
// param (spec §5 Default Arguments) — the count drives the call-site
// arg-count check.
func lambdaDefaultCount(params []ast.Param) int {
	n := 0
	for _, p := range params {
		if p.Default != nil {
			n++
		}
	}
	return n
}

// checkLambdaExpecting checks a lambda against an expected FuncType,
// inferring parameter types for unannotated params from the expected type.
func (c *checker) checkLambdaExpecting(n *ast.Lambda, expected *FuncType) Type {
	// Same boundary push as checkLambda — `try` inside the body unwinds
	// to this lambda, not the enclosing fn.
	prevBoundary := c.tryBoundary
	c.tryBoundary = "lambda"
	defer func() { c.tryBoundary = prevBoundary }()
	defer c.enterBoundaryExits()()

	// Zero-arg lambda passed where the expected callback takes N>0 params:
	// coerce to accept-and-ignore. The body is checked against
	// expected.Return.
	if len(n.Params) == 0 && len(expected.Params) > 0 {
		// Zero-arg lambda ignores the state parameter. For a loop-shaped
		// `(S) -> S` callback, S is output-only — infer it from the body's
		// `break <value>` (via iterBreakTy), defaulting to Unit only when
		// nothing pins it (e.g. `loop(|| break)` solves S to Unit), mirroring
		// how a zero-param `Task.spawn(|| body)` infers its type param from the
		// body.
		subs := map[*TypeParam_]Type{}
		var stateVars []*TypeVar
		for _, p := range expected.Params {
			tp, ok := p.(*TypeParam_)
			if !ok {
				continue
			}
			v := c.freshTypeVar()
			subs[tp] = v
			stateVars = append(stateVars, v)
		}
		expectedReturn := Substitute(expected.Return, subs)
		if expected.Return != nil {
			prev := c.iterBreakTy
			c.iterBreakTy = expectedReturn
			defer func() { c.iterBreakTy = prev }()
		}
		prevReturnTy := c.returnTy
		c.returnTy = expectedReturn
		errsBefore := len(c.errors)
		bodyTy := c.checkLambdaBody(n, lambdaTailExpected(expectedReturn))
		c.returnTy = prevReturnTy
		if bodyTy == nil {
			if len(c.errors) > errsBefore {
				return nil // unknown, as checkLambda answers
			}
			bodyTy = TypeUnit
		}
		// A state var that no `break <value>` pinned defaults to Unit, so
		// `loop(|| break)` and infinite side-effect loops stay Unit-stated.
		// A divergent (break) body stays Infallible, which unifies against
		// the loop's `S` return; S is carried by the stateVar.
		for _, v := range stateVars {
			if resolveTypeVar(v) == v {
				v.Resolved = TypeUnit
			}
		}
		params := make([]Type, len(expected.Params))
		for i, p := range expected.Params {
			tp, isTP := p.(*TypeParam_)
			if !isTP {
				params[i] = p
				continue
			}
			params[i] = resolveTypeVar(subs[tp])
		}
		return &FuncType{Params: params, Return: bodyTy, DefaultCount: lambdaDefaultCount(n.Params)}
	}

	// Determine effective param count: exclude trailing default params
	// that exceed the expected arity (e.g. { n = 5 -> ... } matching () -> Unit).
	effectiveCount := len(n.Params)
	if len(n.Params) > len(expected.Params) {
		for i := len(n.Params) - 1; i >= len(expected.Params); i-- {
			if n.Params[i].Default != nil {
				effectiveCount--
			} else {
				break
			}
		}
	}

	params := make([]Type, effectiveCount)
	// open holds the inference variable standing for each unsolved callee
	// type parameter, one per parameter for the whole lambda, so `|a, b|`
	// against `(U, U) -> U` gives both parameters and the result one variable.
	open := map[*TypeParam_]Type{}
	for i := 0; i < effectiveCount; i++ {
		p := n.Params[i]
		// defaultChecked is set where the default was already checked to pin
		// a type parameter, so it is not checked (and reported) twice.
		defaultChecked := false
		if p.TypeAnnotation != nil {
			// Explicit annotation — resolve with the enclosing function's
			// type-param map so references like `T` inside lambda annotations
			// (e.g. `|state: (Int, List<T>) = (0, []), …|` inside
			// `fn drop<T>(...)`)  resolve to the same TypeParam_ used in the
			// outer signature.
			resolved, err := ResolveTypeExpr(p.TypeAnnotation, c.reg, c.fnTypeParams, c.fa.References)
			if err == nil {
				params[i] = resolved
				if p.Destructure != nil {
					recordLambdaPatternType(c.fa, p.Destructure, resolved)
					checkParamPatternRefutable(p.Destructure, resolved, c.reg, c.addError)
					c.checkPattern(p.Destructure, resolved)
				} else {
					attachParamType(c.fa, p.Name, p.Line, p.Col, resolved)
				}
			} else if te, ok := err.(TypeError); ok {
				c.errors = append(c.errors, te)
			}
		} else if i < len(expected.Params) && expected.Params[i] != nil {
			paramTy := expected.Params[i]
			// When expected param is an unsolved type param and we have a
			// default value, use the default's type for a concrete binding.
			if _, isTP := paramTy.(*TypeParam_); isTP && p.Default != nil {
				// Only pin the caller's TypeParam_ from defaults whose type
				// is concrete enough (see canPinFromDefault). Defaults like
				// `[]` or `None` carry placeholder shapes (`List<Unit>`,
				// `Maybe<TypeParam_>`) that would over-narrow the param;
				// defaults like `5`, `(0, 1)`, `False` are fine.
				defTy := c.checkParamDefaultValue(p)
				defaultChecked = true
				if canPinFromDefault(defTy) {
					paramTy = widenForDefault(defTy)
				}
			}
			// A callee type parameter nothing has solved yet (reduce's
			// accumulator `U` in `Iter.reduce(xs, |a, b| a + b)`) is a hole
			// this call fills, not a rigid parameter of the lambda: the
			// lambda's body solves it. Typed as the callee's own `U`, `a + b`
			// was rejected as "type parameter U cannot use `+`" and the call
			// came out as `U`. The enclosing function's own parameters and a
			// bounded parameter (whose bound the body may lean on) stay.
			if p.Default == nil {
				paramTy = c.openUnsolvedCalleeParams(paramTy, open)
			}
			params[i] = paramTy
			if te := unnamedParam(p, c.fa, c.reg); te != nil {
				// `|String| 1` is a parameter written as its type, as
				// checkLambda and a declared function report it. Checked
				// as a pattern against the expected type it was "enum
				// pattern requires an enum type, got String", which names
				// a pattern the author did not mean to write.
				c.errors = append(c.errors, *te)
				c.checkPattern(p.Destructure, nil)
			} else if p.Destructure != nil {
				// Destructuring param: push the expected type into the pattern
				// so each bound identifier gets the right element type. Without
				// this, bindings like `k` in `{ (k, _) -> ... }` end up with a
				// nil Type and the body's Ident lookup defaults to Unit.
				recordLambdaPatternType(c.fa, p.Destructure, paramTy)
				checkParamPatternRefutable(p.Destructure, paramTy, c.reg, c.addError)
				c.checkPattern(p.Destructure, paramTy)
			} else {
				attachParamType(c.fa, p.Name, p.Line, p.Col, paramTy)
			}
		}
		if !defaultChecked && params[i] != nil {
			c.checkParamDefault(p, params[i])
		}
	}

	// Check default value expressions for extra params and infer their types.
	for i := effectiveCount; i < len(n.Params); i++ {
		p := n.Params[i]
		if p.Default != nil {
			defTy := c.checkParamDefaultValue(p)
			if defTy != nil {
				attachParamType(c.fa, p.Name, p.Line, p.Col, defTy)
			}
		}
	}

	// Compute the `break v` expected type for this callback — expected.Return
	// with any TypeParam_s that were concretized via param defaults/annotations
	// substituted in. `loop(|n = 5| …)` sees iterBreakTy = Int (S bound from
	// the default); a bare `|x| …` without a default leaves iterBreakTy as S.
	expectedReturn := expected.Return
	if expected.Return != nil {
		subs := map[*TypeParam_]Type{}
		for i := 0; i < len(params) && i < len(expected.Params); i++ {
			if params[i] == nil || expected.Params[i] == nil {
				continue
			}
			if tp, ok := expected.Params[i].(*TypeParam_); ok {
				subs[tp] = params[i]
			}
		}
		expectedReturn = Substitute(Substitute(expected.Return, subs), open)
		prev := c.iterBreakTy
		c.iterBreakTy = expectedReturn
		defer func() { c.iterBreakTy = prev }()
	}

	prevReturnTy := c.returnTy
	c.returnTy = expectedReturn
	errsBefore := len(c.errors)
	bodyTy := c.checkLambdaBody(n, lambdaTailExpected(expectedReturn))
	c.returnTy = prevReturnTy
	if bodyTy == nil {
		if len(c.errors) > errsBefore {
			return nil // unknown, as checkLambda answers
		}
		bodyTy = TypeUnit
	}

	// If the body type has unsolved type params (e.g. Ok returns Result<String, E>)
	// and we have an expected return type, unify to solve them. We skip when
	// bodyTy is itself a bare TypeParam_ — that means the body was a single
	// reference to a type-parameterised binding (e.g. a lambda body that
	// returns `k` whose type is the caller's outer type param), and unifying
	// it against the callee's expected TypeParam_ would clobber the caller's
	// name with the callee's.
	if _, bodyIsTP := bodyTy.(*TypeParam_); expected.Return != nil && !bodyIsTP && ContainsTypeParam(bodyTy) {
		subs := map[*TypeParam_]Type{}
		_ = c.unifyInto(expected.Return, bodyTy, subs)
		if len(subs) > 0 {
			bodyTy = Substitute(bodyTy, subs)
		}
	}

	return &FuncType{Params: params, Return: bodyTy, DefaultCount: lambdaDefaultCount(n.Params)}
}

func (c *checker) checkStructLit(n *ast.StructLit) Type {
	// Dot-leading struct-variant literal-attach (`.Rect{w: 4.0, h: 3.0}`).
	// The TypeName carries a *ast.DotVariantType; resolution proceeds
	// against c.expectedEnum staged by checkNodeExpecting. Same diagnostics
	// as the call-form / list-form dot-leading paths.
	if dvt, ok := n.TypeName.(*ast.DotVariantType); ok {
		if c.expectedEnum == nil {
			c.errors = append(c.errors, TypeError{
				Line:    dvt.Line,
				Col:     dvt.Col,
				Message: dotVariantNoEnumMessage(dvt.Name, c.expectedAt),
			})
			for _, f := range n.Fields {
				c.checkNode(f.Value)
			}
			return nil
		}
		et := c.expectedEnum
		var vd *VariantDef
		for i := range et.Variants {
			if et.Variants[i].Name == dvt.Name {
				vd = &et.Variants[i]
				break
			}
		}
		if vd == nil {
			c.errors = append(c.errors, TypeError{
				Line:    dvt.Line,
				Col:     dvt.Col,
				Message: "no variant '" + dvt.Name + "' on enum " + et.Name,
			}.WithHint(didYouMean(dvt.Name, variantNames(et))))
			for _, f := range n.Fields {
				c.checkNode(f.Value)
			}
			return nil
		}
		// Stash the resolved enum so the IR builder can construct the right
		// struct variant.
		dvt.ResolvedEnum = et.Name
		c.recordDotVariantReference(dvt.Name, dvt.Line, dvt.Col, et)
		// Struct-variant field validation, generic inference and the field
		// labels' hover all live in checkStructVariantLit, shared with the
		// qualified spellings. This branch used to carry its own copy of the
		// field walk, which is how it came to validate names and types while
		// never inferring an argument.
		return c.checkStructVariantLit(n, et, dvt.Name, dvt.Line, dvt.Col)
	}

	// Struct update by spread — `{..base, field: value}`. The head carries
	// the result type, so this cannot infer a shape the way the plain
	// anonymous literal below does.
	if n.TypeName == nil && n.Spread != nil {
		return c.checkStructUpdateLit(n)
	}

	// Anonymous struct literal — infer field types.
	if n.TypeName == nil {
		fields := make([]FieldDef, 0, len(n.Fields))
		seen := make(map[string]bool, len(n.Fields))
		for _, f := range n.Fields {
			// Spec §4: struct fields are ALWAYS snake_case, with no
			// nominal/anonymous distinction. checkNamingConventions walks
			// top-level decls only, and an anon struct's fields live in an
			// EXPRESSION, so they were never visited — `{userName: 1}` and
			// `{ok_PRED: 2}` type-checked and ran. The second spelling is the
			// one that bites: internal/irbuild spells a trailing `?` as a
			// `_PRED` suffix, so an unchecked `ok_PRED` beside `ok?` is two
			// Nomi names arriving at one spelling there.
			c.requireSnakeCase(f.Name, f.Line, f.Col, "anon struct field name")
			ty := c.checkNode(f.Value)
			if ty == nil {
				ty = TypeUnit
			}
			if seen[f.Name] {
				c.addError(f.Line, f.Col, fmt.Sprintf("duplicate field '%s' in anon struct literal", f.Name))
				continue
			}
			seen[f.Name] = true
			fields = append(fields, FieldDef{Name: f.Name, Type: ty})
			// Register a SymbolField at the field name's position so hover
			// shows `x: Int`. There's no declaration to point at — the anon
			// struct's fields are inferred from the literal — so the Symbol
			// owns its own Pos and Type.
			// A punned label (`{x}`) is also the read of `x`, which is
			// what References keeps there.
			if f.Line > 0 {
				sym := &Symbol{
					Name: f.Name,
					Kind: SymbolField,
					Pos:  Pos{Line: f.Line, Col: f.Col},
					Type: ty,
				}
				if IsPunnedField(f) {
					c.fa.setPunnedFieldLabel(sym.Pos, sym)
				} else {
					c.fa.References[sym.Pos] = sym
				}
			}
		}
		return &AnonStructType{Fields: fields}
	}
	resolved, err := ResolveTypeExpr(n.TypeName, c.reg, nil, c.fa.References)
	if err != nil {
		// Enum-qualified struct variant: `Error.HttpError{...}`, and with the
		// parser's module-qualified chain, `random.Error.OsEntropy{...}` — the
		// head is the enum's whole dotted name either way, so one lookup
		// serves both. This is the branch that validated nothing; it now goes
		// through the same validator as every other spelling.
		if qt, ok := n.TypeName.(*ast.QualifiedType); ok {
			if enumTy, isEnum := c.reg.Lookup(qt.Module).(*EnumType); isEnum && enumTy != nil {
				name, line, col := "", n.Line, n.Col
				if sm, ok := qt.Member.(*ast.SimpleType); ok {
					name, line, col = sm.Name, sm.Line, sm.Col
				}
				return c.checkStructVariantLit(n, enumTy, name, line, col)
			}
			// A qualified head that resolves to something other than an enum
			// keeps the old shape: walk the values and hand back whatever it
			// resolved to, rather than inventing a diagnostic here.
			if other := c.reg.Lookup(qt.Module); other != nil {
				for _, f := range n.Fields {
					c.checkNode(f.Value)
				}
				return other
			}
		}
		// Nested struct types live in block-scoped registries that c.reg
		// (module-level) can't see. The builder already attached a reference
		// at the TypeName's position; consult it before giving up.
		if qt, ok := n.TypeName.(*ast.QualifiedType); ok {
			if st, ok := qt.Member.(*ast.SimpleType); ok {
				pos := Pos{Line: st.Line, Col: st.Col}
				if sym, ok := c.fa.References[pos]; ok {
					real := sym
					if real.Resolved != nil {
						real = real.Resolved
					}
					if real.Type == nil {
						return nil
					}
					if real.Kind == SymbolEnumVariant {
						// Same validator; the enum comes off the
						// constructor's return type rather than the registry.
						if et := enumOfVariantSymbol(real); et != nil {
							return c.checkStructVariantLit(n, et, real.Name, st.Line, st.Col)
						}
						for _, f := range n.Fields {
							c.checkNode(f.Value)
						}
						if ft, ok := real.Type.(*FuncType); ok && ft.Return != nil {
							return ft.Return
						}
						return real.Type
					}
					resolved = real.Type
					err = nil
				}
			}
		}
		if st, ok := n.TypeName.(*ast.SimpleType); ok {
			pos := Pos{Line: st.Line, Col: st.Col}
			if sym, ok := c.fa.References[pos]; ok {
				real := sym
				if real.Resolved != nil {
					real = real.Resolved
				}
				if real.Type == nil {
					return nil
				}
				// Reject bare struct-variant construction in the same file:
				// `enum Error { HttpError{status: Int, ...} | ... }` requires
				// `Error.HttpError{...}`, never bare `HttpError{...}`. This
				// mirrors checkTypeIdent's rule for positional/nullary forms;
				// without it the user got a confusing function-type mismatch
				// instead of the qualified-form hint. Imported variants
				// (Resolved != nil) and prelude variants (Pos not in this
				// file's Definitions) stay valid.
				if sym.Resolved == nil && sym.Kind == SymbolEnumVariant {
					if def, hasDef := c.fa.Definitions[sym.Pos]; hasDef && def == sym {
						enumName := c.findEnumForVariant(sym.Name)
						hint := ""
						if enumName != "" {
							hint = "; use '" + enumName + "." + sym.Name + "{...}'"
						}
						c.errors = append(c.errors, TypeError{
							Line:    st.Line,
							Col:     st.Col,
							Message: "variant '" + sym.Name + "' must be qualified through its enum" + hint,
						})
						// Best-effort: still walk fields so downstream checks
						// see the value types; return the variant's enclosing
						// enum so the binding/return type-check can proceed
						// cleanly past this error.
						for _, f := range n.Fields {
							c.checkNode(f.Value)
						}
						if ft, ok := sym.Type.(*FuncType); ok && ft.Return != nil {
							return ft.Return
						}
						return nil
					}
				}
				if real.Kind == SymbolEnumVariant {
					// An imported variant spelled bare
					// (`import std/supervisors.Backoff.Exponential`, then
					// `Exponential{}`): the literal builds the variant, as
					// the qualified spelling does, not its constructor.
					if et := enumOfVariantSymbol(real); et != nil {
						return c.checkStructVariantLit(n, et, real.Name, st.Line, st.Col)
					}
				}
				resolved = real.Type
				err = nil
			}
		}
		if err != nil {
			// An unresolvable struct-literal type name must be reported
			// here. If the checker gave up silently, `Bogus{a: 1}` would
			// type-check clean, and so would a bare
			// `Counter{value: -999, increments: 1}` naming an opaque struct
			// in a module-imported sibling, while execution resolved the
			// bare name and built a real `Counter`. Reporting the
			// resolution failure is what makes the opacity guard below
			// reachable.
			if te, ok := err.(TypeError); ok {
				c.errors = append(c.errors, te)
			} else {
				c.addError(n.Line, n.Col, err.Error())
			}
			for _, f := range n.Fields {
				c.checkNode(f.Value)
			}
			return nil
		}
	}

	st, isStruct := resolved.(*StructType)
	if !isStruct {
		// The head resolved to something that is not a struct — a type alias
		// onto a non-struct, an unbuilt type. There is no field shape to
		// judge against, so walk the values for their own errors and hand
		// back whatever it resolved to, as this branch always has.
		for _, f := range n.Fields {
			c.checkNode(f.Value)
		}
		return resolved
	}

	// Opaque-types: constructing an opaque struct via struct literal
	// from outside its owning module exposes the field representation.
	// Block it; the owner module exports a smart-constructor function
	// for outside callers. Mirrors checkOpaqueConstructor for distinct
	// types. It stays here rather than moving into the shared validator
	// because it is about the literal's HEAD, which the constructor-call
	// form spells differently and guards at its own call site
	// (checkStructCallForm).
	if st.Opaque && (c.fa == nil || c.fa.FilePath != st.OwningSourceFile) {
		line, col := n.Line, n.Col
		if simp, ok := n.TypeName.(*ast.SimpleType); ok {
			line, col = simp.Line, simp.Col
		} else if qt, ok := n.TypeName.(*ast.QualifiedType); ok {
			if sm, ok := qt.Member.(*ast.SimpleType); ok {
				line, col = sm.Line, sm.Col
			}
		}
		c.addError(line, col, fmt.Sprintf(
			"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", st.Name))
	}

	return c.checkStructLitAgainstStruct(n, st, false)
}

// findFieldType returns the declared type of the named field, or nil if absent.
func findFieldType(fields []FieldDef, name string) Type {
	for i := range fields {
		if fields[i].Name == name {
			return fields[i].Type
		}
	}
	return nil
}

// variantStructFields returns the field list a struct-variant literal is
// checked against. An `embeds` variant carries its shape as a *StructType in
// DataType rather than in Fields, and both spellings construct with braces.
func variantStructFields(vd *VariantDef) []FieldDef {
	if len(vd.Fields) > 0 {
		return vd.Fields
	}
	if st, ok := vd.DataType.(*StructType); ok {
		return st.Fields
	}
	return nil
}

// variantDefNamed returns the named variant of an enum, or nil.
func variantDefNamed(et *EnumType, name string) *VariantDef {
	for i := range et.Variants {
		if et.Variants[i].Name == name {
			return &et.Variants[i]
		}
	}
	return nil
}

// checkStructVariantLit IS THE VALIDATOR for a struct-variant literal, for
// every spelling that reaches one: `Enum.Variant{...}`, `mod.Enum.Variant{...}`,
// the dot-leading `.Variant{...}`, and the block-scoped fallback that resolves
// the head through a SymbolEnumVariant reference. It validates the field
// names, checks each field VALUE against its declared type, infers the enum's
// type arguments when the site does not supply them, and returns the enum
// instantiated with whatever it solved.
//
// IT EXISTS BECAUSE THERE WAS NO SHARED VALIDATOR AND THE PATHS HAD DIVERGED.
// The dot-leading branch validated field names and field
// types but never inferred an argument, so it was correct only under an
// annotation that supplied one. The `Enum.Variant{...}` branch did NONE of the
// three — it walked each value with a bare checkNode and returned the enum's
// declared type — so it accepted anything at all. The rows that size that:
//
//	Shape.Wrap{inner: "x"}   where `inner: Int`, NON-GENERIC
//	  -> zero diagnostics, AND IT RAN, printing `Wrap{inner: "x"}`
//	Shape.Wrap{bogus: 3}
//	  -> zero diagnostics; caught only at run time, as a trap
//	take(x: Shape<String>) fed Shape.Wrap{inner: 3}
//	  -> zero diagnostics, where `Box`'s literal path reports
//	     `expected Box<String>, got Box<Int>`
//
// So this was never a generics defect and never a hover cosmetic. Generic
// inference is one of three things the branch omitted, and the non-generic
// field-type hole is the one that let a String live in a field declared Int.
//
// THE SEED ARM IS NOT THE INFERENCE ARM, and conflating them is what made the
// annotated form report a useless message. When the site already knows the
// arguments — a dot-leading literal under `s: Shape<String>`, or an expected
// type naming this same enum — those arguments WIN and the fields are checked
// against them, so `s: Shape<String> = Shape.Wrap{inner: 3}` now faults
// `inner`. At the twin it reported `type mismatch: expected Shape<String>, got
// Shape`: an error, but only from comparing the annotation against the BARE
// enum, naming neither `inner` nor `Int`, and identical for every wrong value.
// Inference runs only when nothing seeded it, which is why both directions are
// now reported in their own terms.
//
// MISSING FIELDS ARE NOW REPORTED, which reverses what this header said. The
// stated reason was that adding the rule here alone would put a third
// convention beside two existing ones. The count was wrong: the two
// constructor-call record forms already reported missing fields with the
// phrasing `missing required field 'v' in Box construction`. So there was one convention with
// two holes in it — this path and the struct literal-attach path — and both
// are closed now, in the same words.
func (c *checker) checkStructVariantLit(n *ast.StructLit, et *EnumType, variantName string, nameLine, nameCol int) Type {
	vd := variantDefNamed(et, variantName)
	if vd == nil {
		for _, f := range n.Fields {
			c.checkNode(f.Value)
		}
		c.report(TypeError{Line: nameLine, Col: nameCol, Message: "no variant '" + variantName + "' on enum " + et.Name}.WithHint(didYouMean(variantName, variantNames(et))))
		return nil
	}
	fields := variantStructFields(vd)

	// Seed from arguments the site already supplies: the resolved enum's own
	// TypeArgs first, then an expected type naming THIS enum. The name guard
	// is what keeps `s: Maybe<Int> = Shape.Wrap{inner: 3}` from seeding
	// Shape's parameter out of Maybe's argument list.
	subs := map[*TypeParam_]Type{}
	seeded := false
	if len(et.TypeParamDefs) > 0 {
		args := et.TypeArgs
		if len(args) != len(et.TypeParamDefs) {
			if exp := c.expectedEnum; exp != nil && exp.Name == et.Name && len(exp.TypeArgs) == len(et.TypeParamDefs) {
				args = exp.TypeArgs
			}
		}
		if len(args) == len(et.TypeParamDefs) {
			for i, def := range et.TypeParamDefs {
				subs[def] = args[i]
			}
			seeded = true
		}
	}
	canInferArgs := len(et.TypeParamDefs) > 0 && !seeded

	provided := make(map[string]bool, len(n.Fields))
	for _, f := range n.Fields {
		declared := findFieldType(fields, f.Name)
		if declared == nil {
			c.checkNode(f.Value)
			c.addError(f.Line, f.Col, fmt.Sprintf(
				"no field '%s' on variant %s of enum %s", f.Name, variantName, et.Name))
			continue
		}
		provided[f.Name] = true
		// Push the declared type with everything solved so far, exactly as
		// the struct path does: a lambda field gets its parameters from the
		// field's signature, and a field mentioning an already-solved
		// parameter is checked against the solution rather than against `T`.
		want := Substitute(declared, subs)
		valTy := c.checkNodeExpecting(f.Value, want)
		if valTy == nil {
			continue
		}
		// THE FIELD VALUE IS CHECKED, and this is the arm that closes the
		// soundness hole rather than the hover. `unify` is the mechanism
		// checkBinding uses for the same question, so interface-typed and
		// type-parameter fields stay lenient for the same reasons they do
		// there; a bare TypesEqual would reject both.
		//
		// When nothing seeded the substitution, unify against the RAW
		// declared type so `inner: T` BINDS T from the value. When the site
		// supplied the arguments, unify against the SUBSTITUTED type so
		// `inner: T` with T already String rejects an Int. Two operands, one
		// call: the seeded map makes `want` concrete and the unbound map
		// leaves it a parameter.
		operand := declared
		if !canInferArgs {
			operand = want
		}
		if err := c.unifyInto(operand, valTy, subs); err != nil {
			c.addMismatch(f.Line, f.Col, want, valTy, c.typef("field '%s' type mismatch: expected %s, got %s", f.Name, want, valTy))
		}
	}

	// EVERY NON-DEFAULTED FIELD MUST BE SUPPLIED — the same rule the struct
	// path applies, in the same words, with the variant as the construction
	// subject. This function's header used to decline it on the ground that
	// adding it here alone would put a third convention beside two existing
	// ones. That reading was wrong about the count: the two constructor-call
	// record forms already required it, with this exact phrasing. The path
	// that had no rule was the struct literal, and it
	// has one now, so this is the fourth site of one rule rather than a third
	// convention.
	for _, vf := range fields {
		if provided[vf.Name] || vf.HasDefault {
			continue
		}
		c.addError(nameLine, nameCol, fmt.Sprintf(
			"missing field '%s' of %s.%s", vf.Name, et.Name, variantName))
	}

	// The field LABEL's hover, once the substitution exists — the same
	// re-recording the struct path needs, for the same reason, through the
	// same function. At the twin `Shape.Wrap{inner: 3}` hovered `inner: _`
	// while `Box{value: 1}` hovered `value: Int`.
	c.recordLitFieldLabelTypes(n, fields, subs)

	if len(et.TypeParamDefs) == 0 || len(subs) == 0 {
		return et
	}
	args := make([]Type, len(et.TypeParamDefs))
	for i, def := range et.TypeParamDefs {
		if t, ok := subs[def]; ok {
			args[i] = t
		} else {
			args[i] = def
		}
	}
	return &EnumType{
		Origin:           et.Origin,
		Name:             et.Name,
		Variants:         et.Variants,
		TypeParams:       et.TypeParams,
		TypeParamDefs:    et.TypeParamDefs,
		TypeArgs:         args,
		Opaque:           et.Opaque,
		OwningSourceFile: et.OwningSourceFile,
	}
}

// enumOfVariantSymbol returns the enum a SymbolEnumVariant constructs. A
// struct variant's symbol carries the constructor's FuncType, whose Return is
// the enum; a bare variant carries the enum directly.
func enumOfVariantSymbol(sym *Symbol) *EnumType {
	if sym == nil {
		return nil
	}
	if ft, ok := sym.Type.(*FuncType); ok {
		if et, ok := ft.Return.(*EnumType); ok {
			return et
		}
		return nil
	}
	et, _ := sym.Type.(*EnumType)
	return et
}

// recordStructLitFieldTypes re-records the reference at each of a struct
// literal's field LABELS with the field's INSTANTIATED type, so hovering the
// label inside `Box{value: 1}` reads `value: Int` rather than `value: _`.
//
// WHY NOT THE BUILDER. `registerStructLitFieldRefs` (builder.go) is what
// creates these references, and it points each label at the struct's own
// field declaration symbol. For a generic struct that symbol's Type is the
// unbound type parameter, so every generic struct literal in the repo
// hovered `_`: `Box{value: 1}` gave `value: _`, `Pair{left: 1, right: "x"}`
// gave `left: _` and `right: _`, `App{config: load()}` gave `config: _`, and
// a field declared `inner: Box<T>` gave `inner: Box<_>`. A non-generic
// `Point{x: 1}` was already right, which is what localises the fault to
// substitution rather than to the reference. The builder cannot do better:
// the literal names no type arguments, so the field's instantiated type comes
// from unifying each VALUE's inferred type against the declared field type,
// and that is type inference — it runs in the checker, after the builder.
//
// NO EXPLICIT-ARGUMENT ARM, deliberately. A first draft also derived `subs`
// from `st.TypeArgs` when the resolved struct already carried a full argument
// list. That branch is unreachable and was removed rather than shipped
// untested: `parseStructLit` is only ever handed a `*ast.SimpleType`,
// `*ast.QualifiedType` or `*ast.DotVariantType` (parser.go:2301, 2342, 2386,
// 2405), so `Box<Int>{...}` has no spelling — it parses as a comparison — and
// both synthesized literals (`derive_synthesis.go:3415,3471`) use
// `*ast.SimpleType` too. A `typealias IntBox Box<Int>` does resolve to an
// instantiated struct, but `registerStructLitFieldRefs` records nothing for
// `IntBox{value: 1}` — the alias symbol is not `SymbolStruct` — so there is
// no reference for this function to re-record and its label hovers nothing at
// all, measured identical at the base twin. Flipping the arm to `if false`
// changed no test, which is what identified it as dead.
//
// A COPY, NOT A MUTATION, for the reason recordAppFieldReference states: the
// References map is keyed per SITE and this symbol is not, so writing an
// instantiated type into the declaration symbol mints a symbol that claims to
// be the declaration while carrying a type the declaration does not have. The
// copy keeps Name and Pos, which is what routes go-to-definition —
// definition.go:624 matches `parent.Members[name].Pos` BY VALUE — so the jump
// still lands on the field declaration.
//
// Fields whose substitution is the identity are left alone rather than copied:
// Substitute returns its argument unchanged when no type parameter occurs in
// it, so `tag: String` beside `value: T` keeps the shared declaration symbol
// and the non-generic case stays byte-identical.
//
// IT TAKES A FIELD LIST, NOT A *StructType, because an ENUM's struct-variant
// literal needs exactly this and needed it for exactly this reason:
// `Shape.Wrap{inner: 3}` hovered `inner: _` while
// `Box{value: 1}` hovered `value: Int`. The fix for the struct half
// declined the enum half on the ground that the checker concluded bare `Shape`
// for the literal, so writing `inner: Int` would have reported a type nothing
// had derived. checkStructVariantLit now derives it, which is what makes the
// same recording correct here — one implementation, two callers.
//
// IT ALSO MINTS, for the one caller whose labels have no declaration
// reference to copy. `registerStructLitFieldRefs` returns immediately when
// `n.TypeName == nil` (builder.go:3757) — "anonymous struct literal — no
// declaration to point at" — so the constructor-call record form
// `Cfg({port: 80})` reaches here with nothing at any label position. That
// form used to register its own `SymbolField` inline in its field loop,
// carrying the RAW declared type; for a generic struct that is the `value: _`
// defect this function exists to fix, one path later. Minting here instead
// fixes it, because here the substitution is solved.
//
// Presence decides which branch runs, rather than a flag, because presence is
// the actual difference: a label the builder pointed at a declaration must
// keep that Pos for go-to-definition, and a label it skipped has no Pos to
// keep.
func (c *checker) recordLitFieldLabelTypes(n *ast.StructLit, fields []FieldDef, subs map[*TypeParam_]Type) {
	if c.fa == nil || c.fa.References == nil || len(fields) == 0 {
		return
	}
	for _, f := range n.Fields {
		if f.Line == 0 {
			continue
		}
		declared := findFieldType(fields, f.Name)
		if declared == nil {
			continue
		}
		instantiated := Substitute(declared, subs)
		pos := Pos{Line: f.Line, Col: f.Col}
		// A punned label's field lives in PunnedFieldLabels; References
		// there holds the variable and is left alone.
		labels := c.fa.References
		if IsPunnedField(f) {
			if c.fa.PunnedFieldLabels == nil {
				c.fa.PunnedFieldLabels = map[Pos]*Symbol{}
			}
			labels = c.fa.PunnedFieldLabels
		}
		declSym, ok := labels[pos]
		if !ok || declSym == nil {
			labels[pos] = &Symbol{
				Name: f.Name,
				Kind: SymbolField,
				Pos:  pos,
				Type: instantiated,
			}
			continue
		}
		// Fields whose substitution is the identity keep the shared
		// declaration symbol, so the non-generic case stays byte-identical.
		if instantiated == declared {
			continue
		}
		site := *declSym
		site.Type = instantiated
		labels[pos] = &site
	}
}

// recordAnonLitFieldLabels points each label of an anonymous struct literal
// checked against an expected anonymous struct TYPE at that type's field, so
// `logger:` in `{logger: "x", context}` returned from a function declared
// `: {logger: String, context: Context}` is a reference to the `logger` the
// return type names: Find References lists it and rename renames it.
// checkStructLit has already given each label a symbol of its own, since a
// literal with no expected type declares its fields itself; this replaces it
// where a declaration exists.
//
// The expected field's declaration is its Line/Col (FieldDef), where
// resolveTypeExpr recorded a SymbolField in the declaring file's References.
// Only a declaration in THIS file is linked (anonFieldDecl): a position alone
// does not say which file declared it, and a label pointed at another file's
// position with no SourceFile would send go-to-definition to the same line and
// column here.
//
// A nested anonymous literal whose field is declared with an anonymous type
// is linked the same way. The literal's type checking is unchanged: this
// only records references.
func (c *checker) recordAnonLitFieldLabels(n *ast.StructLit, expected *AnonStructType) {
	if c.fa == nil || c.fa.References == nil {
		return
	}
	for _, f := range n.Fields {
		if f.Line == 0 || IsSynthesizedLine(f.Line) {
			continue
		}
		var decl *FieldDef
		for i := range expected.Fields {
			if expected.Fields[i].Name == f.Name {
				decl = &expected.Fields[i]
				break
			}
		}
		if decl == nil {
			continue
		}
		if inner, ok := resolveTypeVar(decl.Type).(*AnonStructType); ok {
			if lit := anonStructLitOf(f.Value); lit != nil {
				c.recordAnonLitFieldLabels(lit, inner)
			}
		}
		sym := c.anonFieldDecl(*decl)
		if sym == nil {
			continue
		}
		pos := Pos{Line: f.Line, Col: f.Col}
		if IsPunnedField(f) {
			c.fa.setPunnedFieldLabel(pos, sym)
		} else {
			c.fa.References[pos] = sym
		}
	}
}

// anonFieldDecl returns the symbol recorded where an anonymous struct type
// written in this file declares field f, or nil when f has no such
// declaration here (an inferred field, or a type written in another file).
// Its Type is f's type at this use, which differs from the declared one only
// under generic substitution; the copy keeps Name and Pos, which is what
// references and go-to-definition match on.
func (c *checker) anonFieldDecl(f FieldDef) *Symbol {
	if f.Line == 0 || c.fa == nil {
		return nil
	}
	sym := c.fa.References[Pos{Line: f.Line, Col: f.Col}]
	if sym == nil || sym.Kind != SymbolField || sym.Name != f.Name || sym.Pos != (Pos{Line: f.Line, Col: f.Col}) {
		return nil
	}
	if f.Type != nil && sym.Type != f.Type {
		cp := *sym
		cp.Type = f.Type
		return &cp
	}
	return sym
}

// anonStructLitOf returns node as a plain anonymous struct literal (no type
// name, no spread), looking through parentheses, or nil.
func anonStructLitOf(node ast.Node) *ast.StructLit {
	for {
		g, ok := node.(*ast.GroupedExpr)
		if !ok {
			break
		}
		node = g.Expr
	}
	lit, ok := node.(*ast.StructLit)
	if !ok || lit.TypeName != nil || lit.Spread != nil {
		return nil
	}
	return lit
}

// typeImplementsInterface reports whether the given value type implements
// the named interface. Returns true if:
//   - the value type is itself the interface (or a type parameter / variable
//     / unknown — these are deferred to runtime),
//   - the value type's concrete name is registered as implementing the
//     interface in this file's local Impls or the project-wide union
//     (c.fa.ProjectImpls.Impls, built by buildProjectImplIndex over
//     filesByKey — covers stdlib + every reachable sibling).
func (c *checker) typeImplementsInterface(valTy Type, ifaceName string, recPos Pos, recKind RecordingKind) bool {
	valTy = resolveTypeVar(valTy)
	if valTy == nil {
		return true
	}
	// Universal default Debug — satisfied by every type (see the free
	// function typeImplementsInterface above and the design doc). No
	// manifest demand recorded; Debug registers eagerly/universally.
	if ifaceName == "Debug" {
		return true
	}
	// Interface-typed values, type parameters, and type variables are
	// existentially typed — treat as "implements anything" (runtime resolves).
	switch t := valTy.(type) {
	case *InterfaceType, *TypeParam_, *TypeVar:
		return true
	case *PrimitiveType:
		if t == TypeAny || t == TypeInfallible {
			return true
		}
	}
	// Universal struct interface — satisfied structurally by every struct
	// (named or anonymous) and nothing else (see the free-function
	// typeImplementsInterface above). Checked before the concreteTypeName==""
	// rejection so anonymous structs, which have no nominal name, still qualify.
	if ifaceName == "Struct" {
		return isStructShaped(valTy)
	}
	name := concreteTypeName(valTy)
	if name == "" {
		// Unknown concrete type (e.g. function, tuple) — no impl possible.
		return false
	}
	// Both arms below stamp the receiver's nominal identity onto the
	// recording, for recordConformanceRecording's reason: ImplManifest is
	// keyed by bare name and the supply side has to be able to tell which
	// declaration the demand was for. See missing_impl_identity.go.
	recOrigin := OriginUnresolved
	if origin, ok := nominalOrigin(valTy); ok {
		recOrigin = origin
	}
	if ifaces, ok := c.fa.Impls[name]; ok {
		if ifaces[ifaceName] {
			if !c.implReceiverMatches(valTy, ifaceName) {
				return false
			}
			c.fa.RecordManifest(name, ifaceName, Recording{Pos: recPos, Kind: recKind, TypeOrigin: recOrigin})
			c.recordImplReceiverBoundConformances(valTy, ifaceName, recPos, recKind)
			return true
		}
	}
	if c.fa != nil && c.fa.ProjectImpls != nil {
		if ifaces, ok := c.fa.ProjectImpls.Impls[name]; ok {
			if ifaces[ifaceName] {
				if !c.implReceiverMatches(valTy, ifaceName) {
					return false
				}
				c.fa.RecordManifest(name, ifaceName, Recording{Pos: recPos, Kind: recKind, TypeOrigin: recOrigin})
				c.recordImplReceiverBoundConformances(valTy, ifaceName, recPos, recKind)
				return true
			}
		}
	}
	return false
}

// recordConformanceIfConcrete records a (typeName, ifaceName) entry in
// the file's ImplManifest when t resolves to a concrete type. Existential
// types (*InterfaceType, *TypeParam_, *TypeVar) are skipped — the
// concrete type isn't statically known and the runtime will resolve
// dispatch when the value materializes. Used by every conformance-check
// site that records which (Type, Iface) pairs the program demands, where the
// recording is unconditional given a
// concrete type (i.e. the impl-existence check happens elsewhere). Sites
// that gate recording on a successful impl-table lookup keep their
// direct RecordManifest calls.
func (c *checker) recordConformanceIfConcrete(t Type, ifaceName string, recPos Pos, recKind RecordingKind) {
	c.recordConformanceRecording(t, ifaceName, Recording{Pos: recPos, Kind: recKind})
}

// recordConformanceRecording is recordConformanceIfConcrete with a
// caller-built Recording, for sites that attach extra provenance —
// the operator sites set Recording.Op so the missing-impl diagnostic
// can name the operator token.
func (c *checker) recordConformanceRecording(t Type, ifaceName string, rec Recording) {
	if c == nil || c.fa == nil || ifaceName == "" {
		return
	}
	resolved := resolveTypeVar(t)
	switch resolved.(type) {
	case *InterfaceType, *TypeParam_, *TypeVar:
		return
	}
	if kind, unorderable := orderingUnsupportedKind(resolved); unorderable && ifaceName == "Comparable" {
		// A tuple or anonymous struct has no declaration to hang an `impl
		// Comparable` on and no nominal name for the manifest, so a demand
		// for one (`List.compare` over a List of tuples, `Iter.sort` of
		// them, `Comparable.compare` on two) would be dropped here and
		// found missing only when the program ran. The ordering operator
		// reports its own operand; this reports every other route.
		msg := fmt.Sprintf("no impl of `Comparable` for `%s`; %s types cannot implement `Comparable`", resolved, kind)
		for _, e := range c.errors {
			if e.Line == rec.Pos.Line && e.Col == rec.Pos.Col && e.Message == msg {
				// One site can demand the same pair twice (an operator and
				// the container impl it reaches).
				return
			}
		}
		c.addError(rec.Pos.Line, rec.Pos.Col, msg)
		return
	}
	if name := concreteTypeName(resolved); name != "" {
		// The demand's nominal identity. ImplManifest is keyed by bare name,
		// so without this a demand for calendar.Error is indistinguishable
		// from one for random.Error and DetectMissingImpls lets the first
		// borrow the second's conformance. See missing_impl_identity.go.
		if origin, ok := nominalOrigin(resolved); ok {
			rec.TypeOrigin = origin
		}
		c.fa.RecordManifest(name, ifaceName, rec)
		c.recordImplReceiverBoundConformances(resolved, ifaceName, rec.Pos, rec.Kind)
	}
}

func (c *checker) recordImplReceiverBoundConformances(concrete Type, ifaceName string, recPos Pos, recKind RecordingKind) {
	if c == nil || concrete == nil || ifaceName == "" {
		return
	}
	name := interfaceImplName(concrete)
	if name == "" {
		return
	}
	info := lookupImplTypeArgs(name, ifaceName, c.implTypeArgsContext())
	if info == nil || info.Receiver == nil {
		return
	}
	subs := map[*TypeParam_]Type{}
	if unifyFull(info.Receiver, concrete, subs, c.implsContext(), c.implTypeArgsContext()) != nil {
		return
	}
	for tp, boundConcrete := range subs {
		for _, bound := range tp.Bounds {
			c.recordBoundConformance(boundConcrete, bound.Name, recPos, recKind)
		}
	}
}

// recordInterfaceConformanceFromParam walks `paramTy` looking for any
// *InterfaceType node. For each interface position, if the corresponding
// position in `argTy` is a concrete type, records (concrete, iface) in
// the file's ImplManifest. This is the generic-call counterpart to the
// concrete-arg-into-interface-param recording argMatchesParam performs
// for monomorphic calls — needed because checkGenericCall bypasses
// argMatchesParam and unifies directly, leaving the manifest entry
// unrecorded for the most common shape (`Iter.reduce(s: String, ...)`
// where reduce's first param is the bare interface `Iter<T>`).
//
// Top-level shape only (paramTy is *InterfaceType): record (argTy,
// iface). Container shapes (`List<Iter<T>>` etc.) aren't on any
// stdlib API today; if they arise, extend this helper to descend into
// matching positions of paramTy / argTy in lockstep.
func (c *checker) recordInterfaceConformanceFromParam(argTy, paramTy Type, recPos Pos, recKind RecordingKind) {
	if c == nil || c.fa == nil || paramTy == nil || argTy == nil {
		return
	}
	if iface, ok := paramTy.(*InterfaceType); ok {
		c.recordConformanceIfConcrete(argTy, iface.Name, recPos, recKind)
	}
}

// recordBoundConformance records (typeName, ifaceName) for `t` and
// recurses into its TypeArgs (for generic containers like List<E>,
// Map<K, V>, generic host/opaque types like Vector<E> / Set<E>, and
// enum types like Result<T, E> / Maybe<T>) so the manifest demands
// (T, Iface) for every type the container's stdlib impl
// will dispatch through. Existential types at any level skip via
// recordConformanceIfConcrete.
//
// The *DistinctType arm covers a generic `host type` / `opaque type`,
// which is how `Vector<T>` and `Set<T>` are declared. It was missing,
// and the omission was NOT specific to any one recording site: with it
// absent, `#["a"] < #["b"]` (the ordering-operator recorder) and
// `Iter.sort([#["a"], #["b"]])` (the declared-`<T: Comparable>`-bound
// recorder) BOTH recorded (Comparable, Vector) and stopped, so both
// faulted at dispatch on the element type. Only TypeArgs are walked,
// not Inner: a newtype's impl delegating to its representation is a
// different rule, and widening the demand set is how a recorded pair
// with no impl becomes a false DetectMissingImpls report.
func (c *checker) recordBoundConformance(t Type, ifaceName string, recPos Pos, recKind RecordingKind) {
	c.recordBoundConformanceRecording(t, ifaceName, Recording{Pos: recPos, Kind: recKind})
}

// recordBoundConformanceRecording is recordBoundConformance with a
// caller-built Recording, for the operator sites that attach extra
// provenance (Recording.Op, so the missing-impl diagnostic can name the
// operator token). Splitting it out is what lets the ORDERING operator
// recurse without losing its `Op`: `#["a"] < #["b"]` desugars to
// Comparable.compare on Vector<String>, whose std impl body raises the
// Comparable demand at the bare element `T`, so the element pair is as
// necessary here as at any other Comparable site.
func (c *checker) recordBoundConformanceRecording(t Type, ifaceName string, rec Recording) {
	if c == nil || c.fa == nil || ifaceName == "" {
		return
	}
	resolved := resolveTypeVar(t)
	c.recordConformanceRecording(resolved, ifaceName, rec)
	if !boundConformanceRecurses(ifaceName) {
		return
	}
	switch ty := resolved.(type) {
	case *ListType:
		c.recordBoundConformanceRecording(ty.Elem, ifaceName, rec)
	case *MapType:
		c.recordBoundConformanceRecording(ty.Key, ifaceName, rec)
		c.recordBoundConformanceRecording(ty.Val, ifaceName, rec)
	case *EnumType:
		for _, arg := range ty.TypeArgs {
			c.recordBoundConformanceRecording(arg, ifaceName, rec)
		}
	case *StructType:
		for _, arg := range ty.TypeArgs {
			c.recordBoundConformanceRecording(arg, ifaceName, rec)
		}
	case *DistinctType:
		for _, arg := range ty.TypeArgs {
			c.recordBoundConformanceRecording(arg, ifaceName, rec)
		}
	}
}

func boundConformanceRecurses(ifaceName string) bool {
	switch ifaceName {
	case "Debug", "Display", "Equatable", "Hashable", "Comparable":
		return true
	default:
		return false
	}
}

// interfacesDeclaringMethod returns the sorted names of interfaces `typeName`
// impl whose contract declares a method `method` — required methods and
// defaults (open or final) count; inherent ops do not (they aren't contract
// methods, so they're absent from InterfaceType.Methods). Backs type-qualified
// resolution of inherited defaults (spec §13) and the *Function Name
// Collisions* ambiguity check
// when two implemented interfaces declare the same method name.
func (c *checker) interfacesDeclaringMethod(typeName, method string) []string {
	if c == nil || c.fa == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	consider := func(ifaces map[string]bool) {
		for iface := range ifaces {
			if seen[iface] {
				continue
			}
			it, ok := c.reg.Lookup(iface).(*InterfaceType)
			if !ok || it == nil {
				continue
			}
			for _, m := range it.Methods {
				if m.Name == method {
					seen[iface] = true
					out = append(out, iface)
					break
				}
			}
		}
	}
	consider(c.fa.Impls[typeName])
	if c.fa.ProjectImpls != nil {
		consider(c.fa.ProjectImpls.Impls[typeName])
	}
	sort.Strings(out)
	return out
}

// interfacesWritingMethod is interfacesDeclaringMethod widened to the
// interfaces this file cannot name: an impl in a third file, of an
// interface this file never imported, still gives `Type.method` a second
// meaning. Such an interface counts when an impl of it for this receiver
// writes the method, the receiver compared by its declaring module
// (QualifiedReceiver) so another file's same-named type does not.
func (c *checker) interfacesWritingMethod(typeName, method string) []string {
	out := c.interfacesDeclaringMethod(typeName, method)
	idx := c.fa.ProjectImpls
	if idx == nil {
		return out
	}
	seen := map[string]bool{}
	for _, iface := range out {
		seen[iface] = true
	}
	qualified := qualifyReceiver(c.fa, typeName, nil)
	for _, iface := range c.interfacesImplementedBy(typeName) {
		if seen[iface] {
			continue
		}
		if _, visible := c.reg.Lookup(iface).(*InterfaceType); visible {
			// interfacesDeclaringMethod asked this one's own declaration.
			continue
		}
		for _, fn := range idx.IfaceMethodImpls[iface][method] {
			if fn == nil || idx.ReceiverOf(fn) != typeName {
				continue
			}
			if q := idx.QualifiedReceiver[fn]; q != "" && q != qualified {
				continue
			}
			seen[iface] = true
			out = append(out, iface)
			break
		}
	}
	sort.Strings(out)
	return out
}

// interfacesImplementedBy returns the sorted names of every interface
// `typeName` impl, per-file union project-wide, with NO requirement that the
// checking file's type registry can resolve the interface.
//
// That is the whole difference from interfacesDeclaringMethod above, and it
// exists because the registry requirement is a false negative rather than a
// filter: an interface declared in a sibling file the current file never
// imported is absent from `c.reg` while its impl IS in the project index, so
// `interfacesDeclaringMethod` answers "no such interface" for an impl that
// demonstrably exists. Callers that need the interface's own contract must
// still resolve it; fromFragmentsRival needs only the (interface, receiver,
// method) triple, which resolveConcreteImpl answers off the same index.
func (c *checker) interfacesImplementedBy(typeName string) []string {
	if c == nil || c.fa == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	consider := func(ifaces map[string]bool) {
		for iface := range ifaces {
			if seen[iface] {
				continue
			}
			seen[iface] = true
			out = append(out, iface)
		}
	}
	consider(c.fa.Impls[typeName])
	if c.fa.ProjectImpls != nil {
		consider(c.fa.ProjectImpls.Impls[typeName])
	}
	sort.Strings(out)
	return out
}

// lookupTypeMethodSymbol resolves an impl-block method symbol for
// `Type.method` access. Checks the per-file type-method table first (covers
// same-file methods, including module-private ones) then the project-level
// table (cross-module public methods — e.g. stdlib `List.head`,
// `String.to_string`). Returns nil when the type has no such method. The
// returned symbol carries the impl method's resolved type (hover) and source
// position (go-to-def).
//
// `origin` is the build key of the module that declared the RECEIVER type, as
// resolved at the call site. When it is known, the identity-keyed project table
// answers first: `TypeMethods` is keyed by bare receiver name, so std/calendar
// and std/random both deposit an `Error.to_string` into one slot
// and whichever the union loop's map iteration reached first wins — the same
// source resolving to a different module's method on a different run. Passing
// OriginUnresolved (a receiver whose declaration could not be established, or
// an index that never ran PopulateTypeMethodIdentities) falls through to the
// bare table and behaves exactly as before.
//
// Recording wrapper over resolveTypeMethodSymbol. The split exists because
// checkTaggedString has to ASK this procedure which `from_fragments` the
// desugared `<Tag>.from_fragments([…])` would reach, purely to compare it with
// the `(Literal, Tag)` handler the literal site is contracted by — a question,
// not a resolution, so it must not deposit a provider record for a call the
// program never makes. Two entry points onto ONE rule; a second copy of the
// rule is what the split avoids.
func (c *checker) lookupTypeMethodSymbol(typeName, method, origin string) *Symbol {
	sym, from := c.resolveTypeMethodSymbol(typeName, method, origin)
	switch from {
	case typeMethodByIdentity:
		// A genuinely cross-module answer: record the provider so the runtime
		// type-method bridge force-loads it. `byIdentity == local` is this
		// file's OWN method reached by a better key — no boundary was crossed,
		// and recording a provider there asks the bridge to load the module
		// currently being evaluated. Measured: without this arm the tour
		// doctest's `Dog.speak(rex)` sent the bridge after "main" and failed
		// `no project root set; cannot resolve imports`. The bare path
		// encoded the same intent as `key != ""`, which does not hold when
		// the entry's build key is a real name.
		c.recordTypeMethodModuleByIdentity(origin, typeName, method)
	case typeMethodByName:
		// Cross-module resolution: record the provider module so the
		// runtime type-method bridge force-loads it before main (the
		// method's module may not be in the entry's import closure).
		c.recordTypeMethodModule(typeName, method)
	}
	return sym
}

// typeMethodSource names which table answered a type-method resolution. Only
// lookupTypeMethodSymbol acts on it; it exists so the recording lives in one
// place instead of being duplicated into every caller that needs the answer.
type typeMethodSource int

const (
	typeMethodUnresolved typeMethodSource = iota
	typeMethodByIdentity
	typeMethodLocal
	typeMethodByName
)

// resolveTypeMethodSymbol is lookupTypeMethodSymbol's answer with no side
// effects, plus which table supplied it.
func (c *checker) resolveTypeMethodSymbol(typeName, method, origin string) (*Symbol, typeMethodSource) {
	if c == nil || c.fa == nil {
		return nil, typeMethodUnresolved
	}
	// Identity first, and that ordering is the fix. The per-file table is
	// bare-keyed too: `mergeTypeMethods` copies an imported module's methods
	// into the importer, so two SIBLING modules each declaring `Error` land
	// their `to_string` in the importer's single `TypeMethods["Error"]` slot.
	// Measured: leaving the per-file probe first resolved `beta.Error.to_string`
	// to alpha's method with the origin correctly in hand, which is the original
	// defect one table over.
	byIdentity := c.fa.ProjectImpls.LookupTypeMethodByIdentity(origin, typeName, method)
	local := c.fa.LookupTypeMethod(typeName, method)
	if byIdentity != nil && byIdentity != local {
		return byIdentity, typeMethodByIdentity
	}
	// Then the per-file table, which still owns what the project index
	// deliberately excludes: a module-private inherent method, reachable only
	// from inside its declaring module and so never ambiguous by module.
	if local != nil {
		return local, typeMethodLocal
	}
	if c.fa.ProjectImpls == nil {
		return nil, typeMethodUnresolved
	}
	if byName, ok := c.fa.ProjectImpls.TypeMethods[typeName]; ok {
		if sym := byName[method]; sym != nil {
			return sym, typeMethodByName
		}
	}
	return nil, typeMethodUnresolved
}

// receiverOriginForReceiver resolves the declaring module of the type a call
// site's receiver spelling names, for lookupTypeMethodSymbol's identity key.
//
// Two sources, in this order, because neither covers both spellings — measured,
// not assumed:
//
//   - The REFERENCE the builder recorded for a file-qualified owner
//     (`calendar.Error`, `beta.Error`). This is the only source that answers for
//     a SIBLING module: the type registry does not bind the dotted spelling of a
//     sibling's type, so a registry-only version resolved `beta.Error` through
//     the bare name `Error` and picked whichever sibling that happened to hit —
//     reproducing the very defect one level down. `namespaceQualifiedTypeName`
//     reads the same table for the same reason.
//   - The type REGISTRY, by the spellings the caller already computed. This
//     covers the base form under a selective import (`import std/calendar.Error`
//     then `Error.to_string`), where there is no dotted owner to reference.
//
// Returns OriginUnresolved for a primitive, a type parameter, or a name that
// resolves to nothing — every one of which must fall back to the bare table
// rather than refuse.
func (c *checker) receiverOriginForReceiver(object ast.Node, names ...string) string {
	if c == nil || c.fa == nil {
		return OriginUnresolved
	}
	if fa, ok := object.(*ast.FieldAccess); ok && fa.Field != nil {
		sym := c.fa.References[Pos{Line: fa.Field.Line, Col: fa.Field.Col}]
		for sym != nil && sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym != nil {
			if origin, ok := nominalOrigin(sym.Type); ok && origin != OriginUnresolved {
				return origin
			}
		}
	}
	if c.reg == nil {
		return OriginUnresolved
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if origin, ok := nominalOrigin(c.reg.Lookup(name)); ok && origin != OriginUnresolved {
			return origin
		}
	}
	return OriginUnresolved
}

func (c *checker) lookupTypeOnceSymbol(typeName, name string) *Symbol {
	if c == nil || c.fa == nil || c.fa.ModuleScope == nil {
		return nil
	}
	sym := c.fa.ModuleScope.Lookup(typeName)
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym.Members == nil {
		return nil
	}
	member := sym.Members[name]
	if member == nil || member.Kind != SymbolOnce {
		return nil
	}
	return member
}

// ownerOnceSymbol finds an owner-level `once` the module-scope lookup above
// misses. A file-qualified owner (`other.Policy.default`) is resolved through
// the type symbol the checker recorded for the owner's own name, and its
// public members answer. A bare owner (`Policy.secret`) may name a private
// binding declared in this file, which spec §3 makes visible here.
func (c *checker) ownerOnceSymbol(object ast.Node, name string) *Symbol {
	switch o := object.(type) {
	case *ast.FieldAccess:
		if o.Field == nil {
			return nil
		}
		owner := c.fa.References[Pos{Line: o.Field.Line, Col: o.Field.Col}]
		for owner != nil && owner.Resolved != nil {
			owner = owner.Resolved
		}
		if owner == nil {
			return nil
		}
		switch owner.Kind {
		case SymbolStruct, SymbolEnum, SymbolType:
		default:
			return nil
		}
		if member := owner.Members[name]; member != nil && member.Kind == SymbolOnce {
			return member
		}
	case *ast.TypeIdent:
		return c.fileTypeOnceSymbol(o.Name, name)
	}
	return nil
}

// fileTypeOnceSymbol is the owner-level `once` this file declares in an
// inherent `impl owner` block, public or private.
func (c *checker) fileTypeOnceSymbol(owner, name string) *Symbol {
	if c.ownTypeOnces == nil {
		c.ownTypeOnces = map[string]map[string]*Symbol{}
		for _, sym := range c.fa.Definitions {
			if sym == nil || sym.Kind != SymbolOnce || sym.OwningType == "" {
				continue
			}
			if _, ok := sym.Node.(*ast.OnceBinding); !ok {
				continue
			}
			byName := c.ownTypeOnces[sym.OwningType]
			if byName == nil {
				byName = map[string]*Symbol{}
				c.ownTypeOnces[sym.OwningType] = byName
			}
			byName[sym.Name] = sym
		}
	}
	return c.ownTypeOnces[owner][name]
}

func (c *checker) sameOwnerBareCallType(ident *ast.Ident) (Type, bool) {
	if c == nil || ident == nil {
		return nil, false
	}
	// Lexical resolution has already identified locals and ordinary file
	// declarations. An enclosing receiver must not replace that binding.
	if sym := c.fa.References[Pos{Line: ident.Line, Col: ident.Col}]; sym != nil {
		for sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym.OwningType == "" && sym.Kind != SymbolInterfaceMethod {
			return nil, false
		}
	}
	if c.currentInterface != "" {
		if ft, ok := c.sameInterfaceDefaultBareCallType(ident); ok {
			return ft, true
		}
	}
	if c.selfTypeName == "" {
		return nil, false
	}
	providers := c.interfacesDeclaringMethod(c.selfTypeName, ident.Name)
	if len(providers) >= 2 {
		c.addError(ident.Line, ident.Col, fmt.Sprintf(
			"bare same-owner call '%s(...)' is ambiguous: type '%s' impl %s, which each declare '%s' — qualify by interface, e.g. '%s.%s(...)'",
			ident.Name, c.selfTypeName, quoteNames(providers), ident.Name, providers[0], ident.Name))
		return nil, true
	}
	pos := Pos{Line: ident.Line, Col: ident.Col}
	selfOrigin := c.receiverOriginForReceiver(nil, c.selfTypeName)
	if sym := c.lookupTypeMethodSymbol(c.selfTypeName, ident.Name, selfOrigin); sym != nil {
		c.fa.References[pos] = sym
		if sym.Type != nil {
			return sym.Type, true
		}
	}
	if len(providers) == 1 {
		if it, ok := c.reg.Lookup(providers[0]).(*InterfaceType); ok {
			if ft := interfaceMethodFuncType(it, ident.Name); ft != nil {
				return ft, true
			}
		}
	}
	return nil, false
}

func (c *checker) sameInterfaceDefaultBareCallType(ident *ast.Ident) (Type, bool) {
	if c.currentInterface == "" {
		return nil, false
	}
	it, ok := c.reg.Lookup(c.currentInterface).(*InterfaceType)
	if !ok || it == nil {
		return nil, false
	}
	ft := interfaceDefaultMethodFuncType(it, ident.Name)
	if ft == nil {
		return nil, false
	}
	if sym := c.lookupInterfaceMethodSymbol(c.currentInterface, ident.Name); sym != nil {
		c.fa.References[Pos{Line: ident.Line, Col: ident.Col}] = sym
	}
	return ft, true
}

func interfaceDefaultMethodFuncType(iface *InterfaceType, name string) *FuncType {
	ft := interfaceMethodFuncType(iface, name)
	if ft == nil || iface == nil || iface.SelfParam == nil {
		return ft
	}
	subs := map[*TypeParam_]Type{iface.SelfParam: interfaceSelfType(iface)}
	params := make([]Type, len(ft.Params))
	for i := range ft.Params {
		params[i] = Substitute(ft.Params[i], subs)
	}
	ret := Substitute(ft.Return, subs)
	cp := *ft
	cp.Params = params
	cp.Return = ret
	return &cp
}

func interfaceSelfType(iface *InterfaceType) Type {
	if iface == nil {
		return nil
	}
	args := make([]Type, len(iface.TypeParamDefs))
	for i, d := range iface.TypeParamDefs {
		args[i] = d
	}
	return &InterfaceType{
		Origin:        iface.Origin,
		Name:          iface.Name,
		Methods:       iface.Methods,
		TypeParams:    iface.TypeParams,
		TypeParamDefs: iface.TypeParamDefs,
		TypeArgs:      args,
		SelfParam:     iface.SelfParam,
	}
}

func (c *checker) lookupInterfaceMethodSymbol(interfaceName, method string) *Symbol {
	if c == nil || c.fa == nil || c.fa.ModuleScope == nil {
		return nil
	}
	sym := c.fa.ModuleScope.Lookup(interfaceName)
	if sym == nil {
		return nil
	}
	for sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym.Kind != SymbolInterface || sym.Members == nil {
		return nil
	}
	return sym.Members[method]
}

func quoteNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "'" + name + "'"
	}
	return strings.Join(quoted, " and ")
}

// recordTypeMethodModule adds the provider module path of a cross-module
// type-promoted method to the file's TypeMethodModules demand set, so the
// runtime bridge force-loads it. No-op when the provider is the entry ("").
func (c *checker) recordTypeMethodModule(typeName, method string) {
	if c.fa == nil || c.fa.ProjectImpls == nil || c.fa.ProjectImpls.TypeMethodModule == nil {
		return
	}
	byName, ok := c.fa.ProjectImpls.TypeMethodModule[typeName]
	if !ok {
		return
	}
	path, ok := byName[method]
	if !ok || path == "" {
		return
	}
	if c.fa.TypeMethodModules == nil {
		c.fa.TypeMethodModules = make(map[string]bool)
	}
	c.fa.TypeMethodModules[path] = true
}

// recordTypeMethodModuleByIdentity is recordTypeMethodModule for the identity
// path. It must follow the SYMBOL the identity key selected: the bare
// TypeMethodModule slot names whichever module won the collapsed contest, so
// reusing it would force-load std/random for a call that resolved to
// std/calendar's method — the original defect displaced one table sideways.
func (c *checker) recordTypeMethodModuleByIdentity(origin, typeName, method string) {
	if c.fa == nil || c.fa.ProjectImpls == nil {
		return
	}
	path := c.fa.ProjectImpls.TypeMethodModuleByIdentity[TypeMethodKey{
		Origin: origin, Type: typeName, Method: method,
	}]
	if path == "" {
		return
	}
	if c.fa.TypeMethodModules == nil {
		c.fa.TypeMethodModules = make(map[string]bool)
	}
	c.fa.TypeMethodModules[path] = true
}

// attachDispatchImpl records, on the call-site method-name reference of
// an interface-qualified call (`Display.to_string(x)`), the single
// concrete impl FuncDef the call statically dispatches to — when the
// receiver type is concrete and the resolution is unambiguous. The LSP
// reads Symbol.DispatchImpl to make go-to-def land on the impl (and
// hover render it) rather than the abstract interface method. Stores the
// target on a per-call-site PROXY copy so the shared interface-method
// symbol (reused across every call site) never accumulates one site's
// target. No-op when there is no concrete single impl — go-to-def/hover
// then fall back to the interface contract.
func (c *checker) attachDispatchImpl(fa *ast.FieldAccess, ifaceName string, concrete Type) {
	if c == nil || c.fa == nil || c.fa.ProjectImpls == nil || fa.Field == nil {
		return
	}
	var impl ast.ImplMethodDecl
	if fn := resolveConcreteImpl(c.fa.ProjectImpls, ifaceName, fa.Field.Name, concrete); fn != nil {
		impl = fn
	} else if ext := resolveConcreteImplExtern(c.fa.ProjectImpls, ifaceName, fa.Field.Name, concrete); ext != nil {
		impl = ext
	}
	if impl == nil {
		return
	}
	fieldPos := Pos{Line: fa.Field.Line, Col: fa.Field.Col}
	existing, ok := c.fa.References[fieldPos]
	if !ok || existing == nil {
		return
	}
	proxy := *existing
	proxy.DispatchImpl = impl
	proxy.DispatchReceiver = concrete
	c.fa.References[fieldPos] = &proxy
}

func (c *checker) attachDispatchImplForInterfaceKey(fa *ast.FieldAccess, ifaceName, ifaceKey string, concrete Type) {
	if c == nil || c.fa == nil || fa.Field == nil || ifaceKey == "" {
		return
	}
	impl := c.resolveImplForInterfaceKey(ifaceName, ifaceKey, fa.Field.Name, concrete)
	if impl == nil {
		return
	}
	fieldPos := Pos{Line: fa.Field.Line, Col: fa.Field.Col}
	existing, ok := c.fa.References[fieldPos]
	if !ok || existing == nil {
		return
	}
	proxy := *existing
	proxy.DispatchImpl = impl
	proxy.DispatchReceiver = concrete
	c.fa.References[fieldPos] = &proxy
}

func (c *checker) attachBareDispatchImplForInterfaceKey(ident *ast.Ident, ifaceName, ifaceKey string, concrete Type) {
	if c == nil || c.fa == nil || ident == nil || ifaceKey == "" {
		return
	}
	impl := c.resolveImplForInterfaceKey(ifaceName, ifaceKey, ident.Name, concrete)
	if impl == nil {
		return
	}
	pos := Pos{Line: ident.Line, Col: ident.Col}
	existing := c.fa.References[pos]
	if existing == nil {
		existing = c.lookupInterfaceMethodSymbol(ifaceName, ident.Name)
	}
	if existing == nil {
		return
	}
	proxy := *existing
	proxy.DispatchImpl = impl
	proxy.DispatchReceiver = concrete
	if ft := c.implMethodFuncType(impl); ft != nil {
		proxy.Type = ft
	}
	c.fa.References[pos] = &proxy
}

func (c *checker) resolveImplForInterfaceKey(ifaceName, ifaceKey, methodName string, concrete Type) ast.ImplMethodDecl {
	if c == nil || c.fa == nil {
		return nil
	}
	if c.fa.ProjectImpls != nil {
		if fn := resolveConcreteImplForInterfaceKey(c.fa.ProjectImpls, ifaceName, ifaceKey, methodName, concrete); fn != nil {
			return fn
		}
		if ext := resolveConcreteImplExternForInterfaceKey(c.fa.ProjectImpls, ifaceName, ifaceKey, methodName, concrete); ext != nil {
			return ext
		}
	}
	return c.resolveLocalImplForInterfaceKey(ifaceName, ifaceKey, methodName, concrete)
}

func (c *checker) resolveLocalImplForInterfaceKey(ifaceName, ifaceKey, methodName string, concrete Type) ast.ImplMethodDecl {
	name := concreteTypeName(resolveTypeVar(concrete))
	if name == "" || c == nil || c.fa == nil {
		return nil
	}
	var match ast.ImplMethodDecl
	for _, fn := range c.fa.IfaceMethodImpls[ifaceName][methodName] {
		if fn == nil {
			continue
		}
		recv := funcDefReceiverBaseName(fn)
		if c.fa.ImplBlockReceiver != nil {
			if r := c.fa.ImplBlockReceiver[fn]; r != "" {
				recv = r
			}
		}
		if recv != name {
			continue
		}
		if ifaceKey != "" && c.fa.ImplBlockInterfaceKey[fn] != ifaceKey {
			continue
		}
		if match != nil {
			return nil
		}
		match = fn
	}
	for _, ext := range c.fa.IfaceMethodImplExterns[ifaceName][methodName] {
		if ext == nil || c.fa.ImplBlockReceiverExtern[ext] != name {
			continue
		}
		if ifaceKey != "" && c.fa.ImplBlockInterfaceKeyExtern[ext] != ifaceKey {
			continue
		}
		if match != nil {
			return nil
		}
		match = ext
	}
	return match
}

// resolveConcreteImpl returns the single impl FuncDef an interface-method
// call dispatches to for a concrete receiver type, or nil when there is
// no unambiguous, navigable answer: the receiver isn't concrete, no impl
// matches its type, more than one distinct impl matches (a coherence
// violation reported elsewhere — here we just decline to guess), or the
// only match is a `@derive`-synthesized impl that has no real source
// position to navigate to. Matching is by the impl's first-parameter base
// type name against the receiver's impl-lookup name — the same name
// keying the project Impls table and the runtime dispatch — so the
// resolved target agrees with where the call actually lands.
func resolveConcreteImpl(idx *ProjectImplIndex, ifaceName, methodName string, concrete Type) *ast.FuncDef {
	return resolveConcreteImplMatching(idx, ifaceName, "", methodName, concrete)
}

func resolveConcreteImplForInterfaceKey(idx *ProjectImplIndex, ifaceName, ifaceKey, methodName string, concrete Type) *ast.FuncDef {
	return resolveConcreteImplMatching(idx, ifaceName, ifaceKey, methodName, concrete)
}

func resolveConcreteImplMatching(idx *ProjectImplIndex, ifaceName, ifaceKey, methodName string, concrete Type) *ast.FuncDef {
	if idx == nil {
		return nil
	}
	name := concreteTypeName(resolveTypeVar(concrete))
	if name == "" {
		return nil
	}
	var match *ast.FuncDef
	for _, fn := range idx.IfaceMethodImpls[ifaceName][methodName] {
		if fn == nil || len(fn.Params) == 0 || fn.Params[0].TypeAnnotation == nil {
			continue
		}
		// Match by the impl's recorded receiver, not its first-param type:
		// block-form impls write `self` there, so ReceiverOf consults the
		// block-header receiver (falling back to the first param for
		// decorator-form impls). Without this, no block-form interface impl
		// resolves to its concrete source for go-to-def / hover.
		if idx.ReceiverOf(fn) != name {
			continue
		}
		if ifaceKey != "" && idx.ImplBlockInterfaceKey[fn] != ifaceKey {
			continue
		}
		if IsSynthesizedLine(fn.Line) {
			// Derive-synthesized impl: real dispatch target, but no
			// hand-written source position to jump to. Decline.
			continue
		}
		if match != nil {
			if match.Line != fn.Line || match.Col != fn.Col {
				return nil // genuinely ambiguous — two distinct impls match
			}
			continue // duplicate pointer to the same source impl
		}
		match = fn
	}
	return match
}

// resolveConcreteImplExtern is the `host fn` analogue of resolveConcreteImpl:
// it resolves an interface-method call to the single EXTERN impl method it
// dispatches to for a concrete receiver, or nil. Externs are never
// @derive-synthesized, so there is no synthesized-line skip; matching is by the
// recorded block-header receiver (ImplBlockReceiverExtern). Callers try this
// after resolveConcreteImpl returns nil — a given (iface, method, type) is
// implemented either by a `fn` or an `host fn`, never both (coherence), so
// the order is immaterial to correctness.
func resolveConcreteImplExtern(idx *ProjectImplIndex, ifaceName, methodName string, concrete Type) *ast.ExternFunc {
	return resolveConcreteImplExternMatching(idx, ifaceName, "", methodName, concrete)
}

func resolveConcreteImplExternForInterfaceKey(idx *ProjectImplIndex, ifaceName, ifaceKey, methodName string, concrete Type) *ast.ExternFunc {
	return resolveConcreteImplExternMatching(idx, ifaceName, ifaceKey, methodName, concrete)
}

func resolveConcreteImplExternMatching(idx *ProjectImplIndex, ifaceName, ifaceKey, methodName string, concrete Type) *ast.ExternFunc {
	if idx == nil {
		return nil
	}
	name := concreteTypeName(resolveTypeVar(concrete))
	if name == "" {
		return nil
	}
	var match *ast.ExternFunc
	for _, ext := range idx.IfaceMethodImplExterns[ifaceName][methodName] {
		if ext == nil {
			continue
		}
		if idx.ImplBlockReceiverExtern[ext] != name {
			continue
		}
		if ifaceKey != "" && idx.ImplBlockInterfaceKeyExtern[ext] != ifaceKey {
			continue
		}
		if match != nil {
			if match.Line != ext.Line || match.Col != ext.Col {
				return nil // genuinely ambiguous — two distinct externs match
			}
			continue // duplicate pointer to the same source impl
		}
		match = ext
	}
	return match
}

// concreteTypeName returns the impl-lookup name for a concrete Type.
// Returns "" for types that cannot carry an impl block (functions, tuples,
// anonymous structs, etc.).
// isStructShaped reports whether t is a struct type — a named struct or an
// anonymous struct — after following type-var resolution. These are the only
// types with fields to patch, so they are the only valid inner type for a
// `Partial<T>` parameter (enforced by checkPartialParamStructs at generic call
// sites and by the type resolver's `Partial` case for concrete inners).
func isStructShaped(t Type) bool {
	switch resolveTypeVar(t).(type) {
	case *StructType, *AnonStructType:
		return true
	}
	return false
}

// orderingUnsupportedKind reports whether a concrete operand type can never
// carry a `Comparable` impl, returning a human-facing description of its kind.
// Anonymous structs and tuples are structural — they have no declaration to
// attach an `impl Comparable` to and no nominal name for the strong-demand
// recorder, so an ordering operator on them is a compile-time missing-impl
// error rather than the unreachable runtime "cannot compare" trap. Returns
// ("", false) for every type that either has a nominal name (so it *can* impl
// Comparable, named structs included) or is existential (interface / type
// param / type var — those skip the demand recorder separately).
func orderingUnsupportedKind(t Type) (string, bool) {
	if t == nil {
		return "", false
	}
	switch resolveTypeVar(t).(type) {
	case *AnonStructType:
		return "anonymous struct", true
	case *TupleType:
		return "tuple", true
	}
	return "", false
}

func concreteTypeName(t Type) string {
	switch ty := t.(type) {
	case *PrimitiveType:
		return ty.Name_
	case *StructType:
		return ty.Name
	case *EnumType:
		return ty.Name
	case *DistinctType:
		return ty.Name
	case *ListType:
		return "List"
	case *MapType:
		return "Map"
	}
	return ""
}

// coerceMapToList converts Map<K,V> to List<(K,V)> when the expected type is
// List<_>. This matches the runtime's iterNext which yields (key, value) tuples
// when iterating over a map.
func coerceMapToList(argTy Type, expectedTy Type) Type {
	mt, ok := argTy.(*MapType)
	if !ok {
		return argTy
	}
	if _, ok := expectedTy.(*ListType); !ok {
		return argTy
	}
	return &ListType{Elem: &TupleType{Elems: []Type{mt.Key, mt.Val}}}
}

// coerceRangeToList converts Range<T> to List<T> when the expected type is
// List<_>. The normal Iter conformance check still decides whether the
// specific Range<T> is actually iterable (T must satisfy Discrete).
func coerceRangeToList(argTy Type, expectedTy Type) Type {
	st, ok := argTy.(*StructType)
	if !ok || st.Name != "Range" {
		return argTy
	}
	if _, ok := expectedTy.(*ListType); !ok {
		return argTy
	}
	var elem Type = TypeInt
	if len(st.TypeArgs) > 0 {
		elem = st.TypeArgs[0]
	}
	return &ListType{Elem: elem}
}

// checkTaggedString type-checks a typed literal `<tag>"..."` and
// returns the literal's value type (the return type of the resolved
// `<tag>.interpolate`). The flow is:
//
//  1. Look up the `interpolate` symbol on the tag's module via the
//     References table populated by builder.go's resolveTaggedStringTag.
//  2. Verify the symbol's Type is a FuncType with shape
//     `(List<Fragment<I>>) -> R` for some interface or concrete type I
//     and return type R.
//  3. For each Dynamic slot (StringExpr) in the literal's Parts, check
//     the slot's expression type and verify it satisfies I (interface
//     conformance reuses typeImplementsInterface; concrete-I just
//     unifies via TypesEqual).
//  4. Return R as the typed-literal expression's type.
//
// All four steps are best-effort: any failure emits a precise diagnostic
// and returns nil, leaving downstream uses to fall back to permissive
// checking. builder.go's resolveTaggedStringTag handles the "tag not in scope" /
// "module has no interpolate" cases before this method runs.
func (c *checker) checkTaggedString(n *ast.TaggedString) Type {
	// Walk every slot expression first so each one's identifiers /
	// nested calls / etc. get checked even if the tag itself is
	// unresolved. The slot-level conformance check below short-circuits
	// when interfaceTy is nil.
	slotTypes := make([]Type, len(n.Parts))
	for i, part := range n.Parts {
		if se, ok := part.(ast.StringExpr); ok {
			slotTypes[i] = c.checkNode(se.Expr)
		}
	}

	// The TaggedString AST node's Line/Col is the opening quote (the
	// lexer captures position there after consuming the tag identifier).
	// builder.go's resolveTaggedStringTag registers the Reference at the
	// tag identifier position — `len(n.Tag)` columns earlier — so the
	// click range matches the visible tag in editors. Mirror that math
	// here so the lookup hits.
	tagCol := max(n.Col-len(n.Tag), 1)
	tagPos := Pos{Line: n.Line, Col: tagCol}
	interpSym, ok := c.fa.References[tagPos]
	if !ok {
		// builder.go already emitted the "tag not in scope" /
		// "module exports no interpolate" error — no return type to
		// thread through; bail out silently.
		return nil
	}
	if interpSym.Resolved != nil {
		interpSym = interpSym.Resolved
	}
	var ft *FuncType
	switch interpSym.Kind {
	case SymbolStruct, SymbolEnum, SymbolType:
		// Block form: the tag names a type whose handler is an
		// `impl Literal for <Tag> { fn from_fragments }` block — the
		// ordinary (Literal, Tag) dispatch the literal site desugars
		// to. Resolve the impl the interface-aware way and read the
		// receiver-less method's post-BuildTypes signature from the project
		// ImplFuncTypes index (the TypeMethods table doesn't carry
		// interface-impl methods cross-module). The receiver type is
		// matched by name only, so a name-bearing placeholder suffices.
		tagType := &DistinctType{Name: n.Tag}
		impl := resolveConcreteImpl(c.fa.ProjectImpls, "Literal", "from_fragments", tagType)
		if impl == nil {
			c.errors = append(c.errors, TypeError{
				Line:    n.Line,
				Col:     tagCol,
				Message: fmt.Sprintf("type '%s' has no typed-literal handler — write `impl Literal for %s { fn from_fragments(fragments: List<Fragment<I>>): R }`", n.Tag, n.Tag),
			})
			return nil
		}
		if rival, ok := c.fromFragmentsRival(n.Tag, impl); ok {
			c.errors = append(c.errors, TypeError{
				Line:    n.Line,
				Col:     tagCol,
				Message: fmt.Sprintf("typed literal '%s\"...\"' is ambiguous: %s, and a typed literal has no way to say which one it means — %s", n.Tag, rival.describe(n.Tag), rival.remedy(n.Tag)),
			})
			return nil
		}
		f, isFn := c.fa.ProjectImpls.ImplFuncTypes[impl].(*FuncType)
		if !isFn {
			c.errors = append(c.errors, TypeError{
				Line:    n.Line,
				Col:     tagCol,
				Message: fmt.Sprintf("typed-literal handler '%s.from_fragments' has no resolved function type", n.Tag),
			})
			return nil
		}
		ft = f
		// Record the (Literal, Tag) impl demand so coherence / orphan
		// / the runtime manifest bridge force-load the handler's module, and
		// point the tag reference at the impl for go-to-def.
		c.fa.RecordManifest(n.Tag, "Literal", Recording{Pos: tagPos, Kind: RecordingKindCallSite})
		proxy := *interpSym
		proxy.DispatchImpl = impl
		proxy.DispatchReceiver = tagType
		c.fa.References[tagPos] = &proxy
	default:
		c.errors = append(c.errors, TypeError{
			Line:    n.Line,
			Col:     tagCol,
			Message: fmt.Sprintf("'%s' is not a typed-literal handler — write `impl Literal for %s`", n.Tag, n.Tag),
		})
		return nil
	}

	// Step 3: signature shape — exactly one parameter of type
	// List<Fragment<I>>, any return type R.
	if len(ft.Params) != 1 {
		c.errors = append(c.errors, TypeError{
			Line:    n.Line,
			Col:     tagCol,
			Message: fmt.Sprintf("tag module '%s' interpolate must take exactly one parameter of type List<Fragment<...>>, got %d parameters", n.Tag, len(ft.Params)),
		})
		return ft.Return
	}
	listTy, ok := ft.Params[0].(*ListType)
	if !ok {
		c.errors = append(c.errors, TypeError{
			Line:    n.Line,
			Col:     tagCol,
			Message: fmt.Sprintf("tag module '%s' interpolate parameter must be List<Fragment<...>>, got %s", n.Tag, typeStringOrUnknown(ft.Params[0])),
		})
		return ft.Return
	}
	fragmentTy, ok := listTy.Elem.(*EnumType)
	if !ok || fragmentTy.Name != "Fragment" || len(fragmentTy.TypeArgs) != 1 {
		c.errors = append(c.errors, TypeError{
			Line:    n.Line,
			Col:     tagCol,
			Message: fmt.Sprintf("tag module '%s' interpolate parameter must be List<Fragment<...>>, got List<%s>", n.Tag, typeStringOrUnknown(listTy.Elem)),
		})
		return ft.Return
	}
	interfaceTy := fragmentTy.TypeArgs[0]

	// Step 4: type-check each Dynamic slot against I. The signature-
	// shape check above already returned for malformed tags, so we
	// only get here when interfaceTy is well-formed.
	for i, part := range n.Parts {
		se, isExpr := part.(ast.StringExpr)
		if !isExpr {
			continue
		}
		c.checkTaggedStringSlot(n, se, slotTypes[i], interfaceTy)
	}

	if ok, _ := RawLiteralType(n.Raw, ft.Return); ok != nil {
		return ok
	}
	return ft.Return
}

// RawLiteralType is the type of a typed literal whose handler returns ret,
// when the literal is checked at compile time: a backtick (raw) literal whose
// handler can fail, returning `Result<T, E>`. Its body never interpolates, so
// the handler's input is fixed text; the compiler runs the handler (vmhost's
// checkLiterals), an Err is a compile error at the literal, and the literal's
// type is T. ok is T and err is E; both are nil for every other literal,
// whose type is ret itself.
func RawLiteralType(raw bool, ret Type) (ok, err Type) {
	if !raw {
		return nil, nil
	}
	et, isEnum := resolveTypeVar(ret).(*EnumType)
	if !isEnum || et.Name != "Result" || len(et.TypeArgs) != 2 || et.TypeArgs[0] == nil {
		return nil, nil
	}
	return et.TypeArgs[0], et.TypeArgs[1]
}

// fromFragmentsRival is the ONE admission point for "this tag type declares a
// second `from_fragments`", and the rule it enforces is one sentence: a typed
// literal is well-formed only when the tag type carries exactly one
// declaration of that name, the `(Literal, tag)` handler's.
//
// # Why the sugar has to ask
//
// Spec §16: `<Tag>"…"` IS `<Tag>.from_fragments([Fragment.Static(…), …])`. The
// author of a typed literal writes no method name — `from_fragments` is the
// compiler's, from the spec — so every rule the language has for choosing
// between two providers of one method name is stated in terms of text that is
// not here. `preferTypeMethodSymbol` prefers an inherent declaration because
// "the source said which one wins"; between two INTERFACE impls it has nothing
// to say at all and falls through to a positional tie-break that exists to keep
// an index reproducible, not to decide what a program means.
//
// # Why the answer is a DIAGNOSTIC and not a pick
//
// This adds no policy: it applies to the sugar what the language already does
// to the spelling the sugar IS. Measured by running both, with a hand-written
// `Pick.from_fragments([Fragment.Static("x")])` in place of `Pick"x"`:
//
//   - With the INHERENT rival it resolves to the inherent method and prints
//     `inherent`, by inherent-beats-impl. So the sugar picked the OPPOSITE side
//     from its own desugaring — the checker typed the literal by the `Literal`
//     handler.
//   - With the SECOND-INTERFACE rival it is REJECTED, by spec §13 *Function Name Collisions*' own ambiguity
//     check: `type 'Pick' impl 'Literal' and 'Other', which each declare
//     'from_fragments' — type-qualified 'Pick.from_fragments(...)' is
//     ambiguous; qualify by interface, e.g. 'Literal.from_fragments(...)'`. So
//     the sugar was the ONLY form of that call that picked, and it picked
//     where the language refuses. The remedy that section offers is also the one a
//     literal cannot take: there is no `<Iface>"…"` spelling to qualify with.
//
// Picking a side at the literal breaks something exact either way. Picking the
// handler makes the sugar differ from its own desugaring, which the IR builder
// already refuses (`ambiguous type-qualified call`,
// internal/irbuild/literal.go). Picking the other lets an unrelated declaration
// silently disable a type's `impl Literal` handler, including one whose
// parameter is not `List<Fragment<I>>` at all, at which point the literal has
// no meaning to give.
//
// Neither case is a choice between two well-typed readings. The construct is
// UNSOUND both times, because `checkTaggedString` types the literal by the
// `Literal` handler's return while the desugared call resolves through the
// type-method table.
//
//   - INHERENT rival: with the impl returning `Int` and the inherent one
//     `String`, `n: Int = Pick"x"` type-checked and `n + 1` faulted at run time
//     with `cannot add String and Int`.
//   - SECOND-INTERFACE rival: with `interface Other` also declaring
//     `from_fragments`, `s: String = Pick"x"` type-checked against the
//     `Literal` handler's `String` and could run the `Other` impl, which
//     answers `7`, so the meaning of a typed literal depended on the order two
//     unrelated impl blocks were written in.
//
// checker_typed_literal_test.go pins both.
//
// # TWO INDEXES, because neither sees both populations
//
// The type-method table keeps ONE representative per (receiver, method) — its
// own comment says so (defineImplBlockAnnotations) — and for two interface
// impls that representative is whichever block came first. So asking it about
// the second-interface population answers a coin flip: with `impl Literal`
// written first the table holds the handler itself and reports no rival, which
// is exactly the false negative that left this half of the defect live after
// the inherent half was closed. The (receiver → interface) impl index is the
// one that sees both blocks, because it is keyed by interface rather than by
// method name.
//
// That index is read DIRECTLY rather than through `interfacesDeclaringMethod`,
// and the difference is a measured hole rather than a preference:
// `interfacesDeclaringMethod` admits an interface only if `c.reg` can resolve
// it, and the checking file's registry does not bind an interface declared in
// a sibling file it never imported. With the first
// version of this arm, the same-file spelling was rejected in both orders and
// the sibling-file one still type-checked `s: String = Pick"x"` and printed
// `7`. Asking the project index for the (interface, receiver, method) triple
// needs no registry and answers for every file layout.
//
// A MISS is not a rival. Nil, an unpopulated index and an unresolved origin all
// fall through to "no rival", per type_method_identity.go's lattice rule: this
// check may only ever report a POSITIVE second declaration, never a lookup that
// failed to find the first.
//
// # Why not pointer identity against the handler
//
// Because an AST pointer is not a declaration's identity across PARSES, and
// that cost a measurement: the stdlib is parsed once for std.Load() and again
// for the project index, so the type-method symbol and the resolved impl of one
// `fn from_fragments` are two nodes of one declaration. `sym.Node == handler`
// read FALSE for every stdlib handler, and the first run of this check reported
// every `DateTime"…"` and `NaiveDateTime"…"` in the tour as ambiguous against
// its own handler (`vmhost.TestTourDoctests`, two blocks). Both arms below
// therefore compare source POSITIONS with the handler, not pointers.
func (c *checker) fromFragmentsRival(tag string, handler *ast.FuncDef) (fromFragmentsRivalInfo, bool) {
	if c == nil || c.fa == nil || handler == nil {
		return fromFragmentsRivalInfo{}, false
	}
	// Arm 1, the INHERENT rival: the type-method table is sound here because
	// defineImplBlockStub lets an inherent block overwrite an interface-impl
	// entry, so an inherent `from_fragments` always owns the slot and an answer
	// that is still an interface-impl method proves no inherent rival exists.
	origin := c.receiverOriginForReceiver(nil, tag)
	sym, _ := c.resolveTypeMethodSymbol(tag, "from_fragments", origin)
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym != nil && !sym.IsImplMethod &&
		(sym.Pos.Line != handler.Line || sym.Pos.Col != handler.Col) {
		return fromFragmentsRivalInfo{Line: sym.Pos.Line}, true
	}
	// Arm 2, the SECOND-INTERFACE rival. Interface names are sorted so a
	// receiver with two rivals reports the same one on every run; a map-order
	// answer here is a diagnostic that moves under you.
	//
	// `Literal` is NOT special-cased out of this loop, and the first version of
	// it was: `if iface == "Literal" { continue }`, justified as "the handler is
	// unique so it cannot be its own rival". Removing that line and running
	// `./analysis` plus `./runtime` (TestTourDoctests included, which is the
	// test the same check's pointer-identity ancestor died on) left BOTH green,
	// so the claim was true and the line was not carrying it. The POSITION
	// comparison below is: `resolveConcreteImpl` is deterministic — its index
	// row is a slice and two distinct matches return nil — so for the `Literal`
	// row it returns the same FuncDef `checkTaggedString` already resolved as
	// `handler`, and the comparison excludes it by identity of declaration
	// rather than by name. One rule for every interface, and no name string to
	// get wrong.
	for _, iface := range c.interfacesImplementedBy(tag) {
		fd := resolveConcreteImpl(c.fa.ProjectImpls, iface, "from_fragments", &DistinctType{Name: tag})
		if fd == nil || (fd.Line == handler.Line && fd.Col == handler.Col) {
			continue
		}
		return fromFragmentsRivalInfo{Line: fd.Line, Interface: iface}, true
	}
	return fromFragmentsRivalInfo{}, false
}

// fromFragmentsRivalInfo is a second `from_fragments` declaration on a tag
// type: where it is, and what declares it. Interface is "" for an inherent
// `impl Tag { … }` block and the interface's name for an `impl Iface for Tag`
// one. The two spellings need different remedies — an inherent rival can be
// moved out of the block, an interface rival cannot without dropping the impl —
// so the diagnostic is built from this rather than from one fixed sentence.
type fromFragmentsRivalInfo struct {
	Line      int
	Interface string
}

// describe names the rival declaration in the diagnostic's middle clause.
func (r fromFragmentsRivalInfo) describe(tag string) string {
	if r.Interface == "" {
		return fmt.Sprintf("`%s.from_fragments` also names the type function declared at line %d", tag, r.Line)
	}
	return fmt.Sprintf("`impl %s for %s` also declares `from_fragments`, at line %d", r.Interface, tag, r.Line)
}

// remedy is the diagnostic's closing advice, which differs by spelling.
func (r fromFragmentsRivalInfo) remedy(tag string) string {
	if r.Interface == "" {
		return fmt.Sprintf("move one of them out of `impl %s { ... }` or rename it", tag)
	}
	return fmt.Sprintf("rename one of them, or drop `impl %s for %s`", r.Interface, tag)
}

// checkTaggedStringSlot verifies that a single Dynamic-slot expression's
// type conforms to the tag's per-slot value-type interface (or concrete
// type). Slot-level diagnostics point at the slot expression's source
// position so multi-slot typed literals report exactly which `${expr}`
// is wrong, not the literal as a whole.
func (c *checker) checkTaggedStringSlot(n *ast.TaggedString, se ast.StringExpr, slotTy Type, interfaceTy Type) {
	if slotTy == nil {
		// Sub-expression failed to type-check; that path emitted its
		// own error. Skip the conformance check rather than piling on.
		return
	}
	slotLine, slotCol := slotPosition(se)
	if iface, ok := interfaceTy.(*InterfaceType); ok {
		if !c.typeImplementsInterface(slotTy, iface.Name, c.recPos(slotLine, slotCol), RecordingKindTypedLiteralSlot) {
			c.errors = append(c.errors, TypeError{
				Line:    slotLine,
				Col:     slotCol,
				Message: fmt.Sprintf("slot expression of type '%s' does not implement '%s' (required by tag '%s')", slotTy, iface.Name, n.Tag),
			})
		}
		return
	}
	// Concrete-I (e.g. List<Fragment<String>>): require equality. Use
	// unify so TypeVars from polymorphic slot expressions can bind, but
	// fall back to TypesEqual for the fast path.
	if !TypesEqual(slotTy, interfaceTy) {
		if err := c.unify(interfaceTy, slotTy, nil); err != nil {
			c.errors = append(c.errors, TypeError{
				Line:    slotLine,
				Col:     slotCol,
				Message: c.typef("slot expression of type '%s' does not match required type '%s' (required by tag '%s')", slotTy, interfaceTy, n.Tag),
			})
		}
	}
}

// slotPosition returns a best-effort source position for a tagged-
// literal Dynamic slot expression. Used for slot-level diagnostics so
// multi-slot typed literals report which `${expr}` failed.
func slotPosition(se ast.StringExpr) (int, int) {
	if se.Expr == nil {
		return 0, 0
	}
	type lineCol interface {
		LineNum() int
	}
	if lc, ok := se.Expr.(lineCol); ok {
		line := lc.LineNum()
		// Best-effort column extraction. Most expression nodes have a
		// Col field accessible via type-switch; fall back to 0 when
		// the AST doesn't carry one.
		switch v := se.Expr.(type) {
		case *ast.Ident:
			return v.Line, v.Col
		case *ast.TypeIdent:
			return v.Line, v.Col
		case *ast.IntLit:
			return v.Line, v.Col
		case *ast.FloatLit:
			return v.Line, v.Col
		case *ast.DecimalLit:
			return v.Line, v.Col
		case *ast.CodepointLit:
			return v.Line, v.Col
		case *ast.StringLit:
			return v.Line, v.Col
		case *ast.Call:
			return v.Line, v.Col
		case *ast.FieldAccess:
			return v.Line, v.Col
		case *ast.Binary:
			return v.Line, v.Col
		case *ast.Unary:
			return v.Line, v.Col
		}
		return line, 0
	}
	return 0, 0
}

// typeStringOrUnknown stringifies a Type for error messages, returning
// a placeholder when the type is nil so diagnostics don't render as
// "got %!s(<nil>)".
func typeStringOrUnknown(t Type) string {
	if t == nil {
		return "<unknown>"
	}
	return t.String()
}
