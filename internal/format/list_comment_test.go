package format

import (
	"testing"
)

// listCommentCases put a comment in every comma-separated list the grammar
// has: on its own line before the first item, between items, after the last
// item, and at the end of a line after a comma. Each source is the layout
// nomi fmt writes for it: one item per line, an own-line comment above its
// item, an end-of-line comment after its item's comma, a comment after the
// last item on its own line before the closing bracket. A list whose syntax
// has no trailing comma (a lambda's parameters, a type's arguments, a tuple
// pattern, a variant's payload, a list ending in a spread) ends its last
// item without one.
var listCommentCases = []struct {
	name string
	src  string
}{
	{"call positional", `fn main() {
    x = add(
        // the first
        1,
        2, // the second
        // after the last
    )
}
`},
	{"call named", `fn main() {
    x = connect(
        // the host
        host: "a",
        port: 1, // the port
    )
}
`},
	{"call mixed", `fn main() {
    x = connect(
        "a", // the host
        // the port next
        port: 1,
    )
}
`},
	{"call trailing lambda", `fn main() {
    x = run(
        // the first
        1, // the end
        |y| y + 1,
    )
}
`},
	{"list", `fn main() {
    x = [
        // the first
        1,
        2, // the second
        // the end
    ]
}
`},
	{"list spread", `fn main() {
    x = [
        1, // the first
        ..rest // the rest
    ]
}
`},
	{"set", `fn main() {
    x = #{
        // the first
        1,
        2, // the second
        // the end
    }
}
`},
	{"vector", `fn main() {
    x = #[
        // the first
        1,
        2, // the second
        // the end
    ]
}
`},
	{"map", `fn main() {
    x = {
        // the first
        "a" => 1,
        "b" => 2, // the second
        // the end
    }
}
`},
	{"struct", `fn main() {
    x = P{
        // the first
        a: 1,
        b: 2, // the second
        // the end
    }
}
`},
	{"anonymous struct", `fn main() {
    x = {
        a: 1, // the first
        b, // the second
    }
}
`},
	{"tuple", `fn main() {
    x = (
        // the first
        1,
        2, // the second
        // the end
    )
}
`},
	{"lambda params", `fn main() {
    f = |
        // the first
        a, // the first's
        b // the second
        // the end
    | a + b
}
`},
	{"fn params", `fn add(
    // the first
    a: Int,
    b: Int, // the second
    // the end
): Int {
    a + b
}
`},
	{"interface fn params", `interface Adder {
    fn add(
        a: self, // the first
        b: self,
    ): self
}
`},
	{"host fn params", `host fn add(
    a: Int, // the first
    b: Int,
): Int
`},
	{"type args", `fn main() {
    x: Map<
        // the key
        String,
        Int // the value
    > = {}
}
`},
	{"generic params", `fn pick<
    // the first
    A,
    B // the second
>(a: A, b: B): A {
    a
}
`},
	{"type decl params", `struct Box<
    // the first
    A,
    B // the second
> {
    a: A
}
`},
	{"list pattern", `fn main() {
    y = case xs {
        [
            // the first
            a,
            b, // the second
        ] -> a
        _ -> 0
    }
}
`},
	{"list pattern spread", `fn main() {
    y = case xs {
        [
            a, // the first
            ..rest
        ] -> a
        _ -> 0
    }
}
`},
	{"tuple pattern", `fn main() {
    y = case xs {
        (
            // the first
            a, // the first's
            b // the second
        ) -> a
    }
}
`},
	{"map pattern", `fn main() {
    y = case xs {
        {
            // the first
            "a" => a,
            "b" => b, // the second
        } -> a
        _ -> 0
    }
}
`},
	{"struct pattern", `fn main() {
    y = case xs {
        P{
            // the first
            a,
            b, // the second
        } -> a
    }
}
`},
	{"anonymous struct pattern", `fn main() {
    y = case xs {
        {
            a, // the first
            b: c,
            // the end
        } -> a
    }
}
`},
	{"variant payload pattern", `fn main() {
    y = case xs {
        .Pair(
            // the first
            a,
            // the second
            b
        ) -> a
    }
}
`},
	{"enum payloads", `enum E {
    Pair (
        // the first
        Int,
        Int // the second
    )
    Rec {
        // the first
        a: Int,
        b: Int, // the second
    }
}
`},
	{"partial application", `fn main() {
    f = add(
        // the first
        1,
        _, // the open one
    )
}
`},
	{"fn type", `fn main() {
    f: (
        // the first
        Int,
        Int // the second
    ) -> Int = add
}
`},
	{"tuple type", `fn main() {
    f: (
        // the first
        Int,
        Int // the second
    ) = (1, 2)
}
`},
	{"tuple destructure", `fn main() {
    (
        // the first
        a, // the first's
        _,
        b // the second
    ) = triple
}
`},
	{"struct destructure", `fn main() {
    {
        // the first
        a,
        b: c, // the second
    } = s
}
`},
	{"map destructure", `fn main() {
    {
        "a" => a, // the first
        // the second
        "b" => b,
    } = m
}
`},
	{"list destructure", `fn main() {
    [
        // the first
        a,
        ..rest // the rest
    ] = xs
}
`},
	{"case subject tuple", `fn main() {
    x = case (
        a, // the first
        b,
    ) {
        _ -> 0
    }
}
`},
	{"variant constructor", `fn main() {
    x = .Pair(
        1, // the first
        2,
    )
}
`},
	{"type-prefixed literals", `fn main() {
    x = Kvs{
        "a" => 1, // the first
    }

    y = Arr[
        1, // the first
    ]
}
`},
	{"turbofish", `fn main() {
    x = f<
        // the first
        Int,
        Int // the second
    >(1)
}
`},
}

// TestListComment_KeepsEachCommentWhereItWas formats each case, which must
// come back unchanged, keep its meaning, and format again to itself.
func TestListComment_KeepsEachCommentWhereItWas(t *testing.T) {
	for _, c := range listCommentCases {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.src)
		})
	}
}

// A comment in a list written on one line, or written with its items
// sharing lines, breaks the list one item per line.
func TestListComment_BreaksAListWrittenFlat(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"x = add(1, // the first\n    2)\n",
			"x = add(\n    1, // the first\n    2,\n)\n",
		},
		{
			"x = add( // after the paren\n    1, 2)\n",
			"x = add(\n    // after the paren\n    1,\n    2,\n)\n",
		},
		{
			"x = [1, 2 // the second\n]\n",
			"x = [\n    1,\n    2, // the second\n]\n",
		},
		{
			"x = f(a: 1 // the first\n    , b: 2)\n",
			"x = f(\n    a: 1, // the first\n    b: 2,\n)\n",
		},
		{
			"x = (1, 2 // the second\n)\n",
			"x = (\n    1,\n    2, // the second\n)\n",
		},
		{
			// A comment after a lambda parameter's comma keeps the
			// header's own layout (TestFormat_LambdaParameterCommentStaysAfterItsComma).
			"f = |a, // the first\n    b| a\n",
			"f = |a, // the first\n    b|\n    a\n",
		},
		{
			"f = |a, b // the second\n| a\n",
			"f = |\n    a,\n    b // the second\n| a\n",
		},
		{
			"x = A{a: 1, // the first\n    b: 2}\n",
			"x = A{\n    a: 1, // the first\n    b: 2,\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

// An import's selector list keeps its comments too, through the sorting and
// merging nomi fmt does to imports.
func TestListComment_ImportSelector(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"import std/comparable.{\n    // the first\n    Comparable,\n    Ordering, // the second\n}\n",
			"import std/comparable.{\n    // the first\n    Comparable,\n    Ordering, // the second\n}\n",
		},
		{
			"import std/comparable.{Ordering, // the second\n    Comparable}\n",
			"import std/comparable.{\n    Comparable,\n    Ordering, // the second\n}\n",
		},
		{
			"import std/maybe.Maybe.{\n    self,\n    // the variant\n    Some,\n}\n",
			"import std/maybe.Maybe.{\n    self,\n    // the variant\n    Some,\n}\n",
		},
		{
			"import std/comparable.{\n    Comparable, // the first\n}\nimport std/comparable.Ordering\n",
			"import std/comparable.{\n    Comparable, // the first\n    Ordering,\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

// A comment after a list's opening bracket, on its line, goes above the
// first item, and one before the closing bracket on the last item's line
// ends that item's line.
func TestListComment_AtTheBrackets(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"after a parameter list's (",
			"fn double( // why\n    n: Int,\n): Int {\n    n * 2\n}\n",
			"fn double(\n    // why\n    n: Int,\n): Int {\n    n * 2\n}\n",
		},
		{
			"inside a map pattern",
			"fn f(m: Map<String, Int>): Int {\n    case m {\n        { // the key\n            \"a\" => n,\n        } -> n\n        _ -> 0\n    }\n}\n",
			"fn f(m: Map<String, Int>): Int {\n    case m {\n        {\n            // the key\n            \"a\" => n,\n        } -> n\n        _ -> 0\n    }\n}\n",
		},
		{
			"closing a spread list",
			"fn main() {\n    list = [1, ..[] // why\n    ]\n}\n",
			"fn main() {\n    list = [\n        1,\n        ..[] // why\n    ]\n}\n",
		},
		{
			"closing a tuple",
			"fn main() {\n    t = (1, 2 // why\n    )\n    _ = t\n}\n",
			"fn main() {\n    t = (\n        1,\n        2, // why\n    )\n\n    _ = t\n}\n",
		},
		{
			"an interface function's parameters",
			"interface Greeter {\n  fn greet( // c1\nvalue: self): String // t2\n}\n",
			"interface Greeter {\n    fn greet(\n        // c1\n        value: self,\n    ): String // t2\n}\n",
		},
		{
			"a map pattern in a variant pattern",
			"fn f(j: Maybe<Map<String, Int>>): Int {\n    case j {\n        Some({ // t10\n            \"a_rather_long_key_rather_long_key_rather_long_key\" => a,\n            \"b_rather_long_key_rather_long_key_rather_long_key\" => b,\n        }) -> a + b\n        _ -> 0\n    }\n}\n",
			"fn f(j: Maybe<Map<String, Int>>): Int {\n    case j {\n        Some({\n            // t10\n            \"a_rather_long_key_rather_long_key_rather_long_key\" => a,\n            \"b_rather_long_key_rather_long_key_rather_long_key\" => b,\n        }) -> a + b\n        _ -> 0\n    }\n}\n",
		},
		{
			// The parentheses stay for the comment, which they hold.
			"inside grouping parentheses",
			"fn f(): Int {\n    y = try ( // why\n        g())\n    y\n}\n",
			"fn f(): Int {\n    y = try (\n        // why\n        g()\n    )\n\n    y\n}\n",
		},
		{
			"a struct pattern in a case arm",
			"fn area(s: Shape): Int {\n    case s {\n        Shape.Rect{ // c2\n            width_rather_long_rather_long_rather_long_rather_long_rather_long, height} -> width_rather_long_rather_long_rather_long_rather_long_rather_long * height\n        Shape.Point -> 0\n    }\n}\n",
			"fn area(s: Shape): Int {\n    case s {\n        Shape.Rect{\n            // c2\n            width_rather_long_rather_long_rather_long_rather_long_rather_long,\n            height,\n        } -> width_rather_long_rather_long_rather_long_rather_long_rather_long * height\n        Shape.Point -> 0\n    }\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}

// Layouts FuzzFormatKeepsMeaning's comment pass wrote, which the formatter
// wrote back losing a comment or differently on a second format: comments
// in empty parentheses, in nested grouping parentheses, before a tuple
// destructure's `_`, above a single argument, and stacked after a lambda
// parameter's comma.
func TestListComment_KeepsMeaningOnFoundLayouts(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    x = ( // a\n        ( // b\n            1 + 2))\n}\n",
			"fn main() {\n    x = (\n        // a\n        (\n            // b\n            1 + 2\n        )\n    )\n}\n",
		},
		{
			"fn main() {\n    x = Map.empty<String, Int>(\n// b4\n)\n    |> Map.size( // a5\n\t\t)\n    |> dbg // t6\n}\n",
			"fn main() {\n    x =\n        Map.empty<String, Int>(\n            // b4\n        )\n        |> Map.size(\n            // a5\n        )\n        |> dbg // t6\n}\n",
		},
		{
			"fn long_sleep( // b12\n     ): Result<Int, // a13\n\t\tString> { // t14\n    1\n}\n",
			"fn long_sleep(\n    // b12\n): Result<\n    Int, // a13\n    String\n> {\n    // t14\n    1\n}\n",
		},
		{
			"fn main() {\n    (sized,\n// b12\n_) = pair\n}\n",
			"fn main() {\n    (\n        sized,\n        // b12\n        _\n    ) = pair\n}\n",
		},
		{
			"fn main() {\n    io.print(\n        ( // a4\n   fetch.waits())\n        |> Iter.to_list()\n    )\n}\n",
			"fn main() {\n    io.print(\n        (\n            // a4\n            fetch.waits()\n        )\n        |> Iter.to_list()\n    )\n}\n",
		},
		{
			"fn main() {\n    f = |acc1 = g(1), // a79\n        // a18\n        // b19\n        e2| e2\n}\n",
			"fn main() {\n    f = |\n        acc1 = g(1), // a79\n        // a18\n        // b19\n        e2\n    | e2\n}\n",
		},
		{
			// A comment after a map destructure's `{` used to move below
			// the statement, leaving the file without its last newline
			// (vmhost's gapMapPatternOpeningCommentEndsFileWithoutNewline).
			"x = 1\n\n{ // t4\n \"a\" => y} = m // end\n",
			"x = 1\n\n{\n    // t4\n    \"a\" => y,\n} = m // end\n",
		},
		{
			// A comment the last statement could not end goes below it as
			// the file's last line, which ends with a line break.
			"Ok(z) = f() else { return } // x\n\n(Some(n) // a2\n) = (None) else { 0 } // y\n",
			"Ok(z) = f() else { return } // x\n\nSome(n) = None else { 0 } // y\n// a2\n",
		},
		{
			// A comment in parentheses around a variant's payload stays
			// in the variant's parentheses.
			"Ok(z) = f() else { return } // x\n\nSome(( // a2\n\tn)) = (None) else { 0 } // y\n",
			"Ok(z) = f() else { return } // x\n\nSome(\n    // a2\n    n\n) = None else { 0 } // y\n",
		},
		{
			// A struct spread's comments, two comments after one item,
			// and comments in parentheses around a parameter, a
			// destructured name, a `with` value and a variant's payload.
			"fn main() {\n    also_same = {.. // c1\n           user, address: {..user.address, city: \"NYC\"}}\n    f = |( // a23\n     _acc1) = 1, e2| e2\n    (numbers, (\n    // b10\n  errors)) = pair\n    with App.clock = ( // a9\nFakeClock{at: 1})\n    y = case xs {\n        .Dynamic( // a3\n         v) -> v\n    }\n}\n\ninterface Formatted {\n    fn format(\n   // b1\n             value: self // c2\n            , // a3\n\tstyle: String): String // t4\n}\n",
			"fn main() {\n    also_same = {\n        ..user, // c1\n        address: {..user.address, city: \"NYC\"},\n    }\n\n    f = |\n        // a23\n        _acc1 = 1,\n        e2\n    | e2\n\n    (\n        numbers,\n        // b10\n        errors\n    ) = pair\n\n    with App.clock = (\n        // a9\n        FakeClock{at: 1}\n    )\n\n    y = case xs {\n        .Dynamic(\n            // a3\n            v\n        ) -> v\n    }\n}\n\ninterface Formatted {\n    fn format(\n        // b1\n        value: self, // c2\n        // a3\n        style: String,\n    ): String // t4\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
