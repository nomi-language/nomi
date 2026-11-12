package analysis

import "testing"

// With nothing expected of a `case` or an `if`, an arm or branch after one
// whose type names an enum is checked against that type, as an element of
// a collection literal is checked against the elements before it: the
// first qualified variant sets the enum for the bare ones after it.
func TestEarlierArms_SetTheEnumForBareVariantsAfterThem(t *testing.T) {
	const decls = `enum Format
    Portrait
    Square
    Landscape
end

`
	for name, body := range map[string]string{
		"case": `fn pick(n: Int) -> Format
    format = case n
        0 -> Format.Portrait
        1 -> .Square
        _ -> .Landscape
    end
    format
end
`,
		"case after a diverging arm": `fn pick(n: Int) -> Format
    format = case n
        0 -> todo
        1 -> Format.Portrait
        _ -> .Landscape
    end
    format
end
`,
		"if": `fn pick(b: Bool) -> Format
    format = if b then Format.Portrait else .Square
    format
end
`,
		"else if": `fn pick(n: Int) -> Format
    format = if n == 0 then
        Format.Portrait
    else if n == 1 then
        .Square
    else
        .Landscape
    end
    format
end
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(decls + body)
			expectNoErrors(t, errs)
		})
	}
}

// Resolution runs from the first arm on, as in a literal: a bare variant
// before every qualified one has nothing to resolve against.
func TestEarlierArms_DoNotReachBack(t *testing.T) {
	const src = `enum Format
    Portrait
    Square
end

fn pick(n: Int) -> Format
    format = case n
        0 -> .Square
        _ -> Format.Portrait
    end
    format
end
`
	_, errs := checkSource(src)
	expectError(t, errs, ".Square requires a determinable enum type at this position; annotate the binding or qualify the variant")
}
