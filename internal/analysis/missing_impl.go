package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// FinalizeCoherence runs the post-CheckTypes coherence checks that need
// to see the manifest after every recording site has fired. Today this
// is just DetectMissingImpls; future post-Check coherence checks (e.g.
// a tighter missing-impl variant once typeDecls is populated) join here.
//
// Callers run this once at the end of their analyze pipeline, after every
// FA has been CheckTypes'd and their manifests merged into the entry FA.
// Returns errors to be appended to the entry FA's TypeErrors.
//
// Why the entry FA, not the ProjectImplIndex pointer directly: the index
// is a snapshot taken at BuildProjectWithCache time. Two pieces of it
// drift after that snapshot:
//
//   - ImplManifest grows during CheckTypes (the checker writes
//     RecordingKindCallSite / GenericBoundCheck / TypedLiteralSlot /
//     InterfaceTypedParam entries into per-FA fa.ImplManifest, then
//     internal/frontend / stdcompiler / the LSP call
//     MergeImplManifest(entry, sib) to fold sibling recordings into the
//     entry FA — but neither writes back into ProjectImpls.ImplManifest).
//   - Impls picks up struct-level impl registrations from a
//     late pass at the bottom of BuildProjectWithCache (after the
//     buildProjectImplIndex snapshot is taken). Those updates land in
//     per-FA fa.Impls and the entry's fa.Impls via cross-file mergeImpls,
//     but again don't write back into ProjectImpls.Impls.
//
// We synthesize a fresh snapshot here that unions the project-level
// Impls (covers stdlib + sibling impls) with entryFA.Impls (covers
// late-pass struct-level impls and post-merge user-side impls), and
// reads ImplManifest from entryFA directly (the post-CheckTypes union).
// DetectMissingImpls then sees the full picture.
func FinalizeCoherence(entryFA *FileAnalysis) []TypeError {
	if entryFA == nil || entryFA.ProjectImpls == nil {
		return nil
	}
	// Union project-level Impls with entry-level Impls. Project-level
	// holds stdlib and discovery-walked siblings as of the snapshot;
	// entry-level holds the late struct-level impl pass and any
	// cross-file mergeImpls that happened after the snapshot.
	impls := make(map[string]map[string]bool, len(entryFA.ProjectImpls.Impls))
	for t, ifaces := range entryFA.ProjectImpls.Impls {
		copyIfaces := make(map[string]bool, len(ifaces))
		for i := range ifaces {
			copyIfaces[i] = true
		}
		impls[t] = copyIfaces
	}
	// The covered set is COPIED, not shared. Uncovering below is per-call
	// state about this entry's extra supply, and the index it comes from is
	// held by pointer in every FileAnalysis of the build.
	covered := make(map[string]map[string]bool, len(entryFA.ProjectImpls.implNamesWithIdentity))
	for t, ifaces := range entryFA.ProjectImpls.implNamesWithIdentity {
		copyIfaces := make(map[string]bool, len(ifaces))
		for i := range ifaces {
			copyIfaces[i] = true
		}
		covered[t] = copyIfaces
	}
	// The identity index travels with the snapshot, or DetectMissingImpls'
	// supply check has nothing to consult and every bare-name hit stands.
	snapshot := &ProjectImplIndex{
		Impls:                 impls,
		ImplManifest:          entryFA.ImplManifest,
		ImplsByIdentity:       entryFA.ProjectImpls.ImplsByIdentity,
		implNamesWithIdentity: covered,
	}
	// Supply the entry FA adds beyond the project-level snapshot has no
	// identity entry — PopulateImplsByIdentity ran over the project index,
	// before the late struct-level pass and the post-snapshot cross-file
	// merges. Those pairs fall back to the bare-name answer; leaving them
	// covered would read a real entry-declared impl as another module's.
	for t, ifaces := range entryFA.Impls {
		if impls[t] == nil {
			impls[t] = make(map[string]bool, len(ifaces))
		}
		for i := range ifaces {
			if !entryFA.ProjectImpls.Impls[t][i] {
				snapshot.UncoverImplIdentity(t, i)
			}
			impls[t][i] = true
		}
	}
	return DetectMissingImpls(snapshot, nil)
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
		// Universal default Debug: Debug is satisfied by every type (the
		// eager auto-synthesis pass materializes a structural / name-only
		// Debug impl for every declared type), so a (Debug, T) pair is
		// never genuinely missing. Skip the whole interface — belt-and-
		// suspenders against any Debug demand the synth pass doesn't cover
		// (e.g. a non-nominal kind that has no declaration to synthesize
		// from but is rendered by a Go intrinsic). Every other interface
		// stays subject to the missing-impl diagnostic.
		if iface == "Debug" {
			continue
		}
		// Universal struct interface `Struct`: satisfied structurally by every
		// struct (named or anonymous), with no registered impl — conformance is
		// a predicate (isStructShaped), so a (Struct, T) pair is never genuinely
		// missing. Skip it like Debug; struct-only-ness is enforced at the call
		// site by the structural conformance check, not here.
		if iface == "Struct" {
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
			if len(recs) == 0 {
				continue
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
						continue
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
				continue
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
				continue
			}

			errs = append(errs, missingImplError(iface, typeName, recs, typeDecls))
		}
	}
	return errs
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

// missingImplHelp is the help line for a type that has no impl of iface: an
// operator interface's block shape, or a derive or an impl block. The
// missing-impl diagnostic and the call-site bound check both end with it.
func missingImplHelp(iface, typeName string) string {
	if opIface, ok := operatorInterfaceByName(iface); ok {
		return fmt.Sprintf("write an `impl %s<Rhs, Out> for %s { fn %s(lhs: %s, rhs: Rhs): Out { ... } }` block",
			opIface.Interface, typeName, opIface.Method, typeName)
	}
	return fmt.Sprintf("add `derive %s` to `%s` or write an `impl %s for %s { fn <function>(value: %s): <Ret> }` block",
		iface, typeName, iface, typeName, typeName)
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
func (c *checker) boundError(line, col int, concrete Type, iface, param string, over ...ast.Node) {
	end := TypeError{}
	for _, n := range over {
		if sp, ok := spanOf(n); ok && sp.StartLine == line && sp.StartCol == col {
			end.EndLine, end.EndCol = sp.EndLine, sp.EndCol
		}
	}
	c.report(TypeError{Line: line, Col: col, EndLine: end.EndLine, EndCol: end.EndCol, Message: fmt.Sprintf(
		"%s does not implement %s (required by `where %s: %s`)",
		concrete, iface, param, iface)}.WithHint(missingImplHelp(iface, fmt.Sprint(concrete))))
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
