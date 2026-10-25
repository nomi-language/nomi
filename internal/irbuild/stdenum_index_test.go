package irbuild

import "testing"

// The opaqueSpecs indices std enum rows name a field's type by point at
// the row of that type, so a row inserted above one fails here by name
// rather than repointing a field at a type of the same representation.
func TestStdEnumOpaqueIndicesNameTheirRow(t *testing.T) {
	for _, c := range []struct {
		index        int
		origin, nomi string
	}{
		{opaqueDuration, "std/duration", "Duration"},
		{opaqueLocale, "std/locale", "Locale"},
		{opaqueCurrency, "std/currency", "Currency"},
	} {
		if got := opaqueSpecs[c.index]; got.origin != c.origin || got.nomi != c.nomi {
			t.Errorf("opaqueSpecs[%d] is %s.%s, want %s.%s", c.index, got.origin, got.nomi, c.origin, c.nomi)
		}
	}
}
