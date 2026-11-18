package irbuild

// A bare `None` in a `case` or `if` arm.
//
// A bare `None` names no type argument; the builder lowers it at the type
// the checker gave it, and an arm after the first is lowered against the
// kind the earlier arms settled (laterArmSig). An empty collection literal
// needs neither: it has the kind the checker recorded for it from the start
// (emptyLiteral), in whichever arm it stands:
//
//	case frag {              // 17-typed-literals/typed_literals/sql.nomi
//	  .Static(_) -> []       // List<Display>, as the checker typed it
//	  .Dynamic(v) -> [v]     // List<Display>
//	}
//
// testdata/branch_untyped_literal.nomi holds both orders and puts the empty
// arm first, last and in the middle.
