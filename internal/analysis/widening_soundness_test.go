package analysis_test

import "testing"

const wideningDecls = `struct Circle
    r: Int
end

struct Square
    s: Int
end

enum Shape
    embeds Circle
    embeds Square
end

fn area(c: Circle) -> Int
    c.r
end

fn count(n: NonNegativeInt) -> Int
    n
end

fn int_of(c: Bool) -> Int
    if c then 1 else -1
end

fn apply<T>(f: (T) -> Int, x: T) -> Int
    f(x)
end

fn apply_all<T>(f: (T) -> Int, xs: List<T>) -> Int
    xs |> Iter.map(f) |> Iter.sum()
end

fn pick<T>(c: Bool, a: T, b: T) -> T
    if c then a else b
end

fn shapes(xs: List<Shape>) -> Int
    List.length(xs)
end

fn ints(xs: List<Int>) -> Int
    List.length(xs)
end

fn shape_of(s: Shape) -> Int
    case s
        .Circle{} -> 1
        .Square{} -> 2
    end
end

`

func wideningSource(body string) string {
	return wideningDecls + "fn main()\n    c = int_of(True) > 0\n    circle = Circle{r: 1}\n    shape: Shape = Square{s: 2}\n    n: NonNegativeInt = 1\n    i = int_of(c)\n" + body + "\nend\n"
}

// A type parameter an argument solved to an embedded type, or to a
// refinement, widens to the enum or the ancestor a later argument brings,
// only where every earlier argument still fits at the widened type
// (spec §4 *Chains and conversion*). A value position (a value argument, a
// list's elements) takes the wider type; a function argument's parameter
// does not, since the function would be called with values it was not
// written for.
func TestWiden_ATypeParameterWidensOnlyWhereEarlierArgumentsStillFit(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"two values of one parameter", "    _ = shape_of(pick(c, circle, shape))"},
		{"the enum first", "    _ = shape_of(pick(c, shape, circle))"},
		{"lists of variables", "    cs = [circle]\n    ss = [shape]\n    _ = shapes(List.concat(cs, ss))"},
		{"list literals of an enum", "    _ = shapes(List.concat([circle], [shape]))"},
		{"list literals of a refinement", "    _ = ints(List.concat([n], [i]))"},
		{"list literals, the ancestor first", "    _ = ints(List.concat([i], [n]))"},
		{"a list literal of a refinement unannotated", "    xs = List.concat([n], [i])\n    _ = ints(xs)"},
		{"piped list literals", "    _ = ints([n] |> List.concat([i, 5]))\n    _ = shapes([circle] |> List.concat([shape]))"},
		{"a list literal at a function of the enum", "    _ = apply_all(shape_of, [circle, shape])"},
		{"a function at the enum", "    _ = apply(shape_of, circle)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(wideningSource(tc.body))
			expectNoStdlibErrors(t, errs)
		})
	}
}

// The mirror: a parameter a function argument takes is not widened past
// it, and the argument that would widen it is the mismatch.
func TestWiden_NotPastAFunctionArgument(t *testing.T) {
	for _, tc := range []struct{ name, body, want, at string }{
		{"an enum value after a function of a variant", "    _ = apply(area, shape)",
			"argument 2: expected Circle, got Shape\nhelp: Shape may hold a variant other than Circle, and only a match narrows it: `case value` with the arm `.Circle{} -> ...`", "shape"},
		{"an ancestor after a function of a refinement", "    _ = apply(count, i)",
			"argument 2: expected NonNegativeInt, got Int\nhelp: NonNegativeInt is a subtype of Int; check the value with `NonNegativeInt.try_from(...)`, which is `Some` when it satisfies `fn n -> n >= 0` and `None` when not", "i"},
		{"a list literal of the enum after a function of a variant", "    _ = apply_all(area, [shape])",
			"list element type mismatch: expected Circle, got Shape\nhelp: Shape may hold a variant other than Circle, and only a match narrows it: `case value` with the arm `.Circle{} -> ...`", "shape"},
		{"a list literal of an ancestor after a function of a refinement", "    _ = apply_all(count, [i])",
			"list element type mismatch: expected NonNegativeInt, got Int\nhelp: NonNegativeInt is a subtype of Int; check the value with `NonNegativeInt.try_from(...)`, which is `Some` when it satisfies `fn n -> n >= 0` and `None` when not", "i"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := wideningSource(tc.body)
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}
