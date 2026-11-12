package analysis_test

import (
	"strings"
	"testing"
)

// A parameter with no name of its own, a pattern or one written as its
// type, is named in a diagnostic as its signature writes it, never by the
// slot the parser synthesizes for it (`__destr_<line>_<col>`).
func TestParamNameMessages_NameAPatternParameterAsWritten(t *testing.T) {
	const decls = `import std::uuid.Uuid

fn post(request: Int, {id:}: {id: Uuid}) -> String
  "${request} ${id}"
end

fn typed(n: Int, Int) -> Int
  n
end

fn defaulted({id:}: {id: Int} = "x") -> Int
  id
end

fn show<T>((x, y): (T, Int)) -> String
  "${x} ${y}"
end
`
	for _, c := range []struct{ name, body, want string }{
		{"missing pattern argument", "_ = 1 |> post()", "missing argument 2, `{id:}: {id: Uuid}`"},
		{"missing type-only argument", "_ = 1 |> typed()", "missing argument 2, `Int`"},
		{"pattern default", "_ = defaulted({id: 1})", "default value for parameter `{id:}: {id: Int}` is String, expected {id: Int}"},
		{"bound hint", "_ = show((1, 2))", "require it in the signature: `fn show<T>((x, y): (T, Int)) -> String where T: Display`"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + "\nfn demo()\n  " + c.body + "\nend\n")
			found := false
			for _, e := range errs {
				texts := append([]string{e.Message}, e.Hints...)
				for _, text := range texts {
					if strings.Contains(text, "__destr_") {
						t.Errorf("a synthesized name reaches the diagnostic: %q", text)
					}
					if text == c.want {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("no %q among %v", c.want, errs)
			}
		})
	}
}
