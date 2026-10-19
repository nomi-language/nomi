package analysis

import (
	"strings"
	"testing"
)

// A bare variant that two enums declare names both enums, in declaration
// order, on every run. The scope's symbols are a map, and the hint once
// named whichever enum the map gave first.
func TestBareVariantHintNamesEveryOwnerInDeclarationOrder(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "nullary",
			src: `
enum A {
  Origin
  Other
}

enum B {
  Origin
  Elsewhere
}

fn main() -> A {
  Origin
}
`,
			want: "variant 'Origin' must be qualified through its enum; 'Origin' is a variant of both 'A' and 'B', so use 'A.Origin' or 'B.Origin'",
		},
		{
			name: "three owners",
			src: `
enum C {
  Origin
}

enum A {
  Origin
}

enum B {
  Origin
}

fn main() -> A {
  Origin
}
`,
			want: "'Origin' is a variant of 'C', 'A' and 'B', so use 'C.Origin', 'A.Origin' or 'B.Origin'",
		},
		{
			name: "struct variant",
			src: `
enum A {
  At {x: Int}
}

enum B {
  At {x: Int}
}

fn main() -> A {
  #At{x: 1}
}
`,
			want: "'At' is a variant of both 'A' and 'B', so use '#A.At{...}' or '#B.At{...}'",
		},
		{
			name: "one owner",
			src: `
enum A {
  Origin
  Other
}

fn main() -> A {
  Origin
}
`,
			want: "variant 'Origin' must be qualified through its enum; use 'A.Origin'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := ""
			for run := 0; run < 50; run++ {
				_, errs := checkSource(tc.src)
				var msgs []string
				for _, e := range errs {
					msgs = append(msgs, e.Message)
				}
				got := strings.Join(msgs, "\n")
				if run == 0 {
					first = got
					if !strings.Contains(got, tc.want) {
						t.Fatalf("errors do not hold %q:\n%s", tc.want, got)
					}
					continue
				}
				if got != first {
					t.Fatalf("run %d differs from the first:\n%s\nfirst:\n%s", run, got, first)
				}
			}
		})
	}
}
