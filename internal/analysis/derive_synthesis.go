package analysis

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// supportedDeriveInterfaces lists the four interfaces `@derive` knows how to
// synthesize in v1. The order matches spec §38.5: structural-equality,
// structural-hash, total-order, structural-debug.
//
// Use a map for O(1) membership but keep the canonical list as a slice so the
// "unknown protocol" error message can name the supported set in deterministic
// order.
var deriveSupportedList = []string{"Equatable", "Hashable", "Comparable", "Debug", "Display", "ToJson", "FromJson"}

var deriveSupported = func() map[string]bool {
	m := make(map[string]bool, len(deriveSupportedList))
	for _, name := range deriveSupportedList {
		m[name] = true
	}
	return m
}()

// deriveProtocolHomeModule maps each derivable protocol to its stdlib home
// module. Used by the derive-arg scope check (walkTypeDeclDecoratorArgs) to
// suggest the exact import in its diagnostic: a `@derive Comparable` arg
// names `Comparable` in visible source text, so the file must have it in
// scope — "`Comparable` is not in scope — import `std/comparable.Comparable`".
// Debug is exempt (compiler-known universally: every type is Debug-conformant,
// synthesis is automatic, and the impl headers resolve without the name).
var deriveProtocolHomeModule = map[string]string{
	"Equatable":  "std/equatable",
	"Hashable":   "std/hashable",
	"Comparable": "std/comparable",
	"Display":    "std/display",
	"Debug":      "std/debug",
	"ToJson":     "std/json",
	"FromJson":   "std/json",
}

// synthSupportNames is the closed set of names synthesized derive /
// universal-Debug code may reference beyond the receiver type's own
// fields and variants: the five protocol interfaces (recursion callees
// like `Equatable.equal?(...)`), the `Ordering` enum (Comparable
// signatures + the qualifier of `Ordering.Less` value/pattern refs), the
// Bool singletons (Equatable bodies), and the primitive result types.
// Synthesized code is compiler output — these names resolve through the
// compiler-known route, never through the deriving file's imports, so the
// checker suppresses its undefined-name diagnostics for exactly this set
// at synthesized positions (and ONLY this set: any other unresolved name
// in a synthesized body still errors loudly, keeping the synthesizer-bug
// net intact).
//
// The `Ordering` *variants* (Equal/Less/Greater) are deliberately absent:
// synthesized code names them qualified as `Ordering.Less`, so they
// resolve as member access on `Ordering` (already in this set) rather
// than as bare names — which is exactly what lets them stay out of the
// prelude. The Bool singletons True/False are still bare (Bool's variants
// remain prelude-exported), so they keep their entries.
//
// `Json` and `Json.ShapeError` are absent too: they resolve, typed, to std's
// declarations through synthSupportScope and synthSupportRegistry.
var synthSupportNames = map[string]bool{
	"Equatable":  true,
	"Comparable": true,
	"Hashable":   true,
	"Display":    true,
	"Debug":      true,
	"ToJson":     true,
	"FromJson":   true,
	"Result":     true,
	"Map":        true,
	"maps":       true,
	"Ok":         true,
	"Err":        true,
	"Some":       true,
	"None":       true,
	"Ordering":   true,
	"True":       true,
	"False":      true,
	"String":     true,
	"Int":        true,
	"Bool":       true,
}

// synthStdDecls are the std declarations derived code names that no prelude
// import binds: `derive ToJson` builds `Json.Obj{...}` and `Json.String(...)`,
// and `derive FromJson` matches `Json.Obj(fields)` and builds
// `Json.ShapeError{...}`. A file that derives either need import only the
// interface, so these names may be absent from its scope. Synthesized code
// then reaches them through synthSupportScope and synthSupportRegistry, which
// bind each name to the declaration std's own module scope holds.
//
// A name the file's scope binds already, to std's declaration through an
// import or to the file's own, keeps that binding. The IR builder resolves the
// names by the same rule (stdSynthAnchors), and the two must agree on which
// declaration a synthesized `Json` means.
var synthStdDecls = []struct{ module, name string }{
	{"json", "Json"},
	{"json", "Json.ShapeError"},
}

// synthStdSymbols resolves the synthStdDecls that fa's module scope leaves
// unbound against the std module scopes the file was built with. A module the
// build did not load contributes nothing: a file deriving ToJson imports
// std/json, so a missing scope means nothing in the file derives it.
func synthStdSymbols(fa *FileAnalysis) []*Symbol {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	var out []*Symbol
	for _, d := range synthStdDecls {
		if fa.ModuleScope.Lookup(d.name) != nil {
			continue
		}
		scope := fa.StdlibModuleScopes[d.module]
		if scope == nil {
			continue
		}
		if sym := scope.LookupLocal(d.name); sym != nil {
			out = append(out, sym)
		}
	}
	return out
}

// SynthSupportSymbol is the std declaration `name` means in a synthesized impl
// block of fa's file when name is one of synthStdDecls and the file's scope
// leaves it unbound, or nil.
func SynthSupportSymbol(fa *FileAnalysis, name string) *Symbol {
	for _, sym := range synthStdSymbols(fa) {
		if sym.Name == name {
			return sym
		}
	}
	return nil
}

// synthSupportScope is the scope a synthesized impl block's bodies resolve
// in: `parent` with synthStdSymbols bound over it. It is not registered among
// parent's children: it covers no source region, so ScopeAt must never
// descend into it.
func synthSupportScope(fa *FileAnalysis, parent *Scope) *Scope {
	syms := synthStdSymbols(fa)
	if len(syms) == 0 {
		return parent
	}
	s := &Scope{Parent: parent, Symbols: make(map[string]*Symbol, len(syms))}
	for _, sym := range syms {
		s.Symbols[sym.Name] = sym
	}
	return s
}

// synthSupportRegistry is synthSupportScope's counterpart for type names: a
// child of `parent` registering synthStdSymbols' types, for resolving the type
// expressions in a synthesized impl block's signatures and bodies.
func synthSupportRegistry(fa *FileAnalysis, parent *TypeRegistry) *TypeRegistry {
	syms := synthStdSymbols(fa)
	if len(syms) == 0 {
		return parent
	}
	reg := NewChildTypeRegistry(parent)
	for _, sym := range syms {
		if sym.Type != nil {
			reg.Register(sym.Name, sym.Type)
		}
	}
	return reg
}

type deriveOptions struct {
	RenameAll string
}

func defaultDeriveOptions() deriveOptions {
	return deriveOptions{RenameAll: "Snake"}
}

func validateDeriveOptions(ifaceName string, options ast.Node, line, col int) (deriveOptions, []TypeError) {
	opts := defaultDeriveOptions()
	if options == nil {
		return opts, nil
	}
	if ifaceName != "ToJson" && ifaceName != "FromJson" {
		return opts, []TypeError{{Line: line, Col: col, Message: fmt.Sprintf("`derive %s` does not accept options", ifaceName)}}
	}
	lit, ok := options.(*ast.StructLit)
	if !ok || lit.TypeName == nil {
		return opts, []TypeError{{Line: line, Col: col, Message: fmt.Sprintf("`derive %s` options must be `%s.Options{...}`", ifaceName, ifaceName)}}
	}
	want := ifaceName + ".Options"
	if lit.TypeName.TypeString() != want {
		return opts, []TypeError{{Line: line, Col: col, Message: fmt.Sprintf("`derive %s` options must use `%s`, got `%s`", ifaceName, want, lit.TypeName.TypeString())}}
	}
	seen := map[string]bool{}
	var errs []TypeError
	for _, field := range lit.Fields {
		if seen[field.Name] {
			errs = append(errs, TypeError{Line: field.Line, Col: field.Col, Message: fmt.Sprintf("duplicate derive option `%s`", field.Name)})
			continue
		}
		seen[field.Name] = true
		switch field.Name {
		case "rename_all":
			caseName, ok := jsonCaseOptionName(field.Value)
			if !ok {
				errs = append(errs, TypeError{Line: field.Line, Col: field.Col, Message: "`rename_all` must be a `Json.Case` variant such as `Json.Case.Camel`"})
				continue
			}
			opts.RenameAll = caseName
		default:
			errs = append(errs, TypeError{Line: field.Line, Col: field.Col, Message: fmt.Sprintf("unknown derive option `%s` for `%s`", field.Name, want)})
		}
	}
	return opts, errs
}

func parseDeriveOptionsForSynthesis(ifaceName string, options ast.Node) deriveOptions {
	opts, _ := validateDeriveOptions(ifaceName, options, 0, 0)
	return opts
}

func jsonCaseOptionName(n ast.Node) (string, bool) {
	parts := valuePathParts(n)
	switch len(parts) {
	case 1:
		return jsonCaseVariantName(parts[0])
	case 3:
		if parts[0] == "Json" && parts[1] == "Case" {
			return jsonCaseVariantName(parts[2])
		}
	}
	return "", false
}

func jsonCaseVariantName(name string) (string, bool) {
	switch name {
	case "Snake", "Camel", "Pascal", "Kebab", "ScreamingSnake":
		return name, true
	default:
		return "", false
	}
}

func valuePathParts(n ast.Node) []string {
	switch v := n.(type) {
	case *ast.TypeIdent:
		if v.Name == "" {
			return nil
		}
		return strings.Split(v.Name, ".")
	case *ast.Ident:
		if v.Name == "" {
			return nil
		}
		return strings.Split(v.Name, ".")
	case *ast.FieldAccess:
		if v.Field == nil {
			return nil
		}
		parts := valuePathParts(v.Object)
		if len(parts) == 0 {
			return nil
		}
		return append(parts, v.Field.Name)
	default:
		return nil
	}
}

// Synthesized AST nodes need positions that never alias parser output and
// never alias each other. Real source positions are 1-based lines always well
// below 2^30, so synthesized nodes reserve a disjoint band starting at
// synthLineBase. The analyzer's References / Definitions maps key by Pos, and
// without disjoint positions every node inside one synthesized FuncDef would
// share the type-decl's (line, col) and clobber each other (and the type
// decl's own symbol). Each synthesized FuncDef therefore gets its own SLOT —
// a range synthLineStride lines wide — and uniquifyPositions assigns cols 1,
// 2, 3, … within it.
//
// # The slot is a pure function of the declaration, not of execution history
//
// A slot drawn from a process-wide counter would vary with the order files
// are synthesized in, with the prelude-inject path drawing from the same
// counter, and with concurrency (LSP fan-out). Two observable consequences: a
// checker diagnostic that names a different line number on every run, and a
// lowering that is NOT reproducible, because a `case` inside a synthesized
// Debug body carries a synth-band line into its no-match fault.
//
// So the slot is derived from WHAT is being synthesized: the declaration's
// index among its file's top-level nodes, and which protocol is being
// synthesized for it. Both are facts about the source, so the same source
// yields the same positions in every process, under any goroutine
// interleaving, in any file order. Nothing has to be serialized to get there,
// which is why this is preferred over scoping a counter to the build.
//
// Uniqueness is by construction rather than by probability: two declarations
// in one file have different indices, and one declaration's several
// synthesized impls have different kinds. It is a PER-FILE guarantee, which is
// the scope that matters — References / Definitions are per-FileAnalysis. The
// one structure shared ACROSS files is the prelude-import clone cache, and it
// gets its own reserved region below so it cannot collide with any file.
//
// # The slot space is bounded, and that is a fix rather than a limitation
//
// The old counter was unbounded: a long-lived LSP session re-synthesizes on
// every keystroke, so after ~2^18 draws its lines passed 2^32 — and
// protocol.Position.Line is a uint32, so a leaked position would wrap into
// something IsSynthesizedLine reads as REAL source. Every slot below stays
// under 2^32.
const (
	synthLineBase   = 1 << 30
	synthLineStride = 16384
	// (1<<31 - synthLineBase) / synthLineStride — every slot's base line
	// stays below 2^31, i.e. inside a 32-bit signed int. That bound is not
	// cosmetic. A synthesized `case`'s line reaches `rt.NoCaseMatchError(line)`
	// as an `int`, which is 32 bits on a 32-bit GOARCH;
	// protocol.Position.Line is a uint32 besides.
	synthSlotCount = 65536
	// One region per synthesis origin (see synthOrigin). Regions are
	// disjoint, so two origins cannot alias no matter what indices they
	// hand out.
	synthRegionSize = synthSlotCount / int(synthOriginEnd)
	// Indices per region. 2730 is more top-level nodes than the largest
	// file in this repo has (its longest is 1398 LINES); the modulo in
	// synthSlot keeps a pathological file inside the band rather than
	// letting it walk out of it.
	synthIndexCap = synthRegionSize / synthKindCount
)

// synthOrigin names WHERE a synthesized node came from. Each origin indexes
// its own space — a file's top-level nodes, a file's `derive` statements, the
// prelude's re-exports — and those three spaces are unrelated, so each gets a
// disjoint region.
type synthOrigin int

const (
	// synthOriginDecl is the `@derive`-decorator and universal-default-Debug
	// path, indexed by the declaration's position among its file's top-level
	// nodes.
	synthOriginDecl synthOrigin = iota
	// synthOriginConformance is the top-level `derive Iface for T` statement
	// path, indexed by the statement's position in the PRE-lowering node
	// list. It needs its own region rather than sharing synthOriginDecl's:
	// lowering removes the statement from the list, so the two passes that
	// run afterwards index a SHORTER slice and the same integer means a
	// different node.
	synthOriginConformance
	// synthOriginPrelude is the prelude-import clone cache, indexed by the
	// clone's position in prelude.nomi's own statement list. Its own region
	// for a stronger reason than the others: the cache hands the SAME
	// pointers to every file in the build, while a per-file index is only a
	// per-file guarantee, so a shared region would let one clone collide
	// with some file's own synthesized impl.
	synthOriginPrelude
	synthOriginEnd
)

// synthKind names which impl is being synthesized for one site. One slot per
// kind, so a type that derives four protocols gets four disjoint ranges and no
// arithmetic decides which.
type synthKind int

const (
	synthKindEquatable synthKind = iota
	synthKindHashable
	synthKindComparable
	synthKindDebug
	synthKindDisplay
	synthKindToJson
	synthKindFromJson
	// synthKindAutoDebug is the universal-default Debug pass. It is a
	// separate slot from synthKindDebug even though the two never fire for
	// one declaration (SynthesizeUniversalDebug skips types that already
	// have an explicit Debug), so disjointness does not depend on that skip
	// staying correct.
	synthKindAutoDebug
	synthKindEnd
)

const synthKindCount = int(synthKindEnd)

// synthSlot is the whole scheme: a base line that is a pure function of what
// is being synthesized and nothing else.
func synthSlot(origin synthOrigin, index int, k synthKind) int {
	slot := int(origin)*synthRegionSize + (index%synthIndexCap)*synthKindCount + int(k)
	return synthLineBase + slot*synthLineStride
}

// synthSite identifies the declaration an impl is being synthesized FOR.
// Everything about a synthesized position is a pure function of it.
type synthSite struct {
	origin synthOrigin
	// index is the 0-based index within the origin's own space. Unique
	// there by construction.
	index int
	// line and col are the DECLARATION's own source position. They are not
	// part of the slot arithmetic; they are what a diagnostic about the
	// synthesized impl reports, since the impl has no navigable position of
	// its own. See ast.ImplBlock.SynthOriginLine.
	line int
	col  int
	// wrapping names the file's wrapping distinct types (`type Id Int`). A
	// synthesized Display/Debug arm for `embeds Id` rebuilds the `Id` from the
	// inner value the variant pattern binds, so the enum renders the embedded
	// value as the embedded type renders itself (`Id(5)`, not `5`).
	wrapping map[string]bool
}

// wrappingDistincts is the set of wrapping distinct types among a file's
// declarations. See synthSite.wrapping.
func wrappingDistincts(decls map[string]ast.Node) map[string]bool {
	out := map[string]bool{}
	for name, n := range decls {
		if td, ok := n.(*ast.TypeDef); ok && td.InnerTypeExpr != nil {
			out[name] = true
		}
	}
	return out
}

// base is the first line of this site's slot for kind k.
func (s synthSite) base(k synthKind) int { return synthSlot(s.origin, s.index, k) }

// synthPreludeBase is the slot for the i'th cloned prelude import statement.
func synthPreludeBase(i int) int {
	return synthSlot(synthOriginPrelude, i, synthKindEquatable)
}

// isPreludeInjectLine reports whether line lies in the band synthPreludeBase
// allocates from: the line of a prelude import a project build prepended to
// a user file.
func isPreludeInjectLine(line int) bool {
	lo := synthLineBase + int(synthOriginPrelude)*synthRegionSize*synthLineStride
	return line >= lo && line < lo+synthRegionSize*synthLineStride
}

// IsSynthesizedLine reports whether a (1-based) source line falls in the
// synthesized-position band that derive synthesis allocates from. Callers
// that walk fa.Definitions or References across an entire file may want to
// skip these so synthesized symbols (e.g., the inner-destructure binding
// in a synth Debug body) don't surface as user-visible defs.
func IsSynthesizedLine(line int) bool {
	return line >= synthLineBase
}

// SynthesizeDerives walks the file's top-level nodes, processes each type
// decl's `@derive` decorators, and returns a node list with synthesized
// `impl` blocks appended. The original input slice is not mutated.
//
// Validation errors (unknown interface, duplicate interface on one decl,
// non-derive decorator that slipped past the parser gate) are returned in
// the second slice; callers attach them to the appropriate FileAnalysis's
// TypeErrors slice. Each supported interface dispatches to a per-protocol
// synthesizer (Equatable / Hashable / Comparable / Debug), each of which
// emits a full structural body.
//
// Idempotent: calling SynthesizeDerives twice on the same slice produces
// the same extended slice the second time around. The front end
// (internal/frontend's Checker.Prepare) calls SynthesizeDerives once before
// analysis; BuildProject also runs it on each discovered file's nodes
// (because the analyzer's LSP callers reach BuildProject directly without
// going through the front end).
// The idempotency comes from existingDerivedImpls below, which records
// (typeName, ifaceName) pairs already synthesized in the input. That
// scan also distinguishes user-written collisions (manual `impl Iface
// for T` block alongside `derive Iface`) and surfaces them as compile errors per
// spec §38.5.
func SynthesizeDerives(nodes []ast.Node) ([]ast.Node, []TypeError) {
	prior := existingDerivedImpls(nodes)
	wrapping := wrappingDistincts(typeDeclsInNodeList(nodes))
	var synthesized []ast.Node
	var errs []TypeError
	for i, n := range nodes {
		site := synthSiteOf(i, n)
		site.wrapping = wrapping
		decls, decErrs := processTypeDeclDeriveDecorators(n, site, prior)
		synthesized = append(synthesized, decls...)
		errs = append(errs, decErrs...)
	}
	if len(synthesized) == 0 {
		return nodes, errs
	}
	// APPEND only. SynthesizeUniversalDebug runs on this slice next and
	// derives its own slots from the same top-level indices, so prepending
	// here would shift every declaration into a different slot between the
	// two passes.
	out := make([]ast.Node, 0, len(nodes)+len(synthesized))
	out = append(out, nodes...)
	out = append(out, synthesized...)
	return out, errs
}

// synthSiteOf builds the site for the declaration at top-level index i.
func synthSiteOf(i int, n ast.Node) synthSite {
	s := synthSite{origin: synthOriginDecl, index: i}
	s.line, s.col = declPos(n)
	return s
}

// declPos is a type declaration's own source position — what a diagnostic
// about an impl synthesized for it should name. Zero for anything else.
func declPos(n ast.Node) (int, int) {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Line, d.Col
	case *ast.EnumDef:
		return d.Line, d.Col
	case *ast.TypeDef:
		return d.Line, d.Col
	case *ast.ExternType:
		return d.Line, d.Col
	}
	return 0, 0
}

// priorDeriveImpls partitions in-slice `impl Iface for T` blocks that target
// `@derive`d types into two buckets: those previously synthesized by this
// pass (`synth` — distinguished by the disjoint synthLineBase position
// band stamped by uniquifyPositions) and those the user wrote by hand
// (`manual`). Idempotency consults `synth`; collision diagnostics consult
// `manual`. Keying by (typeName, ifaceName) lets the per-decl walker look
// each pair up in O(1).
type priorDeriveImpls struct {
	synth  map[string]map[string]bool         // (typeName, ifaceName) already covered by a synthesized FuncDef
	manual map[string]map[string]*ast.FuncDef // (typeName, ifaceName) → user-written colliding impl
}

// existingDerivedImpls scans `nodes` for `impl Iface for T` blocks
// targeting a `@derive`d type — i.e. name an interface <Iface> that
// is one of the supported derivable protocols, and whose receiver type
// names a struct/enum/typedef in the same file that
// ALSO carries `@derive <Iface>`. Returns a priorDeriveImpls that
// separates synthesized matches (used for idempotency) from user-written
// matches (used for spec §38.5 collision errors).
//
// Synth-band detection: every synthesized FuncDef's Line lands at
// `synthLineBase + k*16384` (see nextSynthBase / uniquifyPositions).
// Real source positions are 1-based and far below 2^30. So `fn.Line >=
// synthLineBase` is a reliable origin marker; user-written impls land in
// the `synth` bucket only by collision with that band, which a real
// source file cannot reach (32k cols × 1<<30/16384 lines worth of
// header).
//
// This shape handles the front-end-then-analyzer call sequence:
// SynthesizeDerives runs once in internal/frontend's Checker.Prepare,
// extending the slice; BuildProject's own call then sees the same slice and detects
// "already done" via `synth`. A user-written `impl Iface for T { fn ... }` for a
// `@derive`d type — which must collide because both register the same
// dispatch tuple — lands in `manual` and produces a targeted compile
// error rather than slipping past the synthesizer's skip and emerging
// later as "no impl found" or as silent override.
func existingDerivedImpls(nodes []ast.Node) priorDeriveImpls {
	// Index type-decl names that carry `@derive <Iface>` so we can match
	// them against subsequent `impl <Iface> for T` blocks.
	derived := map[string]map[string]bool{} // typeName → ifaceName → seen
	for _, n := range nodes {
		var name string
		var decs []ast.Decorator
		switch d := n.(type) {
		case *ast.StructDef:
			name, decs = d.Name, d.Decorators
		case *ast.EnumDef:
			name, decs = d.Name, d.Decorators
		case *ast.TypeDef:
			name, decs = d.Name, d.Decorators
		case *ast.ExternType:
			name, decs = d.Name, d.Decorators
		default:
			continue
		}
		if name == "" {
			continue
		}
		for i := range decs {
			d := &decs[i]
			if d.Name != "derive" {
				continue
			}
			for _, arg := range d.Args {
				if t, ok := arg.(*ast.SimpleType); ok && deriveSupported[t.Name] {
					if derived[name] == nil {
						derived[name] = map[string]bool{}
					}
					// Marker only — the second pass below decides whether
					// to skip based on a matching `impl Iface for T` block already
					// being present.
					derived[name][t.Name] = true
				}
			}
		}
	}
	// Now scan impls that name a derivable protocol and whose receiver matches
	// one of those `@derive`d type-decl names. Bucket each match by origin
	// (synth band vs source position). Impls take the block form
	// `impl Iface for T { fn }` (what synthesis now emits AND what
	// hand-written stdlib + user code use).
	out := priorDeriveImpls{
		synth:  map[string]map[string]bool{},
		manual: map[string]map[string]*ast.FuncDef{},
	}
	// record buckets one (typeName, ifaceName) pair, gated by the derive
	// index; `line` decides synth vs manual, `manualFn` is the FuncDef the
	// collision diagnostic points at (the impl block's method item).
	record := func(typeName, ifaceName string, line int, manualFn *ast.FuncDef) {
		if !deriveSupported[ifaceName] || !derived[typeName][ifaceName] {
			return
		}
		if line >= synthLineBase {
			if out.synth[typeName] == nil {
				out.synth[typeName] = map[string]bool{}
			}
			out.synth[typeName][ifaceName] = true
			return
		}
		if out.manual[typeName] == nil {
			out.manual[typeName] = map[string]*ast.FuncDef{}
		}
		// First-wins: if a file lists multiple manual impls for the same
		// (T, Iface), the duplicate-method check elsewhere catches the
		// second; we just need one to attach the collision diagnostic to.
		if _, dupe := out.manual[typeName][ifaceName]; !dupe {
			out.manual[typeName][ifaceName] = manualFn
		}
	}
	for _, n := range nodes {
		block, ok := n.(*ast.ImplBlock)
		if !ok || block.Interface == nil {
			continue
		}
		recvName := TypeExprBaseName(block.Receiver)
		ifaceName := TypeExprBaseName(block.Interface)
		if recvName == "" || ifaceName == "" {
			continue
		}
		if _, isDerivedType := derived[recvName]; !isDerivedType {
			continue
		}
		record(recvName, ifaceName, block.Line, firstFuncDefItem(block))
	}
	return out
}

// firstFuncDefItem returns the first `fn` item in an impl block, or nil for an
// bodyless / field-only impl. Used to point a collision diagnostic at a manual
// impl method when one exists.
func firstFuncDefItem(ib *ast.ImplBlock) *ast.FuncDef {
	if ib == nil {
		return nil
	}
	for _, item := range ib.Items {
		if fn, ok := item.(*ast.FuncDef); ok {
			return fn
		}
	}
	return nil
}

// builderSynthesizeDerives is the per-builder shim retained for the
// BuildFileWithStdlib → buildModule path. The free function above is the
// canonical entrypoint everywhere else.
func (b *builder) SynthesizeDerives(nodes []ast.Node) []ast.Node {
	out, errs := SynthesizeDerives(nodes)
	b.file.TypeErrors = append(b.file.TypeErrors, errs...)
	return out
}

// processTypeDeclDeriveDecorators returns synthesized FuncDef nodes for any
// `@derive` decorators on the given type-decl, plus any validation errors
// hit (unknown interface, duplicates, malformed args, manual-impl
// collisions per spec §38.5). Returns (nil, nil) for non-type nodes or
// nodes without decorators. The `prior` info — produced by
// existingDerivedImpls — short-circuits emission for (typeName,
// ifaceName) pairs already covered by a previously-synthesized impl, and
// surfaces collisions where the user wrote a manual `impl Iface for T` block
// alongside `@derive Iface`. Validation still runs for skipped pairs so
// duplicate-arg errors fire even on the second call.
func processTypeDeclDeriveDecorators(n ast.Node, site synthSite, prior priorDeriveImpls) ([]ast.Node, []TypeError) {
	var typeName string
	var decorators []ast.Decorator

	switch d := n.(type) {
	case *ast.StructDef:
		typeName, decorators = d.Name, d.Decorators
	case *ast.EnumDef:
		typeName, decorators = d.Name, d.Decorators
	case *ast.TypeDef:
		typeName, decorators = d.Name, d.Decorators
	case *ast.ExternType:
		typeName, decorators = d.Name, d.Decorators
	default:
		return nil, nil
	}
	if len(decorators) == 0 {
		return nil, nil
	}

	var out []ast.Node
	var errs []TypeError
	// Track interfaces seen across all `@derive` decorators on this decl —
	// `@derive Eq @derive Eq` (stacked form) and `@derive Eq, Eq` (comma form)
	// must both surface as duplicates.
	seenIfaces := map[string]bool{}

	for i := range decorators {
		dec := &decorators[i]
		if dec.Name != "derive" {
			// The parser gates type-decl decorators to `@derive`. Anything else
			// reaching here would mean the parser gate broke; record an
			// analyzer-side error rather than silently ignoring so the
			// regression is visible.
			errs = append(errs, TypeError{
				Line:    dec.Line,
				Col:     dec.Col,
				Message: fmt.Sprintf("decorator '@%s' on type '%s': only @derive is supported on type declarations", dec.Name, typeName),
			})
			continue
		}
		if len(dec.Args) == 0 {
			errs = append(errs, TypeError{
				Line:    dec.Line,
				Col:     dec.Col,
				Message: fmt.Sprintf("@derive on type '%s' requires at least one interface argument", typeName),
			})
			continue
		}
		for _, arg := range dec.Args {
			ifaceName, argLine, argCol := deriveArgName(arg, dec)
			if ifaceName == "" {
				errs = append(errs, TypeError{
					Line:    argLine,
					Col:     argCol,
					Message: fmt.Sprintf("@derive arg on type '%s' must be an interface name (PascalCase)", typeName),
				})
				continue
			}
			if !deriveSupported[ifaceName] {
				errs = append(errs, TypeError{
					Line:    argLine,
					Col:     argCol,
					Message: fmt.Sprintf("@derive %s on type '%s': unknown derivable protocol (v1 supports %s)", ifaceName, typeName, deriveSupportedDescription()),
				})
				continue
			}
			if seenIfaces[ifaceName] {
				errs = append(errs, TypeError{
					Line:    argLine,
					Col:     argCol,
					Message: fmt.Sprintf("duplicate @derive %s on type '%s'", ifaceName, typeName),
				})
				continue
			}
			seenIfaces[ifaceName] = true
			if _, optErrs := validateDeriveOptions(ifaceName, dec.Options, argLine, argCol); len(optErrs) > 0 {
				errs = append(errs, optErrs...)
				continue
			}
			if targetErrs := validateDeriveTarget(ifaceName, n, argLine, argCol); len(targetErrs) > 0 {
				errs = append(errs, targetErrs...)
				continue
			}

			// Spec §38.5: a manual `impl Iface for T { fn ... }` alongside
			// `@derive Iface` on the same type is a compile error.
			// Surface it here and skip emission so the analyzer's
			// duplicate-method validation doesn't pile a misleading
			// "duplicate equals fn" diagnostic on top.
			if prior.manual != nil {
				if manualFn, hit := prior.manual[typeName][ifaceName]; hit && manualFn != nil {
					errs = append(errs, TypeError{
						Line: argLine,
						Col:  argCol,
						Message: fmt.Sprintf(
							"@derive %s on type '%s' collides with manual impl %s for %s (fn %s at %d:%d) — remove one of the two",
							ifaceName, typeName, ifaceName, typeName, manualFn.Name, manualFn.Line, manualFn.Col,
						),
					})
					continue
				}
			}

			// Skip emission if a previous SynthesizeDerives call already
			// added the impl block for this (type, iface) pair. The
			// validation above (duplicates, unknowns) still ran, so users
			// continue to see those errors on every pass.
			if prior.synth != nil && prior.synth[typeName][ifaceName] {
				continue
			}
			out = append(out, synthesizeDeriveFor(ifaceName, n, dec.Options, site)...)
		}
	}
	return out, errs
}

// synthesizeDeriveFor dispatches to the per-protocol synthesizer for a
// validated (supported) interface name. Each synthesizer emits a full
// structural body (Equatable / Hashable / Comparable / Debug).
func synthesizeDeriveFor(ifaceName string, typeNode ast.Node, options ast.Node, site synthSite) []ast.Node {
	var fns []ast.Node
	opts := parseDeriveOptionsForSynthesis(ifaceName, options)
	switch ifaceName {
	case "Equatable":
		fns = synthesizeDeriveEquatable(typeNode, site)
	case "Hashable":
		fns = synthesizeDeriveHashable(typeNode, site)
	case "Comparable":
		fns = synthesizeDeriveComparable(typeNode, site)
	case "Debug":
		fns = synthesizeDeriveDebug(typeNode, site, synthKindDebug)
	case "Display":
		fns = synthesizeDeriveDisplay(typeNode, site)
	case "ToJson":
		fns = synthesizeDeriveToJson(typeNode, opts, site)
	case "FromJson":
		fns = synthesizeDeriveFromJson(typeNode, opts, site)
	default:
		// deriveSupported gate above guarantees this is unreachable; kept as
		// a belt-and-braces guard so a future addition to deriveSupported
		// without a matching case here surfaces immediately rather than
		// silently emitting nothing.
		return nil
	}
	return wrapSynthAsImplBlocks(fns, ifaceName, site)
}

// wrapSynthAsImplBlocks wraps each synthesized method FuncDef into
// the equivalent block-form `*ast.ImplBlock`, so synthesis emits the SAME shape
// hand-written code uses — `@derive` and universal Debug produce `impl Iface for
// T { fn }` blocks. The interface comes from the caller-supplied
// ifaceName, the receiver from the FuncDef's first param type except for
// receiver-less constructor interfaces such as FromJson, whose receiver is
// recovered from `Result<Receiver, Json.ShapeError>`. Generics are COPIED onto the block
// header (so the receiver `Box<T>` resolves through the block) AND kept on the
// FuncDef (checkImplBlock checks each item via checkFunc, which reads
// fn.TypeParams to bring T into scope; the generic-derive tests also read the
// bound off fn.TypeParams). The block inherits the FuncDef's synth-band position
// so synth-vs-user origin detection (Line >= synthLineBase) and orphan-rule
// home-module attribution keep working; the FuncDef item keeps its AutoSynth
// flag for Debug precedence. Param-less FuncDefs (and any non-FuncDef node)
// pass through unchanged.
func wrapSynthAsImplBlocks(nodes []ast.Node, ifaceName string, site synthSite) []ast.Node {
	out := make([]ast.Node, 0, len(nodes))
	for _, n := range nodes {
		fn, ok := n.(*ast.FuncDef)
		if !ok {
			out = append(out, n) // not a synth method — leave as-is
			continue
		}
		receiver := synthImplReceiver(fn, ifaceName)
		if receiver == nil {
			out = append(out, n)
			continue
		}
		out = append(out, &ast.ImplBlock{
			// Header interface from the caller's protocol name; receiver from the
			// method's first param type. The interface node reuses the fn's
			// (already-uniquified) synth-band position — aliasing within the
			// synth band is filtered by IsSynthesizedLine, so it never surfaces.
			Interface: &ast.SimpleType{Name: ifaceName, Line: fn.Line, Col: fn.Col},
			Receiver:  receiver,
			Generics:  fn.TypeParams, // copy onto the header; keep on the fn too
			Items:     []ast.Node{fn},
			Line:      fn.Line,
			Col:       fn.Col,
			// Where a diagnostic about this block should point. The block's
			// own position is in the synth band and names nothing a
			// programmer can navigate to; the declaration it was synthesized
			// for is real source.
			SynthOriginLine: site.line,
			SynthOriginCol:  site.col,
		})
	}
	return out
}

func synthImplReceiver(fn *ast.FuncDef, ifaceName string) ast.TypeExpr {
	if ifaceName == "FromJson" {
		if ret, ok := fn.ReturnTypeExpr.(*ast.GenericType); ok && ret.Name == "Result" && len(ret.Params) >= 1 {
			return ret.Params[0]
		}
		return nil
	}
	if len(fn.Params) == 0 {
		return nil
	}
	return fn.Params[0].TypeAnnotation
}

// ---------------------------------------------------------------------------
// Universal default Debug (auto-synthesis)
// ---------------------------------------------------------------------------

// SynthesizeUniversalDebug is the eager universal-Debug pass. After
// SynthesizeDerives has run (so any explicit `@derive Debug` impls are
// already in `nodes`), this walks every top-level type declaration and
// appends an auto-synthesized `impl Debug` block for each type that lacks ANY
// explicit Debug — neither a hand-written `impl Debug for T` block nor an explicit
// `@derive Debug`. The result is that every declared nominal type is
// Debug-inspectable with no `@derive`/`impl` required (spec: universal
// default Debug).
//
// Precedence is enforced by the "skip if any explicit Debug exists" guard:
// explicit `impl Debug` block > explicit `@derive Debug` > auto-synth. That guard
// is also what keeps the coherence checks happy — the pass never adds a
// second impl for an existing (Debug, T) pair, so detectImplCollisions
// stays quiet.
//
// Run per-file (mirroring SynthesizeDerives' call shape in BuildProject):
// each auto-impl lands in the declaring file's node slice, which gives the
// orphan rule correct home-module attribution automatically (T is always
// local to the file that declares it). The original input slice is not
// mutated.
//
// Idempotent: a synthesized auto-Debug impl, like a derived one, is an
// `impl Debug for T` block targeting the type, so a second run of this pass
// sees it via the same explicit-Debug scan and skips re-emission.
func SynthesizeUniversalDebug(nodes []ast.Node) []ast.Node {
	explicit := typesWithExplicitDebug(nodes)
	wrapping := wrappingDistincts(typeDeclsInNodeList(nodes))
	var synthesized []ast.Node
	for i, n := range nodes {
		// typeDeclName recognises struct/enum/typedef AND host types:
		// declared host types are nominal types too — values of them
		// reach Debug dispatch (e.g. a zero-sized extern singleton bound
		// from an `embeds` variant pattern), so they get the name-only
		// auto default like every other declared type, and so do the Go
		// handles the VM holds as rt.HostHandle.
		typeName, ok := typeDeclName(n)
		if !ok {
			// Not a type decl (FuncDef, import, etc.) — universal Debug only
			// synthesizes for declared nominal types. Non-nominal structural
			// kinds (tuples, anon structs) and built-in value kinds are
			// rendered structurally by rt.DebugText, not here.
			continue
		}
		if explicit[typeName] {
			continue
		}
		// One impl per name. A second declaration of the same name is a
		// redeclaration the builder reports at its own line; a second
		// synthesized impl would add a duplicate-impl error at a
		// synthesized line that says nothing about the real cause.
		explicit[typeName] = true
		site := synthSiteOf(i, n)
		site.wrapping = wrapping
		synthesized = append(synthesized, synthesizeAutoDebug(n, site)...)
	}
	if len(synthesized) == 0 {
		return nodes
	}
	out := make([]ast.Node, 0, len(nodes)+len(synthesized))
	out = append(out, nodes...)
	out = append(out, synthesized...)
	return out
}

// builderSynthesizeUniversalDebug is the per-builder shim mirroring
// builderSynthesizeDerives — kept so the BuildProject call site reads
// uniformly (`nodes = b.SynthesizeUniversalDebug(nodes)`). The pass itself
// produces no errors (it only emits well-formed structural / name-only
// impls), so there is nothing to fold into b.file.TypeErrors.
func (b *builder) SynthesizeUniversalDebug(nodes []ast.Node) []ast.Node {
	return SynthesizeUniversalDebug(nodes)
}

// typesWithExplicitDebug returns the set of type names that already carry an
// explicit Debug — either a hand-written `impl Debug for T { fn inspect(value: T) }`
// block, or an explicit `@derive Debug` decorator on the type decl
// (whose synthesized impl block SynthesizeDerives produced, also captured here via
// the impl-block scan). Both forms suppress auto-synthesis for that type.
//
// The `@derive Debug` decorator is scanned directly (not only via the
// produced FuncDef) so the guard is robust even if SynthesizeUniversalDebug
// is somehow run before SynthesizeDerives — the auto path still defers to
// the explicit derive.
func typesWithExplicitDebug(nodes []ast.Node) map[string]bool {
	out := map[string]bool{}
	for _, n := range nodes {
		// Explicit block-form `impl Debug for T { ... }` (hand-written OR
		// derive-/auto-synthesized — all synthesis emits blocks). A Debug block
		// suppresses auto-synthesis for its receiver type.
		if blk, ok := n.(*ast.ImplBlock); ok {
			if blk.Interface != nil && TypeExprBaseName(blk.Interface) == "Debug" {
				if recv := TypeExprBaseName(blk.Receiver); recv != "" {
					out[recv] = true
				}
			}
			continue
		}
		// Explicit @derive Debug decorator on the type decl.
		var typeName string
		var decs []ast.Decorator
		switch d := n.(type) {
		case *ast.StructDef:
			typeName, decs = d.Name, d.Decorators
		case *ast.EnumDef:
			typeName, decs = d.Name, d.Decorators
		case *ast.TypeDef:
			typeName, decs = d.Name, d.Decorators
		case *ast.ExternType:
			typeName, decs = d.Name, d.Decorators
		default:
			continue
		}
		for i := range decs {
			d := &decs[i]
			if d.Name != "derive" {
				continue
			}
			for _, arg := range d.Args {
				if st, ok := arg.(*ast.SimpleType); ok && st.Name == "Debug" {
					out[typeName] = true
				}
			}
		}
	}
	return out
}

// synthesizeAutoDebug produces the auto/universal-default Debug `impl` block for a
// single type declaration. It owns two degenerate-body rules:
//
//   - OPAQUE: for an opaque struct/enum/distinct type the auto-default is a
//     name-only body `"<opaque TypeName>"` — a global structural impl would
//     leak exactly the internals `opaque` exists to hide (spec §15.3).
//   - EXTERN: for a declared `host type` the auto-default is the bare type
//     name `"TypeName"` — the declaration carries no payload shape the
//     synthesizer can see (internals are host-side), which is the same shape
//     as a zero-sized distinct, and those render as their bare name. The bare
//     name (no `<...>` marker) is also what makes `embeds`-variant delegation
//     come out right: an enum embedding a zero-sized extern singleton
//     (`embeds True`) renders the variant as `True`, exactly like a
//     zero-sized distinct embed.
//
// For every other (non-opaque, non-extern) type it delegates to the existing
// STRUCTURAL synthesizer (synthesizeDeriveDebug), producing output
// byte-identical to an explicit `@derive Debug`.
//
// The opaque branch lives here, not inside synthesizeDeriveStringify, on
// purpose: an explicit `@derive Debug` on an opaque type is a deliberate
// structural OVERRIDE (`NonZeroInt(5)`), so the structural synthesizer must
// stay opacity-blind. Only this auto path goes name-only for opaque types.
func synthesizeAutoDebug(typeNode ast.Node, site synthSite) []ast.Node {
	var out []ast.Node
	if et, ok := typeNode.(*ast.ExternType); ok {
		out = synthesizeExternNameOnlyDebug(et, site)
	} else if typeDeclIsOpaque(typeNode) {
		out = synthesizeOpaqueNameOnlyDebug(typeNode, site)
	} else {
		out = synthesizeDeriveDebug(typeNode, site, synthKindAutoDebug)
	}
	// Tag every emitted impl as auto-synthesized so the coherence check can
	// let an explicit (hand-written / @derive) Debug impl
	// win over this one for the same (Debug, T) — see ast.FuncDef.AutoSynth.
	// Tag BEFORE wrapping so the flag lands on the FuncDef item, then wrap into
	// block form (synthesis emits `impl Debug for T { fn }`).
	for _, n := range out {
		if fn, ok := n.(*ast.FuncDef); ok {
			fn.AutoSynth = true
		}
	}
	return wrapSynthAsImplBlocks(out, "Debug", site)
}

// typeDeclIsOpaque reports whether the type-decl node carries the `opaque`
// modifier. Returns false for non-type nodes.
func typeDeclIsOpaque(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.StructDef:
		return v.Opaque
	case *ast.EnumDef:
		return v.Opaque
	case *ast.TypeDef:
		return v.Opaque
	}
	return false
}

// synthesizeOpaqueNameOnlyDebug emits the degenerate name-only Debug impl for
// an opaque type: `impl Debug for T { fn inspect(value: T): String { "<opaque T>" } }`.
// The body is a single static StringLit — no field access, no recursion —
// so nothing about the type's hidden representation is exposed. Generic
// opaque types still carry their type params on the impl (with a harmless
// Debug bound, auto-satisfied by the checker) so the receiver type-expr
// resolves through the same generic-instantiation path as the structural
// synthesizer.
func synthesizeOpaqueNameOnlyDebug(typeNode ast.Node, site synthSite) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		return nil
	}
	line := site.base(synthKindAutoDebug)
	col := 1

	srcParams := typeDeclTypeParams(typeNode)
	fn := &ast.FuncDef{
		Name:           "inspect",
		TypeParams:     boundedTypeParams(srcParams, "Debug", line, col),
		Params:         []ast.Param{{Name: "value", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col}},
		ReturnTypeExpr: &ast.SimpleType{Name: "String", Line: line, Col: col},
		Body: &ast.Block{
			Stmts: []ast.Node{exprStmt(staticString("<opaque "+typeName+">", line, col), line, col)},
			Line:  line,
			Col:   col,
		},
		Line: line,
		Col:  col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

// synthesizeExternNameOnlyDebug emits the bare-name Debug impl for a declared
// `host type`: `impl Debug for T { fn inspect(value: T): String { "T" } }`.
// Like the opaque body it is a single static StringLit — extern internals are
// host-side, so there is nothing structural for the body to reach — but the
// rendering is the BARE name (matching zero-sized distincts like
// `type Expired` → "Expired"), not an `<...>`-marked placeholder: a
// zero-sized extern singleton flowing out of an `embeds` pattern binding
// (`embeds True` -> a bare `True` value) must inspect as `True`.
// Generic host types (`host type Task<T>`) carry their type params on the
// impl the same way the opaque synthesizer does.
func synthesizeExternNameOnlyDebug(et *ast.ExternType, site synthSite) []ast.Node {
	line := site.base(synthKindAutoDebug)
	col := 1

	srcParams := et.TypeParams
	fn := &ast.FuncDef{
		Name:           "inspect",
		TypeParams:     boundedTypeParams(srcParams, "Debug", line, col),
		Params:         []ast.Param{{Name: "value", TypeAnnotation: receiverTypeExpr(et.Name, srcParams, line, col), Line: line, Col: col}},
		ReturnTypeExpr: &ast.SimpleType{Name: "String", Line: line, Col: col},
		Body: &ast.Block{
			Stmts: []ast.Node{exprStmt(staticString(et.Name, line, col), line, col)},
			Line:  line,
			Col:   col,
		},
		Line: line,
		Col:  col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

// ---------------------------------------------------------------------------
// Equatable synthesizer (Task 9)
// ---------------------------------------------------------------------------

// synthesizeDeriveEquatable emits a single `impl Equatable for T { fn equal?(a: T, b:
// T): Bool { ... } }` block whose body is structurally appropriate for the
// type's shape:
//
//   - Struct: AND-chain of pairwise `Equatable.equal?(a.f, b.f)` calls; for a
//     0-field struct the body is bare `True`.
//   - Enum: outer `case a` with one branch per variant; each branch is a
//     nested `case b` that matches the same variant (returning the structural
//     comparison of payload components) or `_ -> False`. Nested case is used
//     uniformly because Nomi's tuple patterns don't accept struct-shaped
//     variant payloads inside `case (a, b) { ... }`, and the nested form
//     handles every variant kind without special-casing.
//   - Distinct type: zero-sized → `True`; tuple inner → AND-chain of
//     pairwise `Equatable.equal?(a_unwrapped.<i>, b_unwrapped.<i>)`; primitive
//     or generic inner → single `Equatable.equal?` on the unwrapped values.
//
// Position info on every synthesized node points at the type decl's own
// line/col so diagnostics that follow back to the synthesized fn land on the
// `@derive` site rather than off-by-N somewhere in space.
func synthesizeDeriveEquatable(typeNode ast.Node, site synthSite) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		// Defensive: only struct/enum/typedef should reach here, but if a
		// future caller widens the dispatch we want a clear "no-op" rather
		// than a panic.
		return nil
	}
	// Take this site's Equatable slot. We build the AST with this single
	// (line, col) on every node, then walk the resulting tree once and
	// assign a unique (line, col) per node so the analyzer's References /
	// Definitions maps don't suffer from position aliasing. Real source
	// positions are 1-based < 2^30, so the slot band is disjoint from real
	// input.
	line := site.base(synthKindEquatable)
	col := 1

	body := equatableBody(typeNode, line, col)
	if body == nil {
		return nil
	}

	srcParams := typeDeclTypeParams(typeNode)
	fn := &ast.FuncDef{
		Name:       "equal?",
		TypeParams: boundedTypeParams(srcParams, "Equatable", line, col),
		Params: []ast.Param{
			{Name: "a", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col},
			{Name: "b", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col},
		},
		ReturnTypeExpr: &ast.SimpleType{Name: "Bool", Line: line, Col: col},
		Body:           &ast.Block{Stmts: body, Line: line, Col: col},
		Line:           line,
		Col:            col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

// equatableBody returns the statement list for the synthesized fn body,
// dispatched on the type decl's shape. Each value-position expression is
// wrapped in *ast.ExprStmt to match what the parser produces for top-level
// block statements — without the wrapper the analyzer's body walker
// silently skips nodes that aren't recognised statements.
func equatableBody(typeNode ast.Node, line, col int) []ast.Node {
	switch t := typeNode.(type) {
	case *ast.StructDef:
		return []ast.Node{exprStmt(equatableStructExpr(t.Fields, "a", "b", line, col), line, col)}
	case *ast.EnumDef:
		return []ast.Node{exprStmt(equatableEnumExpr(t, line, col), line, col)}
	case *ast.TypeDef:
		return equatableDistinctStmts(t, line, col)
	case *ast.ExternType:
		// Derive-as-assertion: `@derive` on an host type asserts trivial
		// structure (a zero-sized singleton — one inhabitant), so any two
		// values are equal. Same constant body as a zero-sized distinct.
		return []ast.Node{exprStmt(trueLit(line, col), line, col)}
	}
	return nil
}

// equatableStructExpr builds the AND-chain of `Equatable.equal?(<obj1>.f, <obj2>.f)`
// for each of the named struct's fields. obj1 and obj2 name the two
// receivers (e.g. "a", "b" for direct field access, or "av", "bv" inside a
// destructured nested case body — currently only the top-level "a"/"b"
// shape is used by the synthesizer). For 0-field structs the body is the
// bare True variant ident (matches the spec's "two empty records are
// equal" rule).
func equatableStructExpr(fields []ast.StructField, obj1, obj2 string, line, col int) ast.Node {
	if len(fields) == 0 {
		return trueLit(line, col)
	}
	parts := make([]ast.Node, len(fields))
	for i, f := range fields {
		parts[i] = equatableEqualsCall(
			fieldAccess(identExpr(obj1, line, col), f.Name, line, col),
			fieldAccess(identExpr(obj2, line, col), f.Name, line, col),
			line, col,
		)
	}
	return andChain(parts, line, col)
}

// equatableEnumExpr builds the outer `case a { ... }` expression. Each
// variant in the enum gets one branch whose body is a nested
// `case b { Variant(...) -> <equality> | _ -> False }`. This nested-case
// shape is uniform across bare / positional / struct / embedded variants —
// the alternative tuple-pattern form `case (a, b) { (V(x), V(y)) -> ... }`
// can't carry struct-shaped variant patterns inside the tuple, so we always
// nest.
func equatableEnumExpr(e *ast.EnumDef, line, col int) ast.Node {
	branches := make([]ast.CaseBranch, 0, len(e.Variants))
	for _, v := range e.Variants {
		branches = append(branches, ast.CaseBranch{
			Pattern: enumVariantPatternForA(e.Name, v, line, col),
			Body:    equatableNestedCaseOnB(e.Name, v, line, col),
			Line:    line,
			Col:     col,
		})
	}
	return &ast.Case{
		Value:    identExpr("a", line, col),
		Branches: branches,
		Line:     line,
		Col:      col,
	}
}

// enumVariantPatternForA builds the outer-case pattern that matches the
// variant in `a` and binds its payload components to a-side names
// (`a0`, `a1`, ...) for positional / embedded variants, or `<field>_a` for
// struct variants. Bare variants produce a bare EnumPattern with no
// payload bindings.
func enumVariantPatternForA(enumName string, v ast.EnumVariant, line, col int) ast.Node {
	return enumVariantPattern(enumName, v, "_a", line, col)
}

// enumVariantPatternForB is the b-side mirror: bind names with a `_b`
// suffix so the inner case branch body can reference both sides without
// collision (`a0` / `b0`, or `<field>_a` / `<field>_b`).
func enumVariantPatternForB(enumName string, v ast.EnumVariant, line, col int) ast.Node {
	return enumVariantPattern(enumName, v, "_b", line, col)
}

// enumVariantPattern returns an EnumPattern whose Variant is the qualified
// `EnumName.Variant` and whose payload bindings are named with the given
// suffix (`_a` or `_b`). The shape depends on the variant kind:
//
//   - bare:       no payload, just `EnumName.Variant`
//   - positional: arity 1 → Binding=<name>; arity ≥ 2 → flat tuple pattern
//   - struct:     StructPattern with field-bound names
//   - embedded:   single positional binding (treat the embedded type as
//     one payload value the user already has destructure for —
//     we just pass the whole thing through `Equatable.equal?`)
func enumVariantPattern(enumName string, v ast.EnumVariant, suffix string, line, col int) ast.Node {
	variantTypeExpr := qualifiedTypeExpr(enumName, v.Name, line, col)
	switch v.Kind {
	case "bare":
		return &ast.EnumPattern{Variant: variantTypeExpr, Line: line, Col: col}
	case "positional":
		// DataTypeExpr is either a single type or a FuncType (tuple) for
		// arity ≥ 2. Single → one ident binding. Tuple → nested tuple
		// pattern with one ident per arg. Canonical form is
		// `Variant((a0, a1, ...))` (Flat: false); the analyzer rejects
		// the flat shorthand for variant payloads (checker.go: "variant
		// pattern '...': multi-binding is not supported").
		if ft, isTuple := v.DataTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
			patterns := make([]ast.Node, len(ft.Params))
			for i := range ft.Params {
				name := positionalBindName(i, suffix)
				patterns[i] = &ast.IdentPattern{Name: name, Line: line, Col: col}
			}
			return &ast.EnumPattern{
				Variant: variantTypeExpr,
				Payload: &ast.TuplePattern{Patterns: patterns, Flat: false, Line: line, Col: col},
				Line:    line,
				Col:     col,
			}
		}
		return &ast.EnumPattern{
			Variant:    variantTypeExpr,
			Binding:    positionalBindName(0, suffix),
			BindingCol: col,
			Line:       line,
			Col:        col,
		}
	case "struct":
		fields := make([]ast.StructPatternField, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = ast.StructPatternField{
				Name:    f.Name,
				Binding: f.Name + suffix,
			}
		}
		return &ast.StructPattern{
			TypeName: variantTypeExpr,
			Fields:   fields,
			Line:     line,
			Col:      col,
		}
	case "embedded":
		// Treat the embedded type as a single-binding payload; the body
		// recurses into `Equatable.equal?` on the bound value.
		return &ast.EnumPattern{
			Variant:    variantTypeExpr,
			Binding:    positionalBindName(0, suffix),
			BindingCol: col,
			Line:       line,
			Col:        col,
		}
	}
	// Defensive default: bare-variant pattern.
	return &ast.EnumPattern{Variant: variantTypeExpr, Line: line, Col: col}
}

// positionalBindName returns the per-position binding name for a flat
// tuple destructure: `a0`/`b0`, `a1`/`b1`, etc. The arity-1 form ("a"/"b"
// would shadow the function params) instead uses the suffixed numeric form
// `a0` / `b0` — uniform across arities. `suffix` is "_a" or "_b"; we strip
// the leading underscore so the binding is a clean identifier.
func positionalBindName(i int, suffix string) string {
	return suffix[1:] + strconv.Itoa(i)
}

// equatableNestedCaseOnB builds the inner `case b { Variant(...) -> <eq> | _ -> False }`
// for one outer-variant branch. The variant-match body is the structural
// equality between the matched-on-a payload and the matched-on-b payload;
// the wildcard branch returns False.
func equatableNestedCaseOnB(enumName string, v ast.EnumVariant, line, col int) ast.Node {
	branches := []ast.CaseBranch{
		{
			Pattern: enumVariantPatternForB(enumName, v, line, col),
			Body:    equatableVariantPayloadEq(v, line, col),
			Line:    line,
			Col:     col,
		},
		{
			Pattern: &ast.WildcardPattern{Line: line, Col: col},
			Body:    falseLit(line, col),
			Line:    line,
			Col:     col,
		},
	}
	return &ast.Case{
		Value:    identExpr("b", line, col),
		Branches: branches,
		Line:     line,
		Col:      col,
	}
}

// equatableVariantPayloadEq builds the equality body for one variant arm:
//
//   - bare:       True (no payload to compare)
//   - positional arity 1: Equatable.equal?(a0, b0)
//   - positional arity N: AND-chain of Equatable.equal?(aI, bI)
//   - struct:     AND-chain of Equatable.equal?(<field>_a, <field>_b)
//   - embedded:   Equatable.equal?(a0, b0) on the bound embedded value
func equatableVariantPayloadEq(v ast.EnumVariant, line, col int) ast.Node {
	switch v.Kind {
	case "bare":
		return trueLit(line, col)
	case "positional":
		arity := 1
		if ft, isTuple := v.DataTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
			arity = len(ft.Params)
		}
		parts := make([]ast.Node, arity)
		for i := 0; i < arity; i++ {
			parts[i] = equatableEqualsCall(
				identExpr(positionalBindName(i, "_a"), line, col),
				identExpr(positionalBindName(i, "_b"), line, col),
				line, col,
			)
		}
		return andChain(parts, line, col)
	case "struct":
		if len(v.Fields) == 0 {
			return trueLit(line, col)
		}
		parts := make([]ast.Node, len(v.Fields))
		for i, f := range v.Fields {
			parts[i] = equatableEqualsCall(
				identExpr(f.Name+"_a", line, col),
				identExpr(f.Name+"_b", line, col),
				line, col,
			)
		}
		return andChain(parts, line, col)
	case "embedded":
		return equatableEqualsCall(
			identExpr(positionalBindName(0, "_a"), line, col),
			identExpr(positionalBindName(0, "_b"), line, col),
			line, col,
		)
	}
	return trueLit(line, col)
}

// equatableDistinctStmts handles `type Name InnerType` and the zero-sized
// `type Name`. For zero-sized: body is True. For tuple inner: unwrap into
// `Name(av) = a; Name(bv) = b;` and AND-chain
// `Equatable.equal?(av.<i>, bv.<i>)` over all tuple positions. For
// non-tuple inner (primitive / generic / qualified): unwrap and compare
// the single inner values via Equatable.equal?.
func equatableDistinctStmts(t *ast.TypeDef, line, col int) []ast.Node {
	if t.InnerTypeExpr == nil {
		return []ast.Node{exprStmt(trueLit(line, col), line, col)}
	}
	// Unwrap both sides into `av` / `bv`. DistinctDestructure is a
	// statement node, not an expression — no ExprStmt wrap.
	stmts := []ast.Node{
		&ast.DistinctDestructure{
			TypeName: t.Name,
			Binding:  &ast.Ident{Name: "av", Line: line, Col: col},
			Value:    identExpr("a", line, col),
			Line:     line,
			Col:      col,
		},
		&ast.DistinctDestructure{
			TypeName: t.Name,
			Binding:  &ast.Ident{Name: "bv", Line: line, Col: col},
			Value:    identExpr("b", line, col),
			Line:     line,
			Col:      col,
		},
	}
	// Tuple inner type → element-wise equality on `.0`, `.1`, ...
	if ft, isTuple := t.InnerTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 1 {
		// Note: a 1-arity FuncType with Return == nil shouldn't occur from
		// the parser (`type Name (Int)` is grouped, not a tuple), but if
		// it ever does this still produces a single-component AND-chain
		// which is just one Equatable.equal? call — semantically correct.
		parts := make([]ast.Node, len(ft.Params))
		for i := range ft.Params {
			parts[i] = equatableEqualsCall(
				fieldAccess(identExpr("av", line, col), strconv.Itoa(i), line, col),
				fieldAccess(identExpr("bv", line, col), strconv.Itoa(i), line, col),
				line, col,
			)
		}
		stmts = append(stmts, exprStmt(andChain(parts, line, col), line, col))
		return stmts
	}
	// Single inner value → recurse via Equatable.equal? on the unwrapped
	// values.
	stmts = append(stmts, exprStmt(equatableEqualsCall(
		identExpr("av", line, col),
		identExpr("bv", line, col),
		line, col,
	), line, col))
	return stmts
}

// ---------------------------------------------------------------------------
// AST construction helpers
// ---------------------------------------------------------------------------

// uniquifyPositions walks the synthesized FuncDef and stamps every
// position-bearing node with a unique (line, col) within the FuncDef's
// reserved line band. Construction left every node sharing the FuncDef's
// (line, col); the analyzer's References / Definitions maps key by
// position, so the shared-position state would alias unrelated nodes
// (struct symbols clobbered by FuncDef symbols, body field-access
// References overwriting each other, etc.). This walker is the single
// place that hands out unique positions; it runs once per FuncDef right
// after construction.
//
// The reserved band starts at `baseLine`; we walk by column within that
// line and roll to baseLine+1 once a line would exceed 16383 columns.
// Callers (synthesizeDeriveEquatable) call nextSynthBase() to claim a
// disjoint band per FuncDef.
//
// Contract for new synthesizers (Tasks 10-12 — Hashable, Comparable,
// Debug — and any future @derive emitters): every synthesizer must
// extend the uniquifyState.node walker when it introduces a new
// ast.Node kind into synthesized output (Block, IntLit, StringLit,
// Lambda, If, StructLit, etc.). The walker's default case panics so
// unhandled types fail loudly during development rather than silently
// retaining the FuncDef's (line, col) and triggering position-aliasing
// bugs in the analyzer's References / Definitions maps.
func uniquifyPositions(fn *ast.FuncDef, baseLine int) {
	state := &uniquifyState{line: baseLine, col: 0}
	state.fn(fn)
}

type uniquifyState struct {
	line int
	col  int
}

func (s *uniquifyState) bump() (int, int) {
	s.col++
	if s.col > 16383 {
		s.line++
		s.col = 1
	}
	return s.line, s.col
}

func (s *uniquifyState) fn(fn *ast.FuncDef) {
	fn.Line, fn.Col = s.bump()
	for i := range fn.Decorators {
		s.decorator(&fn.Decorators[i])
	}
	for i := range fn.TypeParams {
		s.typeParam(&fn.TypeParams[i])
	}
	for i := range fn.Params {
		s.param(&fn.Params[i])
	}
	if fn.ReturnTypeExpr != nil {
		s.typeExpr(fn.ReturnTypeExpr)
	}
	if fn.Body != nil {
		fn.Body.Line, fn.Body.Col = s.bump()
		for _, stmt := range fn.Body.Stmts {
			s.node(stmt)
		}
	}
}

// typeParam walks a TypeParam — its name position plus each bound's
// type-expression. The TypeParam's own Line/Col is the binder, used by
// the analyzer's defineTypeParams to register the param symbol; bounds are
// walked as type expressions so each interface name registers as a
// Reference.
func (s *uniquifyState) typeParam(tp *ast.TypeParam) {
	tp.Line, tp.Col = s.bump()
	for _, bound := range tp.Bounds {
		s.typeExpr(bound)
	}
}

func (s *uniquifyState) decorator(d *ast.Decorator) {
	d.Line, d.Col = s.bump()
	for _, arg := range d.Args {
		// Decorator.Args may be a TypeExpr (e.g. `@derive Equatable` →
		// *ast.SimpleType) or a value-position node. Dispatch by interface
		// to keep both paths flowing through the position-bumping walker.
		if te, ok := arg.(ast.TypeExpr); ok {
			s.typeExpr(te)
			continue
		}
		s.node(arg)
	}
	if d.Options != nil {
		s.node(d.Options)
	}
}

func (s *uniquifyState) param(p *ast.Param) {
	p.Line, p.Col = s.bump()
	if p.TypeAnnotation != nil {
		s.typeExpr(p.TypeAnnotation)
	}
}

func (s *uniquifyState) typeExpr(te ast.TypeExpr) {
	switch t := te.(type) {
	case *ast.SimpleType:
		t.Line, t.Col = s.bump()
	case *ast.QualifiedType:
		t.ModuleLine, t.ModuleCol = s.bump()
		s.typeExpr(t.Member)
	case *ast.GenericType:
		t.Line, t.Col = s.bump()
		for _, p := range t.Params {
			s.typeExpr(p)
		}
	case *ast.FuncType:
		t.Line, t.Col = s.bump()
		for _, p := range t.Params {
			s.typeExpr(p)
		}
		if t.Return != nil {
			s.typeExpr(t.Return)
		}
	}
}

func (s *uniquifyState) node(n ast.Node) {
	if n == nil {
		return
	}
	switch e := n.(type) {
	case *ast.ExprStmt:
		e.Line, e.Col = s.bump()
		s.node(e.Expr)
	case *ast.Binary:
		e.Line, e.Col = s.bump()
		s.node(e.Left)
		s.node(e.Right)
	case *ast.Call:
		e.Line, e.Col = s.bump()
		s.node(e.Func)
		for _, a := range e.Args {
			s.node(a)
		}
	case *ast.StructLit:
		e.Line, e.Col = s.bump()
		if e.TypeName != nil {
			s.typeExpr(e.TypeName)
		}
		for i := range e.Fields {
			e.Fields[i].Line, e.Fields[i].Col = s.bump()
			s.node(e.Fields[i].Value)
		}
	case *ast.ListLit:
		e.Line, e.Col = s.bump()
		if e.TypeName != nil {
			s.typeExpr(e.TypeName)
		}
		for _, item := range e.Items {
			s.node(item)
		}
	case *ast.MapLit:
		e.Line, e.Col = s.bump()
		if e.TypeName != nil {
			s.typeExpr(e.TypeName)
		}
		for _, entry := range e.Entries {
			s.node(entry.Key)
			s.node(entry.Value)
		}
	case *ast.TupleLit:
		e.Line, e.Col = s.bump()
		for _, item := range e.Items {
			s.node(item)
		}
	case *ast.FieldAccess:
		e.Line, e.Col = s.bump()
		// The Field ident also needs a unique position because
		// checkFieldAccess keys c.fa.References by Field.Line/Col.
		e.Field.Line, e.Field.Col = s.bump()
		s.node(e.Object)
	case *ast.DotVariant:
		e.Line, e.Col = s.bump()
	case *ast.Ident:
		e.Line, e.Col = s.bump()
	case *ast.TypeIdent:
		e.Line, e.Col = s.bump()
	case *ast.SimpleType:
		s.typeExpr(e)
	case *ast.GenericType:
		s.typeExpr(e)
	case *ast.QualifiedType:
		s.typeExpr(e)
	case *ast.FuncType:
		s.typeExpr(e)
	case *ast.Case:
		e.Line, e.Col = s.bump()
		s.node(e.Value)
		for i := range e.Branches {
			br := &e.Branches[i]
			br.Line, br.Col = s.bump()
			s.node(br.Pattern)
			if br.Guard != nil {
				s.node(br.Guard)
			}
			s.node(br.Body)
		}
	case *ast.WildcardPattern:
		e.Line, e.Col = s.bump()
	case *ast.IdentPattern:
		e.Line, e.Col = s.bump()
	case *ast.EnumPattern:
		e.Line, e.Col = s.bump()
		if e.Binding != "" {
			// The binding is defined at (e.Line, BindingCol), so it needs a
			// column of its own: left at the synthesizer's column it keyed
			// the same Definitions slot as the impl function, and a
			// function-typed payload binding then read as the function's
			// signature. It must share the pattern's line.
			l, c := s.bump()
			if l != e.Line {
				e.Line, e.Col = l, c
				_, c = s.bump()
			}
			e.BindingCol = c
		}
		s.typeExpr(e.Variant)
		if e.Payload != nil {
			s.node(e.Payload)
		}
	case *ast.StructPattern:
		e.Line, e.Col = s.bump()
		if e.TypeName != nil {
			s.typeExpr(e.TypeName)
		}
		for i := range e.Fields {
			// StructPatternField has no Line, but NameLine/NameCol —
			// reuse the bump for the field name pos.
			f := &e.Fields[i]
			f.NameLine, f.NameCol = s.bump()
			if f.Pattern != nil {
				s.node(f.Pattern)
			}
		}
	case *ast.TuplePattern:
		e.Line, e.Col = s.bump()
		for _, p := range e.Patterns {
			s.node(p)
		}
	case *ast.ListPattern:
		e.Line, e.Col = s.bump()
		if e.TypeName != nil {
			s.typeExpr(e.TypeName)
		}
		for _, p := range e.Heads {
			s.node(p)
		}
		if e.TailSpread != nil {
			s.node(e.TailSpread)
		}
	case *ast.MapPattern:
		e.Line, e.Col = s.bump()
		if e.TypeName != nil {
			s.typeExpr(e.TypeName)
		}
		for _, entry := range e.Entries {
			s.node(entry.Key)
			s.node(entry.Pattern)
		}
	case *ast.DistinctDestructure:
		e.Line, e.Col = s.bump()
		if e.Binding != nil {
			e.Binding.Line, e.Binding.Col = s.bump()
		}
		s.node(e.Value)
	case *ast.IntLit:
		// Leaf node — Hashable synthesizer emits these for the mix
		// multiplier (31) and per-variant tag indices.
		e.Line, e.Col = s.bump()
	case *ast.StringLit:
		// Leaf node — Debug synthesizer emits these for static-string
		// bodies (empty struct, bare enum variant, zero-sized distinct).
		e.Line, e.Col = s.bump()
	case *ast.StringInterp:
		// Debug synthesizer emits these for `"TypeName{... ${dbg(...)}
		// ...}"` style bodies. Parts is a slice of StringText (value)
		// and StringExpr (value) — the StringExpr's inner Expr is a
		// regular Node and needs walking; StringText is a pure
		// position-less leaf.
		e.Line, e.Col = s.bump()
		for _, part := range e.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				s.node(se.Expr)
			}
		}
	default:
		// Synthesizer-bug guard: any new ast.Node kind emitted by a
		// derive synthesizer must add a case here. Falling through
		// would leave the node sharing the FuncDef's (line, col) and
		// alias unrelated symbols downstream. Panic loudly so the
		// drift surfaces during dev rather than as a confusing
		// analyzer error later.
		panic(fmt.Sprintf("uniquifyPositions: unhandled ast.Node type %T — add a case to keep synthesized positions disjoint", n))
	}
}

// identExpr returns `*ast.Ident{Name: name, ...}` — used wherever a Go
// helper needs to emit a value-position name reference.
func identExpr(name string, line, col int) *ast.Ident {
	return &ast.Ident{Name: name, Line: line, Col: col}
}

// exprStmt wraps an expression in *ast.ExprStmt — the parser does this for
// every value-position expression at block-statement scope, and the
// analyzer's body walker dispatches on ExprStmt to recurse into the
// expression. Synthesized bodies need the same wrapping or the body's
// expressions don't get type-checked (and the unwrapped Call nodes get
// misclassified as something else, e.g. lambda invocations).
func exprStmt(e ast.Node, line, col int) *ast.ExprStmt {
	return &ast.ExprStmt{Expr: e, Line: line, Col: col}
}

// fieldAccess returns `*ast.FieldAccess{Object: obj, Field: name, ...}`,
// matching the postfix-DOT shape the parser produces for `expr.field`.
// Used to build both `a.x` (struct fields) and `av.0` (tuple element by
// numeric index).
func fieldAccess(obj ast.Node, fieldName string, line, col int) *ast.FieldAccess {
	return &ast.FieldAccess{
		Object: obj,
		Field:  &ast.Ident{Name: fieldName, Line: line, Col: col},
		Line:   line,
		Col:    col,
	}
}

// equatableEqualsCall wraps two argument expressions in
// `Equatable.equal?(<a>, <b>)`. The callee is a FieldAccess on the
// `Equatable` type-ident — same shape the parser produces for any
// interface-qualified call.
func equatableEqualsCall(a, b ast.Node, line, col int) ast.Node {
	callee := &ast.FieldAccess{
		Object: &ast.TypeIdent{Name: "Equatable", Line: line, Col: col},
		Field:  &ast.Ident{Name: "equal?", Line: line, Col: col},
		Line:   line,
		Col:    col,
	}
	return &ast.Call{
		Func: callee,
		Args: []ast.Node{a, b},
		Line: line,
		Col:  col,
	}
}

// andChain folds a list of bool expressions into a left-associative AND
// chain: `[p, q, r]` → `(p and q) and r`. Empty list returns True; single
// element returns itself unchanged.
func andChain(parts []ast.Node, line, col int) ast.Node {
	if len(parts) == 0 {
		return trueLit(line, col)
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out = &ast.Binary{Left: out, Op: "and", Right: p, Line: line, Col: col}
	}
	return out
}

// trueLit returns the source-form `True` — Nomi has no dedicated bool
// literal node; True/False are bare enum-variant idents in expression
// position. *ast.TypeIdent matches what the parser produces for a
// PascalCase token used as a value.
func trueLit(line, col int) ast.Node {
	return &ast.TypeIdent{Name: "True", Line: line, Col: col}
}

// falseLit returns the source-form `False` (see trueLit).
func falseLit(line, col int) ast.Node {
	return &ast.TypeIdent{Name: "False", Line: line, Col: col}
}

// qualifiedTypeExpr returns `*ast.QualifiedType{Module: mod, Member:
// SimpleType{Name: member}}` — the shape the parser produces for
// `Mod.Member` in type-expression position (variant references in
// patterns, qualified type names, etc.).
func qualifiedTypeExpr(mod, member string, line, col int) ast.TypeExpr {
	return &ast.QualifiedType{
		Module:     mod,
		ModuleLine: line,
		ModuleCol:  col,
		Member:     &ast.SimpleType{Name: member, Line: line, Col: col},
	}
}

// typeDeclName returns the user-visible name of a type-decl node and
// `true` on the recognised kinds (struct / enum / typedef / host type).
// Caller treats `false` as "skip".
func typeDeclName(n ast.Node) (string, bool) {
	switch v := n.(type) {
	case *ast.StructDef:
		return v.Name, true
	case *ast.EnumDef:
		return v.Name, true
	case *ast.TypeDef:
		return v.Name, true
	case *ast.ExternType:
		return v.Name, true
	}
	return "", false
}

// typeDeclTypeParams returns the (possibly empty) TypeParams of a type-decl
// node. Distinct types (TypeDef) cannot carry generic parameters today
// (parser rejects `type Foo<T> ...`), so they always return nil. Returns
// nil for nodes that aren't a recognised type-decl kind.
func typeDeclTypeParams(n ast.Node) []ast.TypeParam {
	switch v := n.(type) {
	case *ast.StructDef:
		return v.TypeParams
	case *ast.EnumDef:
		return v.TypeParams
	case *ast.TypeDef:
		return nil
	case *ast.ExternType:
		return v.TypeParams
	}
	return nil
}

// receiverTypeExpr returns the param/return type expression naming the
// type being derived. For non-generic types this is `*ast.SimpleType{Name:
// typeName}`; for generic types `Foo<A, B>` the result is
// `*ast.GenericType{Name: "Foo", Params: [SimpleType{A}, SimpleType{B}]}`.
// The synthesized fn references this exact type-expr at every parameter /
// return position so the type checker resolves through the same generic-
// instantiation path user-written code uses.
func receiverTypeExpr(typeName string, typeParams []ast.TypeParam, line, col int) ast.TypeExpr {
	if len(typeParams) == 0 {
		return &ast.SimpleType{Name: typeName, Line: line, Col: col}
	}
	params := make([]ast.TypeExpr, len(typeParams))
	for i, tp := range typeParams {
		params[i] = &ast.SimpleType{Name: tp.Name, Line: line, Col: col}
	}
	return &ast.GenericType{Name: typeName, Params: params, Line: line, Col: col}
}

// CheckDeriveBounds walks the file's top-level type decls and, for every
// `@derive Iface` decorator, verifies that each component type the derived
// impl will recurse into (struct field, enum variant payload, distinct
// inner type) has an `Iface` impl visible from this file. Without this
// check the structural-eq / hash / compare / debug body emitted by the
// synthesizer compiles fine but blows up at runtime with `Iface.method:
// no implementation for type 'X'` pointing at a synth-band position —
// useless for diagnosing the user's actual mistake.
//
// The check is run AFTER `defineTopLevel` completes so b.file.Impls is
// fully populated. By that point, the (T, Iface) entries for the
// @derive'd types themselves are in b.file.Impls (registered by
// recordInterfaceImpl via the synthesizer's emitted impl block),
// which means recursive types
// (`struct List { head: Int, tail: List }`) and mutually-recursive types
// (`struct A { b: B }` / `struct B { a: A }` both `@derive Iface`) just
// look themselves up in the impl table and pass.
//
// Type-parameter references (`T` in `struct Box<T> { value: T }`) are
// recognized by name match against the source decl's TypeParams and
// skipped — Gap 2 ensures the synthesized fn carries an implicit
// `T: Iface` bound that the call-site's interface-bound enforcement
// catches.
//
// When fa.ProjectImpls is nil (typical for `buildTypesFromSource` /
// BuildFileWithStdlib unit tests that skip the project pipeline), the
// check is a no-op: we can't tell whether Int / String / Bool genuinely
// lack impls or are simply missing because no project-level index was
// built. Returning no errors in that case lets stdlib-less tests
// continue to exercise the @derive validation pass without false
// positives.
//
// In BuildProjectWithCache, ProjectImpls is broadcast onto every FA
// BEFORE this sweep runs (the broadcast moved to between Sweep B-impls
// and Sweep B-bounds when the StdlibImpls global retired), so the
// project path gets full project-wide impl knowledge here.
//
// Errors are appended to fa.TypeErrors at the @derive arg's position so
// the squiggle lands on the offending interface name rather than on the
// type decl. The message names the field/variant/payload so the user
// knows exactly which component type lacks the impl.
func CheckDeriveBounds(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	if fa == nil || fa.ProjectImpls == nil {
		// Single-file path (BuildFileWithStdlib / buildTypesFromSource):
		// no project-level impl index was built. The check would flag
		// every primitive-typed field as missing; skip rather than
		// surface false positives. See doc comment above.
		return nil
	}
	var errs []TypeError
	for _, n := range nodes {
		var typeName string
		var decorators []ast.Decorator
		var typeParams []ast.TypeParam
		switch d := n.(type) {
		case *ast.StructDef:
			typeName, decorators, typeParams = d.Name, d.Decorators, d.TypeParams
		case *ast.EnumDef:
			typeName, decorators, typeParams = d.Name, d.Decorators, d.TypeParams
		case *ast.TypeDef:
			typeName, decorators = d.Name, d.Decorators
		case *ast.ExternType:
			// `@derive` on an host type is vacuously satisfied: the
			// declaration has no components (fields / variant payloads /
			// inner type) for the synthesized body to recurse into — the
			// trivial constant bodies dispatch on nothing — so there are
			// no component-impl requirements to check and no payload
			// manifest demands to record.
			continue
		default:
			continue
		}
		if len(decorators) == 0 {
			continue
		}
		typeParamSet := make(map[string]bool, len(typeParams))
		for _, tp := range typeParams {
			typeParamSet[tp.Name] = true
		}
		for i := range decorators {
			dec := &decorators[i]
			if dec.Name != "derive" {
				continue
			}
			for _, arg := range dec.Args {
				st, ok := arg.(*ast.SimpleType)
				if !ok || !deriveSupported[st.Name] {
					continue
				}
				ifaceName := st.Name
				ifaceLine, ifaceCol := st.Line, st.Col
				derivePos := Pos{File: fa.FilePath, Line: ifaceLine, Col: ifaceCol}
				errs = append(errs, checkDeriveComponentTypes(fa, n, typeName, ifaceName, derivePos, typeParamSet)...)
				// Synth bodies for Display/Debug emit StringInterp literals like
				// "Some(${Iface.to_string(_v0)})". At runtime, evalStringInterp
				// re-dispatches Display.to_string on the inner String parts
				// (the to_string return values), so (String, Display) must be
				// in the manifest regardless of which payload types the
				// @derive covers. Equatable/Hashable/Comparable synth bodies
				// don't emit StringInterp, so this only fires for Display and
				// Debug.
				if ifaceName == "Display" || ifaceName == "Debug" {
					fa.RecordManifest("String", "Display", Recording{
						Pos:  derivePos,
						Kind: RecordingKindDerivePayload,
						Derive: &DeriveCtx{
							Iface:     ifaceName,
							TypeName:  typeName,
							DerivePos: derivePos,
						},
					})
				}
			}
		}
	}
	return errs
}

// checkDeriveComponentTypes walks one (typeNode, ifaceName) pair and
// reports each component type that lacks an Iface impl. Component types
// are: struct fields (StructDef.Fields), enum variant payloads
// (EnumDef.Variants — DataTypeExpr for positional, Fields for struct
// variants, EmbeddedTypeExpr for embedded), or the distinct inner
// (TypeDef.InnerTypeExpr).
//
// Each component report names the field/variant/payload so the diagnostic
// is actionable. Position of the *error* is the @derive arg's site
// (`derivePos`), not the field's, because that's where the user adds or
// removes the derive. Recordings, however, carry richer context via
// DeriveCtx: the @derive position, the offending field's name, and the
// field's source position. The component's own position drives the
// Recording.Pos so the missing-impl diagnostic can render "Holder.w on
// foo.nomi:7" instead of pointing at the bare `@derive` line.
//
// Generic-instantiation recursion: when a component's base type itself
// has an Iface impl AND carries type arguments (e.g. `w: Wrapper<C>`
// where Wrapper @derives Display), the synthesized Display body on
// Wrapper will recursively dispatch Display.to_string on its inner
// C-typed field at runtime. To pre-register that dispatch entry, each
// TypeArg gets its own Recording attributed to the *current* field
// (Holder.w), not Wrapper's synth body. Without this, a (Display, C)
// demand from Holder's perspective is unrecorded and the runtime
// fails with "Display.to_string: no implementation for type 'C'" at
// a synth-band position. The recursion uses the same DeriveCtx as the
// outer field — the user's actionable fix is on Holder's `w` field,
// not Wrapper's synthesizer.
func checkDeriveComponentTypes(fa *FileAnalysis, typeNode ast.Node, typeName, ifaceName string, derivePos Pos, typeParamSet map[string]bool) []TypeError {
	var errs []TypeError
	report := func(componentDesc, fieldName string, fieldPos Pos, te ast.TypeExpr) {
		if te == nil {
			return
		}
		baseName := TypeExprBaseName(te)
		if baseName == "" {
			// Function/anonymous types — synthesizer doesn't recurse into
			// these as `Iface.method` callees today; skipping matches what
			// the runtime would do and avoids a confusing "function type
			// has no impl" error.
			return
		}
		if typeParamSet[baseName] {
			// Type-param reference — Gap 2's implicit bound carries the
			// requirement to the call site.
			return
		}
		if isInterfaceTypeName(fa, baseName) {
			// Interface-typed field/payload (existential): the runtime
			// value at this slot is of some concrete type that implements
			// `baseName`, but we don't know which at the @derive site.
			// Iface dispatch on the value happens at runtime against the
			// stored type's impl — if that impl isn't there, the runtime
			// surfaces the failure with the actual type name. The static
			// check can't verify this case, so skip rather than emit a
			// blanket "interface 'X' does not implement Y" error that
			// the user can't act on without writing a manual impl.
			return
		}
		// Build the DeriveCtx once; both the outer-component recording and
		// any inner type-arg recursions share the same provenance shape:
		// the user's fix point is the field, not the synthesizer.
		ctx := &DeriveCtx{
			Iface:     ifaceName,
			TypeName:  typeName,
			DerivePos: derivePos,
			FieldName: fieldName,
			FieldPos:  fieldPos,
		}
		// Record (component, iface) unconditionally. When the impl
		// exists, this gives the manifest an entry for the impl that
		// the synthesized derive body will dispatch through. When the impl is MISSING, DetectMissingImpls
		// (run later by FinalizeCoherence) consumes the same manifest
		// entry to surface a "no impl of `Iface` for T" diagnostic with
		// the field's DeriveCtx provenance. Both consumers want the
		// recording; the legacy gate that skipped recording for the
		// missing case forced CheckDeriveBounds to emit its own terser
		// error and left FinalizeCoherence with nothing to diagnose.
		//
		// CheckDeriveBounds no longer emits its own field-impl error
		// for the missing case. The FinalizeCoherence path
		// (DetectMissingImpls) renders the richer "no impl of `Iface`
		// for T" diagnostic with full DeriveCtx provenance. Emitting
		// both produced two reports for one fix. CheckDeriveBounds's
		// outer ProjectImpls-nil guard already short-circuits the
		// whole pass for raw-test callers (BuildFile without an
		// AttachStdlibProjectImpls), so no graceful-degradation branch
		// is needed here — if we got past that guard, FinalizeCoherence
		// is the diagnostic owner.
		fa.RecordManifest(baseName, ifaceName, Recording{
			Pos:    fieldPos,
			Kind:   RecordingKindDeriveSynth,
			Derive: ctx,
		})
		if hasInterfaceImpl(fa, baseName, ifaceName) {
			// Generic-instantiation recursion: when the base type itself
			// implements `ifaceName` and is being instantiated with type
			// arguments, the base type's @derive'd body will dispatch
			// Iface.method on each TypeArg slot. Attribute those nested
			// recordings to the *current* field so the diagnostic points
			// at the user's `w: Wrapper<C>` site, not Wrapper's synth.
			for _, ta := range typeArgs(te) {
				recordDeriveTypeArg(fa, ifaceName, ta, ctx, typeParamSet)
			}
		}
	}
	switch t := typeNode.(type) {
	case *ast.StructDef:
		for _, f := range t.Fields {
			report(fmt.Sprintf("field '%s'", f.Name), f.Name, Pos{File: fa.FilePath, Line: f.Line, Col: f.Col}, f.TypeAnnotation)
		}
	case *ast.EnumDef:
		for _, v := range t.Variants {
			variantPos := Pos{File: fa.FilePath, Line: v.Line, Col: v.Col}
			switch v.Kind {
			case "bare":
				// No payload to verify.
			case "positional":
				// DataTypeExpr is either a single type or a FuncType (tuple)
				// for arity ≥ 2. Walk each arity slot independently so the
				// error message names the slot index.
				if ft, isTuple := v.DataTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
					for i, p := range ft.Params {
						report(fmt.Sprintf("variant '%s' payload[%d]", v.Name, i), fmt.Sprintf("%s[%d]", v.Name, i), variantPos, p)
					}
				} else if v.DataTypeExpr != nil {
					report(fmt.Sprintf("variant '%s' payload", v.Name), v.Name, variantPos, v.DataTypeExpr)
				}
			case "struct":
				for _, f := range v.Fields {
					report(fmt.Sprintf("variant '%s' field '%s'", v.Name, f.Name), v.Name+"."+f.Name, Pos{File: fa.FilePath, Line: f.Line, Col: f.Col}, f.TypeAnnotation)
				}
			case "embedded":
				report(fmt.Sprintf("variant '%s' embedded type", v.Name), v.Name, variantPos, v.EmbeddedTypeExpr)
			}
		}
	case *ast.TypeDef:
		if t.InnerTypeExpr == nil {
			// Zero-sized — body is the static-true / index-0 / "TypeName"
			// shape, no nested call to verify.
			return errs
		}
		innerPos := Pos{File: fa.FilePath, Line: t.Line, Col: t.Col}
		// Tuple inner: verify each element. Non-tuple inner: verify the
		// single inner type.
		if ft, isTuple := t.InnerTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 1 {
			for i, p := range ft.Params {
				report(fmt.Sprintf("inner tuple component[%d]", i), fmt.Sprintf("inner[%d]", i), innerPos, p)
			}
		} else {
			report("inner type", "inner", innerPos, t.InnerTypeExpr)
		}
	}
	return errs
}

// typeArgs returns the type-argument list of a TypeExpr — non-empty for
// GenericType, empty otherwise. Used by checkDeriveComponentTypes to
// recurse into nested generic instantiations the parent type's @derive
// will dispatch through.
func typeArgs(te ast.TypeExpr) []ast.TypeExpr {
	switch t := te.(type) {
	case *ast.GenericType:
		return t.Params
	case *ast.QualifiedType:
		return typeArgs(t.Member)
	}
	return nil
}

// recordDeriveTypeArg records (typeArg's base name, ifaceName) attributed
// to the same DeriveCtx as the parent field. Recurses into the type-arg's
// own TypeArgs when the base type itself has an impl — same rationale as
// the outer report closure's generic-instantiation recursion: the impl's
// synth body will dispatch through each TypeArg slot too.
func recordDeriveTypeArg(fa *FileAnalysis, ifaceName string, te ast.TypeExpr, ctx *DeriveCtx, typeParamSet map[string]bool) {
	if te == nil {
		return
	}
	baseName := TypeExprBaseName(te)
	if baseName == "" {
		return
	}
	if typeParamSet[baseName] {
		// Type-param at the inner position — the outer @derive's
		// implicit bound carries the requirement.
		return
	}
	if isInterfaceTypeName(fa, baseName) {
		// Existential at the inner position — runtime resolves.
		return
	}
	fa.RecordManifest(baseName, ifaceName, Recording{
		Pos:    ctx.FieldPos,
		Kind:   RecordingKindDeriveSynth,
		Derive: ctx,
	})
	// Recurse further: an impl on a generic type will dispatch through
	// each layer of its TypeArgs in turn.
	if hasInterfaceImpl(fa, baseName, ifaceName) {
		for _, inner := range typeArgs(te) {
			recordDeriveTypeArg(fa, ifaceName, inner, ctx, typeParamSet)
		}
	}
}

// hasInterfaceImpl reports whether the named type has an Iface impl
// visible from this file's analysis. Consults two sources in order:
//
//   - fa.Impls — the file's local impl table (already absorbs imports'
//     impls via mergeImpls and carries the @derive synthesizer's own emitted
//     entries from the earlier build pass.
//   - fa.ProjectImpls.Impls — the project-level impl index built by
//     buildProjectImplIndex (covers stdlib + sibling impls). In
//     BuildProjectWithCache, ProjectImpls is broadcast BEFORE
//     CheckDeriveBounds runs so this branch is the load-bearing
//     source for cross-file / stdlib impl knowledge here.
//
// Caller treats `false` as "no impl found, error" — boolean is sufficient
// because the per-component report names both the type and the missing
// interface.
func hasInterfaceImpl(fa *FileAnalysis, typeName, ifaceName string) bool {
	if fa != nil && fa.Impls != nil {
		if ifaces, ok := fa.Impls[typeName]; ok && ifaces[ifaceName] {
			return true
		}
	}
	if fa != nil && fa.ProjectImpls != nil {
		if ifaces, ok := fa.ProjectImpls.Impls[typeName]; ok && ifaces[ifaceName] {
			return true
		}
	}
	return false
}

// isInterfaceTypeName reports whether the named symbol resolves to an
// interface in the file's scope chain. Used by checkDeriveComponentTypes
// to skip interface-typed field slots — those are existentials whose
// runtime value carries a concrete impl that the static check can't
// verify (and shouldn't try to). Unrecognised names also return false,
// landing the caller in the "concrete type without impl" error path,
// which is correct: a name that resolves to nothing in scope already
// surfaces as an undefined-type error elsewhere.
func isInterfaceTypeName(fa *FileAnalysis, name string) bool {
	if fa == nil || fa.ModuleScope == nil {
		return false
	}
	sym := fa.ModuleScope.Lookup(name)
	if sym == nil {
		return false
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Kind == SymbolInterface
}

// boundedTypeParams mirrors the source type's TypeParams onto a synthesized
// FuncDef, attaching the named interface as a bound on each. Spec §38.5:
// `@derive Iface struct Box<T>` synthesizes `fn equal?(a: Box<T>, b: Box<T>):
// Bool` with the implicit bound `T: Iface`. Without the bound the type
// checker would accept calls with a payload type that has no Iface impl;
// the runtime would then surface "no impl found" with synth-band positions.
//
// User-written bounds on the source type (e.g. `where T: SomeOther`) are NOT
// propagated; only the @derive'd interface is added as a bound, matching
// what spec §38.5 documents. If the user wants tighter bounds they can
// hand-write the impl.
func boundedTypeParams(typeParams []ast.TypeParam, ifaceName string, line, col int) []ast.TypeParam {
	if len(typeParams) == 0 {
		return nil
	}
	out := make([]ast.TypeParam, len(typeParams))
	for i, tp := range typeParams {
		out[i] = ast.TypeParam{
			Name:   tp.Name,
			Bounds: []ast.TypeExpr{&ast.SimpleType{Name: ifaceName, Line: line, Col: col}},
			Line:   line,
			Col:    col,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Hashable synthesizer (Task 10)
// ---------------------------------------------------------------------------

// synthesizeDeriveHashable emits a single `impl Hashable for T { fn hash(value: T): Int }`
// block whose body is structurally appropriate for the type's shape:
//
//   - Struct: left-fold mix `Hashable.hash(value.f0) * 31 + Hashable.hash(value.f1) * 31 + ...`.
//     0-field struct: body is `0`. 1-field: bare `Hashable.hash(value.f)` (no mix).
//   - Enum: outer `case value` with one branch per variant; branch body mixes
//     the variant's declaration index (as IntLit) with the structural hash of
//     its payload. Bare variant: just the index. Variant index is per
//     spec §38.5 — declaration order determines hash buckets.
//   - Distinct type: zero-sized → 0; tuple inner → unwrap then mix component
//     hashes; primitive / generic / qualified → unwrap then delegate to
//     `Hashable.hash` on the inner value.
//
// Position info on every synthesized node points at the type decl's line/col
// during construction; uniquifyPositions then walks the result and stamps
// unique (line, col) per node so the analyzer's References / Definitions maps
// don't suffer from position aliasing. Mirrors the Equatable synthesizer's
// shape — see synthesizeDeriveEquatable.
func synthesizeDeriveHashable(typeNode ast.Node, site synthSite) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		return nil
	}
	line := site.base(synthKindHashable)
	col := 1

	body := hashableBody(typeNode, line, col)
	if body == nil {
		return nil
	}

	srcParams := typeDeclTypeParams(typeNode)
	fn := &ast.FuncDef{
		Name:       "hash",
		TypeParams: boundedTypeParams(srcParams, "Hashable", line, col),
		Params: []ast.Param{
			{Name: "value", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col},
		},
		ReturnTypeExpr: &ast.SimpleType{Name: "Int", Line: line, Col: col},
		Body:           &ast.Block{Stmts: body, Line: line, Col: col},
		Line:           line,
		Col:            col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

// hashableBody returns the statement list for the synthesized hash fn body,
// dispatched on the type decl's shape. Each value-position expression is
// wrapped in *ast.ExprStmt to match what the parser produces for top-level
// block statements.
func hashableBody(typeNode ast.Node, line, col int) []ast.Node {
	switch t := typeNode.(type) {
	case *ast.StructDef:
		return []ast.Node{exprStmt(hashableStructExpr(t.Fields, "value", line, col), line, col)}
	case *ast.EnumDef:
		return []ast.Node{exprStmt(hashableEnumExpr(t, line, col), line, col)}
	case *ast.TypeDef:
		return hashableDistinctStmts(t, line, col)
	case *ast.ExternType:
		// Derive-as-assertion (see equatableBody): all values of a
		// trivial-structure host type are equal, so they share one hash
		// bucket — the equals/hash agreement law holds by construction.
		return []ast.Node{exprStmt(intLit(0, line, col), line, col)}
	}
	return nil
}

// hashableStructExpr builds the left-fold mix of `Hashable.hash(<obj>.f)`
// for each field. obj names the receiver ("value" at top level, or a binding
// like a struct-pattern field name from an enum-variant case body). For
// 0-field structs the result is the bare `0` int literal; for 1-field, the
// bare `Hashable.hash(obj.f)` call (no mix).
func hashableStructExpr(fields []ast.StructField, obj string, line, col int) ast.Node {
	if len(fields) == 0 {
		return intLit(0, line, col)
	}
	parts := make([]ast.Node, len(fields))
	for i, f := range fields {
		parts[i] = hashableHashCall(
			fieldAccess(identExpr(obj, line, col), f.Name, line, col),
			line, col,
		)
	}
	return mixHashChain(parts, line, col)
}

// hashableEnumExpr builds the outer `case value { ... }` expression. Each
// variant's branch body is `<index> * 31 + <payload hash>`, or just `<index>`
// for bare variants. Variant declaration order determines the index — same
// as the spec §38.5 description for `@derive Hashable` on enums.
func hashableEnumExpr(e *ast.EnumDef, line, col int) ast.Node {
	branches := make([]ast.CaseBranch, 0, len(e.Variants))
	for i, v := range e.Variants {
		branches = append(branches, ast.CaseBranch{
			Pattern: enumVariantPattern(e.Name, v, "_v", line, col),
			Body:    hashableVariantBody(v, i, line, col),
			Line:    line,
			Col:     col,
		})
	}
	return &ast.Case{
		Value:    identExpr("value", line, col),
		Branches: branches,
		Line:     line,
		Col:      col,
	}
}

// hashableVariantBody builds the per-variant branch body:
//
//   - bare:                 IntLit(index)
//   - positional arity 1:   index * 31 + Hashable.hash(v0)
//   - positional arity N:   index * 31 + (Hashable.hash(v0) * 31 + Hashable.hash(v1) ...)
//   - struct, 0 fields:     IntLit(index)
//   - struct, N fields:     index * 31 + (Hashable.hash(<f>_v) ... mix)
//   - embedded:             index * 31 + Hashable.hash(v0)
//
// The variant pattern's binding suffix is "_v" (see enumVariantPattern call
// in hashableEnumExpr); `positionalBindName(i, "_v")` yields `v0`, `v1`, ...
// and struct patterns bind each field name suffixed with `_v`.
func hashableVariantBody(v ast.EnumVariant, index int, line, col int) ast.Node {
	idx := intLit(int64(index), line, col)
	switch v.Kind {
	case "bare":
		return idx
	case "positional":
		arity := 1
		if ft, isTuple := v.DataTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
			arity = len(ft.Params)
		}
		parts := make([]ast.Node, arity)
		for i := 0; i < arity; i++ {
			parts[i] = hashableHashCall(identExpr(positionalBindName(i, "_v"), line, col), line, col)
		}
		return mixHash(idx, mixHashChain(parts, line, col), line, col)
	case "struct":
		if len(v.Fields) == 0 {
			return idx
		}
		parts := make([]ast.Node, len(v.Fields))
		for i, f := range v.Fields {
			parts[i] = hashableHashCall(identExpr(f.Name+"_v", line, col), line, col)
		}
		return mixHash(idx, mixHashChain(parts, line, col), line, col)
	case "embedded":
		return mixHash(
			idx,
			hashableHashCall(identExpr(positionalBindName(0, "_v"), line, col), line, col),
			line, col,
		)
	}
	return idx
}

// hashableDistinctStmts handles `type Name InnerType` and zero-sized
// `type Name`. Zero-sized → body is the IntLit `0`. For tuple inner: unwrap
// then mix the component hashes via tuple-element field access on the
// unwrapped binding (`inner.0`, `inner.1`, ...). For non-tuple inner:
// unwrap then delegate to `Hashable.hash(inner)` on the unwrapped value.
func hashableDistinctStmts(t *ast.TypeDef, line, col int) []ast.Node {
	if t.InnerTypeExpr == nil {
		return []ast.Node{exprStmt(intLit(0, line, col), line, col)}
	}
	stmts := []ast.Node{
		&ast.DistinctDestructure{
			TypeName: t.Name,
			Binding:  &ast.Ident{Name: "inner", Line: line, Col: col},
			Value:    identExpr("value", line, col),
			Line:     line,
			Col:      col,
		},
	}
	if ft, isTuple := t.InnerTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 1 {
		parts := make([]ast.Node, len(ft.Params))
		for i := range ft.Params {
			parts[i] = hashableHashCall(
				fieldAccess(identExpr("inner", line, col), strconv.Itoa(i), line, col),
				line, col,
			)
		}
		stmts = append(stmts, exprStmt(mixHashChain(parts, line, col), line, col))
		return stmts
	}
	stmts = append(stmts, exprStmt(
		hashableHashCall(identExpr("inner", line, col), line, col),
		line, col,
	))
	return stmts
}

// hashableHashCall wraps an argument expression in `Hashable.hash(<arg>)`.
// Mirrors equatableEqualsCall; the callee is FieldAccess on the `Hashable`
// type-ident so the call routes through the same interface-qualified
// dispatch path as user-written `Hashable.hash(x)`.
func hashableHashCall(arg ast.Node, line, col int) ast.Node {
	callee := &ast.FieldAccess{
		Object: &ast.TypeIdent{Name: "Hashable", Line: line, Col: col},
		Field:  &ast.Ident{Name: "hash", Line: line, Col: col},
		Line:   line,
		Col:    col,
	}
	return &ast.Call{
		Func: callee,
		Args: []ast.Node{arg},
		Line: line,
		Col:  col,
	}
}

// mixHashChain folds a list of int-typed expressions into the canonical
// left-fold mix: `[h0, h1, h2]` → `h0 * 31 + h1 * 31 + h2` (parsed as
// `((h0 * 31) + h1) * 31 + h2` under standard precedence). Empty list
// returns `0`; single element returns itself unchanged.
//
// Note: this matches the FNV-style multiply-then-add convention used by
// Java's String#hashCode and most language-level structural hashes — same
// behaviour the spec §38.5 example assumes for `@derive Hashable`.
func mixHashChain(parts []ast.Node, line, col int) ast.Node {
	if len(parts) == 0 {
		return intLit(0, line, col)
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out = mixHash(out, p, line, col)
	}
	return out
}

// mixHash returns `<left> * 31 + <right>` as a Binary expression tree.
// `*` has higher precedence than `+` in Nomi, so the AST shape is
// `Binary(Op: "+", Left: Binary(Op: "*", Left: <left>, Right: 31),
// Right: <right>)`. 31 is the conventional small prime multiplier.
//
// Collision note: the `*31 + h` mix is fragile for small-int patterns
// the way Java's String#hashCode is — e.g. for primitive Int hashes
// (`Hashable.hash(Int)` returns the value itself), `Hashable.hash(
// Point{x: 0, y: 31}) == Hashable.hash(Point{x: 1, y: 0}) == 31`.
// Acceptable for v1's structural-hash contract (collisions reduce hash
// quality but don't violate the equals/hash agreement); upgrade to a
// proper FNV-1a / SipHash mix if a real workload demands it.
func mixHash(left, right ast.Node, line, col int) ast.Node {
	mul := &ast.Binary{
		Left:     left,
		Op:       "*",
		Right:    intLit(31, line, col),
		Wrapping: true, // hash mixing relies on modular arithmetic, not trapping
		Line:     line,
		Col:      col,
	}
	return &ast.Binary{
		Left:     mul,
		Op:       "+",
		Right:    right,
		Wrapping: true,
		Line:     line,
		Col:      col,
	}
}

// intLit returns `*ast.IntLit{Value: n, ...}` — Lexeme is empty since the
// node has no source representation. The formatter (which round-trips via
// Lexeme when set) would never see this — synthesized nodes don't reach
// the formatter — but uniquifyPositions and the analyzer do.
func intLit(n int64, line, col int) *ast.IntLit {
	return &ast.IntLit{Value: n, Line: line, Col: col}
}

// ---------------------------------------------------------------------------
// Comparable synthesizer (Task 11)
// ---------------------------------------------------------------------------

// synthesizeDeriveComparable emits a single `impl Comparable for T { fn compare(a: T,
// b: T): Ordering }` block whose body is structurally appropriate for the
// type's shape:
//
//   - Struct: lexicographic chain of `Comparable.compare(a.f, b.f)` over
//     fields in declaration order. Each link is a `case` on the prior
//     comparison's Ordering result — `Less -> Less`, `Greater -> Greater`,
//     `Equal -> <next-link>`. 0-field struct: body is bare `Equal`.
//   - Enum: outer `case a` with one branch per variant Vi; each branch is a
//     nested `case b` whose branches yield `Greater` for variants j < i,
//     compare payloads when j == i, and a wildcard `_ -> Less` arm catches
//     all variants j > i. Variant declaration order determines the total
//     order — same convention as Hashable's variant index.
//   - Distinct type: zero-sized → `Equal`; tuple inner → unwrap into `av` /
//     `bv` and lexicographic-compare on `.0`, `.1`, ...; primitive / generic
//     / qualified inner → unwrap and delegate to `Comparable.compare`.
//
// Position info on every synthesized node points at the type decl's line/col
// during construction; uniquifyPositions then walks the result and stamps
// unique (line, col) per node so the analyzer's References / Definitions maps
// don't suffer from position aliasing. Mirrors the Equatable + Hashable
// synthesizers' shape — see synthesizeDeriveEquatable.
func synthesizeDeriveComparable(typeNode ast.Node, site synthSite) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		return nil
	}
	line := site.base(synthKindComparable)
	col := 1

	body := comparableBody(typeNode, line, col)
	if body == nil {
		return nil
	}

	srcParams := typeDeclTypeParams(typeNode)
	fn := &ast.FuncDef{
		Name:       "compare",
		TypeParams: boundedTypeParams(srcParams, "Comparable", line, col),
		Params: []ast.Param{
			{Name: "a", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col},
			{Name: "b", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col},
		},
		ReturnTypeExpr: &ast.SimpleType{Name: "Ordering", Line: line, Col: col},
		Body:           &ast.Block{Stmts: body, Line: line, Col: col},
		Line:           line,
		Col:            col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

// comparableBody returns the statement list for the synthesized compare fn
// body, dispatched on the type decl's shape. Each value-position expression
// is wrapped in *ast.ExprStmt to match what the parser produces for top-level
// block statements — the body walker silently skips raw expressions.
func comparableBody(typeNode ast.Node, line, col int) []ast.Node {
	switch t := typeNode.(type) {
	case *ast.StructDef:
		return []ast.Node{exprStmt(comparableStructExpr(t.Fields, "a", "b", line, col), line, col)}
	case *ast.EnumDef:
		return []ast.Node{exprStmt(comparableEnumExpr(t, line, col), line, col)}
	case *ast.TypeDef:
		return comparableDistinctStmts(t, line, col)
	case *ast.ExternType:
		// Derive-as-assertion (see equatableBody): with one inhabitant,
		// every comparison is a self-comparison. Same constant body as a
		// zero-sized distinct.
		return []ast.Node{exprStmt(orderingIdent("Equal", line, col), line, col)}
	}
	return nil
}

// comparableStructExpr builds the lexicographic compare chain over the
// named struct's fields in declaration order. obj1 / obj2 name the two
// receivers ("a" / "b" at top level). For 0-field structs the body is the
// bare `Equal` ident (matches the spec's "two empty records are equal —
// hence neither less nor greater" rule).
func comparableStructExpr(fields []ast.StructField, obj1, obj2 string, line, col int) ast.Node {
	if len(fields) == 0 {
		return orderingIdent("Equal", line, col)
	}
	pairs := make([]exprPair, len(fields))
	for i, f := range fields {
		pairs[i] = exprPair{
			left:  fieldAccess(identExpr(obj1, line, col), f.Name, line, col),
			right: fieldAccess(identExpr(obj2, line, col), f.Name, line, col),
		}
	}
	return lexCompare(pairs, line, col)
}

// exprPair pairs a left/right expression for one position in a
// lexicographic-compare chain. lexCompare folds a slice of these into the
// nested `case Comparable.compare(p0.left, p0.right) { Less -> Less |
// Greater -> Greater | Equal -> <next> }` shape.
type exprPair struct {
	left, right ast.Node
}

// lexCompare builds the lexicographic compare chain for a list of (left,
// right) pairs:
//
//	[]                           → Equal
//	[(la, lb)]                   → Comparable.compare(la, lb)
//	[(la, lb), (la', lb'), ...]  → case Comparable.compare(la, lb) {
//	                                 Less -> Less
//	                                 Greater -> Greater
//	                                 Equal -> <recurse on rest>
//	                               }
//
// The Less/Greater arms short-circuit (a non-Equal earlier comparison wins
// outright); Equal recurses into the rest of the chain. Used by struct
// fields, multi-positional variant payloads, struct-payload variants, and
// tuple-distinct components.
func lexCompare(pairs []exprPair, line, col int) ast.Node {
	if len(pairs) == 0 {
		return orderingIdent("Equal", line, col)
	}
	if len(pairs) == 1 {
		return comparableCompareCall(pairs[0].left, pairs[0].right, line, col)
	}
	rest := lexCompare(pairs[1:], line, col)
	return &ast.Case{
		Value: comparableCompareCall(pairs[0].left, pairs[0].right, line, col),
		Branches: []ast.CaseBranch{
			{
				Pattern: orderingPattern("Less", line, col),
				Body:    orderingIdent("Less", line, col),
				Line:    line,
				Col:     col,
			},
			{
				Pattern: orderingPattern("Greater", line, col),
				Body:    orderingIdent("Greater", line, col),
				Line:    line,
				Col:     col,
			},
			{
				Pattern: orderingPattern("Equal", line, col),
				Body:    rest,
				Line:    line,
				Col:     col,
			},
		},
		Line: line,
		Col:  col,
	}
}

// comparableEnumExpr builds the outer `case a { ... }` expression. For each
// variant Vi (declaration order), the branch body is a nested `case b {
// V0 -> Greater | V1 -> Greater | ... | Vi -> <compare payloads> | _ -> Less
// }`, where Vj < i arms yield Greater and the wildcard arm yields Less for
// all Vj > i. Variant declaration order determines the total order.
//
// For the LAST variant (i == N-1) the wildcard arm is omitted because the
// preceding explicit arms already cover every variant — adding `_ -> Less`
// would be unreachable. The exhaustiveness check sees N explicit variant
// arms and accepts the case as complete.
func comparableEnumExpr(e *ast.EnumDef, line, col int) ast.Node {
	branches := make([]ast.CaseBranch, 0, len(e.Variants))
	for i, v := range e.Variants {
		branches = append(branches, ast.CaseBranch{
			Pattern: enumVariantPatternForA(e.Name, v, line, col),
			Body:    comparableNestedCaseOnB(e, i, v, line, col),
			Line:    line,
			Col:     col,
		})
	}
	return &ast.Case{
		Value:    identExpr("a", line, col),
		Branches: branches,
		Line:     line,
		Col:      col,
	}
}

// comparableNestedCaseOnB builds the inner `case b { ... }` for one
// outer-variant branch where the outer matched variant Vi at index `i`. For
// every Vj with j < i: an explicit `EnumName.Vj(...) -> Greater` arm. For
// j == i: an `EnumName.Vi(...) -> <compare payloads>` arm. For j > i: a
// single trailing wildcard `_ -> Less` arm.
//
// When `i` is the last variant index, no wildcard is emitted because the
// preceding explicit arms cover every variant and a wildcard would be
// unreachable.
func comparableNestedCaseOnB(e *ast.EnumDef, i int, vi ast.EnumVariant, line, col int) ast.Node {
	branches := make([]ast.CaseBranch, 0, len(e.Variants)+1)
	for j, vj := range e.Variants {
		switch {
		case j < i:
			// Bind with the b-side suffix so the binding name mirrors
			// what a same-variant branch would use; the body ignores
			// these bindings, but reusing the existing helper keeps
			// pattern construction uniform across variant kinds.
			branches = append(branches, ast.CaseBranch{
				Pattern: enumVariantPatternForB(e.Name, vj, line, col),
				Body:    orderingIdent("Greater", line, col),
				Line:    line,
				Col:     col,
			})
		case j == i:
			branches = append(branches, ast.CaseBranch{
				Pattern: enumVariantPatternForB(e.Name, vj, line, col),
				Body:    comparableVariantPayload(vi, line, col),
				Line:    line,
				Col:     col,
			})
		}
	}
	if i < len(e.Variants)-1 {
		branches = append(branches, ast.CaseBranch{
			Pattern: &ast.WildcardPattern{Line: line, Col: col},
			Body:    orderingIdent("Less", line, col),
			Line:    line,
			Col:     col,
		})
	}
	return &ast.Case{
		Value:    identExpr("b", line, col),
		Branches: branches,
		Line:     line,
		Col:      col,
	}
}

// comparableVariantPayload builds the same-variant arm body — the
// lexicographic compare of the matched payload bindings:
//
//   - bare:                 Equal (no payload to compare)
//   - positional arity 1:   Comparable.compare(a0, b0)
//   - positional arity N:   lex chain over (a0, b0), (a1, b1), ...
//   - struct, 0 fields:     Equal
//   - struct, N fields:     lex chain over (<f>_a, <f>_b) for each field
//   - embedded:             Comparable.compare(a0, b0) on the bound value
//
// Binding names match what enumVariantPattern uses with suffixes "_a" and
// "_b" — `positionalBindName(i, "_a")` yields `a0`, `a1`, ... and struct
// patterns bind each field name suffixed with `_a` / `_b`.
func comparableVariantPayload(v ast.EnumVariant, line, col int) ast.Node {
	switch v.Kind {
	case "bare":
		return orderingIdent("Equal", line, col)
	case "positional":
		arity := 1
		if ft, isTuple := v.DataTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
			arity = len(ft.Params)
		}
		pairs := make([]exprPair, arity)
		for i := 0; i < arity; i++ {
			pairs[i] = exprPair{
				left:  identExpr(positionalBindName(i, "_a"), line, col),
				right: identExpr(positionalBindName(i, "_b"), line, col),
			}
		}
		return lexCompare(pairs, line, col)
	case "struct":
		if len(v.Fields) == 0 {
			return orderingIdent("Equal", line, col)
		}
		pairs := make([]exprPair, len(v.Fields))
		for i, f := range v.Fields {
			pairs[i] = exprPair{
				left:  identExpr(f.Name+"_a", line, col),
				right: identExpr(f.Name+"_b", line, col),
			}
		}
		return lexCompare(pairs, line, col)
	case "embedded":
		return comparableCompareCall(
			identExpr(positionalBindName(0, "_a"), line, col),
			identExpr(positionalBindName(0, "_b"), line, col),
			line, col,
		)
	}
	return orderingIdent("Equal", line, col)
}

// comparableDistinctStmts handles `type Name InnerType` and zero-sized
// `type Name`. Zero-sized → body is the bare `Equal` ident. For tuple inner:
// unwrap into `av` / `bv`, then lex-compare on `.0`, `.1`, ... For non-tuple
// inner: unwrap then delegate to `Comparable.compare(av, bv)`.
func comparableDistinctStmts(t *ast.TypeDef, line, col int) []ast.Node {
	if t.InnerTypeExpr == nil {
		return []ast.Node{exprStmt(orderingIdent("Equal", line, col), line, col)}
	}
	stmts := []ast.Node{
		&ast.DistinctDestructure{
			TypeName: t.Name,
			Binding:  &ast.Ident{Name: "av", Line: line, Col: col},
			Value:    identExpr("a", line, col),
			Line:     line,
			Col:      col,
		},
		&ast.DistinctDestructure{
			TypeName: t.Name,
			Binding:  &ast.Ident{Name: "bv", Line: line, Col: col},
			Value:    identExpr("b", line, col),
			Line:     line,
			Col:      col,
		},
	}
	if ft, isTuple := t.InnerTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 1 {
		pairs := make([]exprPair, len(ft.Params))
		for i := range ft.Params {
			pairs[i] = exprPair{
				left:  fieldAccess(identExpr("av", line, col), strconv.Itoa(i), line, col),
				right: fieldAccess(identExpr("bv", line, col), strconv.Itoa(i), line, col),
			}
		}
		stmts = append(stmts, exprStmt(lexCompare(pairs, line, col), line, col))
		return stmts
	}
	stmts = append(stmts, exprStmt(comparableCompareCall(
		identExpr("av", line, col),
		identExpr("bv", line, col),
		line, col,
	), line, col))
	return stmts
}

// comparableCompareCall wraps two argument expressions in
// `Comparable.compare(<a>, <b>)`. Mirrors equatableEqualsCall; the callee
// is FieldAccess on the `Comparable` type-ident so the call routes through
// the same interface-qualified dispatch path as user-written calls.
func comparableCompareCall(a, b ast.Node, line, col int) ast.Node {
	callee := &ast.FieldAccess{
		Object: &ast.TypeIdent{Name: "Comparable", Line: line, Col: col},
		Field:  &ast.Ident{Name: "compare", Line: line, Col: col},
		Line:   line,
		Col:    col,
	}
	return &ast.Call{
		Func: callee,
		Args: []ast.Node{a, b},
		Line: line,
		Col:  col,
	}
}

// orderingIdent returns a value-position reference to one of the
// `Ordering` variants — `Less`, `Equal`, or `Greater` — written QUALIFIED
// (`Ordering.Less`), the FieldAccess-on-TypeIdent shape the parser emits
// for `Ordering.Less` in expression position. Qualifying through the
// `Ordering` type (which IS prelude-exported) — rather than emitting the
// bare variant and relying on it being in scope — is what lets the
// variants themselves stay OUT of the prelude: synthesized derive code
// resolves them via `Ordering`, not via a bare prelude binding. The
// pattern-position mirror is orderingPattern, qualified for the same
// reason (plus the bare-variant-in-pattern rule).
func orderingIdent(name string, line, col int) ast.Node {
	return fieldAccess(&ast.TypeIdent{Name: "Ordering", Line: line, Col: col}, name, line, col)
}

// orderingPattern returns the pattern matching one of the `Ordering`
// variants in pattern position — `Less`, `Equal`, `Greater`. The variant is
// written QUALIFIED (`Ordering.Less`), matching enumVariantPattern: the
// checker rejects bare variants in pattern position (checker.go's "bare
// variant '%s' in pattern position; use '.%s' (or '%s.%s')") since the
// leading-dot/qualified-variant rules landed, so the bare `SimpleType{Name}`
// shape the parser once produced for `case x { Less -> ... }` is no longer
// legal — and being synthesized code, an unqualified pattern here failed
// only at `nomi run` time, which is how it escaped notice. Ordering's variants are bare
// (no payload to bind).
func orderingPattern(name string, line, col int) ast.Node {
	return &ast.EnumPattern{
		Variant: qualifiedTypeExpr("Ordering", name, line, col),
		Line:    line,
		Col:     col,
	}
}

// ---------------------------------------------------------------------------
// Debug / Display synthesizers (Task 12 — Debug; Display follows same shape)
// ---------------------------------------------------------------------------

// synthesizeDeriveDebug emits a single `impl Debug for T { fn to_string(value: T):
// String { ... } }` block whose body produces a structural-shape stringify of
// the value, recursively delegating to `Debug.inspect` on every nested
// payload. See synthesizeDeriveStringify for output formats; Debug recurses
// through `Debug.inspect` on each nested value (strings render quoted).
func synthesizeDeriveDebug(typeNode ast.Node, site synthSite, kind synthKind) []ast.Node {
	return synthesizeDeriveStringify("Debug", typeNode, site, kind)
}

// synthesizeDeriveDisplay emits a single `impl Display for T { fn to_string(value: T):
// String { ... } }` block whose body produces a structural-shape stringify of
// the value, recursively delegating to `Display.to_string` on every nested
// payload. Same output shape as the Debug synthesizer; only the recursive
// dispatch changes (Display vs Debug — strings render unquoted under Display).
func synthesizeDeriveDisplay(typeNode ast.Node, site synthSite) []ast.Node {
	return synthesizeDeriveStringify("Display", typeNode, site, synthKindDisplay)
}

// synthesizeDeriveStringify is the shared body for both Display and Debug
// synthesizers. Output formats by shape (Display and Debug are byte-identical
// except for what the recursive `<iface>.to_string(...)` slot renders for
// each nested value):
//
//   - Struct: `TypeName{f1: <iface(value.f1)>, f2: ...}`. Empty struct →
//     bare static `"TypeName{}"`.
//   - Enum: `case value { ... }` with one branch per variant. Variant names
//     are emitted bare (not `EnumName.Variant`), matching how stringification
//     works in most languages (Java enum.toString, Rust Debug for enums).
//   - bare:                `"VariantName"`
//   - positional arity 1:  `"VariantName(<iface(payload)>)"`
//   - positional arity ≥2: `"VariantName(<iface(p0)>, <iface(p1)>, ...)"`
//     (no extra parens around the tuple — the runtime represents the
//     payload as a tuple, but the output mirrors the construction form).
//   - struct payload:      `"VariantName{f: <iface>, ...}"`
//   - embedded:            `"<iface(inner)>"` — bare delegation; an
//     embedded variant transparently behaves as the embedded type at
//     the value level, so its output is the embedded value's output
//     unwrapped.
//   - Distinct primitive:  `"TypeName(<iface(inner)>)"`
//   - Distinct tuple:      `"TypeName(<iface(inner.0)>, <iface(inner.1)>, ...)"`
//   - Distinct zero-sized: bare static `"TypeName"`
//
// The body uses string-interpolation AST (`*ast.StringInterp` /
// `*ast.StringLit`) so the produced text appears verbatim except for the
// `${<iface>.to_string(...)}` slots, which the runtime evaluates by calling
// the appropriate interface method on each nested value.
//
// Position info follows the same disjoint-band scheme as the other
// synthesizers — see synthesizeDeriveEquatable for the rationale.
func synthesizeDeriveStringify(iface string, typeNode ast.Node, site synthSite, kind synthKind) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		return nil
	}
	line := site.base(kind)
	col := 1

	body := stringifyBody(iface, typeNode, site.wrapping, line, col)
	if body == nil {
		return nil
	}

	srcParams := typeDeclTypeParams(typeNode)
	fn := &ast.FuncDef{
		Name:       stringifyMethod(iface),
		TypeParams: boundedTypeParams(srcParams, iface, line, col),
		Params: []ast.Param{
			{Name: "value", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col},
		},
		ReturnTypeExpr: &ast.SimpleType{Name: "String", Line: line, Col: col},
		Body:           &ast.Block{Stmts: body, Line: line, Col: col},
		Line:           line,
		Col:            col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

// stringifyBody returns the body statements for the synthesized to_string fn,
// dispatched on the type-decl shape. Like the other synthesizers, every
// value-position expression at block-statement scope is wrapped in
// *ast.ExprStmt so the analyzer's body walker descends into it.
func stringifyBody(iface string, typeNode ast.Node, wrapping map[string]bool, line, col int) []ast.Node {
	switch t := typeNode.(type) {
	case *ast.StructDef:
		return []ast.Node{exprStmt(stringifyStructExpr(iface, t.Name, t.Fields, line, col), line, col)}
	case *ast.EnumDef:
		return []ast.Node{exprStmt(stringifyEnumExpr(iface, t, wrapping, line, col), line, col)}
	case *ast.TypeDef:
		return stringifyDistinctStmts(iface, t, line, col)
	case *ast.ExternType:
		// Derive-as-assertion (see equatableBody): the declaration carries
		// no payload shape, so the rendering is the bare type name — the
		// same shape a zero-sized distinct stringifies to, and the same
		// body the universal-Debug extern default emits
		// (synthesizeExternNameOnlyDebug).
		return []ast.Node{exprStmt(staticString(t.Name, line, col), line, col)}
	}
	return nil
}

// stringifyStructExpr builds the struct body's interpolation expression. Each
// field renders as `<name>: ${<iface>.to_string(value.<name>)}`. Empty struct
// → static `"TypeName{}"` literal.
func stringifyStructExpr(iface, typeName string, fields []ast.StructField, line, col int) ast.Node {
	if len(fields) == 0 {
		return staticString(typeName+"{}", line, col)
	}
	exprs := make([]fieldDbg, len(fields))
	for i, f := range fields {
		exprs[i] = fieldDbg{
			name: f.Name,
			expr: fieldAccess(identExpr("value", line, col), f.Name, line, col),
		}
	}
	return stringifyStructLikeBody(iface, typeName, exprs, line, col)
}

// stringifyEnumExpr builds the outer `case value { ... }` for an enum. Each
// variant produces one branch whose pattern matches the variant (binding
// payload components) and whose body is the variant's interpolation.
func stringifyEnumExpr(iface string, e *ast.EnumDef, wrapping map[string]bool, line, col int) ast.Node {
	branches := make([]ast.CaseBranch, 0, len(e.Variants))
	for _, v := range e.Variants {
		branches = append(branches, ast.CaseBranch{
			Pattern: enumVariantPattern(e.Name, v, "_v", line, col),
			Body:    stringifyVariantBody(iface, v, wrapping, line, col),
			Line:    line,
			Col:     col,
		})
	}
	return &ast.Case{
		Value:    identExpr("value", line, col),
		Branches: branches,
		Line:     line,
		Col:      col,
	}
}

// stringifyVariantBody builds the per-variant body using the bound payload
// names that enumVariantPattern (with suffix "_v") produced:
//
//   - positional bindings: v0, v1, ... (via positionalBindName)
//   - struct bindings:     <field>_v
//
// Variant name in the rendered string is BARE (not enum-qualified) — that
// matches what Debug-style stringification looks like in most languages
// and isn't intended to be re-parseable.
func stringifyVariantBody(iface string, v ast.EnumVariant, wrapping map[string]bool, line, col int) ast.Node {
	switch v.Kind {
	case "bare":
		return staticString(v.Name, line, col)
	case "positional":
		arity := 1
		if ft, isTuple := v.DataTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
			arity = len(ft.Params)
		}
		exprs := make([]ast.Node, arity)
		for i := 0; i < arity; i++ {
			exprs[i] = identExpr(positionalBindName(i, "_v"), line, col)
		}
		return stringifyTupleLikeBody(iface, v.Name, exprs, line, col)
	case "struct":
		exprs := make([]fieldDbg, len(v.Fields))
		for i, f := range v.Fields {
			exprs[i] = fieldDbg{
				name: f.Name,
				expr: identExpr(f.Name+"_v", line, col),
			}
		}
		return stringifyStructLikeBody(iface, v.Name, exprs, line, col)
	case "embedded":
		// Embedded variants behave as the embedded type at the value
		// level — output is the embedded value's output unwrapped, no
		// `VariantName(...)` wrapping. The pattern binds a wrapping
		// distinct's INNER value (spec §7), so that value is rebuilt into
		// the distinct first: `Shape.Id(5)` renders `Id(5)`, as the `Id`
		// does on its own. Only a same-file distinct is known to wrap here.
		bound := ast.Node(identExpr(positionalBindName(0, "_v"), line, col))
		if st, ok := v.EmbeddedTypeExpr.(*ast.SimpleType); ok && wrapping[st.Name] {
			bound = &ast.Call{
				Func: &ast.TypeIdent{Name: st.Name, Line: line, Col: col},
				Args: []ast.Node{bound},
				Line: line,
				Col:  col,
			}
		}
		return stringifyToStringCall(iface, bound, line, col)
	}
	return staticString(v.Name, line, col)
}

// stringifyDistinctStmts handles `type Name InnerType` and the zero-sized
// `type Name`. Zero-sized → static `"TypeName"`. For tuple inner: unwrap
// into `inner` then render `TypeName(<iface inner.0>, <iface inner.1>, ...)`.
// For non-tuple inner: unwrap then render `TypeName(<iface inner>)`.
func stringifyDistinctStmts(iface string, t *ast.TypeDef, line, col int) []ast.Node {
	if t.InnerTypeExpr == nil {
		return []ast.Node{exprStmt(staticString(t.Name, line, col), line, col)}
	}
	stmts := []ast.Node{
		&ast.DistinctDestructure{
			TypeName: t.Name,
			Binding:  &ast.Ident{Name: "inner", Line: line, Col: col},
			Value:    identExpr("value", line, col),
			Line:     line,
			Col:      col,
		},
	}
	if ft, isTuple := t.InnerTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 1 {
		exprs := make([]ast.Node, len(ft.Params))
		for i := range ft.Params {
			exprs[i] = fieldAccess(identExpr("inner", line, col), strconv.Itoa(i), line, col)
		}
		stmts = append(stmts, exprStmt(stringifyTupleLikeBody(iface, t.Name, exprs, line, col), line, col))
		return stmts
	}
	// Single inner value → `TypeName(<iface inner>)`.
	stmts = append(stmts, exprStmt(stringifyTupleLikeBody(iface, t.Name, []ast.Node{identExpr("inner", line, col)}, line, col), line, col))
	return stmts
}

// fieldDbg pairs a rendered field name with the expression that produces
// its value. Used for both struct decls (expr is `value.<name>`) and
// struct-payload variants (expr is `<name>_v`, the bound pattern name).
type fieldDbg struct {
	name string
	expr ast.Node
}

// stringifyStructLikeBody emits the interpolation for a record-shaped value:
//
//	"TypeName{f1: ${<iface>.to_string(<expr1>)}, f2: ${<iface>.to_string(<expr2>)}, ...}"
//
// 0 fields collapses to the static `"TypeName{}"` (no interpolation slots).
// Reused for top-level structs and enum struct-payload variants.
func stringifyStructLikeBody(iface, typeName string, fields []fieldDbg, line, col int) ast.Node {
	if len(fields) == 0 {
		return staticString(typeName+"{}", line, col)
	}
	parts := make([]ast.StringPart, 0, 2*len(fields)+2)
	// Leading "TypeName{f1: "
	parts = append(parts, ast.StringText{Value: typeName + "{" + fields[0].name + ": "})
	parts = append(parts, ast.StringExpr{Expr: stringifyToStringCall(iface, fields[0].expr, line, col)})
	for i := 1; i < len(fields); i++ {
		parts = append(parts, ast.StringText{Value: ", " + fields[i].name + ": "})
		parts = append(parts, ast.StringExpr{Expr: stringifyToStringCall(iface, fields[i].expr, line, col)})
	}
	parts = append(parts, ast.StringText{Value: "}"})
	return &ast.StringInterp{Parts: parts, Line: line, Col: col}
}

// stringifyTupleLikeBody emits the interpolation for a positional-shaped value:
//
//	"TypeName(${<iface>.to_string(<e0>)}, ${<iface>.to_string(<e1>)}, ...)"
//
// 0 exprs collapses to the static `"TypeName"` (used by zero-sized distinct
// and bare variants — the latter takes the staticString path directly, but
// keeping the empty case here means callers don't need to special-case).
// Reused for distinct primitive (1 expr), distinct tuple (N exprs), and
// multi-positional enum variants (N exprs).
func stringifyTupleLikeBody(iface, typeName string, exprs []ast.Node, line, col int) ast.Node {
	if len(exprs) == 0 {
		return staticString(typeName, line, col)
	}
	parts := make([]ast.StringPart, 0, 2*len(exprs)+2)
	parts = append(parts, ast.StringText{Value: typeName + "("})
	parts = append(parts, ast.StringExpr{Expr: stringifyToStringCall(iface, exprs[0], line, col)})
	for i := 1; i < len(exprs); i++ {
		parts = append(parts, ast.StringText{Value: ", "})
		parts = append(parts, ast.StringExpr{Expr: stringifyToStringCall(iface, exprs[i], line, col)})
	}
	parts = append(parts, ast.StringText{Value: ")"})
	return &ast.StringInterp{Parts: parts, Line: line, Col: col}
}

// stringifyToStringCall wraps an argument expression in
// `<iface>.to_string(<arg>)` — the recursive delegation that drives the
// format's "render every nested value with the chosen interface" rule.
// Mirrors equatableEqualsCall / hashableHashCall / comparableCompareCall.
// stringifyMethod returns the interface's stringify method name: Display →
// "to_string", Debug → "inspect". They deliberately differ so the two most
// prolific interfaces don't collide on one method name (see std/debug.nomi);
// the synthesizer therefore emits the right name per interface for both the
// FuncDef and the recursive `${<iface>.<method>(...)}` delegation slot.
func stringifyMethod(iface string) string {
	if iface == "Debug" {
		return "inspect"
	}
	return "to_string"
}

func stringifyToStringCall(iface string, arg ast.Node, line, col int) ast.Node {
	callee := &ast.FieldAccess{
		Object: &ast.TypeIdent{Name: iface, Line: line, Col: col},
		Field:  &ast.Ident{Name: stringifyMethod(iface), Line: line, Col: col},
		Line:   line,
		Col:    col,
	}
	return &ast.Call{
		Func: callee,
		Args: []ast.Node{arg},
		Line: line,
		Col:  col,
	}
}

// staticString returns a `*ast.StringLit{Value: s}` — the parser's shape
// for a string literal with no interpolation slots. Used for zero-field /
// zero-payload cases where the rendered text is purely static.
func staticString(s string, line, col int) ast.Node {
	return &ast.StringLit{Value: s, Line: line, Col: col}
}

// ---------------------------------------------------------------------------
// JSON derive synthesizers
// ---------------------------------------------------------------------------

func synthesizeDeriveToJson(typeNode ast.Node, opts deriveOptions, site synthSite) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		return nil
	}
	line := site.base(synthKindToJson)
	col := 1
	body := toJsonBody(typeNode, opts, line, col)
	if body == nil {
		return nil
	}
	srcParams := typeDeclTypeParams(typeNode)
	fn := &ast.FuncDef{
		Name:       "to_json",
		TypeParams: boundedTypeParams(srcParams, "ToJson", line, col),
		Params: []ast.Param{
			{Name: "value", TypeAnnotation: receiverTypeExpr(typeName, srcParams, line, col), Line: line, Col: col},
		},
		ReturnTypeExpr: &ast.SimpleType{Name: "Json", Line: line, Col: col},
		Body:           &ast.Block{Stmts: body, Line: line, Col: col},
		Line:           line,
		Col:            col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

func toJsonBody(typeNode ast.Node, opts deriveOptions, line, col int) []ast.Node {
	switch t := typeNode.(type) {
	case *ast.StructDef:
		return []ast.Node{exprStmt(toJsonStructExpr(t.Fields, opts, line, col), line, col)}
	case *ast.EnumDef:
		return []ast.Node{exprStmt(toJsonEnumExpr(t, opts, line, col), line, col)}
	case *ast.TypeDef:
		return toJsonDistinctStmts(t, line, col)
	case *ast.ExternType:
		return []ast.Node{exprStmt(jsonString(t.Name, line, col), line, col)}
	}
	return nil
}

func toJsonStructExpr(fields []ast.StructField, opts deriveOptions, line, col int) ast.Node {
	entries := make([]ast.MapEntry, 0, len(fields))
	for _, f := range fields {
		key := jsonFieldName(f.Name, opts)
		entries = append(entries, ast.MapEntry{
			Key:   staticString(key, line, col),
			Value: toJsonCall(fieldAccess(identExpr("value", line, col), f.Name, line, col), line, col),
		})
	}
	return jsonObj(entries, line, col)
}

func toJsonEnumExpr(e *ast.EnumDef, opts deriveOptions, line, col int) ast.Node {
	branches := make([]ast.CaseBranch, 0, len(e.Variants))
	for _, v := range e.Variants {
		branches = append(branches, ast.CaseBranch{
			Pattern: enumVariantPattern(e.Name, v, "_v", line, col),
			Body:    toJsonVariantExpr(v, opts, line, col),
			Line:    line,
			Col:     col,
		})
	}
	return &ast.Case{Value: identExpr("value", line, col), Branches: branches, Line: line, Col: col}
}

func toJsonVariantExpr(v ast.EnumVariant, opts deriveOptions, line, col int) ast.Node {
	switch v.Kind {
	case "bare":
		return jsonString(v.Name, line, col)
	case "positional":
		arity := 1
		if ft, isTuple := v.DataTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
			arity = len(ft.Params)
		}
		var payload ast.Node
		if arity == 1 {
			payload = toJsonCall(identExpr(positionalBindName(0, "_v"), line, col), line, col)
		} else {
			items := make([]ast.Node, arity)
			for i := 0; i < arity; i++ {
				items[i] = toJsonCall(identExpr(positionalBindName(i, "_v"), line, col), line, col)
			}
			payload = jsonArr(items, line, col)
		}
		return jsonObj([]ast.MapEntry{{Key: staticString(v.Name, line, col), Value: payload}}, line, col)
	case "struct":
		entries := make([]ast.MapEntry, 0, len(v.Fields))
		for _, f := range v.Fields {
			entries = append(entries, ast.MapEntry{
				Key:   staticString(jsonFieldName(f.Name, opts), line, col),
				Value: toJsonCall(identExpr(f.Name+"_v", line, col), line, col),
			})
		}
		return jsonObj([]ast.MapEntry{{Key: staticString(v.Name, line, col), Value: jsonObj(entries, line, col)}}, line, col)
	case "embedded":
		return jsonObj([]ast.MapEntry{{
			Key:   staticString(v.Name, line, col),
			Value: toJsonCall(identExpr(positionalBindName(0, "_v"), line, col), line, col),
		}}, line, col)
	}
	return jsonNull(line, col)
}

func toJsonDistinctStmts(t *ast.TypeDef, line, col int) []ast.Node {
	if t.InnerTypeExpr == nil {
		return []ast.Node{exprStmt(jsonString(t.Name, line, col), line, col)}
	}
	stmts := []ast.Node{
		&ast.DistinctDestructure{
			TypeName: t.Name,
			Binding:  &ast.Ident{Name: "inner", Line: line, Col: col},
			Value:    identExpr("value", line, col),
			Line:     line,
			Col:      col,
		},
	}
	if ft, isTuple := t.InnerTypeExpr.(*ast.FuncType); isTuple && ft.Return == nil && len(ft.Params) >= 2 {
		items := make([]ast.Node, len(ft.Params))
		for i := range ft.Params {
			items[i] = toJsonCall(fieldAccess(identExpr("inner", line, col), strconv.Itoa(i), line, col), line, col)
		}
		stmts = append(stmts, exprStmt(jsonArr(items, line, col), line, col))
		return stmts
	}
	stmts = append(stmts, exprStmt(toJsonCall(identExpr("inner", line, col), line, col), line, col))
	return stmts
}

func synthesizeDeriveFromJson(typeNode ast.Node, opts deriveOptions, site synthSite) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		return nil
	}
	line := site.base(synthKindFromJson)
	col := 1
	body := fromJsonBody(typeNode, opts, line, col)
	if body == nil {
		return nil
	}
	srcParams := typeDeclTypeParams(typeNode)
	fn := &ast.FuncDef{
		Name:       "from_json",
		TypeParams: boundedTypeParams(srcParams, "FromJson", line, col),
		Params: []ast.Param{
			{Name: "json", TypeAnnotation: &ast.SimpleType{Name: "Json", Line: line, Col: col}, Line: line, Col: col},
		},
		ReturnTypeExpr: &ast.GenericType{
			Name: "Result",
			Params: []ast.TypeExpr{
				receiverTypeExpr(typeName, srcParams, line, col),
				&ast.SimpleType{Name: "Json.ShapeError", Line: line, Col: col},
			},
			Line: line,
			Col:  col,
		},
		Body: &ast.Block{Stmts: body, Line: line, Col: col},
		Line: line,
		Col:  col,
	}
	uniquifyPositions(fn, line)
	return []ast.Node{fn}
}

func fromJsonBody(typeNode ast.Node, opts deriveOptions, line, col int) []ast.Node {
	switch t := typeNode.(type) {
	case *ast.StructDef:
		return []ast.Node{exprStmt(fromJsonStructExpr(t, opts, line, col), line, col)}
	case *ast.TypeDef:
		return fromJsonDistinctStmts(t, line, col)
	case *ast.ExternType:
		return []ast.Node{exprStmt(okCall(&ast.TypeIdent{Name: t.Name, Line: line, Col: col}, line, col), line, col)}
	case *ast.EnumDef:
		return nil
	}
	return nil
}

func fromJsonStructExpr(t *ast.StructDef, opts deriveOptions, line, col int) ast.Node {
	objPattern := &ast.EnumPattern{
		Variant: qualifiedTypeExpr("Json", "Obj", line, col),
		Payload: &ast.IdentPattern{Name: "fields", Line: line, Col: col},
		Line:    line,
		Col:     col,
	}
	return &ast.Case{
		Value: identExpr("json", line, col),
		Branches: []ast.CaseBranch{
			{
				Pattern: objPattern,
				Body:    fromJsonStructFieldsExpr(t, opts, 0, nil, line, col),
				Line:    line,
				Col:     col,
			},
			{
				Pattern: &ast.WildcardPattern{Line: line, Col: col},
				Body:    jsonShapeErr("object", identExpr("json", line, col), line, col),
				Line:    line,
				Col:     col,
			},
		},
		Line: line,
		Col:  col,
	}
}

func fromJsonStructFieldsExpr(t *ast.StructDef, opts deriveOptions, idx int, decoded []ast.StructFieldVal, line, col int) ast.Node {
	if idx >= len(t.Fields) {
		return okCall(&ast.StructLit{
			TypeName: &ast.SimpleType{Name: t.Name, Line: line, Col: col},
			Fields:   decoded,
			Line:     line,
			Col:      col,
		}, line, col)
	}
	field := t.Fields[idx]
	key := jsonFieldName(field.Name, opts)
	jvName := "__json_" + field.Name
	valueName := field.Name + "_value"
	nextDecoded := append(append([]ast.StructFieldVal{}, decoded...), ast.StructFieldVal{
		Name:  field.Name,
		Value: identExpr(valueName, line, col),
		Line:  line,
		Col:   col,
	})
	rest := fromJsonStructFieldsExpr(t, opts, idx+1, nextDecoded, line, col)
	decodePresent := &ast.Case{
		Value: fromJsonCallForType(field.TypeAnnotation, identExpr(jvName, line, col), line, col),
		Branches: []ast.CaseBranch{
			{
				Pattern: enumBindPattern("Ok", valueName, line, col),
				Body:    rest,
				Line:    line,
				Col:     col,
			},
			{
				Pattern: enumBindPattern("Err", "__err", line, col),
				Body:    errCall(jsonShapePrepend(identExpr("__err", line, col), key, line, col), line, col),
				Line:    line,
				Col:     col,
			},
		},
		Line: line,
		Col:  col,
	}
	var missing ast.Node
	switch {
	case isMaybeType(field.TypeAnnotation):
		missingDecoded := append(append([]ast.StructFieldVal{}, decoded...), ast.StructFieldVal{
			Name:  field.Name,
			Value: &ast.TypeIdent{Name: "None", Line: line, Col: col},
			Line:  line,
			Col:   col,
		})
		missing = fromJsonStructFieldsExpr(t, opts, idx+1, missingDecoded, line, col)
	case field.Default != nil:
		defaultDecoded := append(append([]ast.StructFieldVal{}, decoded...), ast.StructFieldVal{
			Name:  field.Name,
			Value: field.Default,
			Line:  line,
			Col:   col,
		})
		missing = fromJsonStructFieldsExpr(t, opts, idx+1, defaultDecoded, line, col)
	default:
		missing = errCall(&ast.StructLit{
			TypeName: &ast.SimpleType{Name: "Json.ShapeError", Line: line, Col: col},
			Fields: []ast.StructFieldVal{
				{Name: "path", Value: &ast.ListLit{Items: []ast.Node{staticString(key, line, col)}, Line: line, Col: col}, Line: line, Col: col},
				{Name: "expected", Value: staticString(field.TypeAnnotation.TypeString(), line, col), Line: line, Col: col},
				{Name: "got", Value: staticString("missing", line, col), Line: line, Col: col},
			},
			Line: line,
			Col:  col,
		}, line, col)
	}
	return &ast.Case{
		Value: mapGetCall(identExpr("fields", line, col), staticString(key, line, col), line, col),
		Branches: []ast.CaseBranch{
			{
				Pattern: enumBindPattern("Some", jvName, line, col),
				Body:    decodePresent,
				Line:    line,
				Col:     col,
			},
			{
				Pattern: &ast.EnumPattern{Variant: &ast.SimpleType{Name: "None", Line: line, Col: col}, Line: line, Col: col},
				Body:    missing,
				Line:    line,
				Col:     col,
			},
		},
		Line: line,
		Col:  col,
	}
}

func fromJsonDistinctStmts(t *ast.TypeDef, line, col int) []ast.Node {
	if t.InnerTypeExpr == nil {
		return []ast.Node{exprStmt(okCall(&ast.TypeIdent{Name: t.Name, Line: line, Col: col}, line, col), line, col)}
	}
	innerName := "inner"
	body := &ast.Case{
		Value: fromJsonCallForType(t.InnerTypeExpr, identExpr("json", line, col), line, col),
		Branches: []ast.CaseBranch{
			{
				Pattern: enumBindPattern("Ok", innerName, line, col),
				Body: okCall(&ast.Call{
					Func: &ast.TypeIdent{Name: t.Name, Line: line, Col: col},
					Args: []ast.Node{identExpr(innerName, line, col)},
					Line: line,
					Col:  col,
				}, line, col),
				Line: line,
				Col:  col,
			},
			{
				Pattern: enumBindPattern("Err", "__err", line, col),
				Body:    errCall(identExpr("__err", line, col), line, col),
				Line:    line,
				Col:     col,
			},
		},
		Line: line,
		Col:  col,
	}
	return []ast.Node{exprStmt(body, line, col)}
}

func validateDeriveTarget(ifaceName string, typeNode ast.Node, line, col int) []TypeError {
	if ifaceName != "FromJson" {
		return nil
	}
	if e, ok := typeNode.(*ast.EnumDef); ok {
		return []TypeError{{
			Line:    line,
			Col:     col,
			Message: fmt.Sprintf("derive FromJson for enum '%s' is not supported", e.Name),
		}}
	}
	return nil
}

func jsonFieldName(name string, opts deriveOptions) string {
	parts := strings.Split(name, "_")
	switch opts.RenameAll {
	case "Camel":
		return lowerFirst(joinTitle(parts))
	case "Pascal":
		return joinTitle(parts)
	case "Kebab":
		return strings.Join(parts, "-")
	case "ScreamingSnake":
		return strings.ToUpper(name)
	default:
		return name
	}
}

func joinTitle(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func isMaybeType(te ast.TypeExpr) bool {
	return TypeExprBaseName(te) == "Maybe"
}

func toJsonCall(arg ast.Node, line, col int) ast.Node {
	return qualifiedCall("ToJson", "to_json", []ast.Node{arg}, line, col)
}

func fromJsonCallForType(target ast.TypeExpr, arg ast.Node, line, col int) ast.Node {
	return &ast.Call{
		Func: fieldAccess(cloneTypeExprNode(target, line, col), "from_json", line, col),
		Args: []ast.Node{arg},
		Line: line,
		Col:  col,
	}
}

func mapGetCall(m, key ast.Node, line, col int) ast.Node {
	return qualifiedCall("Map", "get", []ast.Node{m, key}, line, col)
}

func jsonShapePrepend(err ast.Node, segment string, line, col int) ast.Node {
	return qualifiedCall("json", "shape_error_prepend", []ast.Node{err, staticString(segment, line, col)}, line, col)
}

func jsonShapeErr(expected string, got ast.Node, line, col int) ast.Node {
	return errCall(qualifiedCall("json", "shape_error_root", []ast.Node{staticString(expected, line, col), got}, line, col), line, col)
}

func qualifiedCall(owner, method string, args []ast.Node, line, col int) ast.Node {
	return &ast.Call{
		Func: fieldAccess(&ast.TypeIdent{Name: owner, Line: line, Col: col}, method, line, col),
		Args: args,
		Line: line,
		Col:  col,
	}
}

func okCall(value ast.Node, line, col int) ast.Node {
	return &ast.Call{Func: &ast.TypeIdent{Name: "Ok", Line: line, Col: col}, Args: []ast.Node{value}, Line: line, Col: col}
}

func errCall(value ast.Node, line, col int) ast.Node {
	return &ast.Call{Func: &ast.TypeIdent{Name: "Err", Line: line, Col: col}, Args: []ast.Node{value}, Line: line, Col: col}
}

func jsonString(value string, line, col int) ast.Node {
	return &ast.Call{
		Func: fieldAccess(&ast.TypeIdent{Name: "Json", Line: line, Col: col}, "String", line, col),
		Args: []ast.Node{staticString(value, line, col)},
		Line: line,
		Col:  col,
	}
}

func jsonObj(entries []ast.MapEntry, line, col int) ast.Node {
	return &ast.MapLit{TypeName: qualifiedTypeExpr("Json", "Obj", line, col), Entries: entries, Line: line, Col: col}
}

func jsonArr(items []ast.Node, line, col int) ast.Node {
	return &ast.ListLit{TypeName: qualifiedTypeExpr("Json", "Arr", line, col), Items: items, Line: line, Col: col}
}

func jsonNull(line, col int) ast.Node {
	return fieldAccess(&ast.TypeIdent{Name: "Json", Line: line, Col: col}, "Null", line, col)
}

func enumBindPattern(variant, bind string, line, col int) ast.Node {
	return &ast.EnumPattern{
		Variant: &ast.SimpleType{Name: variant, Line: line, Col: col},
		Payload: &ast.IdentPattern{Name: bind, Line: line, Col: col},
		Line:    line,
		Col:     col,
	}
}

func cloneTypeExprNode(te ast.TypeExpr, line, col int) ast.Node {
	if te == nil {
		return &ast.TypeIdent{Name: "", Line: line, Col: col}
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		return &ast.TypeIdent{Name: t.Name, Line: line, Col: col}
	case *ast.GenericType:
		params := make([]ast.TypeExpr, len(t.Params))
		for i, p := range t.Params {
			if cloned, ok := cloneTypeExpr(p, line, col); ok {
				params[i] = cloned
			}
		}
		return &ast.GenericType{Name: t.Name, Params: params, Line: line, Col: col}
	case *ast.QualifiedType:
		member, _ := cloneTypeExpr(t.Member, line, col)
		return &ast.QualifiedType{Module: t.Module, ModuleLine: line, ModuleCol: col, Member: member}
	default:
		if cloned, ok := cloneTypeExpr(te, line, col); ok {
			return cloned
		}
		return &ast.TypeIdent{Name: te.TypeString(), Line: line, Col: col}
	}
}

func cloneTypeExpr(te ast.TypeExpr, line, col int) (ast.TypeExpr, bool) {
	switch t := te.(type) {
	case *ast.SimpleType:
		return &ast.SimpleType{Name: t.Name, Line: line, Col: col}, true
	case *ast.GenericType:
		params := make([]ast.TypeExpr, len(t.Params))
		for i, p := range t.Params {
			cloned, ok := cloneTypeExpr(p, line, col)
			if !ok {
				return nil, false
			}
			params[i] = cloned
		}
		return &ast.GenericType{Name: t.Name, Params: params, Line: line, Col: col}, true
	case *ast.QualifiedType:
		member, ok := cloneTypeExpr(t.Member, line, col)
		if !ok {
			return nil, false
		}
		return &ast.QualifiedType{Module: t.Module, ModuleLine: line, ModuleCol: col, Member: member}, true
	case *ast.FuncType:
		params := make([]ast.TypeExpr, len(t.Params))
		for i, p := range t.Params {
			cloned, ok := cloneTypeExpr(p, line, col)
			if !ok {
				return nil, false
			}
			params[i] = cloned
		}
		var ret ast.TypeExpr
		if t.Return != nil {
			cloned, ok := cloneTypeExpr(t.Return, line, col)
			if !ok {
				return nil, false
			}
			ret = cloned
		}
		return &ast.FuncType{Params: params, Return: ret, Line: line, Col: col}, true
	}
	return nil, false
}

// deriveArgName extracts the PascalCase interface name from a decorator arg.
// The derive lowering produces *ast.SimpleType args, the only shape valid for
// a `derive` decorator. Returns ("", line, col) when the arg shape is wrong
// (caller records the error). Falls back to the decorator's own line/col when the
// arg has no positional info.
func deriveArgName(arg ast.Node, dec *ast.Decorator) (string, int, int) {
	switch a := arg.(type) {
	case *ast.SimpleType:
		return a.Name, a.Line, a.Col
	case *ast.Ident:
		// snake_case arg — wrong shape for @derive, but report at its own
		// position so the squiggle lands on the offending token.
		return "", a.Line, a.Col
	}
	return "", dec.Line, dec.Col
}

// deriveSupportedDescription joins the canonical supported-protocol names
// with commas for use in error messages. Computed once per call (cheap) so
// the canonical list stays the single source of truth.
func deriveSupportedDescription() string {
	switch len(deriveSupportedList) {
	case 0:
		return ""
	case 1:
		return deriveSupportedList[0]
	}
	out := deriveSupportedList[0]
	for _, name := range deriveSupportedList[1:] {
		out += ", " + name
	}
	return out
}
