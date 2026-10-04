package ir

import "strconv"

// The `iter` class: one instruction for every `Iter.` operation the builder
// lowers, plus the SOURCE VIEW that is not written in the program at all.
//
// # The push protocol does not appear here
//
// `Iter` is push, not pull: `Seq<T>` is
// `opaque struct Seq<T> { run: ((T) -> Bool) -> Bool }` and `each_while` is
// the whole interface, so a closure is a total representation of an `Iter<T>`.
// A push protocol threads a callback through every adapter, which is why a
// lowered iteration looked as though it might not be a sequence of
// instructions the way an arithmetic expression is.
//
// It is. `xs |> Iter.map(f) |> Iter.filter(g) |> Iter.to_list()` is SEVEN
// instructions, straight-line, no blocks and no terminators:
//
//	t1 = <xs>
//	t2 = iter view(list) t1
//	t3 = <f>
//	t4 = iter map t2, t3
//	t5 = <g>
//	t6 = iter filter t4, t5
//	t7 = iter to_list t6
//
// The protocol is the CALLEE's obligation. Threading a yield through a
// pipeline is what `rt.SeqMap` does when `rt.SeqToList` finally runs it; none
// of it is a control-flow relationship between these seven instructions. A
// PULL protocol would produce the same seven — `t4` would name a cursor
// instead of a stage — so the most consequential decision in the iterator
// design is INVISIBLE at this level. That is the honest answer to "does a push
// protocol linearize": it linearizes because the protocol is below the
// instruction set, not because push happens to be tractable.
//
// # THREE FORMS, AND THE FOURTH POPULATION COLLAPSES
//
// A reader sizing this class would name four populations — lazy adapters,
// terminals, materializers and constructors. There are THREE, because
// materializers are not a form:
//
//	SOURCE    produce a sequence from something that is not one.
//	STAGE     a sequence in, a sequence out, nothing driven.
//	TERMINAL  drive a sequence to a value.
//
// `Iter.to_list` and `Iter.count` are one form. Both drive the source to
// exhaustion and both answer one value; that one is a container and the other
// a scalar is a fact about the RESULT TYPE, which this node does not carry and
// which is the consumer's answer (see irmake.go for the same conclusion at a
// container's element type). Splitting them would put a type in the opcode.
//
// # THE SOURCE VIEW IS THE OPERATION THAT ERASES THE DOMAIN
//
// `IterView` is not written anywhere in a Nomi program. It is what happens
// where a List, a String, a Map, a Set, a Vector, a Range, a `Bytes` or a user
// type with its own `impl Iter` meets a parameter declared `Iter<T>`. It is
// also the reason this class has ONE instruction shape rather than 31: after
// the view, every other operation runs on a sequence, so exactly three
// operations read the domain at all and the other 34 are `Over() == IterOverSeq`
// by construction.
//
// # THE DOMAIN IS A PROPERTY OF OPERAND ZERO, NOT OF THE OPERATION
//
// `IterDomain` names the representation family the SOURCE OPERAND is in. It is
// a field rather than part of the opcode for `arith`'s reason: `ArithOp` is
// `+`, not `AddInt`, because the operand's type family is the operand's and
// putting it in the opcode would encode an operand in the operation. The
// builder reads it off the static kind.
//
// THAT MEANING IS EXACT, AND IT IS WHY `flat_map` AND `flatten` HAVE TWO OPS
// EACH. Their emitted shape is selected by whether the INNER sequences are
// already sequences or are Lists, and the inner element is reached through the
// callback's RESULT — a different operand position. Writing that into `Over()`
// would give one field two meanings. So `IterFlatMapList` and `IterFlattenList` are their own
// operations and the field keeps one meaning.
//
// # THE PREDICATE: A SIGNALLING CALLBACK
//
// `Signalling` reports that this operation's callback answers a CONTROL SIGNAL
// rather than a plain value — `break`, `continue` or `return` inside an
// `Iter.map`, `filter`, `take_while`, `each`, `reduce` or `iterate` callback, which is the
// set `analysis/iter_sensitive.go`'s `iterCallbackSlots` sanctions.
//
// It passes the three-part test this package applies to a candidate field.
// NOT DERIVABLE FROM THE KIND: `Iter.map` takes both forms and the op is the
// same op. NOT DERIVABLE FROM THE TEXT: this consumer's text differs
// (`rt.SeqMapCtl` against `rt.SeqMap`), but it differs BECAUSE the bit is read
// — deriving the bit from the text would be reading the answer out of the
// consequence. VARIES OVER ITS POPULATION, measured over the corpus by
// TestIRIter_TheSignallingPredicateVaries.
//
// AND IT CANNOT LIVE ON THE CALLBACK, which is what makes it earned rather
// than invented. The obvious home is the closure: a callback that can signal
// answers `(V, rt.Ctl)` instead of `V`. But `ir.Closure` has no result LIST,
// because no Nomi function has one and adding one for these would be modelling
// the consumer, so the
// widened calling convention has nowhere to be recorded except at the CALL
// that imposes it. It is imposed at the call in the source too: the same lambda
// reached through a binding is not widened, because the builder cannot see
// through the binding to find the signal.
//
// # WHAT THIS CLASS DOES NOT TAKE
//
// `Iter.loop`, the three widened callback FRAMES, and the sort family. See
// internal/irbuild/iriter.go, which names each one and its own reason.

// IterForm is what an operation does to a sequence.
type IterForm uint8

const (
	// IterFormSource produces a sequence from something that is not one.
	IterFormSource IterForm = iota + 1
	// IterFormStage takes a sequence and answers a sequence, driving nothing.
	IterFormStage
	// IterFormTerminal drives a sequence to a value.
	IterFormTerminal
)

func (f IterForm) String() string {
	switch f {
	case IterFormSource:
		return "source"
	case IterFormStage:
		return "stage"
	case IterFormTerminal:
		return "terminal"
	}
	return "IterForm(" + strconv.Itoa(int(f)) + ")"
}

// IterDomain is the representation family of an operation's SOURCE OPERAND.
//
// Named `IterOver*` rather than `Domain*` because `arith.go` already owns
// `Domain` for an arithmetic operand's type family, and two `Domain` types in
// one package is how a producer passes the wrong one.
type IterDomain uint8

const (
	// IterOverNothing is the absence of a source: a constructor.
	IterOverNothing IterDomain = iota
	// IterOverSeq is a value that is already a push sequence.
	IterOverSeq
	IterOverList
	// IterOverEmptyList is an untyped `[]`, whose element type is
	// unobservable because nothing is ever pushed.
	IterOverEmptyList
	IterOverString
	IterOverMap
	IterOverSet
	IterOverVector
	IterOverRange
	IterOverBytes
	// IterOverUserImpl is a named type carrying the program's own
	// `impl Iter for T`.
	IterOverUserImpl
)

func (d IterDomain) String() string {
	switch d {
	case IterOverNothing:
		return "nothing"
	case IterOverSeq:
		return "seq"
	case IterOverList:
		return "list"
	case IterOverEmptyList:
		return "[]"
	case IterOverString:
		return "string"
	case IterOverMap:
		return "map"
	case IterOverSet:
		return "set"
	case IterOverVector:
		return "vector"
	case IterOverRange:
		return "range"
	case IterOverBytes:
		return "bytes"
	case IterOverUserImpl:
		return "impl"
	}
	return "IterDomain(" + strconv.Itoa(int(d)) + ")"
}

// IterOp is one `Iter.` operation, or the implicit source view.
type IterOp uint8

const (
	// IterView is the SOURCE VIEW: a collection seen as a push sequence. It
	// has no spelling in a Nomi program.
	IterView IterOp = iota + 1

	// The constructors, which take no source.
	IterFrom
	IterRepeat
	IterIterate

	// The lazy stages.
	IterMap
	IterFilter
	IterTake
	IterTakeWhile
	IterDropWhile
	IterDrop
	IterWithIndex
	IterCycle
	IterConcat
	IterChunks
	IterChunkBy
	IterFlatMap
	IterFlatMapList
	IterZip

	// The terminals.
	IterToList
	IterCount
	IterKnownCount
	IterEmpty
	IterNotEmpty
	IterFirst
	IterLast
	IterAt
	IterEach
	IterAny
	IterAll
	IterFind
	IterReduce
	IterEachWhile
	IterToSet
	IterToVector
	IterToMap
	IterGroupBy
	IterReverse
	// IterJoin is `String.join`: the source's strings with the separator
	// operand between each.
	IterJoin
	IterPartition
	IterFlatten
	IterFlattenList
	IterSortWith
	// IterSortBy stably sorts by a projected key.
	IterSortBy
)

// iterOpSpec is one operation's Nomi-level facts: how it is spelled, what it
// does to a sequence, how many operands it takes, which source families it
// admits, and whether its callback has a signalling form.
//
// A TABLE RATHER THAN FIVE SWITCHES, because every one of the five would
// otherwise have to list every operation and a new operation would be silently
// absent from whichever one somebody forgot. `TestIRIter_EveryOpHasASpec`
// walks the op range and requires a row.
type iterOpSpec struct {
	name   string
	form   IterForm
	min    int
	max    int
	over   []IterDomain
	signal bool
}

// overSeq is the source-family list of every operation that runs on a sequence,
// which is all but four of them. A shared slice rather than a literal per row:
// 34 copies of one answer is 34 places for it to differ.
var overSeq = []IterDomain{IterOverSeq}

var iterOpSpecs = map[IterOp]iterOpSpec{
	// The view admits every collection family and NOT `IterOverSeq`: a value
	// that is already a sequence has no view to take, and the builder answers
	// it unchanged with no instruction at all.
	// A Range view also takes the element's `Comparable.compare` and
	// `Discrete.next`, because walking a Range calls both, and a user source
	// takes the `each_while` its impl declares, because walking it calls that;
	// newIter holds every other domain to its one operand.
	IterView: {name: "(source view)", form: IterFormSource, min: 1, max: 4, over: []IterDomain{
		IterOverList, IterOverEmptyList, IterOverString, IterOverMap, IterOverSet,
		IterOverVector, IterOverRange, IterOverBytes, IterOverUserImpl,
	}},

	IterFrom:    {name: "from", form: IterFormSource, min: 1, max: 1, over: []IterDomain{IterOverNothing}},
	IterRepeat:  {name: "repeat", form: IterFormSource, min: 1, max: 1, over: []IterDomain{IterOverNothing}},
	IterIterate: {name: "iterate", form: IterFormSource, min: 2, max: 2, over: []IterDomain{IterOverNothing}, signal: true},

	IterMap:         {name: "map", form: IterFormStage, min: 2, max: 2, over: overSeq, signal: true},
	IterFilter:      {name: "filter", form: IterFormStage, min: 2, max: 2, over: overSeq, signal: true},
	IterTake:        {name: "take", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterTakeWhile:   {name: "take_while", form: IterFormStage, min: 2, max: 2, over: overSeq, signal: true},
	IterDropWhile:   {name: "drop_while", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterDrop:        {name: "drop", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterWithIndex:   {name: "with_index", form: IterFormStage, min: 1, max: 1, over: overSeq},
	IterCycle:       {name: "cycle", form: IterFormStage, min: 1, max: 1, over: overSeq},
	IterConcat:      {name: "concat", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterChunks:      {name: "chunks", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterChunkBy:     {name: "chunk_by", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterFlatMap:     {name: "flat_map", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterFlatMapList: {name: "flat_map", form: IterFormStage, min: 2, max: 2, over: overSeq},
	IterZip:         {name: "zip", form: IterFormStage, min: 2, max: 2, over: overSeq},

	IterToList: {name: "to_list", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	// `count` and `known_count` are the two operations that read the domain
	// after a view could have been taken, because std declares an O(1)
	// override for exactly these families and the builder resolves the
	// protocol statically instead of dispatching.
	IterCount: {name: "count", form: IterFormTerminal, min: 1, max: 1, over: []IterDomain{
		IterOverSeq, IterOverList, IterOverMap,
	}},
	IterKnownCount: {name: "known_count", form: IterFormTerminal, min: 1, max: 1, over: []IterDomain{
		IterOverSeq, IterOverList, IterOverMap, IterOverString, IterOverSet, IterOverVector,
	}},
	IterEmpty:    {name: "empty?", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterNotEmpty: {name: "not_empty?", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterFirst:    {name: "first", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterLast:     {name: "last", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterAt:       {name: "at", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	IterEach:     {name: "each", form: IterFormTerminal, min: 2, max: 2, over: overSeq, signal: true},
	IterAny:      {name: "any?", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	IterAll:      {name: "all?", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	IterFind:     {name: "find", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	// `reduce` is the one operation whose arity is not fixed: the seed is
	// written as the callback's first-parameter DEFAULT, so a seeded fold has
	// three operands and an unseeded one folds from the first element.
	IterReduce:      {name: "reduce", form: IterFormTerminal, min: 2, max: 3, over: overSeq, signal: true},
	IterEachWhile:   {name: "each_while", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	IterToSet:       {name: "to_set", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterToVector:    {name: "to_vector", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterToMap:       {name: "to_map", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterGroupBy:     {name: "group_by", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	IterReverse:     {name: "reverse", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterJoin:        {name: "join", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	IterPartition:   {name: "partition", form: IterFormTerminal, min: 2, max: 2, over: overSeq},
	IterFlatten:     {name: "flatten", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	IterFlattenList: {name: "flatten", form: IterFormTerminal, min: 1, max: 1, over: overSeq},
	// sort_with's operands are the source and the comparator, then an
	// optional `Direction`: Descending compares (b, a) where Ascending
	// compares (a, b), as std's `Iter.sort` does.
	IterSortWith: {name: "sort_with", form: IterFormTerminal, min: 2, max: 3, over: overSeq},
	// sort_by's operands are the source, the key projection and the key's
	// ordering, then an optional `Direction`: Descending projects and
	// compares (b, a), as std's `Iter.sort_by` does.
	IterSortBy: {name: "sort_by", form: IterFormTerminal, min: 3, max: 4, over: overSeq},
}

// Name is the operation's Nomi spelling. `IterView` has none, because no
// program writes it.
func (o IterOp) Name() string {
	if s, known := iterOpSpecs[o]; known {
		return s.name
	}
	return "IterOp(" + strconv.Itoa(int(o)) + ")"
}

func (o IterOp) String() string { return o.Name() }

// Form is what the operation does to a sequence.
func (o IterOp) Form() IterForm { return iterOpSpecs[o].form }

// MinArity and MaxArity bracket the operand count. They differ for exactly one
// operation, `reduce`, whose seed is optional.
func (o IterOp) MinArity() int { return iterOpSpecs[o].min }

// MaxArity is the largest operand count this operation takes.
func (o IterOp) MaxArity() int { return iterOpSpecs[o].max }

// HasSignallingForm reports whether this operation's callback slot is one the
// front end lets `break` / `continue` / `return` reach.
func (o IterOp) HasSignallingForm() bool { return iterOpSpecs[o].signal }

// Admits reports whether this operation can run over a source in family d.
func (o IterOp) Admits(d IterDomain) bool {
	for _, ok := range iterOpSpecs[o].over {
		if ok == d {
			return true
		}
	}
	return false
}

// Iter is one `Iter.` operation over already-named operands.
type Iter struct {
	pos  Pos
	dst  Temp
	op   IterOp
	over IterDomain
	sig  bool
	args []Temp
}

// IterViewOperands is the operand count of a source view over domain d: the
// source, plus a Range's comparator and successor functions, or a user
// source's own `each_while`. A Range view may carry one more operand, the
// element's `Discrete.steps_between`, when the view enters a declared
// `Iter<T>` position and must answer the Range's `known_count`.
func IterViewOperands(d IterDomain) int {
	switch d {
	case IterOverRange:
		return 3
	case IterOverUserImpl:
		return 2
	}
	return 1
}

// NewIter is one iteration operation whose callback, if it has one, answers an
// ordinary value.
//
// pos is the CALL's own position: one per stage, plus the source's for the
// loop exit. Per stage is what a node per operation gives: a pipeline written
// across four lines has four instructions with four positions, where a text
// cursor has one that moves.
func NewIter(pos Pos, dst Temp, op IterOp, over IterDomain, args ...Temp) *Iter {
	return newIter(pos, dst, op, over, false, args)
}

// NewIterSignalling is the same operation with a callback that answers a
// CONTROL SIGNAL. It panics on an operation whose callback slot the front end
// does not sanction, so the widened convention cannot be claimed for an
// operation that has none.
func NewIterSignalling(pos Pos, dst Temp, op IterOp, over IterDomain, args ...Temp) *Iter {
	if !op.HasSignallingForm() {
		panic("ir.NewIterSignalling: " + op.Name() + " has no callback slot a control signal may reach")
	}
	return newIter(pos, dst, op, over, true, args)
}

func newIter(pos Pos, dst Temp, op IterOp, over IterDomain, sig bool, args []Temp) *Iter {
	requirePos(pos, "ir.NewIter")
	spec, known := iterOpSpecs[op]
	if !known {
		panic("ir.NewIter: unknown operation " + strconv.Itoa(int(op)))
	}
	if len(args) < spec.min || len(args) > spec.max {
		want := strconv.Itoa(spec.min)
		if spec.max != spec.min {
			want += " or " + strconv.Itoa(spec.max)
		}
		panic("ir.NewIter: " + op.Name() + " takes " + want + " operand(s), got " +
			strconv.Itoa(len(args)))
	}
	if !op.Admits(over) {
		panic("ir.NewIter: " + op.Name() + " does not run over a " + over.String())
	}
	if op == IterView && len(args) != IterViewOperands(over) && !(over == IterOverRange && len(args) == IterViewOperands(over)+1) {
		panic("ir.NewIter: a " + over.String() + " view takes " +
			strconv.Itoa(IterViewOperands(over)) + " operand(s), got " + strconv.Itoa(len(args)))
	}
	out := &Iter{pos: pos, dst: dst, op: op, over: over, sig: sig}
	out.args = append(out.args, args...)
	return out
}

// Op is the operation.
func (i *Iter) Op() IterOp { return i.op }

// Over is the representation family of operand zero. For a constructor it is
// IterOverNothing; for every stage and for every terminal but `count` and
// `known_count` it is IterOverSeq, because the source view already ran.
func (i *Iter) Over() IterDomain { return i.over }

// Signalling reports whether this operation's callback answers a control
// signal. See the file comment for why the bit is here and not on the closure.
func (i *Iter) Signalling() bool { return i.sig }

// NumArgs is the operand count.
func (i *Iter) NumArgs() int { return len(i.args) }

// Arg is operand n.
func (i *Iter) Arg(n int) Temp { return i.args[n] }

func (i *Iter) Pos() Pos  { return i.pos }
func (i *Iter) Dst() Temp { return i.dst }

func (i *Iter) AppendUses(dst []Temp) []Temp { return append(dst, i.args...) }

func (i *Iter) String() string {
	s := i.dst.String() + " = iter " + i.op.Name()
	if i.over != IterOverSeq && i.over != IterOverNothing {
		s += "(" + i.over.String() + ")"
	}
	if i.sig {
		s += "!"
	}
	for n, a := range i.args {
		if n == 0 {
			s += " "
		} else {
			s += ", "
		}
		s += a.String()
	}
	return s
}

func (i *Iter) irNode()  {}
func (i *Iter) irInstr() {}
