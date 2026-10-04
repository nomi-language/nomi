package ir

import "fmt"

// FuncValue creates a callable with an explicit body and captured operands: a
// flat closure. Captures supply the body's trailing parameters; ordinary call
// arguments supply the prefix. A capture is the operand's VALUE when the
// FuncValue runs: a Nomi closure sees what a name held when the closure was
// made, and a later rebinding of the name is a new binding it never sees
// (spec §23). Nothing a later instruction does can change a closure.
//
// A recursive nested fn refers to itself through its own closure: its FIRST
// capture is the closure being built (Self), which has no operand. The
// consumer fills it with the new value when it builds it, so the self
// reference is the closure's own identity and exists only once the closure
// is complete.
type FuncValue struct {
	pos      Pos
	dst      Temp
	body     *Func
	self     bool
	captures []Temp
}

func NewFuncValue(pos Pos, dst Temp, body *Func, captures ...Temp) *FuncValue {
	return newFuncValue(pos, dst, body, false, captures)
}

// NewRecursiveFuncValue is a FuncValue whose first capture is the closure
// itself; captures supply the rest.
func NewRecursiveFuncValue(pos Pos, dst Temp, body *Func, captures ...Temp) *FuncValue {
	return newFuncValue(pos, dst, body, true, captures)
}

func newFuncValue(pos Pos, dst Temp, body *Func, self bool, captures []Temp) *FuncValue {
	requirePos(pos, "NewFuncValue")
	n := &FuncValue{pos: pos, dst: dst, body: body, self: self, captures: append([]Temp(nil), captures...)}
	if dst == NoTemp || body == nil || n.NumCaptures() > len(body.Params()) {
		panic("ir: invalid function value")
	}
	for _, t := range captures {
		if t == NoTemp {
			panic("ir: function value capture has no operand")
		}
	}
	return n
}

func (n *FuncValue) Body() *Func { return n.body }

// Self reports whether the first capture is the closure itself.
func (n *FuncValue) Self() bool { return n.self }

// NumCaptures is the number of trailing parameters the closure supplies,
// the self capture included.
func (n *FuncValue) NumCaptures() int {
	if n.self {
		return len(n.captures) + 1
	}
	return len(n.captures)
}

// Capture is the operand of capture i, or NoTemp for the self capture.
func (n *FuncValue) Capture(i int) Temp {
	if n.self {
		if i == 0 {
			return NoTemp
		}
		return n.captures[i-1]
	}
	return n.captures[i]
}

func (n *FuncValue) Arity() int                   { return len(n.body.Params()) - n.NumCaptures() }
func (n *FuncValue) Pos() Pos                     { return n.pos }
func (n *FuncValue) Dst() Temp                    { return n.dst }
func (n *FuncValue) AppendUses(dst []Temp) []Temp { return append(dst, n.captures...) }
func (n *FuncValue) String() string {
	if n.self {
		return fmt.Sprintf("%s = function %s captures self %v", n.dst, n.body.Name(), n.captures)
	}
	return fmt.Sprintf("%s = function %s captures %v", n.dst, n.body.Name(), n.captures)
}
func (*FuncValue) irNode()  {}
func (*FuncValue) irInstr() {}
