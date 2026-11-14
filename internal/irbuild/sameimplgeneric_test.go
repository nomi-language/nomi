package irbuild

import "testing"

// A bare call inside an inherent block on a generic type to a function of
// the same block reaches it as the type-qualified call does. A function
// that declares type parameters of its own (`fn has<K>` in `impl Tree<K>`,
// whose K shadows the block's, spec §4 *Generics*) is withheld from its
// block and instantiated per call (genericMethodItem). A bare call takes the
// type-qualified call's route from the checker's record at the callee
// (bareImplCall), so a route that read only the block's items, and declined
// as "this call to `has` is not supported yet", cannot recur. The variants
// are a bare
// self-call and a bare call to a sibling function, with and without the
// redeclared parameter and with and without a `where` bound.
// verifyLambdaProgram fails if the front end rejects the program.
func TestSameImplBareCall_GenericOwnerFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const tree = `import std::io

enum Tree<K>
    Leaf
    Node (Tree<K>, K, Tree<K>)
end
`
	cases := []struct{ name, impl, main, want string }{
		{
			name: "SelfCallRedeclaredWhere",
			impl: `impl Tree<K>
    fn has<K>(tree: Tree<K>, key: K) -> Bool where K: Comparable
        case tree
            .Leaf -> False
            .Node(left, k, right) ->
                case Comparable.compare(key, k)
                    .Less -> has(left, key)
                    .Greater -> has(right, key)
                    .Equal -> True
                end
        end
    end
end
`,
			main: `    io.print(Tree.has(Tree.Node(.Leaf, 2, .Leaf), 2))
    io.print(Tree.has(Tree.Node(Tree.Node(.Leaf, "a", .Leaf), "b", .Leaf), "a"))
    io.print(Tree.has(Tree.Node(.Leaf, 2, .Leaf), 3))
`,
			want: "True\nTrue\nFalse\n",
		},
		{
			name: "SelfCallBlockParamWhere",
			impl: `impl Tree<K>
    fn has(tree: Tree<K>, key: K) -> Bool where K: Comparable
        case tree
            .Leaf -> False
            .Node(left, k, right) ->
                case Comparable.compare(key, k)
                    .Less -> has(left, key)
                    .Greater -> has(right, key)
                    .Equal -> True
                end
        end
    end
end
`,
			main: `    io.print(Tree.has(Tree.Node(.Leaf, 2, .Leaf), 2))
`,
			want: "True\n",
		},
		{
			name: "SelfCallRedeclared",
			impl: `impl Tree<K>
    fn size<K>(tree: Tree<K>) -> Int
        case tree
            .Leaf -> 0
            .Node(left, _, right) -> size(left) + 1 + size(right)
        end
    end
end
`,
			main: `    io.print(Tree.size(Tree.Node(.Leaf, 2, Tree.Node(.Leaf, 3, .Leaf))))
`,
			want: "2\n",
		},
		{
			name: "SiblingCallRedeclared",
			impl: `impl Tree<K>
    fn size<K>(tree: Tree<K>) -> Int
        case tree
            .Leaf -> 0
            .Node(left, _, right) -> count(left) + 1 + count(right)
        end
    end

    fn count<K>(tree: Tree<K>) -> Int
        size(tree)
    end
end
`,
			main: `    io.print(Tree.size(Tree.Node(.Leaf, "x", Tree.Node(.Leaf, "y", .Leaf))))
`,
			want: "2\n",
		},
		{
			name: "SiblingCallBlockParam",
			impl: `impl Tree<K>
    fn size(tree: Tree<K>) -> Int
        case tree
            .Leaf -> 0
            .Node(left, _, right) -> count(left) + 1 + count(right)
        end
    end

    fn count(tree: Tree<K>) -> Int
        size(tree)
    end
end
`,
			main: `    io.print(Tree.size(Tree.Node(.Leaf, 2, Tree.Node(.Leaf, 3, .Leaf))))
`,
			want: "2\n",
		},
		{
			// A per-instance function calls a sibling that redeclares K.
			name: "SiblingCallIntoRedeclaredWhere",
			impl: `impl Tree<K>
    fn has<K>(tree: Tree<K>, key: K) -> Bool where K: Comparable
        case tree
            .Leaf -> False
            .Node(left, k, right) ->
                case Comparable.compare(key, k)
                    .Less -> has(left, key)
                    .Greater -> has(right, key)
                    .Equal -> True
                end
        end
    end

    fn has_both(tree: Tree<K>, a: K, b: K) -> Bool where K: Comparable
        has(tree, a) and has(tree, b)
    end
end
`,
			main: `    t = Tree.Node(Tree.Node(.Leaf, 1, .Leaf), 2, .Leaf)
    io.print(Tree.has_both(t, 1, 2))
    io.print(Tree.has_both(t, 1, 3))
`,
			want: "True\nFalse\n",
		},
		{
			// A per-instance function with a type parameter of its own.
			name: "SelfCallOwnTypeParam",
			impl: `impl Tree<K>
    fn map<U>(tree: Tree<K>, f: (K) -> U) -> Tree<U>
        case tree
            .Leaf -> .Leaf
            .Node(left, k, right) -> .Node(map(left, f), f(k), map(right, f))
        end
    end

    fn sum(tree: Tree<Int>) -> Int
        case tree
            .Leaf -> 0
            .Node(l, k, r) -> sum(l) + k + sum(r)
        end
    end
end
`,
			main: `    t = Tree.Node(Tree.Node(.Leaf, 1, .Leaf), 2, .Leaf)
    io.print(Tree.sum(Tree.map(t, &(&1 * 10))))
`,
			want: "30\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verifyLambdaProgram(t, tree+"\n"+c.impl+"\nfn main()\n"+c.main+"end\n", c.want)
		})
	}
}

// A bare call in an interface default to a sibling requirement is the
// interface-qualified call (bareImplCall), so it reaches the default's own
// interface's function when the implementor also implements another
// interface with a requirement of the same name. The checker resolves the
// name to the default's interface; a route that declined any such rival
// left `Named.shout(l)` unable to run.
func TestBareImplCall_DefaultBesideRivalRequirement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

interface Named
    fn label(value: self) -> String

    fn shout(value: self) -> String
        label(value) + "!"
    end
end

interface Tagged
    fn label(value: self) -> String
end

struct Loud
    name: String
end

impl Named for Loud
    fn label(value: Loud) -> String
        "named:" + value.name
    end
end

impl Tagged for Loud
    fn label(value: Loud) -> String
        "tagged:" + value.name
    end
end

fn main()
    l = Loud{name: "a"}
    io.print(Named.shout(l))
    io.print(Tagged.label(l))
end
`, "named:a!\ntagged:a\n")
}
