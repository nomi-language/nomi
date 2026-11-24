package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestStdIterBareOwnCallIsTheQualifiedCall holds a bare call inside `impl
// Iter<T>` to an operation the builder lowers itself to the qualified call's
// lowering (stdGenericSiblingCall). `chunk_equal`'s body is `chunk_by(source,
// fn item -> item)`; instantiating `chunk_by`'s std body instead reaches the
// module host fn `chunk_by_each`, which only iterCall lowers, and the program
// declined at `Iter.chunk_equal`. The test first checks the body still spells
// the call bare, so it keeps exercising that route.
func TestStdIterBareOwnCallIsTheQualifiedCall(t *testing.T) {
	src, ok := std.ReadFile("iter")
	if !ok {
		t.Fatal("std/iter is not embedded")
	}
	_, body, _ := strings.Cut(string(src), "pub fn chunk_equal<")
	body, _, _ = strings.Cut(body, "\n    end\n")
	if !strings.Contains(body, "\n        chunk_by(source,") {
		t.Fatalf("Iter.chunk_equal no longer calls chunk_by bare, so this test checks nothing; its body:\n%s", body)
	}
	verifyLambdaProgram(t, `import std::io

fn main()
    io.inspect([1, 1, 2, 2, 1] |> Iter.chunk_equal() |> Iter.to_list())
    io.inspect("aab" |> Iter.chunk_equal() |> Iter.map(Iter.count) |> Iter.to_list())
end
`, "[[1, 1], [2, 2], [1]]\n[2, 1]\n")
}
