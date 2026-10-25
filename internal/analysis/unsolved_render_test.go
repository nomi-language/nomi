package analysis_test

import (
	"regexp"
	"testing"
)

// A type variable no solution reached is `_` in a diagnostic, never the
// checker's own name for it (`?23`): a generic reader's result read into a
// field or a result of another type says `Maybe<_>`.
func TestDiagnostics_AnUnsolvedTypeVariableReadsAsUnderscore(t *testing.T) {
	internal := regexp.MustCompile(`\?[0-9]+`)
	for _, tc := range []struct {
		name, src, want string
	}{
		{"a struct field", "struct Contact {\n  name: String\n}\n\nfn read<T>() -> Maybe<T> {\n  None\n}\n\nfn main() {\n  _ = #Contact{name: read()}\n}\n",
			"field 'name' of Contact: expected String, got Maybe<_>"},
		{"a return", "fn read<T>() -> Maybe<T> {\n  None\n}\n\nfn get() -> Int {\n  read()\n}\n\nfn main() {\n  _ = get()\n}\n",
			"return type mismatch: expected Int, got Maybe<_>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			found := false
			for _, e := range errs {
				if internal.MatchString(e.Message) {
					t.Errorf("a diagnostic names an internal type variable: %s", e.Message)
				}
				found = found || e.Message == tc.want
			}
			if !found {
				t.Errorf("no diagnostic says %q; got %v", tc.want, errs)
			}
		})
	}
}
