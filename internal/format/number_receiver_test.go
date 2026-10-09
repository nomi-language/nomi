package format

import "testing"

// A field read whose receiver is a decimal Int literal and whose field is a
// number must not print as `0.0`, which lexes as one Float. The checker
// rejects such a read (an Int has no fields), but the formatter still owes
// the parser the same tree back.
func TestFormat_NumberFieldOnIntLiteralDoesNotBecomeAFloat(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"fn A() {\n    0 .0\n}\n", "fn A() {\n    (0).0\n}\n"},
		{"fn A() {\n    (0).0\n}\n", "fn A() {\n    (0).0\n}\n"},
		{"fn A() {\n    1. 5\n}\n", "fn A() {\n    (1).5\n}\n"},
		{"fn A() {\n    1_000 .0\n}\n", "fn A() {\n    (1_000).0\n}\n"},
		{"fn A() {\n    1 .0.1\n}\n", "fn A() {\n    (1).0.1\n}\n"},
		{"fn A() {\n    -1 .0\n}\n", "fn A() {\n    -(1).0\n}\n"},
		// No hazard: these print without parentheses and reparse the same.
		{"fn A() {\n    0x1 .0\n}\n", "fn A() {\n    0x1.0\n}\n"},
		{"fn A() {\n    1 .e5\n}\n", "fn A() {\n    1.e5\n}\n"},
		{"fn A() {\n    1.5 .0\n}\n", "fn A() {\n    1.5.0\n}\n"},
		{"fn A() {\n    1e5 .0\n}\n", "fn A() {\n    1e5.0\n}\n"},
		{"fn A() {\n    t.1 .0\n}\n", "fn A() {\n    t.1.0\n}\n"},
		// An accessor writes its path as one run; `.1.0` parses as the
		// two indices `.1 .0` does.
		{"fn A() {\n    f(.1 .0)\n}\n", "fn A() {\n    f(.1.0)\n}\n"},
		{"fn A() {\n    f(.x.1 .0)\n}\n", "fn A() {\n    f(.x.1.0)\n}\n"},
	} {
		got, err := Format(tc.src)
		if err != nil {
			t.Fatalf("Format(%q): %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("Format(%q) =\n%s\nwant\n%s", tc.src, got, tc.want)
		}
		if err := SameMeaning(tc.src, got); err != nil {
			t.Errorf("Format(%q) changed its meaning: %v", tc.src, err)
		}
		again, err := Format(got)
		if err != nil || again != got {
			t.Errorf("Format is not idempotent on %q: %q, %v", got, again, err)
		}
	}
}
