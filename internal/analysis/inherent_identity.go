package analysis

// Nominal identity for the INHERENT (type-owned) method manifest — the
// `duplicate type-owned function` check.
//
// # The defect, measured rather than assumed
//
// `detectInherentImplCollisions` grouped its records by `Receiver + method`,
// a BARE type name. Two types the nominal-identity rule calls distinct
// therefore shared one manifest slot, so importing a stdlib module could make
// a name in the user's own file un-declarable. For example:
//
//	import std/calendar
//	struct Date { flavor: String }
//	impl Date { pub fn new(a: String, b: String): Date { Date{flavor: a} } }
//
// is refused: "duplicate type-owned function: `Date.new` is defined 2 times".
// `std/calendar` declares its own `impl Date { pub fn new(year, month, day) }`,
// and the diagnostic blames the user's line for a collision they cannot see.
// Both `Date`s are legal and both must keep their own `new`.
//
// This is the fifth instance of one pattern in this repo — a bare-name key
// collapsing module-distinct declarations. `internal/irbuild`'s `stdGoName`,
// `TypeMethods` (`type_method_identity.go`), the impl-block
// receiver (`PopulateQualifiedReceivers`) and the impl'd interface
// (`interface_identity.go`) are the previous four; this one is on the write
// side of a CHECK rather than of a lookup, which is why it presents as a
// rejected program rather than as a wrong answer.
//
// # Why a field on the record, and not a parallel map
//
// `QualifiedReceiver`'s reason for a parallel map is that the
// type-keyed maps — `Impls`, `ImplTypeArgs`, `TypeMethods`, `ImplManifest` —
// are queried by BARE name from all over the checker and unifier, and the
// orphan rule deliberately indexes by base name. None of that applies here:
// `[]InherentMethodRecord` is not a map and has exactly one consumer,
// `detectInherentImplCollisions`. `Receiver` keeps its bare spelling, which is
// what the diagnostic prints; identity rides beside it in a new field.
//
// # Why a separate pass, and not population at index time
//
// `PopulateQualifiedReceivers`' precondition, by the same mechanism: the
// origin is read off the receiver type's `Origin`, and a type symbol carries
// no `Type` pointer — hence no `Origin` — until BuildTypes (Sweep C-types).
// `IndexImplBlockFuncDefs` runs in Sweep B, long before that, so filling the
// field there yields `OriginUnresolved` for everything.
//
// The second trap that pass records — that the per-file impl maps are
// broadcast, so the iterated `FileAnalysis` is not the declaring one — does
// not bite here: each record carries its own home build key in `Module`,
// stamped from `fb.key` at index time. That is the impl's home file by
// construction, so no search is needed.
//
// # Why an unknown origin FALLS BACK to the bare grouping
//
// `OriginUnresolved` is the bottom of the identity lattice: it matches any
// origin with the same name. A check must not become weaker than it was when
// identity is unavailable, so records whose receiver origin could not be
// established join every resolved bucket for their name rather than forming a
// bucket of their own. A build that never ran this pass (a hand-rolled test
// index, a single-file build) therefore behaves exactly as it did before this
// file existed: one bucket per bare name, one diagnostic per real duplicate.
//
// Over a stdlib-backed two-file project every inherent record that reaches
// this pass resolves an origin, so the fallback is a floor rather than a hot
// path.

// PopulateInherentReceiverOrigins fills the ReceiverOrigin field of every
// record in `inherents`, resolving each receiver's declaring file through the
// impl's OWN home file (`rec.Module`) rather than through any broadcast map.
//
// Must run AFTER BuildTypes (Sweep C-types) — see the file comment. Mutates
// the records in place; `inherents` is a slice of values, so the caller's
// backing array is the one updated.
func PopulateInherentReceiverOrigins(inherents []InherentMethodRecord, filesByKey map[string]*FileAnalysis) {
	// One lookup per (home file, receiver head) pair rather than per record:
	// a type body with twenty functions asks the same question twenty times.
	type originKey struct{ module, head string }
	memo := make(map[originKey]string, len(inherents))
	primitives := primitiveOriginIndex(filesByKey)
	for i := range inherents {
		recv := inherents[i].Receiver
		if recv == "" {
			continue
		}
		k := originKey{module: inherents[i].Module, head: typeNameHead(recv)}
		origin, seen := memo[k]
		if !seen {
			origin = declaringOriginOfType(filesByKey[k.module], k.head, primitives)
			memo[k] = origin
		}
		inherents[i].ReceiverOrigin = origin
	}
}

// inherentOriginBuckets partitions one bare-name group of records — every
// record in it shares a (Receiver, Method) pair — into the sets that would
// genuinely be duplicate definitions of one type's function.
//
// A record with a resolved origin belongs to that origin's bucket. A record
// with `OriginUnresolved` belongs to ALL of them, because the lattice says an
// unresolved origin may be any of them and the check must not weaken; when no
// origin in the group resolved, the whole group is one bucket, which is
// exactly the pre-identity behaviour.
//
// Buckets are returned in a deterministic order — first appearance of each
// origin in the (already position-sorted) group — so the diagnostic a user
// sees does not depend on Go map iteration.
func inherentOriginBuckets(group []InherentMethodRecord) [][]InherentMethodRecord {
	// A group is the records sharing one (Receiver, Method) pair, so it is a
	// handful at most — a linear scan for distinctness beats allocating a set.
	var origins []string
	for _, rec := range group {
		if rec.ReceiverOrigin == OriginUnresolved {
			continue
		}
		known := false
		for _, o := range origins {
			if o == rec.ReceiverOrigin {
				known = true
				break
			}
		}
		if !known {
			origins = append(origins, rec.ReceiverOrigin)
		}
	}
	// One origin (or none) means one bucket, and it is the whole group: an
	// unresolved record belongs to every bucket, so with a single resolved
	// origin there is nothing to partition. This is the overwhelmingly common
	// shape of a group that reaches here, and it allocates nothing.
	if len(origins) <= 1 {
		return [][]InherentMethodRecord{group}
	}
	buckets := make([][]InherentMethodRecord, 0, len(origins))
	for _, origin := range origins {
		bucket := make([]InherentMethodRecord, 0, len(group))
		for _, rec := range group {
			if rec.ReceiverOrigin == origin || rec.ReceiverOrigin == OriginUnresolved {
				bucket = append(bucket, rec)
			}
		}
		buckets = append(buckets, bucket)
	}
	return buckets
}
