package analysis

import "strings"

// The runtime dispatch key, derived ONCE.
//
// Dispatch tells impls apart by:
//
//	operator interface  ->  receiver plus the right-hand type's base name
//	anything else       ->  receiver
//
// So the output type is absent from an operator impl's identity, and EVERY type
// argument is absent from any other generic interface impl's. That is not an
// oversight to be fixed here: an operator impl's output is a function of its
// (receiver, right-hand type) pair — `checkOperatorBinary` takes the result type
// FROM the impl it resolved, and no syntax in the language selects an impl by its
// return type — so `Out` is derivable rather than distinguishing.
//
// The analyzer's coherence check has to key on exactly the same thing, or it
// accepts programs dispatch cannot represent. `internal/irbuild` imports
// `analysis`, so the functions below are the one derivation of the
// right-hand-type extraction in the tree.

// DispatchImplKey renders the part of an impl's interface header that the
// runtime dispatch table can tell apart, given the interface's bare name and the
// header as written (`ast.TypeExpr.TypeString()`, e.g. `Add<Days, Day>`).
//
// The result is a grouping token, not source text: `Add<Days>` names the slot
// `Add<Days, Day>` and `Add<Days, Days>` both claim. A caller rendering a
// diagnostic should use the headers it recorded, not this.
//
// An operator header with no readable right-hand type FAILS OPEN — it returns
// what it was given rather than collapsing onto the bare interface name. That
// matters: collapsing would put every rung of `std/calendar`'s eleven-rung `Add`
// ladder in one group and reject the stdlib on a missing index entry rather than
// on a real conflict. Same posture as `OriginUnresolved` on the identity side.
func DispatchImplKey(iface, ifaceKey string) string {
	if _, isOperator := operatorInterfaceByName(iface); !isOperator {
		return iface
	}
	if rhs := OperatorInterfaceRhs(ifaceKey); rhs != "" {
		return iface + "<" + rhs + ">"
	}
	if ifaceKey == "" {
		return iface
	}
	return ifaceKey
}

// OperatorInterfaceRhs reads the RIGHT-HAND type's base name out of an operator
// impl's interface header: `Add<Days, Day>` -> `Days`. It returns "" when
// ifaceKey does not name one of the four operator interfaces, carries no type
// arguments, or has no complete first argument.
//
// The walk tracks angle-bracket depth rather than splitting on the first comma,
// because the right-hand type may itself be generic (`Add<Map<String, Int>,
// Bag>` -> `Map`). The base name is what registration has to hand — the header is
// source text with no module attached, and the lookup side spells the same thing
// through `rt.ShortTypeName` — so `Map<String, Int>` and `Map<Int, Int>` share
// one slot, and this function says so by stripping the arguments.
func OperatorInterfaceRhs(ifaceKey string) string {
	open := strings.Index(ifaceKey, "<")
	if open < 0 {
		return ""
	}
	if _, isOperator := operatorInterfaceByName(ifaceKey[:open]); !isOperator {
		return ""
	}
	depth := 0
	start := open + 1
	for i := start; i < len(ifaceKey); i++ {
		switch ifaceKey[i] {
		case '<':
			depth++
		case '>':
			if depth == 0 {
				return typeArgBaseName(ifaceKey[start:i])
			}
			depth--
		case ',':
			if depth == 0 {
				return typeArgBaseName(ifaceKey[start:i])
			}
		}
	}
	return ""
}

// typeArgBaseName trims a type argument to its base name: `Map<String, Int>` ->
// `Map`, ` Days ` -> `Days`.
func typeArgBaseName(s string) string {
	if idx := strings.Index(s, "<"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}
