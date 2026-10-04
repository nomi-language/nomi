package hostadapt

// Integer conversions for the helpers an inline `go { }` binding body calls
// (internal/ffirun/helpers.go's toGoInt and toNomiInt).

import (
	"fmt"
	"math"
	"unsafe"
)

// GoInteger is the set of Go integer widths that project from Nomi Int.
//
// byte/uint8 is intentionally excluded: Go byte projects to Nomi Byte, not Int.
type GoInteger interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint16 | ~uint32 | ~uint64
}

// CheckedGoInt converts a Nomi Int carrier value to a Go integer width.
func CheckedGoInt[T GoInteger](n int64) (T, error) {
	var zero T
	bits := int(unsafe.Sizeof(zero) * 8)
	if isSignedGoInteger(zero) {
		min, max := signedIntegerBounds(bits)
		if n < min || n > max {
			return zero, fmt.Errorf("Nomi Int value %d overflows Go %T", n, zero)
		}
		return T(n), nil
	}
	if n < 0 {
		return zero, fmt.Errorf("Nomi Int value %d overflows Go %T", n, zero)
	}
	if bits < 64 {
		max := uint64(1)<<bits - 1
		if uint64(n) > max {
			return zero, fmt.Errorf("Nomi Int value %d overflows Go %T", n, zero)
		}
	}
	return T(n), nil
}

// MustGoInt is CheckedGoInt for inline Go bodies where panic recovery should
// surface conversion failure as a Nomi runtime error.
func MustGoInt[T GoInteger](n int64) T {
	out, err := CheckedGoInt[T](n)
	if err != nil {
		panic(err)
	}
	return out
}

// CheckedNomiInt converts a Go integer width back to Nomi's Int carrier.
func CheckedNomiInt[T GoInteger](n T) (int64, error) {
	var zero T
	if isSignedGoInteger(zero) {
		return int64(n), nil
	}
	u := uint64(n)
	if u > math.MaxInt64 {
		return 0, fmt.Errorf("Go %T value %d overflows Nomi Int", n, u)
	}
	return int64(u), nil
}

// MustNomiInt is CheckedNomiInt for inline Go bodies where panic recovery
// should surface conversion failure as a Nomi runtime error.
func MustNomiInt[T GoInteger](n T) int64 {
	out, err := CheckedNomiInt(n)
	if err != nil {
		panic(err)
	}
	return out
}

func isSignedGoInteger[T GoInteger](zero T) bool {
	return ^zero < zero
}

func signedIntegerBounds(bits int) (int64, int64) {
	switch bits {
	case 8:
		return math.MinInt8, math.MaxInt8
	case 16:
		return math.MinInt16, math.MaxInt16
	case 32:
		return math.MinInt32, math.MaxInt32
	default:
		return math.MinInt64, math.MaxInt64
	}
}
