package analysis

import (
	"strings"
	"testing"
)

// Two interfaces whose unoverridden defaults share a name both want the
// file scope's slot for it (it is what lets a bare `describe(x)` be reported
// as bare-name dispatch naming its interface). The default of the interface
// whose impl block comes first in the file holds it, in every build: the
// registration once ranged over a map of impls and let the last write win,
// so two builds of one file disagreed about the slot about half the time
// (FuzzIncrementalMatchesFull found it as a recheck that differed from a
// full check). The impls here come in the other order from the interfaces,
// so neither declaration order nor name order can pass for source order of
// the impls.
func TestImplDefaultsTakeTheScopeSlotInImplOrder(t *testing.T) {
	src := `interface Alpha {
  fn name(value: self) -> String

  fn describe(value: self) -> String {
    "alpha " + Alpha.name(value)
  }
}

interface Beta {
  fn label(value: self) -> String

  fn describe(value: self) -> String {
    "beta " + Beta.label(value)
  }
}

struct Robot {
  model: String
}

struct User {
  handle: String
}

impl Beta for User {
  fn label(u: User) -> String {
    u.handle
  }
}

impl Alpha for Robot {
  fn name(r: Robot) -> String {
    r.model
  }
}
`
	betaDescribe := Pos{Line: 12, Col: 6}
	if line := strings.Split(src, "\n")[betaDescribe.Line-1]; !strings.Contains(line, "fn describe") {
		t.Fatalf("the fixture moved: line %d is %q, not Beta's describe", betaDescribe.Line, line)
	}
	for i := 0; i < 40; i++ {
		fa, errs := buildAndCheckFromSource(src)
		if len(errs) != 0 {
			t.Fatalf("the fixture does not check cleanly:\n  %s", joinErrs(errs))
		}
		sym := fa.ModuleScope.LookupLocal("describe")
		if sym == nil {
			t.Fatal("the file scope holds no `describe`; a bare call could not name its interface")
		}
		if sym.Kind != SymbolInterfaceMethod || sym.OwningInterface != "Beta" || sym.Pos != betaDescribe {
			t.Fatalf("build %d: the `describe` slot holds %s's function at %d:%d, want Beta's at %d:%d (the first impl block's interface)",
				i, sym.OwningInterface, sym.Pos.Line, sym.Pos.Col, betaDescribe.Line, betaDescribe.Col)
		}
	}
}

// An interface default is the weakest occupant of its name's slot: a file
// function of that name keeps it, so a bare call to the function is an
// ordinary call. The default once overwrote the function's entry, and the
// call was rejected as bare-name dispatch to the interface.
func TestImplDefaultLeavesAFileFunctionOfItsNameInScope(t *testing.T) {
	src := `interface Named {
  fn name(value: self) -> String

  fn describe(value: self) -> String {
    "I am " + Named.name(value)
  }
}

struct Robot {
  model: String
}

impl Named for Robot {
  fn name(r: Robot) -> String {
    r.model
  }
}

fn describe(n: Int) -> String {
  "number ${n}"
}

fn both(r: Robot) -> String {
  describe(3) + Robot.describe(r)
}
`
	fa, errs := buildAndCheckFromSource(src)
	if len(errs) != 0 {
		t.Fatalf("a bare call to the file function `describe` is rejected:\n  %s", joinErrs(errs))
	}
	if sym := fa.ModuleScope.LookupLocal("describe"); sym == nil || sym.Kind != SymbolFunction {
		t.Fatalf("the `describe` slot holds %v, want the file function", sym)
	}
}
