package rt

// std/collation's types, here for Locale's reason (rt/locale.go): nothing
// in rt operates on them, and internal/irbuild's std type specs pair each
// std declaration with its Go type here. The collator is
// internal/collation's, reached through internal/stdformat.

// Collator is `std/collation.Collator`: the locale's tag and the options
// the collator was made with, which internal/stdformat reads.
type Collator string

// CollationStrength is `std/collation.Strength` (Primary to Identical), a
// bare enum: std/collation.nomi passes its position across, so the type
// only lets `Collator.new`'s signature lower.
type CollationStrength struct {
	Tag uint8
}

// CaseFirst is `std/collation.CaseFirst` (Default, Upper, Lower, Off),
// CollationStrength's twin.
type CaseFirst struct {
	Tag uint8
}
