package analysis

import "testing"

// The type a block's position expects reaches its tail and no statement
// before it. A binding there must be locally determined (§5 *Locally-determined bindings and turbofish*), as it must be
// in a function body: `b = .Circle(2)` names no enum on its own line, so it
// is rejected inside a `once`'s, a binding's or an argument's block that
// expects a Shape, as it is in a
// function body that returns a Shape.
func TestBlockExpectedType_DoesNotReachABindingBeforeTheTail(t *testing.T) {
	const decls = `enum Shape
    Circle Int
    Square Int
end

fn take(s: Shape) -> Shape
    s
end

`
	cases := map[string]string{
		"once": `once o: Shape = begin
    b = .Circle(2)
    b
end
`,
		"annotated binding": `fn f() -> Int
    s: Shape = begin
        b = .Circle(2)
        b
    end
    1
end
`,
		"argument": `fn f() -> Shape
    take(begin
        b = .Circle(2)
        b
    end)
end
`,
		"function body": `fn f() -> Shape
    b = .Circle(2)
    b
end
`,
		"case arm": `fn f(n: Int) -> Shape
    case n
        0 ->
            b = .Circle(2)
            b
        _ -> .Square(n)
    end
end
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
	src := `enum Shape
    Circle Int
    Square Int
end

once o: Shape = begin
    n = 2
    .Circle(n)
end

once p: Shape = begin
    b: Shape = .Square(1)
    b
end
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}
