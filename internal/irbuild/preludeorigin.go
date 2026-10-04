package irbuild

import (
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// A prelude enum's identity WITHOUT the using module's scope, and why the
// obvious route does not reach it.
//
// # The problem
//
// preludeAnchorsOf takes a spec's identity from `fa.ModuleScope.Lookup(name)`,
// so an anchor is a statement about ONE module: the name is in scope, resolves
// to a type whose (Origin, Name) is the spec's, and that type's declaration has
// the shape the spec describes. `Maybe` and `Result` are auto-imported into
// every module, so for them "this module says so" and "this is the type"
// coincide. For `Fragment` they do not.
//
// `Fragment` is an ordinary `pub enum` in std/literals, and the mentions that
// matter are the ones no module writes. A typed literal's `<Type>"…"` desugars
// to a call whose parameter is the HANDLER's `List<Fragment<I>>` — declared in
// std/calendar or std/toml, and named in the using file NOWHERE. A sibling
// file's `pub fn f(): Fragment<String>` is the same shape one file boundary
// closer, and testdata/literal_sibling is that program.
//
// # The route that does NOT transfer
//
// stdiface.go solves an adjacent problem — a stdlib INTERFACE carries no
// `Origin` at all — by reading the declaring module off the analyzer's
// resolution chain: `ModuleScope.Lookup(name)` yields an import symbol whose
// `ImportStmt.ModulePath` is the origin module. That is the correct door there
// and it is unavailable here. In
// tests/17-typed-literals/toml_literal_test.nomi,
// `fa.ModuleScope.Lookup("Fragment")` returns nil: there is no import symbol
// because the file never mentions the name, so there is no ModulePath to read.
//
// The two failure modes are different and only one of them is a bug. Lookup
// answering WRONGLY — an import shadowing a local declaration — is a soundness
// defect the analyzer owns. Lookup not answering AT ALL is absence, and no fix
// to the first can reach it.
//
// # The route that does
//
// An anchor's two halves are (identity, validated declaration shape), and both
// are available without the using module:
//
//   - IDENTITY. Unlike `analysis.InterfaceType`, `analysis.EnumType` CARRIES an
//     `Origin` (`Maybe` resolves with `Origin == "std/maybe"`), so
//     the analyzer's own nominal-identity rule answers directly. `std` is
//     unforgeable as a module name (analysis/manifest.go's LoadManifest rejects
//     `[package].name = "std"`), so a user's own `Fragment` carries a different
//     Origin and can never reach a spec.
//
//   - SHAPE. std's own declaration is reachable from `std.Load()`, and
//     `spec.matches` is the same predicate preludeAnchorsOf applies to a
//     module's resolved node. Validating against std rather than against the
//     using module is not a weakening: the using module's scope could only ever
//     have resolved to that same declaration.
//
// So this EVALUATES its precondition rather than citing it. A std edit that
// reorders `Fragment`'s variants, renames a type parameter, or moves `Static`'s
// payload off `String` produces no anchor and every mention refuses loudly —
// TestPreludeOrigin_AShapeMismatchProducesNoAnchor drives exactly that.
//
// # Why the anchor it hands back is SPEC-ONLY
//
// `preludeAnchor.decl` holds an `*ast.EnumDef`, and `std.Load()` is not
// memoized: two loads hand back different pointers and neither is the one the
// program's analysis resolved to. (stdiface.go makes the same point about
// `typeRegistry.byDecl`: a stdlib node registered there could never HIT.) So
// std's node is used to VALIDATE and then dropped, and what is
// returned is stdprelude.go's interned spec-only anchor — the one a def that
// outlives a single module already carries.
//
// # What this does NOT do
//
// It does not make a typed literal whose handler lives in std or in a sibling
// file lowerable: that is a separate obstacle and literal.go reports it. What
// the anchor buys is that such a site refuses with the handler's own reason
// rather than `prelude enum without a module anchor`.

// stdDeclaresSpec reports whether std's own source declares the enum this spec
// describes, in the shape it describes.
//
// Uncached and taking the library explicitly, so a test can ask it about a spec
// the table does not contain — which is the only way to exercise the negative
// half without editing std.
func stdDeclaresSpec(lib *std.StdLib, spec *preludeSpec) bool {
	if lib == nil || spec == nil {
		return false
	}
	module, isStd := strings.CutPrefix(spec.origin, "std/")
	if !isStd {
		return false
	}
	for _, n := range lib.Nodes[module] {
		decl, isEnum := n.(*ast.EnumDef)
		if !isEnum || decl.Name != spec.nomi {
			continue
		}
		return spec.matches(decl)
	}
	return false
}

// stdPreludeValid is stdDeclaresSpec over the spec TABLE, computed once for the
// process.
//
// Once is sound for the same reason stdlibLowering's cache is: the stdlib is
// fixed when the compiler binary is built, so the answer cannot differ between
// loads. Closed by construction — a spec absent from the table answers false.
var stdPreludeValid = func() func(*preludeSpec) bool {
	var once sync.Once
	valid := map[*preludeSpec]bool{}
	return func(spec *preludeSpec) bool {
		once.Do(func() {
			lib := stdAnchorLib()
			for i := range preludeSpecs {
				s := &preludeSpecs[i]
				valid[s] = stdDeclaresSpec(lib, s)
			}
		})
		return valid[spec]
	}
}()

// preludeSpecAnchor is the anchor for a spec whose identity the caller has
// already established, or false when std's own declaration does not match it.
//
// The caller supplies identity and this supplies shape, which is the same
// two-part rule preludeAnchorsOf applies — with the halves coming from the
// analyzer and from std instead of both from one module's scope.
func preludeSpecAnchor(spec *preludeSpec) (*preludeAnchor, bool) {
	if spec == nil || !stdPreludeValid(spec) {
		return nil, false
	}
	return sharedAnchorFor(spec), true
}

// preludeOriginAnchor is the anchor a TYPE's own (Origin, Name) names.
//
// An empty Origin declines. That is what a type produced by INSTANTIATING the
// generic declaration carries (see projectPreludeEnum), and it says nothing
// about which module declared anything. Admitting it would key the whole rule
// on a NAME, and a type identity matched by name is silently wrong the first
// time two declarations share one.
func preludeOriginAnchor(origin, name string) (*preludeAnchor, bool) {
	if origin == "" {
		return nil, false
	}
	return preludeSpecAnchor(preludeSpecFor(origin, name))
}
