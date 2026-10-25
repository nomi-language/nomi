package rt

// PluralCategory is `std/plural.PluralCategory`: one of CLDR's six plural
// categories.
//
// Here for NormalForm's reason (internal/irbuild/stdenum.go): declared in a
// std module and named from user modules. Selecting a category is
// internal/stdplural's.
type PluralCategory struct {
	// Tag is 1..6 in std/plural.nomi's declaration order. 0 means never
	// constructed.
	Tag uint8
}

// Tag values for PluralCategory, in std/plural.nomi's declaration order.
const (
	TagPluralZero uint8 = iota + 1
	TagPluralOne
	TagPluralTwo
	TagPluralFew
	TagPluralMany
	TagPluralOther
)
