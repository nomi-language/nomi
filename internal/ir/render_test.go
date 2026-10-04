package ir_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

const renderFile = "render.nomi"

// TestRender_TheDisciplineIsTheWholeContent pins that the three constructors
// differ in exactly one field and in nothing else.
//
// It is the class's shape assertion and it is deliberately blunt: if a change
// adds a field, this test is where the addition has to be argued rather
// than absorbed.
func TestRender_TheDisciplineIsTheWholeContent(t *testing.T) {
	pos := ir.At(renderFile, 7, 11)
	dst, src := ir.Temp(2), ir.Temp(1)

	row := ir.NewRenderRow(pos, dst, src)
	disp := ir.NewRenderDisplay(pos, dst, src)
	dbg := ir.NewRenderDebug(pos, dst, src)

	for _, r := range []*ir.Render{row, disp, dbg} {
		if r.Pos() != pos {
			t.Errorf("%s: position is %s, want %s", r.Kind(), r.Pos(), pos)
		}
		if r.Dst() != dst {
			t.Errorf("%s: destination is %s, want %s", r.Kind(), r.Dst(), dst)
		}
		if r.Src() != src {
			t.Errorf("%s: source is %s, want %s", r.Kind(), r.Src(), src)
		}
		uses := r.AppendUses(nil)
		if len(uses) != 1 || uses[0] != src {
			t.Errorf("%s: uses %v, want exactly [%s]", r.Kind(), uses, src)
		}
	}

	// THREE DISTINCT KINDS. A constructor that copied its neighbour would
	// make two of these equal, and nothing in a program's output would notice
	// because both answer a String.
	kinds := map[ir.RenderKind]bool{row.Kind(): true, disp.Kind(): true, dbg.Kind(): true}
	if len(kinds) != 3 {
		t.Fatalf("three constructors produced %d distinct kinds: %s / %s / %s",
			len(kinds), row.Kind(), disp.Kind(), dbg.Kind())
	}

	// The rendered form names the discipline, so a diagnostic dump
	// distinguishes them.
	for _, want := range []struct {
		r    *ir.Render
		text string
	}{{row, "t2 = render row t1"}, {disp, "t2 = render display t1"}, {dbg, "t2 = render debug t1"}} {
		if got := want.r.String(); got != want.text {
			t.Errorf("String() is %q, want %q", got, want.text)
		}
	}
}

// TestRender_DispatchesSeparatesTheStructuralRenderingFromTheTwoDispatched is
// the predicate, and it is asserted over ALL THREE kinds so the answer cannot
// be a constant that happens to be right for the one a fixture reaches.
//
// The predicate is derived from the kind — the `Assert.Refuted` species — and
// this test is what makes the derivation checkable rather than a comment.
func TestRender_DispatchesSeparatesTheStructuralRenderingFromTheTwoDispatched(t *testing.T) {
	pos := ir.At(renderFile, 3, 3)
	cases := []struct {
		r    *ir.Render
		want bool
	}{
		{ir.NewRenderRow(pos, 2, 1), false},
		{ir.NewRenderDisplay(pos, 2, 1), true},
		{ir.NewRenderDebug(pos, 2, 1), true},
	}
	sawTrue, sawFalse := false, false
	for _, c := range cases {
		if got := c.r.Dispatches(); got != c.want {
			t.Errorf("%s: Dispatches() is %v, want %v", c.r.Kind(), got, c.want)
		}
		if c.want {
			sawTrue = true
		} else {
			sawFalse = true
		}
	}
	// PLANT BOTH POSITIVES. A predicate this test only ever saw one answer
	// for would pass against a constant, a field that carries no
	// information.
	if !sawTrue || !sawFalse {
		t.Fatal("the table does not exercise both answers, so the predicate is untested")
	}
}

// TestRender_TheChecksHavePower plants the accepting case beside each
// rejection, so a check that could never fire is visible.
func TestRender_TheChecksHavePower(t *testing.T) {
	pos := ir.At(renderFile, 5, 5)

	// ACCEPTED: a rendering with a source and a destination.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("a well-formed rendering panicked: %v", r)
			}
		}()
		_ = ir.NewRenderRow(pos, 2, 1)
	}()

	for _, c := range []struct {
		name string
		want string
		make func()
	}{
		{
			// A rendering reads a value. Without one there is nothing to
			// render and the consumer would spell `f()`.
			name: "no source",
			want: "reads a value and this one has none",
			make: func() { ir.NewRenderDisplay(pos, 2, ir.NoTemp) },
		},
		{
			// A rendering nobody reads is dead. `recordComparisonOperands`
			// is the one producer that would hit this, and irrender.go
			// routes it to a table QUERY instead.
			name: "no destination",
			want: "nobody reads is dead",
			make: func() { ir.NewRenderDebug(pos, ir.NoTemp, 1) },
		},
		{
			// Every node has a position and the zero Pos is not one.
			name: "no position",
			want: "a position",
			make: func() { ir.NewRenderRow(ir.Pos{}, 2, 1) },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s was accepted", c.name)
				}
				if msg, isStr := r.(string); !isStr || !strings.Contains(msg, c.want) {
					t.Fatalf("panicked with %v, want a message containing %q", r, c.want)
				}
			}()
			c.make()
		})
	}
}

// TestRender_AnUnknownKindRendersAsAQuestionRatherThanEmpty pins the String
// fallback, which is the shape every other kind enum in this package uses.
func TestRender_AnUnknownKindRendersAsAQuestionRatherThanEmpty(t *testing.T) {
	if got := ir.RenderKind(0).String(); got != "render?" {
		t.Errorf("the zero kind renders as %q, want %q", got, "render?")
	}
}
