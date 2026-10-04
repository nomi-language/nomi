package rt

import "strings"

// Vector operations for Nomi's `Vector<T>`.
// A visible window over a backing slice makes length and indexed access O(1),
// and lets tail views share storage without copying. Structural updates copy
// only the visible window, preserving every existing snapshot. Element equality,
// hashing, ordering and rendering are supplied as callbacks; the representation
// stores no dispatch or frontend state.

// Vector is Nomi's `Vector<T>`: an immutable indexable sequence.
//
// The zero value is the empty vector. Fields are exported because a generated
// package in another Go package constructs and reads them.
type Vector[T any] struct {
	// Items is the shared backing store. It may be LONGER than the window:
	// Tail advances Start without copying, so Items[Start+Len:] is retained and
	// invisible. Never index it directly — use At.
	Items []T
	// Start is the window's offset into Items.
	Start int
	// Len is the number of visible elements, and therefore `Vector.length`.
	Len int
}

// --- construction -----------------------------------------------------------

// VectorOf builds `#[a, b, c]`, copying the elements so the literal's slice
// cannot be aliased by a later push into the same backing array.
func VectorOf[T any](elems []T) Vector[T] {
	if len(elems) == 0 {
		return Vector[T]{}
	}
	copied := make([]T, len(elems))
	copy(copied, elems)
	return Vector[T]{Items: copied, Len: len(copied)}
}

// vectorPreallocCap bounds the capacity SeqToVector reserves from a source's
// `known_count`. A List, Vector, Set or Map counts itself exactly, but a
// Range's count comes from the element's `Discrete.steps_between`, which a
// program writes; a wrong answer there must cost a regrow, not an allocation
// of whatever size it names.
const vectorPreallocCap = 1 << 20

// SeqToVector is `Iter.to_vector`: the source's elements in push order, built
// in one pass with no intermediate List. When the source answers
// `known_count` with `Some(n)` the backing slice is allocated once at that
// size (up to vectorPreallocCap); a lazy chain declines and the slice grows.
// The source is driven through its `each_while`, so an adapter chain fuses
// into the one walk.
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
	return Vector[T]{Items: items, Len: len(items)}
}

// --- indexed access ---------------------------------------------------------

// VectorLength is `Vector.length`. O(1) — a field read, and std documents the
// complexity explicitly. See the header.
func VectorLength[T any](v Vector[T]) int64 { return int64(v.Len) }

// VectorAt is `Vector.at`: the element at a zero-based index, or None out of
// bounds.
//
// A NEGATIVE index is None rather than a wrap-around, and the corpus reads it: `Vector.at(v, -1) == None`.
func VectorAt[T any](v Vector[T], index int64) Maybe[T] {
	if index < 0 || index >= int64(v.Len) {
		return None[T]()
	}
	return Some(v.Items[v.Start+int(index)])
}

// --- structural updates -----------------------------------------------------

// VectorPush is `Vector.push`: a new vector with item appended.
//
// It copies the window rather than appending into `Items`, and that is
// correctness rather than caution: `Items` is SHARED with every view that took a
// tail or a push from the same parent, so appending in place would mutate a
// value another binding still holds. The boxed runtime delegates here too.
func VectorPush[T any](v Vector[T], item T) Vector[T] {
	out := make([]T, v.Len+1)
	copy(out, v.Items[v.Start:v.Start+v.Len])
	out[v.Len] = item
	return Vector[T]{Items: out, Len: len(out)}
}

// VectorSet is `Vector.set`: a new vector with `index` replaced, or None when
// out of bounds.
//
// Copies for VectorPush's reason. The bounds rule is VectorAt's, shared through
// the same test rather than restated — an in-range `set` and an in-range `at`
// must agree about what "in range" is.
func VectorSet[T any](v Vector[T], index int64, item T) Maybe[Vector[T]] {
	if index < 0 || index >= int64(v.Len) {
		return None[Vector[T]]()
	}
	out := make([]T, v.Len)
	copy(out, v.Items[v.Start:v.Start+v.Len])
	out[int(index)] = item
	return Some(Vector[T]{Items: out, Len: len(out)})
}

// VectorConcat is `Vector.concat`: a followed by b.
func VectorConcat[T any](a, b Vector[T]) Vector[T] {
	if a.Len == 0 {
		return VectorOf(b.Items[b.Start : b.Start+b.Len])
	}
	if b.Len == 0 {
		return VectorOf(a.Items[a.Start : a.Start+a.Len])
	}
	out := make([]T, 0, a.Len+b.Len)
	out = append(out, a.Items[a.Start:a.Start+a.Len]...)
	out = append(out, b.Items[b.Start:b.Start+b.Len]...)
	return Vector[T]{Items: out, Len: len(out)}
}

// VectorTail is the window without its first element, in O(1).
//
// The one operation the three-field layout exists for. Not part of std's public
// surface — `next_item` is — but separated because `each_while`, `compare` and
// `next_item` all need it and a shared implementation is what keeps the window
// arithmetic in one place.
func VectorTail[T any](v Vector[T]) Vector[T] {
	if v.Len <= 1 {
		return Vector[T]{}
	}
	return Vector[T]{Items: v.Items, Start: v.Start + 1, Len: v.Len - 1}
}

// --- iteration --------------------------------------------------------------

// VectorEachWhile is `impl Iter for Vector<T>`'s `each_while`.
//
// std's own comment at that declaration says what this must be: "Host-side: a
// Vector is a windowed slice, so the push loop is an index walk with no
// per-element view to rebuild." So it walks the window directly and allocates
// nothing per element — a `next_item` loop would build one `Maybe` and one tuple
// per element to throw both away.
func VectorEachWhile[T any](fr *Frame, v Vector[T], yield func(fr *Frame, item T) bool) bool {
	for i := range v.Len {
		if !yield(fr, v.Items[v.Start+i]) {
			return false
		}
	}
	return true
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
// `a == b`, i.e. STRUCTURAL equality, element-wise in order.
//
// Length first, which is sound for equality and is NOT sound for ordering: two
// vectors of different lengths are never equal, while two of different lengths
// may still order by their first differing element. ListCellsEqual makes the
// same split for the same reason.
func VectorEqual[T any](a, b Vector[T], eq func(T, T) bool) bool {
	if a.Len != b.Len {
		return false
	}
	for i := range a.Len {
		if !eq(a.Items[a.Start+i], b.Items[b.Start+i]) {
			return false
		}
	}
	return true
}

// --- rendering --------------------------------------------------------------

// FormatVector renders a Vector as `#[a, b]`, and `#[]` when empty.
//
// The BRACKETS and the separator are all it decides; which of Nomi's renderings
// you get is decided by `render`, exactly as in FormatListCells, FormatMap and
// FormatSetElems. `impl Display for Vector<T>` joins `Display.to_string` and `impl
// Debug for Vector<T>` joins `Debug.inspect` — std writes the two impls as the
// same fold over different element renderers — and an assertion's `values:`
// row joins RowText. All three
// are this function with a different `render`.
func FormatVector[T any](v Vector[T], render func(T) string) string {
	if v.Len == 0 {
		return "#[]"
	}
	parts := make([]string, v.Len)
	for i := range v.Len {
		parts[i] = render(v.Items[v.Start+i])
	}
	return "#[" + strings.Join(parts, ", ") + "]"
}
