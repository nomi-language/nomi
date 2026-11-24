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
)

// machineImpl names an operator impl by its identity, as the checker's
// record of an operator (analysis.OperatorUse.Impl) carries it: std's
// function (analysis.StdFunc).
type machineImpl struct {
	std analysis.StdFunc
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
// does. For Hashable, it is the machine's hash of the value (vm.hash_value,
// hashByMachine), the hash a Map key of the type takes: std's List,
// Vector, Map and Set declare their `equal` and `hash` as `host fn`s no
// host table binds, so rt's structural equality and hash, keyed by the
// process, are the one equality and hash of a container. Being `host
// fn`s is also what tells the checker that a use of them asks Equatable
// and Hashable of every element (analysis.ImplAsksParts,
// TestMachineImpls_ShapeRowsAskTheirParts). An operator, a rendering or a
// hash whose record names an impl with no row calls it.
//
// A std impl must have a row when nothing else computes it: a `host fn`
// no host table binds (Int's, Float's, Decimal's and String's
// arithmetic), and a body that is its own operator over its parameters
// (`a == b` in Int's Equatable) or its own rendering of its parameter
// (`Debug.inspect(w)` in Type's Debug), which a call would reach again
// (TestMachineImplsAreComplete). The others have one because the machine
// computes what their body does (Int's compare, Unit's equal, Bool's
// derived Comparable, List's `+`, whose body calls `List.concat`, Decimal's
// equal, whose `host fn` is rt.EqDecimal, the function rt.Equal compares
// two Decimals with; TestMachineImplsComputeTheirBodies). Float's compare
// is the one row whose operators differ from its body, as the spec says:
// `<` over Floats is IEEE 754, and compare puts NaN highest (spec §5 *NaN
// and the three comparison behaviours*).
//
// A derived Equatable writes no function (analysis's derivesByShape): the
// checker records its uses structurally, and `==` compares by the
// operands' shape as for a type with no Equatable block, which rt.Equal
// answers, each part with a written impl through the key table
// (irRecordKeyImpls). So no row names one, Bool's, True's and False's,
// Option's and the std newtypes rt declares (opaqueSpecs: Duration,
// Codepoint) among them.
//
// The machine renders a value as std's written Debug of its type does
// for Int, Float, Bool, String, Byte, Bytes, Decimal, List, Vector, Map,
// Set, a channel half, an arm and a task (`<channel>`, `<arm>`, `<task>`,
// rt's placeholders), as the host function std's Debug of Dynamic and of
// Context is (rt.DynamicInspect, rt.ContextInspect), as std's Debug of a
// `Type<T>` witness, whose body is the machine's rendering and prints the
// type the witness names (`Type<TraceId>`, rt.TypeWitnessInspect), which no
// Nomi body can spell, and as Unit's name-only Debug does. Its parts
// render through their own records (nestedDebugImpls). A type with no
// Debug block (Option, Result, a struct of the program's) or a derived
// one, which writes no function, has no row: the record answers it by its
// shape, and the machine renders it so (debugRender). Every other Debug is
// called: a struct's, an enum's or a distinct's written one, an opaque
// type's name-only one (`<opaque Meters>`), and std's written Debug of
// its other types.
//
// The machine renders a value as std's Display of its type does for Int,
// Float, String and Decimal (rt.DisplayShapeText, ir.Render at
// RenderDisplay; Decimal's `host fn` is the same rt function), the
// rendering an interpolation hole of the type lowers to
// (analysis.DisplayScalar, DisplayText). Bool's, True's and False's
// Display is derived and writes no function: the machine renders it by
// its shape, as every derived Display. Every other Display is called. A
// std container's Display is called too at the top: the machine's text is
// its body's where every element is structural, which the hole's record
// decides by the element type (analysis.DisplayStructural) and no row can
// say, and inside a rendering by shape, where each element renders by its
// own record (machineDisplays).
var machineImpls = machineImplTable()

// stdRow names std's function (module, owner, impl, name), as a row of
// machineImpls.
func stdRow(module, owner, impl, name string) machineImpl {
	return machineImpl{std: analysis.StdFunc{Module: module, Owner: owner, Impl: impl, Name: name}}
}

func machineImplTable() map[machineImpl]operatorRoute {
	return map[machineImpl]operatorRoute{
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

		stdRow("std/bool", "Bool", "Comparable", "compare"): routeNative,

		stdRow("std/unit", "Unit", "Equatable", "equal"):       routeStructural,
		stdRow("std/bytes", "Byte", "Equatable", "equal"):      routeStructural,
		stdRow("std/bytes", "Bytes", "Equatable", "equal"):     routeStructural,
		stdRow("std/dynamic", "Dynamic", "Equatable", "equal"): routeStructural,
		stdRow("std/lists", "List", "Equatable", "equal"):      routeStructural,
		stdRow("std/maps", "Map", "Equatable", "equal"):        routeStructural,
		stdRow("std/sets", "Set", "Equatable", "equal"):        routeStructural,
		stdRow("std/vectors", "Vector", "Equatable", "equal"):  routeStructural,

		stdRow("std/lists", "List", "Hashable", "hash"):     routeStructural,
		stdRow("std/maps", "Map", "Hashable", "hash"):       routeStructural,
		stdRow("std/sets", "Set", "Hashable", "hash"):       routeStructural,
		stdRow("std/vectors", "Vector", "Hashable", "hash"): routeStructural,

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

		stdRow("std/int", "Int", "Display", "to_string"):         routeStructural,
		stdRow("std/float", "Float", "Display", "to_string"):     routeStructural,
		stdRow("std/strings", "String", "Display", "to_string"):  routeStructural,
		stdRow("std/decimal", "Decimal", "Display", "to_string"): routeStructural,
	}
}

// machineRouteOf is the route machineImpls gives an operator whose
// record is use: its row for std's function the record names. It is
// routeUnset when the operator calls the impl (no row), and for a record
// of another form or none.
func (g *gen) machineRouteOf(use *analysis.OperatorUse) operatorRoute {
	if use == nil || use.Form != analysis.OperatorByImpl || use.Impl == nil || isNilNode(use.Impl.Method) {
		return routeUnset
	}
	if s := g.operatorImplStd(use.Impl); !s.IsZero() {
		return machineImpls[machineImpl{std: s}]
	}
	return routeUnset
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
