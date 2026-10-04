package rt

// std/dynamic's two value types, as Go types.
//
// # The layout is a CONTRACT with the IR builder
//
// internal/irbuild/stdenum.go and stdstruct.go name every variant and every field
// here, in declaration order, with the Nomi name it answers to -- and refuse to
// anchor anything whose std declaration disagrees. A rename here is a Go compile
// error there (each spec holds reflect.TypeFor of its type), and a REORDER or a
// retype produces no anchor, so every mention refuses loudly instead of lowering
// against a layout nobody wrote.
//
// Neither type has a hand-written method here, and that is the point of the
// pair: `derive Display for PathSegment` and `impl Display for DecodeError` are
// ordinary Nomi in std/dynamic.nomi and lower through the ordinary impl path, so
// there is ONE renderer rather than a Go copy that could disagree with it.

// TagPathSegmentField and TagPathSegmentIndex are PathSegment's variants, in
// std/dynamic.nomi's declaration order.
//
// NAMED rather than spelled 1 and 2 at each construction site, because
// dynamicops.go now builds these values and a bare literal there would be a
// second, uncommented statement of an order this file owns. Deliberately NOT
// `TagField`/`TagIndex`: rt is one package and `TagDynamic` already means
// Fragment's interpolated-slot variant (rt/fragment.go), so a short name here
// would read as that family's.
//
// A separate `const` block rather than members of an existing one, and no
// `iota`: an inserted `iota` member silently renumbers its neighbours, and these
// two numbers are a layout contract the builder's stdEnumSpec checks.
const (
	TagPathSegmentField uint8 = 1
	TagPathSegmentIndex uint8 = 2
)

// DynamicPathSegment is Nomi's `std/dynamic.PathSegment`: one step in the path
// to a decode failure.
//
// Two variants with payloads of DIFFERENT Go types, so unlike CalendarError this
// carries two payload fields rather than one shared slot. types.go's assignSlots
// allocates one slot per distinct underlying Go type and reuses it across
// variants, because only one variant is ever live; here the two types differ, so
// neither can share.
type DynamicPathSegment struct {
	// Tag is TagPathSegmentField or TagPathSegmentIndex. 0 means never
	// constructed, which is what makes a Go zero value detectable rather than
	// silently reading as the first variant.
	Tag uint8
	// Field is the payload of `Field String`.
	Field string
	// Index is the payload of `Index Int`.
	Index int64
}

// DynamicDecodeError is Nomi's `std/dynamic.DecodeError`: a structured decode
// failure, with the path that located it.
//
// `Path` is a List over the enum above, which is why the struct spec for this
// type is the first one whose field reaches the ENUM table rather than another
// struct row. That direction is one-way and acyclic: the enum specs name no
// struct, so nothing here can re-enter.
type DynamicDecodeError struct {
	Path     *List[DynamicPathSegment]
	Expected string
	Got      string
}
