package rt

import "strings"

// Vector operations for Nomi's `Vector<T>`: the indexed persistent sequence.
//
// # The structure: a bit-partitioned trie with a tail, under a window
//
// A Vector is a window (off, n) over a vecTrie, the persistent vector Clojure
// and Scala use: elements live in leaves of 32, under branches of up to 32
// children, and the last 1 to 32 elements live in a separate tail slice. Every
// node and every tail is immutable once built, so an update copies the nodes on
// one root-to-leaf path and shares the rest, and every older version stays
// valid and unchanged.
//
//	Vector.length       O(1)          the window's n.
//	Vector.get, At      O(log32 n)    one hop per level; a tail read is O(1).
//	Vector.push         O(log32 n)    copies the tail (at most 32 elements),
//	                                  and once in 32 pushes moves the full
//	                                  tail into the trie by a path copy.
//	Vector.set          O(log32 n)    copies one path, or the tail.
//	VectorTail, Slice   O(1)          a narrower window over the same trie, so
//	                                  a recursive `[x, ..rest]` walk is linear.
//	Vector.concat       O(len b)      b's elements appended a tail at a time.
//	VectorOf, iteration O(n)          built and walked a leaf at a time.
//
// An update allocates as few objects as it can: a new tail is an array in the
// same object as the new trie header (vecAllocTail), and a copied leaf or full
// branch holds its array inline. A set or push on a vector of up to 32
// elements is one allocation; TestVectorAllocations pins the counts.
//
// A window that does not reach the trie's end (a slice that dropped trailing
// elements) takes a push as a set of the trie's element just past the window,
// as Clojure's subvec does. A window whose dropped prefix outgrows it (off >=
// n, as a queue made by tail and push becomes) is rebuilt on the next push, so
// a long-running queue does not keep every element it ever held; the rebuild
// costs n and needs at least n tails to be due again.
//
// Element equality, hashing, ordering and rendering are supplied as callbacks;
// the representation stores no dispatch or frontend state. Equality, hashing
// and rendering read the window in order, so a view and a freshly built
// vector of the same elements answer alike.

const (
	vecBits  = 5
	vecWidth = 1 << vecBits
	vecMask  = vecWidth - 1
)

// Vector is Nomi's `Vector<T>`: an immutable indexable sequence.
//
// The zero value is the empty vector. Code outside rt reads a Vector through
// VectorLength, VectorAt, VectorGet, VectorEachWhile and VectorToSlice, and
// builds one with VectorOf; the layout is rt's.
type Vector[T any] struct {
	// t holds the elements; nil only when n is 0.
	t *vecTrie[T]
	// off is the window's first index into t.
	off int
	// n is the number of visible elements, and therefore `Vector.length`.
	n int
}

// vecTrie is one persistent vector: cnt elements, the first tailOff(cnt) of
// them in the trie under root and the rest in tail. A vecTrie is never
// changed after it is built.
type vecTrie[T any] struct {
	cnt int
	// shift is the bit offset of root's level: 5 for a root whose children
	// are leaves, 10 for one more level, and so on. Leaves are level 0.
	shift uint
	// root is nil while every element fits in the tail.
	root *vecNode[T]
	// tail is the last 1 to 32 elements: len(tail) == cnt - tailOff(cnt).
	tail []T
	// edit is the owner token of a trie an owned operation built
	// (transient.go), and nil on every other: such a header, its tail and
	// each node carrying the same token belong to one owned value, which
	// writes them in place. The token is the header's own field own, so
	// minting one allocates nothing.
	edit *editToken
	own  editToken
}

// vecNode is a trie node: a branch with up to 32 children, or a leaf of
// exactly 32 elements.
type vecNode[T any] struct {
	kids []*vecNode[T]
	leaf []T
	// edit is the owner token of the trie that may write this node in
	// place, or nil (transient.go).
	edit *editToken
}

// vecTailOff is the index of the first element held in the tail of a trie of
// cnt elements: the tail holds 1 to 32 elements whenever cnt > 0.
func vecTailOff(cnt int) int {
	if cnt < vecWidth {
		return 0
	}
	return ((cnt - 1) >> vecBits) << vecBits
}

// chunk is the leaf or tail holding index i, and the index of its first
// element.
func (t *vecTrie[T]) chunk(i int) ([]T, int) {
	if off := vecTailOff(t.cnt); i >= off {
		return t.tail, off
	}
	node := t.root
	for level := t.shift; level > 0; level -= vecBits {
		vecCountVisit()
		node = node.kids[(i>>level)&vecMask]
	}
	vecCountVisit()
	return node.leaf, i &^ vecMask
}

func (t *vecTrie[T]) get(i int) T {
	c, base := t.chunk(i)
	return c[i-base]
}

// push is t with x appended.
func (t *vecTrie[T]) push(x T) *vecTrie[T] {
	if t == nil || t.cnt == 0 {
		h, tail := vecAllocTail[T](1)
		tail[0] = x
		h.cnt, h.shift = 1, vecBits
		return h
	}
	if n := len(t.tail); n < vecWidth {
		h, tail := vecAllocTail[T](n + 1)
		vecCountCopy(copy(tail, t.tail))
		tail[n] = x
		h.cnt, h.shift, h.root = t.cnt+1, t.shift, t.root
		return h
	}
	// The tail is full: it becomes a leaf of the trie, unchanged and shared,
	// and x starts the next tail.
	leaf := vecNewNode[T](nil, t.tail)
	root, shift := t.root, t.shift
	if t.cnt>>vecBits > 1<<shift {
		// The trie is full at this height: a new root over the old one and
		// a fresh path down to the leaf.
		root = vecNewNode([]*vecNode[T]{t.root, vecPath(shift, leaf)}, nil)
		shift += vecBits
	} else {
		root = vecPushLeaf(t.cnt, shift, t.root, leaf)
	}
	h, tail := vecAllocTail[T](1)
	tail[0] = x
	h.cnt, h.shift, h.root = t.cnt+1, shift, root
	return h
}

// vecPushLeaf copies the path from node (at level) down to where the leaf
// holding the elements just before index cnt goes, and places leaf there.
func vecPushLeaf[T any](cnt int, level uint, node *vecNode[T], leaf *vecNode[T]) *vecNode[T] {
	sub := ((cnt - 1) >> level) & vecMask
	var kids []*vecNode[T]
	if node != nil {
		kids = node.kids
	}
	node, out := vecAllocBranch[T](max(len(kids), sub+1))
	vecCountCopy(copy(out, kids))
	switch {
	case level == vecBits:
		out[sub] = leaf
	case sub < len(kids):
		out[sub] = vecPushLeaf(cnt, level-vecBits, kids[sub], leaf)
	default:
		out[sub] = vecPath(level-vecBits, leaf)
	}
	return node
}

// vecPath is a chain of single-child branches from level down to leaf.
func vecPath[T any](level uint, leaf *vecNode[T]) *vecNode[T] {
	if level == 0 {
		return leaf
	}
	return vecNewNode([]*vecNode[T]{vecPath(level-vecBits, leaf)}, nil)
}

// set is t with the element at i (in range) replaced by x.
func (t *vecTrie[T]) set(i int, x T) *vecTrie[T] {
	if off := vecTailOff(t.cnt); i >= off {
		h, tail := vecAllocTail[T](len(t.tail))
		vecCountCopy(copy(tail, t.tail))
		tail[i-off] = x
		h.cnt, h.shift, h.root = t.cnt, t.shift, t.root
		return h
	}
	return &vecTrie[T]{cnt: t.cnt, shift: t.shift, root: vecSetIn(t.shift, t.root, i, x), tail: t.tail}
}

func vecSetIn[T any](level uint, node *vecNode[T], i int, x T) *vecNode[T] {
	if level == 0 {
		out := vecAllocLeaf[T]()
		vecCountCopy(copy(out.leaf, node.leaf))
		out.leaf[i&vecMask] = x
		return out
	}
	sub := (i >> level) & vecMask
	out, kids := vecAllocBranch[T](len(node.kids))
	vecCountCopy(copy(kids, node.kids))
	kids[sub] = vecSetIn(level-vecBits, node.kids[sub], i, x)
	return out
}

// appendAll is t with xs appended, a tail at a time: each step copies at most
// one tail and moves at most one full tail into the trie, so the cost is
// linear in len(xs).
func (t *vecTrie[T]) appendAll(xs []T) *vecTrie[T] {
	for len(xs) > 0 {
		if t == nil || t.cnt == 0 || len(t.tail) == vecWidth {
			t, xs = t.push(xs[0]), xs[1:]
			continue
		}
		k := min(vecWidth-len(t.tail), len(xs))
		h, tail := vecAllocTail[T](len(t.tail) + k)
		vecCountCopy(copy(tail, t.tail))
		vecCountCopy(copy(tail[len(t.tail):], xs[:k]))
		h.cnt, h.shift, h.root = t.cnt+k, t.shift, t.root
		t, xs = h, xs[k:]
	}
	return t
}

// vecFromOwned builds a trie over items, which the caller gives up: its
// leaves and tail are windows of items' array. Nothing appends into a leaf or
// a tail (an update copies it), so the windows need no capacity cap.
func vecFromOwned[T any](items []T) *vecTrie[T] {
	n := len(items)
	if n == 0 {
		return nil
	}
	off := vecTailOff(n)
	t := &vecTrie[T]{cnt: n, shift: vecBits, tail: items[off:]}
	if off == 0 {
		return t
	}
	level := make([]*vecNode[T], 0, off/vecWidth)
	for i := 0; i < off; i += vecWidth {
		level = append(level, vecNewNode[T](nil, items[i:i+vecWidth:i+vecWidth]))
	}
	for len(level) > vecWidth {
		up := make([]*vecNode[T], 0, (len(level)+vecMask)/vecWidth)
		for i := 0; i < len(level); i += vecWidth {
			j := min(i+vecWidth, len(level))
			up = append(up, vecNewNode(level[i:j:j], nil))
		}
		level = up
		t.shift += vecBits
	}
	t.root = vecNewNode(level, nil)
	return t
}

func vecNewNode[T any](kids []*vecNode[T], leaf []T) *vecNode[T] {
	vecCountNode()
	return &vecNode[T]{kids: kids, leaf: leaf}
}

// vecAllocTail is a trie header and its tail of n (1 to 32) elements in one
// allocation: the tail is an array in the same object as the header, so a
// set or push on a vector that fits in its tail allocates once. The array's
// length is the smallest of a few sizes that holds n, so a 6-element tail
// does not carry 32 slots. The caller fills the tail and the header's other
// fields.
func vecAllocTail[T any](n int) (*vecTrie[T], []T) {
	switch {
	case n <= 1:
		b := new(struct {
			h vecTrie[T]
			a [1]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	case n <= 2:
		b := new(struct {
			h vecTrie[T]
			a [2]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	case n <= 4:
		b := new(struct {
			h vecTrie[T]
			a [4]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	case n <= 6:
		b := new(struct {
			h vecTrie[T]
			a [6]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	case n <= 8:
		b := new(struct {
			h vecTrie[T]
			a [8]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	case n <= 12:
		b := new(struct {
			h vecTrie[T]
			a [12]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	case n <= 16:
		b := new(struct {
			h vecTrie[T]
			a [16]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	case n <= 24:
		b := new(struct {
			h vecTrie[T]
			a [24]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	default:
		b := new(struct {
			h vecTrie[T]
			a [vecWidth]T
		})
		b.h.tail = b.a[:n:n]
		return &b.h, b.h.tail
	}
}

// vecAllocLeaf is a leaf node and its 32 elements in one allocation, for the
// caller to fill.
func vecAllocLeaf[T any]() *vecNode[T] {
	vecCountNode()
	b := new(struct {
		n vecNode[T]
		a [vecWidth]T
	})
	b.n.leaf = b.a[:]
	return &b.n
}

// vecAllocBranch is a branch node and its n (1 to 32) child slots in one
// allocation, for the caller to fill. Only a full branch carries its array
// inline: a branch of fewer children, a root or the end of the trie, takes
// its slots as a separate exact slice rather than 32 slots it mostly leaves
// empty.
func vecAllocBranch[T any](n int) (*vecNode[T], []*vecNode[T]) {
	vecCountNode()
	if n == vecWidth {
		b := new(struct {
			n vecNode[T]
			a [vecWidth]*vecNode[T]
		})
		b.n.kids = b.a[:]
		return &b.n, b.n.kids
	}
	kids := make([]*vecNode[T], n)
	return &vecNode[T]{kids: kids}, kids
}

// --- construction -----------------------------------------------------------

// VectorOf builds `[a, b, c]`, copying the elements so the caller's slice
// cannot change the vector later.
func VectorOf[T any](elems []T) Vector[T] {
	if len(elems) == 0 {
		return Vector[T]{}
	}
	if len(elems) <= vecWidth {
		h, tail := vecAllocTail[T](len(elems))
		copy(tail, elems)
		h.cnt, h.shift = len(elems), vecBits
		return Vector[T]{t: h, n: len(elems)}
	}
	copied := make([]T, len(elems))
	copy(copied, elems)
	return Vector[T]{t: vecFromOwned(copied), n: len(copied)}
}

// vectorPreallocCap bounds the capacity SeqToVector reserves from a source's
// `known_count`. A List, Vector, Set or Map counts itself exactly, but a
// Range's count comes from the element's `Discrete.steps_between`, which a
// program writes; a wrong answer there must cost a regrow, not an allocation
// of whatever size it names.
const vectorPreallocCap = 1 << 20

// SeqToVector is `Iter.to_vector`: the source's elements in push order, built
// in one pass with no intermediate List. When the source answers
// `known_count` with `Some(n)` the slice the elements are gathered in is
// allocated once at that size (up to vectorPreallocCap); a lazy chain
// declines and the slice grows. The trie's leaves are then windows of that
// slice. The source is driven through its `each_while`, so an adapter chain
// fuses into the one walk.
func SeqToVector[T any](fr *Frame, src Seq[T]) Vector[T] {
	var items []T
	if n := SeqKnownCount(fr, src); n.Tag == TagSome && n.Some > 0 {
		items = make([]T, 0, min(n.Some, vectorPreallocCap))
	}
	src.Run(fr, func(_ *Frame, item T) bool {
		items = append(items, item)
		return true
	})
	if len(items) == 0 {
		return Vector[T]{}
	}
	return Vector[T]{t: vecFromOwned(items), n: len(items)}
}

// --- indexed access ---------------------------------------------------------

// VectorLength is `Vector.length`. O(1): a field read, and std documents the
// complexity explicitly.
func VectorLength[T any](v Vector[T]) int64 { return int64(v.n) }

// Len is the number of elements, as VectorLength.
func (v Vector[T]) Len() int { return v.n }

// At is the element at a zero-based index the caller knows is in the vector.
// An index outside it traps, as VectorAt does.
func (v Vector[T]) At(i int) T { return VectorAt(v, int64(i)) }

// VectorGet is `Vector.get`: the element at a zero-based index, or None out of
// bounds.
//
// A negative index is None rather than a wrap-around, and the corpus reads it: `Vector.get(v, -1) == None`.
func VectorGet[T any](v Vector[T], index int64) Maybe[T] {
	if !vectorInRange(v, index) {
		return None[T]()
	}
	return Some(v.t.get(v.off + int(index)))
}

// VectorAt is the element at a zero-based index the caller knows is in the
// vector, as a comparison walking it does. An index outside the vector traps
// with IndexOutOfRangeText; `Vector.get` is the total form.
func VectorAt[T any](v Vector[T], index int64) T {
	if !vectorInRange(v, index) {
		Trap(IndexOutOfRangeText("Vector", index, int64(v.n)))
	}
	return v.t.get(v.off + int(index))
}

// vectorInRange is the bounds rule `get`, `set` and VectorAt share, so the
// three agree about what "in range" is.
func vectorInRange[T any](v Vector[T], index int64) bool {
	return index >= 0 && index < int64(v.n)
}

// --- structural updates -----------------------------------------------------

// VectorPush is `Vector.push`: a new vector with item appended. v is
// unchanged: the new version shares every node of v's trie but one path and
// the tail. See the header for a window that does not reach the trie's end.
func VectorPush[T any](v Vector[T], item T) Vector[T] {
	if v.n == 0 {
		return Vector[T]{t: (*vecTrie[T])(nil).push(item), n: 1}
	}
	if v.off >= v.n {
		v = Vector[T]{t: vecFromOwned(VectorToSlice(v)), n: v.n}
	}
	if end := v.off + v.n; end < v.t.cnt {
		return Vector[T]{t: v.t.set(end, item), off: v.off, n: v.n + 1}
	}
	return Vector[T]{t: v.t.push(item), off: v.off, n: v.n + 1}
}

// VectorSet is `Vector.set`: a new vector with `index` replaced. An index
// outside the vector traps with IndexOutOfRangeText. The bounds rule is
// vectorInRange.
func VectorSet[T any](v Vector[T], index int64, item T) Vector[T] {
	if !vectorInRange(v, index) {
		Trap(IndexOutOfRangeText("Vector", index, int64(v.n)))
	}
	return Vector[T]{t: v.t.set(v.off+int(index), item), off: v.off, n: v.n}
}

// VectorConcat is `Vector.concat`: a followed by b. Either side empty answers
// the other, which is safe because neither can change. Otherwise b's elements
// are appended to a's trie when a's window reaches its end and has no stale
// prefix to drop, and to a fresh copy of a's window when not.
func VectorConcat[T any](a, b Vector[T]) Vector[T] {
	if a.n == 0 {
		return b
	}
	if b.n == 0 {
		return a
	}
	t := a.t
	if a.off >= a.n || a.off+a.n != t.cnt {
		t, a.off = vecFromOwned(VectorToSlice(a)), 0
	}
	return Vector[T]{t: t.appendAll(VectorToSlice(b)), off: a.off, n: a.n + b.n}
}

// VectorTail is the window without its first element, in O(1).
//
// Not part of std's public surface (`next_item` is), but separated because
// `each_while`, `compare` and `next_item` all need it and a shared
// implementation keeps the window arithmetic in one place.
func VectorTail[T any](v Vector[T]) Vector[T] {
	if v.n <= 1 {
		return Vector[T]{}
	}
	return Vector[T]{t: v.t, off: v.off + 1, n: v.n - 1}
}

// VectorSlice is the window of v from position from up to drop elements
// before its end, in O(1): the `..rest` of a vector pattern
// `[a, ..rest, z]`, with from the heads and drop the tails. Like
// VectorTail it shares v's trie, so a recursive `[x, ..rest]` walk
// reads each element once instead of copying the rest at every step. A
// window that would be empty or negative is the empty vector.
func VectorSlice[T any](v Vector[T], from, drop int) Vector[T] {
	n := v.n - from - drop
	if from < 0 || drop < 0 || n <= 0 {
		return Vector[T]{}
	}
	return Vector[T]{t: v.t, off: v.off + from, n: n}
}

// --- iteration --------------------------------------------------------------

// vectorChunks yields v's elements a leaf at a time: each slice is the part of
// one leaf or of the tail inside the window, in order. It stops early when
// yield returns false, and reports whether it ran to the end.
func vectorChunks[T any](v Vector[T], yield func([]T) bool) bool {
	end := v.off + v.n
	for i := v.off; i < end; {
		c, base := v.t.chunk(i)
		part := c[i-base : min(len(c), end-base)]
		if !yield(part) {
			return false
		}
		i += len(part)
	}
	return true
}

// VectorToSlice is v's elements in order, in a new slice the caller owns.
func VectorToSlice[T any](v Vector[T]) []T {
	out := make([]T, 0, v.n)
	vectorChunks(v, func(c []T) bool {
		out = append(out, c...)
		return true
	})
	return out
}

// VectorEachWhile is `impl Iter for Vector<T>`'s `each_while`.
//
// std's own comment at that declaration says what this must be: the push loop
// is an index walk with no per-element view to rebuild. So it walks the
// window a leaf at a time and allocates nothing per element; a `next_item`
// loop would build one `Maybe` and one tuple per element to throw both away.
func VectorEachWhile[T any](fr *Frame, v Vector[T], yield func(fr *Frame, item T) bool) bool {
	return vectorChunks(v, func(c []T) bool {
		for _, x := range c {
			if !yield(fr, x) {
				return false
			}
		}
		return true
	})
}

// VectorSeq views a Vector as a push sequence.
//
// The walk is inside `Run` so the sequence is replayable and holds no
// materialized slice between consumptions — MapSeq's rule.
func VectorSeq[T any](v Vector[T]) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		return VectorEachWhile(fr, v, yield)
	}}
}

// VectorKnownCount is `impl Iter for Vector<T>`'s `known_count` override: std
// answers `Some(length(vector))`, which is O(1). Keeping it O(1) is the point —
// `Iter.count` over a Vector must not degrade to a walk behind a name std
// documents as free.
func VectorKnownCount[T any](v Vector[T]) Maybe[int64] { return Some(VectorLength(v)) }

// --- identity ---------------------------------------------------------------

// VectorEqual is `impl Equatable for Vector<T>` — whose std body is literally
// `a == b`, i.e. structural equality, element-wise in order.
//
// Length first, which is sound for equality and is not sound for ordering: two
// vectors of different lengths are never equal, while two of different lengths
// may still order by their first differing element. ListCellsEqual makes the
// same split for the same reason.
func VectorEqual[T any](a, b Vector[T], eq func(T, T) bool) bool {
	if a.n != b.n {
		return false
	}
	i := 0
	return vectorChunks(a, func(c []T) bool {
		// b's matching run, read a leaf of b at a time.
		for len(c) > 0 {
			bc, base := b.t.chunk(b.off + i)
			bc = bc[b.off+i-base:]
			k := min(len(c), len(bc))
			for j := range k {
				if !eq(c[j], bc[j]) {
					return false
				}
			}
			c, i = c[k:], i+k
		}
		return true
	})
}

// VectorContains is `impl Contains<T> for Vector<T>`: whether an element is
// eq to x, scanned in order and stopping at the first match. eq is the
// element's `==`, so the scan makes one comparison per element it visits and
// calls nothing else.
func VectorContains[T any](v Vector[T], x T, eq func(T, T) bool) bool {
	return !VectorEachWhile(nil, v, func(_ *Frame, item T) bool { return !eq(item, x) })
}

// --- rendering --------------------------------------------------------------

// FormatVector renders a Vector as `Vector[a, b]`, and `Vector[]` when empty.
//
// The brackets and the separator are all it decides; which of Nomi's renderings
// you get is decided by `render`, exactly as in FormatListCells, FormatMap and
// FormatSetElems. `impl Display for Vector<T>` joins `Display.to_string` and `impl
// Debug for Vector<T>` joins `Debug.inspect` — std writes the two impls as the
// same fold over different element renderers — and an assertion's `values:`
// row joins RowText. All three
// are this function with a different `render`.
func FormatVector[T any](v Vector[T], render func(T) string) string {
	if v.n == 0 {
		return "Vector[]"
	}
	parts := make([]string, 0, v.n)
	VectorEachWhile(nil, v, func(_ *Frame, x T) bool {
		parts = append(parts, render(x))
		return true
	})
	return "Vector[" + strings.Join(parts, ", ") + "]"
}
