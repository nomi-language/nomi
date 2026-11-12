package analysis

import "testing"

// A single-variant enum's pattern is irrefutable, so `E.V(x) = value` binds
// x to V's payload, as the same pattern does as a parameter (§7 Parameter
// Destructuring). It was rejected as a distinct destructure of a non-distinct
// type.
func TestSingleVariantBinding_BindsThePayload(t *testing.T) {
	src := `enum Only
    One Int
end

enum Rec
    Fields {a: Int, b: String}
end

enum Wrap<T>
    Hold T
end

fn f(o: Only, r: Rec, w: Wrap<String>) -> Int
    Only.One(p) = o
    Rec.Fields(fields) = r
    Wrap.Hold(s) = w
    p
end
`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	for name, want := range map[string]string{"p": "Int", "fields": "{a: Int, b: String}", "s": "String"} {
		var sym *Symbol
		for _, d := range fa.Definitions {
			if d.Name == name && d.Kind == SymbolBinding {
				sym = d
			}
		}
		if sym == nil || sym.Type == nil {
			t.Errorf("%s has no type", name)
			continue
		}
		if got := sym.Type.String(); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}

func TestSingleVariantBinding_Rejections(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"another variant name": {`enum Only
    One Int
end

fn f(o: Only) -> Int
    Only.Two(p) = o
    p
end
`, "variant Two not found in enum Only"},
		"a payload-less variant": {`enum Only
    One
end

fn f(o: Only) -> Int
    Only.One(p) = o
    1
end
`, "variant One carries no data"},
		"a multi-variant enum stays refutable": {`enum Two
    A Int
    B Int
end

fn f(t: Two) -> Int
    Two.A(p) = t
    p
end
`, "this pattern can fail to match a value of type Two"},
		"a non-enum value": {`fn f() -> Int
    Id(x) = 42
    x
end
`, "distinct type destructure requires a distinct type"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(c.src)
			expectError(t, errs, c.want)
		})
	}
}
