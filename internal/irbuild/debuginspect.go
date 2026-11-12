package irbuild

// `Debug.inspect(x)` where x is a CONTAINER: a list, a map, a tuple.
//
// # What this file is NOT, and the mistake it exists to avoid
//
// It is not inspect.go. inspect.go answers "how does this value read in an
// assertion's `values:` row", which is `rt.RowText`, a structural rendering
// with NO Nomi dispatch in it. `Debug.inspect` is a real interface method that
// dispatches, honours a hand-written `impl Debug`, and DISAGREES with the row
// for shapes the corpus contains: a DECIMAL's row is `1.50` and Debug is
// `1.50d`. See
// inspect.go's isDecimalKind arm, which reaches for `rt.DecimalToString` on
// purpose. (A record's row and its Debug both order fields by name.)
//
// So a container's Debug rendering may NOT be built out of inspect.go's
// `inspector`, however similar the two look — and the similarity is exactly what
// makes the mistake attractive: borrowing the row's renderer would assume an
// agreement on a path where disagreement is a silent wrong string.
//
// # What it is instead: composition out of the call this builder already spells
//
// Every leaf here is the FUNCTION `Debug.inspect(elem)` ALREADY LOWERS TO, named
// rather than re-derived:
//
//	scalar        the stdlib index's own `Debug.inspect` at that receiver kind —
//	              `nomistd23.NomiStd_Int_Debug_inspect`, which is what
//	              stdlibCall's byIface row spells for `Debug.inspect(42)`
//	named type    the impl function this module registered —
//	              `NomiI_Debug_OrderedPoint_inspect`, which is what
//	              foreignIfaceCall spells for `Debug.inspect(p)`
//	record        debugRecordInspector, inspectcall.go's own straight-line form
//
// That is correctness BY CONSTRUCTION rather than by an agreement claim: if
// `Debug.inspect(elem)` is right at the top level then the same function called
// per element is right inside a container, whatever either one renders. The two
// cannot drift apart, because there is one function.
//
// The STRUCTURE is shared with Display and with the row, and that part is safe
// to share because it is not a rendering decision: a List is `[a, b, c]` and a
// Map is `{k => v}` under all three, which is why `rt.FormatList` and
// `rt.FormatMap` take the element renderer as a parameter. std's
// `impl Debug for List<T>` (std/lists.nomi) joins with ", " inside brackets and
// so does `impl Display for List<T>`; only the element differs.
//
// # Why a SET and a RANGE are NOT here
//
// Those two arms live in inspectcall.go and they DO use inspect.go's
// `inspector`, because there the agreement holds: std's `impl Debug for Set<T>`
// and `impl Display for Set<T>` differ only in the element renderer, and a
// Range's endpoints go through `Debug.inspect` in BOTH of std's impls.
//
// # What is REFUSED, and it is one key with the shape in the detail
//
// A receiver whose concrete type is not known at the call site — an EXISTENTIAL
// or a bare type PARAMETER — is refused as `Debug on an unrepresented receiver`.
// One key rather than one per shape, as with `recursive tail call`
// (differential_test.go): `Unsupported.Construct` is the report's key and has
// to stay stable, so the SHAPE goes in the detail.
//
// Answering those would cost this: `Debug` is UNIVERSAL — the front end
// synthesizes an `impl Debug` for every declared type
// (`SynthesizeUniversalDebug`) — so a `rt.Method[F]` table keyed on
// `*rt.TypeID` would be bound by an `init` for every type in the artifact. That
// is eager registration proportional to the program rather than to its Debug
// use.

// debugNamedInspects reports whether this module registered an `impl Debug`
// for the named receiver kind k whose `inspect` lowers at k.
//
// `implsByIface["Debug"][k]` is the same table `Debug.inspect(x)` consults, so
// a receiver this answers for is exactly a receiver that call already lowers
// for.
func (g *gen) debugNamedInspects(k kind) bool {
	if k.tag != tagNamed || k.def == nil {
		return false
	}
	d := g.implsByIface["Debug"][k]
	if d == nil || !d.lowerable {
		return false
	}
	it := d.items["inspect"]
	return it != nil && it.lowerable && len(it.params) == 1 && it.params[0] == k && it.result == kindString
}

// debugScalarInspects reports whether std's own `Debug.inspect` resolves at
// receiver kind k, through the index row an interface qualifier with a known
// receiver kind reads.
func (g *gen) debugScalarInspects(k kind) bool {
	if g.std == nil {
		return false
	}
	f := stdPick(g.std.byIface["Debug.inspect"][k], []kind{k})
	return f != nil && f.why == "" && len(f.params) == 1 && f.params[0] == k && f.result == kindString
}
