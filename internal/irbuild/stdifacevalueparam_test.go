package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A std function whose parameter is a std interface held as a value
// (`Json.encode_as(value: ToJson)`, `PluralCategory.cardinal(value:
// PluralOperand, ...)`) runs from two files: a scalar and a boxed std type
// pass to it, and it runs named as a value. A `Matcher` value, held or a
// function of the program's own interface-typed parameter, passes on to
// the generic `String.split` too. The shared stdlib
// lowering has no def for such an interface, so the plan for the
// `Type.method` call found no function to call and declined; each is now
// one instance per program, built at its first caller's def, which each
// file passes its value to at its own.
func TestStdInterfaceValueParameter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import
    std::io
    std::json.Json
    std::matcher.Matcher
    std::plural.PluralCategory
    words
end

fn main()
    io.print(words.words("a b  c"))
    io.print(String.split("x,y", ","))
    comma: Matcher = ","
    io.print(String.split("p,q", comma))
    io.print(words.parts("1;2", ";"))
    io.print(String.starts_with("hello", "he"))
    io.print(String.replace("a-b", "-", "+"))
    io.print(Json.encode_as({"k" => [1]}))
    io.print(words.encoded([4, 5]))
    io.print(PluralCategory.cardinal(1, Locale` + "`en`" + `) == PluralCategory.One)
    io.print(PluralCategory.cardinal(1.5d, Locale` + "`fr`" + `) == PluralCategory.One)
    encode = Json.encode_as
    io.print(encode([6]))
end
`,
		"words.nomi": `import
    std::json.Json
    std::matcher.Matcher
    std::regex.Regex
end

pub fn words(text: String) -> List<String>
    String.split(text, Regex` + "`\\s+`" + `)
end

pub fn parts(text: String, on: Matcher) -> List<String>
    String.split(text, on)
end

pub fn encoded(xs: List<Int>) -> String
    Json.encode_as(xs)
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = "[a, b, c]\n[x, y]\n[p, q]\n[1, 2]\nTrue\na+b\n{\"k\":[1]}\n[4,5]\nTrue\nTrue\n[6]\n"
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
