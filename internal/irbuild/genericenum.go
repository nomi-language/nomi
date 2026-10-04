package irbuild

// A USER-DECLARED generic ENUM, MONOMORPHIZED per instantiation.
//
// generictype.go owns the mechanism, the intern table and the representation
// argument; this file owns the one thing an enum needs that a struct does not,
// which is WHERE THE TYPE ARGUMENTS COME FROM AT A CONSTRUCTION SITE.
//
// # Where an enum's type arguments come from
//
// A struct is instantiated by a LITERAL whose field annotations name every type
// parameter, so the arguments are recoverable. An enum is instantiated by a
// VARIANT CONSTRUCTION, and one declaration has two kinds of construction:
//
//	Wrapper.Wrapped(42)   the payload's declared annotation IS `T`, so `T := Int`
//	                      falls out of the same unifier a struct literal uses
//	Wrapper.Empty         names nothing
//
// So the payload-carrying construction is served, and the payload-free one is
// refused BY NAME at the construction rather than by walling the declaration
// every OTHER construction of it needs. Walling a declaration for a refusal only
// some of its use sites have is what `ifaceDecl`'s header rejects for an
// interface, and what `templateWall` avoids for an impl (genericimpl.go).
//
// # WHY NOT GUESS THE ARGUMENT FOR THE BARE VARIANT
//
// `Wrapper.Empty` at `Wrapper<Int>` and at `Wrapper<String>` are two Go types
// with two different slot layouts, and nothing at the construction says which.
// Defaulting to a scalar would put a wrong Go type at a slot offset — which is
// `recoverTypeArgs`' stated rule, and it is sharper here than there: a struct
// literal's wrong guess is a wrong FIELD type that `structValue` then rejects
// by kind, while an enum's is a wrong SLOT that no later check reads.
//
// The refusal is narrower than it looks: a bare variant reached as a `case`
// PATTERN resolves through the SUBJECT's def, which is already the instance, so
// `case w { .Empty -> 0 }` needs nothing from here. Only a bare variant
// CONSTRUCTED in a position with no other source of the arguments lands on it.

// templateArgs reads the solved bindings back out in the template's DECLARED
// parameter order, reporting false when any parameter is unsolved or
// unrepresentable.
//
// One reader for the three recovery paths — a struct literal, a positional
// variant call, a struct-shaped variant literal. They differ only in where the
// annotations come from; the rule about what counts as solved is one rule, and
// three copies of it are three answers waiting to disagree about whether a
// `kindInvalid` argument may be admitted.
func templateArgs(tpl *genericTemplate, solved map[string]kind) ([]kind, bool) {
	args := make([]kind, 0, len(tpl.params))
	for _, p := range tpl.params {
		k, found := solved[p]
		// kindInvalid: propagates — an unsolved or unrepresentable argument refuses at the construction.
		if !found || k == kindInvalid {
			return nil, false
		}
		args = append(args, k)
	}
	return args, true
}
