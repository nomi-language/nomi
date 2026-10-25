package rt

import "sync"

// A boxed existential: a value held as an interface whose own runtime value
// does not name the impl a dispatch must select.
//
// An existential is the concrete value itself, and the engine selects an
// impl by the type the value carries (a record's descriptor, a scalar's own
// type). Two kinds of value carry no such name. A std container (`List`,
// `Vector`, `Map`, `Set`, `Range`, `Maybe`, `Result`) names none of its type
// arguments, so `List<Int>`'s impl and `List<String>`'s cannot be told apart.
// A value widened into an enum's `embeds` variant is the embedded value
// itself, which names the embedded type and not the enum. Where such a value
// becomes an interface value, the producer wraps it in a box whose
// descriptor's Name is the static type it had there; dispatch selects the
// impl by that name and passes the value inside.
//
// Everywhere else a box is its value: Hash and every rendering see through
// it (Unbox), and Equal compares two boxes of one name as their values. Its
// name is the one thing equality keeps: boxes of two static types, or a box
// and a value that is none, are unequal, since they held two types where
// they became interface values. A value whose own runtime type names its
// impl is never boxed, and costs nothing.

var boxDescs sync.Map // name -> *TypeDesc

// BoxDesc is the process-wide descriptor of the box for the static type
// named name: one reference field, the value.
func BoxDesc(name string) *TypeDesc {
	if d, ok := boxDescs.Load(name); ok {
		return d.(*TypeDesc)
	}
	d := &TypeDesc{Name: name, Short: name, Kind: KindBoxed, Layout: newLayout([]FieldSpec{{Type: SlotRef}})}
	actual, _ := boxDescs.LoadOrStore(name, d)
	return actual.(*TypeDesc)
}

// Box wraps v in the box d describes.
func Box(d *TypeDesc, v any) *Record {
	r := d.New()
	r.R[0] = v
	return r
}

// Unbox is the value a box holds, or v itself when v is no box.
func Unbox(v any) any {
	if r, ok := v.(*Record); ok && r != nil && r.Desc.Kind == KindBoxed {
		return r.R[0]
	}
	return v
}

// Boxed reports whether v is a box.
func Boxed(v any) bool {
	r, ok := v.(*Record)
	return ok && r != nil && r.Desc.Kind == KindBoxed
}
