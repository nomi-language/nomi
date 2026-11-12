package analysis

import "testing"

// A `fn` declared in an impl's function, in a parameter's default or in an
// interface's default gets its signature as one in a module-level
// function's body does, so its body and its call are checked and typed.
// Without the signature checkNestedFunc skipped the body, and expressions
// in it, and a call of it, had no type. The interface defaults bind a name
// first: a `fn name(` on the line after a signature starts the next item
// (spec §12 *Item kinds*), so a default body cannot open with one.
func TestDeclNestedFn_IsChecked(t *testing.T) {
	src := `fn far(k: Int, a: Int = begin
    fn inner() -> Int
        j = 1
        j + 1
    end
    inner()
end) -> Int
    _ = k
    a
end

struct Mk
    v: Int
end

impl Mk
    pub fn make(_o: Mk) -> Int
        fn inner() -> Int
            j = 1
            j + 1
        end
        inner()
    end
end

interface Make
    fn tag(x: self) -> Int

    fn make(_x: self) -> Int
        base = 0
        fn inner() -> Int
            j = 1
            j + 1
        end
        base + inner()
    end
end

impl Make for Mk
    fn tag(m: Mk) -> Int
        m.v
    end
end

fn run() -> Int
    far(0) + Mk.make(Mk{v: 0}) + Make.make(Mk{v: 0})
end
`
	fa, nodes := checkNodes(t, src)
	for _, u := range UnresolvedExprs(fa, nodes) {
		t.Errorf("%d:%d: %s %s has %s", u.Line, u.Col, u.Node.NodeType(), u.Where, u.Missing)
	}
}

// The nested fn's body is checked, so an error in it is reported.
func TestDeclNestedFn_BodyErrorIsReported(t *testing.T) {
	for _, src := range []string{
		"struct Mk\n    v: Int\nend\n\nimpl Mk\n    pub fn make(_o: Mk) -> Int\n        fn inner() -> Int\n            \"nope\"\n        end\n        inner()\n    end\nend\n",
		"fn far(a: Int = begin\n    fn inner() -> Int\n        \"nope\"\n    end\n    inner()\nend) -> Int\n    a\nend\n",
		"interface Make\n    fn make(_x: self) -> Int\n        base = 0\n        fn inner() -> Int\n            \"nope\"\n        end\n        base + inner()\n    end\nend\n",
	} {
		_, errs := checkSource(src)
		expectError(t, errs, "return type mismatch: expected Int, got String")
	}
}
