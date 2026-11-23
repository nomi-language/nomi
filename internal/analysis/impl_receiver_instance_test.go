package analysis_test

import (
	"strings"
	"testing"
)

// An interface implemented at one instance of a generic type (`impl Opt for
// Option<String>`) is implemented at that instance only: another instance is
// refused, and a bare `None`, whose element type nothing else names, is the
// instance that implements it.
const optAtOneInstance = `interface Opt
    fn attribute(value: self) -> Option<String>
end

impl Opt for Option<String>
    fn attribute(value: Option<String>) -> Option<String>
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
		t.Fatal("the checker admits an Option<Int> where only Option<String> implements Opt")
	}
	if !strings.Contains(errs[0].Message, "expected Opt, got Option<Int>") {
		t.Fatalf("rejected for another reason: %v", errs)
	}
}

func TestImplAtOneInstance_BareNoneIsThatInstance(t *testing.T) {
	fa, errs := checkSourceWithStdlib(optAtOneInstance + "\nfn main()\n    _ = show(None)\nend\n")
	if len(errs) > 0 {
		t.Fatalf("the checker rejects a bare None where Option<String> implements Opt: %v", errs)
	}
	found := false
	for n, ty := range fa.ExprTypes {
		if n.NodeType() == "TypeIdent" && strings.HasPrefix(ty.String(), "Option<") {
			found = true
			if ty.String() != "Option<String>" {
				t.Fatalf("None is typed %s, want Option<String>", ty)
			}
		}
	}
	if !found {
		t.Fatal("no Option-typed None recorded")
	}
}
