package irbuild

import "testing"

func TestIRIO_InspectCompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"rendering and effects", `import std/io as out
fn text(): String { out.print("text") "a\nb\t\"\\z" }
fn main() {
 out.print("plain")
 out.inspect("quoted")
 out.inspect(text())
 out.inspect(42)
 out.inspect(True)
 out.inspect(1.5)
 out.inspect([[1], [2, 3]])
 empty = || { out.print("empty") [] }
 out.inspect(empty())
}`, "plain\n\"quoted\"\ntext\n\"a\nb\t\\\"\\\\z\"\n42\nTrue\n1.5\n[[1], [2, 3]]\nempty\n[]\n"},
		{"multiline and lambda", `import std/io
fn main() {
 show = |x: String| { io.inspect(x) }
 show("closure")
 io.inspect([
  "a",
  "b",
 ])
}`, "\"closure\"\n[\"a\", \"b\"]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
