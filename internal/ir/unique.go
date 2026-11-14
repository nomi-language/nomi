package ir

// Values a consumer may write in place.
//
// Every Nomi update makes a new value: `{..p, x: 1}` leaves `p` as it was,
// and so does `Vector.set(v, i, x)` leave `v`. When nothing can read the old
// value again, a consumer may write the new one into the old one's storage
// instead of a copy, and no program can tell. Go keeps no run-time count of
// a value's holders, so the proof is static, a property of the graph and of
// what the function's direct callees return: Owns computes it from the
// function, asking a Callees for each callee's ResultOwnership, and a
// consumer that never asks copies, which is always right.
//
// OWNERSHIP. A temporary is owned when the value it holds is held by
// nothing but the temporaries of its ALIAS CLASS (the temporaries Copy and
// Bind join: a binding, a branch's slot, a loop's state). Owns answers, per
// class, either never, or a NEED: a mask of the function's parameters
// (the first MaxOwnedParams) whose arguments the caller must have handed
// over, bit j, and of those it must have handed over deep, bit DeepBit+j,
// for the class to be owned. An empty need is unconditional. Owns answers
// a second need per class, the one under which it is owned DEEP: the
// values its fields hold are held by nothing else either (fields.go).
//
//   - Fresh: every write of every member is a struct, record, tuple,
//     Vector, Map or Set literal, an update, `Iter.to_vector`, `to_map` or
//     `to_set` (each builds a new collection), a Copy or Bind from a
//     member, a parameter (which adds its bit to the need), a direct call
//     whose callee's result is fresh, a crossing Callees.Writes names (a
//     collection operation, below), a field read fields.go grants, or a
//     fold that hands its accumulator on (below). A direct call's result is
//     owned when its callee answers Fresh and every argument the callee may
//     return (ResultOwnership.Params) is a move (below) of an owned class:
//     the result's need is the union of theirs, each argument's deep need
//     for a deep bit. Any other field read, a `once`, a constant, any other
//     call or crossing, and anything else may have another holder: the
//     class is never owned.
//   - Not escaped: every read of a member is a Copy or Bind into the class,
//     the base of an update, a field read (ProjField, ProjRecordField,
//     ProjSlot), a
//     crossing Callees.Borrows names (`Vector.get`, `Map.size`), a Return,
//     or a MOVE: a read after which no member is live, on any path,
//     a fault edge's included, the instruction's own destination excepted.
//     A move hands the value on (to a callee, a list, a closure, a channel)
//     and this activation never reads it again, so whoever receives it is
//     its one holder. Any other read keeps the value somewhere while the
//     class still reads it, and the class is never owned. So is a class one
//     of whose bindings a RefLocal reads by its symbol, a read no operand
//     shows, and a class a terminator other than Return reads.
//
// An update's or a call's result is a value of its own, so the destination
// starts its own class unless a Copy joins it to an operand's (a loop
// writing its state back), and the destination is the one member the
// last-use test excuses.
//
// WHAT OWNS ANSWERS.
//
//   - Updates: an update `d = update b, ...` may write into b's record when
//     b's class is owned and the update is its last use; its need is the
//     class's. An update storing a member of its base's class (`{..p, f:
//     p}`) copies, since the record would hold itself.
//   - Moves: per direct call, the arguments that are moves of an owned
//     class, each with its need and its deep need. At run time a caller
//     whose own mask meets the need hands the callee the argument's
//     parameter as owned, and as owned deep when it meets the deep need;
//     the callee's bits are what its needs test. A parameter given by
//     any other path (a function value, interface dispatch, a call from Go
//     or a host, a test) arrives borrowed, so a callee's bits are only ever
//     set by a caller that proved the argument its own.
//   - Takes: per crossing Callees.Writes names, when its receiver operand
//     is a move of an owned class, the need. The crossing may write the
//     receiver's storage (internal/vm package doc, "Updates written in
//     place"); its result is owned under the same need.
//   - Folds: per `Iter.reduce` or `Iter.map_reduce` whose callback is a
//     lambda the function builds and whose seed is a move of an owned
//     class, the seed's needs and the callback's ResultOwnership (asked of
//     Callees.Lambda). At run time the fold hands the callback its
//     accumulator as its parameter 0 when the activation's mask meets the
//     seed's need (deep when it meets the deep need), and hands each
//     answer on again when the callback's result is fresh under what it
//     was given (Fold.NextReduce, NextMapReduce: a map_reduce's state is
//     its answer's first slot, so the answer must be owned deep). The
//     fold keeps no other reference to the accumulator, the item and the
//     captures are never handed over, and the fold's result is owned under
//     the seed's need when every step's answer is. `Iter.scan` yields each
//     state to its consumer, so it hands none on, and neither does a fold
//     whose callback signals (`break`, `continue`).
//   - Result: whether every value the function returns is owned, and the
//     union of their needs, and whether each is owned deep, with the union
//     of those needs; a caller's direct call reads it.
//
// A move that reads a class in two operands (`f(p, p)`) hands neither on:
// the callee would hold one value under two parameters.
//
// The analysis is flow-insensitive about escape and conservative about
// aliasing: a class whose members could hold two values (a slot written by
// two literals) is held to the rule as one. Nothing unclear is owned.

// ResultOwnership is what a function's returns hand its caller: Fresh says
// every returned value is owned by the caller given the caller's handing
// in Params (a need: bit j, argument j handed over; bit DeepBit+j, handed
// over deep); Deep says every returned value is owned deep given
// DeepParams. A function that is not Fresh returns values another holder
// may keep.
type ResultOwnership struct {
	Fresh      bool
	Params     uint64
	Deep       bool
	DeepParams uint64
}

// DeepBit is where a need's deep half starts: bit DeepBit+j says parameter
// j was handed over deep, so the values its fields hold are its own too.
const DeepBit = 32

// MaxOwnedParams bounds the parameters a need names: a parameter at this
// index or past it is never handed over, so no mask a caller builds has
// every bit set.
const MaxOwnedParams = DeepBit - 1

// Move is a direct call's argument the caller hands over: Arg is its index,
// Need the caller's handing the value needs to be the caller's to give,
// and DeepNeed what it needs to be given deep (Never: it is not).
type Move struct {
	Arg      int
	Need     uint64
	DeepNeed uint64
}

// Never is the need of a value that is never owned (or never deep). No
// mask meets it.
const Never = ^uint64(0)

// Fold is a host-driven fold (`Iter.reduce`, `Iter.map_reduce`) whose seed
// the activation hands over: under Need the fold may hand the accumulator
// to its callback as owned, under DeepNeed as owned deep. Result is the
// callback's own ResultOwnership, which says whether each step's answer
// is the fold's again. Body is the callback.
type Fold struct {
	Need, DeepNeed uint64
	Result         ResultOwnership
	Body           *Func
}

// FoldMask is the mask a fold hands its callback's accumulator under,
// given the activation's own mask: bit 0 when the seed is owned, bit
// DeepBit too when it is owned deep.
func (fd Fold) FoldMask(owned uint64) uint64 {
	var m uint64
	if owned&fd.Need == fd.Need {
		m = 1
		if owned&fd.DeepNeed == fd.DeepNeed {
			m |= 1 << DeepBit
		}
	}
	return m
}

// NextReduce is the mask the next step of a reduce hands its callback's
// accumulator under, the step before having run under m: the callback's
// answer is owned when its result is fresh under what m gave it.
func (fd Fold) NextReduce(m uint64) uint64 {
	r := fd.Result
	if !r.Fresh || r.Params&^m != 0 {
		return 0
	}
	if r.Deep && r.DeepParams&^m == 0 {
		return 1 | 1<<DeepBit
	}
	return 1
}

// NextMapReduce is NextReduce for a map_reduce, whose callback answers a
// pair: the state is the pair's first slot, owned when the pair is owned
// deep.
func (fd Fold) NextMapReduce(m uint64) uint64 {
	r := fd.Result
	if !r.Fresh || !r.Deep || r.Params&^m != 0 || r.DeepParams&^m != 0 {
		return 0
	}
	return 1 | 1<<DeepBit
}

// Callees tells Owns about the calls a function makes. Result answers a
// direct, non-crossing call's callee's ResultOwnership (nil: none is
// fresh). Writes answers, for a crossing, the operand it may write in place
// when that operand is owned, or -1 (nil: none).
type Callees struct {
	Result func(*Call) ResultOwnership
	Writes func(*Call) int
	// Borrows reports whether a crossing reads its operands and keeps
	// none of them (a lookup, a size), so a read by it is no escape
	// (nil: none).
	Borrows func(*Call) bool
	// Lambda answers a fold's callback's ResultOwnership (nil: no fold
	// hands its accumulator on).
	Lambda func(*Func) ResultOwnership
}

// Ownership is what Owns answers for one function.
type Ownership struct {
	// Updates are the updates that may write into their base, with the
	// need each tests.
	Updates map[*Make]uint64
	// Moves are the arguments each direct call may hand its callee.
	Moves map[*Call][]Move
	// Takes are the crossings that may write their receiver, with the
	// need each tests.
	Takes map[*Call]uint64
	// Folds are the folds that may hand their accumulator to their
	// callback.
	Folds map[*Iter]Fold
	// Fields are the field reads that own the value they read, with the
	// need each holds under. The VM reads none: a field read's value is
	// written in place by the operations that read it (Updates, Takes,
	// Moves); a census counts them.
	Fields map[*Proj]uint64
	// Result is what the function's returns hand a caller.
	Result ResultOwnership
}

// InPlaceUpdates answers the updates of f (MakeUpdate) whose base nothing
// can read after them and that need nothing of a caller: the updates Owns
// answers with an empty need, knowing nothing of f's callees. It answers
// nil when f has none.
func InPlaceUpdates(f *Func) map[*Make]bool {
	o := Owns(f, Callees{})
	var out map[*Make]bool
	for u, need := range o.Updates {
		if need == 0 {
			if out == nil {
				out = map[*Make]bool{}
			}
			out[u] = true
		}
	}
	return out
}

// ownableKind reports whether a temporary of kind k can hold a value
// something writes in place, or carry one through a generic body or a
// tuple.
func ownableKind(t *ValType) bool {
	if t == nil {
		return true
	}
	switch t.Kind() {
	case KindStruct, KindRecord, KindTuple, KindVector, KindMap, KindSet, KindAny:
		return true
	}
	return false
}

// fieldsKind reports whether a value of type t may have fields a
// projection reads (a struct, a record, a tuple, or a generic value).
func fieldsKind(t *ValType) bool {
	if t == nil {
		return true
	}
	switch t.Kind() {
	case KindStruct, KindRecord, KindTuple, KindAny:
		return true
	}
	return false
}

// never is a class's need when the class is never owned.
const never = Never

// fieldKey names a field a projection reads: a struct's or a record's by
// name, a tuple's slot by index.
type fieldKey struct {
	slot bool
	idx  int
	name string
}

// projField answers the field p reads, when p reads a field of a struct, a
// record or a tuple.
func projField(p *Proj) (fieldKey, bool) {
	switch p.kind {
	case ProjField, ProjRecordField:
		return fieldKey{name: p.text}, true
	case ProjSlot:
		return fieldKey{slot: true, idx: p.idx}, true
	}
	return fieldKey{}, false
}

// replaces reports whether update u gives field k a new value.
func (k fieldKey) replacedBy(u *Make) bool {
	if k.slot {
		return false
	}
	for _, n := range u.names {
		if n == k.name {
			return true
		}
	}
	return false
}

// Owns answers which values f may write in place, what each of its direct
// calls may hand on, and what its returns hand its caller.
func Owns(f *Func, callees Callees) *Ownership {
	out := &Ownership{}
	n := f.NumTemps() + 1
	tracked := false
	for t := Temp(1); int(t) < n; t++ {
		if ownableKind(f.TempType(t)) {
			tracked = true
			break
		}
	}
	if !tracked {
		fresh := !returnsAValue(f)
		out.Result = ResultOwnership{Fresh: fresh, Deep: fresh}
		return out
	}
	parent := make([]Temp, n)
	for i := range parent {
		parent[i] = Temp(i)
	}
	find := func(t Temp) Temp {
		for parent[t] != t {
			parent[t] = parent[parent[t]]
			t = parent[t]
		}
		return t
	}
	inRange := func(t Temp) bool { return t != NoTemp && int(t) < n }
	union := func(a, b Temp) {
		if !inRange(a) || !inRange(b) {
			return
		}
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	params := paramTemps(f)
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch mv := in.(type) {
			case *Copy:
				union(mv.Src(), mv.Dst())
			case *Bind:
				union(mv.Src(), mv.Dst())
			case *Ref:
				if p, isParam := paramRead(mv, params); isParam {
					union(p, mv.Dst())
				}
			}
		}
	}
	// need is each class root's need, or never; deep is what the class
	// needs beyond it to be owned deep (its fields' values its own too),
	// or never.
	need := make([]uint64, n)
	deep := make([]uint64, n)
	owned := make([]bool, n)  // the class has an ownable member
	fields := make([]bool, n) // the class has a member with fields
	for t := Temp(1); int(t) < n; t++ {
		if ownableKind(f.TempType(t)) {
			owned[find(t)] = true
		}
		if fieldsKind(f.TempType(t)) {
			fields[find(t)] = true
		}
	}
	for t := Temp(1); int(t) < n; t++ {
		if r := find(t); !owned[r] {
			need[r] = never
		}
	}
	refuse := func(t Temp) {
		if inRange(t) {
			need[find(t)] = never
		}
	}
	classOf := func(t Temp) (Temp, bool) {
		if !inRange(t) {
			return NoTemp, false
		}
		r := find(t)
		return r, need[r] != never
	}
	// deepOf is what class r needs to be owned deep: its need, and for a
	// class with fields, deep too.
	lost := make([]bool, n) // a projection's value may have another holder
	deepOf := func(r Temp) uint64 {
		if need[r] == never {
			return never
		}
		if !fields[r] {
			return need[r]
		}
		if deep[r] == never || lost[r] {
			return never
		}
		return need[r] | deep[r]
	}
	for j, p := range f.Params() {
		if r, ok := classOf(p.Temp); ok {
			if j >= MaxOwnedParams {
				need[r] = never
			} else {
				need[r] |= 1 << j
				deep[r] |= 1 << (DeepBit + j)
			}
		}
	}
	// A RefLocal naming a binding reads it by symbol, a read no operand
	// shows: the bound value's class is never owned. (A RefLocal naming a
	// parameter is a Copy of it, and a use of it, below.)
	bound := map[*Symbol][]Temp{}
	funcs := map[Temp]*FuncValue{}
	writes := make([]int, n)
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if bd, isBind := in.(*Bind); isBind {
				bound[bd.Sym()] = append(bound[bd.Sym()], bd.Dst())
			}
			if d := in.Dst(); inRange(d) {
				writes[d]++
			}
			if fv, isFunc := in.(*FuncValue); isFunc {
				funcs[fv.dst] = fv
			}
		}
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if r, isRef := in.(*Ref); isRef && r.Kind() == RefLocal {
				if _, isParam := paramRead(r, params); !isParam {
					refuse(r.Dst())
				}
				for _, t := range bound[r.Sym()] {
					refuse(t)
				}
			}
		}
	}
	// The writes: what may own its destination, and what never does.
	var calls, takes []*Call
	var lits []*Make
	var projs []*Proj
	type foldSite struct {
		it   *Iter
		body *Func
	}
	var folds []foldSite
	writesTo := func(c *Call) int {
		if callees.Writes == nil || !c.Crosses() {
			return -1
		}
		return callees.Writes(c)
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch x := in.(type) {
			case *Copy, *Bind:
			case *Ref:
				if _, isParam := paramRead(x, params); !isParam {
					refuse(x.dst)
				}
			case *Make:
				switch x.kind {
				case MakeStruct, MakeRecord, MakeTuple, MakeUpdate:
					lits = append(lits, x)
				case MakeVector, MakeMap, MakeSet:
				default:
					refuse(x.dst)
				}
			case *Proj:
				if _, ok := projField(x); ok && ownableKind(f.TempType(x.dst)) {
					projs = append(projs, x)
					continue
				}
				refuse(x.dst)
			case *Iter:
				switch x.op {
				case IterToVector, IterToMap, IterToSet:
					// A collection built from the sequence's elements.
				case IterReduce, IterMapReduce:
					fv := funcs[x.args[len(x.args)-1]]
					if x.sig || callees.Lambda == nil || fv == nil || writes[fv.dst] != 1 || len(x.args) != 3 {
						refuse(x.dst)
						continue
					}
					folds = append(folds, foldSite{x, fv.body})
				default:
					refuse(x.dst)
				}
			case *Call:
				switch {
				case x.form == CalleeDirect && !x.crosses && callees.Result != nil:
					calls = append(calls, x)
				case writesTo(x) >= 0 && writesTo(x) < x.NumArgs():
					takes = append(takes, x)
				default:
					refuse(x.dst)
				}
			default:
				refuse(in.Dst())
			}
		}
	}
	// Liveness over the members of the classes that may be owned.
	index := make([]int32, n)
	k := 0
	for t := Temp(0); int(t) < n; t++ {
		index[t] = -1
		if _, ok := classOf(t); ok && t != NoTemp {
			index[t] = int32(k)
			k++
		}
	}
	members := map[Temp][]Temp{}
	for t := Temp(1); int(t) < n; t++ {
		if index[t] >= 0 {
			members[find(t)] = append(members[find(t)], t)
		}
	}
	live := newLiveness(f, params, index, k)
	// liveAfter reports whether a member of class r but except is live in
	// the set after.
	liveAfter := func(after bitset, r, except Temp) bool {
		for _, t := range members[r] {
			if t != except && after.has(int(index[t])) {
				return true
			}
		}
		return false
	}
	// moved marks, per instruction, the classes it reads at their last
	// use; doubled marks a class read in two operands of one.
	type readKey struct {
		in Instr
		r  Temp
	}
	moved := map[readKey]bool{}
	doubled := map[readKey]bool{}
	selfStore := map[*Make]bool{}
	baseDies := map[*Make]bool{}
	var buf []Temp
	for _, b := range f.Blocks() {
		live.walk(b, func(in Instr, after bitset) {
			buf = appendUses(in, params, buf[:0])
			if len(buf) == 0 {
				return
			}
			seen := map[Temp]bool{}
			for _, u := range buf {
				r, ok := classOf(u)
				if !ok {
					continue
				}
				if seen[r] {
					doubled[readKey{in, r}] = true
				}
				seen[r] = true
			}
			switch x := in.(type) {
			case *Copy, *Bind, *Ref:
				// A move within the class (a Ref here reads a parameter).
				return
			case *Proj:
				if x.kind == ProjField || x.kind == ProjRecordField || x.kind == ProjSlot {
					return
				}
			case *Call:
				if x.crosses && callees.Borrows != nil && callees.Borrows(x) {
					return
				}
			case *Make:
				if x.kind == MakeUpdate {
					base, ok := classOf(x.ops[0])
					if ok {
						baseDies[x] = !liveAfter(after, base, x.dst)
					}
					for _, t := range x.ops[1:] {
						r, ok := classOf(t)
						if !ok {
							continue
						}
						if r == base {
							selfStore[x] = true
						}
						if liveAfter(after, r, x.dst) {
							need[r] = never
						} else {
							moved[readKey{in, r}] = true
						}
					}
					return
				}
			}
			for r := range seen {
				if need[r] == never {
					continue
				}
				if liveAfter(after, r, in.Dst()) {
					need[r] = never
				} else {
					moved[readKey{in, r}] = true
				}
			}
		})
		if t := b.Term(); t != nil {
			if _, isRet := t.(*Return); !isRet {
				buf = t.AppendUses(buf[:0])
				for _, u := range buf {
					refuse(u)
				}
			}
		}
	}
	// The projections a class's own value may hand on (fields.go).
	granted := grantProjections(f, projGraph{
		params: params, index: index, k: k, find: find, members: members,
		classOf: classOf, live: live, projs: projs,
		borrows: func(c *Call) bool { return c.crosses && callees.Borrows != nil && callees.Borrows(c) },
	}, lost)
	// handed reports whether in hands on operand t's class: a move of an
	// owned class that reads it in no other operand.
	handedAt := func(in Instr, t Temp) (Temp, bool) {
		r, ok := classOf(t)
		if !ok || !moved[readKey{in, r}] || doubled[readKey{in, r}] {
			return NoTemp, false
		}
		return r, true
	}
	handed := func(c *Call, i int) (Temp, bool) { return handedAt(c, c.args[i]) }
	// The results of calls, takes, projections and folds, and what each
	// class needs to be owned deep, until no need changes.
	results := make([]ResultOwnership, len(calls))
	asked := make([]bool, len(calls))
	foldResults := make([]ResultOwnership, len(folds))
	for i, fd := range folds {
		foldResults[i] = callees.Lambda(fd.body)
	}
	for changed := true; changed; {
		changed = false
		grow := func(at []uint64, dst Temp, v uint64) {
			r, ok := classOf(dst)
			if !ok {
				return
			}
			if v == never {
				if at[r] != never {
					at[r] = never
					changed = true
				}
				return
			}
			if at[r]|v != at[r] {
				at[r] |= v
				changed = true
			}
		}
		set := func(dst Temp, v uint64) { grow(need, dst, v) }
		setDeep := func(dst Temp, v uint64) { grow(deep, dst, v) }
		// argNeed is the handing a callee's need (over its parameters)
		// asks of this call's arguments.
		argNeed := func(c *Call, p uint64) uint64 {
			if p == 0 {
				return 0
			}
			v := uint64(0)
			for j := 0; j < c.NumArgs() && j < MaxOwnedParams; j++ {
				shallow, deepToo := p&(1<<j) != 0, p&(1<<(DeepBit+j)) != 0
				if !shallow && !deepToo {
					continue
				}
				r, ok := handed(c, j)
				if !ok {
					return never
				}
				if shallow {
					v |= need[r]
				}
				if deepToo {
					v |= deepOf(r)
				}
			}
			args := uint64(1)<<min(c.NumArgs(), MaxOwnedParams) - 1
			if p&^(args|args<<DeepBit) != 0 {
				// The callee returns a parameter this call does not fill.
				return never
			}
			return v
		}
		for i, c := range calls {
			if _, ok := classOf(c.dst); !ok {
				continue
			}
			if !asked[i] {
				results[i], asked[i] = callees.Result(c), true
			}
			res := results[i]
			if !res.Fresh {
				set(c.dst, never)
				continue
			}
			set(c.dst, argNeed(c, res.Params))
			if res.Deep {
				setDeep(c.dst, argNeed(c, res.DeepParams))
			} else {
				setDeep(c.dst, never)
			}
		}
		for _, c := range takes {
			if _, ok := classOf(c.dst); !ok {
				continue
			}
			r, ok := handed(c, writesTo(c))
			if !ok {
				set(c.dst, never)
				continue
			}
			set(c.dst, need[r])
		}
		// A literal's or an update's fields are its own when each value
		// stored in one is handed over deep; an update keeps its base's
		// other fields, its own only when the base dies there.
		for _, x := range lits {
			if _, ok := classOf(x.dst); !ok {
				continue
			}
			from := 0
			if x.kind == MakeUpdate {
				from = 1
				base, ok := classOf(x.ops[0])
				if !ok || selfStore[x] || !baseDies[x] {
					setDeep(x.dst, never)
					continue
				}
				setDeep(x.dst, deepOf(base))
			}
			for _, t := range x.ops[from:] {
				if !inRange(t) || !ownableKind(f.TempType(t)) {
					continue
				}
				r, ok := handedAt(x, t)
				if !ok {
					setDeep(x.dst, never)
					break
				}
				setDeep(x.dst, deepOf(r))
			}
		}
		for _, p := range projs {
			base, ok := classOf(p.subj)
			if !ok || !granted[p] {
				set(p.dst, never)
				continue
			}
			v := deepOf(base)
			set(p.dst, v)
			setDeep(p.dst, v)
		}
		for i, fd := range folds {
			if _, ok := classOf(fd.it.dst); !ok {
				continue
			}
			seed, ok := handedAt(fd.it, fd.it.args[1])
			if !ok {
				set(fd.it.dst, never)
				continue
			}
			v, d := foldResult(fd.it.op, foldResults[i], need[seed], deepOf(seed))
			set(fd.it.dst, v)
			setDeep(fd.it.dst, d)
		}
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch x := in.(type) {
			case *Make:
				if x.kind != MakeUpdate || selfStore[x] || !baseDies[x] || x.ops[0] == x.dst {
					continue
				}
				if r, ok := classOf(x.ops[0]); ok {
					if out.Updates == nil {
						out.Updates = map[*Make]uint64{}
					}
					out.Updates[x] = need[r]
				}
			case *Call:
				if x.form == CalleeDirect && !x.crosses {
					var ms []Move
					for i := 0; i < x.NumArgs() && i < MaxOwnedParams; i++ {
						if r, ok := handed(x, i); ok {
							ms = append(ms, Move{Arg: i, Need: need[r], DeepNeed: deepOf(r)})
						}
					}
					if len(ms) > 0 {
						if out.Moves == nil {
							out.Moves = map[*Call][]Move{}
						}
						out.Moves[x] = ms
					}
					continue
				}
				if i := writesTo(x); i >= 0 && i < x.NumArgs() {
					if r, ok := handed(x, i); ok {
						if out.Takes == nil {
							out.Takes = map[*Call]uint64{}
						}
						out.Takes[x] = need[r]
					}
				}
			}
		}
	}
	for i, fd := range folds {
		seed, ok := handedAt(fd.it, fd.it.args[1])
		if !ok {
			continue
		}
		if out.Folds == nil {
			out.Folds = map[*Iter]Fold{}
		}
		out.Folds[fd.it] = Fold{Need: need[seed], DeepNeed: deepOf(seed), Result: foldResults[i], Body: fd.body}
	}
	for _, p := range projs {
		if r, ok := classOf(p.dst); ok && granted[p] {
			if out.Fields == nil {
				out.Fields = map[*Proj]uint64{}
			}
			out.Fields[p] = need[r]
		}
	}
	out.Result = ResultOwnership{Fresh: true, Deep: true}
	for _, b := range f.Blocks() {
		r, isRet := b.Term().(*Return)
		if !isRet || !r.HasVal() {
			continue
		}
		c, ok := classOf(r.Val())
		if !ok || r.Ctl() != CtlNone {
			out.Result = ResultOwnership{}
			break
		}
		out.Result.Params |= need[c]
		if d := deepOf(c); d == never {
			out.Result.Deep = false
		} else {
			out.Result.DeepParams |= d
		}
	}
	if !out.Result.Deep {
		out.Result.DeepParams = 0
	}
	return out
}

// foldResult is what a fold's result needs to be owned, and owned deep,
// given its callback's ResultOwnership and the seed's needs: a reduce
// answers its last step's accumulator (or the seed, from an empty
// source), a map_reduce a pair it builds of the last state and the
// outputs.
func foldResult(op IterOp, r ResultOwnership, seed, seedDeep uint64) (uint64, uint64) {
	const acc, accDeep = uint64(1), uint64(1) | 1<<DeepBit
	switch {
	case !r.Fresh:
		return never, never
	case op == IterMapReduce:
		if !r.Deep || r.DeepParams&^accDeep != 0 || r.Params&^accDeep != 0 {
			return 0, never
		}
		return 0, seedDeep
	}
	var v uint64
	switch {
	case r.Params&^acc == 0:
		v = seed
	case r.Params&^accDeep == 0 && r.Deep && r.DeepParams&^accDeep == 0:
		v = seedDeep
	default:
		return never, never
	}
	if !r.Deep || r.DeepParams&^accDeep != 0 {
		return v, never
	}
	return v, seedDeep
}

// paramTemps maps each parameter's symbol to its temporary.
func paramTemps(f *Func) map[*Symbol]Temp {
	out := make(map[*Symbol]Temp, len(f.Params()))
	for _, p := range f.Params() {
		if p.Sym != nil {
			out[p.Sym] = p.Temp
		}
	}
	return out
}

// paramRead answers the parameter a RefLocal reads: a parameter's value is
// in its temporary, and the read copies it from there.
func paramRead(r *Ref, params map[*Symbol]Temp) (Temp, bool) {
	if r.Kind() != RefLocal {
		return NoTemp, false
	}
	t, isParam := params[r.Sym()]
	return t, isParam
}

// appendUses is in's uses, a RefLocal's parameter included.
func appendUses(in Instr, params map[*Symbol]Temp, dst []Temp) []Temp {
	dst = in.AppendUses(dst)
	if r, isRef := in.(*Ref); isRef {
		if p, isParam := paramRead(r, params); isParam {
			dst = append(dst, p)
		}
	}
	return dst
}

// returnsAValue reports whether a Return of f carries a value.
func returnsAValue(f *Func) bool {
	for _, b := range f.Blocks() {
		if r, isRet := b.Term().(*Return); isRet && r.HasVal() {
			return true
		}
	}
	return false
}

// bitset is a set of dense indices.
type bitset []uint64

func newBitset(k int) bitset { return make(bitset, (k+63)/64) }

func (s bitset) has(i int) bool { return s[i/64]&(1<<(i%64)) != 0 }
func (s bitset) add(i int)      { s[i/64] |= 1 << (i % 64) }
func (s bitset) del(i int)      { s[i/64] &^= 1 << (i % 64) }

// or adds o to s and reports whether s grew.
func (s bitset) or(o bitset) bool {
	grew := false
	for i, w := range o {
		if s[i]|w != s[i] {
			s[i] |= w
			grew = true
		}
	}
	return grew
}

// liveness is a backward liveness over the temporaries index numbers (the
// rest are not tracked). A block's fault edge is a successor of every
// instruction in it: what its handler reads is live throughout the block.
type liveness struct {
	// params are the parameters a RefLocal reads, which is a use of the
	// parameter's temporary.
	params map[*Symbol]Temp
	index  []int32
	k      int
	in     map[BlockID]bitset
	buf    []Temp
	succs  []BlockID
}

func newLiveness(f *Func, params map[*Symbol]Temp, index []int32, k int) *liveness {
	l := &liveness{params: params, index: index, k: k, in: map[BlockID]bitset{}}
	blocks := f.Blocks()
	for _, b := range blocks {
		l.in[b.ID()] = newBitset(k)
	}
	for changed := true; changed; {
		changed = false
		for i := len(blocks) - 1; i >= 0; i-- {
			b := blocks[i]
			in := l.walk(b, nil)
			if l.in[b.ID()].or(in) {
				changed = true
			}
		}
	}
	return l
}

// out is the set live after b's terminator: its successors' and its
// fault handler's live-in.
func (l *liveness) out(b *Block) bitset {
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
func (l *liveness) walk(b *Block, at func(Instr, bitset)) bitset {
	live := newBitset(l.k)
	var fault bitset
	if h, has := b.Fault(); has {
		if s, ok := l.in[h]; ok {
			fault = s
		}
	}
	if t := b.Term(); t != nil {
		l.succs = t.AppendSuccessors(l.succs[:0])
		for _, s := range l.succs {
			if in, ok := l.in[s]; ok {
				live.or(in)
			}
		}
		l.buf = t.AppendUses(l.buf[:0])
		for _, u := range l.buf {
			l.use(live, u)
		}
	}
	if fault != nil {
		live.or(fault)
	}
	instrs := b.Instrs()
	for i := len(instrs) - 1; i >= 0; i-- {
		in := instrs[i]
		if at != nil {
			at(in, live)
		}
		if d := in.Dst(); d != NoTemp && int(d) < len(l.index) && l.index[d] >= 0 {
			live.del(int(l.index[d]))
		}
		l.buf = appendUses(in, l.params, l.buf[:0])
		for _, u := range l.buf {
			l.use(live, u)
		}
		if fault != nil {
			live.or(fault)
		}
	}
	return live
}

func (l *liveness) use(live bitset, t Temp) {
	if t != NoTemp && int(t) < len(l.index) && l.index[t] >= 0 {
		live.add(int(l.index[t]))
	}
}
