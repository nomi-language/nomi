package analysis

// Nominal identity on the missing-impl SUPPLY side.
//
// `DetectMissingImpls` asks "is there an impl of Iface for T" by indexing
// `idx.Impls[typeName][iface]`, and `Impls` is keyed by BARE type name. So a
// type borrows an interface from a same-named type in another module, the
// demand is judged satisfied, and a program with no impl at all is accepted.
//
// # The defect, measured rather than assumed
//
// `std/calendar.nomi` and `std/random.nomi` each declare their own
// `pub enum Error` and carry `derive Equatable for Error`. Keyed by bare name,
// `Impls["Error"]["Equatable"]` is true for ANY type called `Error`, so
// `Equatable.equal?` over a user's own `Error` with no impl type-checks.
//
// Two identical programs, differing only in the type's name:
//
//	pub enum Zonk  { Bad String }   -> "no impl of `Equatable` for `Zonk`"
//	pub enum Error { Bad String }   -> accepted, then faults at run time
//
// Accepted, it has no meaning to run: a dispatch keyed by bare name would run
// a stdlib module's synthesized body against a value of another type, and a
// structural comparison would ignore that the program demanded an impl.
// Neither is the program's meaning.
//
// # Why a PARALLEL index, and not a re-key
//
// The same reason as `TypeMethodByIdentity` and `QualifiedReceiver`: `Impls`,
// `ImplTypeArgs`, `TypeMethods` and `ImplManifest` are queried by BASE NAME
// from all over the checker and unifier, which frequently hold nothing else.
// Identity goes where it is needed, keyed the same way the table it qualifies
// is keyed, and nothing else changes shape. This is the sixth such index.
//
// # Why an unknown origin ACCEPTS
//
// `OriginUnresolved` is the bottom of the identity lattice. A demand whose
// receiver is a primitive, a type parameter, or a type this build could not
// resolve carries no origin, and a build that never ran
// `PopulateImplsByIdentity` has an empty index. Both answer "supplied", so
// every path that does not populate this index behaves exactly as it did
// before the index existed. The check can only ever REJECT when the demand's
// origin is known, the index covers the name, and no supplier matches.
//
// A primitive receiver is keyed by the file that declares it, wherever the
// impl is written: `declaringOriginOfType` reads an imported PrimitiveType's
// origin from `primitiveOriginIndex`, so `impl ToJson for Int` in std/json
// registers under std/int. `nominalOrigin` reports no origin for a built-in
// primitive, so a demand on one lands at the bottom and is accepted. A
// non-generic host type is not a built-in: it carries its own Origin, and a
// demand on it is judged like a demand on a struct.

// ImplIdentityKey is `Impls`' key with the receiver's declaring module
// attached: the supply side's nominal identity rather than its base name.
type ImplIdentityKey struct {
	// Origin is the build key of the file that declared the receiver type
	// ("std/calendar", "std/random", OriginEntry for the project entry).
	Origin string
	// Type is the receiver's base name, spelled as `Impls` spells it.
	Type string
	// Iface is the interface base name, spelled as `Impls` spells it.
	Iface string
}

// PopulateImplsByIdentity fills idx.ImplsByIdentity and
// idx.implNamesWithIdentity.
//
// Must run AFTER BuildTypes (Sweep C-types), for `PopulateQualifiedReceivers`'
// reason and by the same mechanism: the origin is read off the receiver type's
// `Origin`, and a type symbol carries no `Type` pointer — hence no `Origin` —
// until then. Run earlier it yields an empty index, which the file comment's
// lattice rule then reads as "everything supplied".
//
// The origin is resolved through the impl's HOME file, not through whichever
// FileAnalysis happens to be iterated: the per-file impl maps are broadcast,
// so every FA holds every file's impls and only `ImplFiles` says where an impl
// was written. That is `PopulateQualifiedReceivers`' second recorded trap.
func PopulateImplsByIdentity(idx *ProjectImplIndex, filesByKey map[string]*FileAnalysis) {
	if idx == nil {
		return
	}
	if idx.ImplsByIdentity == nil {
		idx.ImplsByIdentity = make(map[ImplIdentityKey]bool, len(idx.ImplBlockReceiver))
	}
	// One origin lookup per (home module, receiver head) rather than per
	// impl: a module's impls share a home file, and declaringOriginOfType
	// walks a scope and an import chain on every call.
	type memoKey struct{ module, head string }
	memo := make(map[memoKey]string)
	primitives := primitiveOriginIndex(filesByKey)
	originOf := func(module, recv string) string {
		k := memoKey{module: module, head: typeNameHead(recv)}
		origin, seen := memo[k]
		if !seen {
			origin = declaringOriginOfType(filesByKey[k.module], k.head, primitives)
			memo[k] = origin
		}
		return origin
	}

	// A pair is COVERED once some supplier of it resolved an origin, and
	// BLOCKED for good if any supplier of it did not. Blocked wins: an
	// unresolved supplier may be the very declaration a demand names, so
	// answering "that origin has no entry" would reject a conformance that
	// exists. `ReceiverOf` returns bare names for inherent-block methods
	// (ImplFiles carries interface-impl FuncDefs only, so filesByKey[""] is
	// nil and the origin is unresolved), and that is exactly the case this
	// has to survive rather than assume away.
	covered := make(map[string]map[string]bool)
	blocked := make(map[string]map[string]bool)
	mark := func(into map[string]map[string]bool, recv, iface string) {
		if into[recv] == nil {
			into[recv] = make(map[string]bool)
		}
		into[recv][iface] = true
	}
	record := func(module, recv, iface string) {
		if recv == "" || iface == "" {
			return
		}
		origin := originOf(module, recv)
		if origin == OriginUnresolved {
			mark(blocked, recv, iface)
			return
		}
		idx.ImplsByIdentity[ImplIdentityKey{Origin: origin, Type: recv, Iface: iface}] = true
		mark(covered, recv, iface)
	}

	for fn := range idx.ImplBlockReceiver {
		if fn != nil {
			record(idx.ImplFiles[fn], idx.ReceiverOf(fn), idx.ImplBlockInterfaceKey[fn])
		}
	}
	for _, byMethod := range idx.IfaceMethodImpls {
		for _, fns := range byMethod {
			for _, fn := range fns {
				if fn != nil {
					record(idx.ImplFiles[fn], idx.ReceiverOf(fn), idx.ImplBlockInterfaceKey[fn])
				}
			}
		}
	}
	// Extern impls (`impl Iface for T { host fn ... }`) have no FuncDef and
	// travel on the parallel extern maps, which the FuncDef maps deliberately
	// stay free of. They are real conformances, so a pass that skipped them
	// would block every pair std supplies that way — `String`'s `impl Iter`
	// among them — and quietly shrink the check to nothing.
	for ex, recv := range idx.ImplBlockReceiverExtern {
		if ex != nil {
			record(idx.ImplExternFiles[ex], recv, idx.ImplBlockInterfaceKeyExtern[ex])
		}
	}

	idx.implNamesWithIdentity = make(map[string]map[string]bool, len(covered))
	for recv, ifaces := range covered {
		for iface := range ifaces {
			if blocked[recv][iface] {
				continue
			}
			mark(idx.implNamesWithIdentity, recv, iface)
		}
	}
}

// UncoverImplIdentity drops a (T, Iface) pair from the identity index's
// covered set, so demands for it fall back to the bare-name answer.
//
// For supply that reaches `DetectMissingImpls` from somewhere
// `PopulateImplsByIdentity` never walked. `FinalizeCoherence` unions the
// entry FA's own `Impls` — the late struct-level registration pass and any
// post-snapshot cross-file merge — into the table it judges against, and
// those conformances have no identity entry. Left covered, an entry type's
// perfectly good `impl Equatable for Error` would read as "some other
// module's Error supplies this" and the program would be rejected.
func (idx *ProjectImplIndex) UncoverImplIdentity(typeName, iface string) {
	if idx == nil || idx.implNamesWithIdentity == nil {
		return
	}
	delete(idx.implNamesWithIdentity[typeName], iface)
}

// ImplIdentityCovers reports whether the identity index can judge a
// (T, Iface) pair at all. Exported for the guard that pins the fallback
// population as an exact closed set: a pair that falls back is a pair the
// check cannot reject, so silent growth here silently disables it.
func (idx *ProjectImplIndex) ImplIdentityCovers(typeName, iface string) bool {
	return idx != nil && idx.implNamesWithIdentity[typeName][iface]
}

// implSuppliedByIdentity answers DetectMissingImpls' supply question for a
// demand that already matched by bare name, and reports whether it could
// answer at all.
//
// `known` is false — meaning "keep the bare-name answer" — whenever anything
// in the chain is missing: an index that was never populated, a demand with no
// origin, or a (name, iface) pair this index does not cover. Only when the
// pair IS covered and the demand's own origin has no entry does this say the
// bare-name match was a borrow.
func (idx *ProjectImplIndex) implSuppliedByIdentity(origin, typeName, iface string) (supplied, known bool) {
	if idx == nil || len(idx.ImplsByIdentity) == 0 {
		return false, false
	}
	if origin == OriginUnresolved {
		return false, false
	}
	if !idx.implNamesWithIdentity[typeName][iface] {
		return false, false
	}
	return idx.ImplsByIdentity[ImplIdentityKey{Origin: origin, Type: typeName, Iface: iface}], true
}

// unsuppliedRecordings keeps only the demands whose OWN declaration has no
// impl of `iface`, out of a bucket that already matched by bare name.
//
// Filtering per RECORDING rather than judging the bucket is not a refinement,
// it is the whole mechanism. `ImplManifest[iface][typeName]` is keyed by bare
// name too, so one bucket holds demands for every same-named type: the
// `Equatable`/`Error` bucket holds the demands the stdlib raised about its own
// `Error` types and the ones the user raised about theirs. Asking whether "the
// bucket's origin" is supplied has no answer — the origins disagree — and any
// whole-bucket rule either accepts the borrow or rejects the stdlib's own
// correct demands.
//
// Returns nil — meaning "keep the bare-name answer, the pair is supplied" —
// for every recording the index cannot judge: no origin on the demand, or a
// (name, iface) pair the index does not cover. So a build that never
// populated the index, a primitive receiver, and an origin-less generic
// `host type` all behave exactly as they did before the index existed.
func (idx *ProjectImplIndex) unsuppliedRecordings(recs []Recording, typeName, iface string) []Recording {
	var out []Recording
	for _, rec := range recs {
		supplied, known := idx.implSuppliedByIdentity(rec.TypeOrigin, typeName, iface)
		if known && !supplied {
			out = append(out, rec)
		}
	}
	return out
}
