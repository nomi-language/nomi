package analysis

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// FinalizeCoherence runs the post-CheckTypes coherence checks over the
// whole of fa's program: MissingImpls with every file of the program as a
// demander. Callers run it once at the end of their analyze pipeline, after
// every file whose demands it reads has been checked: the checker records a
// file's demands (RecordingKindCallSite / GenericBoundCheck /
// TypedLiteralSlot / InterfaceTypedParam) in that file's own ImplManifest
// while it checks the file. The errors are the program's, whichever file
// the caller reports them with.
//
// The program keeps what each file contributes (programDemands), so a call
// after a recheck rebuilt some files' bodies reads only those files again.
func FinalizeCoherence(fa *FileAnalysis) []TypeError {
	return fa.Program().finalizeCoherence()
}

// MissingImpls reports each (interface, type) pair the demanders' own
// ImplManifests demand and no impl of program p supplies, worked out afresh
// as a program's kept check works it out (programDemands): the supply is
// the impl index's, plus any file's own impls the index does not hold (a
// module a function body's import loaded after the index was built), and
// the demands are the demanders' with the stdlib base's when the demanders
// hold every file of it. Nil when p has no impl index.
func MissingImpls(p *Program, demanders []*FileAnalysis) []TypeError {
	if p == nil || p.ImplIndex == nil {
		return nil
	}
	return newProgramDemands(p, nil, demanders, false).diagnostics()
}

// stdMissingImpls is the missing-impl check a std module fa makes of its
// own demands, which a program's check leaves to it (programDemands.wants):
// each pair a body, a derive or a `//!` prompt of fa demands and no impl
// supplies, reported as a program's check reports one of the program's
// own. The checker runs it at the end of a std file's check, so std's
// analysis reports a std body's `a <= b` on a type with no Comparable
// where std is checked, and the builder never meets the body.
func stdMissingImpls(fa *FileAnalysis) []TypeError {
	p := fa.Program()
	if p == nil || p.ImplIndex == nil {
		return nil
	}
	return newProgramDemands(p, nil, []*FileAnalysis{fa}, true).diagnostics()
}

// sortRecordings orders recordings by source location, (Pos.File, Pos.Line,
// Pos.Col), with Kind as the final tiebreaker for two at one position.
func sortRecordings(recs []Recording) {
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Pos.File != recs[j].Pos.File {
			return recs[i].Pos.File < recs[j].Pos.File
		}
		if recs[i].Pos.Line != recs[j].Pos.Line {
			return recs[i].Pos.Line < recs[j].Pos.Line
		}
		if recs[i].Pos.Col != recs[j].Pos.Col {
			return recs[i].Pos.Col < recs[j].Pos.Col
		}
		return recs[i].Kind < recs[j].Kind
	})
}

// DetectMissingImpls reports a TypeError for every (Iface, T) pair the
// analyzer recorded a demand for (idx.ImplManifest) but no impl supplies
// (idx.Impls). The runtime dispatch table is keyed (Iface.method, T) and
// dispatchShim raises an "unknown impl" error at call time when the pair
// isn't pre-registered; this diagnostic surfaces the same condition at
// build time so the user sees a precise demand-site location instead of
// a runtime stack trace.
//
// Stdlib hands-off rule: a pair every Recording of which has a stdlib
// Pos.File is a program's check's to leave out, as programDemands.wants
// leaves it out: std's own check reports it (stdMissingImpls), once,
// rather than every program that loads the module.
//
// Primary error site: the first user-project Recording (non-stdlib
// Pos.File), falling back to the first Recording overall if all are
// stdlib (std's own check). The message names how the
// primary site demands the impl, never its position, which the diagnostic
// itself carries; the remaining Recordings the author can change are
// related locations (missingImplError). DeriveCtx
// info on each derive-sourced Recording is unpacked into the "via"
// clause so the diagnostic names the @derive site and the offending
// component.
//
// `typeDecls` is reserved for a "T declared here" note polish item;
// pass nil for the v1 call site. When non-nil and the type is present,
// a `(<T> declared at <File>:<Line>)` clause is appended to the
// message.
func DetectMissingImpls(idx *ProjectImplIndex, typeDecls map[string]Pos) []TypeError {
	if idx == nil || len(idx.ImplManifest) == 0 {
		return nil
	}
	var errs []TypeError

	// Deterministic iteration: sort ifaces, then types within each iface.
	// Same posture as detectOrphanImpls.
	ifaceNames := make([]string, 0, len(idx.ImplManifest))
	for iface := range idx.ImplManifest {
		ifaceNames = append(ifaceNames, iface)
	}
	sort.Strings(ifaceNames)

	for _, iface := range ifaceNames {
		if !missingImplsChecked(iface) {
			continue
		}
		byType := idx.ImplManifest[iface]
		typeNames := make([]string, 0, len(byType))
		for t := range byType {
			typeNames = append(typeNames, t)
		}
		sort.Strings(typeNames)

		for _, typeName := range typeNames {
			recs := byType[typeName]
			if !slices.ContainsFunc(recs, func(rec Recording) bool { return !isStdlibRecording(rec) }) {
				continue
			}
			if e, ok := idx.missingImpl(iface, typeName, recs, typeDecls); ok {
				errs = append(errs, e)
			}
		}
	}
	return errs
}

// missingImplsChecked reports whether DetectMissingImpls reports a missing
// impl of iface at all.
func missingImplsChecked(iface string) bool {
	// Universal default Debug: Debug is satisfied by every type (the
	// eager auto-synthesis pass materializes a structural / name-only
	// Debug impl for every declared type), so a (Debug, T) pair is
	// never genuinely missing. Skip the whole interface — belt-and-
	// suspenders against any Debug demand the synth pass doesn't cover
	// (e.g. a non-nominal kind that has no declaration to synthesize
	// from but is rendered by a Go intrinsic). Every other interface
	// stays subject to the missing-impl diagnostic.
	if iface == "Debug" {
		return false
	}
	// Universal struct interface `Struct`: satisfied structurally by every
	// struct (named or anonymous), with no registered impl — conformance is
	// a predicate (isStructShaped), so a (Struct, T) pair is never genuinely
	// missing. Skip it like Debug; struct-only-ness is enforced at the call
	// site by the structural conformance check, not here.
	if iface == "Struct" {
		return false
	}
	// `Distinct<T>` is decided the same way, by what a distinct type
	// wraps (distinct_self.go).
	if iface == "Distinct" {
		return false
	}
	return true
}

// missingImpl answers DetectMissingImpls' diagnostic for the bucket of
// recordings recs that demand typeName's impl of iface, and false when the
// bucket draws none.
func (idx *ProjectImplIndex) missingImpl(iface, typeName string, recs []Recording, typeDecls map[string]Pos) (TypeError, bool) {
	if len(recs) == 0 {
		return TypeError{}, false
	}
	// Supply side: is there an impl for this (T, Iface)?
	//
	// `Impls` is keyed by BARE type name, and so is this bucket, so a
	// bare-name hit may be another module's conformance on a
	// same-named type — and the bucket itself may hold demands for
	// several of them. `unsuppliedRecordings` keeps only the demands
	// whose OWN declaration has no impl; it returns nothing whenever
	// the identity index cannot answer, which is what every path that
	// does not populate the index relies on. See
	// missing_impl_identity.go.
	// borrowed is whether another type of the same name has the impl,
	// so the message names the type by its module.
	borrowed := false
	if idx.Impls != nil {
		if ifaces, ok := idx.Impls[typeName]; ok && ifaces[iface] {
			recs = idx.unsuppliedRecordings(recs, typeName, iface)
			if len(recs) == 0 {
				return TypeError{}, false
			}
			borrowed = true
		}
	}
	// Equality-operator demands are weak — see
	// RecordingKindEqualityOperator. `==` / `!=` on a concrete
	// type *prefer* a custom `impl Equatable` block when one
	// exists, but they fall back to structural `rt.Equal` at
	// runtime if no impl is supplied. So a
	// group whose recordings are ALL equality-operator-kind
	// shouldn't surface a missing-impl diagnostic — `pub type
	// Id Int` keeps working with `==` even though it has no
	// `impl Equatable`. A mix of equality-operator + "real"
	// demands still surfaces (the real demand needs the impl).
	// Ordering-operator demands (RecordingKindOrderingOperator)
	// are NOT skipped: ordering has no structural fallback, so
	// `<` on a type with no `impl Comparable` is an ordinary
	// missing-impl error.
	allEqualityOperator := true
	for _, rec := range recs {
		if rec.Kind != RecordingKindEqualityOperator {
			allEqualityOperator = false
			break
		}
	}
	if allEqualityOperator {
		return TypeError{}, false
	}

	return idx.missingImplError(iface, typeName, recs, typeDecls, borrowed), true
}

// isStdlibRecording reports whether the Recording's Pos.File names a
// stdlib source file. The question is stdlibModuleForPath's — the same one
// DocumentManager.isStdlibFile and originForStandalonePath ask, which is why
// it is one function and not three copies of a directory-name check. An empty
// Pos.File (single-file BuildFileWithStdlib path; recorder didn't have
// FilePath in scope) is treated as non-stdlib so the suppression doesn't
// accidentally silence diagnostics in raw-test contexts.
func isStdlibRecording(rec Recording) bool {
	_, ok := stdlibModuleForPath(rec.Pos.File)
	return ok
}

// missingImplError builds the diagnostic for one (Iface, T) pair with
// at least one Recording. Anchors the primary site at the first user-
// project Recording (stdlib fallback when every Recording is stdlib),
// attaches the other Recordings outside std as related locations, and
// appends a help line suggesting either derive or a hand-written impl block.
// When borrowed is set, an impl of iface's name matched by bare name is
// another declaration's, and the message names that half of the pair
// through its declaring module: the interface (`b.Show`) when the type has
// an impl of another interface of the name, else the type (`users.User`),
// which another type of its name lends the impl.
func (idx *ProjectImplIndex) missingImplError(iface, typeName string, recs []Recording, typeDecls map[string]Pos, borrowed bool) TypeError {
	// Primary: first user-project Recording, fallback to recs[0].
	primaryIdx := 0
	for i, rec := range recs {
		if !isStdlibRecording(rec) {
			primaryIdx = i
			break
		}
	}
	primary := recs[primaryIdx]
	shown, shownIface := typeName, iface
	otherIface := borrowed && idx.implementsInterfaceNamed(primary.TypeOrigin, typeName, iface)
	switch {
	case otherIface:
		shownIface = idx.shownInterface(primary.TypeOrigin, typeName, primary.IfaceOrigin, iface)
	case borrowed:
		shown = throughFile(primary.TypeOrigin, typeName)
	}

	e := TypeError{
		Line:    primary.Pos.Line,
		Col:     primary.Pos.Col,
		Message: fmt.Sprintf("no impl of `%s` for `%s` (%s)", shownIface, shown, requiredClause(primary)),
		File:    primary.Pos.File,
	}.WithHint(implBoundNote(primary))

	// Every other Recording in the code the report is for is a related
	// location. In a program's report a std one is not: it is a site inside
	// std's own bodies or doc prompts (`std/json.nomi:82:39` for a
	// program's `Error`, which shares its bare name with std's), which the
	// author can neither read as their code nor change. Std's own report
	// (stdMissingImpls), whose primary is std's, names them all.
	stdOwn := isStdlibRecording(primary)
	for i, rec := range recs {
		if i == primaryIdx || (isStdlibRecording(rec) && !stdOwn) {
			continue
		}
		e = e.WithRelated(RelatedInfo{File: rec.Pos.File, Line: rec.Pos.Line, Col: rec.Pos.Col,
			Message: "also required here " + requiredBy(rec)})
	}

	if typeDecls != nil && shown == typeName {
		// typeDecls is keyed by bare name, so a qualified type's entry may
		// be the other type's declaration.
		if decl, ok := typeDecls[typeName]; ok {
			e = e.WithRelated(nameRelated(decl.File, decl, typeName, "`"+typeName+"` is declared here"))
		}
	}

	switch {
	case IsBodyOrigin(primary.IfaceOrigin):
		return e.WithHint(bodyInterfaceHelp(iface, shown))
	case otherIface:
		// The type implements another interface of the name. A derive
		// writes std's interface, and an operator's or the prelude's
		// interface has one declaration, so the fix is an impl block of
		// the interface the demand names.
		return e.WithHint(implBlockHelp(withTypeParams(shownIface, primary.IfaceTypeParams), shown))
	case !stdOwn && (IsStdlibKey(primary.TypeOrigin) || (builtinPrimitive(primary.TypeOrigin, typeName) && stdOnlyInterface(iface))):
		// A std or built-in type missing a std interface: the program
		// cannot derive or implement it, so the help says what to do
		// instead. Std, whose own demand it is, can.
		return e.WithHint(stdTypeHelp(iface, typeName, primary.TypeOrigin, nil))
	}
	return e.WithHint(missingImplHelp(iface, primary.IfaceTypeParams, primary.IfaceOrigin, shown, primary.TypeOrigin, recs))
}

// throughFile is name, declared by the file of origin, named through that
// file as a diagnostic names one of two declarations of a name
// (`users.User`, `b.Show`): a declaration inside a body through the file
// whose body declares it, and one of the entry or of no known file by its
// name alone.
func throughFile(origin, name string) string {
	file, _ := bodyOriginFile(origin)
	if file == OriginUnresolved || file == OriginEntry {
		return name
	}
	return lastSegment(file) + "." + name
}

// builtinPrimitive reports whether a demand for typeName of origin is for
// a built-in type with no declaration (`String`, `Unit`): no origin, and
// one of the built-in singletons' names, which a program cannot declare
// (checkReservedTypeName).
func builtinPrimitive(origin, typeName string) bool {
	if origin != OriginUnresolved {
		return false
	}
	for _, t := range []*PrimitiveType{TypeInt, TypeFloat, TypeDecimal, TypeString, TypeByte, TypeBytes, TypeUnit} {
		if t.Name_ == typeName {
			return true
		}
	}
	return false
}

// preludeInterfaces are the std interfaces the prelude puts in every file's
// scope, which a program cannot declare again (checkReservedTypeName).
var preludeInterfaces = map[string]bool{
	"Add": true, "Comparable": true, "Debug": true, "Discrete": true, "Display": true,
	"Divide": true, "Equatable": true, "Hashable": true, "Iter": true, "Multiply": true,
	"Steppable": true, "Struct": true, "Subtract": true,
}

// stdOnlyInterface reports whether iface, named bare in a missing-impl
// bucket, is certainly std's: a prelude interface, or an operator's, which
// only std declares. A program may implement an interface it declares for a
// built-in type, so any other name keeps the impl-block help.
func stdOnlyInterface(iface string) bool {
	if preludeInterfaces[iface] {
		return true
	}
	_, ok := operatorInterfaceByName(iface)
	return ok
}

// readerFormatted are the std types, by origin and name, that have no
// `Display` because their text is for a reader in a locale, with the call
// that writes it.
var readerFormatted = map[string]string{
	"std/phrase.Phrase": "Phrase.format(m, locale)",
	"std/phrase.Noun":   "Localize.localize(noun, locale)",
}

// stdTypeHelp is the help for typeName, a std type of origin that has no
// impl of iface, which only std could write. A type formatted for a reader
// says how to format it; any other gets stdOnlyHelp's.
func stdTypeHelp(iface, typeName, origin string, implementers []string) string {
	if iface == "Display" {
		if how, ok := readerFormatted[origin+"."+typeName]; ok {
			return fmt.Sprintf("a `%s` is formatted for a reader: `%s`", typeName, how)
		}
	}
	return stdOnlyHelp(typeName, iface, implementers)
}

// missingImplHelp is the help line for a type of origin that has no impl
// of iface (declared at ifaceOrigin with the type parameters params,
// Recording.IfaceTypeParams), for the demands recs: an operator
// interface's block shape, a derive when iface is the std interface derive
// writes (derivableInterface; a file's own ToJson is not), or an impl
// block (implBlockHelp). A type declared inside a body can have neither,
// so its help is bodyTypeHelp's. boundHelp is the call-site bound check's,
// which knows the interface's functions.
//
// For Equatable or Hashable, a declared type with equality meets either
// with no impl (structural_bounds.go), so the checker records such a
// demand only for a type that holds a value with none. A derive's demand
// says what that is when the derive found it (DeriveCtx.NoEquality); when
// it did not, the help claims nothing about the type's parts.
func missingImplHelp(iface, params, ifaceOrigin, typeName, origin string, recs []Recording) string {
	if IsBodyOrigin(origin) {
		return bodyTypeHelp(iface, typeName)
	}
	impl := implBlockHelp(withTypeParams(iface, params), typeName)
	if opIface, ok := operatorInterfaceByName(iface); ok {
		return fmt.Sprintf("%s with `fn %s(lhs: %s, rhs: Rhs) -> Out`", impl, opIface.Method, typeName)
	}
	if iface == "Equatable" || iface == "Hashable" {
		holds, checked := "", false
		for _, rec := range recs {
			if rec.Derive == nil {
				checked = true
			} else if holds == "" {
				holds = rec.Derive.NoEquality
			}
		}
		switch {
		case holds != "":
			return fmt.Sprintf("`%s` holds %s, which has no equality; %s that leaves it out", typeName, holds, impl)
		case checked:
			return fmt.Sprintf("`%s` holds a function, an Iter or a handle to running state, which has no equality; %s that leaves it out", typeName, impl)
		}
		return impl
	}
	if derivableInterface(&InterfaceType{Origin: ifaceOrigin, Name: iface}) {
		return fmt.Sprintf("add `derive %s` to `%s` or %s", iface, typeName, impl)
	}
	return impl
}

// implBlockHelp is the help that says to write an impl block of the
// interface iface, shown as a diagnostic names it with its type parameters
// or arguments (`Scale<N>`, `Scale<Int>`, `b.Show`), for typeName. Every
// help that offers the block says it through this one.
func implBlockHelp(iface, typeName string) string {
	return fmt.Sprintf("write an `impl %s for %s` block", iface, typeName)
}

// withTypeParams is the interface iface named with params, the type
// parameters its declaration lists (Recording.IfaceTypeParams): `Scale<N>`,
// or iface alone when it has none.
func withTypeParams(iface, params string) string {
	if params == "" {
		return iface
	}
	return iface + "<" + params + ">"
}

// bodyInterfaceHelp is the help for typeName, which has no impl of iface,
// an interface declared inside a body. An impl stands only at module
// level, where no header can name a body's interface (BodyOrigin), so the
// fix is to declare the interface at module level.
func bodyInterfaceHelp(iface, typeName string) string {
	return fmt.Sprintf("`%s` is declared inside a body, where no impl can name it; declare it at module level to %s", iface, implBlockHelp(iface, typeName))
}

// bodyTypeHelp is the help for typeName, a type declared inside a body,
// that has no impl of iface. An impl and a derive stand only at module
// level, and no impl can name a body's type (BodyOrigin), so the fix is to
// declare the type at module level.
func bodyTypeHelp(iface, typeName string) string {
	return fmt.Sprintf("`%s` is declared inside a body, where it can have no impl or derive; declare it at module level to give it an impl of `%s`", typeName, iface)
}

// boundHelp is the help line for concrete, which does not implement bound.
// The orphan rule allows `impl bound for concrete` only where the program
// declares one of the two:
//
//   - it declares concrete: `derive` when bound is derivable, and an impl
//     block with bound's own required functions at concrete, unless
//     concrete is declared inside a body (bodyTypeHelp);
//   - it declares bound only: the impl block;
//   - it declares neither (`Unit` where std's `Hashable` is required): that
//     only std can write the impl, and what the author can do instead
//     (stdOnlyHelp).
//
// A bound declared inside a body has no impl at all (bodyInterfaceHelp).
// An operator interface keeps missingImplHelp's block shape.
func (c *checker) boundHelp(concrete Type, bound *InterfaceType) string {
	if sub := c.refinementMissing(concrete, bound); sub != nil {
		return refinementMakesBaseHelp(sub)
	}
	if help, ok := c.refinementReadByKindHelp(concrete, bound); ok {
		return help
	}
	name := fmt.Sprint(concrete)
	if bound.Name == "Display" {
		if _, fn := resolveTypeVar(concrete).(*FuncType); fn {
			return "a function value has no `Display`"
		}
		if parts, ok := displayParts(concrete); ok {
			// A tuple or anonymous struct is Display when every part is, so
			// the help is the first part's.
			for _, p := range parts {
				if !typeSatisfiesInterfaceForImplMatch(p, bound, c.implsContext(), c.implTypeArgsContext(), nil) {
					return fmt.Sprintf("`%s` is Display when each part is, and `%s` is not: %s", name, p, c.boundHelp(p, bound))
				}
			}
		}
	}
	switch bound.Name {
	case "Equatable", "Hashable", "Comparable":
		if part, none := c.noEqualityPart(concrete, map[Type]bool{}); none {
			if part == resolveTypeVar(concrete) {
				return fmt.Sprintf("%s has no equality", noEqualityWhat(part))
			}
			return fmt.Sprintf("`%s` holds `%s`, and %s has no equality", name, part, noEqualityWhat(part))
		}
	}
	if help, ok := c.structuralBoundHelp(concrete, bound); ok {
		return help
	}
	if part := c.partLackingBound(concrete, bound); part != nil {
		// Its impl asks bound of a part that lacks it, so the help is the
		// part's: an `Option<Foo>` is Display only where Foo is.
		return fmt.Sprintf("`%s` is %s when `%s` is, and `%s` is not: %s", name, bound.Name, part, part, c.boundHelp(part, bound))
	}
	ownsType := programDeclares(concrete)
	origin, _ := nominalOrigin(resolveTypeVar(concrete))
	if !ownsType && IsStdlibKey(bound.Origin) {
		return stdTypeHelp(bound.Name, name, origin, c.implementersOf(bound.Name))
	}
	if IsBodyOrigin(bound.Origin) {
		return bodyInterfaceHelp(bound.Name, name)
	}
	if _, ok := operatorInterfaceByName(bound.Name); ok || IsBodyOrigin(origin) {
		return missingImplHelp(bound.Name, strings.Join(bound.TypeParams, ", "), bound.Origin, name, origin, nil)
	}
	impl := implBlockHelp(c.shownInterfaceFor(concrete, bound, true), name)
	if sigs := c.requiredSignatures(bound, concrete); len(sigs) > 0 {
		impl += " with `" + strings.Join(sigs, "` and `") + "`"
	}
	if ownsType && derivableInterface(bound) {
		return fmt.Sprintf("add `derive %s` to `%s` or %s", c.shownInterfaceFor(concrete, bound, false), name, impl)
	}
	return impl
}

// programDeclares reports whether t is a struct, enum or distinct type the
// program declares, so it may derive or implement a std interface.
func programDeclares(t Type) bool {
	switch t := resolveTypeVar(t).(type) {
	case *StructType:
		return !IsStdlibKey(t.Origin)
	case *EnumType:
		return !IsStdlibKey(t.Origin)
	case *DistinctType:
		return !IsStdlibKey(t.Origin)
	}
	return false
}

// implementersOf answers the types with an impl of the interface named
// iface that this file can see, sorted.
func (c *checker) implementersOf(iface string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tables := range c.implsContext() {
		for typeName, ifaces := range tables.Impls {
			if ifaces[iface] && !seen[typeName] {
				seen[typeName] = true
				out = append(out, typeName)
			}
		}
	}
	sort.Strings(out)
	return out
}

// mostImplementersNamed is how many implementing types stdOnlyHelp names.
// Up to this many, the interface is a narrow one (`Matcher`: `Regex` and
// `String`) and its implementers are what the call can be given instead, so
// naming them is the fix. Past it, the interface is one most std types
// implement (`Hashable`, `Display`: more than thirty each). The author
// already knows the common ones and no list says which to pick, so the help
// says to wrap the type instead.
const mostImplementersNamed = 4

// stdOnlyHelp is the help for typeName, which the program does not declare,
// missing iface, a std interface: only std can write that impl. A narrow
// interface names its implementers. A broad one, or one with none, gets the
// fix that always applies: a type the program declares may implement iface.
func stdOnlyHelp(typeName, iface string, implementers []string) string {
	head := fmt.Sprintf("only std can implement `%s` for `%s`", iface, typeName)
	if n := len(implementers); n > 0 && n <= mostImplementersNamed {
		return head + "; " + implementerList(implementers)
	}
	return head + fmt.Sprintf("; wrap it in a type of your own that implements `%s`", iface)
}

// implementerList is "`Regex` and `String` implement it".
func implementerList(types []string) string {
	quoted := make([]string, len(types))
	for i, t := range types {
		quoted[i] = "`" + t + "`"
	}
	if len(quoted) == 1 {
		return "only " + quoted[0] + " implements it"
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1] + " implement it"
}

// requiredSignatures is each function an impl of bound for concrete must
// write, as `fn name(param: Type) -> Result` with `self` read as concrete.
// A function with a default is left out.
func (c *checker) requiredSignatures(bound *InterfaceType, concrete Type) []string {
	it := bound
	if len(it.Methods) == 0 {
		if full := lookupInterfaceType(c.fa, c.reg, bound.Name); full != nil {
			it = full
		}
	}
	subs := map[*TypeParam_]Type{}
	if it.SelfParam != nil {
		subs[it.SelfParam] = concrete
	}
	if len(bound.TypeArgs) == len(it.TypeParamDefs) {
		for i, tp := range it.TypeParamDefs {
			subs[tp] = bound.TypeArgs[i]
		}
	}
	var out []string
	for _, m := range it.Methods {
		if m.HasDefault || m.Extern {
			continue
		}
		params := make([]string, len(m.Params))
		for i, pt := range m.Params {
			pname := fmt.Sprintf("p%d", i+1)
			if i < len(m.ParamNames) && m.ParamNames[i] != "" {
				pname = m.ParamNames[i]
			}
			params[i] = pname + ": " + fmt.Sprint(Substitute(pt, subs))
		}
		sig := "fn " + m.Name + "(" + strings.Join(params, ", ") + ")"
		if m.Return != nil && m.Return != TypeUnit {
			sig += " -> " + fmt.Sprint(Substitute(m.Return, subs))
		}
		out = append(out, sig)
	}
	return out
}

// callCol is where a diagnostic about call n points: the callee as written
// (`Option.hash` in `Option.hash(x)`), or the call when it names none.
func callCol(n *ast.Call) int {
	switch f := n.Func.(type) {
	case *ast.FieldAccess:
		switch o := f.Object.(type) {
		case *ast.TypeIdent:
			if o.Col > 0 {
				return o.Col
			}
		case *ast.Ident:
			if o.Col > 0 {
				return o.Col
			}
		}
		if f.Col > 0 {
			return f.Col
		}
	case *ast.Ident:
		if f.Col > 0 {
			return f.Col
		}
	}
	return n.Col
}

// callBoundError is boundError for a call whose demands are recorded at
// demand. When the bound error is reported, the call's own demands for the
// same type and interface are dropped from the file's manifest: they would
// otherwise reach DetectMissingImpls and report the one missing impl a
// second time. `List.contains(points, p)` with no `Equatable` for Point
// records (Equatable, Point) through `impl Contains for List<T> where T:
// Equatable` before the bound check reports it.
func (c *checker) callBoundError(demand Pos, line, col int, concrete Type, bound *InterfaceType, param string, over ...ast.Node) {
	if c.boundError(line, col, concrete, bound, param, over...) {
		c.dropDemandsAt(concrete, bound.Name, demand)
	}
}

// dropDemandsAt removes the file's recordings at pos that demand ifaceName
// of concrete.
func (c *checker) dropDemandsAt(concrete Type, ifaceName string, pos Pos) {
	if c == nil || c.fa == nil {
		return
	}
	name := concreteTypeName(resolveTypeVar(concrete))
	recs := c.fa.ImplManifest[ifaceName][name]
	if len(recs) == 0 {
		return
	}
	var kept []Recording
	for _, rec := range recs {
		if rec.Pos != pos {
			kept = append(kept, rec)
		}
	}
	if len(kept) == 0 {
		delete(c.fa.ImplManifest[ifaceName], name)
		return
	}
	c.fa.ImplManifest[ifaceName][name] = kept
}

// boundError is a call's unmet interface bound: at the call, naming the
// bound it violates, with the missing-impl help. It reports whether it
// reported an error; a bound an impl's own bounds leave unmet is recorded
// as their demand instead. A type with a part of no type draws none
// (hasPartOfNoType): the part's error is the one error, as it is for a
// demand of the type (demandOfNoType).
func (c *checker) boundError(line, col int, concrete Type, bound *InterfaceType, param string, over ...ast.Node) bool {
	if hasPartOfNoType(concrete) {
		return false
	}
	if sub := c.refinementMissing(concrete, bound); sub != nil {
		required := fmt.Sprintf("`where %s: %s`", param, c.shownInterfaceFor(concrete, bound, true))
		if !TypesIdentical(sub, concrete) {
			required += fmt.Sprintf(" at %s", concrete)
		}
		c.report(refinementMakesBaseError(line, col, sub, bound, required))
		return true
	}
	if c.boundsMissOf(concrete, bound) != missNotByBounds {
		c.recordBoundConformance(concrete, bound.Name, bound.Origin, c.site(line, col, RecordingKindGenericBoundCheck))
		return false
	}
	end := TypeError{}
	for _, n := range over {
		if sp, ok := spanOf(n); ok && sp.StartLine == line && sp.StartCol == col {
			end.EndLine, end.EndCol = sp.EndLine, sp.EndCol
		}
	}
	if containsTypeVar(concrete) {
		// `v = Ok(1)` then `io.print(v)`: nothing names Ok's error type, so
		// whether the Result has the bound is not known yet, and no impl
		// is missing. The answer is an annotation.
		c.report(TypeError{Line: line, Col: col, EndLine: end.EndLine, EndCol: end.EndCol, Message: c.typef(
			"%s needs %s (required by `where %s: %s`), and nothing names the type its `_` stands for",
			concrete, boundAt(bound, param, concrete), param, bound)}.WithHint(
			"annotate the binding this value comes from with its whole type, as in `r: Result<Int, String> = Ok(1)`, or a lambda's parameters and result, as in `f: (Int) -> Result<Int, String> = fn x -> Ok(x)`"))
		return true
	}
	e := TypeError{Line: line, Col: col, EndLine: end.EndLine, EndCol: end.EndCol, Message: fmt.Sprintf(
		"%s does not implement %s (required by `where %s: %s`)",
		concrete, c.shownInterfaceFor(concrete, boundAt(bound, param, concrete), true), param, c.shownInterfaceFor(concrete, bound, true))}
	if why, ok := c.withConstructionReason(e, concrete, bound); ok {
		c.report(why)
		return true
	}
	c.report(e.WithHint(c.boundHelp(concrete, bound)))
	return true
}

// boundAt is bound with the bounded parameter param read as concrete, as
// the call needs it implemented: `where T: Add<T, T>` at Bool is
// `Add<Bool, Bool>`. Another parameter the bound names stays as written.
func boundAt(bound *InterfaceType, param string, concrete Type) *InterfaceType {
	subs := map[*TypeParam_]Type{}
	for _, a := range bound.TypeArgs {
		if tp, ok := a.(*TypeParam_); ok && tp.Name_ == param {
			subs[tp] = concrete
		}
	}
	if len(subs) == 0 {
		return bound
	}
	args := make([]Type, len(bound.TypeArgs))
	for i, a := range bound.TypeArgs {
		args[i] = Substitute(a, subs)
	}
	return &InterfaceType{Name: bound.Name, TypeArgs: args}
}

// requiredClause is what a missing-impl report says made the demand rec
// records, in the program's terms: "required by parameter `x` of
// `shown`", "required to interpolate it", "required via `derive Display
// for Wrapper`, field `u`". The one rendering of a RecordingKind;
// TestEveryRecordingKindSaysWhatRequiredIt holds it to every kind.
func requiredClause(rec Recording) string {
	return "required " + requiredBy(rec)
}

// requiredBy is requiredClause after its "required ".
func requiredBy(rec Recording) string {
	switch rec.Kind {
	case RecordingKindCallSite:
		switch {
		case rec.Callee != "":
			return fmt.Sprintf("by the call `%s`", rec.Callee)
		case rec.Tag != "":
			return fmt.Sprintf("by this `%s` literal", rec.Tag)
		}
		return "via call site"
	case RecordingKindInterpolation:
		return "to interpolate it"
	case RecordingKindArgument:
		if rec.Param != "" {
			return fmt.Sprintf("by parameter `%s` of `%s`", rec.Param, rec.Callee)
		}
		return fmt.Sprintf("by argument %d of `%s`", rec.Arg, rec.Callee)
	case RecordingKindTypedLiteralSlot:
		if rec.Tag != "" {
			return fmt.Sprintf("by this `%s` slot", rec.Tag)
		}
		return "by a typed literal's slot"
	case RecordingKindGenericBoundCheck:
		return "by a `where` bound"
	case RecordingKindInterfaceTypedParam:
		return "by the interface type declared for it"
	case RecordingKindDeriveSynth:
		if rec.Derive != nil {
			if rec.Derive.FieldName != "" {
				return fmt.Sprintf("via `derive %s for %s`, field `%s`",
					rec.Derive.Iface, rec.Derive.TypeName, rec.Derive.FieldName)
			}
			return fmt.Sprintf("via `derive %s for %s`",
				rec.Derive.Iface, rec.Derive.TypeName)
		}
		return "via a derive"
	case RecordingKindDerivePayload:
		if rec.Derive != nil {
			return fmt.Sprintf("via `derive %s for %s` (an interpolated payload)",
				rec.Derive.Iface, rec.Derive.TypeName)
		}
		return "via a derive's interpolated payload"
	case RecordingKindEqualityOperator:
		if rec.Op != "" {
			return fmt.Sprintf("via equality operator `%s`", rec.Op)
		}
		return "via equality operator"
	case RecordingKindOrderingOperator:
		if rec.Op != "" {
			return fmt.Sprintf("via ordering operator `%s`", rec.Op)
		}
		return "via ordering operator"
	case RecordingKindOperator:
		if rec.Op == "in" {
			return "via membership test `in`"
		}
		if rec.Op != "" {
			if opIface, ok := operatorInterfaceForOp(rec.Op); ok {
				return fmt.Sprintf("via %s operator `%s`", opIface.Verb, rec.Op)
			}
			return fmt.Sprintf("via operator `%s`", rec.Op)
		}
		return "via operator"
	default:
		return "via an unknown site"
	}
}

// implBoundNote says which impl's where bound asked the demand rec
// records, when one did (Recording.Bound): "`Measure<P>`'s `Display` impl
// requires `P: Unit2`". "" for a demand the site made itself.
func implBoundNote(rec Recording) string {
	b := rec.Bound
	if b.Iface == "" {
		return ""
	}
	return fmt.Sprintf("`%s`'s `%s` impl requires `%s: %s`", b.Receiver, b.Iface, b.Param, b.Bound)
}

// boundsMiss is how a type misses an interface by the where bounds of its
// impl of it (boundsMissOf). It decides which error the miss draws: the
// position's own, which names the whole value and what the position takes,
// or the demand of the bound left unmet, whose missing-impl report names the
// type that lacks it.
type boundsMiss int

const (
	// missNotByBounds: the type has no impl of the interface that matches
	// it, or misses it at a part the demand would not report by name. The
	// position's own error is the one error.
	missNotByBounds boundsMiss = iota
	// missAtOwnParts: the impl asks the interface itself of a part that
	// lacks it. `impl ToHtml for Option<T> where T: ToHtml` misses at an
	// `Option<Bool>` because Bool has no ToHtml, and std's derived Display
	// of Option misses at an `Option<Html>` because Html has no Display.
	missAtOwnParts
	// missByAnotherBound: every miss ends at a where bound of another
	// interface, at the type or at a part the impl asks the interface of:
	// `impl Display for Measure<P> where P: Unit2` at a `Measure<Bare>`, and
	// at a `List<Measure<Bare>>`.
	missByAnotherBound
)

// boundsMissOf is how concrete misses bound, the interface a position
// takes, by the where bounds of its impl of bound. It is the one answer to
// which error the miss draws, and each position reads it:
//
//   - A where bound (boundError) records the demand for any miss by bounds,
//     so the missing-impl report names the part that lacks the interface, as
//     an interpolation's demand does: `List<Foo>` has Display only where Foo
//     does.
//   - A position that takes a value of an interface type (a parameter or a
//     field, ifaceParamAdmits and reportGenericArgMismatch, and a typed
//     literal's slot, checkTaggedStringSlot) records it only for
//     missByAnotherBound: the impl the value's type has is unmet at this
//     instance, and the demand names the bound. A part that lacks the
//     interface itself is the position's own error, which names the whole
//     value and what the position takes, as an `Html` template's slot says
//     what its context shows.
//
// A type that holds a function, an Iter or a handle is the bound's own
// error under Equatable and Hashable, whose help names what it holds.
func (c *checker) boundsMissOf(concrete Type, bound *InterfaceType) boundsMiss {
	if (bound.Name == "Equatable" || bound.Name == "Hashable") && c.holdsNoEqualityOutsideDeclaredTypes(concrete) {
		return missNotByBounds
	}
	return c.implBoundsMiss(concrete, bound, bound)
}

// unmetBound is a where bound of an impl, at the type argument it binds,
// that the argument does not meet.
type unmetBound struct {
	arg   Type
	bound *InterfaceType
}

// unmetImplBounds is the where bounds of concrete's impl of bound
// (`impl Display for List<T> where T: Display`) that concrete's type
// arguments leave unmet, ordered by the name of the parameter each binds.
// ok is false where concrete's type has no impl of bound whose receiver it
// matches.
func (c *checker) unmetImplBounds(concrete Type, bound *InterfaceType) (unmet []unmetBound, ok bool) {
	if c == nil || bound == nil {
		return nil, false
	}
	name := interfaceImplName(concrete)
	if name == "" {
		return nil, false
	}
	info := lookupImplTypeArgs(name, bound.Name, c.implTypeArgsContext())
	if info == nil || info.Receiver == nil {
		return nil, false
	}
	subs := map[*TypeParam_]Type{}
	if unifyFull(info.Receiver, concrete, subs, c.implsContext(), c.implTypeArgsContext()) != nil {
		return nil, false
	}
	params := make([]*TypeParam_, 0, len(subs))
	for tp := range subs {
		params = append(params, tp)
	}
	sort.Slice(params, func(i, j int) bool { return params[i].Name_ < params[j].Name_ })
	for _, tp := range params {
		for _, b := range tp.Bounds {
			if !typeSatisfiesInterfaceForImplMatch(subs[tp], b, c.implsContext(), c.implTypeArgsContext(), nil) {
				unmet = append(unmet, unmetBound{arg: subs[tp], bound: b})
			}
		}
	}
	return unmet, true
}

// implBoundsMiss is boundsMissOf for concrete, which misses bound, at a
// position that takes taken: concrete's impl of bound matches it but for
// where bounds, and each argument that misses one is reported where its
// demand is recorded (unmetDemandMiss).
func (c *checker) implBoundsMiss(concrete Type, bound, taken *InterfaceType) boundsMiss {
	unmet, ok := c.unmetImplBounds(concrete, bound)
	if !ok {
		return missNotByBounds
	}
	miss := missNotByBounds
	for _, u := range unmet {
		m := c.unmetDemandMiss(u.arg, u.bound, taken)
		if m == missNotByBounds {
			return missNotByBounds
		}
		if m == missAtOwnParts || miss == missNotByBounds {
			miss = m
		}
	}
	return miss
}

// unmetDemandMiss is how t, which misses bound, misses it at a position
// that takes taken, where the demand that t implement bound is reported
// where it is recorded: t is a named type; or for Equatable and Hashable a
// type that holds a function, an Iter or a handle outside a declared type's
// fields, which recordConformanceRecording reports (demandWithoutEquality);
// or a tuple or anonymous struct whose parts that miss bound are, as
// recordConformanceRecording splits an Equatable, Hashable or Display
// demand (and a tuple's Comparable one) into its parts'. A bound of another
// interface than taken is missByAnotherBound. taken itself is
// missByAnotherBound only where t misses it so in turn (implBoundsMiss), and
// otherwise missAtOwnParts: t lacks the interface the position takes.
func (c *checker) unmetDemandMiss(t Type, bound, taken *InterfaceType) boundsMiss {
	resolved := resolveTypeVar(t)
	own := sameNominalIdentity(bound.Origin, bound.Name, taken.Origin, taken.Name)
	if concreteTypeName(resolved) != "" {
		if own && c.implBoundsMiss(resolved, bound, taken) != missByAnotherBound {
			return missAtOwnParts
		}
		return missByAnotherBound
	}
	if (bound.Name == "Equatable" || bound.Name == "Hashable") && c.holdsNoEqualityOutsideDeclaredTypes(resolved) {
		if own {
			return missAtOwnParts
		}
		return missByAnotherBound
	}
	var parts []Type
	switch bound.Name {
	case "Equatable", "Hashable", "Display":
		ps, ok := displayParts(resolved)
		if !ok {
			return missNotByBounds
		}
		parts = ps
	case "Comparable":
		tup, ok := resolved.(*TupleType)
		if !ok {
			return missNotByBounds
		}
		parts = tup.Elems
	default:
		return missNotByBounds
	}
	miss := missNotByBounds
	for _, p := range parts {
		if typeSatisfiesInterfaceForImplMatch(p, bound, c.implsContext(), c.implTypeArgsContext(), nil) {
			continue
		}
		m := c.unmetDemandMiss(p, bound, taken)
		if m == missNotByBounds {
			return missNotByBounds
		}
		if m == missAtOwnParts || miss == missNotByBounds {
			miss = m
		}
	}
	return miss
}

// partLackingBound is the type argument of which concrete's impl of bound
// asks bound itself and which lacks it (missAtOwnParts), for the help that
// names it: an `Option<Foo>` is Display only where Foo is. Nil where no
// unmet where bound of the impl names bound.
func (c *checker) partLackingBound(concrete Type, bound *InterfaceType) Type {
	unmet, _ := c.unmetImplBounds(concrete, bound)
	for _, u := range unmet {
		if sameNominalIdentity(u.bound.Origin, u.bound.Name, bound.Origin, bound.Name) {
			return u.arg
		}
	}
	return nil
}
