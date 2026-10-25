package rt

// LocaleError is `std/locale.Error`: why text is not a locale tag.
//
// Here for IOError's reason (internal/irbuild/stdenum.go): declared in a std
// module and named from user modules, so `Result<Locale, locale.Error>` needs a
// representation every generated package shares. The classification itself
// is internal/stdlocale's.
type LocaleError struct {
	// Tag is 1..4 in std/locale.nomi's declaration order. 0 means never
	// constructed.
	Tag uint8
	// Suggestion is `Underscore`'s tag written with `-`.
	Suggestion string
	// Text is `Malformed`'s rejected text.
	Text string
	// Subtag is `UnknownSubtag`'s unknown subtag.
	Subtag string
}

// LocaleResult carries `Result<Locale, locale.Error>` from std/locale's host
// functions: the canonical tag when Kind is 0, else the error's tag and its
// one field. See internal/irbuild/stdstruct.go's carrier rows.
type LocaleResult struct {
	Value  string
	Kind   int64
	Detail string
}

// Tag values for LocaleError, in std/locale.nomi's declaration order.
const (
	TagLocaleEmpty uint8 = iota + 1
	TagLocaleUnderscore
	TagLocaleMalformed
	TagLocaleUnknownSubtag
)

// LocaleLoadError is `std/locale.LoadError`: why `Locale.load` could not
// make a locale ready, here for LocaleError's reason. Both variants carry
// the locale whose file failed and why.
type LocaleLoadError struct {
	// Tag is 1 (Unreachable) or 2 (Unreadable), std/locale.nomi's order.
	// 0 means never constructed.
	Tag    uint8
	Locale Locale
	Reason string
}
