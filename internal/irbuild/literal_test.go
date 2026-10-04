package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The refusal a typed literal carries is PINNED ABSOLUTELY — construct AND
// detail, byte for byte — and the pin has now had to move three times, which
// is the point of it: each move is the refusal tracking a frontier, and the
// text is the only thing that says WHICH frontier.
//
// A relative assertion ("some blocker was named") would pass for every
// mutation this design can sustain, each verified in a throwaway worktree and
// each caught here:
//
//   - the tag-column math without the `- len(Tag)`, which turns the analyzer
//     lookup into a silent MISS: every literal falls back to naming the
//     keyword, no test outside this file fails, and the sweep stays at
//     0 COMPILE-FAIL / 0 RUN-FAIL / 0 DIFFED.
//   - reading the handler's RESULT rather than its fragment PARAMETER, which
//     names a true refusal about the wrong POSITION.
//
// A typed literal is LOWERED now, so requireIdentical finally has something to
// say about the construct — see TestPinned_TypedLiteral. This file keeps the
// refusal half, which is still where most of the corpus lands.

// TestTypedLiteral_TagPositionIsTheTagAndNotTheQuote pins the one piece of
// arithmetic this file owns.
//
// The lexer captures a TaggedString's Line/Col at the opening quote, having
// already consumed the tag; both resolveTaggedStringTag and checkTaggedString
// key their reference len(Tag) columns earlier. Getting that wrong is a silent
// miss — every literal falls back to naming the keyword and the corpus stays
// green — so the agreement is asserted against the ANALYZER's own table rather
// than against a recomputation.
func TestTypedLiteral_TagPositionIsTheTagAndNotTheQuote(t *testing.T) {
	const src = "import std/toml.Toml\n\nfn main() {\n  _ = Toml`k = 1`\n}\n"
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("front end: %v", err)
	}
	m := p.Modules[0]
	var lit *ast.TaggedString
	walkForTaggedString(m.Nodes, &lit)
	if lit == nil {
		t.Fatal("the fixture contains no typed literal")
	}
	pos := analysis.Pos{Line: lit.Line, Col: max(lit.Col-len(lit.Tag), 1)}
	sym := m.FA.References[pos]
	if sym == nil {
		t.Fatalf("no analyzer reference at the tag position %v; the node itself is at %d:%d",
			pos, lit.Line, lit.Col)
	}
	if sym.DispatchImpl == nil {
		t.Fatal("the analyzer reference carries no DispatchImpl, so the handler is unreachable")
	}
	// The QUOTE position must not resolve, or the arithmetic would be
	// unfalsifiable.
	if m.FA.References[analysis.Pos{Line: lit.Line, Col: lit.Col}] != nil {
		t.Fatal("the opening-quote position also resolves; this test cannot fail and proves nothing")
	}
}

func walkForTaggedString(nodes []ast.Node, out **ast.TaggedString) {
	for _, n := range nodes {
		if isNilNode(n) {
			continue
		}
		if ts, ok := n.(*ast.TaggedString); ok {
			*out = ts
			return
		}
		walkForTaggedString(childNodes(n), out)
		if *out != nil {
			return
		}
	}
}

// TestLiteral_FragmentSpecIsResolved pins the one name this file resolves.
//
// literal.go identifies the fragment enum by SPEC POINTER, resolved once
// through the (Origin, Name) rule. If the spec table ever stops carrying that
// row the pointer goes nil, `lowerTaggedLiteral` declines for every site, and
// every typed literal silently reverts to a refusal — green everywhere else.
func TestLiteral_FragmentSpecIsResolved(t *testing.T) {
	if fragmentSpec == nil {
		t.Fatal("no preludeSpec for std/literals.Fragment; every typed literal " +
			"would decline to lower and nothing else would notice")
	}
	if fragmentSpec.nomi != "Fragment" || fragmentSpec.origin != "std/literals" {
		t.Fatalf("fragmentSpec resolved to %s.%s", fragmentSpec.origin, fragmentSpec.nomi)
	}
}
