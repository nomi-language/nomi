package rt

import (
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"strconv"
)

// Nomi's `Int` is exact at any size. A value that fits int64 is an int64
// everywhere it is today: in a Value, in a VM word register, in a record's
// word slot. A value that does not fit is a *BigInt, a heap value over
// math/big. There is one canonical form, and every constructor here keeps it:
// a value that fits int64 is never a *BigInt. So two equal Ints have the same
// Go type, equality and hashing compare like with like, and a Map keyed by
// Int needs no normalization at its use sites.
//
// docs/implementation-notes.md ("Int: exact at any size") gives the design,
// including how the VM keeps a big value beside a word register.

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
type Int struct{ v Value }

// IntOf is the Int whose value v is: an int64 or a *BigInt.
func IntOf(v Value) Int {
	if !IsInt(v) {
		panic(notAnInt("IntOf", v))
	}
	return Int{v}
}

// SmallInt is n as an Int.
func SmallInt(n int64) Int { return Int{n} }

// BigIntOf is z's value as an Int; the caller keeps z.
func BigIntOf(z *big.Int) Int { return Int{IntFromBig(z)} }

// Value is i as the Int Value the VM holds.
func (i Int) Value() Value {
	if i.v == nil {
		return int64(0)
	}
	return i.v
}

// Int64 is i as an int64 when it fits.
func (i Int) Int64() (int64, bool) {
	n, ok := i.Value().(int64)
	return n, ok
}

// Big is i's value as a fresh *big.Int.
func (i Int) Big() *big.Int { return IntBig(i.Value()) }

// Sign is -1, 0 or 1.
func (i Int) Sign() int { return IntSign(i.Value()) }

// Saturated is i as an int64, clamped past its range (IntSaturated).
func (i Int) Saturated() int64 { return IntSaturated(i.Value()) }

// String is i in base 10.
func (i Int) String() string { return FormatIntValue(i.Value()) }

// DurationNanosInRange is std/duration's `nanos_in_range`: ns when a Duration
// holds it, a signed 64-bit count of nanoseconds; a fault otherwise.
func DurationNanosInRange(ns Int) int64 {
	n, ok := ns.Int64()
	if !ok {
		Trap(fmt.Sprintf("Duration out of range: %s nanoseconds is more than a Duration holds, about 292 years either way", ns))
	}
	return n
}

// InstantNanosInRange is std/instant's `nanos_in_range`: ns when an Instant
// holds it, a signed 64-bit count of nanoseconds from the epoch; a fault
// otherwise.
func InstantNanosInRange(ns Int) int64 {
	n, ok := ns.Int64()
	if !ok {
		Trap(fmt.Sprintf("Instant out of range: %s nanoseconds from the epoch is past what an Instant holds, the years 1677 to 2262", ns))
	}
	return n
}

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
