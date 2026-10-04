package irbuild

import "testing"

// Shapes std bodies use, each driven from a user program so the shape runs on
// the VM against its expected output (verifyLambdaProgram), and each naming
// the functions that must be retained.
func TestIRStdRetention_ProgramsRunOnTheVM(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
		retained        []string
	}{
		{"Int and Float rt hosts", `import std/io

fn wrap(a: Int, b: Int): Int {
  Int.wrapping_add(Int.wrapping_mul(a, b), Int.bitwise_xor(a, 5))
}

fn classify(x: Float): String {
  if Float.nan?(x) {
    "nan"
  } else if x == Float.positive_infinity() {
    "inf"
  } else {
    Float.to_string(Float.floor(x))
  }
}

fn text(n: Int): String {
  Int.to_string(n)
}

fn whole(x: Float): Maybe<Int> {
  Float.to_int(x)
}

fn main() {
  io.print(wrap(3, 4))
  io.print(classify(Float.nan()))
  io.print(classify(Float.positive_infinity()))
  io.print(classify(2.75))
  io.print(text(-12))
  io.inspect(whole(3.9))
  io.inspect(whole(Float.nan()))
}
`, "18\nnan\ninf\n2.0\n-12\nSome(3)\nNone\n",
			[]string{"wrap", "classify", "text", "whole", "main"}},
		{"String hosts", `import std/io

fn shout(s: String): String {
  String.to_upper(String.replace(s, "a", "o"))
}

fn trimmed(s: String): String {
  if String.starts_with?(s, "<") and String.ends_with?(s, ">") {
    String.slice(s, 1, String.length(s) - 1)
  } else {
    s
  }
}

fn words(s: String): List<String> {
  String.split(s, " ")
}

fn joined(xs: List<String>): String {
  String.join(xs, "-")
}

fn main() {
  io.print(shout("banana"))
  io.print(trimmed("<tag>"))
  io.print(trimmed("tag"))
  io.inspect(words("a b c"))
  io.print(joined(words("x y")))
  io.print(String.reverse("abc"))
}
`, "BONONO\ntag\ntag\n[\"a\", \"b\", \"c\"]\nx-y\ncba\n",
			[]string{"shout", "trimmed", "words", "joined"}},
		{"Decimal ordering", `import std/io

fn smaller(a: Decimal, b: Decimal): Decimal {
  if a <= b { a } else { b }
}

fn sign(d: Decimal): Int {
  case {
    d < 0d -> -1
    d > 0d -> 1
    _ -> 0
  }
}

fn main() {
  io.inspect(smaller(1.50d, 1.5d))
  io.inspect(smaller(2d, 1.25d))
  io.print(sign(-0.5d))
  io.print(sign(0.00d))
  io.print(sign(3d))
  io.inspect(1.5d >= 1.50d)
}
`, "1.50d\n1.25d\n-1\n0\n1\nTrue\n",
			[]string{"smaller", "sign", "main"}},
		{"lists of String pairs", `import std/io

fn entry_name(entry: (String, String)): String {
  (name, _) = entry
  name
}

fn entry_value(entry: (String, String)): String {
  (_, value) = entry
  value
}

fn lookup(entries: List<(String, String)>, key: String): Maybe<String> {
  case Iter.find(entries, |entry| entry_name(entry) == key) {
    .Some(entry) -> Maybe.Some(entry_value(entry))
    .None -> Maybe.None
  }
}

fn values(entries: List<(String, String)>, key: String): List<String> {
  entries
  |> Iter.filter(|entry| entry_name(entry) == key)
  |> Iter.map(|entry| entry_value(entry))
  |> Iter.to_list()
}

fn put(entries: List<(String, String)>, key: String, value: String): List<(String, String)> {
  entries
  |> Iter.filter(|entry| entry_name(entry) != key)
  |> Iter.concat([(key, value)])
  |> Iter.to_list()
}

fn main() {
  xs = [("a", "1"), ("b", "2"), ("a", "3")]
  io.inspect(lookup(xs, "a"))
  io.inspect(lookup(xs, "z"))
  io.inspect(values(xs, "a"))
  io.inspect(Iter.any?(xs, |entry| entry_value(entry) == "2"))
  io.inspect(values(put(xs, "a", "9"), "a"))
  io.inspect(Iter.count(put(xs, "c", "4")))
}
`, "Some(\"1\")\nNone\n[\"1\", \"3\"]\nTrue\n[\"9\"]\n4\n",
			[]string{"entry_name", "entry_value", "lookup", "values", "put", "main"}},
		{"std Debug through io.inspect", `import std/io
import std/calendar.{Date}

fn show(d: Decimal): Decimal {
  d
}

fn day(d: Date): Date {
  d
}

fn main() {
  io.inspect(show(2.50d))
  case Date"2024-01-02" {
    Ok(d) -> io.inspect(day(d))
    Err(_) -> io.print("bad")
  }
}
`, "2.50d\n2024-01-02\n",
			[]string{"show", "day", "main"}},
		{"std/json's DecodeError and ShapeError", `import std/io
import std/json.Json

fn failure(): Json.DecodeError {
  Json.DecodeError{message: "bad", line: 2, col: 3, offset: 9}
}

fn position(e: Json.DecodeError): String {
  "${e.line}:${e.col}"
}

fn shape(): Json.ShapeError {
  Json.ShapeError{path: ["a", "b"], expected: "Int", got: "String"}
}

fn depth(e: Json.ShapeError): Int {
  Iter.count(e.path)
}

fn main() {
  io.print(position(failure()))
  io.print(Display.to_string(failure()))
  io.print(depth(shape()))
  io.inspect(shape())
}
`, "2:3\njson decode error at line 2, col 3: bad\n2\nJson.ShapeError{path: [\"a\", \"b\"], expected: \"Int\", got: \"String\"}\n",
			[]string{"failure", "position", "shape", "depth", "main"}},
		// Project carries a prelude Maybe over a std distinct type in a field
		// whose default std declares, and a spread replaces it.
		{"std/compiler's Project", `import std/io
import std/compiler.Project
import std/toml.Toml

fn project(): Project {
  Project{entry_point: "main", files: {"main" => "fn main() {}"}}
}

fn configured(p: Project): Project {
  {..p, manifest: Some(Toml"[module]\nname = \"demo\"\n")}
}

fn has_manifest?(p: Project): Bool {
  case p.manifest {
    Some(_) -> True
    None -> False
  }
}

fn main() {
  io.print(has_manifest?(project()))
  io.print(has_manifest?(configured(project())))
  io.print(project().entry_point)
}
`, "False\nTrue\nmain\n",
			[]string{"project", "configured", "has_manifest?", "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			irLeftoversRetained(t, tc.src, tc.retained...)
			verifyLambdaProgram(t, tc.src, tc.want)
		})
	}
}
