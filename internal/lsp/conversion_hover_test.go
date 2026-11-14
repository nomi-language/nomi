package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The hover over a value that converts to a wider type (spec §5 *Covariant
// conversion*) ends with what the conversion does at run time, read from
// the checker's record the IR builder obeys (hoverdoc.ConversionNote). A
// conversion that relabels the value says nothing.
func TestHover_ConversionCost(t *testing.T) {
	const decls = `import std::io

struct Box<T>
    item: T
end

fn maybe_one() -> Maybe<Int>
    Some(1)
end

fn maybes() -> List<Maybe<Int>>
    [Some(1)]
end

fn shown<T>(xs: List<T>) -> List<Display> where T: Display
    ▮xs
end

`
	cases := []struct {
		name, body string
		// want is the hover's last line, or "" for a hover with none.
		want string
	}{
		{"a List rebuilt", `    xs = [Some(1)]
    wide: List<Display> = ▮xs`, "*converts* by rebuilding the `List<Maybe<Int>>` as a `List<Display>`, one pass over its elements"},
		{"a nested List rebuilt", `    xs = [[Some(1)]]
    wide: List<List<Display>> = ▮xs`, "*converts* by rebuilding the `List<List<Maybe<Int>>>` as a `List<List<Display>>`, one pass over its elements"},
		{"a Vector rebuilt", `    xs = Vector[Some(1)]
    wide: Vector<Display> = ▮xs`, "*converts* by rebuilding the `Vector<Maybe<Int>>` as a `Vector<Display>`, one pass over its elements"},
		{"a Set rebuilt", `    xs = Set[Some(1), None]
    wide: Set<Hashable> = ▮xs`, "*converts* by rebuilding the `Set<Maybe<Int>>` as a `Set<Hashable>`, one pass over its elements, rehashing each one"},
		{"a Map rebuilt", `    m = {Some(1) => [1], None => []}
    wide: Map<Hashable, Display> = ▮m`, "*converts* by rebuilding the `Map<Maybe<Int>, List<Int>>` as a `Map<Hashable, Display>`, one pass over its entries, rehashing each key"},
		{"a Maybe rebuilt", `    m = Some([1, 2])
    wide: Maybe<Display> = ▮m`, "*converts* by rebuilding the `Maybe<List<Int>>` as a `Maybe<Display>`, one match"},
		{"a Result rebuilt", `    r: Result<List<Int>, String> = Ok([1])
    wide: Result<Display, String> = ▮r`, "*converts* by rebuilding the `Result<List<Int>, String>` as a `Result<Display, String>`, one match"},
		{"a tuple rebuilt", `    p = (Some(1), 2)
    wide: (Display, Int) = ▮p`, "*converts* by rebuilding the `(Maybe<Int>, Int)` as a `(Display, Int)` field by field"},
		{"a record rebuilt", `    r = {n: Some(1)}
    wide: {n: Display} = ▮r`, "*converts* by rebuilding the `{n: Maybe<Int>}` as a `{n: Display}` field by field"},
		{"a declared generic struct rebuilt", `    b = Box{item: 1}
    wide: Box<Display> = ▮b`, "*converts* by rebuilding the `Box<Int>` as a `Box<Display>` field by field"},
		{"a function wrapped", `    wide: () -> Display = ▮maybe_one`, "*converts* by wrapping the function: each call converts its arguments and result"},
		{"a call's result", `    wide: List<Display> = ▮maybes()`, "*converts* by rebuilding the `List<Maybe<Int>>` as a `List<Display>`, one pass over its elements"},
		{"a List rebuilt into a conjunction", `    xs = [Some(1)]
    wide: List<Display and Debug> = ▮xs`, "*converts* by rebuilding the `List<Maybe<Int>>` as a `List<Debug and Display>`, one pass over its elements"},
		{"a List relabelled into a conjunction", `    xs = [1, 2]
    wide: List<Display and Debug> = ▮xs`, ""},
		{"a List relabelled", `    xs = [1, 2]
    wide: List<Display> = ▮xs`, ""},
		{"a nested List relabelled", `    xs = [[1]]
    wide: List<List<Display>> = ▮xs`, ""},
		{"a function relabelled", `    f = fn -> 1
    wide: () -> Display = ▮f`, ""},
	}
	check := func(t *testing.T, src, want string) {
		t.Helper()
		src, pos := hoverMarkerPosition(t, src)
		doc := NewServer().docs.Open("file:///conversion.nomi", src)
		if doc == nil || doc.Analysis == nil || len(doc.Errors) > 0 || len(doc.Analysis.TypeErrors) > 0 {
			t.Fatalf("the program does not check clean: %v %v", doc.Errors, doc.Analysis.TypeErrors)
		}
		got, _ := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)})
		if got == "" {
			t.Fatal("no hover")
		}
		last := got[strings.LastIndex(got, "\n\n")+2:]
		switch {
		case want == "" && strings.Contains(got, "*converts*"):
			t.Errorf("hover = %q, want no conversion line", got)
		case want != "" && last != want:
			t.Errorf("hover ends %q, want %q\nwhole hover: %q", last, want, got)
		}
	}
	// The one marker in decls is in shown's body; each case's own marker
	// replaces it.
	unmarked := strings.Replace(decls, "▮", "", 1)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := unmarked + "fn main()\n" + tc.body + "\n    io.inspect(wide)\nend\n"
			check(t, src, tc.want)
		})
	}
	t.Run("a generic body, decided per instance", func(t *testing.T) {
		check(t, decls+"fn main()\n    io.inspect(shown([1]))\nend\n",
			"*converts* with no copy where each `T` is an `Int`, `Float`, `Bool`, `String` or declared type, and otherwise by rebuilding the `List<T>` as a `List<Display>`, one pass over its elements")
	})
}
