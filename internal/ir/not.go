package ir

// Not negates one Bool and writes a Bool. Unlike arithmetic negation it cannot
// overflow and does not carry an arithmetic domain or fault policy.
type Not struct {
	pos      Pos
	dst, val Temp
}

func NewNot(pos Pos, dst, val Temp) *Not {
	requirePos(pos, "ir.NewNot")
	if dst == NoTemp || val == NoTemp {
		panic("ir.NewNot needs a destination and operand")
	}
	return &Not{pos: pos, dst: dst, val: val}
}
func (n *Not) Pos() Pos                     { return n.pos }
func (n *Not) Dst() Temp                    { return n.dst }
func (n *Not) Val() Temp                    { return n.val }
func (n *Not) AppendUses(dst []Temp) []Temp { return append(dst, n.val) }
func (n *Not) String() string               { return n.dst.String() + " = !" + n.val.String() }
func (n *Not) irNode()                      {}
func (n *Not) irInstr()                     {}
