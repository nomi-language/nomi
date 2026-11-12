package irbuild

import (
	"testing"
)

func TestIRUnicode_TourTextProgram(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
 io.print(String.length("café"))
 io.inspect(Iter.to_list("café"))
 "café" |> String.to_codepoints() |> Iter.map(Codepoint.to_int) |> Iter.to_list() |> io.inspect()
end
`, "4\n[\"c\", \"a\", \"f\", \"é\"]\n[99, 97, 102, 233]\n")
}

func TestIRUnicode_ClustersAndScalars(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn points(s: String) -> List<Codepoint>
    String.to_codepoints(s)
end
fn numbers(s: String) -> List<Int>
 points(s) |> Iter.map(Codepoint.to_int) |> Iter.to_list()
end
fn main()
 io.print(Iter.count("é👩‍💻"))
 io.inspect(Iter.to_list("é👩‍💻"))
 io.inspect(numbers("é👩‍💻"))
 io.inspect(numbers(""))
 io.inspect(Iter.to_list(""))
 xs = Iter.map("é👩‍💻x", fn s ->
     io.print(s)
     s
 end) |> Iter.take(2)
 io.print("created")
 io.inspect(Iter.to_list(xs))
 io.inspect(Iter.to_list(xs))
end
`, "2\n[\"é\", \"👩‍💻\"]\n[101, 769, 128105, 8205, 128187]\n[]\n[]\ncreated\né\n👩‍💻\n[\"é\", \"👩‍💻\"]\né\n👩‍💻\n[\"é\", \"👩‍💻\"]\n")
}

// A list of a std named type renders through std's own Debug impl, as
// native's container inspector does.
func TestIRUnicode_CodepointListDebug(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
    io.inspect(String.to_codepoints("a\u{301}"))
end
`, "[Codepoint(97), Codepoint(769)]\n")
}

// `==` on two lists compares them structurally whatever their elements.
func TestIRUnicode_CodepointListEquality(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  io.print(String.to_codepoints("a") == String.to_codepoints("a"))
  io.print(String.to_codepoints("a") != String.to_codepoints("b"))
end
`, "True\nTrue\n")
}

// A list pattern over a list of Codepoints tests its length and binds cells,
// which is the same over any element.
func TestIRUnicode_NominalListPatterns(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  case String.to_codepoints("a")
    [_, ..] -> io.print(1)
    _ -> io.print(0)
  end
end
`, "1\n")
}
