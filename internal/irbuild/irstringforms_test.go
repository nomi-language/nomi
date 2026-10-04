package irbuild

import "testing"

func TestIRStringForms_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"raw", "import std/io\nfn main() {\n pattern = `\\d{3,4}`\n template = `price: ${PRICE} #{untouched} ##`\n query = `\n   SELECT *\n   FROM invoices\n   `\n io.print(pattern)\n io.print(template)\n io.print(query)\n}\n", "\\d{3,4}\nprice: ${PRICE} #{untouched} ##\nSELECT *\nFROM invoices\n"},
		{"triple", `import std/io
fn main() {
 text = """
   first
   second
   """
 io.print(text)
}
`, "first\nsecond\n"},
		{"captured and returned", "import std/io\nfn text(): String { `\\n` }\nfn main() {\n prefix = `#{raw}:`\n join = |suffix: String| prefix + suffix\n io.print(join(text()))\n}\n", "#{raw}:\\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
