package vmhost

import (
	"strings"
	"testing"
)

// A generic function's use of a std interface its bound names, through an
// operator, a qualified call or a rendering, reaches every implementation
// of that interface in the program, another file's among them: an
// implementation in money.nomi that reads a file is refused through the
// generic function in genlib.nomi, which nothing instantiates, and one
// that only computes passes. Comparable, Debug and Display record their
// implementations under a symbol per file, which the walk reads across
// files for a call through the bound; Equatable and Hashable under one.
// Money is opaque, so its impls are no roots of their own.
func TestEffectRoots_AStdBoundReachesAnotherFilesImplementation(t *testing.T) {
	cases := []struct {
		name, generic, impl string
	}{
		{"an ordering", `pub fn max<T>(a: T, b: T) -> T
where T: Comparable
    if a < b then b else a
end
`, `impl Comparable for Money
    fn compare(a: Money, b: Money) -> Ordering
        READ
        Int.compare(a.cents, b.cents)
    end
end
`},
		{"Comparable.compare", `pub fn order<T>(a: T, b: T) -> Ordering
where T: Comparable
    Comparable.compare(a, b)
end
`, `impl Comparable for Money
    fn compare(a: Money, b: Money) -> Ordering
        READ
        Int.compare(a.cents, b.cents)
    end
end
`},
		{"Debug.inspect", `pub fn show<T>(a: T) -> String
    Debug.inspect(a)
end
`, `impl Debug for Money
    fn inspect(m: Money) -> String
        READ
        "${m.cents}"
    end
end
`},
		{"io.inspect", `import std::io

pub fn peek<T>(a: T)
    io.inspect(a)
end
`, `impl Debug for Money
    fn inspect(m: Money) -> String
        READ
        "${m.cents}"
    end
end
`},
		{"a Display rendering", `pub fn label<T>(a: T) -> String
where T: Display
    "<${a}>"
end
`, `impl Display for Money
    fn to_string(m: Money) -> String
        READ
        "${m.cents}"
    end
end
`},
		{"an equality", `pub fn same<T>(a: T, b: T) -> Bool
where T: Equatable
    a == b
end
`, `impl Equatable for Money
    fn equal(a: Money, b: Money) -> Bool
        READ
        a.cents == b.cents
    end
end
`},
		{"Hashable.hash", `pub fn digest<T>(a: T) -> Int
where T: Hashable
    Hashable.hash(a)
end
`, `impl Hashable for Money
    fn hash(m: Money) -> Int
        READ
        m.cents
    end
end
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			std := "[]"
			if strings.Contains(c.generic, "std::io") {
				std = `["std::io"]`
			}
			generic := strings.Replace(c.generic, "pub fn", "import money.make\n\npub fn sample()\n    _ = make(1)\nend\n\npub fn", 1)
			if strings.HasPrefix(c.generic, "import std::io") {
				generic = strings.Replace(c.generic, "import std::io\n", "import\n    std::io\n    money.make\nend\n\npub fn sample()\n    _ = make(1)\nend\n", 1)
			}
			files := func(read string) map[string]string {
				imports := ""
				if strings.Contains(read, "fs.") {
					imports = "import std::fs\n\n"
				}
				return map[string]string{
					"nomi.toml":   "[package]\nname = \"genlib\"\n\n[effects.self]\neffects = " + std + "\n",
					"genlib.nomi": generic,
					"money.nomi": imports + "pub opaque struct Money\n    cents: Int\nend\n\npub fn make(cents: Int) -> Money\n    Money{cents:}\nend\n\n" +
						strings.Replace(c.impl, "READ", read, 1),
				}
			}
			got, ds := effectDiagnostics(t, "genlib.nomi", files(`_ = fs.read_text("rates.txt")`))
			_, rest, _ := strings.Cut(c.generic, "pub fn ")
			fn, _, _ := strings.Cut(rest, "<")
			if len(ds) != 1 || ds[0].Code != EffectNotAllowedCode || !strings.Contains(got, "money.nomi:") || !strings.Contains(got, "help: reached from `"+fn+"`;") {
				t.Errorf("diagnostics:\n%s\nwant one %s at money.nomi's read, reached from `%s`", got, EffectNotAllowedCode, fn)
			}
			checkedRootsPass(t, "genlib.nomi", files(`_ = 1`))
		})
	}
}
