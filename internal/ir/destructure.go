package ir

// The `destructure` class: name what a pattern matched.
//
// This class contributes one node, because `match` and `destructure` are not
// two operations. They are one pattern vocabulary in two positions.
//
// The evidence is the producer's own, and it is written down at four of the
// class's owners. `internal/irbuild`'s `destructureFields` is "case.go's
// structFieldArms with the block-opening removed, since an irrefutable
// pattern has no arm to abandon"; `destructureTuple` is "case.go's tupleArm
// with the arm-abandoning removed"; `destructureVariantFields` is
// "variantFieldArms, again without the block-opening"; `mapDestructure`
// reproduces "the SAME five rules about map patterns" as `mapArm` and differs
// in exactly two, both of which are WHERE THE MISS GOES.
//
// So the two classes decompose into the same three operations, and only the
// third is new:
//
//	navigate to a sub-position   ir.Proj        proj.go
//	refute                       ir.Match       match.go
//	name what was matched        ir.Bind        here
//
// A destructure's DELIVERY of a failure — a trap for `{k => v} = m`, an
// assertion failure for `assert pat = e`, nothing at all for an irrefutable
// parameter — is the enclosing lowering's, which is match.go's argument and
// the fault/delivery division of ir.go's package header.
//
// WHY Bind AND NOT Copy. `ir.Copy` already moves one temporary into another
// and it carries no name. A binding INTRODUCES A NAME, and the name is what a
// reader of the graph needs to know which local a value is. The two nodes are
// therefore both needed: `internal/irbuild`'s `tupleDestructure` treats a fresh
// name and a name already bound in the same scope at the same type
// differently. A fresh name is a `Bind` and a rebind is a `Copy`.
//
// A COPY AND NOT AN ALIAS. The builder says why at `bindPattern`: "the arm
// body may rebind the name, and a subject expression is re-read by every later
// arm, so a name that aliased it would make the arm's meaning depend on where
// the tree happened to put it". So `Bind` writes a destination rather than
// renaming its source, and a consumer that can prove the copy unobservable may
// elide it.
//
// The symbol is minted, not interned, and that is correct here. `Table.Symbol`
// exists because two READS of
// one declaration must produce one symbol; a `Bind` is not a read, it IS the
// declaration, and two bindings of the same name in two arms are two
// declarations. So `NewSymbol` is the right constructor, which is the case its
// own comment names ("a producer naming something that genuinely has no prior
// identity — a synthesized binding").
//
// This node is where a local binding acquires its declaration identity. The
// pointer identity is what `ir.RefLocal` takes, so a later read of the name
// resolves to this binding.

// Bind introduces a name for the value in src.
type Bind struct {
	pos Pos
	dst Temp
	src Temp
	sym *Symbol
}

// NewBind names src. name is the binding's own declaration identity; see the
// header for why it is minted rather than interned.
//
// A DISCARDED BINDING IS NOT A NODE. `_` binds nothing, so
// a producer that reaches one must not build a Bind: there is no name and
// nothing reads the value.
func NewBind(pos Pos, dst, src Temp, name *Symbol) *Bind {
	requirePos(pos, "NewBind")
	if dst == NoTemp {
		panic("ir.NewBind: a binding with no destination names nothing")
	}
	if src == NoTemp {
		panic("ir.NewBind: a binding with no source has no value to name")
	}
	if name == nil {
		panic("ir.NewBind: a binding IS a declaration and needs its identity")
	}
	if name.Name() == "" {
		panic("ir.NewBind: a binding with an empty name cannot be read back")
	}
	return &Bind{pos: pos, dst: dst, src: src, sym: name}
}

// Sym is the declaration this binding introduces. Its identity is the
// pointer; its Name is what a consumer spells.
func (b *Bind) Sym() *Symbol { return b.sym }

// Src is the temporary whose value is named.
func (b *Bind) Src() Temp { return b.src }

func (b *Bind) Pos() Pos                     { return b.pos }
func (b *Bind) Dst() Temp                    { return b.dst }
func (b *Bind) AppendUses(dst []Temp) []Temp { return append(dst, b.src) }
func (b *Bind) String() string {
	return b.dst.String() + " = bind " + b.sym.Name() + " " + b.src.String()
}
func (b *Bind) irNode()  {}
func (b *Bind) irInstr() {}
