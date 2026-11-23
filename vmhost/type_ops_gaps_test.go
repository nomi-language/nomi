package vmhost

import (
	"regexp"
	"testing"
)

// typeOpGap is a failure of the type × operation grid (type_ops_test.go):
// the cells of one cause, and a minimal reproducer of it.
type typeOpGap struct {
	name string
	// kind is what the cells and the reproducer do: "declines" (the
	// builder declines), "panics", "untyped", "crash", "wrong output" (the
	// run traps or prints the wrong thing), "should reject" (the checker
	// accepts a program it should reject: the reproducer is accepted, and
	// the cells then fail however they do), or "should accept" (the checker
	// rejects a program it should accept).
	kind string
	// fix says where the fix belongs.
	fix string
	// cells matches the names of the cells the gap excuses.
	cells *regexp.Regexp
	// detail matches the outcome's detail, in the cells and the
	// reproducer: for a decline, the builder's reason.
	detail *regexp.Regexp
	// src is the reproducer: a program with a `fn main` that runs, its
	// asserts holding, once the gap is fixed; for "should reject", one the
	// checker should reject.
	src string
}

// knownTypeOpGaps are the grid's failures. A fix removes its entry in the
// same change.
var knownTypeOpGaps = []typeOpGap{
	{
		name: "a function returning an Option of an Option widened to one returning Display",
		kind: "declines",
		fix: "internal/irbuild: a function value whose result is an Option<Option<Int>> enters a `() -> Display` " +
			"position through an adapter, as one returning an Option<Int> does",
		cells:  regexp.MustCompile(`^option option × into a function returning Display$`),
		detail: regexp.MustCompile(`a value of kind \(\) -> Option<Option<Int>> where \(\) -> Display is expected`),
		src: `fn main()
    v = Some(Some(1))
    f = fn -> v
    g: () -> Display = f
    dbg "${g()}"
end
`,
	},
	{
		name: "in over a container of functions",
		kind: "should reject",
		fix: "the checker: List's `T: Equatable` at a List or Map that holds a function is met at the impl match " +
			"(typeSatisfiesInterfaceForImplMatch), where `==` and Iter.contains refuse it (noEqualityPartWith)",
		cells:  regexp.MustCompile(`^(list fn|map fn value) × in$`),
		detail: regexp.MustCompile(`Contains\.contains`),
		src: `fn inc(n: Int) -> Int
    n + 1
end

fn main()
    v: List<(Int) -> Int> = [inc]
    dbg v in [[inc]]
end
`,
	},
	{
		name: "in over a List of interface conjunction values",
		kind: "should accept",
		fix: "the checker: a conjunction value meets List's `T: Equatable` as `==` and Iter.contains compare it; " +
			"typeSatisfiesInterfaceForImplMatch has an arm for an interface value and none for a conjunction",
		cells:  regexp.MustCompile(`^interface conjunction value × in$`),
		detail: regexp.MustCompile("List<Named and Tagged> does not implement Contains"),
		src: `interface Named
    fn name(value: self) -> String
end

interface Tagged
    fn tag(value: self) -> String
end

struct Pt
    x: Int
end

impl Named for Pt
    fn name(p: Pt) -> String
        "pt${p.x}"
    end
end

impl Tagged for Pt
    fn tag(p: Pt) -> String
        "t${p.x}"
    end
end

fn main()
    v: Named and Tagged = (Pt{x: 1} as (Named and Tagged))
    dbg v in [v]
end
`,
	},
}

// knownTypeOpGapFor is the gap that excuses c's outcome o, or nil.
func knownTypeOpGapFor(c toCell, o mxOutcome) *typeOpGap {
	for i := range knownTypeOpGaps {
		g := &knownTypeOpGaps[i]
		kind := g.kind
		switch kind {
		case "should reject":
			// The checker accepts it, so the builder or the run fails it
			// however it does.
			kind = o.class
		case "should accept":
			kind = "unexpected rejection"
		}
		if kind == o.class && g.cells.MatchString(c.name()) && g.detail.MatchString(o.detail) {
			return g
		}
	}
	return nil
}

// typeOpOutcome checks and runs src with no expectation of it: "rejected"
// with the checker's text, a lowering failure, a crash, "wrong output" for
// a run that traps, or "ok".
func typeOpOutcome(dir, src string) mxOutcome {
	var p *Program
	lo := checkLowersKeeping(dir, src, &p)
	if !lo.accepted {
		return mxOutcome{class: "rejected", detail: lo.rejection}
	}
	if o, done := mxCheckOutcome(lo, "", "", ""); done {
		return o
	}
	plan := mxPlan(p)
	if plan.main == nil {
		return mxOutcome{class: "crash", detail: "nothing ran: main is not retained"}
	}
	if o, done := mxRunFailure(runOnce(p, plan, dir, nil)); done {
		return o
	}
	return mxOutcome{class: "ok"}
}

// TestKnownTypeOpGaps holds each knownTypeOpGaps reproducer to its kind:
// it fails the way the gap says, with a detail the gap matches. When one
// stops failing that way, the gap is fixed: remove the entry.
func TestKnownTypeOpGaps(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	for _, g := range knownTypeOpGaps {
		t.Run(g.name, func(t *testing.T) {
			o := typeOpOutcome(t.TempDir(), g.src)
			switch g.kind {
			case "should reject":
				if o.class == "rejected" {
					t.Fatalf("the checker rejects the reproducer of %s now; if that is the fix (%s), remove the entry:\n%s\n%s",
						g.name, g.fix, numbered(g.src), o.detail)
				}
				return
			case "should accept":
				if o.class != "rejected" || !g.detail.MatchString(o.detail) {
					t.Fatalf("the checker no longer rejects the reproducer of %s with %s; if that is the fix (%s), remove the entry:\n%s\n%s",
						g.name, g.detail, g.fix, numbered(g.src), o.detail)
				}
				return
			}
			if o.class != g.kind || !g.detail.MatchString(o.detail) {
				t.Fatalf("the reproducer of %s is %s now, not %s matching %s; if it is fixed (%s), remove the entry:\n%s\n%s",
					g.name, o.class, g.kind, g.detail, g.fix, numbered(g.src), o.detail)
			}
		})
	}
}
