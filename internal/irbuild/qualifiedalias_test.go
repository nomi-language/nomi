package irbuild

import "testing"

// A struct literal through a typealias of another file's struct builds that
// struct, written through the alias's module, and the alias names it in type
// position. The alias's target is found by its declaration: no spelling
// through `shapes` names `Pt`, which `geom` declares.
func TestQualifiedAlias_LiteralThroughAnAliasOfAnImportedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"nomi.toml": "[module]\nname = \"aliased\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import shapes
import std::io

fn main()
    p = shapes.P{x: 1}
    q: shapes.P = shapes.P{x: 2}
    io.print("${p.x + q.x}")
end
`,
		"shapes.nomi": `import geom as g

pub typealias P g.Pt
`,
		"geom.nomi": `pub struct Pt
    x: Int
end
`,
	}, "3\n")
}

// An alias whose target has the alias's own name in another file
// (`pub typealias Item src.Item`), named through the alias's qualifier, as
// sqlkit's `Migration` stands for the migrate module's. Resolving the alias
// by rewriting `items.Item` to its target's name gave `items.Item` again and
// recursed until the stack overflowed.
func TestQualifiedAlias_TargetOfTheSameName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"nomi.toml": "[module]\nname = \"samename\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import items
import std::io

fn main()
    all: List<items.Item> = [items.Item{n: 4}]
    io.print("${Iter.sum_by(all, &(&1.n))}")
end
`,
		"items.nomi": `import base as src

pub typealias Item src.Item
`,
		"base.nomi": `pub struct Item
    n: Int
end
`,
	}, "4\n")
}
