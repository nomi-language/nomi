package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestStdIterSeqLiteralsAreBuilderIntrinsics holds std/iter to the rule its
// Seq comment states: a function whose body builds a `#Seq{...}` must be one the
// builder lowers itself (iterCall: irIterOwnArity, `from`, `repeat`,
// `iterate`). The builder never instantiates `impl Iter for Seq`'s
// `each_while` for a program: generic std functions are instantiated per
// program when called by name, and an impl member reached through a source
// view is not. So a Seq built by a body instantiated from Nomi leaves every
// program that walks it blocked at `Seq.each_while`, with no decline reason.
//
// The gap is std-internal (no program can name Seq), so it is pinned here
// rather than in vmhost's knownLoweringGaps. Reproducer: in std/iter, `pub fn
// f<T>(items: Iter<T>) -> Iter<List<T>> { #Seq{run: fn yield: (List<T>) -> Bool ->
// yield(to_list(items))} }`, walked by a program with `Iter.to_list`.
func TestStdIterSeqLiteralsAreBuilderIntrinsics(t *testing.T) {
	src, ok := std.ReadFile("iter")
	if !ok {
		t.Fatal("std/iter is not embedded")
	}
	checked := 0
	for _, chunk := range strings.Split(string(src), "\n    pub fn ")[1:] {
		name := chunk[:strings.IndexAny(chunk, "<(")]
		builds := false
		for _, line := range strings.Split(chunk, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "//") && strings.Contains(line, "#Seq{") {
				builds = true
			}
		}
		if !builds {
			continue
		}
		checked++
		_, own := irIterOwnArity(name)
		if !own && name != "from" && name != "repeat" && name != "iterate" {
			t.Errorf("Iter.%s builds a Seq but the builder does not lower it itself, so no "+
				"program can walk what it returns (see the comment on Seq in std/iter.nomi): "+
				"write it over the existing adapters", name)
		}
	}
	if checked == 0 {
		t.Fatal("found no Seq literal in std/iter, so this checked nothing")
	}
}
