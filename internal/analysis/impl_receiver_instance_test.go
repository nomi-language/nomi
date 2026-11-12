package analysis_test

import (
	"strings"
	"testing"
)

// An interface implemented at one instance of a generic type (`impl Opt for
// Maybe<String>`) is implemented at that instance only: another instance is
// refused, and a bare `None`, whose element type nothing else names, is the
// instance that implements it.
const optAtOneInstance = `interface Opt
    fn attribute(value: self) -> Maybe<String>
end

impl Opt for Maybe<String>
    fn attribute(value: Maybe<String>) -> Maybe<String>
        value
    end
end

fn show(value: Opt) -> String
    case Opt.attribute(value)
        None -> "none"
        Some(text) -> text
    end
end
`

func TestImplAtOneInstance_AnotherInstanceIsRefused(t *testing.T) {
	_, errs := checkSourceWithStdlib(optAtOneInstance + "\nfn main()\n    _ = show(Some(1))\nend\n")
	if len(errs) == 0 {
		t.Fatal("the checker admits a Maybe<Int> where only Maybe<String> implements Opt")
	}
	if !strings.Contains(errs[0].Message, "expected Opt, got Maybe<Int>") {
		t.Fatalf("rejected for another reason: %v", errs)
	}
}

func TestImplAtOneInstance_BareNoneIsThatInstance(t *testing.T) {
	fa, errs := checkSourceWithStdlib(optAtOneInstance + "\nfn main()\n    _ = show(None)\nend\n")
	if len(errs) > 0 {
		t.Fatalf("the checker rejects a bare None where Maybe<String> implements Opt: %v", errs)
	}
	found := false
	for n, ty := range fa.ExprTypes {
		if n.NodeType() == "TypeIdent" && strings.HasPrefix(ty.String(), "Maybe<") {
			found = true
			if ty.String() != "Maybe<String>" {
				t.Fatalf("None is typed %s, want Maybe<String>", ty)
			}
		}
	}
	if !found {
		t.Fatal("no Maybe-typed None recorded")
	}
}
