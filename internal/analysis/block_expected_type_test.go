package analysis

import "testing"

// The type a block's position expects reaches its tail and no statement
// before it. A binding there must be locally determined (§2), as it must be
// in a function body: `b = .Circle(2)` names no enum on its own line, so it
// is rejected inside a `once`'s, a binding's or an argument's block that
// expects a Shape, as it is in `fn f() -> Shape { b = .Circle(2); b }`.
func TestBlockExpectedType_DoesNotReachABindingBeforeTheTail(t *testing.T) {
	const decls = `enum Shape {
    Circle Int
    Square Int
}

fn take(s: Shape) -> Shape {
    s
}

`
	cases := map[string]string{
		"once": `once o: Shape = {
    b = .Circle(2)
    b
}
`,
		"annotated binding": `fn f() -> Int {
    s: Shape = {
        b = .Circle(2)
        b
    }
    1
}
`,
		"argument": `fn f() -> Shape {
    take({
        b = .Circle(2)
        b
    })
}
`,
		"function body": `fn f() -> Shape {
    b = .Circle(2)
    b
}
`,
		"case arm": `fn f(n: Int) -> Shape {
    case n {
        0 -> {
            b = .Circle(2)
            b
        }
        _ -> .Square(n)
    }
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(decls + src)
			expectError(t, errs, ".Circle requires a determinable enum type at this position; annotate the binding or qualify the variant")
		})
	}
}

// The tail still has the block's expected type, and an annotated binding
// before it gives its own.
func TestBlockExpectedType_ReachesTheTail(t *testing.T) {
	src := `enum Shape {
    Circle Int
    Square Int
}

once o: Shape = {
    n = 2
    .Circle(n)
}

once p: Shape = {
    b: Shape = .Square(1)
    b
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}
