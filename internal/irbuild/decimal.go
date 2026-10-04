package irbuild

// Decimal.
//
// The type has an rt representation (stdhost.go's third spec row) and the
// literal's meaning has exactly one implementation (rt.ParseDecimalLexeme), so
// the builder only hands the source token over.

// isDecimalKind reports whether k is the anchored `Decimal`.
//
// By POINTER against the process-wide def, which is the identity every other
// std anchor family uses and the reason a per-gen def would be wrong here: a
// stdFunc's parameter kinds are built once and compared by pointer against a
// call site's kinds in a different gen. Never by NAME — a user module may
// declare its own `Decimal`, and `stdHostAnchors` already refuses to anchor the
// spelling in that module, so a name test would claim a type this builder has
// no representation for.
func isDecimalKind(k kind) bool {
	d := decimalKind()
	// An unanchored Decimal must not match every kindInvalid operand, which would
	// claim refused expressions as Decimals.
	// kindInvalid: sentinel — decimalKind's not-found answer, not an operand's kind.
	return d != kindInvalid && k == d
}
