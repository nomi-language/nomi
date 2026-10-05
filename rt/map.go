package rt

import (
	"math/bits"
	"slices"
	"strings"
)

// Nomi's `Map<K, V>`: a persistent hash trie, insertion-ordered, keyed by a
// structural hash and equality supplied by the caller.
//
// # Why a Go `map` is not available, and it is not a phasing decision
//
// A Nomi Map key is any keyable value (spec §19, *Map*).
// Lists, tuples, structs, distincts, variants and maps themselves are
// keyable, and they compare structurally with no user hook and no
// `K: Hashable` bound in the signature of `Map.get`. The
// corpus relies on it — 08-pattern-matching/map_patterns_test.nomi has
// `{(1, 2) => "a"}` and `{[1, 2] => "x"}`.
//
// Go requires a map key to be `comparable`, and for the types where that is
// satisfiable at all it is the wrong comparison: a `*List[T]` key is comparable
// in Go and compares by address, so `{[1, 2] => "x"}` would be a map you cannot
// read a value out of unless you kept the original cons cells alive. That is
// the trap collections.go names for `==` on a tuple, in its worst form — the
// key silently vanishes rather than the answer being visibly false.
//
// So the key operations are parameters: `hash func(K) uint64` and
// `eq func(K, K) bool`, assembled per key type by the caller from rt/hash.go's
// leaves. Exactly the shape ListCellsEqual already uses, for exactly the same reason
// — and see hash.go for why they may not be the user-facing `Hashable`/
// `Equatable` impls.
//
// # Immutable, so `put` may not rebuild
//
// Nomi's Map is a value: `Map.put(m, k, v)` returns a new map and m is
// unchanged. That rules out the obvious compact representation — an ordered
// `[]MapEntry` plus a hash index — because copying it makes `put` O(n), and the
// idiomatic accumulate-into-a-map fold would go quadratic. That is precisely
// the mistake rt/list.go declined for `List` ("the compiled program would agree
// on every answer and stop finishing on real input"), and it has the same shape
// here.
//
// # The structure: a 32-way persistent hash trie (HAMT)
//
// A boxed caller supplies hashing and equality over boxed values; a typed
// caller supplies typed callbacks. Both use the same persistent updates and
// insertion-order rules:
//
//	Map.size        O(1)          a field read. std/maps.nomi:66 documents O(1)
//	                              and `Iter.known_count` on a Map returns
//	                              Some(size), so a size that walked the trie
//	                              would make `Iter.count` O(n) on a program
//	                              that only asks how big the map is — the trap
//	                              rt/list.go avoided by caching Len on the cons
//	                              cell.
//	Map.get         O(log32 n)    at most 13 node hops, at most one eq call.
//	Map.put         O(log32 n)    copies the nodes on one root path (<= 13) and
//	                              shares every other node.
//	Map.remove      O(log32 n)    same.
//	Map.merge(a,b)  O(m log32 n)  a fold of puts, m = size of b.
//	iteration       O(n) or       O(n) when no key was ever removed, O(n log n)
//	                O(n log n)    otherwise — see MapEntries.
//
// It is written here rather than taken as a dependency, because rt keeps its
// require list minimal (see rt.go). A HAMT is ~200 lines of
// well-understood bit-twiddling over `math/bits`, which is a far smaller cost
// than everything linking rt inheriting a third-party module and its version
// constraints.
//
// The alternative considered and rejected was a persistent balanced BST keyed
// on the hash — materially easier to verify than a HAMT, at O(log2 n) instead
// of O(log32 n). Rejected for a reason that is not the constant factor: a BST
// needs a total order on keys, and Nomi gives an arbitrary value a structural
// hash and an equality, not an order. Ordering on the hash alone still needs a
// collision bucket compared by equality — so the BST does not avoid the bucket,
// it only adds a requirement the language does not supply. A HAMT needs exactly
// the two operations that exist.
//
// # Insertion order is a sequence number
//
// New keys append. Updating an existing key preserves its position; removal
// leaves the counter unchanged, so reinsertion moves the key to the end.
// These rules hold for every caller.
//
// The alternative — a persistent insertion-ordered vector beside the trie —
// buys O(n) iteration unconditionally and costs a second persistent structure
// plus tombstones and compaction for `remove`. MapEntries gets the O(n) case
// for free instead, from the observation that the sequence counter and the
// entry count can only diverge once something has been removed.

// MapEntry is one key/value pair as a caller sees it: a map literal's element,
// and one step of iteration. Exported fields because callers outside rt build
// it.
//
// Separate from the internal mapEntry, which adds the cached hash and the
// insertion sequence — the storage type is not the boundary type. Embedding
// keeps them one declaration apart instead of two lists of fields that can
// drift.
type MapEntry[K, V any] struct {
	Key K
	Val V
}

type mapEntry[K, V any] struct {
	MapEntry[K, V]
	// hash is cached so a lookup compares an integer before calling eq, which
	// turns the common miss into one comparison instead of a structural walk
	// of a list or tuple key.
	hash uint64
	// seq is the insertion sequence. Iteration order is ascending seq.
	seq int
}

// Map is Nomi's `Map<K, V>`.
//
// A value, not a pointer, so the zero Map is the empty map — the analogue of
// nil being the empty List. That makes `Map.empty()` free (no allocation and no
// constructor to emit) and makes a Map-typed struct field correct without
// initialization, which matters because Nomi has no zero values and a caller
// would otherwise have to invent one here.
//
// The key operations are not fields. Carrying them would make every Map value
// two words fatter, make the zero value unusable (nil funcs), and store per
// value what the caller already knows at every call site.
type Map[K, V any] struct {
	root *mapNode[K, V]
	// n is the entry count, so MapSize is a field read.
	n int
	// seq is the sequence the next brand-new key receives. It never decreases,
	// so `seq == n` holds if and only if nothing was ever removed — the
	// property MapEntries uses to skip its sort.
	seq int
}

// mapNode is one trie node. Two shapes, told apart without a tag byte:
//
//   - a branch — `leaves|subs != 0`. The two bitmaps are disjoint: a slot holds
//     an entry or a child, never both. `leaf[i]` is the entry at the i-th set
//     bit of `leaves`, `sub[i]` the child at the i-th set bit of `subs`.
//   - a collision bucket — `leaves == 0 && subs == 0`, with `leaf` holding
//     every entry whose full 64-bit structural hash is that one value. Reached
//     only when the trie has run out of hash bits to separate two distinct
//     keys, which takes a 64-bit hash collision.
//
// The two are unambiguous because an empty node is never stored: an empty Map
// has a nil root, and every operation that would leave a node with no entry
// and no child returns nil instead.
type mapNode[K, V any] struct {
	leaves uint32
	subs   uint32
	leaf   []mapEntry[K, V]
	sub    []*mapNode[K, V]
}

const (
	// mapBits is the branching exponent: 32-way, five bits of hash per level,
	// so a 64-bit hash gives at most 13 levels.
	mapBits = 5
	mapMask = 1<<mapBits - 1
	// mapLastShift is the deepest shift at which a 64-bit hash still has bits.
	// Descending past it would shift by 65, which Go defines as zero — an
	// infinite descent rather than an error — so that is where a collision
	// bucket gets built instead.
	mapLastShift = 60
)

func mapSlot(h uint64, shift uint) uint32 { return uint32(h>>shift) & mapMask }

// mapAt is a slot's index into leaf or sub: how many occupied slots precede it.
func mapAt(bm, bit uint32) int { return bits.OnesCount32(bm & (bit - 1)) }

// --- construction -----------------------------------------------------------

// MapOf builds a map from entries in order: last-write-wins on a duplicate key,
// with the first occurrence keeping its position. That is the rule both `{"a" => 1, "a" => 9}` and `Iter.to_map` depend on.
//
// A map literal lowers to this. It is a fold of persistent puts, so an n-entry
// literal copies O(n log32 n) nodes where a transient builder would copy
// O(n/32). Map literals are typically one to three entries, so a transient
// builder would buy nothing measurable and is not written.
func MapOf[K, V any](hash func(K) uint64, eq func(K, K) bool, entries []MapEntry[K, V]) Map[K, V] {
	var m Map[K, V]
	for _, e := range entries {
		m = MapPut(m, hash, eq, e.Key, e.Val)
	}
	return m
}

// --- queries ----------------------------------------------------------------

// MapSize is `Map.size` — O(1), a field read. See the cost table above for why
// that is a requirement and not a bonus.
func MapSize[K, V any](m Map[K, V]) int64 { return int64(m.n) }

// MapLookup is the raw get: the bound value, and whether the key was present.
//
// Exported because it is the shape more than one caller wants — MapEqual and
// MapMerge use it, `Map.contains_key?` wants only the bool, and `Map.get` wants
// a `Maybe<V>`.
//
// mapCountNode and mapCountProbe mark each node visited and each entry whose
// hash is compared. They are empty functions in every normal build and exist
// only so TestMeasureMapCosts can count this loop's work under the
// `rtmapcount` tag; see mapcount_off.go.
func MapLookup[K, V any](m Map[K, V], hash func(K) uint64, eq func(K, K) bool, key K) (V, bool) {
	var zero V
	h := hash(key)
	n := m.root
	for shift := uint(0); n != nil; shift += mapBits {
		mapCountNode()
		if n.leaves == 0 && n.subs == 0 {
			for i := range n.leaf {
				mapCountProbe()
				if n.leaf[i].hash == h && eq(n.leaf[i].Key, key) {
					return n.leaf[i].Val, true
				}
			}
			return zero, false
		}
		bit := uint32(1) << mapSlot(h, shift)
		if n.leaves&bit != 0 {
			// A branch slot holds at most one entry, so a miss here is a miss
			// outright: there is nowhere deeper this hash could be.
			mapCountProbe()
			e := &n.leaf[mapAt(n.leaves, bit)]
			if e.hash == h && eq(e.Key, key) {
				return e.Val, true
			}
			return zero, false
		}
		if n.subs&bit == 0 {
			return zero, false
		}
		n = n.sub[mapAt(n.subs, bit)]
	}
	return zero, false
}

// MapEntries returns the entries in insertion order.
//
// The single ordered accessor: iteration, rendering and key/value extraction all go through it, so the
// trie's shape stays private to this file.
//
// Two paths, and the fast one is not an optimization of the slow one — it is
// the common case. Sequence numbers are handed out densely from 0 and only
// `remove` creates a gap, and a gap never closes, so `seq == n` holds exactly
// when no key was ever removed. Each entry's seq is then its index, and the
// walk places it directly: O(n), no sort and no intermediate slice. Otherwise
// the entries are collected and sorted by seq.
func MapEntries[K, V any](m Map[K, V]) []MapEntry[K, V] {
	if m.n == 0 {
		return nil
	}
	out := make([]MapEntry[K, V], m.n)
	if m.seq == m.n {
		mapPlace(m.root, out)
		return out
	}
	all := make([]mapEntry[K, V], 0, m.n)
	all = mapCollect(m.root, all)
	slices.SortFunc(all, func(a, b mapEntry[K, V]) int { return a.seq - b.seq })
	for i := range all {
		out[i] = all[i].MapEntry
	}
	return out
}

// mapPlace writes each entry at the index its sequence number names. Sound only
// when those numbers are exactly 0..len(out)-1, which MapEntries checks.
func mapPlace[K, V any](n *mapNode[K, V], out []MapEntry[K, V]) {
	if n == nil {
		return
	}
	for i := range n.leaf {
		out[n.leaf[i].seq] = n.leaf[i].MapEntry
	}
	for _, c := range n.sub {
		mapPlace(c, out)
	}
}

func mapCollect[K, V any](n *mapNode[K, V], out []mapEntry[K, V]) []mapEntry[K, V] {
	if n == nil {
		return out
	}
	out = append(out, n.leaf...)
	for _, c := range n.sub {
		out = mapCollect(c, out)
	}
	return out
}

// MapKeys is `Map.keys`: the keys as a List, in insertion order.
//
// std/maps.nomi writes it as `Iter.map(m, |(k, _)| k) |> Iter.to_list()`, and
// this is not a second rule — it is the same rule with the intermediate
// sequence removed. Both spellings bottom out in MapEntries, the single ordered
// accessor this file's rendering and equality already go through, so there is
// one order in rt and not two. What is not shared with the Nomi body is the
// pipeline, and skipping it is why this function exists: the Nomi form allocates
// a Seq, a per-element tuple and a closure to project one component out of it,
// where a List of n cells over one slice walk is the operation.
//
// The result is built by consing in reverse off the ordered slice, which is
// SeqToListCells' shape and the cheap direction: a List is built head-first.
func MapKeys[K, V any](m Map[K, V]) *List[K] {
	es := MapEntries(m)
	var out *List[K]
	for i := len(es) - 1; i >= 0; i-- {
		out = Cons(es[i].Key, out)
	}
	return out
}

// MapValues is `Map.values`: the values as a List, in the insertion order of
// their keys. See MapKeys.
func MapValues[K, V any](m Map[K, V]) *List[V] {
	es := MapEntries(m)
	var out *List[V]
	for i := len(es) - 1; i >= 0; i-- {
		out = Cons(es[i].Val, out)
	}
	return out
}

// MapMapValues is `Map.map_values`: every value transformed, every key and every
// position kept.
//
// The key operations are still parameters, and it would be a bug to drop them
// even though no key changes. std/maps.nomi implements this as
// `Iter.map |> Iter.to_map`, so the result is a map rebuilt from
// scratch, and a rebuild needs the hasher and the comparator like any other.
// Building it that way rather than by rewriting the values in place is
// deliberate: an in-place rewrite would keep the trie's shape and the sequence
// numbers, which is observably the same answer, and would silently stop being
// the same answer the day `Iter.to_map`'s last-write-wins rule mattered — a
// duplicate key cannot arise from a Map, but the identity of the two
// implementations is what makes the equivalence checkable instead of argued.
//
// `f` takes a Frame because a Nomi lambda may fault, and the frame is how a
// fault carries its position — the same reason every Seq callback takes one.
func MapMapValues[K, V, U any](
	fr *Frame,
	m Map[K, V],
	hash func(K) uint64,
	eq func(K, K) bool,
	f func(fr *Frame, val V) U,
) Map[K, U] {
	es := MapEntries(m)
	out := make([]MapEntry[K, U], len(es))
	for i := range es {
		out[i] = MapEntry[K, U]{Key: es[i].Key, Val: f(fr, es[i].Val)}
	}
	return MapOf(hash, eq, out)
}

// --- updates ----------------------------------------------------------------

// MapPut is `Map.put`: a new map with key bound to val.
//
// Last-write-wins, and an existing key keeps its insertion position — the
// sequence number is inherited from the entry being replaced, never reassigned.
func MapPut[K, V any](m Map[K, V], hash func(K) uint64, eq func(K, K) bool, key K, val V) Map[K, V] {
	e := mapEntry[K, V]{MapEntry: MapEntry[K, V]{Key: key, Val: val}, hash: hash(key), seq: m.seq}
	root, added := mapInsert(m.root, 0, e, eq)
	out := Map[K, V]{root: root, n: m.n, seq: m.seq}
	if added {
		out.n++
		out.seq++
	}
	return out
}

// MapRemove is `Map.remove`: a new map without key, or the same map if absent.
//
// `seq` is deliberately not rolled back, which is what makes a removed key
// re-inserted later land at the end of the iteration order rather than back in
// its old position. `Map.put(Map.remove({"a" => 1, "b" => 2}, "a"), "a", 7)` printing
// `{b => 2, a => 7}` is the observable consequence.
func MapRemove[K, V any](m Map[K, V], hash func(K) uint64, eq func(K, K) bool, key K) Map[K, V] {
	root, removed := mapDelete(m.root, 0, hash(key), key, eq)
	if !removed {
		return m
	}
	return Map[K, V]{root: root, n: m.n - 1, seq: m.seq}
}

// MapMerge is `Map.merge`: b's bindings win, a keeps its ordering for the keys
// it already had, and b's new keys append in b's order.
//
// A fold of puts over b's ordered entries, which is what makes that ordering
// fall out instead of being arranged: `Map.merge({"a" => 1}, {"b" => 2,
// "a" => 9})` is `{a => 9, b => 2}`, and a fold reproduces
// it because put-over-existing inherits the position.
func MapMerge[K, V any](a, b Map[K, V], hash func(K) uint64, eq func(K, K) bool) Map[K, V] {
	out := a
	for _, e := range MapEntries(b) {
		out = MapPut(out, hash, eq, e.Key, e.Val)
	}
	return out
}

// mapInsert returns the node with e bound, and whether the key was new.
//
// Every node on the path from the root to the change is copied and every other
// node is shared. That is what persistence costs here, and all it costs.
func mapInsert[K, V any](n *mapNode[K, V], shift uint, e mapEntry[K, V], eq func(K, K) bool) (*mapNode[K, V], bool) {
	if n == nil {
		return &mapNode[K, V]{leaves: 1 << mapSlot(e.hash, shift), leaf: []mapEntry[K, V]{e}}, true
	}
	if n.leaves == 0 && n.subs == 0 {
		for i := range n.leaf {
			if eq(n.leaf[i].Key, e.Key) {
				e.seq = n.leaf[i].seq
				return &mapNode[K, V]{leaf: mapSet(n.leaf, i, e)}, false
			}
		}
		return &mapNode[K, V]{leaf: mapInsertAt(n.leaf, len(n.leaf), e)}, true
	}
	bit := uint32(1) << mapSlot(e.hash, shift)
	switch {
	case n.leaves&bit != 0:
		i := mapAt(n.leaves, bit)
		old := n.leaf[i]
		if old.hash == e.hash && eq(old.Key, e.Key) {
			e.seq = old.seq
			return &mapNode[K, V]{leaves: n.leaves, subs: n.subs, leaf: mapSet(n.leaf, i, e), sub: n.sub}, false
		}
		// Two distinct keys want one slot, so the slot becomes a child holding
		// both of them, split on the next chunk of their hashes.
		return &mapNode[K, V]{
			leaves: n.leaves &^ bit,
			subs:   n.subs | bit,
			leaf:   mapRemoveAt(n.leaf, i),
			sub:    mapInsertAt(n.sub, mapAt(n.subs, bit), mapSplit(shift+mapBits, old, e)),
		}, true
	case n.subs&bit != 0:
		i := mapAt(n.subs, bit)
		child, added := mapInsert(n.sub[i], shift+mapBits, e, eq)
		return &mapNode[K, V]{leaves: n.leaves, subs: n.subs, leaf: n.leaf, sub: mapSet(n.sub, i, child)}, added
	default:
		return &mapNode[K, V]{
			leaves: n.leaves | bit,
			subs:   n.subs,
			leaf:   mapInsertAt(n.leaf, mapAt(n.leaves, bit), e),
			sub:    n.sub,
		}, true
	}
}

// mapSplit builds the node holding two entries with different keys, descending
// until their hashes disagree — or, once the hash is exhausted, giving up and
// making a collision bucket.
func mapSplit[K, V any](shift uint, a, b mapEntry[K, V]) *mapNode[K, V] {
	if shift > mapLastShift {
		return &mapNode[K, V]{leaf: []mapEntry[K, V]{a, b}}
	}
	sa, sb := mapSlot(a.hash, shift), mapSlot(b.hash, shift)
	if sa == sb {
		return &mapNode[K, V]{subs: 1 << sa, sub: []*mapNode[K, V]{mapSplit(shift+mapBits, a, b)}}
	}
	n := &mapNode[K, V]{leaves: 1<<sa | 1<<sb}
	if sa < sb {
		n.leaf = []mapEntry[K, V]{a, b}
	} else {
		n.leaf = []mapEntry[K, V]{b, a}
	}
	return n
}

// mapDelete returns the node without key, and whether it was there.
//
// A node left with no entry and no child becomes nil, so an emptied subtree is
// released rather than retained. What it deliberately does not do is
// canonicalize — collapse a branch holding exactly one entry back into its
// parent's slot. The shape then depends on insertion history, and that is
// invisible: every observable (lookup, size, order, equality, rendering) is
// defined over the entries and not over the shape, and depth stays bounded by
// the 13 levels a 64-bit hash allows. What it costs is a pointer hop on a
// lookup down a path some removal thinned out.
func mapDelete[K, V any](n *mapNode[K, V], shift uint, h uint64, key K, eq func(K, K) bool) (*mapNode[K, V], bool) {
	if n == nil {
		return nil, false
	}
	if n.leaves == 0 && n.subs == 0 {
		for i := range n.leaf {
			if n.leaf[i].hash == h && eq(n.leaf[i].Key, key) {
				if len(n.leaf) == 1 {
					return nil, true
				}
				return &mapNode[K, V]{leaf: mapRemoveAt(n.leaf, i)}, true
			}
		}
		return n, false
	}
	bit := uint32(1) << mapSlot(h, shift)
	switch {
	case n.leaves&bit != 0:
		i := mapAt(n.leaves, bit)
		if n.leaf[i].hash != h || !eq(n.leaf[i].Key, key) {
			return n, false
		}
		leaves := n.leaves &^ bit
		if leaves == 0 && n.subs == 0 {
			return nil, true
		}
		return &mapNode[K, V]{leaves: leaves, subs: n.subs, leaf: mapRemoveAt(n.leaf, i), sub: n.sub}, true
	case n.subs&bit != 0:
		i := mapAt(n.subs, bit)
		child, removed := mapDelete(n.sub[i], shift+mapBits, h, key, eq)
		if !removed {
			return n, false
		}
		if child != nil {
			return &mapNode[K, V]{leaves: n.leaves, subs: n.subs, leaf: n.leaf, sub: mapSet(n.sub, i, child)}, true
		}
		subs := n.subs &^ bit
		if subs == 0 && n.leaves == 0 {
			return nil, true
		}
		return &mapNode[K, V]{leaves: n.leaves, subs: subs, leaf: n.leaf, sub: mapRemoveAt(n.sub, i)}, true
	}
	return n, false
}

// --- slice surgery ----------------------------------------------------------
//
// Three one-liners rather than `slices.Insert` / `slices.Delete` / a plain
// element assignment, and the reason is the whole correctness argument for a
// persistent structure: those reuse the backing array when capacity allows, so
// they would write through into a node some other Map value still points at. A
// persistent structure that mutates a shared node is wrong in the worst way —
// it shows up as a stale read on an unrelated value, arbitrarily later. These
// always allocate.
//
// The unchanged sibling slice is shared, not copied, in every caller above:
// updating a child never touches `leaf` and updating an entry never touches
// `sub`. Copying both would double a put's allocation for nothing, since
// neither slice is ever written through.

func mapSet[T any](xs []T, i int, x T) []T {
	out := make([]T, len(xs))
	copy(out, xs)
	out[i] = x
	return out
}

func mapInsertAt[T any](xs []T, i int, x T) []T {
	out := make([]T, len(xs)+1)
	copy(out, xs[:i])
	out[i] = x
	copy(out[i+1:], xs[i:])
	return out
}

func mapRemoveAt[T any](xs []T, i int) []T {
	out := make([]T, len(xs)-1)
	copy(out, xs[:i])
	copy(out[i:], xs[i+1:])
	return out
}

// --- equality and rendering -------------------------------------------------

// mapEvery visits unordered trie entries without allocating an ordered view.
// Equality and structural hashing do not depend on insertion order.
func mapEvery[K, V any](n *mapNode[K, V], visit func(mapEntry[K, V]) bool) bool {
	if n == nil {
		return true
	}
	for _, e := range n.leaf {
		if !visit(e) {
			return false
		}
	}
	for _, child := range n.sub {
		if !mapEvery(child, visit) {
			return false
		}
	}
	return true
}

// MapEqual is Nomi's `==` on a Map: structural and order-insensitive.
//
// `{"a" => 1, "b" => 2} == {"b" => 2, "a" => 1}` is True — map equality
// compares contents, not iteration order — so this compares
// sizes and then looks each of a's keys up in b. O(n log32 n).
//
// It cannot be Go's `==`: a Map holds a pointer, and two structurally equal
// maps are two tries. It also cannot walk the two tries in parallel, even
// entry-for-entry in insertion order, because mapDelete does not canonicalize:
// two maps with identical entries may have different shapes.
func MapEqual[K, V any](a, b Map[K, V], hash func(K) uint64, eq func(K, K) bool, valEq func(V, V) bool) bool {
	if a.n != b.n {
		return false
	}
	return mapEvery(a.root, func(e mapEntry[K, V]) bool {
		other, ok := MapLookup(b, hash, eq, e.Key)
		return ok && valEq(e.Val, other)
	})
}

// MapHash is the structural hash of a Map — needed because a Map is itself a
// keyable value, so `{{"a" => 1} => 2}` is legal
// Nomi and a Map-keyed Map has to bucket.
//
// Order-insensitive, by a commutative fold, because MapEqual is: two maps with
// the same entries in different orders are equal and must therefore hash alike.
// It is the one place in the structural hash where the fold may not be
// sequential.
//
// Not to be confused with `Hashable.hash(m)`, which std/maps.nomi defines in
// Nomi over the elements' own `Hashable` impls with a different mix. That one
// is user-visible and user-overridable; this one is neither. See hash.go.
func MapHash[K, V any](m Map[K, V], hashKey func(K) uint64, hashVal func(V) uint64) uint64 {
	var acc uint64
	mapEvery(m.root, func(e mapEntry[K, V]) bool {
		acc += HashMix(hashKey(e.Key), hashVal(e.Val))
		return true
	})
	return acc
}

// FormatMap renders a Map as `{k => v, w => x}` in insertion order, and `{=>}`
// when empty. Like FormatListCells, the braces and the `=>` are all it decides;
// which of Nomi's two renderings you get is decided by the two render arguments.
//
// `impl Display for Map<K, V>` (std/maps.nomi:137) renders key and value with
// Display.to_string, so `${{"a" => 1}}` is `{a => 1}`. An assertion's
// `values:` row renders them with Inspect, so the same map is `{"a" => 1}`
// there. Both are this function:
// pass the identity for the first and InspectString for the second. There is
// deliberately no InspectMap, for the reason there is no InspectList.
func FormatMap[K, V any](m Map[K, V], renderKey func(K) string, renderVal func(V) string) string {
	if m.n == 0 {
		return "{=>}"
	}
	ents := MapEntries(m)
	parts := make([]string, len(ents))
	for i, e := range ents {
		parts[i] = renderKey(e.Key) + " => " + renderVal(e.Val)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
