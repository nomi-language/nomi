package irbuild

// Field access on a value whose STATIC type does not name the storage.
//
// Nomi has no `.method()` postfix dispatch — `x.f` is field access and nothing
// else, and `x.f(...)` means "read the field, then call its value". The case
// here is reading a field whose LOCATION the static type leaves open: an ENUM.
// The type names several variants and only the run-time tag says which is
// here, so the read is a tag switch. The checker types the access from the
// FIRST variant that declares the name and the read answers from the tag,
// which is why the two providers of one name cannot be collapsed into one slot
// read.
//
// An interface value and a type parameter have no fields at all: an interface
// declares functions only, and the checker rejects `x.f` on either.
//
// # The rule
//
// A field read on an enum value reads the name out of the variant's payload
// only when that payload is a struct. That single rule covers three storage
// shapes the builder keeps apart:
//
//	Shape.Rect{radius: 1.5, height: 3}   struct-shaped   one slot per field
//	Shape.Square{radius: 9.0}            embeds a struct ONE slot, the struct
//	Shape.Wrap(Circle{radius: 4.0})      positional      ONE slot, the struct
//
// and it answers for all three. The last is the one a reader would not predict:
// a POSITIONAL payload that happens to be a struct is indistinguishable from an
// `embeds` at run time, so the read goes through it, and the checker agrees (`fieldTypeFromObject`'s `v.DataType.(*StructType)` arm).
//
// When the tag is not a provider the read FAULTS, and which fault is a
// per-variant fact:
//
//	variant 'Rect' has no field 'radius'          payload is a struct, name absent
//	cannot access field 'height' on variant 'Num' payload is not a struct
//
// So one trap for the whole switch is the wrong answer, and testdata/
// enum_field_read_trap.nomi is the fixture that says so.
//
// A field read answers both for a `c: Shape` coerced from a bare Circle and
// for the same value built as `Shape.Circle{...}`:
// `07-structs-and-enums/enums_test.nomi` has
// `c = Drawable.Circle2{radius: 5.0}` / `assert c.radius == 5.0`, and
// `s: Shape = Circle{radius: 7.0}` answers 7.0 through the tagged variant this
// builder eagerly coerces it into.

// fieldOfKind is the declared field of a receiver kind, or nil.
func fieldOfKind(recv kind, name string) *fieldDef {
	if recv.tag != tagNamed || recv.def == nil {
		return nil
	}
	return recv.def.field(name)
}
