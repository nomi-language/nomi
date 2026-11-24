package analysis_test

import (
	"slices"
	"strings"
	"testing"
)

// An interface value meets a derived interface at a field only when the
// interface is among its own or holds of every value: Nomi has no
// supertraits, so a `Debug` or a `Show` field may hold a type with no
// Display and cannot take a derived Display, at the top, in a List, an
// Option, a tuple or a variant's payload. A `Display` field and one of a
// conjunction with Display can. Debug holds of every value, and Equatable
// and Hashable compare and hash the held value by its type, so their
// derives take any interface field (spec §12 *Derive*, §5 *Equality*).
func TestDeriveOverAnInterfaceField(t *testing.T) {
	const show = "interface Show\n    fn show(value: self) -> String\nend\n\n"
	rejected := []struct{ name, src, want, hint string }{
		{"Display over Debug", "struct H\n    g: Debug\nend\n\nderive Display for H\n",
			"`derive Display for H`: field `g` holds values of `Debug`, and Debug does not require Display of its implementors",
			"type the field with `Display` in place of `Debug`, or write an `impl Display for H` by hand"},
		{"Display over a List of Debug", "struct H\n    g: List<Debug>\nend\n\nderive Display for H\n",
			"`derive Display for H`: field `g` holds values of `Debug`, and Debug does not require Display of its implementors", ""},
		{"Display over an Option of Debug", "struct H\n    g: Option<Debug>\nend\n\nderive Display for H\n",
			"`derive Display for H`: field `g` holds values of `Debug`, and Debug does not require Display of its implementors", ""},
		{"Display over a program's interface", show + "struct H\n    g: Show\nend\n\nderive Display for H\n",
			"`derive Display for H`: field `g` holds values of `Show`, and Show does not require Display of its implementors", ""},
		{"Display over a tuple holding one", show + "struct H\n    g: (Show, Int)\nend\n\nderive Display for H\n",
			"`derive Display for H`: field `g` holds values of `Show`, and Show does not require Display of its implementors", ""},
		{"Display over a payload", show + "enum E\n    A(Show)\nend\n\nderive Display for E\n",
			"`derive Display for E`: field `A` holds values of `Show`, and Show does not require Display of its implementors", ""},
	}
	for _, c := range rejected {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, e := range buildWithStdlibForDerive(c.src).TypeErrors {
				got = append(got, e.Message)
				if e.Message == c.want && c.hint != "" && !slices.Contains(e.Hints, c.hint) {
					t.Errorf("hints %q; want %q", e.Hints, c.hint)
				}
			}
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("the checker admits this derive or reports something else; want the one error %q, got %v\n%s", c.want, got, c.src)
			}
		})
	}
	accepted := []struct{ name, src string }{
		{"Display over Display", "struct H\n    g: Display\nend\n\nderive Display for H\n"},
		{"Display over a conjunction with Display", show + "struct H\n    g: Show and Display\nend\n\nderive Display for H\n"},
		{"Debug over an interface", show + "struct H\n    g: Show\nend\n\nderive Debug for H\n"},
		{"Equatable over an interface", show + "struct H\n    g: Show\nend\n\nderive Equatable for H\n"},
		{"Equatable over a List of an interface", show + "struct H\n    g: List<Show>\nend\n\nderive Equatable for H\n"},
		{"Hashable over an interface", show + "struct H\n    g: Show\nend\n\nderive Hashable for H\n"},
	}
	for _, c := range accepted {
		t.Run(c.name, func(t *testing.T) {
			for _, e := range buildWithStdlibForDerive(c.src).TypeErrors {
				if strings.Contains(e.Message, "required via `derive") || strings.Contains(e.Message, "of its implementors") {
					t.Errorf("the derive is rejected: %s\n%s", e.Message, c.src)
				}
			}
		})
	}
}
