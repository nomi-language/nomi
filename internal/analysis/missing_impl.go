package analysis

import (
	"fmt"
	"maps"
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
// ImplManifests demand and no impl of program p supplies (DetectMissingImpls).
// It reads only facts each file holds about itself, folded here: the impl
// index's supply, every file's own Impls, and each demander's own demands.
// Nil when p has no impl index.
//
// The supply is the index's, plus any file's own impls the index does not
// hold (a module a function body's import loaded after the index was built).
// Those pairs have no identity entry — PopulateImplsByIdentity ran over the
// index — so they fall back to the bare-name answer; leaving them covered
// would read a real impl as another module's.
func MissingImpls(p *Program, demanders []*FileAnalysis) []TypeError {
	if p == nil || p.ImplIndex == nil {
		return nil
	}
	idx := p.ImplIndex
	demands := unionDemandsOver(demanders, p.stdBase)
	// DetectMissingImpls reads the supply of the demanded types only, so
	// only theirs is copied. The covered set is COPIED, not shared.
	// Uncovering below is per-call state, and the index it comes from is
	// held by every file of the program.
	impls := map[string]map[string]bool{}
	covered := map[string]map[string]bool{}
	for _, byType := range demands {
		for t := range byType {
			if _, done := impls[t]; !done {
				impls[t] = maps.Clone(idx.Impls[t])
				covered[t] = maps.Clone(idx.implNamesWithIdentity[t])
			}
		}
	}
	// The identity index travels with the snapshot, or DetectMissingImpls'
	// supply check has nothing to consult and every bare-name hit stands.
	snapshot := &ProjectImplIndex{
		Impls:                 impls,
		ImplManifest:          demands,
		ImplsByIdentity:       idx.ImplsByIdentity,
		implNamesWithIdentity: covered,
	}
	for _, file := range p.Files() {
		// An index laid over the stdlib's base holds every impl of the
		// base's files.
		if p.stdBase.Owns(file) {
			continue
		}
		noteStdlibWork(file.Origin)
		for t, ifaces := range file.Impls {
			if _, demanded := impls[t]; !demanded {
				continue
			}
			if impls[t] == nil {
				impls[t] = make(map[string]bool, len(ifaces))
			}
			for i := range ifaces {
				if !idx.Impls[t][i] {
					snapshot.UncoverImplIdentity(t, i)
				}
				impls[t][i] = true
			}
		}
	}
	return DetectMissingImpls(snapshot, nil)
}

// unionDemands answers the union of files' own ImplManifests, keeping only
// the (interface, type) buckets some recording outside the stdlib demands:
// DetectMissingImpls reports nothing for a bucket every recording of which
// is the stdlib's, so the stdlib's demands matter only beside a program's.
// Each bucket's recordings are deduplicated and sorted by position, so the
// order does not depend on the order the files were checked or given in.
func unionDemands(files []*FileAnalysis) map[string]map[string][]Recording {
	return unionDemandsOver(files, nil)
}

// unionDemandsOver is unionDemands that takes the demands of base's files
// from base when files holds every one of them.
func unionDemandsOver(files []*FileAnalysis, base *StdBase) map[string]map[string][]Recording {
	if !base.ownsAll(files) {
		base = nil
	}
	type bucket = implPair
	var wanted []bucket
	seen := map[bucket]bool{}
	if base != nil {
		for _, b := range base.wanted {
			seen[b] = true
			wanted = append(wanted, b)
		}
	}
	for _, file := range files {
		if base.Owns(file) {
			continue
		}
		noteStdlibWork(file.Origin)
		if isStdlibRecording(Recording{Pos: Pos{File: file.FilePath}}) {
			// A file's checker records at the file's own path.
			continue
		}
		for iface, byType := range file.ImplManifest {
			for typeName, recs := range byType {
				b := bucket{typeName: typeName, iface: iface}
				if seen[b] {
					continue
				}
				for _, rec := range recs {
					if !isStdlibRecording(rec) {
						seen[b] = true
						wanted = append(wanted, b)
						break
					}
				}
			}
		}
	}
	out := make(map[string]map[string][]Recording)
	for _, b := range wanted {
		var recs []Recording
		dup := map[Recording]bool{}
		add := func(from []Recording) {
			for _, rec := range from {
				if !dup[rec] {
					dup[rec] = true
					recs = append(recs, rec)
				}
			}
		}
		for _, file := range files {
			if !base.Owns(file) {
				add(file.ImplManifest[b.iface][b.typeName])
			}
		}
		if base != nil {
			add(base.demands[b.iface][b.typeName])
		}
		sortRecordings(recs)
		if out[b.iface] == nil {
			out[b.iface] = make(map[string][]Recording)
		}
		out[b.iface][b.typeName] = recs
	}
	return out
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
// Stdlib hands-off rule: if every Recording for a pair has a stdlib
// Pos.File, the pair is suppressed. The stdlib's own (Iface, T) coverage
// is pinned by stdlib load/tests — emitting a "missing impl" at every
// user build for a stdlib-internal demand would surface the same issue
// at every site rather than once at the stdlib coverage test.
// The rule suppresses based on the recording side alone. Tightening it
// with a "T is a stdlib type" check needs a typeDecls input this
// function does not have. The false negative it allows (a stdlib call
// site demanding a user-defined T) is unusual and benign.
//
// Primary error site: the first user-project Recording (non-stdlib
// Pos.File), falling back to the first Recording overall if all are
// stdlib (only reachable when the suppression rule above is bypassed
// by some yet-unmodeled case; defensive). Remaining Recordings are
// inlined as `also required at ...` notes in the Message string —
// structured multi-note TypeError rendering is a later task. DeriveCtx
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
	// Same posture as detectImplCollisions / detectOrphanImpls.
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
			if e, ok := idx.missingImpl(iface, typeName, byType[typeName], typeDecls); ok {
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
	if idx.Impls != nil {
		if ifaces, ok := idx.Impls[typeName]; ok && ifaces[iface] {
			recs = idx.unsuppliedRecordings(recs, typeName, iface)
			if len(recs) == 0 {
				return TypeError{}, false
			}
		}
	}
	// Stdlib hands-off: every Recording is stdlib-internal.
	allStdlib := true
	for _, rec := range recs {
		if !isStdlibRecording(rec) {
			allStdlib = false
			break
		}
	}
	if allStdlib {
		return TypeError{}, false
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

	return missingImplError(iface, typeName, recs, typeDecls), true
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
// inlines the other Recordings as `also required at` notes, and
// appends a help line suggesting either derive or a hand-written impl block.
func missingImplError(iface, typeName string, recs []Recording, typeDecls map[string]Pos) TypeError {
	// Primary: first user-project Recording, fallback to recs[0].
	primaryIdx := 0
	for i, rec := range recs {
		if !isStdlibRecording(rec) {
			primaryIdx = i
			break
		}
	}
	primary := recs[primaryIdx]

	var b strings.Builder
	fmt.Fprintf(&b, "no impl of `%s` for `%s` (required at %s via %s)",
		iface, typeName,
		formatRecordingLoc(primary), formatRecordingVia(primary))

	e := TypeError{
		Line:    primary.Pos.Line,
		Col:     primary.Pos.Col,
		Message: b.String(),
		File:    primary.Pos.File,
	}

	// Every other Recording is a related location.
	for i, rec := range recs {
		if i == primaryIdx {
			continue
		}
		e = e.WithRelated(RelatedInfo{File: rec.Pos.File, Line: rec.Pos.Line, Col: rec.Pos.Col,
			Message: "also required here via " + formatRecordingVia(rec)})
	}

	if typeDecls != nil {
		if decl, ok := typeDecls[typeName]; ok {
			e = e.WithRelated(nameRelated(decl.File, decl, typeName, "`"+typeName+"` is declared here"))
		}
	}

	return e.WithHint(missingImplHelp(iface, typeName))
}

// missingImplHelp is the help line for a type that has no impl of iface,
// from names alone (DetectMissingImpls): an operator interface's block
// shape, a derive when iface is derivable, or an impl block. boundHelp is
// the call-site bound check's, which knows the interface's functions.
func missingImplHelp(iface, typeName string) string {
	if opIface, ok := operatorInterfaceByName(iface); ok {
		return fmt.Sprintf("write an `impl %s<Rhs, Out> for %s { fn %s(lhs: %s, rhs: Rhs): Out { ... } }` block",
			opIface.Interface, typeName, opIface.Method, typeName)
	}
	if deriveSupported[iface] {
		return fmt.Sprintf("add `derive %s` to `%s` or write an `impl %s for %s` block", iface, typeName, iface, typeName)
	}
	return fmt.Sprintf("write an `impl %s for %s` block", iface, typeName)
}

// boundHelp is the help line for concrete, which does not implement bound.
// The orphan rule allows `impl bound for concrete` only where the program
// declares one of the two:
//
//   - it declares concrete: `derive` when bound is derivable, and an impl
//     block with bound's own required functions at concrete;
//   - it declares bound only: the impl block;
//   - it declares neither (`Unit` where std's `Hashable` is required): that
//     only std can write the impl, and what the author can do instead
//     (stdOnlyHelp).
//
// An operator interface keeps missingImplHelp's block shape.
func (c *checker) boundHelp(concrete Type, bound *InterfaceType) string {
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
	if _, ok := operatorInterfaceByName(bound.Name); ok {
		return missingImplHelp(bound.Name, name)
	}
	ownsType := programDeclares(concrete)
	if !ownsType && IsStdlibKey(bound.Origin) {
		return stdOnlyHelp(name, bound.Name, c.implementersOf(bound.Name))
	}
	impl := fmt.Sprintf("write an `impl %s for %s` block", bound, name)
	if sigs := c.requiredSignatures(bound, concrete); len(sigs) > 0 {
		impl += " with `" + strings.Join(sigs, "` and `") + "`"
	}
	if ownsType && deriveSupported[bound.Name] {
		return fmt.Sprintf("add `derive %s` to `%s` or %s", bound.Name, name, impl)
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
// write, as `fn name(param: Type): Result` with `self` read as concrete.
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
			sig += ": " + fmt.Sprint(Substitute(m.Return, subs))
		}
		out = append(out, sig)
	}
	return out
}

// callCol is where a diagnostic about call n points: the callee as written
// (`Maybe.hash` in `Maybe.hash(x)`), or the call when it names none.
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

// pipeBoundCol is callCol for a piped call's stage, or the pipe itself when
// the stage is not a call.
func pipeBoundCol(n *ast.Binary, stage *ast.Call) int {
	if stage != nil {
		return callCol(stage)
	}
	return n.Col
}

// boundError is a call's unmet interface bound: at the call, naming the
// bound it violates, with the missing-impl help.
func (c *checker) boundError(line, col int, concrete Type, bound *InterfaceType, param string, over ...ast.Node) {
	if c.implBoundsUnmetByNamedTypes(concrete, bound) {
		// `List<Foo>` has Display only where Foo does: the demand is Foo's,
		// and the missing-impl report names Foo with its own help.
		c.recordBoundConformance(concrete, bound.Name, c.recPos(line, col), RecordingKindGenericBoundCheck)
		return
	}
	end := TypeError{}
	for _, n := range over {
		if sp, ok := spanOf(n); ok && sp.StartLine == line && sp.StartCol == col {
			end.EndLine, end.EndCol = sp.EndLine, sp.EndCol
		}
	}
	c.report(TypeError{Line: line, Col: col, EndLine: end.EndLine, EndCol: end.EndCol, Message: fmt.Sprintf(
		"%s does not implement %s (required by `where %s: %s`)",
		concrete, bound.Name, param, bound.Name)}.WithHint(c.boundHelp(concrete, bound)))
}

// formatRecordingLoc renders "file:line:col" with an "<unknown>"
// fallback for the empty-File case (single-file BuildFileWithStdlib
// recorders).
func formatRecordingLoc(rec Recording) string {
	file := rec.Pos.File
	if file == "" {
		file = "<unknown>"
	}
	return fmt.Sprintf("%s:%d:%d", file, rec.Pos.Line, rec.Pos.Col)
}

// formatRecordingVia renders the demand-site kind in user-relevant
// terms. For derive-sourced Recordings, unpacks the DeriveCtx into the
// "via" clause naming the @derive interface, the type, and (when known)
// the offending field.
func formatRecordingVia(rec Recording) string {
	switch rec.Kind {
	case RecordingKindCallSite:
		return "call site"
	case RecordingKindTypedLiteralSlot:
		return "typed-literal slot"
	case RecordingKindGenericBoundCheck:
		return "generic where bound check"
	case RecordingKindInterfaceTypedParam:
		return "interface-typed param/field"
	case RecordingKindDeriveSynth:
		if rec.Derive != nil {
			if rec.Derive.FieldName != "" {
				return fmt.Sprintf("@derive(%s) on %s field %s",
					rec.Derive.Iface, rec.Derive.TypeName, rec.Derive.FieldName)
			}
			return fmt.Sprintf("@derive(%s) on %s",
				rec.Derive.Iface, rec.Derive.TypeName)
		}
		return "@derive synth"
	case RecordingKindDerivePayload:
		if rec.Derive != nil {
			return fmt.Sprintf("@derive(%s) on %s (interpolated payload)",
				rec.Derive.Iface, rec.Derive.TypeName)
		}
		return "@derive payload"
	case RecordingKindEqualityOperator:
		if rec.Op != "" {
			return fmt.Sprintf("equality operator `%s`", rec.Op)
		}
		return "equality operator"
	case RecordingKindOrderingOperator:
		if rec.Op != "" {
			return fmt.Sprintf("ordering operator `%s`", rec.Op)
		}
		return "ordering operator"
	case RecordingKindOperator:
		if rec.Op != "" {
			if opIface, ok := operatorInterfaceForOp(rec.Op); ok {
				return fmt.Sprintf("%s operator `%s`", opIface.Verb, rec.Op)
			}
			return fmt.Sprintf("operator `%s`", rec.Op)
		}
		return "operator"
	default:
		return "unknown"
	}
}

// implBoundsUnmetByNamedTypes reports whether concrete's type has an impl of
// bound (`impl Display for List<T> where T: Display`) whose receiver it
// matches but for a bound on the impl's type parameters, and each type
// argument that misses one is a named type. Recording the demand then
// reaches that argument, which the missing-impl report names.
func (c *checker) implBoundsUnmetByNamedTypes(concrete Type, bound *InterfaceType) bool {
	if c == nil || bound == nil {
		return false
	}
	name := interfaceImplName(concrete)
	if name == "" {
		return false
	}
	info := lookupImplTypeArgs(name, bound.Name, c.implTypeArgsContext())
	if info == nil || info.Receiver == nil {
		return false
	}
	subs := map[*TypeParam_]Type{}
	if unifyFull(info.Receiver, concrete, subs, c.implsContext(), c.implTypeArgsContext()) != nil {
		return false
	}
	unmet := false
	for tp, arg := range subs {
		for _, b := range tp.Bounds {
			if typeSatisfiesInterfaceForImplMatch(arg, b, c.implsContext(), c.implTypeArgsContext(), nil) {
				continue
			}
			if concreteTypeName(resolveTypeVar(arg)) == "" {
				return false
			}
			unmet = true
		}
	}
	return unmet
}
