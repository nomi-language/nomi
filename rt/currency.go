package rt

// Currency is `std/currency.Currency`: an ISO 4217 code (`EUR`), or a
// custom code and its minor digits (`BTC:8`).
//
// Here for Locale's reason (rt/locale.go): nothing in rt operates on it, and
// a Go type in rt is what lets a std signature, `NumberParseError`'s
// `WrongCurrency` among them, name the type across generated packages. The
// codes and their digits are internal/stdformat's.
type Currency string

// CurrencyError is `std/currency.Error`: why text, or a code and its digits,
// are not a currency, here for LocaleError's reason. `IsoCode` and `NotIso`
// share the Code slot.
type CurrencyError struct {
	// Tag is 1..4 in std/currency.nomi's declaration order. 0 means never
	// constructed.
	Tag uint8
	// Text is `Malformed`'s rejected text.
	Text string
	// Code is `IsoCode`'s and `NotIso`'s code.
	Code string
	// Digits is `Digits`' rejected count.
	Digits int64
}

// Tag values for CurrencyError, in std/currency.nomi's declaration order.
const (
	TagCurrencyMalformed uint8 = iota + 1
	TagCurrencyIsoCode
	TagCurrencyNotIso
	TagCurrencyDigits
)
