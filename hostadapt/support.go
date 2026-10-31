package hostadapt

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
	"unsafe"

	"github.com/nomi-language/nomi/rt"
)

// The helpers below are what generated adapters call. None reflects: each is
// a type switch or a type assertion on a closed set of dynamic types.

// Recover turns a panic in a host function into the adapter's error, as the
// marshaller's wrapper does (`<name>: panic: <value>`). It must be deferred
// directly: `defer hostadapt.Recover("Regex.compile", &err)`.
func Recover(name string, err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%s: panic: %v", name, r)
	}
}

// Arity is the error for a call with the wrong operand count.
func Arity(name string, want, got int) error {
	return fmt.Errorf("%s: expected %d args, got %d", name, want, got)
}

// Arg wraps an operand conversion failure with the adapter and position.
func Arg(name string, i int, err error) error {
	return fmt.Errorf("%s: arg %d: %w", name, i, err)
}

// ConvertError is a value the other side's type cannot hold: an Int past a
// fixed-width Go integer, a Float past float32, a nil *big.Int, a time.Time
// outside an Instant's range. It is the program's value that is wrong, not
// the adapter, so a call that meets one traps as a Nomi fault (Param,
// Returned) rather than failing the machine.
type ConvertError struct {
	// Takes is what the receiving slot holds, in the author's words: "an Int
	// that fits in 64 bits".
	Takes string
	// Got is the value as the author would write or read it.
	Got string
	// msg is the conversion's own text, which Error answers where no
	// binding names it.
	msg string
}

func (e *ConvertError) Error() string { return e.msg }

// Param is the failure of converting argument i of the adapter key, which
// calls fn, whose parameter there is param. A value the parameter's Go type
// cannot hold is an *rt.Error, which the machine raises at the call as a
// Nomi fault: "`strconv.FormatInt`'s `i` takes an Int that fits in 64 bits;
// got 123456789012345678901234567890". Anything else fails as Arg does.
func Param(key string, i int, fn, param string, err error) error {
	var ce *ConvertError
	if !errors.As(err, &ce) {
		return Arg(key, i, err)
	}
	if at := within(err, ce); at != "" {
		param += "` (" + at + ")"
	} else {
		param += "`"
	}
	return &rt.Error{Msg: fmt.Sprintf("`%s`'s `%s takes %s; got %s", fn, param, ce.Takes, ce.Got)}
}

// Returned is the failure of converting fn's result. A value Nomi's
// declared type cannot hold is an *rt.Error, raised at the call as a Nomi
// fault: "`big.NewInt` returned a nil *big.Int, which is not an Int".
// Anything else is err.
func Returned(fn string, err error) error {
	var ce *ConvertError
	if !errors.As(err, &ce) {
		return err
	}
	got := ce.Got
	if at := within(err, ce); at != "" {
		got += " at " + at
	}
	return &rt.Error{Msg: fmt.Sprintf("`%s` returned %s, which is not %s", fn, got, ce.Takes)}
}

// within is where in a value its unconvertible part sits, `index 2` for an
// element of a list, from the path the conversions wrapped ce in; "" when
// ce is the whole value.
func within(err error, ce *ConvertError) string {
	return strings.TrimSuffix(strings.TrimSuffix(err.Error(), ce.Error()), ": ")
}

// Want is the error for an operand whose dynamic type is not the one the
// declaration names.
func Want(nomiType string, got rt.Value) error {
	return fmt.Errorf("expected %s, got %T", nomiType, got)
}

// Record answers v as a record of the named type. Identity is compared by
// short name, as rt's kernels compare it, because a value built by another
// producer may carry the bare or the module-qualified spelling.
func Record(v rt.Value, name string) (*rt.Record, error) {
	r, ok := v.(*rt.Record)
	if !ok || r == nil {
		return nil, Want(name, v)
	}
	if name != "" && r.Desc.Short != rt.ShortTypeName(name) {
		return nil, fmt.Errorf("expected %s, got a %s record", name, r.Desc.Name)
	}
	return r, nil
}

// Variant answers v as an enum record of the named enum, and its variant name.
func Variant(v rt.Value, name string) (*rt.Record, string, error) {
	r, err := Record(v, name)
	if err != nil {
		return nil, "", err
	}
	if r.Desc.Kind != rt.KindEnum {
		return nil, "", fmt.Errorf("expected an enum %s, got a %s record of kind %d", name, r.Desc.Name, r.Desc.Kind)
	}
	return r, r.Desc.Variants[r.Tag].Name, nil
}

// field reads field i of r by position when r was built with d, and by name
// otherwise. The fast path is the engine's own descriptor; the slow path is a
// record some other producer laid out by name.
func field(r *rt.Record, d *rt.TypeDesc, i int, name string) (rt.Value, error) {
	if r.Desc == d {
		return r.Field(i), nil
	}
	v, ok := r.FieldNamed(name)
	if !ok {
		return nil, fmt.Errorf("%s has no field %q", r.Desc.Name, name)
	}
	return v, nil
}

// IntValueField reads an Int field as the Int value it holds: an int64, or a
// *rt.BigInt past int64's range.
func IntValueField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (rt.Value, error) {
	if r.Desc == d {
		return r.IntSlot(slot), nil
	}
	v, err := field(r, d, i, name)
	if err != nil {
		return nil, err
	}
	if !rt.IsInt(v) {
		return nil, fmt.Errorf("field %q: %w", name, Want("Int", v))
	}
	return v, nil
}

// IntField reads an Int field as an int64, refusing a value past its range.
func IntField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (int64, error) {
	v, err := IntValueField(r, d, i, slot, name)
	if err != nil {
		return 0, err
	}
	return ToSigned[int64](v, "int64")
}

// FloatField reads a Float field.
func FloatField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (float64, error) {
	if r.Desc == d {
		return rt.WordFloat(r.W[slot]), nil
	}
	v, err := field(r, d, i, name)
	if err != nil {
		return 0, err
	}
	x, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("field %q: %w", name, Want("Float", v))
	}
	return x, nil
}

// BoolField reads a Bool field.
func BoolField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (bool, error) {
	if r.Desc == d {
		return rt.WordBool(r.W[slot]), nil
	}
	v, err := field(r, d, i, name)
	if err != nil {
		return false, err
	}
	x, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("field %q: %w", name, Want("Bool", v))
	}
	return x, nil
}

// ByteField reads a Byte field.
func ByteField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (rt.Byte, error) {
	if r.Desc == d {
		return rt.WordByte(r.W[slot]), nil
	}
	v, err := field(r, d, i, name)
	if err != nil {
		return 0, err
	}
	x, ok := v.(rt.Byte)
	if !ok {
		return 0, fmt.Errorf("field %q: %w", name, Want("Byte", v))
	}
	return x, nil
}

// StringField reads a String field.
func StringField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (string, error) {
	if r.Desc == d {
		return r.S[slot], nil
	}
	v, err := field(r, d, i, name)
	if err != nil {
		return "", err
	}
	x, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q: %w", name, Want("String", v))
	}
	return x, nil
}

// BytesField reads a Bytes field.
func BytesField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (rt.Bytes, error) {
	if r.Desc == d {
		return rt.Bytes(r.S[slot]), nil
	}
	v, err := field(r, d, i, name)
	if err != nil {
		return "", err
	}
	x, ok := v.(rt.Bytes)
	if !ok {
		return "", fmt.Errorf("field %q: %w", name, Want("Bytes", v))
	}
	return x, nil
}

// RefField reads a field held in the reference bank.
func RefField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (rt.Value, error) {
	if r.Desc == d {
		return r.R[slot], nil
	}
	return field(r, d, i, name)
}

// Payload reads the single payload of a positional variant record.
func Payload(r *rt.Record) rt.Value { return r.Field(0) }

// VariantField reads a named field of a struct-shaped variant record.
func VariantField(r *rt.Record, name string) (rt.Value, error) {
	v, ok := r.FieldNamed(name)
	if !ok {
		return nil, fmt.Errorf("%s.%s has no field %q", r.Desc.Name, r.Variant().Name, name)
	}
	return v, nil
}

// List answers v as a list. An empty list is a nil *rt.List.
func List(v rt.Value) (*rt.List[any], error) {
	xs, ok := v.(*rt.List[any])
	if !ok {
		return nil, Want("List", v)
	}
	return xs, nil
}

// Len is the length of a list.
func Len(xs *rt.List[any]) int {
	if xs == nil {
		return 0
	}
	return xs.Len
}

// Handle answers v as the Go value behind a host handle of the named type.
func Handle(v rt.Value, name string) (any, error) {
	h, ok := v.(rt.HostHandle)
	if !ok {
		return nil, Want(name, v)
	}
	if h.TypeName != name {
		return nil, fmt.Errorf("expected a %s handle, got a %s handle", name, h.TypeName)
	}
	return h.Value, nil
}

// Signed is every Go signed integer type an Int projects to.
type Signed interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

// Unsigned is every Go unsigned integer type an Int projects to, except the
// 8-bit ones, which are Byte.
type Unsigned interface {
	~uint | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// ToSigned narrows an Int (an int64 or a *rt.BigInt) to a Go signed type,
// refusing a value that does not fit, as the marshaller does. An Int is exact
// at any size and a Go integer is not, so this is where a value too large
// for the Go side stops, with an error the adapter names its binding in.
func ToSigned[T Signed](v rt.Value, goType string) (T, error) {
	var zero T
	takes := fmt.Sprintf("an Int that fits in %d bits", unsafe.Sizeof(zero)*8)
	n, ok := v.(int64)
	if !ok {
		return 0, overflows(goType, takes, v)
	}
	t := T(n)
	if int64(t) != n {
		return 0, overflows(goType, takes, v)
	}
	return t, nil
}

// ToUnsigned narrows an Int to a Go unsigned type.
func ToUnsigned[T Unsigned](v rt.Value, goType string) (T, error) {
	var zero T
	takes := fmt.Sprintf("an Int from 0 to %d", uint64(^zero))
	if !rt.IsInt(v) || rt.IntSign(v) < 0 {
		return 0, overflows(goType, takes, v)
	}
	var u uint64
	switch x := v.(type) {
	case int64:
		u = uint64(x)
	case *rt.BigInt:
		z := x.Big()
		if !z.IsUint64() {
			return 0, overflows(goType, takes, v)
		}
		u = z.Uint64()
	}
	t := T(u)
	if uint64(t) != u {
		return 0, overflows(goType, takes, v)
	}
	return t, nil
}

// overflows is the failure of narrowing v to goType, which holds takes. A v
// that is no Int is the adapter's mistake, not the program's.
func overflows(goType, takes string, v rt.Value) error {
	if rt.IsInt(v) {
		got := rt.FormatIntValue(v)
		return &ConvertError{Takes: takes, Got: got, msg: fmt.Sprintf("unmarshal to %s: value %s overflows", goType, got)}
	}
	return fmt.Errorf("unmarshal to %s: %w", goType, Want("Int", v))
}

// FromBig is a Go *big.Int as an Int. A nil pointer is no number, so a Go
// function that returns one where the binding declares an Int traps at the
// call.
func FromBig(z *big.Int) (rt.Value, error) {
	if z == nil {
		return nil, &ConvertError{Takes: "an Int", Got: "a nil *big.Int", msg: "marshal *big.Int: the pointer is nil"}
	}
	return rt.IntFromBig(z), nil
}

// ToFloat32 narrows a Float, refusing a finite magnitude float32 cannot hold.
func ToFloat32[T ~float32](v float64, goType string) (T, error) {
	if !math.IsInf(v, 0) && !math.IsNaN(v) && (v > math.MaxFloat32 || v < -math.MaxFloat32) {
		return 0, &ConvertError{
			Takes: "a Float within float32's range",
			Got:   rt.FormatFloat(v),
			msg:   fmt.Sprintf("unmarshal to %s: value %v overflows", goType, v),
		}
	}
	return T(v), nil
}

var (
	instantMinTime = time.Unix(0, math.MinInt64)
	instantMaxTime = time.Unix(0, math.MaxInt64)
)

// InstantNanos is an Instant's nanoseconds for a Go time, refusing a time
// outside the range UnixNano represents, as the marshaller does.
func InstantNanos(t time.Time) (int64, error) {
	if t.Before(instantMinTime) || t.After(instantMaxTime) {
		got := t.UTC().Format(time.RFC3339Nano)
		lo, hi := instantMinTime.UTC().Format(time.RFC3339), instantMaxTime.UTC().Format(time.RFC3339)
		return 0, &ConvertError{
			Takes: "an Instant from " + lo + " to " + hi,
			Got:   got,
			msg: fmt.Sprintf(
				"marshal time.Time: %s is outside Instant's representable range (%s .. %s); "+
					"UnixNano would wrap silently and name a different moment",
				got, lo, hi),
		}
	}
	return t.UnixNano(), nil
}

// CallbackFailure is the panic value of a Go callback with no `error` result
// whose Nomi function failed: it has nowhere to put the failure. The text is
// the marshaller's.
func CallbackFailure(err error, goType string) string {
	return fmt.Sprintf(
		"Nomi callback failed: %v — the Go callback type %s has no error return, "+
			"so the failure has nowhere to go; declare it as func(...) (T, error) to receive Nomi failures",
		err, goType)
}

// Unwind is the panic value of a frame-taking Go callback (`func(fr
// *rt.Frame, ...)`) whose Nomi function failed: the failure, a trap
// included, unwinds to the host call the callback runs inside, and the
// engine answers that call with Err as its own failure.
type Unwind struct{ Err error }

// Slot answers a pointer to s, for a DescSpec's Inner.
func Slot(s rt.SlotType) *rt.SlotType { return &s }
