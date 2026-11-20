package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
)

// operatorRoute is how an operator whose record names an impl
// machineImpls lists is lowered instead of as a call of it.
type operatorRoute uint8

const (
	routeUnset operatorRoute = iota
	// routeNative is the machine's own operation: over scalars, ir.Arith,
	// ir.UnaryArith, ir.Concat, an ir.Compare of a scalar shape; over
	// Lists, the concatenation `List.concat` is the host operation of.
	routeNative
	// routeStructural compares by the operands' shape.
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
// an enum, a distinct, or another (a struct).
type derivedForm uint8

const (
	derivedOther derivedForm = iota
	derivedEnum
	derivedDistinct
)

// machineImpls says, for an operator whose record names one of these
// impls, how the operator is lowered instead of as a call of it:
// routeNative is the machine's operation over scalars (ir.Arith,
// ir.UnaryArith, ir.Concat, an ir.Compare of a scalar shape) or over
// Lists (the concatenation `List.concat` is the host operation of), and
// routeStructural a comparison by the operands' shape (an ir.Compare of a
// container or a variant, a distinct through its inner value, Unit's and
// a marker's constant answer), which rt.Equal answers as the impl's body
// does. A row of routeImpl keeps an impl a wider row names called. An
// operator whose record names an impl with no row calls it.
//
// A std impl must have a row when nothing else computes it: a `host fn`
// no host table binds (Int's, Float's, Decimal's and String's
// arithmetic), and a body that is its own operator over its parameters
// (`a == b` in List's Equatable), which a call would reach again
// (TestMachineImplsAreComplete). The others have one because the machine
// computes what their body does (Int's compare, Unit's equal, Bool's
// derived impls, List's `+`, whose body calls `List.concat`;
// TestMachineImplsComputeTheirBodies). Float's compare is
// the one row whose operators differ from its body, as the spec says:
// `<` over Floats is IEEE 754, and compare puts NaN highest (spec §5 *NaN
// and the three comparison behaviours*).
//
// A derived Equatable compares each part with `==`: an enum's by its tag
// and payloads, a distinct's by its inner value, which is the comparison
// by shape. A derived struct's Equatable and a derived Comparable are
// called, and so is the derived Equatable of a std newtype rt declares
// (opaqueSpecs: Duration, Codepoint), which the machine holds as rt's
// value.
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

		{derived: derivedImpl{"Equatable", derivedEnum}}:     routeStructural,
		{derived: derivedImpl{"Equatable", derivedDistinct}}: routeStructural,
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
		form := derivedOther
		switch analysis.ResolveTypeVar(use.Impl.Receiver).(type) {
		case *analysis.EnumType:
			form = derivedEnum
		case *analysis.DistinctType:
			form = derivedDistinct
		}
		route = machineImpls[machineImpl{derived: derivedImpl{use.Impl.Interface, form}}]
	}
	if route == routeImpl {
		return routeUnset
	}
	return route
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
