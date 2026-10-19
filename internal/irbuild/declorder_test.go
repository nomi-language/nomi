package irbuild

import (
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Declaration order does not change what a program does: names resolve
// whole-program, so a type may name one declared after it, in its own file or
// another. Each type's layout (boxing, slots) is decided after every
// declaration it stores is resolved, including the generic instances built
// while they were (deferInstLayout).

// declOrderBlocks are mutually referring declarations, each with its derives:
// a generic enum whose payload is a struct (Tree<Int>'s `Node Child`), a
// generic struct holding another declaration (Pair<T, Child>), a Maybe of a
// generic instance, an enum that embeds a marker, and a distinct type.
var declOrderBlocks = []string{
	`pub struct Parent {
    t: Tree<Int>
    k: Kind
    p: Maybe<Pair<Child, Mark>>
}`,
	`pub struct Child {
    n: Int
    m: Meters
}

derive Equatable for Child`,
	`pub enum Tree<T> {
    Leaf T
    Node Child
    Both {
        left: Pair<T, Child>
        size: Meters
    }
}

derive Equatable for Tree<T> where T: Equatable`,
	`pub struct Pair<A, B> {
    a: A
    b: B
}

derive Equatable for Pair<A, B> where A: Equatable, B: Equatable`,
	`pub enum Kind {
    Plain
    Holds Child
    embeds Mark
}`,
	`pub type Mark`,
	`pub type Meters Int

derive Equatable for Meters`,
}

const (
	declOrderMain = `fn show(p: Parent) {
    io.inspect(p)
    case p.t {
        Tree.Leaf(n) -> io.print("leaf ${n}")
        Tree.Node(ch) -> io.print("node ${ch.n}")
        #Tree.Both{left, size: _} -> io.print("both ${left.a} ${left.b.n}")
    }
    case p.k {
        Kind.Plain -> io.print("plain")
        Kind.Holds(ch) -> io.print("holds ${ch.n}")
        Kind.Mark -> io.print("mark")
    }
}

fn main() {
    c = #Child{n: 3, m: Meters(5)}
    show(#Parent{t: Tree.Leaf(1), k: Kind.Plain, p: None})
    show(#Parent{t: Tree.Node(c), k: Kind.Holds(c), p: Some(#Pair{a: c, b: Mark})})
    io.print(Tree.Node(c) == Tree.Node(c))
    io.print(#Tree.Both{left: #Pair{a: 2, b: c}, size: Meters(9)} == Tree.Leaf(2))
    show(#Parent{t: #Tree.Both{left: #Pair{a: 2, b: c}, size: Meters(9)}, k: Mark, p: None})
}
`
	declOrderWant = `#Parent{t: Leaf(1), k: Plain, p: None}
leaf 1
plain
#Parent{t: Node(#Child{n: 3, m: Meters(5)}), k: Holds(#Child{n: 3, m: Meters(5)}), p: Some(#Pair{a: #Child{n: 3, m: Meters(5)}, b: Mark})}
node 3
holds 3
True
False
#Parent{t: #Both{left: #Pair{a: 2, b: #Child{n: 3, m: Meters(5)}}, size: Meters(9)}, k: Mark, p: None}
both 2 3
mark
`
)

// declOrders is every rotation of the blocks and of their reverse, then
// shuffles from a fixed seed: each block comes first and last, and each pair
// is met in both orders.
func declOrders(n, shuffles int) [][]int {
	base := make([]int, n)
	for i := range base {
		base[i] = i
	}
	rev := slices.Clone(base)
	slices.Reverse(rev)
	var out [][]int
	for _, b := range [][]int{base, rev} {
		for r := range n {
			out = append(out, append(slices.Clone(b[r:]), b[:r]...))
		}
	}
	rng := rand.New(rand.NewSource(1))
	for range shuffles {
		p := slices.Clone(base)
		rng.Shuffle(n, func(i, j int) { p[i], p[j] = p[j], p[i] })
		out = append(out, p)
	}
	return out
}

var declOrderName = regexp.MustCompile(`(?m)^pub (?:struct|enum|type) (\w+)`)

// declOrderFiles stages the blocks in order across nfiles files, dealt round
// robin, with the main program in main.nomi. With one file the blocks go in
// main.nomi itself. Each file imports, from the others, the names its text
// mentions.
func declOrderFiles(order []int, nfiles int) map[string]string {
	owner := map[string]string{}
	bodies := make([][]string, nfiles)
	fileName := func(i int) string {
		if nfiles == 1 {
			return "main.nomi"
		}
		return fmt.Sprintf("decls%d.nomi", i)
	}
	for i, b := range order {
		f := i % nfiles
		bodies[f] = append(bodies[f], declOrderBlocks[b])
		owner[declOrderName.FindStringSubmatch(declOrderBlocks[b])[1]] = fileName(f)
	}
	texts := map[string]string{}
	for f := range nfiles {
		texts[fileName(f)] = strings.Join(bodies[f], "\n\n") + "\n"
	}
	if nfiles == 1 {
		texts["main.nomi"] += "\n" + declOrderMain
	} else {
		texts["main.nomi"] = declOrderMain
	}
	out := map[string]string{}
	for name, text := range texts {
		byFile := map[string][]string{}
		for ty, file := range owner {
			if file != name && regexp.MustCompile(`\b`+ty+`\b`).MatchString(text) {
				mod := strings.TrimSuffix(file, ".nomi")
				byFile[mod] = append(byFile[mod], ty)
			}
		}
		var imports []string
		if name == "main.nomi" {
			imports = append(imports, "std/io")
		}
		for _, mod := range slices.Sorted(func(yield func(string) bool) {
			for m := range byFile {
				if !yield(m) {
					return
				}
			}
		}) {
			tys := byFile[mod]
			slices.Sort(tys)
			imports = append(imports, mod+".{"+strings.Join(tys, ", ")+"}")
		}
		head := ""
		if len(imports) > 0 {
			head = "import {\n    " + strings.Join(imports, "\n    ") + "\n}\n\n"
		}
		out[name] = head + text
	}
	return out
}

// Every order of the declarations, in one file and dealt across two and three
// files, prints the same thing.
func TestDeclarationOrderDoesNotChangeAProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	orders := declOrders(len(declOrderBlocks), 10)
	for i, order := range orders {
		for _, nfiles := range []int{1, 2, 3} {
			if nfiles > 1 && i%3 != 0 {
				// Across files, every third order: the rotations still put
				// each block first in some file.
				continue
			}
			t.Run(fmt.Sprintf("order%d/files%d", i, nfiles), func(t *testing.T) {
				typeCycleRun(t, declOrderFiles(order, nfiles), declOrderWant)
			})
		}
	}
}
