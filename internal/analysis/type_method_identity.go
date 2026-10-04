package analysis

// Nominal identity for the TYPE-METHOD table — `Type.method(...)` resolution.
//
// # The defect, measured rather than assumed
//
// `ProjectImplIndex.TypeMethods` is keyed `receiver base name → method name`.
// Two stdlib modules declare a type named `Error` — `std/calendar` and
// `std/random` — and each writes an `impl Display for Error`. Both
// `to_string` symbols therefore contend for the single slot
// `TypeMethods["Error"]["to_string"]`, and the union loop resolves the contest
// with `if _, present := ...; !present` over a range of a Go map. Go randomizes
// map iteration, so WHICH module wins is drawn fresh on every build.
//
// The observable was `calendar.Error.to_string(e)` — a legal call — refused
// with `argument 1: expected Error, got Error`. That message is not a
// diagnostic bug and the comparison behind it is not a comparison bug:
// `TypesEqual` compares `(Origin, Name)` and was reporting, correctly, that
// the parameter type `std/random.Error` is not the argument type
// `std/calendar.Error`. The parameter came from the wrong module's method,
// and re-running the same binary on the same source could pick a different
// one. A legal program's meaning depended on map iteration order.
//
// # Why an identity KEY rather than a determinism fix
//
// Sorting the union loop would make the collapse stable, not correct: one of
// the `Error`s would win every time and the other would stay
// unreachable. `TypeMethodKey` carries the receiver's `Origin`, so two
// same-named receivers cannot occupy one slot — the collapse becomes
// unrepresentable rather than merely detected, and iteration order stops being
// an input. `TypeMethodIdentityConflicts` then catches a GENUINE duplicate:
// two different symbols contending for one fully-qualified key.
//
// This is the fourth instance of one pattern in this repo — a bare-name key
// collapsing module-distinct declarations, with iteration order picking the
// winner (`byKey` losing 13 of 706 declarations; `stdKey` dropping interface
// type arguments and losing 69 of 80; the runtime dispatch key still spelling
// the interface's bare name beside the receiver's qualified identity). The
// direct precedent is `c2afa942`, which fixed nondeterministic namespaced
// dispatch caused by a `ShortTypeName` collapse: same shape, same mechanism,
// a different table.
//
// # Why a PARALLEL map, and not a re-key
//
// `QualifiedReceiver`'s and `ImplIfaceOrigin`'s reason: `TypeMethods` is
// queried by base name from the checker, the orphan rule, the typed-literal
// handler lookup and the runtime bridge, which frequently hold nothing else.
// Identity goes where it is needed and nothing else changes shape.
//
// # Why an unknown origin FALLS BACK
//
// `OriginUnresolved` is the bottom of the identity lattice. A lookup whose
// receiver origin could not be established, and a build that never ran this
// pass (single-file `BuildFileWithStdlib` without `AttachStdlibProjectImpls`,
// a hand-rolled test index), fall through to the bare table and behave exactly
// as they did before this map existed. The identity table can only ever
// REDIRECT a lookup to a better-attributed symbol, never refuse one.

// TypeMethodKey identifies one type-promoted method by the receiver's nominal
// identity rather than by its printed name.
//
// Type is the receiver's base name exactly as `TypeMethods` keys it — the
// possibly-namespaced spelling (`Json.DecodeError`), never the module-qualified
// one. Origin supplies the module. A struct key rather than nested maps because
// every lookup holds all three components at once and a struct key hashes
// without allocating.
type TypeMethodKey struct {
	// Origin is the build key of the file that DECLARED the receiver type —
	// `std/calendar`, `shapes`, `OriginEntry` — matching the `Origin` field on
	// StructType / EnumType / DistinctType.
	Origin string
	Type   string
	Method string
}

// PopulateTypeMethodIdentities fills idx.TypeMethodByIdentity from every
// reachable FA's per-file TypeMethods table, re-keying each entry by the
// receiver type's declaring origin.
//
// Must run AFTER BuildTypes (Sweep C-types), for PopulateQualifiedReceivers'
// reason and by the same mechanism: the origin is read off the receiver type's
// `Origin`, and a type symbol carries no `Type` pointer — hence no `Origin` —
// until then. Run earlier it yields an empty table, which the file comment's
// lattice rule reads as "fall back to the bare table".
//
// Reach and access-control are deliberately identical to the bare table's: the
// same `Public || IsImplMethod` filter, over the same per-file source. Only the
// KEY differs. Widening the reach here would newly expose a module-private
// inherent method across a module boundary.
//
// Order-independent. On contention for one fully-qualified key the winner is
// chosen by rule, not by arrival: inherent beats interface-impl (matching
// defineImplBlock's per-file precedence), then typed beats untyped (matching
// PopulateProjectTypeMethodSymbols), then the earlier source position. A
// contest that reaches the last tier is a genuine duplicate and is recorded.
func PopulateTypeMethodIdentities(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	if idx.TypeMethodByIdentity == nil {
		idx.TypeMethodByIdentity = make(map[TypeMethodKey]*Symbol)
	}
	if idx.TypeMethodModuleByIdentity == nil {
		idx.TypeMethodModuleByIdentity = make(map[TypeMethodKey]string)
	}
	primitives := primitiveOriginIndex(filesByKey)
	for provider, fa := range filesByKey {
		if fa == nil {
			continue
		}
		for typeName, byMethod := range fa.TypeMethods {
			origin := declaringOriginOfType(fa, typeNameHead(typeName), primitives)
			if origin == OriginUnresolved {
				continue
			}
			for methodName, sym := range byMethod {
				if sym == nil || (!sym.Public && !sym.IsImplMethod) {
					continue
				}
				key := TypeMethodKey{Origin: origin, Type: typeName, Method: methodName}
				prev, seen := idx.TypeMethodByIdentity[key]
				if seen && prev == sym {
					continue
				}
				if !seen {
					idx.TypeMethodByIdentity[key] = sym
					idx.TypeMethodModuleByIdentity[key] = provider
					continue
				}
				keep, conflict := preferTypeMethodSymbol(prev, sym)
				if keep == sym {
					idx.TypeMethodByIdentity[key] = sym
					idx.TypeMethodModuleByIdentity[key] = provider
				}
				if conflict {
					idx.TypeMethodIdentityConflicts = append(idx.TypeMethodIdentityConflicts, key)
				}
			}
		}
	}
}

// preferTypeMethodSymbol picks between two distinct symbols contending for one
// TypeMethodKey and reports whether the contest was a genuine duplicate — two
// symbols the rules cannot separate on merit.
func preferTypeMethodSymbol(prev, next *Symbol) (keep *Symbol, conflict bool) {
	if prev.IsImplMethod != next.IsImplMethod {
		// Inherent (`impl Type { pub fn f }`) beats interface-impl
		// (`impl Iface for Type { fn f }`), the same way the per-file table
		// resolves the clash. Not a duplicate: the source said which one wins.
		if next.IsImplMethod {
			return prev, false
		}
		return next, false
	}
	if (prev.Type == nil) != (next.Type == nil) {
		// One side is a pre-BuildTypes symbol that never got its signature.
		// Not a duplicate: the typed one is the same declaration, later.
		if next.Type == nil {
			return prev, false
		}
		return next, false
	}
	// Two declarations of one method on one nominal type. Deterministic
	// tie-break so the index is reproducible, and a recorded conflict so the
	// guard can see it; the coherence pass reports the collision itself.
	if earlierDeclaration(next, prev) {
		return next, true
	}
	return prev, true
}

// earlierDeclaration orders two symbols by source position, file first, so a
// tie-break over them is stable across builds.
func earlierDeclaration(a, b *Symbol) bool {
	if a.SourceFile != b.SourceFile {
		return a.SourceFile < b.SourceFile
	}
	if a.Pos.Line != b.Pos.Line {
		return a.Pos.Line < b.Pos.Line
	}
	return a.Pos.Col < b.Pos.Col
}

// LookupTypeMethodByIdentity resolves `Type.method` for a receiver whose
// declaring module is known. Returns nil when the origin is unknown, the index
// was never populated, or that module declares no such method — every one of
// which must fall back to the bare table rather than refuse.
func (idx *ProjectImplIndex) LookupTypeMethodByIdentity(origin, typeName, method string) *Symbol {
	if idx == nil || origin == OriginUnresolved || idx.TypeMethodByIdentity == nil {
		return nil
	}
	return idx.TypeMethodByIdentity[TypeMethodKey{Origin: origin, Type: typeName, Method: method}]
}

// typeNameHead is the leading segment of a possibly-namespaced type name.
// A nested type (`Json.DecodeError`) takes its origin from its owner, which is
// the name the declaring file's scope actually binds — `qualifyReceiver` reads
// the same segment for the same reason.
func typeNameHead(typeName string) string {
	for i := 0; i < len(typeName); i++ {
		if typeName[i] == '.' {
			return typeName[:i]
		}
	}
	return typeName
}
