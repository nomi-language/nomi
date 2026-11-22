package rt

import (
	"math"
	"math/bits"
)

// std::int's fixed-width bit operations: rotations, zero counts, byte swaps
// and modular arithmetic at 32 and 64 bits, for hashes, checksums and wire
// formats defined over machine words. An Int has any size, so each reads
// the low 32 or 64 bits of its operands, two's complement. A 32-bit answer
// is unsigned, 0 to 2^32 - 1, which an Int64 holds; a 64-bit answer is the
// two's complement Int64, as Int.wrapping_add's is, so it stays in a
// machine word.

// low32Of is n's low 32 bits.
func low32Of(n Int) uint32 { return uint32(n.Low64()) }

// rotation is k taken modulo width, for a left rotation: a negative k
// rotates right. A k past int64's range is reduced exactly.
func rotation(k Int, width int64) int {
	if x, ok := k.Int64(); ok {
		r := x % width
		if r < 0 {
			r += width
		}
		return int(r)
	}
	r, _ := IntRem(k.Value(), width)
	m := r.(int64)
	if m < 0 {
		m += width
	}
	return int(m)
}

// IntRotateLeft32 is `Int.rotate_left_32`: n's low 32 bits rotated left by
// k modulo 32.
func IntRotateLeft32(n, k Int) int64 {
	return int64(bits.RotateLeft32(low32Of(n), rotation(k, 32)))
}

// IntRotateLeft64 is `Int.rotate_left_64`: n's low 64 bits rotated left by
// k modulo 64.
func IntRotateLeft64(n, k Int) int64 {
	return int64(bits.RotateLeft64(low64Of(n), rotation(k, 64)))
}

// IntLeadingZeros32 is `Int.leading_zeros_32`: the 0 bits above the
// highest 1 bit of n's low 32 bits, 32 when they are all 0.
func IntLeadingZeros32(n Int) int64 { return int64(bits.LeadingZeros32(low32Of(n))) }

// IntLeadingZeros64 is `Int.leading_zeros_64`.
func IntLeadingZeros64(n Int) int64 { return int64(bits.LeadingZeros64(low64Of(n))) }

// IntTrailingZeros32 is `Int.trailing_zeros_32`: the 0 bits below the
// lowest 1 bit of n's low 32 bits, 32 when they are all 0.
func IntTrailingZeros32(n Int) int64 { return int64(bits.TrailingZeros32(low32Of(n))) }

// IntTrailingZeros64 is `Int.trailing_zeros_64`.
func IntTrailingZeros64(n Int) int64 { return int64(bits.TrailingZeros64(low64Of(n))) }

// IntSwapBytes16 is `Int.swap_bytes_16`: n's low 16 bits with their two
// bytes in the other order.
func IntSwapBytes16(n Int) int64 { return int64(bits.ReverseBytes16(uint16(low64Of(n)))) }

// IntSwapBytes32 is `Int.swap_bytes_32`.
func IntSwapBytes32(n Int) int64 { return int64(bits.ReverseBytes32(low32Of(n))) }

// IntSwapBytes64 is `Int.swap_bytes_64`.
func IntSwapBytes64(n Int) int64 { return int64(bits.ReverseBytes64(low64Of(n))) }

// IntWrapAdd32 is `Int.wrapping_add_32`: a + b modulo 2^32, unsigned.
func IntWrapAdd32(a, b Int) int64 { return int64(low32Of(a) + low32Of(b)) }

// IntWrapSub32 is `Int.wrapping_sub_32`: a - b modulo 2^32, unsigned.
func IntWrapSub32(a, b Int) int64 { return int64(low32Of(a) - low32Of(b)) }

// IntWrapMul32 is `Int.wrapping_mul_32`: a * b modulo 2^32, unsigned.
func IntWrapMul32(a, b Int) int64 { return int64(low32Of(a) * low32Of(b)) }

// IntWrapSigned is `Int.wrap_signed`: n reduced modulo 2^width into the
// two's complement range of width bits, -2^(width-1) to 2^(width-1) - 1.
// An n already in the range is the answer, so the cost is bounded by n's
// size and not by width's value. width is a PositiveInt in Nomi; a smaller
// one, which only rt's own callers could pass, faults.
func IntWrapSigned(n, width Int) Int {
	if width.Sign() <= 0 {
		Trap("Int.wrap_signed: width " + width.String() + " is not positive")
	}
	w := width.Saturated()
	if IntBitLength(n) < w {
		return n
	}
	if w <= 64 {
		shift := 64 - uint(w)
		return SmallInt(int64(low64Of(n)<<shift) >> shift)
	}
	// n has more bits than width, so width fits an int, and the mask is no
	// larger than n.
	modulus, _ := IntShl(int64(1), w)
	low := IntAnd(n.Value(), IntSub(modulus, int64(1)))
	half, _ := IntShl(int64(1), w-1)
	if IntCompare(low, half) >= 0 {
		low = IntSub(low, modulus)
	}
	return IntOf(low)
}

// Float32Bits is x rounded to the nearest 32-bit float, ties to even (an
// x past the 32-bit range rounds to an infinity of its sign, and a NaN
// stays a NaN), as the IEEE 754 bit pattern of that float.
func Float32Bits(x float64) uint32 { return math.Float32bits(float32(x)) }

// FloatToBits32 is `Float.to_bits_32`: Float32Bits, as an Int.
func FloatToBits32(x float64) int64 { return int64(Float32Bits(x)) }

// FloatFromBits is `Float.from_bits`: the Float whose IEEE 754 bits are
// n's low 64 bits.
func FloatFromBits(n Int) float64 { return math.Float64frombits(low64Of(n)) }

// FloatFromBits32 is `Float.from_bits_32`: the 32-bit float whose bits are
// n's low 32 bits, widened exactly.
func FloatFromBits32(n Int) float64 { return float64(math.Float32frombits(low32Of(n))) }
