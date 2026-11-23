package vmhost_test

import (
	"strings"
	"testing"
)

// A std call whose type parameter an earlier operand solved narrower and a
// later one widened to the enum (spec §4 *Chains and conversion*) runs as a
// user function of the same signature does: the earlier operand converts
// to the checker's instantiation. `List.concat(circles, shapes)` is a
// List<Shape>, as are the lists, vectors, sets, maps, Options and Iters of
// the other operations here, whether they cross to the host (List, Vector,
// Set, Map, `with_default`), are the builder's own (`Iter.concat`) or are
// std bodies instantiated at the call (`Map.merge_with`). A refinement's
// ancestor widens the same way and costs nothing, since a refinement is
// its base at run time.
func TestRun_StdCallConvertsAnOperandToTheWidenedTypeParameter(t *testing.T) {
	out, err := runVM(t, `import std::io

struct Circle
    r: Int
end

struct Square
    s: Int
end

enum Shape
    embeds Circle
    embeds Square
end

derive Equatable for Circle
derive Hashable for Circle
derive Equatable for Square
derive Hashable for Square
derive Equatable for Shape
derive Hashable for Shape

fn kind(s: Shape) -> String
    case s
        .Circle{r:} -> "c${r}"
        .Square{s:} -> "s${s}"
    end
end

fn kinds(xs: Iter<Shape>) -> String
    xs |> Iter.map(kind) |> String.join(",")
end

fn main()
    circle = Circle{r: 1}
    shape: Shape = Square{s: 2}
    cs = [circle]
    ss = [shape]
    io.print(kinds(List.concat(cs, ss)))
    io.print(kinds(List.concat(ss, cs)))
    io.print(kinds(List.concat([circle], [shape])))
    io.print(kinds([circle] |> List.concat([shape])))
    cv = Iter.to_vector(cs)
    io.print(kinds(Vector.concat(cv, Iter.to_vector(ss))))
    io.print(kinds(Vector.push(cv, shape)))
    io.print(kinds(Iter.concat(cs, ss)))
    cset = Iter.to_set(cs)
    io.print(Set.union(cset, Iter.to_set(ss)) |> Set.size())
    io.print(Set.insert(cset, shape) |> Set.contains(shape))
    m = {"a" => circle}
    io.print(Map.put(m, "b", shape) |> Map.values() |> kinds())
    ms: Map<String, Shape> = {"b" => shape}
    io.print(Map.merge(m, ms) |> Map.values() |> kinds())
    io.print(Map.merge_with(m, ms, fn a, _b -> a) |> Map.values() |> kinds())
    io.print(kind(Option.with_default(Some(circle), shape)))
    r: Result<Circle, String> = Err("no")
    io.print(kind(Result.with_default(r, shape)))
    n: NonNegativeInt = 3
    i = -4
    ns: List<NonNegativeInt> = [n]
    io.print(List.concat(ns, [i]))
    io.print(List.concat([n], [i]))
end
`)
	if err != nil {
		t.Fatalf("the program does not run: %v", err)
	}
	want := strings.Join([]string{
		"c1,s2", "s2,c1", "c1,s2", "c1,s2",
		"c1,s2", "c1,s2", "c1,s2",
		"2", "True",
		"c1,s2", "c1,s2", "c1,s2",
		"c1", "s2",
		"[3, -4]", "[3, -4]",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
