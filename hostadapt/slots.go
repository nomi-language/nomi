package hostadapt

import (
	"math"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// MaxSlots is the most operands a register form takes, and the most parts
// its answer has.
const MaxSlots = 8

// Slots carries one crossing's operands into a register form, and its
// answer back, unboxed, as an engine's registers and a record's slots hold
// them. Operand i is in W[i], S[i] or R[i] by its slot type (rt.SlotType):
// an Int, a Float's bits, a Bool or a Byte in W, an Int past int64's range
// as rt.BigWord in W with its value in Big; a String or a Bytes in S; any
// other value, boxed, in R. The answer's parts go in the same arrays from
// index 0 by their own slot types: one for a value or a payload, one per
// component of a tuple, none for Unit. A None or an Err is its record in
// R[0].
//
// An engine keeps one Slots per goroutine and reuses it, so a register
// form keeps nothing of it past its return.
type Slots struct {
	W   [MaxSlots]uint64
	Big [MaxSlots]*rt.BigInt
	S   [MaxSlots]string
	R   [MaxSlots]rt.Value
}

// RegFunc is a bound adapter's register form: the same call as the
// adapter, over operands and an answer in slots instead of boxed values. It
// answers whether the answer is a Some or an Ok, for a binding that answers
// an Option or a Result, and true for any other. It fails as the adapter
// does, with the same errors.
type RegFunc func(fr *rt.Frame, s *Slots) (carried bool, err error)

// Regs is a binding's register form and the slot types of its operands
// and its answer's parts, which an engine reads its registers by.
type Regs struct {
	// Key is the binding's name, which a refused operand's error names.
	Key string
	Fn  RegFunc
	In  []rt.SlotType
	Out []rt.SlotType
	// Carries says the binding answers an Option or a Result: Out is the
	// payload's parts, written when the form answers carried.
	Carries bool
	// Whole is the descriptor of the Option or Result a carrying form
	// answers, and Tuple that of the tuple its parts make when it has more
	// than one: what Answer builds the whole answer with.
	Whole, Tuple *rt.TypeDesc
}

// Row is one binding's register form as a generated table lists it: a
// method of the table's bound descriptors (B) over slots, and the Option or
// Result spec a carrying answer is (nil for one that carries none).
type Row[B any] struct {
	Key     string
	Fn      func(B, *rt.Frame, *Slots) (bool, error)
	In, Out []rt.SlotType
	Whole   *DescSpec
}

// BindRows binds a generated table's register forms over its bound
// descriptors b: each is handed to env.AddRegs, and its adapter, the same
// call over boxed values (Regs.Func), goes into funcs by key. A binding
// with a register form has no other adapter, so its conversions are
// written once.
func BindRows[B any](env *Env, b B, rows []Row[B], funcs map[string]Func) error {
	for i := range rows {
		row := &rows[i]
		fn := row.Fn
		r := &Regs{Key: row.Key, In: row.In, Out: row.Out, Carries: row.Whole != nil,
			Fn: func(fr *rt.Frame, s *Slots) (bool, error) { return fn(b, fr, s) }}
		if row.Whole != nil {
			d, err := env.Desc(row.Whole)
			if err != nil {
				return err
			}
			r.Whole = d
		}
		if len(row.Out) > 1 {
			r.Tuple = rt.TupleDesc(row.Out...)
		}
		if env.AddRegs != nil {
			env.AddRegs(row.Key, *r)
		}
		funcs[row.Key] = r.Func()
	}
	return nil
}

// slotsPool holds the slots Func's calls take, so an adapter called over
// boxed values allocates none of its own.
var slotsPool = sync.Pool{New: func() any { return new(Slots) }}

// Func is the binding's adapter: the register form over boxed operands,
// laid into slots of its own (Call).
func (r *Regs) Func() Func {
	return func(fr *rt.Frame, args []rt.Value) (rt.Value, error) {
		s := slotsPool.Get().(*Slots)
		v, err := r.Call(fr, s, args)
		slotsPool.Put(s)
		return v, err
	}
}

// Call runs the register form over boxed operands in slots s: each operand
// laid into its slot, refused as an adapter's conversion refuses a value
// of another kind, and the answer built whole (Answer). It leaves s clear.
func (r *Regs) Call(fr *rt.Frame, s *Slots, args []rt.Value) (rt.Value, error) {
	if len(args) != len(r.In) {
		return nil, Arity(r.Key, len(r.In), len(args))
	}
	for i, a := range args {
		if !s.set(i, r.In[i], a) {
			s.Clear(i)
			return nil, Arg(r.Key, i, Want(slotNames[r.In[i]], a))
		}
	}
	carried, err := r.Fn(fr, s)
	var v rt.Value
	if err == nil {
		v = r.Answer(s, carried)
	}
	s.Clear(max(len(r.In), len(r.Out)))
	return v, err
}

// slotNames is the Nomi type a scalar slot holds, as a refusal names it.
var slotNames = [...]string{
	rt.SlotRef: "", rt.SlotInt: "Int", rt.SlotFloat: "Float", rt.SlotBool: "Bool",
	rt.SlotByte: "Byte", rt.SlotString: "String", rt.SlotBytes: "Bytes",
}

// set lays the boxed value v into slot i as slot type st holds it, and
// reports whether v is of st's kind. Any value goes into a reference slot,
// which the form converts itself.
func (s *Slots) set(i int, st rt.SlotType, v rt.Value) bool {
	switch st {
	case rt.SlotInt:
		switch x := v.(type) {
		case int64:
			s.W[i], s.Big[i] = uint64(x), nil
		case *rt.BigInt:
			s.SetIntValue(i, x)
		default:
			return false
		}
	case rt.SlotFloat:
		x, ok := v.(float64)
		s.W[i] = rt.FloatWord(x)
		return ok
	case rt.SlotBool:
		x, ok := v.(bool)
		s.W[i] = rt.BoolWord(x)
		return ok
	case rt.SlotByte:
		x, ok := v.(rt.Byte)
		s.W[i] = rt.ByteWord(x)
		return ok
	case rt.SlotString:
		x, ok := v.(string)
		s.S[i] = x
		return ok
	case rt.SlotBytes:
		x, ok := v.(rt.Bytes)
		s.S[i] = string(x)
		return ok
	default:
		s.R[i] = v
	}
	return true
}

// Answer is the whole answer a call of the form left in s: Unit for no
// parts, the one part, or a tuple of them, and for a carrying form the
// Some or Ok holding it, or the None or Err record in R[0] when it
// answered carried false.
func (r *Regs) Answer(s *Slots, carried bool) rt.Value {
	switch {
	case !r.Carries:
		return r.Payload(s)
	case !carried:
		return s.R[0]
	}
	rec := r.Whole.NewVariant(0)
	f := &rec.Variant().Fields[0]
	if len(r.Out) == 1 {
		s.store(rec, f, 0)
	} else {
		rec.R[f.Slot] = r.Payload(s)
	}
	return rec
}

// Payload is the value the parts a call of the form left in s make: Unit
// for none, the one part, or a tuple of them. For a carrying form that
// answered carried, it is the Some's or Ok's payload.
func (r *Regs) Payload(s *Slots) rt.Value {
	switch len(r.Out) {
	case 0:
		return rt.Unit{}
	case 1:
		return s.Value(0, r.Out[0])
	}
	tup := r.Tuple.New()
	for i := range r.Out {
		s.store(tup, &tup.Desc.Fields[i], i)
	}
	return tup
}

// store writes part i into field f of record rec, which holds it as the
// part's slot does.
func (s *Slots) store(rec *rt.Record, f *rt.FieldDesc, i int) {
	switch f.Type.Bank() {
	case rt.BankWord:
		rec.W[f.Slot] = s.W[i]
		if f.Type == rt.SlotInt && s.W[i] == rt.BigWord && s.Big[i] != nil {
			rec.SetBigSlot(f.Slot, s.Big[i])
		}
	case rt.BankStr:
		rec.S[f.Slot] = s.S[i]
	default:
		rec.R[f.Slot] = s.R[i]
	}
}

// Int is operand i as an Int.
func (s *Slots) Int(i int) rt.Int { return rt.IntOfWord(s.W[i], s.Big[i]) }

// IntValue is operand i as an Int Value, boxed.
func (s *Slots) IntValue(i int) rt.Value {
	if s.W[i] == rt.BigWord && s.Big[i] != nil {
		return s.Big[i]
	}
	return int64(s.W[i])
}

// SetInt writes the Int n into part i.
func (s *Slots) SetInt(i int, n rt.Int) { s.W[i], s.Big[i] = n.Word() }

// SetInt64 writes the int64 n into part i.
func (s *Slots) SetInt64(i int, n int64) { s.W[i], s.Big[i] = uint64(n), nil }

// SetUint64 writes the Int u into part i.
func (s *Slots) SetUint64(i int, u uint64) {
	if u <= math.MaxInt64 {
		s.W[i], s.Big[i] = u, nil
		return
	}
	s.SetInt(i, rt.UintInt(u))
}

// SetIntValue writes the Int Value v into part i.
func (s *Slots) SetIntValue(i int, v rt.Value) { s.SetInt(i, rt.IntOf(v)) }

// SlotSigned is operand i, an Int, as Go signed integer type T, refusing a
// value T cannot hold as ToSigned does.
func SlotSigned[T Signed](s *Slots, i int, goType string) (T, error) {
	if s.W[i] == rt.BigWord && s.Big[i] != nil {
		return ToSigned[T](s.Big[i], goType)
	}
	n := int64(s.W[i])
	if t := T(n); int64(t) == n {
		return t, nil
	}
	return ToSigned[T](n, goType)
}

// SlotUnsigned is operand i, an Int, as Go unsigned integer type T, refusing a
// value T cannot hold as ToUnsigned does.
func SlotUnsigned[T Unsigned](s *Slots, i int, goType string) (T, error) {
	if s.W[i] == rt.BigWord && s.Big[i] != nil {
		return ToUnsigned[T](s.Big[i], goType)
	}
	n := int64(s.W[i])
	if t := T(n); n >= 0 && uint64(t) == uint64(n) {
		return t, nil
	}
	return ToUnsigned[T](n, goType)
}

// Value is part i of slot type st, boxed as the rt value of its kind.
func (s *Slots) Value(i int, st rt.SlotType) rt.Value {
	switch st {
	case rt.SlotInt:
		return s.IntValue(i)
	case rt.SlotFloat:
		return rt.WordFloat(s.W[i])
	case rt.SlotBool:
		return rt.WordBool(s.W[i])
	case rt.SlotByte:
		return rt.WordByte(s.W[i])
	case rt.SlotString:
		return s.S[i]
	case rt.SlotBytes:
		return rt.Bytes(s.S[i])
	}
	return s.R[i]
}

// Clear drops the first n parts' references, so the slots keep nothing a
// crossing handed over alive.
func (s *Slots) Clear(n int) {
	// Entry by entry, and only those set: a bulk clear of slices that hold
	// pointers takes the collector's write barrier over every word.
	for i := range n {
		if s.Big[i] != nil {
			s.Big[i] = nil
		}
		if s.S[i] != "" {
			s.S[i] = ""
		}
		if s.R[i] != nil {
			s.R[i] = nil
		}
	}
}
