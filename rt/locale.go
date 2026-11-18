package rt

// Locale is `std/locale.Locale`: a canonical BCP 47 language tag.
//
// Nothing in rt operates on it. It is here because internal/irbuild's std
// type specs pair the std declaration with this Go type, and rt is the
// package they and std's Go adapters share. Parsing, canonicalization and
// fallback live in internal/stdlocale, which may import golang.org/x/text;
// rt may not.
type Locale string
