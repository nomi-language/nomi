package irbuild

// An untyped literal in a `case` or `if` arm.
//
// `[]`, a bare `None`, `Map.empty()` and `#{}` are the four values whose type
// the CHECKER solves from context and the builder cannot infer; coerce
// discharges each of them at the one place a wanted type is known
// (types.go's coerce, and untypedLiteral for the set).
//
// The order of the arms matters: an untyped arm that comes FIRST has no earlier
// arm's kind to be coerced to:
//
//	case frag {              // 17-typed-literals/typed_literals/sql.nomi
//	  .Static(_) -> []       // List<_>
//	  .Dynamic(v) -> [v]     // List<Display>
//	}
//
// testdata/branch_untyped_literal.nomi holds BOTH orders and puts the untyped
// arm first, last and in the middle.
