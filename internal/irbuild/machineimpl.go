package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
)

// operatorRoute is how an operator, or a rendering, whose record names an
// impl machineImpls lists is lowered instead of as a call of it.
type operatorRoute uint8

const (
	routeUnset operatorRoute = iota
	// routeNative is the machine's own operation: over scalars, ir.Arith,
	// ir.UnaryArith, ir.Concat, an ir.Compare of a scalar's or a Decimal's
	// shape; over Lists, the concatenation `List.concat` is the host
	// operation of.
	routeNative
	// routeStructural answers by the operands' shape: a comparison of
	// it, or, for Debug, the machine's rendering of the value
	// (rt.DebugText, ir.Render at RenderDebug).
	routeStructural
	// routeImpl calls the impl's function.
	routeImpl
)

// machineImpl names an operator impl by its identity, as the checker's
// record of an operator (analysis.OperatorUse.Impl) carries it: std's
// function (analysis.StdFunc), or a derived impl by its interface and the
// form of the type it is derived for. A derived impl of a std type has a
// std identity too, which is looked up first (machineRouteOf).
type machineImpl struct {
	std     analysis.StdFunc
	derived derivedImpl
}

// derivedImpl is a derived impl of the interface iface for a declared
// type of one form.
type derivedImpl struct {
	iface string
	form  derivedForm
}

// derivedForm is the form of the declared type a derived impl is for:
// a struct, an enum, a marker (a distinct with no inner value, neither
// opaque nor generic), a distinct over one Int, Float, Bool or String
// (neither opaque nor generic), or another distinct.
type derivedForm uint8

const (
	derivedStruct derivedForm = iota
	derivedEnum
	derivedMarker
	derivedWrapping
	derivedDistinct
)

// derivedFormOf is the form of t, the type a derived impl is for.
func derivedFormOf(t analysis.Type) derivedForm {
	switch d := analysis.ResolveTypeVar(t).(type) {
	case *analysis.EnumType:
		return derivedEnum
	case *analysis.DistinctType:
		plain := !d.Opaque && len(d.TypeParams) == 0
		switch {
		case d.Inner == nil && plain:
			return derivedMarker
		case plain && scalarType(d.Inner):
			return derivedWrapping
		}
		return derivedDistinct
	}
	return derivedStruct
}

// scalarType reports whether t is Int, Float, Bool or String.
func scalarType(t analysis.Type) bool {
	switch analysis.ResolveTypeVar(t) {
	case analysis.TypeInt, analysis.TypeFloat, analysis.TypeBool, analysis.TypeString:
		return true
	}
	return false
}

// machineImpls says, for an operator or a rendering whose record names
// one of these impls, how it is lowered instead of as a call of it:
// routeNative is the machine's operation over scalars (ir.Arith,
// ir.UnaryArith, ir.Concat, an ir.Compare of a scalar's or a Decimal's
// shape) or over Lists (the concatenation `List.concat` is the host
// operation of), and
// routeStructural a comparison by the operands' shape (an ir.Compare of a
// container or a variant, a distinct through its inner value, Unit's and
// a marker's constant answer), which rt.Equal answers as the impl's body
// does. For Debug, routeStructural is the machine's rendering of the
// value by its shape (rt.DebugText), which answers as the impl's body
// does. A row of routeImpl keeps an impl a wider row names called. An
// operator or a rendering whose record names an impl with no row calls
// it.
//
// A std impl must have a row when nothing else computes it: a `host fn`
// no host table binds (Int's, Float's, Decimal's and String's
// arithmetic), and a body that is its own operator over its parameters
// (`a == b` in List's Equatable) or its own rendering of its parameter
// (`Debug.inspect(w)` in Type's Debug), which a call would reach again
// (TestMachineImplsAreComplete). The others have one because the machine
// computes what their body does (Int's compare, Unit's equal, Bool's
// derived impls, List's `+`, whose body calls `List.concat`, Decimal's
// equal, whose `host fn` is rt.EqDecimal, the function rt.Equal compares
// two Decimals with; TestMachineImplsComputeTheirBodies). Float's compare
// is the one row whose operators differ from its body, as the spec says:
// `<` over Floats is IEEE 754, and compare puts NaN highest (spec §5 *NaN
// and the three comparison behaviours*).
//
// A derived Equatable compares each part with `==`: a struct's by its
// fields, an enum's by its tag and payloads, a distinct's by its inner
// value, a marker's as its one value, which is the comparison by shape,
// and rt.Equal reaches a part's written impl through the key table
// (irRecordKeyImpls). These rows are the one place a derived Equatable is
// decided to be its type's shape: `==` at the top level and a part the
// builder compares itself (partEqualBody) both read them. A derived
// Comparable is called, and so is the derived Equatable of a std newtype
// rt declares (opaqueSpecs: Duration, Codepoint), which the machine holds
// as rt's value.
//
// The machine renders a value as std's written Debug of its type does
// for Int, Float, Bool, String, Byte, Bytes, Decimal, List, Vector, Map,
// Set, a channel half, an arm and a task (`<channel>`, `<arm>`, `<task>`,
// rt's placeholders), as the host function std's Debug of Dynamic and of
// Context is (rt.DynamicInspect, rt.ContextInspect), as std's Debug of a
// `Type<T>` witness, whose body is the machine's rendering and prints the
// type the witness names (`Type<TraceId>`, rt.TypeWitnessInspect), which no
// Nomi body can spell, as Unit's name-only Debug does, and as a marker's
// derived Debug does (its name), and a distinct's over one scalar does
// (`Meters(1)`). Its parts render through their own records
// (nestedDebugImpls). A type with no Debug block (Maybe, Result, a struct
// of the program's) has no row: the record answers it by its shape, and
// the machine renders it so (debugRender). Every other Debug is called: a
// struct's or an enum's derived or written one, an opaque type's
// (`<opaque Meters>`), a distinct's over anything else (`Coord(3, 4)`,
// which rt would spell `Coord((3, 4))`), and std's written Debug of its
// other types.
//
// The machine renders a value as std's Display of its type does for Int,
// Float, Bool, String and Decimal (rt.DisplayText, ir.Render at
// RenderDisplay; Decimal's `host fn` is the same rt function), and as the
// derived Display of True and False does over the Bool the machine holds
// (`True`, `False`), the rendering an interpolation hole of the type
// lowers to (analysis.DisplayScalar, DisplayText). The marker of True or
// False that Bool's derived body binds is no Bool, and its body is called
// (displayCall). Every other Display is called. A std container's Display
// is called too: the machine's text is its body's only where every
// element is structural, which the hole's record decides by the element
// type (analysis.DisplayStructural) and no row can say.
var machineImpls = machineImplTable()

// stdRow names std's function (module, owner, impl, name), as a row of
// machineImpls.
func stdRow(module, owner, impl, name string) machineImpl {
	return machineImpl{std: analysis.StdFunc{Module: module, Owner: owner, Impl: impl, Name: name}}
}

func machineImplTable() map[machineImpl]operatorRoute {
	rows := map[machineImpl]operatorRoute{
		stdRow("std/int", "Int", "Add<Int, Int>", "add"):                           routeNative,
		stdRow("std/int", "Int", "Subtract<Int, Int>", "subtract"):                 routeNative,
		stdRow("std/int", "Int", "Multiply<Int, Int>", "multiply"):                 routeNative,
		stdRow("std/int", "Int", "Divide<NonZeroInt, Int>", "divide"):              routeNative,
		stdRow("std/int", "Int", "Remainder<NonZeroInt, Int>", "remainder"):        routeNative,
		stdRow("std/int", "Int", "BitwiseAnd<Int, Int>", "bitwise_and"):            routeNative,
		stdRow("std/int", "Int", "BitwiseOr<Int, Int>", "bitwise_or"):              routeNative,
		stdRow("std/int", "Int", "BitwiseXor<Int, Int>", "bitwise_xor"):            routeNative,
		stdRow("std/int", "Int", "BitwiseNot<Int>", "bitwise_not"):                 routeNative,
		stdRow("std/int", "Int", "ShiftLeft<NonNegativeInt, Int>", "shift_left"):   routeNative,
		stdRow("std/int", "Int", "ShiftRight<NonNegativeInt, Int>", "shift_right"): routeNative,
		stdRow("std/int", "Int", "Equatable", "equal"):                             routeNative,
		stdRow("std/int", "Int", "Comparable", "compare"):                          routeNative,

		stdRow("std/float", "Float", "Add<Float, Float>", "add"):             routeNative,
		stdRow("std/float", "Float", "Subtract<Float, Float>", "subtract"):   routeNative,
		stdRow("std/float", "Float", "Multiply<Float, Float>", "multiply"):   routeNative,
		stdRow("std/float", "Float", "Divide<Float, Float>", "divide"):       routeNative,
		stdRow("std/float", "Float", "Remainder<Float, Float>", "remainder"): routeNative,
		stdRow("std/float", "Float", "Equatable", "equal"):                   routeNative,
		stdRow("std/float", "Float", "Comparable", "compare"):                routeNative,

		stdRow("std/decimal", "Decimal", "Add<Decimal, Decimal>", "add"):                    routeNative,
		stdRow("std/decimal", "Decimal", "Subtract<Decimal, Decimal>", "subtract"):          routeNative,
		stdRow("std/decimal", "Decimal", "Multiply<Decimal, Decimal>", "multiply"):          routeNative,
		stdRow("std/decimal", "Decimal", "Divide<NonZeroDecimal, Decimal>", "divide"):       routeNative,
		stdRow("std/decimal", "Decimal", "Remainder<NonZeroDecimal, Decimal>", "remainder"): routeNative,
		stdRow("std/decimal", "Decimal", "Equatable", "equal"):                              routeNative,

		stdRow("std/strings", "String", "Add<String, String>", "add"): routeNative,
		stdRow("std/strings", "String", "Equatable", "equal"):         routeNative,

		stdRow("std/lists", "List", "Add<List<T>, List<T>>", "add"): routeNative,

		stdRow("std/bool", "Bool", "Equatable", "equal"):    routeNative,
		stdRow("std/bool", "Bool", "Comparable", "compare"): routeNative,
		stdRow("std/bool", "True", "Equatable", "equal"):    routeNative,
		stdRow("std/bool", "False", "Equatable", "equal"):   routeNative,

		stdRow("std/unit", "Unit", "Equatable", "equal"):       routeStructural,
		stdRow("std/bytes", "Byte", "Equatable", "equal"):      routeStructural,
		stdRow("std/bytes", "Bytes", "Equatable", "equal"):     routeStructural,
		stdRow("std/dynamic", "Dynamic", "Equatable", "equal"): routeStructural,
		stdRow("std/lists", "List", "Equatable", "equal"):      routeStructural,
		stdRow("std/maps", "Map", "Equatable", "equal"):        routeStructural,
		stdRow("std/sets", "Set", "Equatable", "equal"):        routeStructural,
		stdRow("std/vectors", "Vector", "Equatable", "equal"):  routeStructural,

		{derived: derivedImpl{"Equatable", derivedStruct}}:   routeStructural,
		{derived: derivedImpl{"Equatable", derivedEnum}}:     routeStructural,
		{derived: derivedImpl{"Equatable", derivedMarker}}:   routeStructural,
		{derived: derivedImpl{"Equatable", derivedWrapping}}: routeStructural,
		{derived: derivedImpl{"Equatable", derivedDistinct}}: routeStructural,

		stdRow("std/int", "Int", "Debug", "inspect"):           routeStructural,
		stdRow("std/float", "Float", "Debug", "inspect"):       routeStructural,
		stdRow("std/bool", "Bool", "Debug", "inspect"):         routeStructural,
		stdRow("std/strings", "String", "Debug", "inspect"):    routeStructural,
		stdRow("std/bytes", "Byte", "Debug", "inspect"):        routeStructural,
		stdRow("std/bytes", "Bytes", "Debug", "inspect"):       routeStructural,
		stdRow("std/decimal", "Decimal", "Debug", "inspect"):   routeStructural,
		stdRow("std/lists", "List", "Debug", "inspect"):        routeStructural,
		stdRow("std/vectors", "Vector", "Debug", "inspect"):    routeStructural,
		stdRow("std/maps", "Map", "Debug", "inspect"):          routeStructural,
		stdRow("std/sets", "Set", "Debug", "inspect"):          routeStructural,
		stdRow("std/dynamic", "Dynamic", "Debug", "inspect"):   routeStructural,
		stdRow("std/context", "Context", "Debug", "inspect"):   routeStructural,
		stdRow("std/unit", "Unit", "Debug", "inspect"):         routeStructural,
		stdRow("std/channels", "Sender", "Debug", "inspect"):   routeStructural,
		stdRow("std/channels", "Receiver", "Debug", "inspect"): routeStructural,
		stdRow("std/channels", "Arm", "Debug", "inspect"):      routeStructural,
		stdRow("std/tasks", "Task", "Debug", "inspect"):        routeStructural,
		stdRow("std/type", "Type", "Debug", "inspect"):         routeStructural,
		{derived: derivedImpl{"Debug", derivedMarker}}:         routeStructural,
		{derived: derivedImpl{"Debug", derivedWrapping}}:       routeStructural,

		stdRow("std/int", "Int", "Display", "to_string"):         routeStructural,
		stdRow("std/float", "Float", "Display", "to_string"):     routeStructural,
		stdRow("std/bool", "Bool", "Display", "to_string"):       routeStructural,
		stdRow("std/bool", "True", "Display", "to_string"):       routeStructural,
		stdRow("std/bool", "False", "Display", "to_string"):      routeStructural,
		stdRow("std/strings", "String", "Display", "to_string"):  routeStructural,
		stdRow("std/decimal", "Decimal", "Display", "to_string"): routeStructural,
	}
	for _, s := range opaqueSpecs {
		rows[stdRow(s.origin, s.nomi, "Equatable", "equal")] = routeImpl
	}
	return rows
}

// machineRouteOf is the route machineImpls gives an operator whose
// record is use: its row for std's function the record names, else, for
// a derived impl, the row for its interface and its type's form. It is
// routeUnset when the operator calls the impl (a row of routeImpl, or
// none), and for a record of another form or none.
func (g *gen) machineRouteOf(use *analysis.OperatorUse) operatorRoute {
	if use == nil || use.Form != analysis.OperatorByImpl || use.Impl == nil || isNilNode(use.Impl.Method) {
		return routeUnset
	}
	route, listed := routeUnset, false
	if s := g.operatorImplStd(use.Impl); !s.IsZero() {
		route, listed = machineImpls[machineImpl{std: s}]
	}
	if !listed && use.Impl.Derived() {
		route = machineImpls[machineImpl{derived: derivedImpl{use.Impl.Interface, derivedFormOf(use.Impl.Receiver)}}]
	}
	if route == routeImpl {
		return routeUnset
	}
	return route
}

// debugByMachine reports whether c, the record of a use of Debug's
// inspect on a value of checker type recv, names an impl whose row in
// machineImpls is the machine's rendering of the value: the value names
// no body of its own, and rt renders it, through the bodies the records
// of its parts name (nestedDebugImpls).
func (g *gen) debugByMachine(c analysis.Callee, recv analysis.Type) bool {
	if c.Form != analysis.OperatorByImpl {
		return false
	}
	return g.machineRouteOf(operatorUseOfCall(stdDebug, c, recv)) == routeStructural
}

// operatorImplStd is std's identity of the impl function an operator's
// record names, or the zero StdFunc for one that is not std's: looked up
// in the file whose check recorded the operator, and for a record
// answered at an instance in the file whose program the instance's types
// are (instanceFA). A std body lowered for a program's std module (a
// std instance's gen) is checked in the shared std, and its instance's
// record names the impl in the program's parse of std.
func (g *gen) operatorImplStd(impl *analysis.OperatorImpl) analysis.StdFunc {
	src := g.srcFA()
	if s := src.StdFuncOf(impl.Method); !s.IsZero() {
		return s
	}
	if fa := g.instanceFA(); fa != src {
		return fa.StdFuncOf(impl.Method)
	}
	return analysis.StdFunc{}
}
