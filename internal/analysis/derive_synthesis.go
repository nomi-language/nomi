package analysis

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// derivableInterfaces is the set of interfaces `derive` writes, each by its
// identity: the std module that declares it and its name, in the order of
// spec §12's *Derivable interfaces* table. A derive names one of them by any
// spelling that resolves to it (`ToJson`, `json.ToJson`); an interface of
// the same name that another file declares is not one of them
// (checkDerivedInterface). deriveSupported and deriveProtocolHomeModule are
// read from it.
var derivableInterfaces = []struct{ Name, Module string }{
	{"Equatable", "std/equatable"},
	{"Hashable", "std/hashable"},
	{"Comparable", "std/comparable"},
	{"Debug", "std/debug"},
	{"Display", "std/display"},
	{"ToJson", "std/json"},
	{"FromJson", "std/json"},
	{"Variants", "std/variants"},
}

// deriveSupported holds the names of derivableInterfaces. Lowering reads it
// to decide what it can synthesize, before names resolve.
var deriveSupported = func() map[string]bool {
	m := make(map[string]bool, len(derivableInterfaces))
	for _, d := range derivableInterfaces {
		m[d.Name] = true
	}
	return m
}()

// deriveProtocolHomeModule maps each name of derivableInterfaces to the std
// module that declares it: the identity a derive's interface must resolve
// to, and the import the scope check's message suggests ("`Comparable` is
// not in scope — import `std::comparable.Comparable`").
var deriveProtocolHomeModule = func() map[string]string {
	m := make(map[string]string, len(derivableInterfaces))
	for _, d := range derivableInterfaces {
		m[d.Name] = d.Module
	}
	return m
}()

// synthSupportNames is the closed set of names synthesized derive /
// universal-Debug code may reference beyond the receiver type's own
// fields and variants: the five protocol interfaces (recursion callees
// like `Equatable.equal(...)`), the `Ordering` enum (Comparable
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
// `Json` and `Json.ShapeError` are absent too: derived code names them
// through the hidden `json$` import (jsonType).
var synthSupportNames = map[string]bool{
	"Equatable":  true,
	"Comparable": true,
	"Hashable":   true,
	"Display":    true,
	"Debug":      true,
	"ToJson":     true,
	"FromJson":   true,
	"Variants":   true,
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

type deriveOptions struct {
	RenameAll string
}

func defaultDeriveOptions() deriveOptions {
	return deriveOptions{RenameAll: "Snake"}
}

// validateDeriveOptions checks a derive's `with` options against iface, the
// interface as the derive writes it: the options are `<iface>.Options{...}`,
// spelled as the interface is (`json.ToJson.Options{...}` for `derive
// json.ToJson`).
func validateDeriveOptions(iface ast.TypeExpr, options ast.Node, line, col int) (deriveOptions, []TypeError) {
	opts := defaultDeriveOptions()
	if options == nil {
		return opts, nil
	}
	ifaceName, written := TypeExprBaseName(iface), iface.TypeString()
	if ifaceName != "ToJson" && ifaceName != "FromJson" {
		return opts, []TypeError{{Line: line, Col: col, Message: fmt.Sprintf("`derive %s` does not accept options", written)}}
	}
	lit, ok := options.(*ast.StructLit)
	if !ok || lit.TypeName == nil {
		return opts, []TypeError{{Line: line, Col: col, Message: fmt.Sprintf("`derive %s` options must be `%s.Options{...}`", written, written)}}
	}
	want := written + ".Options"
	if lit.TypeName.TypeString() != want {
		return opts, []TypeError{{Line: line, Col: col, Message: fmt.Sprintf("`derive %s` options must use `%s`, got `%s`", written, want, lit.TypeName.TypeString())}}
	}
	return deriveOptionFields(lit, want)
}

// deriveOptionFields reads the fields of lit, a JSON derive's options
// literal of the type spelled want.
func deriveOptionFields(lit *ast.StructLit, want string) (deriveOptions, []TypeError) {
	opts := defaultDeriveOptions()
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

// parseDeriveOptionsForSynthesis is the options a derive of the interface
// named ifaceName synthesizes with, read from options, which lowering has
// validated (validateDeriveOptions).
func parseDeriveOptionsForSynthesis(ifaceName string, options ast.Node) deriveOptions {
	lit, ok := options.(*ast.StructLit)
	if !ok || (ifaceName != "ToJson" && ifaceName != "FromJson") {
		return defaultDeriveOptions()
	}
	opts, _ := deriveOptionFields(lit, "")
	return opts
}

func jsonCaseOptionName(n ast.Node) (string, bool) {
	parts := valuePathParts(n)
	switch len(parts) {
	case 1:
		return jsonCaseVariantName(parts[0])
	case 3, 4:
		// `Json.Case.Camel`, or `json.Json.Case.Camel` through the module.
		if k := len(parts); parts[k-3] == "Json" && parts[k-2] == "Case" {
			return jsonCaseVariantName(parts[k-1])
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
	// synthOriginBlock is the name-only Debug of an opaque type declared in
	// a body (synthesizeBlockDebug), indexed by the declaration's place among the
	// file's block-local types in the order its bodies are walked. It has
	// the one slot the three regions leave over (synthSlotCount is not a
	// multiple of synthOriginEnd), synthBlockLines lines per declaration.
	synthOriginBlock = synthOriginEnd
)

// synthBlockLines is how many lines of the leftover slot one block-local
// type's Debug takes: one line holds 16383 nodes. synthLineStride /
// synthBlockLines declarations fit, and the modulo in synthSlot keeps a
// file with more inside the band.
const synthBlockLines = 16

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
	synthKindVariants
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
	if origin == synthOriginBlock {
		last := synthLineBase + (synthSlotCount-1)*synthLineStride
		return last + (index%(synthLineStride/synthBlockLines))*synthBlockLines
	}
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

// IsPreludeImport reports whether s is one of the prelude imports a build
// prepended to a user file, rather than an import its source writes or an
// expansion's hidden import (whose alias holds a `$`).
func IsPreludeImport(s *ast.ImportStmt) bool {
	return isPreludeInjectLine(s.Line) && s.ModuleAlias == nil
}

// IsSynthesizedLine reports whether a (1-based) source line falls in the
// synthesized-position band that derive synthesis allocates from. Callers
// that walk fa.Definitions or References across an entire file may want to
// skip these so synthesized symbols (e.g., the inner-destructure binding
// in a synth Debug body) don't surface as user-visible defs.
func IsSynthesizedLine(line int) bool {
	return line >= synthLineBase
}

// sourceIndices answers each node's index among the nodes the file's source
// wrote. A prelude import a project build prepends to a user file takes no
// index, so a declaration's synthesized positions are the same whether its
// file was synthesized before the build prepended them (the front end's and
// the language server's entry) or after (every other file of the build).
func sourceIndices(nodes []ast.Node) []int {
	out := make([]int, len(nodes))
	next := 0
	for i, n := range nodes {
		out[i] = next
		if imp, ok := n.(*ast.ImportStmt); ok && isPreludeInjectLine(imp.Line) {
			continue
		}
		next++
	}
	return out
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

// synthesizeDeriveFor dispatches to the per-protocol synthesizer for a
// validated (supported) interface name. Each synthesizer emits a full
// structural body, but those of deriveByShape, whose blocks write no
// function (derivedShapeBlock).
func synthesizeDeriveFor(ifaceName string, typeNode ast.Node, options ast.Node, site synthSite) []ast.Node {
	typeNode = withoutRepeatedNames(typeNode)
	if s, byShape := deriveByShape[ifaceName]; byShape {
		return derivedShapeBlock(ifaceName, s.kind, s.bounded, typeNode, site)
	}
	var fns []ast.Node
	opts := parseDeriveOptionsForSynthesis(ifaceName, options)
	switch ifaceName {
	case "Comparable":
		fns = synthesizeDeriveComparable(typeNode, site)
	case "ToJson":
		fns = synthesizeDeriveToJson(typeNode, opts, site)
	case "FromJson":
		fns = synthesizeDeriveFromJson(typeNode, opts, site)
	case "Variants":
		fns = synthesizeDeriveVariants(typeNode, site)
	default:
		// deriveSupported gate above guarantees this is unreachable; kept as
		// a belt-and-braces guard so a future addition to deriveSupported
		// without a matching case here surfaces immediately rather than
		// silently emitting nothing.
		return nil
	}
	return wrapSynthAsImplBlocks(fns, ifaceName, site)
}

// deriveByShape are the derivable interfaces whose derive writes an impl
// block with no function (derivedShapeBlock), answered by the type's shape:
// Equatable compares it, Hashable hashes it, Debug and Display render it.
// bounded says the derive implies the interface as a bound on the type's
// parameters (spec §12 *Generic types*), which Debug, always met, does
// not.
var deriveByShape = map[string]struct {
	kind    synthKind
	bounded bool
}{
	"Equatable": {synthKindEquatable, true},
	"Hashable":  {synthKindHashable, true},
	"Debug":     {synthKindDebug, false},
	"Display":   {synthKindDisplay, true},
}

// wrapSynthAsImplBlocks wraps each synthesized method FuncDef into
// the equivalent block-form `*ast.ImplBlock`, so synthesis emits the SAME shape
// hand-written code uses — `@derive` and universal Debug produce `impl Iface for
// T { fn }` blocks. The interface comes from the caller-supplied
// ifaceName, the receiver from the FuncDef's first param type except for
// receiver-less constructor interfaces such as FromJson, whose receiver is
// recovered from `Result<Receiver, Json.ShapeError>`, and Variants, whose
// `variants()` takes nothing and returns `List<Receiver>`. Generics are COPIED onto the block
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
		generics := fn.TypeParams
		if len(fn.Params) == 0 || ifaceName == "FromJson" {
			// A function with no self-position parameter reads `self` off
			// the header, so its `T` must be the header's: one of its own
			// would be a second, distinct `T`, and `Result<Pair<T>, ...>`
			// would match no `self` the header applies. The header alone
			// declares them, as in a hand-written `impl FromJson for
			// Pair<T> where T: FromJson`.
			fn.TypeParams = nil
		}
		out = append(out, &ast.ImplBlock{
			// Header interface from the caller's protocol name; receiver from the
			// method's first param type. The interface node reuses the fn's
			// (already-uniquified) synth-band position — aliasing within the
			// synth band is filtered by IsSynthesizedLine, so it never surfaces.
			Interface: &ast.SimpleType{Name: ifaceName, Line: fn.Line, Col: fn.Col},
			Receiver:  receiver,
			Generics:  generics,
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
	if ifaceName == "Variants" {
		if ret, ok := fn.ReturnTypeExpr.(*ast.GenericType); ok && ret.Name == "List" && len(ret.Params) == 1 {
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

// SynthesizeUniversalDebug is the universal-Debug pass. Every type
// implements Debug (spec §12 *Display and Debug*). A struct, enum or
// distinct type with no written or derived Debug has no block of it: a use
// of Debug's function at it is answered by its shape (noImplUse), and the
// machine renders it as the literal that builds it (rt.DebugText). Two
// kinds of type render other than by their shape, and this pass appends
// the `impl Debug` block that says how for each that lacks an explicit
// Debug (a hand-written `impl Debug for T` or a `derive Debug`), after
// LowerDerives has run: an opaque type, which shows its name only
// (`<opaque T>`), and a `host type`, which shows its bare name.
//
// Precedence is enforced by the "skip if any explicit Debug exists" guard:
// explicit `impl Debug` block > explicit `@derive Debug` > this pass's. That
// guard is also what keeps the coherence checks happy — the pass never adds a
// second impl for an existing (Debug, T) pair, so detectImplCollisions
// stays quiet.
//
// Run per-file, after LowerDerives in BuildProject:
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
	var synthesized []ast.Node
	index := sourceIndices(nodes)
	for i, n := range nodes {
		// typeDeclName recognises struct/enum/typedef AND host types:
		// declared host types are nominal types too — values of them
		// reach Debug dispatch (e.g. a zero-sized extern singleton bound
		// from an `embeds` variant pattern), so they get the name-only
		// default, and so do the Go handles the VM holds as rt.HostHandle.
		typeName, ok := typeDeclName(n)
		if !ok {
			// Not a type decl (FuncDef, import, etc.).
			continue
		}
		if _, host := n.(*ast.ExternType); !host && !typeDeclIsOpaque(n) {
			// Its Debug is its shape.
			continue
		}
		if explicit[typeName] || isDistinctSubtypeDecl(n) {
			// A distinct subtype has its base's Debug: its value is
			// the base's at run time (subtype.go).
			continue
		}
		// One impl per name. A second declaration of the same name is a
		// redeclaration the builder reports at its own line; a second
		// synthesized impl would add a duplicate-impl error at a
		// synthesized line that says nothing about the real cause.
		explicit[typeName] = true
		synthesized = append(synthesized, synthesizeAutoDebug(n, synthSiteOf(index[i], n))...)
	}
	// Each subtype's `try_from` and `unchecked` (subtype_synthesis.go),
	// in this pass because it runs wherever a file's declarations are
	// completed with what the front end writes for them.
	synthesized = append(synthesized, synthesizeSubtypes(nodes, index)...)
	if len(synthesized) == 0 {
		return nodes
	}
	out := make([]ast.Node, 0, len(nodes)+len(synthesized))
	out = append(out, nodes...)
	out = append(out, synthesized...)
	return out
}

// synthesizeBlockDebug is the universal Debug of decl, an opaque struct,
// enum or distinct type declared in a body, which no impl can name: the
// name-only body SynthesizeUniversalDebug writes for a module-level opaque
// type. index is decl's place among the block-local types of its file,
// which gives its positions (synthOriginBlock). The builder resolves it in
// decl's block and the checker checks it there (FileBodies.BlockDebug).
// It answers nil for every other type declared in a body, whose Debug is
// its shape (noImplUse), which rt renders as the literal that builds it,
// and for a distinct subtype, whose Debug is its base's.
func synthesizeBlockDebug(decl ast.Node, index int) *ast.ImplBlock {
	if _, ok := typeDeclName(decl); !ok || !typeDeclIsOpaque(decl) || isDistinctSubtypeDecl(decl) {
		return nil
	}
	site := synthSite{origin: synthOriginBlock, index: index}
	site.line, site.col = declPos(decl)
	for _, n := range synthesizeAutoDebug(decl, site) {
		if ib, ok := n.(*ast.ImplBlock); ok {
			return ib
		}
	}
	return nil
}

// blockDebugImpls is the universal Debug of each type a body among nodes
// declares (FileBodies.BlockDebug), in source order: derived bodies that
// the checker's passes over a file's calls and VerifyElaboration cover as
// they cover a module-level type's.
func blockDebugImpls(fa *FileAnalysis, nodes []ast.Node) []ast.Node {
	var out []ast.Node
	if len(fa.BlockDebug) == 0 {
		return nil
	}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			if impl := fa.BlockDebug[n]; impl != nil {
				out = append(out, impl)
			}
			return true
		})
	}
	return out
}

// builderSynthesizeUniversalDebug is the per-builder shim for the
// buildModule call site (`nodes = b.SynthesizeUniversalDebug(nodes)`). The pass itself
// produces no errors (it only emits well-formed structural / name-only
// impls), so there is nothing to fold into b.file.TypeErrors.
func (b *builder) SynthesizeUniversalDebug(nodes []ast.Node) []ast.Node {
	return SynthesizeUniversalDebug(nodes)
}

// typesWithExplicitDebug returns the set of type names that already carry an
// explicit Debug — either a hand-written `impl Debug for T { fn inspect(value: T) }`
// block, or the `@derive Debug` decorator derive lowering stamps on the
// type decl beside the impl block it writes (also captured here via the
// impl-block scan). Both forms suppress auto-synthesis for that type. The
// pass runs before the file has a scope, so a header names Debug through
// the file's import aliases (importedAs): `impl Dbg for T` after `import
// std::debug.{Debug as Dbg}` is an explicit Debug.
func typesWithExplicitDebug(nodes []ast.Node) map[string]bool {
	out := map[string]bool{}
	aliases := importedAs(nodes)
	for _, n := range nodes {
		// Explicit block-form `impl Debug for T { ... }` (hand-written OR
		// derive-/auto-synthesized — all synthesis emits blocks). A Debug block
		// suppresses auto-synthesis for its receiver type.
		if blk, ok := n.(*ast.ImplBlock); ok {
			iface := TypeExprBaseName(blk.Interface)
			if imported, ok := aliases[iface]; ok {
				iface = imported
			}
			if blk.Interface != nil && iface == "Debug" {
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
				if deriveArgName(arg) == "Debug" {
					out[typeName] = true
				}
			}
		}
	}
	return out
}

// synthesizeAutoDebug produces the universal Debug `impl` block of a type
// whose Debug is not its shape, one of two name-only bodies:
//
//   - OPAQUE: for an opaque struct/enum/distinct type the body is
//     `"<opaque TypeName>"`, since its shape would show exactly the
//     internals `opaque` exists to hide (spec §4, *Opaque distinct types*).
//   - EXTERN: for a declared `host type` the body is the bare type
//     name `"TypeName"` — the declaration carries no payload shape the
//     synthesizer can see (internals are host-side), which is the same shape
//     as a zero-sized distinct, and those render as their bare name. The bare
//     name (no `<...>` marker) is also what makes `embeds`-variant delegation
//     come out right: an enum embedding a zero-sized extern singleton
//     (`embeds True`) renders the variant as `True`, exactly like a
//     zero-sized distinct embed.
//
// An explicit `derive Debug` on an opaque type is a deliberate structural
// override (`NonZeroInt(5)`): its block writes no function, and Debug
// renders the type by its shape (derivedShapeBlock).
func synthesizeAutoDebug(typeNode ast.Node, site synthSite) []ast.Node {
	typeNode = withoutRepeatedNames(typeNode)
	var out []ast.Node
	if et, ok := typeNode.(*ast.ExternType); ok {
		out = synthesizeExternNameOnlyDebug(et, site)
	} else {
		out = synthesizeOpaqueNameOnlyDebug(typeNode, site)
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
// an opaque type: `impl Debug for T { fn inspect(value: T) -> String { "<opaque T>" } }`.
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
// `host type`: `impl Debug for T { fn inspect(value: T) -> String { "T" } }`.
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

// enumVariantPatternForA builds the outer-case pattern that matches the
// variant in `a` and binds its payload components to a-side names
// (`a0`, `a1`, ...) for positional / embedded variants, or `a_<field>` for
// struct variants. Bare variants produce a bare EnumPattern with no
// payload bindings.
func enumVariantPatternForA(enumName string, v ast.EnumVariant, line, col int) ast.Node {
	return enumVariantPattern(enumName, v, "_a", line, col)
}

// enumVariantPatternForB is the b-side mirror: bind names with a `_b`
// suffix so the inner case branch body can reference both sides without
// collision (`a0` / `b0`, or `a_<field>` / `b_<field>`).
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
//     we just pass the whole thing through `Equatable.equal`)
func enumVariantPattern(enumName string, v ast.EnumVariant, suffix string, line, col int) ast.Node {
	variantTypeExpr := qualifiedTypeExpr(enumName, v.Name, line, col)
	switch v.Kind {
	case "bare":
		return &ast.EnumPattern{Variant: variantTypeExpr, Line: line, Col: col}
	case "positional":
		// A payload written as one type binds one ident; one written as
		// parts (writtenParts) a tuple pattern with one ident per part:
		// `Variant((a0, a1, ...))`.
		if ws := writtenParts(v.DataTypeExpr); ws != nil {
			patterns := make([]ast.Node, len(ws))
			for i := range ws {
				name := positionalBindName(i, suffix)
				patterns[i] = &ast.IdentPattern{Name: name, Line: line, Col: col}
			}
			return &ast.EnumPattern{
				Variant: variantTypeExpr,
				Payload: &ast.TuplePattern{Patterns: patterns, Line: line, Col: col},
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
				Binding: fieldBindName(f.Name, suffix),
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
		// recurses into `Equatable.equal` on the bound value.
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

// fieldBindName returns the binding a struct-variant pattern gives field
// `field`: the side letter, an underscore, then the field name (`a_w`,
// `b_w`, `v_w`). The letter comes first so a field spelled with a leading
// underscore (`_w`) still binds a readable name; `_w_a` would be a discard.
func fieldBindName(field, suffix string) string {
	return suffix[1:] + "_" + field
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
// Callers (synthesizeDeriveComparable) call nextSynthBase() to claim a
// disjoint band per FuncDef.
//
// Contract for synthesizers (Comparable, the JSON derives, Variants,
// the name-only Debug, and any future one): every synthesizer must
// extend the uniquifyState.node walker when it introduces a new
// ast.Node kind into synthesized output (Block, StringLit,
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
	case *ast.SubtypeBaseType:
		t.Line, t.Col = s.bump()
		for _, p := range t.Params {
			s.typeExpr(p)
		}
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
	case *ast.EmbeddedFiles:
		e.Line, e.Col = s.bump()
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
		for _, ta := range e.TypeArgs {
			s.typeExpr(ta)
		}
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
	case *ast.AsPattern:
		e.Line, e.Col = s.bump()
		s.node(e.Pattern)
		e.NameLine, e.NameCol = s.bump()
	case *ast.OrPattern:
		e.Line, e.Col = s.bump()
		for _, alt := range e.Alternatives {
			s.node(alt)
		}
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
			if f.Binding != "" && f.Binding != f.Name {
				// A renaming binding (`width: a_width`) is defined at its
				// own position: left at column 0, every binding of the
				// pattern keyed one Definitions slot and each took the
				// type of the last.
				f.BindingLine, f.BindingCol = s.bump()
			} else if f.Binding != "" {
				f.BindingLine, f.BindingCol = f.NameLine, f.NameCol
			}
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
		for _, p := range e.Tails {
			s.node(p)
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
	case *ast.PatternBinding:
		// The statement starts where its pattern does, as a parsed one
		// does: the builder declares a pattern's names at the pattern, and
		// the checker's symbol for one names the statement.
		ep, ok := e.Pattern.(*ast.EnumPattern)
		if !ok {
			panic(fmt.Sprintf("uniquifyPositions: a synthesized binding of a %T; derive synthesis binds only `T(x) = v` (distinctUnwrap)", e.Pattern))
		}
		s.node(ep)
		e.Line, e.Col = ep.Line, ep.Col
		s.node(e.Value)
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
	case *ast.Binding:
		e.Line, e.Col = s.bump()
		if e.TypeAnnotation != nil {
			s.typeExpr(e.TypeAnnotation)
		}
		s.node(e.Value)
	case *ast.If:
		e.Line, e.Col = s.bump()
		s.node(e.Cond)
		s.node(e.Then)
		if e.Else != nil {
			s.node(e.Else)
		}
	case *ast.Block:
		e.Line, e.Col = s.bump()
		for _, stmt := range e.Stmts {
			s.node(stmt)
		}
	case *ast.UncheckedSubtype:
		e.Line, e.Col = s.bump()
		s.node(e.Value)
	case *ast.SubtypeRule:
		e.Line, e.Col = s.bump()
		s.node(e.Rule)
		s.node(e.Value)
	case *ast.SubtypeParentCheck:
		e.Line, e.Col = s.bump()
		if e.Parent != nil {
			s.typeExpr(e.Parent)
		}
		if e.Subtype != nil {
			s.node(e.Subtype)
		}
		s.node(e.Plain)
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

// distinctUnwrap is `typeName(binding) = value`, the statement that binds
// the value a distinct type wraps: the PatternBinding the parser builds for
// it.
func distinctUnwrap(typeName, binding string, value ast.Node, line, col int) *ast.PatternBinding {
	pattern := &ast.EnumPattern{
		Variant:    &ast.SimpleType{Name: typeName, Line: line, Col: col},
		Binding:    binding,
		BindingCol: col,
		Line:       line,
		Col:        col,
	}
	return &ast.PatternBinding{Pattern: pattern, Value: value, Line: line, Col: col}
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

// tupleItemName is the ordinal name a derived body reads a named tuple's
// item i by (`inner.first`). A tuple wider than the ordinals go has no name
// for its later items, and a derive over one reports the missing field.
func tupleItemName(i int) string {
	name, _ := ast.TupleOrdinal(i)
	return name
}

// fieldAccess returns `*ast.FieldAccess{Object: obj, Field: name, ...}`,
// matching the postfix-DOT shape the parser produces for `expr.field`.
// Used to build both `a.x` (struct fields) and `av.first` (tuple item by
// ordinal name).
func fieldAccess(obj ast.Node, fieldName string, line, col int) *ast.FieldAccess {
	return &ast.FieldAccess{
		Object: obj,
		Field:  &ast.Ident{Name: fieldName, Line: line, Col: col},
		Line:   line,
		Col:    col,
	}
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
// node. Returns nil for nodes that aren't a recognised type-decl kind.
func typeDeclTypeParams(n ast.Node) []ast.TypeParam {
	switch v := n.(type) {
	case *ast.StructDef:
		return v.TypeParams
	case *ast.EnumDef:
		return v.TypeParams
	case *ast.TypeDef:
		return v.TypeParams
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

// CheckDeriveBounds reports, for each `derive ToJson` or `derive FromJson`
// of a type nodes declare, the parts whose shape std's impls of either do
// not cover (jsonFieldGap) and a field whose type comes from its default
// under FromJson (untypedFieldDerive), each at the part. That each part has
// the derived interface is the checker's demand (checkDerivedParts), which
// these do not ask: std's `impl ToJson for Map<String, V>` meets a demand of
// ToJson at a `Map<Int, Int>` by the type's name, and a tuple has no name to
// demand an impl of. fileOf finds the file that declares an alias a part
// names.
func CheckDeriveBounds(fa *FileAnalysis, nodes []ast.Node, fileOf declFiles) []TypeError {
	if fa == nil {
		return nil
	}
	var errs []TypeError
	for _, n := range nodes {
		typeName, ok := typeDeclName(n)
		if !ok {
			continue
		}
		var decorators []ast.Decorator
		switch d := n.(type) {
		case *ast.StructDef:
			decorators = d.Decorators
		case *ast.EnumDef:
			decorators = d.Decorators
		case *ast.TypeDef:
			decorators = d.Decorators
		}
		for _, dec := range decorators {
			if dec.Name != "derive" {
				continue
			}
			for _, arg := range dec.Args {
				if te, ok := arg.(ast.TypeExpr); ok && derivesStdInterface(fa, te) {
					errs = append(errs, checkJsonDeriveParts(fa, fileOf, n, typeName, TypeExprBaseName(te))...)
				}
			}
		}
	}
	return errs
}

// checkJsonDeriveParts is CheckDeriveBounds for one derive of ifaceName for
// typeNode, named typeName, which fa declares.
func checkJsonDeriveParts(fa *FileAnalysis, fileOf declFiles, typeNode ast.Node, typeName, ifaceName string) []TypeError {
	if ifaceName != "ToJson" && ifaceName != "FromJson" {
		return nil
	}
	var errs []TypeError
	eachDeclPart(typeNode, func(p declPart) {
		if p.te == nil {
			errs = append(errs, untypedFieldDerive(fa, *p.field, typeName, ifaceName)...)
			return
		}
		if bad, help := jsonFieldGap(fa, fileOf, p.te, map[*Symbol]bool{}); bad != nil {
			errs = append(errs, TypeError{
				Line: p.line, Col: p.col, File: fa.FilePath,
				Message: fmt.Sprintf("no impl of `%s` for `%s` (required via `derive %s for %s`, field `%s`)",
					ifaceName, bad.TypeString(), ifaceName, typeName, p.name),
			}.WithHint(help))
		}
	})
	return errs
}

// declPart is one part of a type declaration whose type a derived body
// reads: a struct's or a record variant's field, a payload or one of its
// written parts, an embedded type, a distinct type's inner type or one of its
// written parts.
type declPart struct {
	// name names the part as a derive's error does: `email`,
	// `Email.address`, `Email[0]`, `inner`.
	name string
	// line and col are where the part is written.
	line, col int
	// te is the part's written type, nil for a field whose type comes from
	// its default, which field names.
	te    ast.TypeExpr
	field *ast.StructField
}

// eachDeclPart calls each with every part of the type declaration n, in the
// order the declaration writes them. A zero-sized type and a bare variant
// have none.
func eachDeclPart(n ast.Node, each func(declPart)) {
	field := func(name string, f *ast.StructField) {
		each(declPart{name: name, line: f.Line, col: f.Col, te: f.TypeAnnotation, field: f})
	}
	// A payload or an inner type written as a tuple of its parts
	// (writtenParts) is a part per element, named by its index.
	written := func(name string, line, col int, te ast.TypeExpr) {
		if ws := writtenParts(te); ws != nil {
			for i, p := range ws {
				each(declPart{name: fmt.Sprintf("%s[%d]", name, i), line: line, col: col, te: p})
			}
		} else if te != nil {
			each(declPart{name: name, line: line, col: col, te: te})
		}
	}
	switch t := n.(type) {
	case *ast.StructDef:
		for i := range t.Fields {
			field(t.Fields[i].Name, &t.Fields[i])
		}
	case *ast.EnumDef:
		for _, v := range t.Variants {
			switch v.Kind {
			case "positional":
				written(v.Name, v.Line, v.Col, v.DataTypeExpr)
			case "struct":
				for i := range v.Fields {
					field(v.Name+"."+v.Fields[i].Name, &v.Fields[i])
				}
			case "embedded":
				if v.EmbeddedTypeExpr != nil {
					each(declPart{name: v.Name, line: v.Line, col: v.Col, te: v.EmbeddedTypeExpr})
				}
			}
		}
	case *ast.TypeDef:
		written("inner", t.Line, t.Col, t.InnerTypeExpr)
	}
}

// untypedFieldDerive is the error at a field whose type comes from its
// default (`misses = 0`) under a derive that names each field's type: a
// derived from_json decodes a field by calling its type's from_json, which
// it writes from the annotation. The other derives read the fields' values
// only, and their bodies are checked as written ones are.
func untypedFieldDerive(fa *FileAnalysis, f ast.StructField, typeName, ifaceName string) []TypeError {
	if ifaceName != "FromJson" || f.Default == nil {
		return nil
	}
	return []TypeError{TypeError{
		Line: f.Line, Col: f.Col, File: fa.FilePath,
		Message: fmt.Sprintf("`derive FromJson for %s` needs field '%s' to have a written type", typeName, f.Name),
	}.WithHint(fmt.Sprintf("write its type: `%s: T = %s`", f.Name, format.RenderNode(f.Default)))}
}

// jsonFieldGap answers the part of te, a field type under a derived ToJson
// or FromJson, that no impl of either covers, with the help for it, or nil:
// a tuple, which implements neither (spec §12 *Tuples, anonymous structs
// and functions*), and a Map whose key is not String, since std implements
// both for Map<String, V> alone (a JSON object's keys are strings). It
// looks through aliases, anonymous struct fields and std's containers,
// whose impls need their elements to implement the interface. A function
// is left to the other checks.
func jsonFieldGap(fa *FileAnalysis, fileOf declFiles, te ast.TypeExpr, seen map[*Symbol]bool) (ast.TypeExpr, string) {
	switch t := te.(type) {
	case nil:
		return nil, ""
	case *ast.FuncType:
		if t.Return != nil {
			return nil, ""
		}
		return te, "a tuple implements neither `ToJson` nor `FromJson`, since JSON has no tuple; use an anonymous struct, `{a: Int, b: String}`, or a list"
	case *ast.AnonStructType:
		for _, f := range t.Fields {
			if bad, help := jsonFieldGap(fa, fileOf, f.TypeAnnotation, seen); bad != nil {
				return bad, help
			}
		}
		return nil, ""
	case *ast.GenericType:
		switch t.Name {
		case "Map":
			if len(t.Params) == 2 {
				if key, _ := deriveAliasTarget(fa, fileOf, t.Params[0], seen); !isStringTypeExpr(key) {
					return te, "std implements `ToJson` and `FromJson` for `Map<String, V>` alone, since a JSON object's keys are strings; key the map by String"
				}
				return jsonFieldGap(fa, fileOf, t.Params[1], seen)
			}
		case "List", "Vector", "Set", "Option":
			for _, p := range t.Params {
				if bad, help := jsonFieldGap(fa, fileOf, p, seen); bad != nil {
					return bad, help
				}
			}
		}
		return nil, ""
	case *ast.SimpleType, *ast.QualifiedType:
		if target, targetFA := deriveAliasTarget(fa, fileOf, te, seen); target != te {
			return jsonFieldGap(targetFA, fileOf, target, seen)
		}
	}
	return nil, ""
}

// deriveAliasTarget answers the type a `typealias` named by te stands for,
// through chains of aliases, with the analysis of the file that declares
// it, or te itself and fa: a derive's component type, or a type argument of
// one, is what the alias names, whose impls the derived body reaches. The
// alias is the symbol the file resolved te's name to, written bare or
// imported (`Counts`) or through its file (`counts.Counts`), and its target
// names types in its own file's scope.
func deriveAliasTarget(fa *FileAnalysis, fileOf declFiles, te ast.TypeExpr, seen map[*Symbol]bool) (ast.TypeExpr, *FileAnalysis) {
	for {
		alias := resolvedTypeAlias(typeExprSymbol(fa, te))
		if alias == nil || seen[alias] {
			return te, fa
		}
		node, ok := alias.Node.(*ast.TypeAlias)
		if !ok || node.TargetTypeExpr == nil {
			return te, fa
		}
		seen[alias] = true
		if alias.SourceFile != "" && fileOf != nil {
			if decl := fileOf(alias.SourceFile, alias); decl != nil {
				fa = decl
			}
		}
		te = node.TargetTypeExpr
	}
}

// typeExprSymbol is the symbol fa resolved te's name to, for a type written
// as a name, bare or through its file (`Counts`, `counts.Counts`), or nil.
func typeExprSymbol(fa *FileAnalysis, te ast.TypeExpr) *Symbol {
	if fa == nil {
		return nil
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		if sym := fa.References[Pos{Line: t.Line, Col: t.Col}]; sym != nil {
			return sym
		}
		if fa.ModuleScope != nil {
			return fa.ModuleScope.Lookup(t.Name)
		}
	case *ast.QualifiedType:
		if m, ok := t.Member.(*ast.SimpleType); ok {
			return fa.References[Pos{Line: m.Line, Col: m.Col}]
		}
	}
	return nil
}

// isStringTypeExpr reports whether te names std's String.
func isStringTypeExpr(te ast.TypeExpr) bool {
	st, ok := te.(*ast.SimpleType)
	return ok && st.Name == "String"
}

// declFiles finds the analysis of a file of the build that declares sym:
// by path, the SourceFile of a symbol of the program's own files, or for a
// symbol with none, a std module's, the build's file whose definitions
// hold it. Nil when the build has no such file, and a nil declFiles finds
// none.
type declFiles func(path string, sym *Symbol) *FileAnalysis

// buildDeclFiles is the declFiles over a build's files, which remembers
// the file it found for each symbol with no path.
func buildDeclFiles(files map[string]*FileAnalysis) declFiles {
	byPath := make(map[string]*FileAnalysis, len(files))
	for _, fa := range files {
		if fa != nil && fa.FilePath != "" {
			byPath[fa.FilePath] = fa
		}
	}
	found := map[*Symbol]*FileAnalysis{}
	return func(path string, sym *Symbol) *FileAnalysis {
		if path != "" {
			return byPath[path]
		}
		if sym == nil {
			return nil
		}
		if fa, ok := found[sym]; ok {
			return fa
		}
		var decl *FileAnalysis
		for _, fa := range files {
			if fa != nil && fa.Definitions[sym.Pos] == sym {
				decl = fa
				break
			}
		}
		found[sym] = decl
		return decl
	}
}

// declaringFile answers the analysis of the file that declares sym, and
// true: fa when fa declares it, or a file of the build that fileOf finds.
// Otherwise it answers fa and false: sym is declared in a file out of reach
// (a std module's, seen from a file checked against std's interfaces),
// whose positions mean nothing in fa, though its names may still be looked
// up there.
func declaringFile(fa *FileAnalysis, fileOf declFiles, sym *Symbol) (*FileAnalysis, bool) {
	if fa == nil || sym == nil {
		return fa, false
	}
	if (sym.SourceFile != "" && sym.SourceFile == fa.FilePath) || fa.Definitions[sym.Pos] == sym {
		return fa, true
	}
	if fileOf != nil {
		if decl := fileOf(sym.SourceFile, sym); decl != nil && decl.ModuleScope != nil {
			return decl, true
		}
	}
	return fa, false
}

// boundedTypeParams mirrors the source type's TypeParams onto a synthesized
// FuncDef, attaching the named interface as a bound on each. Spec §12 *Derive*:
// `@derive Iface struct Box<T>` synthesizes `fn equal(a: Box<T>, b: Box<T>):
// Bool` with the implicit bound `T: Iface`. Without the bound the type
// checker would accept calls with a payload type that has no Iface impl;
// the runtime would then surface "no impl found" with synth-band positions.
//
// User-written bounds on the source type (e.g. `where T: SomeOther`) are NOT
// propagated; only the @derive'd interface is added as a bound, matching
// what spec §12 *Derive* documents. If the user wants tighter bounds they can
// hand-write the impl.
func boundedTypeParams(typeParams []ast.TypeParam, ifaceName string, line, col int) []ast.TypeParam {
	if len(typeParams) == 0 {
		return nil
	}
	out := make([]ast.TypeParam, len(typeParams))
	for i, tp := range typeParams {
		var bound ast.TypeExpr = &ast.SimpleType{Name: ifaceName, Line: line, Col: col}
		if ifaceName == "ToJson" || ifaceName == "FromJson" {
			// std's, through jsonModuleAlias, whatever the file binds
			// the name to.
			bound = jsonType(ifaceName, line, col)
		}
		out[i] = ast.TypeParam{
			Name:   tp.Name,
			Bounds: []ast.TypeExpr{bound},
			Line:   line,
			Col:    col,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Comparable synthesizer
// ---------------------------------------------------------------------------

// synthesizeDeriveComparable emits a single `impl Comparable for T { fn compare(a: T,
// b: T) -> Ordering }` block whose body is structurally appropriate for the
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
//     order.
//   - Distinct type: zero-sized → `Equal`; tuple inner → unwrap into `av` /
//     `bv` and lexicographic-compare on `.first`, `.second`, ...; primitive / generic
//     / qualified inner → unwrap and delegate to `Comparable.compare`.
//
// Position info on every synthesized node points at the type decl's line/col
// during construction; uniquifyPositions then walks the result and stamps
// unique (line, col) per node so the analyzer's References / Definitions maps
// don't suffer from position aliasing.
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
		// Derive-as-assertion (spec §12 *Host types*): with one inhabitant,
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
//   - struct, N fields:     lex chain over (a_<f>, b_<f>) for each field
//   - embedded:             Comparable.compare(a0, b0) on the bound value
//
// Binding names match what enumVariantPattern uses with suffixes "_a" and
// "_b" — `positionalBindName(i, "_a")` yields `a0`, `a1`, ... and struct
// patterns bind each field as `a_<field>` / `b_<field>` (fieldBindName).
func comparableVariantPayload(v ast.EnumVariant, line, col int) ast.Node {
	switch v.Kind {
	case "bare":
		return orderingIdent("Equal", line, col)
	case "positional":
		arity := 1
		if ws := writtenParts(v.DataTypeExpr); ws != nil {
			arity = len(ws)
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
				left:  identExpr(fieldBindName(f.Name, "_a"), line, col),
				right: identExpr(fieldBindName(f.Name, "_b"), line, col),
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
// unwrap into `av` / `bv`, then lex-compare on `.first`, `.second`, ... For non-tuple
// inner: unwrap then delegate to `Comparable.compare(av, bv)`.
func comparableDistinctStmts(t *ast.TypeDef, line, col int) []ast.Node {
	if t.InnerTypeExpr == nil {
		return []ast.Node{exprStmt(orderingIdent("Equal", line, col), line, col)}
	}
	stmts := []ast.Node{
		distinctUnwrap(t.Name, "av", identExpr("a", line, col), line, col),
		distinctUnwrap(t.Name, "bv", identExpr("b", line, col), line, col),
	}
	if ws := writtenParts(t.InnerTypeExpr); ws != nil {
		pairs := make([]exprPair, len(ws))
		for i := range ws {
			pairs[i] = exprPair{
				left:  fieldAccess(identExpr("av", line, col), tupleItemName(i), line, col),
				right: fieldAccess(identExpr("bv", line, col), tupleItemName(i), line, col),
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
// `Comparable.compare(<a>, <b>)`. The callee
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
// Derives that write no function: Equatable, Hashable, Debug and Display
// ---------------------------------------------------------------------------

// derivedShapeBlock is the impl block `derive Equatable for T`, `derive
// Hashable for T`, `derive Debug for T` and `derive Display for T` write:
// one that writes no function. A use of the interface at T is answered by
// its shape (derivesByShape): Equatable compares two values as the
// machine's one structural equality does, each part through its own impl
// (spec §5 *Equality*), Hashable hashes a value as the machine hashes a
// Map key of T (spec §5 *Hashing and keys*), and Debug and Display render
// the literal that builds the value, the output spec §12 *Derive* gives
// them, an opaque type's included. The block is the derive's impl all the
// same: a hand-written impl of the interface for T beside it is the
// derive conflict (detectImplCollisions), its header is held to std's
// interface (checkDerivedInterface), it is what meets a `T: Equatable`,
// `T: Hashable` or `T: Display` bound, and a Debug block ranks over the
// name-only Debug of an opaque type (ImplPrecedence), which
// SynthesizeUniversalDebug then does not write. A generic T's block names
// its parameters, so its receiver `Box<T>` resolves; bounded, each
// parameter carries the interface as its bound, the bound a derive
// implies (spec §12 *Generic types*), which Equatable's, Hashable's and
// Display's are and Debug's, always met, is not.
func derivedShapeBlock(iface string, kind synthKind, bounded bool, typeNode ast.Node, site synthSite) []ast.Node {
	typeName, ok := typeDeclName(typeNode)
	if !ok {
		return nil
	}
	line := site.base(kind)
	params := typeDeclTypeParams(typeNode)
	generics := make([]ast.TypeParam, len(params))
	for i, tp := range params {
		generics[i] = ast.TypeParam{Name: tp.Name, Line: line, Col: i + 2}
		if bounded {
			generics[i].Bounds = []ast.TypeExpr{&ast.SimpleType{Name: iface, Line: line, Col: i + 2}}
		}
	}
	return []ast.Node{&ast.ImplBlock{
		Interface:       &ast.SimpleType{Name: iface, Line: line, Col: 1},
		Receiver:        receiverTypeExpr(typeName, params, line, 1),
		Generics:        generics,
		Line:            line,
		Col:             1,
		SynthOriginLine: site.line,
		SynthOriginCol:  site.col,
	}}
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
		ReturnTypeExpr: jsonType("Json", line, col),
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
			Value: toJsonFieldCall(fieldAccess(identExpr("value", line, col), f.Name, line, col), line, col),
		})
	}
	return jsonFieldsObj(entries, line, col)
}

// jsonFieldsObj is the object of a struct's fields: entries map each key to
// its field's `ToJson.to_json_field`, and std/json's object_of_fields
// leaves out each key whose answer is None (an `Absent` field).
func jsonFieldsObj(entries []ast.MapEntry, line, col int) ast.Node {
	return jsonCall("object_of_fields", []ast.Node{&ast.MapLit{Entries: entries, Line: line, Col: col}}, line, col)
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
		if ws := writtenParts(v.DataTypeExpr); ws != nil {
			arity = len(ws)
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
				Value: toJsonFieldCall(identExpr(fieldBindName(f.Name, "_v"), line, col), line, col),
			})
		}
		return jsonObj([]ast.MapEntry{{Key: staticString(v.Name, line, col), Value: jsonFieldsObj(entries, line, col)}}, line, col)
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
		distinctUnwrap(t.Name, "inner", identExpr("value", line, col), line, col),
	}
	if ws := writtenParts(t.InnerTypeExpr); ws != nil {
		items := make([]ast.Node, len(ws))
		for i := range ws {
			items[i] = toJsonCall(fieldAccess(identExpr("inner", line, col), tupleItemName(i), line, col), line, col)
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
			{Name: "json", TypeAnnotation: jsonType("Json", line, col), Line: line, Col: col},
		},
		ReturnTypeExpr: &ast.GenericType{
			Name: "Result",
			Params: []ast.TypeExpr{
				receiverTypeExpr(typeName, srcParams, line, col),
				jsonType("Json.ShapeError", line, col),
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
		return fromJsonDistinctStmts(t, opts, line, col)
	case *ast.ExternType:
		return []ast.Node{exprStmt(okCall(&ast.TypeIdent{Name: t.Name, Line: line, Col: col}, line, col), line, col)}
	case *ast.EnumDef:
		return []ast.Node{exprStmt(fromJsonEnumExpr(t, opts, line, col), line, col)}
	}
	return nil
}

// fromJsonEnumExpr decodes the shape derived ToJson writes for an enum: a
// bare variant as the JSON string of its name, and a payload variant as an
// object whose one key is its name, holding the payload (an array for a
// tuple payload, an object of its fields for a struct variant). std/json's
// tagged_entry reads that object, as Result's FromJson does, and refuses
// one with no key, several keys or a key no payload variant has.
func fromJsonEnumExpr(e *ast.EnumDef, opts deriveOptions, line, col int) ast.Node {
	json := func() ast.Node { return identExpr("json", line, col) }
	var tagged []ast.EnumVariant
	var bare []ast.CaseBranch
	for _, v := range e.Variants {
		if v.Kind == "bare" {
			bare = append(bare, ast.CaseBranch{
				Pattern: staticString(v.Name, line, col),
				Body:    okCall(fieldAccess(&ast.TypeIdent{Name: e.Name, Line: line, Col: col}, v.Name, line, col), line, col),
				Line:    line,
				Col:     col,
			})
			continue
		}
		tagged = append(tagged, v)
	}
	// rest decodes json when it is not a bare variant's string.
	var rest ast.Node = jsonShapeErr(e.Name, json(), line, col)
	if len(tagged) > 0 {
		tags := make([]ast.Node, len(tagged))
		var arms []ast.CaseBranch
		for i, v := range tagged {
			tags[i] = staticString(v.Name, line, col)
			var decoded ast.Node
			if tupleVariant(v) {
				// Its errors carry the variant's name already.
				decoded = fromJsonVariantPayloadExpr(e, v, opts, line, col)
			} else {
				decoded = &ast.Case{
					Value: fromJsonVariantPayloadExpr(e, v, opts, line, col),
					Branches: []ast.CaseBranch{
						{Pattern: enumBindPattern("Ok", "__variant", line, col), Body: okCall(identExpr("__variant", line, col), line, col), Line: line, Col: col},
						{Pattern: enumBindPattern("Err", "__err", line, col), Body: errCall(jsonShapePrepend(identExpr("__err", line, col), v.Name, line, col), line, col), Line: line, Col: col},
					},
					Line: line,
					Col:  col,
				}
			}
			// The last variant's arm takes any key: tagged_entry answers
			// only the keys in tags, and the arms before it took the others.
			var key ast.Node = staticString(v.Name, line, col)
			if i == len(tagged)-1 {
				key = &ast.WildcardPattern{Line: line, Col: col}
			}
			arms = append(arms, ast.CaseBranch{
				Pattern: &ast.EnumPattern{
					Variant: &ast.SimpleType{Name: "Ok", Line: line, Col: col},
					Payload: &ast.TuplePattern{Patterns: []ast.Node{key, &ast.IdentPattern{Name: "__payload", Line: line, Col: col}}, Line: line, Col: col},
					Line:    line,
					Col:     col,
				},
				Body: decoded,
				Line: line,
				Col:  col,
			})
		}
		arms = append(arms, ast.CaseBranch{
			Pattern: enumBindPattern("Err", "__err", line, col),
			Body:    errCall(identExpr("__err", line, col), line, col),
			Line:    line,
			Col:     col,
		})
		rest = &ast.Case{
			Value: jsonCall("tagged_entry", []ast.Node{
				json(),
				staticString(e.Name, line, col),
				&ast.ListLit{Items: tags, Line: line, Col: col},
			}, line, col),
			Branches: arms,
			Line:     line,
			Col:      col,
		}
	}
	if len(bare) == 0 {
		return rest
	}
	// An unknown name is reported with its text: `got "Purple"`.
	unknown := errCall(&ast.StructLit{
		TypeName: jsonVariant("ShapeError", line, col),
		Fields: []ast.StructFieldVal{
			{Name: "path", Value: &ast.ListLit{Line: line, Col: col}, Line: line, Col: col},
			{Name: "expected", Value: staticString(e.Name, line, col), Line: line, Col: col},
			{Name: "got", Value: &ast.StringInterp{Parts: []ast.StringPart{
				ast.StringText{Value: `"`},
				ast.StringExpr{Expr: identExpr("__tag", line, col)},
				ast.StringText{Value: `"`},
			}, Line: line, Col: col}, Line: line, Col: col},
		},
		Line: line,
		Col:  col,
	}, line, col)
	bare = append(bare, ast.CaseBranch{Pattern: &ast.WildcardPattern{Line: line, Col: col}, Body: unknown, Line: line, Col: col})
	return &ast.Case{
		Value: json(),
		Branches: []ast.CaseBranch{
			{
				Pattern: &ast.EnumPattern{
					Variant: jsonVariant("String", line, col),
					Payload: &ast.IdentPattern{Name: "__tag", Line: line, Col: col},
					Line:    line,
					Col:     col,
				},
				Body: &ast.Case{Value: identExpr("__tag", line, col), Branches: bare, Line: line, Col: col},
				Line: line,
				Col:  col,
			},
			{Pattern: &ast.WildcardPattern{Line: line, Col: col}, Body: rest, Line: line, Col: col},
		},
		Line: line,
		Col:  col,
	}
}

// tupleVariant reports a variant whose payload is a tuple, `V (A, B)`.
func tupleVariant(v ast.EnumVariant) bool {
	return v.Kind == "positional" && writtenParts(v.DataTypeExpr) != nil
}

// fromJsonVariantPayloadExpr decodes `__payload`, the value under a payload
// variant's key, into the variant: `Ok(E.V(...))`, or the payload's error.
// A tuple variant's errors name the variant and the element; the caller
// prepends the variant's name to the others'. (A tuple variant's `case`
// cannot be the subject of that prepending `case`: the checker leaves the
// error binding untyped there.)
func fromJsonVariantPayloadExpr(e *ast.EnumDef, v ast.EnumVariant, opts deriveOptions, line, col int) ast.Node {
	payload := func() ast.Node { return identExpr("__payload", line, col) }
	ctor := func(args ...ast.Node) ast.Node {
		return &ast.Call{
			Func: fieldAccess(&ast.TypeIdent{Name: e.Name, Line: line, Col: col}, v.Name, line, col),
			Args: args,
			Line: line,
			Col:  col,
		}
	}
	// decodeThen is `case T.from_json(subject) { Ok(bind) -> then; Err(e) ->
	// Err(prepend(e, segment)) }`, with no prepend when segment is "".
	decodeThen := func(te ast.TypeExpr, subject ast.Node, bind, segment string, then ast.Node) ast.Node {
		var fail ast.Node = errCall(identExpr("__err", line, col), line, col)
		if segment != "" {
			fail = errCall(jsonShapePrepend(jsonShapePrepend(identExpr("__err", line, col), segment, line, col), v.Name, line, col), line, col)
		}
		return &ast.Case{
			Value: fromJsonCallForType(te, subject, line, col),
			Branches: []ast.CaseBranch{
				{Pattern: enumBindPattern("Ok", bind, line, col), Body: then, Line: line, Col: col},
				{Pattern: enumBindPattern("Err", "__err", line, col), Body: fail, Line: line, Col: col},
			},
			Line: line,
			Col:  col,
		}
	}
	switch v.Kind {
	case "positional":
		ws := writtenParts(v.DataTypeExpr)
		if ws == nil {
			return decodeThen(v.DataTypeExpr, payload(), "__a0", "", okCall(ctor(identExpr("__a0", line, col)), line, col))
		}
		items := make([]ast.Node, len(ws))
		heads := make([]ast.Node, len(ws))
		for i := range ws {
			items[i] = identExpr(fmt.Sprintf("__a%d", i), line, col)
			heads[i] = &ast.IdentPattern{Name: fmt.Sprintf("__j%d", i), Line: line, Col: col}
		}
		var body ast.Node = okCall(ctor(&ast.TupleLit{Items: items, Line: line, Col: col}), line, col)
		for i := len(ws) - 1; i >= 0; i-- {
			body = decodeThen(ws[i], identExpr(fmt.Sprintf("__j%d", i), line, col), fmt.Sprintf("__a%d", i), fmt.Sprintf("[%d]", i), body)
		}
		return &ast.Case{
			Value: payload(),
			Branches: []ast.CaseBranch{
				{
					Pattern: &ast.EnumPattern{
						Variant: jsonVariant("Arr", line, col),
						Payload: &ast.ListPattern{Heads: heads, Line: line, Col: col},
						Line:    line,
						Col:     col,
					},
					Body: body,
					Line: line,
					Col:  col,
				},
				{
					Pattern: &ast.WildcardPattern{Line: line, Col: col},
					Body: errCall(jsonShapePrepend(
						jsonCall("shape_error_root", []ast.Node{staticString(v.DataTypeExpr.TypeString(), line, col), payload()}, line, col),
						v.Name, line, col), line, col),
					Line: line,
					Col:  col,
				},
			},
			Line: line,
			Col:  col,
		}
	case "struct":
		litType := func() ast.TypeExpr { return qualifiedTypeExpr(e.Name, v.Name, line, col) }
		return fromJsonObjectExpr(payload(), e.Name+"."+v.Name, v.Fields, litType, len(e.Variants) > 1, opts, line, col)
	case "embedded":
		return decodeThen(v.EmbeddedTypeExpr, payload(), "__a0", "", okCall(identExpr("__a0", line, col), line, col))
	}
	return jsonShapeErr(e.Name, payload(), line, col)
}

func fromJsonStructExpr(t *ast.StructDef, opts deriveOptions, line, col int) ast.Node {
	litType := func() ast.TypeExpr { return &ast.SimpleType{Name: t.Name, Line: line, Col: col} }
	return fromJsonObjectExpr(identExpr("json", line, col), t.Name, t.Fields, litType, false, opts, line, col)
}

// fromJsonObjectExpr decodes subject, a Json, as an object holding fields
// and answers `Ok(litType{...})`, or an Err naming the field that failed.
// A derived struct decodes its whole JSON this way, and an enum's struct
// variant its payload (partial: the enum has other variants, so a pattern
// of this variant needs a wildcard beside it).
func fromJsonObjectExpr(subject ast.Node, typeName string, fields []ast.StructField, litType func() ast.TypeExpr, partial bool, opts deriveOptions, line, col int) ast.Node {
	objPattern := &ast.EnumPattern{
		Variant: jsonVariant("Obj", line, col),
		Payload: &ast.IdentPattern{Name: "fields", Line: line, Col: col},
		Line:    line,
		Col:     col,
	}
	return &ast.Case{
		Value: subject,
		Branches: []ast.CaseBranch{
			{
				Pattern: objPattern,
				Body:    fromJsonStructFieldsExpr(subject, typeName, fields, litType, partial, opts, line, col),
				Line:    line,
				Col:     col,
			},
			{
				Pattern: &ast.WildcardPattern{Line: line, Col: col},
				Body:    jsonShapeErr(typeName, subject, line, col),
				Line:    line,
				Col:     col,
			},
		},
		Line: line,
		Col:  col,
	}
}

// fromJsonStructFieldsExpr decodes `fields`, the object's entries, one field
// at a time and builds the value once at the end, so the body grows by one
// step per field:
//
//	case json$.decode_field<A>(fields, "a", "A")
//	    Ok(__value_a) ->
//	        case json$.decode_defaulted_field<B>(fields, "b")
//	            Ok(__value_b) ->
//	                case T{a: __value_a}
//	                    T{b: __default_b} -> Ok(T{a: __value_a, b: case __value_b { Some(v) -> v; None -> __default_b }})
//	                end
//	            Err(__err) -> Err(__err)
//	        end
//	    Err(__err) -> Err(__err)
//	end
//
// A field with no default reads its key, else `from_missing`, else the error
// naming the key (std/json's decode_field). A defaulted field reads
// Some(value) the same way, and None where its default fills it
// (decode_defaulted_field). The defaults come from a literal of the
// required fields alone, which the struct literal's own default rule
// completes. Placing field.Default in the body would share the
// declaration's node with the synthesized body: uniquifyPositions would
// restamp the declaration's positions into the synthesized band, and would
// refuse any default expression its walker does not list (`[]`, a lambda).
func fromJsonStructFieldsExpr(subject ast.Node, typeName string, fields []ast.StructField, litType func() ast.TypeExpr, partial bool, opts deriveOptions, line, col int) ast.Node {
	value := func(name string) ast.Node { return identExpr("__value_"+name, line, col) }
	var requiredNames []string
	var defaulted []ast.StructFieldVal
	var defaults []ast.StructPatternField
	var decoded []ast.StructField
	for _, field := range fields {
		if field.TypeAnnotation == nil {
			// A field whose type comes from its default is refused at the
			// field (untypedFieldDerive); the body leaves it to its default
			// rather than name a type it does not have.
			continue
		}
		decoded = append(decoded, field)
		if field.Default == nil {
			requiredNames = append(requiredNames, field.Name)
			continue
		}
		defaultName := "__default_" + field.Name
		defaults = append(defaults, ast.StructPatternField{Name: field.Name, Binding: defaultName})
		defaulted = append(defaulted, ast.StructFieldVal{
			Name: field.Name,
			Value: &ast.Case{
				Value: value(field.Name),
				Branches: []ast.CaseBranch{
					{Pattern: enumBindPattern("Some", "__v", line, col), Body: identExpr("__v", line, col), Line: line, Col: col},
					{Pattern: &ast.EnumPattern{Variant: &ast.SimpleType{Name: "None", Line: line, Col: col}, Line: line, Col: col}, Body: identExpr(defaultName, line, col), Line: line, Col: col},
				},
				Line: line,
				Col:  col,
			},
			Line: line,
			Col:  col,
		})
	}
	requiredFields := func() []ast.StructFieldVal {
		out := make([]ast.StructFieldVal, len(requiredNames))
		for i, name := range requiredNames {
			out[i] = ast.StructFieldVal{Name: name, Value: value(name), Line: line, Col: col}
		}
		return out
	}
	var body ast.Node
	if len(defaults) == 0 {
		body = okCall(&ast.StructLit{TypeName: litType(), Fields: requiredFields(), Line: line, Col: col}, line, col)
	} else {
		branches := []ast.CaseBranch{{
			Pattern: &ast.StructPattern{TypeName: litType(), Fields: defaults, Line: line, Col: col},
			Body: okCall(&ast.StructLit{
				TypeName: litType(),
				Fields:   append(requiredFields(), defaulted...),
				Line:     line,
				Col:      col,
			}, line, col),
			Line: line,
			Col:  col,
		}}
		if partial {
			// Never taken: the subject is the variant the pattern names.
			branches = append(branches, ast.CaseBranch{
				Pattern: &ast.WildcardPattern{Line: line, Col: col},
				Body:    jsonShapeErr(typeName, subject, line, col),
				Line:    line,
				Col:     col,
			})
		}
		body = &ast.Case{
			Value:    &ast.StructLit{TypeName: litType(), Fields: requiredFields(), Line: line, Col: col},
			Branches: branches,
			Line:     line,
			Col:      col,
		}
	}
	for i := len(decoded) - 1; i >= 0; i-- {
		field := decoded[i]
		key := jsonFieldName(field.Name, opts)
		ta, ok := cloneTypeExpr(field.TypeAnnotation, line, col)
		if !ok {
			ta = &ast.SimpleType{Name: field.TypeAnnotation.TypeString(), Line: line, Col: col}
		}
		args := []ast.Node{identExpr("fields", line, col), staticString(key, line, col)}
		method := "decode_defaulted_field"
		if field.Default == nil {
			method = "decode_field"
			args = append(args, staticString(field.TypeAnnotation.TypeString(), line, col))
		}
		decode := &ast.Call{
			Func:     fieldAccess(identExpr(jsonModuleAlias, line, col), method, line, col),
			TypeArgs: []ast.TypeExpr{ta},
			Args:     args,
			Line:     line,
			Col:      col,
		}
		body = &ast.Case{
			Value: decode,
			Branches: []ast.CaseBranch{
				{Pattern: enumBindPattern("Ok", "__value_"+field.Name, line, col), Body: body, Line: line, Col: col},
				{Pattern: enumBindPattern("Err", "__err", line, col), Body: errCall(identExpr("__err", line, col), line, col), Line: line, Col: col},
			},
			Line: line,
			Col:  col,
		}
	}
	return body
}

func fromJsonDistinctStmts(t *ast.TypeDef, opts deriveOptions, line, col int) []ast.Node {
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

func toJsonCall(arg ast.Node, line, col int) ast.Node {
	return jsonIfaceCall("ToJson", "to_json", []ast.Node{arg}, line, col)
}

func toJsonFieldCall(arg ast.Node, line, col int) ast.Node {
	return jsonIfaceCall("ToJson", "to_json_field", []ast.Node{arg}, line, col)
}

// jsonIfaceCall is `json$.<iface>.<method>(args)`, a call of a function of
// std/json's interface iface (`ToJson`, `FromJson`) through
// jsonModuleAlias: std's interface, whatever the file binds the name to.
func jsonIfaceCall(iface, method string, args []ast.Node, line, col int) *ast.Call {
	return &ast.Call{
		Func: fieldAccess(fieldAccess(identExpr(jsonModuleAlias, line, col), iface, line, col), method, line, col),
		Args: args,
		Line: line,
		Col:  col,
	}
}

func fromJsonCallForType(target ast.TypeExpr, arg ast.Node, line, col int) ast.Node {
	return fromJsonHookForType(target, "from_json", []ast.Node{arg}, line, col)
}

// fromJsonHookForType is `json$.FromJson.method<Target>(args)`, a call of
// std's FromJson's method at the type target, as source writes it. The
// type argument resolves wherever target is declared: a typealias another
// file declares (`counts.Counts`), like a type with arguments or an
// anonymous struct, is no call's owner (`Counts.from_json`), and the
// checker records this call as it records any other.
func fromJsonHookForType(target ast.TypeExpr, method string, args []ast.Node, line, col int) ast.Node {
	ta, ok := cloneTypeExpr(target, line, col)
	if !ok {
		ta = &ast.SimpleType{Name: target.TypeString(), Line: line, Col: col}
	}
	call := jsonIfaceCall("FromJson", method, args, line, col)
	call.TypeArgs = []ast.TypeExpr{ta}
	return call
}

// jsonModuleAlias is the name derived ToJson and FromJson code reaches
// std/json by: the alias of a hidden whole-module import that lowering the
// derive adds (jsonImport). No source can write a `$`, so the derived code
// means std's `Json` whatever the file itself binds that name to.
const jsonModuleAlias = "json$"

// jsonImport is `import std::json as json$`, on the line of the message
// expansions' hidden imports (hiddenImportLine), after their columns.
func jsonImport() *ast.ImportStmt {
	l := hiddenImportLine
	return &ast.ImportStmt{
		ModulePath:   []ast.Node{&ast.Ident{Name: "std", Line: l, Col: 10}, &ast.Ident{Name: "json", Line: l, Col: 11}},
		FileSegments: 2,
		ModuleAlias:  &ast.Ident{Name: jsonModuleAlias, Line: l, Col: 12},
		Line:         l,
		Col:          9,
	}
}

// hasJsonImport reports whether nodes hold jsonImport's import.
func hasJsonImport(nodes []ast.Node) bool {
	for _, n := range nodes {
		if imp, ok := n.(*ast.ImportStmt); ok && imp.Line == hiddenImportLine && imp.ModuleAlias != nil &&
			ast.ImportNodeName(imp.ModuleAlias) == jsonModuleAlias {
			return true
		}
	}
	return false
}

// jsonType is the type std/json declares as name (`Json`,
// `Json.ShapeError`), named through jsonModuleAlias.
func jsonType(name string, line, col int) ast.TypeExpr {
	return &ast.QualifiedType{
		Module:     jsonModuleAlias,
		ModuleLine: line,
		ModuleCol:  col,
		Member:     &ast.SimpleType{Name: name, Line: line, Col: col},
	}
}

// jsonVariant is `json$.Json.<member>` as a literal prefix or a pattern
// writes it: a variant of std's `Json`, or its `ShapeError` struct.
func jsonVariant(member string, line, col int) ast.TypeExpr {
	return qualifiedTypeExpr(jsonModuleAlias+".Json", member, line, col)
}

// jsonOwner is `json$.Json` as the owner of a variant constructor call.
func jsonOwner(line, col int) ast.Node {
	return fieldAccess(identExpr(jsonModuleAlias, line, col), "Json", line, col)
}

// jsonCall is a call of std/json's function method through
// jsonModuleAlias.
func jsonCall(method string, args []ast.Node, line, col int) ast.Node {
	return &ast.Call{
		Func: fieldAccess(identExpr(jsonModuleAlias, line, col), method, line, col),
		Args: args,
		Line: line,
		Col:  col,
	}
}

func jsonShapePrepend(err ast.Node, segment string, line, col int) ast.Node {
	return jsonCall("shape_error_prepend", []ast.Node{err, staticString(segment, line, col)}, line, col)
}

func jsonShapeErr(expected string, got ast.Node, line, col int) ast.Node {
	return errCall(jsonCall("shape_error_root", []ast.Node{staticString(expected, line, col), got}, line, col), line, col)
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
		Func: fieldAccess(jsonOwner(line, col), "String", line, col),
		Args: []ast.Node{staticString(value, line, col)},
		Line: line,
		Col:  col,
	}
}

func jsonObj(entries []ast.MapEntry, line, col int) ast.Node {
	return &ast.MapLit{TypeName: jsonVariant("Obj", line, col), Entries: entries, Line: line, Col: col}
}

func jsonArr(items []ast.Node, line, col int) ast.Node {
	return &ast.ListLit{TypeName: jsonVariant("Arr", line, col), Items: items, Line: line, Col: col}
}

func jsonNull(line, col int) ast.Node {
	return fieldAccess(jsonOwner(line, col), "Null", line, col)
}

func enumBindPattern(variant, bind string, line, col int) ast.Node {
	return &ast.EnumPattern{
		Variant: &ast.SimpleType{Name: variant, Line: line, Col: col},
		Payload: &ast.IdentPattern{Name: bind, Line: line, Col: col},
		Line:    line,
		Col:     col,
	}
}

func cloneTypeExpr(te ast.TypeExpr, line, col int) (ast.TypeExpr, bool) {
	switch t := te.(type) {
	case *ast.AnonStructType:
		fields := make([]ast.StructField, len(t.Fields))
		for i, f := range t.Fields {
			ft, ok := cloneTypeExpr(f.TypeAnnotation, line, col)
			if !ok {
				return nil, false
			}
			fields[i] = ast.StructField{Name: f.Name, TypeAnnotation: ft, Line: line, Col: col}
		}
		return &ast.AnonStructType{Fields: fields, Line: line, Col: col}, true
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
	case *ast.ConjunctionType:
		members := make([]ast.TypeExpr, len(t.Members))
		for i, m := range t.Members {
			cloned, ok := cloneTypeExpr(m, line, col)
			if !ok {
				return nil, false
			}
			members[i] = cloned
		}
		return &ast.ConjunctionType{Members: members, Line: line, Col: col}, true
	}
	return nil, false
}

// deriveArgName is the base name of the interface a `@derive` decorator's
// argument names: the interface as the derive writes it (`ToJson`,
// `json.ToJson`), which the derive lowering puts there. "" for anything
// else.
func deriveArgName(arg ast.Node) string {
	if te, ok := arg.(ast.TypeExpr); ok {
		return TypeExprBaseName(te)
	}
	return ""
}

// deriveSupportedDescription is the names of derivableInterfaces joined
// with commas, for a message that lists them.
func deriveSupportedDescription() string {
	return strings.Join(DerivableInterfaces(), ", ")
}

// withoutRepeatedNames answers a struct or enum declaration as synthesis
// should read it: each field and variant once, the first of a repeated name
// kept. A repeated name is CheckRepeatedFields' error at the declaration; the
// code synthesized from the declaration as written would repeat it again, in
// a pattern or literal with no source of its own. A declaration with no
// repeat is answered unchanged.
func withoutRepeatedNames(typeNode ast.Node) ast.Node {
	switch n := typeNode.(type) {
	case *ast.StructDef:
		if fields, ok := firstOfEachField(n.Fields); ok {
			c := *n
			c.Fields = fields
			return &c
		}
	case *ast.EnumDef:
		changed := false
		seen := map[string]bool{}
		var variants []ast.EnumVariant
		for _, v := range n.Variants {
			if seen[v.Name] {
				changed = true
				continue
			}
			seen[v.Name] = true
			if fields, ok := firstOfEachField(v.Fields); ok {
				v.Fields = fields
				changed = true
			}
			variants = append(variants, v)
		}
		if changed {
			c := *n
			c.Variants = variants
			return &c
		}
	}
	return typeNode
}

// firstOfEachField answers fields with each name's first field only, and
// whether that dropped any.
func firstOfEachField(fields []ast.StructField) ([]ast.StructField, bool) {
	seen := map[string]bool{}
	var out []ast.StructField
	for _, f := range fields {
		if seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		out = append(out, f)
	}
	return out, len(out) != len(fields)
}
