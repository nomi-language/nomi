package rt

// Locale is `std/locale.Locale`: a canonical BCP 47 language tag.
//
// Here for Toml's and Seed's reason (see rt/toml.go): nothing in rt operates
// on it, and a Go type in rt is what lets a std signature name the type
// across generated packages. Parsing, canonicalization and fallback live in
// internal/stdlocale, which may import golang.org/x/text; rt may not.
type Locale string
