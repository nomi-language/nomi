package rt

import "slices"

// Collections written in place.
//
// Every Vector and Map operation answers a new value and leaves its operand
// as it was (vector.go, map.go). A caller that can prove nothing reads the
// operand again (the VM's ownership analysis, internal/vm package doc,
// "Updates written in place") may call the Owned form instead, which
// writes into the operand's storage where it may and answers the result.
// The result is the same value the persistent form answers; only the
// operand, which nobody reads again, may differ.
//
// What "where it may" means follows Clojure's transients. An Owned
// operation stamps every node it builds or copies with an owner token
// (editToken), the token of the value's lineage: a Vector's header carries
// it, and a Map's root does. A node carrying the lineage's token was built
// by an earlier Owned operation on this same value and is reachable from
// nothing else, so the operation writes it in place. Any other node (one
// a persistent operation built, one a literal or Iter.to_vector built, one
// shared with another value) is copied once, stamped, and written from
// then on. A value whose header or root carries no token gets a new token
// on its first Owned operation, so two values never share one.
//
// Nothing mutates a node another live value can reach:
//
//   - A persistent operation never stamps, so a value derived from an
//     owned one by a persistent operation (a value the caller kept) has a
//     header or root without a token or with its own, and its first Owned
//     operation copies every stamped node it touches.
//   - The caller's proof is that the operand has one holder, which the
//     operation consumes: no other value holds the header or root that
//     carries the token.
//   - A window over part of a Vector's trie (a slice, a tail) shares the
//     header with the vector it came from, so an Owned operation on one
//     falls back to the persistent form.

// editToken is an owner token: its identity is all it carries. It has a
// size so that every one is a distinct allocation.
type editToken struct{ _ byte }

// owned is t's header, or, when t carries no token, a copy of it whose
// token is its own field: the copy is one allocation, the header and its
// tail together (vecAllocTail), and pushOwned gives the tail room to grow
// when it first appends.
func (t *vecTrie[T]) owned() *vecTrie[T] {
	if t != nil && t.edit != nil {
		return t
	}
	var h *vecTrie[T]
	if t != nil && t.cnt > 0 {
		var tail []T
		h, tail = vecAllocTail[T](len(t.tail))
		vecCountCopy(copy(tail, t.tail))
		h.cnt, h.shift, h.root = t.cnt, t.shift, t.root
	} else {
		h = &vecTrie[T]{shift: vecBits}
	}
	h.edit = &h.own
	return h
}

// VectorPushOwned is VectorPush, written into v's own storage where v's
// lineage owns it. The caller gives v up.
func VectorPushOwned[T any](v Vector[T], item T) Vector[T] {
	var t *vecTrie[T]
	switch {
	case v.n == 0:
		t = (*vecTrie[T])(nil).owned()
	case v.off != 0 || v.n != v.t.cnt:
		return VectorPush(v, item)
	default:
		t = v.t.owned()
	}
	t.pushOwned(item)
	return Vector[T]{t: t, n: t.cnt}
}

// pushOwned appends x to the owned trie t.
func (t *vecTrie[T]) pushOwned(x T) {
	if len(t.tail) < vecWidth {
		if len(t.tail) == cap(t.tail) {
			tail := make([]T, len(t.tail), vecWidth)
			vecCountCopy(copy(tail, t.tail))
			t.tail = tail
		}
		t.tail = append(t.tail, x)
		t.cnt++
		return
	}
	// The full tail, the header's own array, becomes a leaf of the trie,
	// and x starts the next tail.
	e := t.edit
	vecCountNode()
	leaf := &vecNode[T]{leaf: t.tail, edit: e}
	if t.cnt>>vecBits > 1<<t.shift {
		kids := make([]*vecNode[T], 2, vecWidth)
		kids[0], kids[1] = t.root, vecPathOwned(e, t.shift, leaf)
		vecCountNode()
		t.root = &vecNode[T]{kids: kids, edit: e}
		t.shift += vecBits
	} else {
		t.root = vecPushLeafOwned(e, t.cnt, t.shift, t.root, leaf)
	}
	t.tail = make([]T, 1, vecWidth)
	t.tail[0] = x
	t.cnt++
}

// vecOwnBranch is node when e owns it, else a copy of it e owns, with room
// for 32 children.
func vecOwnBranch[T any](e *editToken, node *vecNode[T]) *vecNode[T] {
	if node != nil && node.edit == e {
		return node
	}
	vecCountNode()
	out := &vecNode[T]{kids: make([]*vecNode[T], 0, vecWidth), edit: e}
	if node != nil {
		out.kids = append(out.kids, node.kids...)
		vecCountCopy(len(node.kids))
	}
	return out
}

// vecPushLeafOwned places leaf where the elements just before index cnt
// go, under node at level, writing the nodes e owns in place.
func vecPushLeafOwned[T any](e *editToken, cnt int, level uint, node, leaf *vecNode[T]) *vecNode[T] {
	sub := ((cnt - 1) >> level) & vecMask
	node = vecOwnBranch(e, node)
	had := len(node.kids)
	for len(node.kids) <= sub {
		node.kids = append(node.kids, nil)
	}
	switch {
	case level == vecBits:
		node.kids[sub] = leaf
	case sub < had:
		node.kids[sub] = vecPushLeafOwned(e, cnt, level-vecBits, node.kids[sub], leaf)
	default:
		node.kids[sub] = vecPathOwned(e, level-vecBits, leaf)
	}
	return node
}

// vecPathOwned is vecPath with each branch e's.
func vecPathOwned[T any](e *editToken, level uint, leaf *vecNode[T]) *vecNode[T] {
	if level == 0 {
		return leaf
	}
	kids := make([]*vecNode[T], 1, vecWidth)
	kids[0] = vecPathOwned(e, level-vecBits, leaf)
	vecCountNode()
	return &vecNode[T]{kids: kids, edit: e}
}

// VectorSetOwned is VectorSet, written into v's own storage where v's
// lineage owns it. The caller gives v up.
func VectorSetOwned[T any](v Vector[T], index int64, item T) Vector[T] {
	if !vectorInRange(v, index) {
		Trap(IndexOutOfRangeText("Vector", index, int64(v.n)))
	}
	if v.off != 0 || v.n != v.t.cnt {
		return VectorSet(v, index, item)
	}
	t := v.t.owned()
	i := int(index)
	if off := vecTailOff(t.cnt); i >= off {
		t.tail[i-off] = item
	} else {
		t.root = vecSetOwned(t.edit, t.shift, t.root, i, item)
	}
	return Vector[T]{t: t, n: v.n}
}

// vecSetOwned writes x at i under node at level, copying each node on the
// path e does not own.
func vecSetOwned[T any](e *editToken, level uint, node *vecNode[T], i int, x T) *vecNode[T] {
	if level == 0 {
		if node.edit != e {
			out := vecAllocLeaf[T]()
			vecCountCopy(copy(out.leaf, node.leaf))
			out.edit = e
			node = out
		}
		node.leaf[i&vecMask] = x
		return node
	}
	node = vecOwnBranch(e, node)
	sub := (i >> level) & vecMask
	node.kids[sub] = vecSetOwned(e, level-vecBits, node.kids[sub], i, x)
	return node
}

// mapToken is the owner token of the map whose root is root: the root's,
// or a new one.
func mapToken[K, V any](root *mapNode[K, V]) *editToken {
	if root != nil && root.edit != nil {
		return root.edit
	}
	return &editToken{}
}

// mapOwn is n when e owns it, else a copy e owns, with slices of its own.
func mapOwn[K, V any](e *editToken, n *mapNode[K, V]) *mapNode[K, V] {
	if n.edit == e {
		return n
	}
	mapCountNode()
	return &mapNode[K, V]{leaves: n.leaves, subs: n.subs, leaf: slices.Clone(n.leaf), sub: slices.Clone(n.sub), edit: e}
}

// MapPutOwned is MapPut, written into m's own nodes where m's lineage owns
// them. The caller gives m up.
func MapPutOwned[K, V any](m Map[K, V], hash func(K) uint64, eq func(K, K) bool, key K, val V) Map[K, V] {
	e := mapEntry[K, V]{MapEntry: MapEntry[K, V]{Key: key, Val: val}, hash: hash(key), seq: m.seq}
	root, added := mapInsertOwned(mapToken(m.root), m.root, 0, e, eq)
	out := Map[K, V]{root: root, n: m.n, seq: m.seq}
	if added {
		out.n++
		out.seq++
	}
	return out
}

// mapInsertOwned is mapInsert, writing the nodes tok owns in place.
func mapInsertOwned[K, V any](tok *editToken, n *mapNode[K, V], shift uint, e mapEntry[K, V], eq func(K, K) bool) (*mapNode[K, V], bool) {
	if n == nil {
		mapCountNode()
		return &mapNode[K, V]{leaves: 1 << mapSlot(e.hash, shift), leaf: []mapEntry[K, V]{e}, edit: tok}, true
	}
	if n.leaves == 0 && n.subs == 0 {
		n = mapOwn(tok, n)
		for i := range n.leaf {
			if eq(n.leaf[i].Key, e.Key) {
				e.seq = n.leaf[i].seq
				n.leaf[i] = e
				return n, false
			}
		}
		n.leaf = append(n.leaf, e)
		return n, true
	}
	bit := uint32(1) << mapSlot(e.hash, shift)
	switch {
	case n.leaves&bit != 0:
		i := mapAt(n.leaves, bit)
		old := n.leaf[i]
		n = mapOwn(tok, n)
		if old.hash == e.hash && eq(old.Key, e.Key) {
			e.seq = old.seq
			n.leaf[i] = e
			return n, false
		}
		j := mapAt(n.subs, bit)
		n.leaves &^= bit
		n.subs |= bit
		n.leaf = slices.Delete(n.leaf, i, i+1)
		n.sub = slices.Insert(n.sub, j, mapSplit(shift+mapBits, old, e))
		return n, true
	case n.subs&bit != 0:
		i := mapAt(n.subs, bit)
		n = mapOwn(tok, n)
		child, added := mapInsertOwned(tok, n.sub[i], shift+mapBits, e, eq)
		n.sub[i] = child
		return n, added
	default:
		n = mapOwn(tok, n)
		n.leaf = slices.Insert(n.leaf, mapAt(n.leaves, bit), e)
		n.leaves |= bit
		return n, true
	}
}

// MapRemoveOwned is MapRemove, written into m's own nodes where m's
// lineage owns them. The caller gives m up.
func MapRemoveOwned[K, V any](m Map[K, V], hash func(K) uint64, eq func(K, K) bool, key K) Map[K, V] {
	root, removed := mapDeleteOwned(mapToken(m.root), m.root, 0, hash(key), key, eq)
	if !removed {
		return m
	}
	return Map[K, V]{root: root, n: m.n - 1, seq: m.seq}
}

// mapDeleteOwned is mapDelete, writing the nodes tok owns in place.
func mapDeleteOwned[K, V any](tok *editToken, n *mapNode[K, V], shift uint, h uint64, key K, eq func(K, K) bool) (*mapNode[K, V], bool) {
	if n == nil {
		return nil, false
	}
	if n.leaves == 0 && n.subs == 0 {
		for i := range n.leaf {
			if n.leaf[i].hash == h && eq(n.leaf[i].Key, key) {
				if len(n.leaf) == 1 {
					return nil, true
				}
				n = mapOwn(tok, n)
				n.leaf = slices.Delete(n.leaf, i, i+1)
				return n, true
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
		n = mapOwn(tok, n)
		n.leaves = leaves
		n.leaf = slices.Delete(n.leaf, i, i+1)
		return n, true
	case n.subs&bit != 0:
		i := mapAt(n.subs, bit)
		child, removed := mapDeleteOwned(tok, n.sub[i], shift+mapBits, h, key, eq)
		if !removed {
			return n, false
		}
		if child != nil {
			n = mapOwn(tok, n)
			n.sub[i] = child
			return n, true
		}
		subs := n.subs &^ bit
		if subs == 0 && n.leaves == 0 {
			return nil, true
		}
		n = mapOwn(tok, n)
		n.subs = subs
		n.sub = slices.Delete(n.sub, i, i+1)
		return n, true
	}
	return n, false
}
