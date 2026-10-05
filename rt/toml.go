package rt

// Toml is `std/toml.Toml`: TOML source text as a distinct value.
//
// # Why this is here at all, when nothing in rt operates on it
//
// There is no extern over Toml. Every function std declares for it —
// `Toml.text`, `Toml.from_fragments` — is an ordinary Nomi body that lowers
// into std/toml's generated package. What a Go type in rt buys is the thing
// opaque.go's header says a spec row is for: letting a stdlib signature name
// the type. `Toml.from_fragments(fragments: List<Fragment<Display>>): Toml`
// cannot be admitted while `Toml` has no representation a call site in another
// generated package can compare against, because a stdFunc's kinds are built
// once and compared by pointer across gens.
//
// # A plain distinct, and that is the difference from Duration
//
// std declares `pub type Toml String`, not `pub opaque type`. `opaque` is a
// use-site rule the front end enforces — outside the declaring file nobody may
// write `Duration(n)` or destructure one — and dropping it changes nothing
// about the representation: a Nomi distinct over a scalar is a Go defined type
// over that scalar either way. What it does change is the surface, and the
// surface is cheap here: `Toml("x")` is `rt.Toml("x")` and `Toml(raw) = t` is
// `string(t)`, the same shapes a user's own distinct has. testdata/toml_use.nomi exercises both from a module that only
// imports the type.
//
// # Nominally distinct, deliberately
//
// Go refuses to assign a Toml to a string or a string to a Toml without a
// conversion, which is the rule the analyzer enforces on the Nomi side. A
// `type Toml = string` alias would compile and would silently let a Toml flow
// into every `String` position in generated code.
type Toml string
