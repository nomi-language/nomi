package stdcompiler_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/stdcompiler"
)

// TestHoverContentTypesTheFunctionsWrittenForASubtype: `compiler.hover`
// checks the source's declarations completed as a checked tree's are
// (PrepareNodes), so a call of a subtype's `unchecked` or `try_from` in the
// declaring file resolves and hover shows the function's signature.
func TestHoverContentTypesTheFunctionsWrittenForASubtype(t *testing.T) {
	for _, tc := range []struct{ call, want string }{
		{"Positive.ˇunchecked(1)", "fn unchecked(value: Int) -> Positive"},
		{"Positive.ˇtry_from(1)", "fn try_from(value: Int) -> Option<Positive>"},
	} {
		source := "subtype Positive Int when fn n -> n > 0\n\nfn main()\n    _ = " + tc.call + "\nend\n"
		h, err := stdcompiler.HoverContent(source, "", nil)
		if err != nil {
			t.Errorf("%s: %v", tc.call, err)
			continue
		}
		if h.Signature != tc.want {
			t.Errorf("%s: signature %q, want %q", tc.call, h.Signature, tc.want)
		}
	}
}
