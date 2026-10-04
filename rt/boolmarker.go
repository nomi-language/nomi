package rt

// Nomi's `Bool` is not a primitive. `std/bool.nomi` declares
//
//	pub host type True
//	pub host type False
//	pub enum Bool { embeds False; embeds True }
//
// so `True` and `False` are two SINGLETON TYPES and `Bool` is an enum embedding
// them. The IR builder (internal/irbuild) gives `Bool` the kind of a Go `bool`,
// the one compiler special case, and the two singletons then have nowhere to
// live.
//
// # Why they need a Go type at all, when Bool already has one
//
// Because `Bool.False(x)` is legal Nomi and binds `x` to the singleton. That is
// not an obscure spelling: it is what the DERIVE SYNTHESIZER writes for every
// interface `Bool` derives. `analysis/derive_synthesis.go`'s `stringifyEnumExpr`
// plus `stringifyVariantBody`'s `"embedded"` arm produce, for `derive Debug for
// Bool`,
//
//	fn inspect(value: Bool): String {
//	  case value {
//	    Bool.False(v0) -> Debug.inspect(v0)
//	    Bool.True(v0)  -> Debug.inspect(v0)
//	  }
//	}
//
// and `Debug.inspect` at a `pub host type` is the extern name-only default
// (`synthesizeExternNameOnlyDebug`), i.e. the bare type name. So `Debug`
// answers `"False"` and `"True"` by going THROUGH these two types, and a
// builder with no representation for them cannot lower `bool.Bool.inspect`,
// `bool.Bool.to_string`, `bool.Bool.equal?` or `bool.Bool.hash` — four
// declarations, one cause, and the cause is a missing pair of zero-width structs.
//
// MEASURED at 32ee8f7f, and this is what makes the pair worth its own file
// rather than a widened tolerance somewhere: the same absence was being reported
// under THREE different refusal keys in two corpus files —
// `unlowered stdlib function | bool.Bool.inspect`,
// `binding a Bool variant payload | Bool.False` and
// `Debug on an unrepresented receiver | List<Bool>` — plus, in a third file,
// `Debug on an unrepresented receiver | Token` for an 18-variant enum with one
// `Flag Bool` variant. Dropping that one variant took that file's Debug key away
// and its `masked` count from 3 to 0.
//
// # Zero-WIDTH, not a leaf with contents
//
// Each type has exactly ONE inhabitant, so naming the type names the value:
// `rt.BoolTrue{}` is the only `rt.BoolTrue` there is. That is what lets a `case`
// arm binding the payload MATERIALIZE it from a Go `bool` it was never stored
// in — the payload contributes no information, so there is nothing to recover.
//
// The consequences are the marker consequences and they are all forced rather
// than chosen: two are always equal because there is nothing to differ, the hash
// is the unit hash, and `zeroSized()` is true. That is the opposite of rt.Bytes,
// which looks like a marker from the builder's side and has a payload rt owns —
// see stdhost.go's `rtOpaque` field for the four wrong answers that shape
// produced before the flag existed.
//
// # Spelled BoolTrue/BoolFalse rather than True/False
//
// The Nomi names are `True` and `False`, and the builder carries them on the
// def's `nomi` field. In Go, `rt.True` as a TYPE beside a language with `true`
// as a VALUE is a reader trap (`var x rt.True = rt.True{}`), and rt's own
// callers would meet it in signatures. The prefix costs a reader
// nothing and the mapping is stated in one place, the stdHostSpecs rows.
type BoolTrue struct{}

// BoolFalse is `std/bool.False`. See BoolTrue.
type BoolFalse struct{}
