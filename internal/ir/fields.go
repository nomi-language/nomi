package ir

// Fields an owner hands on.
//
// A field read (`state.v`, a tuple's slot) leaves the value in its record
// as well as in the read's temporary. Owns lets the temporary own that
// value, so `Vector.set(state.v, i, x)` may write the vector in place,
// when nothing reads it through the record again: grantProjections
// decides which projections may, and which classes lose their deep
// ownership because a value read out of them may have another holder.
//
// DEEP. A class is owned deep when it is owned and every value its fields
// hold is held by nothing else (Owns' deep need, below the deep bits of a
// caller's mask). A literal or an update is deep when each value stored
// in a field is handed over deep, and an update keeps its base's other
// fields, so it is deep only when its base is and dies there. A
// parameter is deep when its caller handed it over deep. A Vector, Map or
// Set is deep when it is owned: no projection reads an element.
//
// A PROJECTION `d = b.f` of a class owned deep is GRANTED (d owns the
// value, under b's deep need) when, after each use of d's class that may
// change the value or hand it on (any read but a Copy or Bind within the
// class, a borrowing crossing, or a projection of a value no update
// writes):
//
//   - nothing reads field f through b's LINEAGE (b's class and the classes
//     an update not replacing f makes from it or makes it from) again:
//     the f-liveness below, in which a read of f, a transfer, and an
//     update keeping f whose result reads f later, read it; and
//   - no other projection of f from the lineage is live.
//
// A projection that is not granted leaves the value with two holders, so
// its lineage is never deep. So is a lineage a member of which is handed
// on, returned, stored or updated keeping f while a projection of f from
// it is live: whoever receives it would read the value the projection
// holds. A projection with no other use than borrows is granted at no
// cost (it changes nothing), and so `swap`'s `Vector.get(state.v, i)`
// beside `Vector.set(state.v, i, x)` reads the vector before the set
// writes it.

// projGraph is what grantProjections reads of Owns' classes.
type projGraph struct {
	params  map[*Symbol]Temp
	index   []int32
	k       int
	find    func(Temp) Temp
	members map[Temp][]Temp
	classOf func(Temp) (Temp, bool)
	live    *liveness
	projs   []*Proj
	borrows func(*Call) bool
}

// grantProjections answers the projections of g that may own their
// value, and marks in lost the classes whose fields' values may have
// another holder.
func grantProjections(f *Func, g projGraph, lost []bool) map[*Proj]bool {
	granted := map[*Proj]bool{}
	byKey := map[fieldKey][]*Proj{}
	var keys []fieldKey
	for _, p := range g.projs {
		if _, ok := g.classOf(p.subj); !ok || g.index[p.subj] < 0 {
			continue
		}
		key, _ := projField(p)
		if byKey[key] == nil {
			keys = append(keys, key)
		}
		byKey[key] = append(byKey[key], p)
	}
	for _, key := range keys {
		grantField(f, g, key, byKey[key], granted, lost)
	}
	return granted
}

// grantField decides the projections ps, all of field key.
func grantField(f *Func, g projGraph, key fieldKey, ps []*Proj, granted map[*Proj]bool, lost []bool) {
	n := len(g.index)
	// The lineages: classes an update not replacing key joins.
	lin := make(map[Temp]Temp)
	var linFind func(Temp) Temp
	linFind = func(r Temp) Temp {
		p, ok := lin[r]
		if !ok || p == r {
			return r
		}
		q := linFind(p)
		lin[r] = q
		return q
	}
	tracked := func(t Temp) bool { return t != NoTemp && int(t) < n && g.index[t] >= 0 }
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if x, isMake := in.(*Make); isMake && x.kind == MakeUpdate && !key.replacedBy(x) &&
				tracked(x.ops[0]) && tracked(x.dst) {
				ra, rb := linFind(g.find(x.ops[0])), linFind(g.find(x.dst))
				if ra != rb {
					lin[ra] = rb
				}
			}
		}
	}
	failed := map[*Proj]bool{}
	projClass := map[Temp][]*Proj{} // a projection's class: its projections
	projOfLin := map[Temp][]Temp{}  // a lineage: its projections' classes
	lineageOf := map[*Proj]Temp{}   // a projection: its base's lineage
	for _, p := range ps {
		l := linFind(g.find(p.subj))
		lineageOf[p] = l
		if !tracked(p.dst) || g.find(p.dst) == g.find(p.subj) {
			failed[p] = true
			continue
		}
		d := g.find(p.dst)
		if projClass[d] == nil {
			projOfLin[l] = append(projOfLin[l], d)
		}
		projClass[d] = append(projClass[d], p)
	}
	// linTemps are each lineage's members, by liveness index.
	linTemps := map[Temp][]int32{}
	for r, ms := range g.members {
		l := linFind(r)
		if projOfLin[l] == nil {
			continue
		}
		for _, t := range ms {
			linTemps[l] = append(linTemps[l], g.index[t])
		}
	}
	lostLin := map[Temp]bool{}
	for _, p := range ps {
		if failed[p] {
			lostLin[lineageOf[p]] = true
		}
	}
	if len(projClass) > 0 {
		fl := newFieldLiveness(f, g.params, g.index, g.k, key)
		classLive := func(after bitset, r Temp) bool {
			for _, t := range g.members[r] {
				if after.has(int(g.index[t])) {
					return true
				}
			}
			return false
		}
		check := func(in Instr, term bool, after, fAfter bitset, uses []Temp) {
			seen := map[Temp]bool{}
			for _, u := range uses {
				if !tracked(u) {
					continue
				}
				r := g.find(u)
				if seen[r] {
					continue
				}
				seen[r] = true
				if dps := projClass[r]; dps != nil && (term || changesProjected(f, in, g)) {
					for _, p := range dps {
						l := lineageOf[p]
						for _, i := range linTemps[l] {
							if fAfter.has(int(i)) {
								failed[p] = true
							}
						}
						for _, d := range projOfLin[l] {
							if d != r && classLive(after, d) {
								failed[p] = true
							}
						}
					}
				}
				if l := linFind(r); projOfLin[l] != nil && !lostLin[l] && (term || transfersLineage(in, u, key, g)) {
					for _, d := range projOfLin[l] {
						if classLive(after, d) {
							lostLin[l] = true
						}
					}
				}
			}
		}
		var buf []Temp
		for _, b := range f.Blocks() {
			afters := map[Instr]bitset{}
			g.live.walk(b, func(in Instr, after bitset) {
				afters[in] = append(bitset(nil), after...)
			})
			fl.walk(b, func(in Instr, fAfter bitset) {
				buf = appendUses(in, g.params, buf[:0])
				check(in, false, afters[in], fAfter, buf)
			})
			if t := b.Term(); t != nil {
				buf = t.AppendUses(buf[:0])
				check(nil, true, g.live.out(b), fl.out(b), buf)
			}
		}
	}
	for _, p := range ps {
		if failed[p] {
			lostLin[lineageOf[p]] = true
			continue
		}
		granted[p] = true
	}
	for r := range g.members {
		if lostLin[linFind(r)] {
			lost[r] = true
		}
	}
}

// changesProjected reports whether in may change or hand on the value of
// a projected class it reads: any read but a move within the class, a
// borrowing crossing, or a projection of a value nothing writes in place.
func changesProjected(f *Func, in Instr, g projGraph) bool {
	switch x := in.(type) {
	case *Copy, *Bind:
		return false
	case *Ref:
		_, isParam := paramRead(x, g.params)
		return !isParam
	case *Call:
		return !g.borrows(x)
	case *Proj:
		return ownableKind(f.TempType(x.dst))
	}
	return true
}

// transfersLineage reports whether in's read of u (a member of a lineage
// with projections of key) gives the value, field key included, to
// another holder: anything but a move within the class, a field read, a
// borrowing crossing, or an update that replaces key.
func transfersLineage(in Instr, u Temp, key fieldKey, g projGraph) bool {
	switch x := in.(type) {
	case *Copy, *Bind:
		return false
	case *Ref:
		_, isParam := paramRead(x, g.params)
		return !isParam
	case *Proj:
		_, isField := projField(x)
		return !isField
	case *Call:
		return !g.borrows(x)
	case *Make:
		if x.kind == MakeUpdate && x.ops[0] == u && key.replacedBy(x) {
			for _, t := range x.ops[1:] {
				if t == u {
					return true
				}
			}
			return false
		}
	}
	return true
}

// fieldLiveness is a backward liveness of field key: a temporary is live
// where the value it holds may have its field key read later, through it
// or a value it is copied to or updated into keeping key. As liveness, a
// block's fault edge is a successor of every instruction in it.
type fieldLiveness struct {
	params map[*Symbol]Temp
	index  []int32
	k      int
	key    fieldKey
	in     map[BlockID]bitset
	buf    []Temp
	succs  []BlockID
}

func newFieldLiveness(f *Func, params map[*Symbol]Temp, index []int32, k int, key fieldKey) *fieldLiveness {
	l := &fieldLiveness{params: params, index: index, k: k, key: key, in: map[BlockID]bitset{}}
	blocks := f.Blocks()
	for _, b := range blocks {
		l.in[b.ID()] = newBitset(k)
	}
	for changed := true; changed; {
		changed = false
		for i := len(blocks) - 1; i >= 0; i-- {
			b := blocks[i]
			if l.in[b.ID()].or(l.walk(b, nil)) {
				changed = true
			}
		}
	}
	return l
}

func (l *fieldLiveness) has(live bitset, t Temp) bool {
	return t != NoTemp && int(t) < len(l.index) && l.index[t] >= 0 && live.has(int(l.index[t]))
}

func (l *fieldLiveness) use(live bitset, t Temp) {
	if t != NoTemp && int(t) < len(l.index) && l.index[t] >= 0 {
		live.add(int(l.index[t]))
	}
}

// out is the set live after b's terminator.
func (l *fieldLiveness) out(b *Block) bitset {
	live := newBitset(l.k)
	if t := b.Term(); t != nil {
		l.succs = t.AppendSuccessors(l.succs[:0])
		for _, s := range l.succs {
			if in, ok := l.in[s]; ok {
				live.or(in)
			}
		}
	}
	if h, has := b.Fault(); has {
		if s, ok := l.in[h]; ok {
			live.or(s)
		}
	}
	return live
}

// walk runs backward over b from its live-out, calling at (when non-nil)
// with each instruction and the set live just after it, and answers b's
// live-in.
func (l *fieldLiveness) walk(b *Block, at func(Instr, bitset)) bitset {
	live := l.out(b)
	var fault bitset
	if h, has := b.Fault(); has {
		fault = l.in[h]
	}
	if t := b.Term(); t != nil {
		l.buf = t.AppendUses(l.buf[:0])
		for _, u := range l.buf {
			l.use(live, u)
		}
	}
	instrs := b.Instrs()
	for i := len(instrs) - 1; i >= 0; i-- {
		in := instrs[i]
		if at != nil {
			at(in, live)
		}
		d := in.Dst()
		dLive := l.has(live, d)
		if d != NoTemp && int(d) < len(l.index) && l.index[d] >= 0 {
			live.del(int(l.index[d]))
		}
		switch x := in.(type) {
		case *Copy:
			if dLive {
				l.use(live, x.Src())
			}
		case *Bind:
			if dLive {
				l.use(live, x.Src())
			}
		case *Ref:
			if p, isParam := paramRead(x, l.params); isParam && dLive {
				l.use(live, p)
			}
		case *Proj:
			if key, isField := projField(x); !isField || key == l.key {
				l.use(live, x.subj)
			}
		case *Make:
			if x.kind == MakeUpdate {
				if !l.key.replacedBy(x) && dLive {
					l.use(live, x.ops[0])
				}
				for _, t := range x.ops[1:] {
					l.use(live, t)
				}
				break
			}
			l.buf = x.AppendUses(l.buf[:0])
			for _, u := range l.buf {
				l.use(live, u)
			}
		default:
			l.buf = appendUses(in, l.params, l.buf[:0])
			for _, u := range l.buf {
				l.use(live, u)
			}
		}
		if fault != nil {
			live.or(fault)
		}
	}
	return live
}
