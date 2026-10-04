package irbuild

import (
	"strings"
)

// The SCALAR OPERATOR IMPLS written out by name: `Subtract.subtract(9, 4)`,
// `Int.add(a, b)`, `Divide.divide(9.0, 2.0)`.
//
// # These are the same operation as `+`, at a different source position
//
// std/int.nomi declares `impl Add<Int, Int> for Int { host fn add(lhs: Int, rhs:
// Int): Int }` and eight siblings across int, float and strings. Those nine
// declarations ARE the definitions of `+ - * /` on the scalars, and native.go's
// arith() lowers the OPERATOR spelling natively. So nothing here is a new
// operation; this is the route from the written-out spelling to the same rt
// call.
//
// # Which line a fault names
//
// The trapping Int forms carry a source line in their fault text
// (`line 6: integer overflow: 9223372036854775807 + 1`), and a written-out
// `Int.add(a, b)` has no operator to blame. It has a CALL to blame: `t.Line`
// reaches every `rt` call that can fault, so a fault names the call's own
// source line. Line 0 is not a position any file has, and nothing here passes
// it.
//
// # Why a builder arm rather than nine registry rows
//
// stdlibBindings holds a Go FUNCTION VALUE whose signature is reflected over, so
// a row for `int.Int.Add<Int, Int>.add` needs a named rt symbol of exactly two
// int64 parameters. `rt.AddInt` takes three, and hostCallName rejects a closure
// outright (`strings.ContainsAny(local, ".[(")` catches the `.func1` a literal
// gets). A row of that shape cannot carry a line: there is no parameter to put
// one in. The Decimal family goes through this arm for the same reason.
//
// scalarEqualCall lowers `Equatable.equal?` on a scalar the same way, with no
// registry row, and textQualifiedCall lowers `Int.to_string`. Three things
// follow:
//
//   - NO NEW rt SYMBOL. The overflow contract keeps ONE implementation. rt owns
//     the predicates (AddOverflows, SubOverflows, MulOverflows, QuoOverflows) and
//     the text (OverflowError, DivByZeroError), and this arm emits a call to the
//     SAME rt.AddInt the operator emits, differing only in the constant it passes
//     for `line`. A second entry point would be a second thing to keep in
//     agreement.
//   - The nine keys stay unbound and exempted, so
//     TestStdlibRefusesUnmappedHostFnsByName keeps holding its line for the
//     stdlib index. implCall's chain answers these calls before the index is
//     consulted, as scalarEqualCall and the List/Map/Set arms do.
//   - The KEYS do not matter on this route. stdKey instantiates whenever the
//     interface is generic, so the builder's index calls the first three
//     `int.Int.Add<Int, Int>.add` and friends, while a key that instantiates
//     only where an interface is implemented twice for Int would spell them
//     `int.Int.add`, `int.Int.subtract`, `int.Int.multiply` BARE (only `Divide`
//     is implemented twice). No row exists to be wrong. If a row is ever added
//     to stdlibBindings for any of the six add/subtract/multiply keys, decide
//     then which spelling it is keyed on.
//
// # WHAT IS DELIBERATELY NOT HERE
//
//   - `%`. std declares no Modulo interface — stdOperatorIfaces says so — so
//     there is no written-out spelling to lower.
//   - `Int.divide(n, nz)` over a `NonZeroInt` divisor. That is a SECOND
//     declaration — `impl Divide<NonZeroInt, Int> for Int`, a Nomi body — and it
//     lowers through the stdlib index. This arm requires both operands to be the
//     SAME scalar kind, so a NonZeroInt right operand declines here and keeps
//     that route.
//   - A named LEFT operand. `Score(2) + Score(3)` dispatches to a user
//     `impl Add<Score, Score> for Score`, which is a different mechanism
//     entirely; see userarith.go.

// scalarArithOps pairs an operator interface with its one method, derived from
// stdOperatorIfaces so the two cannot drift: that map is what native.go's
// stdlibOperator selects with, and a second hand-written copy of "which
// interface is which operator" is the shape of every identity drift this package
// guards against.
//
// Keyed by INTERFACE name, valued by (method, operator). An owner naming an
// interface must spell that interface's own method; a CONCRETE owner (`Int`,
// `Float`, `String`) may spell any of the four, because a type implements
// several.
var scalarArithOps = func() map[string]struct{ method, op string } {
	out := make(map[string]struct{ method, op string }, len(stdOperatorIfaces))
	for op, key := range stdOperatorIfaces {
		iface, method, ok := strings.Cut(key, ".")
		if !ok || iface == "" || method == "" {
			// Unreachable: every stdOperatorIfaces value is `Iface.method`.
			// Left as a skip rather than a panic because a malformed row would
			// otherwise take down every lowering in the package at init.
			continue
		}
		out[iface] = struct{ method, op string }{method, op}
	}
	return out
}()

// scalarArithMethods is the reverse index: method name to operator, for the
// CONCRETE-qualifier spelling where the owner is a type rather than an
// interface.
var scalarArithMethods = func() map[string]string {
	out := make(map[string]string, len(scalarArithOps))
	for _, v := range scalarArithOps {
		out[v.method] = v.op
	}
	return out
}()

// scalarArithOp is the operator a `owner.method` spelling names, or "" .
func scalarArithOp(owner, method string) (string, bool) {
	if v, isIface := scalarArithOps[owner]; isIface {
		if method != v.method {
			return "", false
		}
		return v.op, true
	}
	switch owner {
	case "Int", "Float", "String":
	default:
		return "", false
	}
	op, known := scalarArithMethods[method]
	return op, known
}
