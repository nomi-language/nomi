package rt

import (
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"strconv"
)

// Nomi's `Int` is exact at any size: an int64 when it fits, a *BigInt when
// it does not, never a *BigInt that fits int64. Every constructor here
// keeps that canonical form. The package doc, "Int: exact at any size",
// gives the design; internal/vm's package doc, "Big Ints in the word bank",
// gives how the VM keeps a big value beside a word register.

// BigInt is an Int outside int64's range. It is immutable: nothing changes
// one after it is built, and Big hands out a copy.
type BigInt big.Int

// BigWord is the word a *BigInt occupies in a word register or a record's word
// slot. The value itself is beside the word, where the collector can see it: a
// register's shadow (internal/vm) or the record's big slots (record.go). The
// word is also math.MinInt64's own, so a reader that finds BigWord looks
// beside it, and finds nil when the value is that int64.
const BigWord = uint64(1) << 63

// bigView is b as the math/big value it is, for reading only.
func (b *BigInt) bigView() *big.Int { return (*big.Int)(b) }

// Big is a copy of b's value, which the caller may change.
func (b *BigInt) Big() *big.Int { return new(big.Int).Set(b.bigView()) }

// Sign is -1 or 1. A *BigInt is never zero.
func (b *BigInt) Sign() int { return b.bigView().Sign() }

// String is b in base 10.
func (b *BigInt) String() string { return b.bigView().String() }

// intOwned is z as an Int Value, taking z: the caller does not touch z again.
func intOwned(z *big.Int) Value {
	if z.IsInt64() {
		return z.Int64()
	}
	return (*BigInt)(z)
}

// IntFromBig is z's value as an Int Value. z is copied when it does not fit
// int64, so the caller keeps z.
func IntFromBig(z *big.Int) Value {
	if z.IsInt64() {
		return z.Int64()
	}
	return (*BigInt)(new(big.Int).Set(z))
}

// IntFromUint64 is u as an Int Value.
func IntFromUint64(u uint64) Value {
	if u <= math.MaxInt64 {
		return int64(u)
	}
	return (*BigInt)(new(big.Int).SetUint64(u))
}

// IsInt reports whether v is an Int Value: an int64 or a *BigInt.
func IsInt(v Value) bool {
	switch v.(type) {
	case int64, *BigInt:
		return true
	}
	return false
}

// IntBig is v's value as a fresh *big.Int. v must be an Int Value.
func IntBig(v Value) *big.Int {
	switch x := v.(type) {
	case int64:
		return big.NewInt(x)
	case *BigInt:
		return x.Big()
	}
	panic(notAnInt("IntBig", v))
}

// intView is v's value for reading: b's own value for a *BigInt, else scratch
// set to the int64.
func intView(v Value, scratch *big.Int) *big.Int {
	if b, ok := v.(*BigInt); ok {
		return b.bigView()
	}
	return scratch.SetInt64(v.(int64))
}

// IntSaturated is v as an int64, with a value past int64's range clamped to
// math.MaxInt64 or math.MinInt64. It is for an index, a count or a size, where
// a value past the range is past any collection, so the clamped value gives
// the same answer the exact one would: out of range, all of it, none of it.
func IntSaturated(v Value) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case *BigInt:
		if x.Sign() > 0 {
			return math.MaxInt64
		}
		return math.MinInt64
	}
	panic(notAnInt("IntSaturated", v))
}

// IntSign is -1, 0 or 1.
func IntSign(v Value) int {
	switch x := v.(type) {
	case int64:
		switch {
		case x < 0:
			return -1
		case x > 0:
			return 1
		}
		return 0
	case *BigInt:
		return x.Sign()
	}
	panic(notAnInt("IntSign", v))
}

// Int is a Nomi Int of any size, for a Go function bound to a `host fn` that
// handles every Int: the adapter generator passes an Int argument to an
// rt.Int parameter as it is, where an int64 parameter refuses a value past
// int64's range. The zero Int is 0.
//
// An Int that fits int64 is held as one, unboxed, so making one from a
// machine word or a register, and reading it back, allocates nothing; only
// Value boxes it. A larger one is its *BigInt.
type Int struct {
	big *BigInt // the value past int64's range, or nil
	n   int64   // the value when big is nil
}

// IntOf is the Int whose value v is: an int64 or a *BigInt.
func IntOf(v Value) Int {
	switch x := v.(type) {
	case int64:
		return Int{n: x}
	case *BigInt:
		return Int{big: x}
	}
	panic(notAnInt("IntOf", v))
}

// SmallInt is n as an Int.
func SmallInt(n int64) Int { return Int{n: n} }

// IntOfWord is the Int a word and the value beside it hold, as a VM
// register or a record's word slot holds one: b when the word is BigWord
// and b is set, else the word's int64.
func IntOfWord(w uint64, b *BigInt) Int {
	if w == BigWord && b != nil {
		return Int{big: b}
	}
	return Int{n: int64(w)}
}

// Word is i as a word and the value beside it: BigWord and its *BigInt for
// an Int past int64's range, else its int64's bits and nil.
func (i Int) Word() (uint64, *BigInt) {
	if i.big != nil {
		return BigWord, i.big
	}
	return uint64(i.n), nil
}

// BigIntOf is z's value as an Int; the caller keeps z.
func BigIntOf(z *big.Int) Int { return IntOf(IntFromBig(z)) }

// UintInt is u as an Int.
func UintInt(u uint64) Int {
	if u <= math.MaxInt64 {
		return Int{n: int64(u)}
	}
	return Int{big: (*BigInt)(new(big.Int).SetUint64(u))}
}

// Value is i as the Int Value the VM holds.
func (i Int) Value() Value {
	if i.big != nil {
		return i.big
	}
	return i.n
}

// Int64 is i as an int64 when it fits.
func (i Int) Int64() (int64, bool) { return i.n, i.big == nil }

// Low64 is i's low 64 bits, two's complement, as Wrap64 reads them.
func (i Int) Low64() uint64 {
	if i.big != nil {
		return low64(i.big.bigView())
	}
	return uint64(i.n)
}

// Big is i's value as a fresh *big.Int.
func (i Int) Big() *big.Int {
	if i.big != nil {
		return i.big.Big()
	}
	return big.NewInt(i.n)
}

// Sign is -1, 0 or 1.
func (i Int) Sign() int {
	switch {
	case i.big != nil:
		return i.big.Sign()
	case i.n < 0:
		return -1
	case i.n > 0:
		return 1
	}
	return 0
}

// Saturated is i as an int64, clamped past its range (IntSaturated).
func (i Int) Saturated() int64 {
	switch {
	case i.big == nil:
		return i.n
	case i.big.Sign() > 0:
		return math.MaxInt64
	}
	return math.MinInt64
}

// String is i in base 10.
func (i Int) String() string { return FormatIntValue(i.Value()) }

// Format writes i in base 10 for every verb, so a message formats an Int
// with %d as it does an int64.
func (i Int) Format(f fmt.State, _ rune) { fmt.Fprint(f, i.String()) }

// --- arithmetic ----------------------------------------------------------
//
// Each operation takes two Int Values and answers the exact result in
// canonical form. The int64 pair is the fast path and allocates nothing when
// the result fits; the VM inlines that path itself and calls these only when
// an operand is big or the result leaves int64.

// IntAdd is a + b.
func IntAdd(a, b Value) Value {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok && !AddOverflows(x, y) {
			return x + y
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).Add(intView(a, &sa), intView(b, &sb)))
}

// IntSub is a - b.
func IntSub(a, b Value) Value {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok && !SubOverflows(x, y) {
			return x - y
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).Sub(intView(a, &sa), intView(b, &sb)))
}

// IntMul is a * b.
func IntMul(a, b Value) Value {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok && !MulOverflows(x, y) {
			return x * y
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).Mul(intView(a, &sa), intView(b, &sb)))
}

// IntQuo is a / b truncated toward zero, as Nomi's `/` is. ok is false for a
// zero b.
func IntQuo(a, b Value) (q Value, ok bool) {
	if IntSign(b) == 0 {
		return nil, false
	}
	if x, isSmall := a.(int64); isSmall {
		if y, isSmall := b.(int64); isSmall && !QuoOverflows(x, y) {
			return x / y, true
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).Quo(intView(a, &sa), intView(b, &sb))), true
}

// IntRem is a % b: the remainder of IntQuo, with a's sign. ok is false for a
// zero b.
func IntRem(a, b Value) (r Value, ok bool) {
	if IntSign(b) == 0 {
		return nil, false
	}
	if x, isSmall := a.(int64); isSmall {
		if y, isSmall := b.(int64); isSmall {
			// Go's MinInt64 % -1 is 0 without a panic, which is the answer.
			return x % y, true
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).Rem(intView(a, &sa), intView(b, &sb))), true
}

// IntNeg is -a.
func IntNeg(a Value) Value {
	if x, ok := a.(int64); ok && x != math.MinInt64 {
		return -x
	}
	var sa big.Int
	return intOwned(new(big.Int).Neg(intView(a, &sa)))
}

// The bitwise operations read an Int as two's complement with as many bits
// as it needs, a negative one with infinitely many leading ones, as math/big's
// And, Or, Xor and Not do. So they agree with int64's for every pair of int64
// operands.

// IntAnd is a &&& b.
func IntAnd(a, b Value) Value {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			return x & y
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).And(intView(a, &sa), intView(b, &sb)))
}

// IntOr is a ||| b.
func IntOr(a, b Value) Value {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			return x | y
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).Or(intView(a, &sa), intView(b, &sb)))
}

// IntXor is a ^^^ b.
func IntXor(a, b Value) Value {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			return x ^ y
		}
	}
	var sa, sb big.Int
	return intOwned(new(big.Int).Xor(intView(a, &sa), intView(b, &sb)))
}

// IntNot is ~~~a, the one's complement: -a - 1.
func IntNot(a Value) Value {
	if x, ok := a.(int64); ok {
		return ^x
	}
	var sa big.Int
	return intOwned(new(big.Int).Not(intView(a, &sa)))
}

// IntShl is a <<< n: a times 2 to the n, exact, so no bit is shifted out.
// ok is false for a count ShiftCountProblem refuses, one whose result would
// not fit in memory. `<<<` takes a NonNegativeInt count, so no checked
// program passes a negative one; a negative count shifts right by its
// size, so the operation has an answer for every count.
func IntShl(a, n Value) (v Value, ok bool) {
	if IntSign(n) < 0 {
		return IntShr(a, IntNeg(n))
	}
	if ShiftCountProblem(n, true) != "" {
		return nil, false
	}
	count := IntSaturated(n)
	if IntSign(a) == 0 {
		return int64(0), true
	}
	if x, small := a.(int64); small && count < 63 {
		if s := x << count; s>>count == x {
			return s, true
		}
	}
	var sa big.Int
	return intOwned(new(big.Int).Lsh(intView(a, &sa), uint(count))), true
}

// IntShr is a >>> n: a divided by 2 to the n, rounded toward negative
// infinity, an arithmetic shift. A count past a's bits answers 0 for a
// non-negative a and -1 for a negative one. A negative count, which no
// checked program passes, shifts left by its size, and ok is false where
// that left shift's would be (IntShl).
func IntShr(a, n Value) (v Value, ok bool) {
	if IntSign(n) < 0 {
		return IntShl(a, IntNeg(n))
	}
	count := IntSaturated(n)
	if x, small := a.(int64); small {
		if count > 63 {
			count = 63
		}
		return x >> count, true
	}
	var sa big.Int
	return intOwned(new(big.Int).Rsh(intView(a, &sa), uint(count))), true
}

// ShiftCountProblem is why n is no count for a shift, left or right, or ""
// when it is one: a shift that moves left, a left shift by n or a right
// shift by a negative n, must leave a result that fits in memory (maxShift
// bits). A shift that moves right takes any count.
func ShiftCountProblem(n Value, left bool) string {
	if IntSign(n) < 0 {
		left = !left
		n = IntNeg(n)
	}
	if left && IntSaturated(n) > maxShift {
		return fmt.Sprintf("shift count %s is too large: the result would have more than %d bits", FormatIntValue(n), maxShift)
	}
	return ""
}

// ShiftCountError is the fault `<<<` and `>>>` report at line for a count
// ShiftCountProblem refuses.
func ShiftCountError(line int, n Value, left bool) error {
	return positioned(line, linePrefix(line)+ShiftCountProblem(n, left))
}

// IntCompare is -1, 0 or 1 as a is less than, equal to or greater than b.
func IntCompare(a, b Value) int {
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	}
	var sa, sb big.Int
	return intView(a, &sa).Cmp(intView(b, &sb))
}

// IntEqual is a == b. In canonical form an int64 never equals a *BigInt, so
// only two *BigInts compare digits.
func IntEqual(a, b Value) bool {
	switch x := a.(type) {
	case int64:
		y, ok := b.(int64)
		return ok && x == y
	case *BigInt:
		y, ok := b.(*BigInt)
		return ok && x.bigView().Cmp(y.bigView()) == 0
	}
	return false
}

// --- 64-bit modular arithmetic -------------------------------------------

// Wrap64 is v reduced into int64's range modulo 2^64, two's complement: the
// int64 whose low 64 bits are v's. It is what `Int.wrapping_add` and its
// siblings answer, and what `derive Hashable`'s mixers keep a hash in.
func Wrap64(v Value) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case *BigInt:
		return int64(low64(x.bigView()))
	}
	panic(notAnInt("Wrap64", v))
}

// low64 is the low 64 bits of z in two's complement.
func low64(z *big.Int) uint64 {
	words := z.Bits()
	var u uint64
	if len(words) > 0 {
		u = uint64(words[0])
		if bits.UintSize == 32 && len(words) > 1 {
			u |= uint64(words[1]) << 32
		}
	}
	if z.Sign() < 0 {
		u = -u
	}
	return u
}

// --- text ----------------------------------------------------------------

// FormatIntValue renders an Int Value in base 10.
func FormatIntValue(v Value) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case *BigInt:
		return x.String()
	}
	panic(notAnInt("FormatIntValue", v))
}

// ParseIntValue parses an optional sign and base-10 digits, any number of
// them, as an Int Value. ok is false for anything else.
func ParseIntValue(s string) (Value, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return n, true
	}
	if ne, isNum := err.(*strconv.NumError); !isNum || ne.Err != strconv.ErrRange {
		return nil, false
	}
	z, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, false
	}
	return intOwned(z), true
}

// --- hashing -------------------------------------------------------------

// HashIntValue is the structural hash of an Int Value. An int64 hashes as
// HashInt; a *BigInt folds its words, and needs to avoid no int64's hash,
// since a *BigInt is never equal to an int64.
func HashIntValue(v Value) uint64 {
	switch x := v.(type) {
	case int64:
		return HashInt(x)
	case *BigInt:
		return hashBig(x)
	}
	panic(notAnInt("HashIntValue", v))
}

func hashBig(b *BigInt) uint64 {
	z := b.bigView()
	h := uint64(0x6a09e667f3bcc909)
	if z.Sign() < 0 {
		h = ^h
	}
	for _, w := range z.Bits() {
		h = HashMix(h, uint64(w))
	}
	return h
}

func notAnInt(fn string, v Value) string {
	return fmt.Sprintf("rt: %s: %T is not an Int", fn, v)
}
