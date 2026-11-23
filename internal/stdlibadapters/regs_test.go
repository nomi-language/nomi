package stdlibadapters

import (
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// regsTable binds the adapters' register forms (hostadapt.Regs) by binding
// name.
func regsTable(t *testing.T) map[string]hostadapt.Regs {
	t.Helper()
	regs := map[string]hostadapt.Regs{}
	env := &hostadapt.Env{AddRegs: func(name string, r hostadapt.Regs) { regs[name] = r }}
	if _, err := Bind(env); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return regs
}

// intoSlots lays operands into slots by their slot types, as a machine
// copies them from its registers, and reports whether each operand is one
// a register of its slot type holds: a case that hands an adapter a value
// of the wrong kind, to see it refused, has no register form to compare.
func intoSlots(s *hostadapt.Slots, in []rt.SlotType, args []rt.Value) bool {
	if len(args) != len(in) {
		return false
	}
	for i, a := range args {
		s.W[i], s.Big[i], s.S[i], s.R[i] = 0, nil, "", nil
		switch in[i] {
		case rt.SlotInt:
			switch x := a.(type) {
			case int64:
				s.SetInt64(i, x)
			case *rt.BigInt:
				s.SetIntValue(i, x)
			default:
				return false
			}
		case rt.SlotFloat:
			x, ok := a.(float64)
			if !ok {
				return false
			}
			s.W[i] = rt.FloatWord(x)
		case rt.SlotBool:
			x, ok := a.(bool)
			if !ok {
				return false
			}
			s.W[i] = rt.BoolWord(x)
		case rt.SlotByte:
			x, ok := a.(rt.Byte)
			if !ok {
				return false
			}
			s.W[i] = rt.ByteWord(x)
		case rt.SlotString:
			x, ok := a.(string)
			if !ok {
				return false
			}
			s.S[i] = x
		case rt.SlotBytes:
			x, ok := a.(rt.Bytes)
			if !ok {
				return false
			}
			s.S[i] = string(x)
		default:
			s.R[i] = a
		}
	}
	return true
}

// sameRegs holds a register form's answer to the adapter's whole answer for
// the same operands: a failure for a failure, and the answer's parts for
// the answer: the value itself, a tuple's components, or for an Option or a
// Result the payload's parts with carried, or the None or Err record
// without. shapeOnly compares which of these it was and not the values,
// for an answer that differs from call to call. It answers whether the
// form ran.
//
// The adapter of a binding with a register form is that form over boxed
// values (hostadapt.Regs.Func), so the recorded outcomes hold the form's
// conversions themselves, and this holds what an engine does around it:
// operands laid in as a machine copies them from its registers
// (intoSlots), against Regs.Call's laying of boxed ones, and the parts a
// machine reads back, against the whole answer Regs.Answer builds from
// them, which a destination that keeps the answer whole gets.
func sameRegs(t *testing.T, key string, regs hostadapt.Regs, whole hostOutcome, call func(*hostadapt.Slots) (bool, error), args []rt.Value, shapeOnly bool) bool {
	t.Helper()
	var s hostadapt.Slots
	if regs.Fn == nil || !intoSlots(&s, regs.In, args) {
		return false
	}
	var carried bool
	got := classify(func() (rt.Value, error) {
		var err error
		carried, err = call(&s)
		return nil, err
	})
	if (whole.err != nil) != (got.err != nil) || whole.fault != got.fault || whole.panic != got.panic {
		t.Errorf("%s: the register form fails with %v / %q / %q, the adapter with %v / %q / %q",
			key, got.err, got.fault, got.panic, whole.err, whole.fault, whole.panic)
		return true
	}
	if whole.err != nil {
		if whole.err.Error() != got.err.Error() {
			t.Errorf("%s: the register form fails with %v, the adapter with %v", key, got.err, whole.err)
		}
		return true
	}
	if whole.fault != "" || whole.panic != "" {
		return true
	}
	want := whole.val
	if regs.Carries {
		rec, isRec := want.(*rt.Record)
		if !isRec || rec.Desc.Kind != rt.KindEnum {
			t.Errorf("%s: the adapter answers %T, not an Option or a Result", key, want)
			return true
		}
		variant := rec.Desc.Variants[rec.Tag].Name
		carries := variant == "Some" || variant == "Ok"
		if carried != carries {
			t.Errorf("%s: the adapter answers %s and the register form says carried = %v", key, variant, carried)
			return true
		}
		if !carries {
			if !shapeOnly && !sameValue(s.R[0], want) {
				t.Errorf("%s: the register form answers %#v, the adapter %#v", key, s.R[0], want)
			}
			return true
		}
		want = rec.Field(0)
	} else if !carried {
		t.Errorf("%s: a register form that carries no Option answers carried = false", key)
		return true
	}
	if shapeOnly {
		return true
	}
	switch len(regs.Out) {
	case 0:
		if _, isUnit := want.(rt.Unit); !isUnit {
			t.Errorf("%s: the register form answers no parts and the adapter %#v", key, want)
		}
	case 1:
		if v := s.Value(0, regs.Out[0]); !sameValue(v, want) {
			t.Errorf("%s: the register form answers %#v, the adapter %#v", key, v, want)
		}
	default:
		tup, isTuple := want.(*rt.Record)
		if !isTuple || tup.Desc.Kind != rt.KindTuple || len(tup.Desc.Fields) != len(regs.Out) {
			t.Errorf("%s: the register form answers %d parts and the adapter %#v", key, len(regs.Out), want)
			return true
		}
		for j, st := range regs.Out {
			if v := s.Value(j, st); !sameValue(v, tup.Field(j)) {
				t.Errorf("%s: the register form's part %d is %#v, the adapter's %#v", key, j, v, tup.Field(j))
			}
		}
	}
	return true
}

// sameValue reports whether two answers are the same value: equal, or a
// handle each call makes afresh (a Builder, a Context) that reads the same
// in the recordings' canonical text.
func sameValue(a, b rt.Value) bool {
	return rt.Equal(a, b) || canonText(a, false) == canonText(b, false)
}
