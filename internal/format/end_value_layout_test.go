package format

import "testing"

// A value the grammar closes with `end` that spans lines moves below its
// binding's `=` or its field's `:`, one indent in, so its keyword and its
// `end` share a column (emitValueAfter, closedByEnd). Each case is formatted
// twice to the same text and keeps its meaning.
func TestFormat_EndClosedValueMovesUnderItsName(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"if",
			`fn f(game: Game, letter: String) -> Int
    misses = if String.contains(game.word, letter) then
        game.misses
    else
        game.misses + 1
    end
    misses
end
`,
			`fn f(game: Game, letter: String) -> Int
    misses =
        if String.contains(game.word, letter) then
            game.misses
        else
            game.misses + 1
        end

    misses
end
`,
		},
		{
			"case",
			`fn f(y: Option<Int>) -> Int
    base = case y
        Some(n) -> n
        None -> 0
    end
    base
end
`,
			`fn f(y: Option<Int>) -> Int
    base =
        case y
            Some(n) -> n
            None    -> 0
        end

    base
end
`,
		},
		{
			"lambda",
			`fn f() -> Int
    other = fn header: (String, String) ->
        header.first
    end
    0
end
`,
			`fn f() -> Int
    other =
        fn header: (String, String) ->
            header.first
        end

    0
end
`,
		},
		{
			"begin",
			`fn f() -> Int
    total = begin
        a = 1
        a + 1
    end
    total
end
`,
			`fn f() -> Int
    total =
        begin
            a = 1
            a + 1
        end

    total
end
`,
		},
		{
			"concurrent",
			`fn f() -> Int
    r = concurrent
        1
    end
    r
end
`,
			`fn f() -> Int
    r =
        concurrent
            1
        end

    r
end
`,
		},
		{
			"destructuring",
			`fn f(y: Option<Int>) -> Int
    (p, q) = case y
        Some(n) -> (n, n)
        None -> (0, 0)
    end
    p + q
end
`,
			`fn f(y: Option<Int>) -> Int
    (p, q) =
        case y
            Some(n) -> (n, n)
            None    -> (0, 0)
        end

    p + q
end
`,
		},
		{
			"once",
			`once table: Int = case 3
    3 -> 1
    _ -> 2
end
`,
			`once table: Int =
    case 3
        3 -> 1
        _ -> 2
    end
`,
		},
		{
			"struct literal field",
			`fn f(misses: Int) -> Box
    Box{
        v: if misses > 2 then
            1
        else
            misses + 2000000000000000000000000000000000000000000000000000000000000000000
        end,
        label: "x",
    }
end
`,
			`fn f(misses: Int) -> Box
    Box{
        v:
            if misses > 2 then
                1
            else
                misses + 2000000000000000000000000000000000000000000000000000000000000000000
            end,
        label: "x",
    }
end
`,
		},
		{
			"struct declaration default",
			`struct Game
    word: String
    misses: Int = case 3
        3 -> 1
        _ -> 2
    end
end
`,
			`struct Game
    word: String
    misses: Int =
        case 3
            3 -> 1
            _ -> 2
        end
end
`,
		},
		{
			"variant field default",
			`enum Shape
    Circle{radius: Int = case 3
        3 -> 1
        _ -> 2
    end}
    Square
end
`,
			`enum Shape
    Circle {
        radius: Int =
            case 3
                3 -> 1
                _ -> 2
            end,
    }
    Square
end
`,
		},
		{
			// A postfix mark after the `end` ends the same line.
			"postfix mark",
			`fn f(xs: List<Int>) -> Option<Int>
    first = case xs
        [] -> None
        [x, ..] -> Some(x)
    end?
    Some(first)
end
`,
			`fn f(xs: List<Int>) -> Option<Int>
    first =
        case xs
            []      -> None
            [x, ..] -> Some(x)
        end?

    Some(first)
end
`,
		},
		{
			// A pipeline whose head is a `case` moves under as a pipeline.
			"pipeline after end",
			`fn f(xs: List<Int>) -> String
    s = case xs
        [] -> 0
        _ -> 1
    end |> Int.to_string
    s
end
`,
			`fn f(xs: List<Int>) -> String
    s =
        case xs
            [] -> 0
            _  -> 1
        end
        |> Int.to_string

    s
end
`,
		},
		{
			// Too wide for the binding's line, the inline `if` moves below
			// it and stays on one line there, as an overflowing pipeline
			// does.
			"one line below",
			`fn f(is_dir: Bool) -> Option<Int>
    a_very_long_binding_name_that_takes_most_of_the_line_and_then_some = if is_dir then Some(1) else None
    a_very_long_binding_name_that_takes_most_of_the_line_and_then_some
end
`,
			`fn f(is_dir: Bool) -> Option<Int>
    a_very_long_binding_name_that_takes_most_of_the_line_and_then_some =
        if is_dir then Some(1) else None

    a_very_long_binding_name_that_takes_most_of_the_line_and_then_some
end
`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}

// A value that fits on its name's line stays there, and so does a value
// closed by a bracket however many lines it spans, and a lambda that is a
// call's argument, which no name binds.
func TestFormat_ValueOnItsNamesLineStays(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"one-line if", "fn f(is_dir: Bool) -> Kind\n    kind = if is_dir then Kind.Directory else Kind.File\n    kind\nend\n"},
		{"one-line lambda", "fn f() -> Int\n    inc = fn x -> x + 1\n    inc(1)\nend\n"},
		{"one-line field if", "fn f(a: Bool) -> Box\n    Box{v: if a then 1 else 2, label: \"x\"}\nend\n"},
		{"one-line default", "struct Game\n    misses: Int = if True then 1 else 2\nend\n"},
		{"struct literal", "fn f() -> NewJob\n    new_job = NewJob{\n        id:   1,\n        name: \"a\",\n    }\n\n    new_job\nend\n"},
		{"call", "fn f(files: Map<String, String>, path: String) -> Map<String, String>\n    files2 = Map.put(\n        files,\n        path,\n        \"a long value that keeps this call too wide for a single line of code\",\n    )\n\n    files2\nend\n"},
		{"list", "fn f() -> List<Int>\n    xs = [\n        1, 2,\n    ]\n\n    xs\nend\n"},
		{"lambda with a struct body", "fn f() -> Int\n    make = fn x -> Point{\n        x:,\n        y: 0,\n    }\n\n    0\nend\n"},
		{"lambda argument", "fn f(xs: Iter<Int>) -> Iter<Int>\n    ys = Iter.map(xs, fn x ->\n        x + 1\n    end)\n\n    ys\nend\n"},
		{"empty case", "fn f(y: Int) -> Int\n    x = case y end\n    x\nend\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.src)
		})
	}
}

// A comment between a name and its value, or around a value that moves,
// stays where it was: inside the pattern, above the field, after the `end`.
func TestFormat_CommentsAroundAValueThatMoves(t *testing.T) {
	src := `fn f(a: Bool, y: Option<Int>) -> Int
    (p, // first
    q) = case y
        Some(n) -> (n, n)
        None -> (0, 0)
    end
    b = Box{
        // the value
        v: if a then
            1
        else
            2000000000000000000000000000000000000000000000000000000000000000000000000
        end, // after the value
        label: "x",
    }
    w = case y
        Some(n) -> n
        None -> 0
    end // done
    p + q + b.v + w
end
`
	want := `fn f(a: Bool, y: Option<Int>) -> Int
    (
        p, // first
        q
    ) =
        case y
            Some(n) -> (n, n)
            None    -> (0, 0)
        end

    b = Box{
        // the value
        v:
            if a then
                1
            else
                2000000000000000000000000000000000000000000000000000000000000000000000000
            end, // after the value
        label: "x",
    }

    w =
        case y
            Some(n) -> n
            None    -> 0
        end // done

    p + q + b.v + w
end
`
	formatsKeepingMeaning(t, src, want)
}

// A binding `else` keeps its value on the binding's line, and the other
// places a value follows code (a named argument, a map entry, `return`, a
// parameter's default, `assert`) keep theirs.
func TestFormat_OtherValuePositionsKeepTheirLayout(t *testing.T) {
	src := `fn f(y: Option<Int>) -> Int
    Some(n) = case y
        Some(n) -> Some(n)
        None    -> None
    end else
        return 0
    end

    m = {
        "a" => case y
            Some(k) -> k
            None    -> 0
        end,
    }

    g(
        value: case y
            Some(k) -> k
            None    -> 0
        end
    )

    assert [a] = case y
        Some(k) -> [k]
        None    -> [0]
    end

    if n > 1 then
        return case y
            Some(k) -> k
            None    -> 0
        end
    end

    n + a
end
`
	formatsKeepingMeaning(t, src, src)
}
