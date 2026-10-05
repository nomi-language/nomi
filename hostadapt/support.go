package hostadapt

import (
	"fmt"
	"math"
	"time"

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

// IntField reads an Int field.
func IntField(r *rt.Record, d *rt.TypeDesc, i, slot int, name string) (int64, error) {
	if r.Desc == d {
		return rt.WordInt(r.W[slot]), nil
	}
	v, err := field(r, d, i, name)
	if err != nil {
		return 0, err
	}
	x, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("field %q: %w", name, Want("Int", v))
	}
	return x, nil
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

// ToSigned narrows an Int to a Go signed type, refusing a value that does not
// fit, as the marshaller does.
func ToSigned[T Signed](v int64, goType string) (T, error) {
	t := T(v)
	if int64(t) != v {
		return 0, fmt.Errorf("unmarshal to %s: value %d overflows", goType, v)
	}
	return t, nil
}

// ToUnsigned narrows an Int to a Go unsigned type.
func ToUnsigned[T Unsigned](v int64, goType string) (T, error) {
	t := T(v)
	if v < 0 || uint64(t) != uint64(v) {
		return 0, fmt.Errorf("unmarshal to %s: value %d overflows", goType, v)
	}
	return t, nil
}

// FromUnsigned widens a Go unsigned value to an Int, refusing one past
// math.MaxInt64.
func FromUnsigned[T Unsigned](v T, goType string) (int64, error) {
	if uint64(v) > math.MaxInt64 {
		return 0, fmt.Errorf("marshal %s: value %d overflows int64", goType, uint64(v))
	}
	return int64(v), nil
}

// ToFloat32 narrows a Float, refusing a finite magnitude float32 cannot hold.
func ToFloat32[T ~float32](v float64, goType string) (T, error) {
	if !math.IsInf(v, 0) && !math.IsNaN(v) && (v > math.MaxFloat32 || v < -math.MaxFloat32) {
		return 0, fmt.Errorf("unmarshal to %s: value %v overflows", goType, v)
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
		return 0, fmt.Errorf(
			"marshal time.Time: %s is outside Instant's representable range (%s .. %s); "+
				"UnixNano would wrap silently and name a different moment",
			t.UTC().Format(time.RFC3339Nano),
			instantMinTime.UTC().Format(time.RFC3339),
			instantMaxTime.UTC().Format(time.RFC3339))
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

// Slot answers a pointer to s, for a DescSpec's Inner.
func Slot(s rt.SlotType) *rt.SlotType { return &s }
