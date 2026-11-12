package analysis

import "testing"

// A `fn` declared in a struct's or a record variant's field default gets its
// signature as one in a function body does, so its body and its call are
// checked and typed. Without the signature checkNestedFunc skipped the body,
// and expressions in it, and a call of it, had no type.
func TestFieldDefault_NestedFnIsChecked(t *testing.T) {
	src := `struct D
    v: (Int) -> Int = begin
        fn inner(n: Int) -> Int
            n * 2
        end
        inner
    end
end

enum Job
    Run {label: Int = begin
        fn pick() -> Int
            case 1
                1 -> 2
                _ -> 3
            end
        end
        pick()
    end}
    Idle
end

fn make() -> D
    D{}
end
`
	fa, nodes := checkNodes(t, src)
	if got := UnresolvedExprs(fa, nodes); len(got) > 0 {
		for _, u := range got {
			t.Errorf("%d:%d: %s %s has %s", u.Line, u.Col, u.Node.NodeType(), u.Where, u.Missing)
		}
	}
}

// The nested fn's body is checked, so an error in it is reported.
func TestFieldDefault_NestedFnBodyErrorIsReported(t *testing.T) {
	src := `struct D
    v: Int = begin
        fn inner() -> Int
            "nope"
        end
        inner()
    end
end
`
	_, errs := checkSource(src)
	expectError(t, errs, "return type mismatch: expected Int, got String")
}
