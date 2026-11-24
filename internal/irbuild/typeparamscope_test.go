package irbuild

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A type parameter is its declaration, not its spelling. The builder enters a
// declaration while it lowers another one: a generic type's impl block is
// registered while the call that first names the instance is lowered, inside
// the caller's frame. A frame that opens a declaration hides the frames below
// it (substScope, visibleSubst), so a caller's parameter never answers for a
// callee's parameter of the same name. Before that, `fn make<S>` in `impl
// Box<M>`, reached from `fn mk<S>`, read mk's `S` as bound, was built as a
// plain member at mk's argument and declined its body's call to `id`; renaming
// mk's parameter made the same program run. Each program here spells the
// parameters of caller and callee alike. verifyLambdaProgram fails if the
// front end rejects one.
func TestTypeParamScope_SameSpellingAcrossDeclarations(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cases := []struct{ name, src, want string }{
		{
			// The reported program: an impl function's own parameter, reached
			// from a generic caller whose parameter is spelled alike, whose
			// body calls another generic function.
			name: "ImplFnOwnParamAndCallerParam",
			src: `import std::io

struct Box<M>
    m: M
end

impl Box<M>
    fn make<S>(s: S, m: M) -> Box<M>
        _ = id(s)
        Box{m: id(m)}
    end
end

fn id<T>(t: T) -> T
    t
end

fn mk<S>(s: S) -> Box<Option<S>>
    Box.make(s, Some(s))
end

fn main()
    io.print(mk(1).m)
end
`,
			want: "Some(1)\n",
		},
		{
			// The same spelling at three depths: top, mid and the impl
			// function, with id spelled alike too.
			name: "ThreeDepths",
			src: `import std::io

struct Box<M>
    m: M
end

impl Box<M>
    fn make<S>(s: S, m: M) -> Box<M>
        _ = id(s)
        Box{m: id(m)}
    end
end

fn id<S>(s: S) -> S
    s
end

fn mid<S>(s: S) -> Box<Option<S>>
    Box.make(s, Some(id(s)))
end

fn top<S>(s: S) -> Box<Option<S>>
    mid(id(s))
end

fn main()
    io.print(top(1).m)
    io.print(top("a").m)
end
`,
			want: "Some(1)\nSome(a)\n",
		},
		{
			// The type's parameter and the impl function's own parameter
			// spelled as the caller's two, bound the other way round.
			name: "BlockAndOwnParamsSpelledAsTheCallers",
			src: `import std::io

struct Box<T>
    t: T
end

impl Box<T>
    fn pair<U>(b: Box<T>, u: U) -> (T, U)
        (id(b.t), id(u))
    end
end

fn id<T>(t: T) -> T
    t
end

fn go<T, U>(t: T, u: U) -> (U, T)
    Box.pair(Box{t: u}, t)
end

fn main()
    io.print(go(1, "x"))
end
`,
			want: "(x, 1)\n",
		},
		{
			// A std generic called from a user generic of the same spelling.
			// A std instance is built in its module's own gen, whose frames
			// are its own, so this held before; it stays held.
			name: "StdGenericFromUserGeneric",
			src: `import std::io

fn twice<T>(xs: List<T>) -> List<T>
    List.concat(xs, xs)
end

fn firsts<T>(xs: List<T>) -> Option<T>
    List.first(twice(xs))
end

fn main()
    io.print(firsts([1, 2]))
    io.print(firsts(["a"]))
end
`,
			want: "Some(1)\nSome(a)\n",
		},
		{
			// std::actors' Agent shape: a block whose functions name the
			// type's parameter, calling a function of another generic type's
			// block that declares a parameter of its own spelled alike.
			name: "AgentShape",
			src: `import std::io

struct Letter<M>
    message: M
end

struct Actor<M>
    letters: List<Letter<M>>
end

impl Actor<M>
    fn start<S>(state: S, handle: (S, M) -> S, message: M) -> (Actor<M>, S)
        (Actor{letters: [Letter{message:}]}, serve(state, handle, message))
    end
end

fn serve<S, M>(state: S, handle: (S, M) -> S, message: M) -> S
    handle(state, message)
end

struct Agent<S>
    actor: Actor<(S) -> S>
    state: S
end

impl Agent<S>
    fn start(state: S, change: (S) -> S) -> Agent<S>
        (actor, next) = Actor.start(state, apply, change)
        Agent{actor:, state: next}
    end
end

fn apply<S>(state: S, change: (S) -> S) -> S
    change(state)
end

fn main()
    io.print(Agent.start(1, fn n -> n + 1).state)
    io.print(Agent.start("a", fn s -> s + "b").state)
end
`,
			want: "2\nab\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			verifyLambdaProgram(t, c.src, c.want)
		})
	}
}

// The generic interface an impl block implements binds its own parameters,
// which may share a spelling with the block's: `impl Holds<List<T>> for
// Box<T>` binds the block's `T` to Int and the interface's to List<Int>.
// The interface's frame is entered only around the code the block takes
// from the interface (enterIfaceCode), so the block's own functions resolve
// their `T` to the block's argument. Before that, the interface's frame lay
// over the block's, `get` was resolved at `Box<List<Int>>`, whose impls
// named `Box<List<List<Int>>>`, and `nomi check` never finished. An impl
// function's linkage name is its block's identity (implItemLink), so two
// blocks of one interface for one type stay two functions through objects.
// Each program runs under a deadline, so a regression fails and does not
// hang.
func TestTypeParamScope_InterfaceAndBlockSpelledAlike(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cases := []struct{ name, src, want string }{
		{
			// The reported program.
			name: "ImplOfAnInterfaceAtTheBlocksParameterWrapped",
			src: `import std::io

interface Holds<T>
    fn get(value: self) -> T
end

struct Box<T>
    t: T
end

impl Holds<List<T>> for Box<T>
    fn get(b: Box<T>) -> List<T>
        [b.t]
    end
end

fn main()
    io.print(Holds.get(Box{t: 1}))
end
`,
			want: "[1]\n",
		},
		{
			// Two blocks of one generic interface for one type, each with
			// the interface's default inherited at its own arguments.
			name: "TwoImplsOfOneInterfaceForOneType",
			src: `import std::io

interface Holds<T>
    fn holds(b: self, x: T) -> Bool

    fn holds_both(b: self, x: T, y: T) -> Bool
        Holds.holds(b, x) and Holds.holds(b, y)
    end
end

struct Box2
    n: Int
    s: String
end

impl Holds<Int> for Box2
    fn holds(b: Box2, x: Int) -> Bool
        b.n == x
    end
end

impl Holds<String> for Box2
    fn holds(b: Box2, x: String) -> Bool
        b.s == x
    end
end

fn held_by<C, T>(c: C, x: T) -> Bool where C: Holds<T>
    C.holds(c, x)
end

fn main()
    b = Box2{n: 1, s: "a"}
    io.print(Box2.holds(b, 1))
    io.print(Holds.holds(b, "b"))
    io.print(held_by(b, "a"))
    io.print(Box2.holds_both(b, 1, 1))
    io.print(Holds.holds_both(b, "a", "b"))
end
`,
			want: "True\nFalse\nTrue\nTrue\nFalse\n",
		},
		{
			// An interface default that names the interface's parameter,
			// inherited by a block whose own parameter is spelled alike and
			// bound to another type.
			name: "InheritedDefaultNamesTheInterfacesParameter",
			src: `import std::io

interface Holds<T>
    fn get(value: self) -> T

    fn twice(value: self) -> (T, T)
        (Holds.get(value), Holds.get(value))
    end
end

struct Box<T>
    t: T
end

impl Holds<List<T>> for Box<T>
    fn get(b: Box<T>) -> List<T>
        [b.t]
    end
end

fn main()
    io.print(Box.twice(Box{t: 1}))
    io.print(Holds.twice(Box{t: "a"}))
end
`,
			want: "([1], [1])\n([a], [a])\n",
		},
		{
			// A std interface with a parameter, implemented by a program's
			// generic type whose parameter is spelled alike.
			name: "StdInterfaceImplementedByAGenericTypeSpelledAlike",
			src: `import std::io

struct Bag<T>
    items: List<T>
end

impl Contains<List<T>> for Bag<T>
    fn contains(b: Bag<T>, x: List<T>) -> Bool
        b.items == x
    end
end

struct Pairs<T>
    items: List<T>
end

impl Iter<List<T>> for Pairs<T>
    fn each_while(p: Pairs<T>, yield: (List<T>) -> Bool) -> Bool
        Iter.each_while(p.items, fn x -> yield([x, x]))
    end
end

fn main()
    b = Bag{items: [1, 2]}
    io.print(Contains.contains(b, [1, 2]))
    io.print(Bag.contains(b, [2]))
    io.print(Iter.to_list(Pairs{items: [1, 2]}))
end
`,
			want: "True\nFalse\n[[1, 1], [2, 2]]\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer lowersWithin(t, 5*time.Minute)()
			verifyLambdaProgram(t, c.src, c.want)
			t.Run("linked through objects", func(t *testing.T) {
				defer LinkThroughObjects()()
				verifyLambdaProgram(t, c.src, c.want)
			})
		})
	}
}

// An instance built inside typeInstCap others is refused, with the reason
// that names the chain, and interned refused, so a later mention gets the
// same answer; one built inside fewer is laid out. A type whose parts hold
// itself at a larger argument is the checker's polymorphic-recursion
// error, so no checked program of that shape reaches the bound, and the
// bound is held here by setting the count where such a chain would have
// taken it.
func TestTypeInstCap_TheInstancePastTheBoundIsRefused(t *testing.T) {
	g := lowerableGen(t, "pub struct Box<T>\n  item: T\nend\n\nfn peek(b: Box<Int>) -> Int\n  b.item\nend\n")
	tpl := g.genericTemplates["Box"]
	if tpl == nil {
		t.Fatal("no `Box` template, so the bound has nothing to refuse")
	}
	instanceAt := func(arg kind) *typeDef {
		for _, d := range g.genericInstOrder {
			if d.genericOf == tpl && samePartsSlice(d.genericArgs, []kind{arg}) {
				return d
			}
		}
		return nil
	}
	g.genericInstNesting = typeInstCap
	if k, built := g.genericInstance(tpl, []kind{kindString}, nil); built || k != kindInvalid {
		t.Fatalf("Box<String> was built inside %d other instances, the bound", typeInstCap)
	}
	if d := instanceAt(kindString); d == nil || d.lowerable || d.why != "generic instantiation depth" ||
		!strings.Contains(d.whyDetail, "Box<String>") {
		t.Fatalf("the refused instance is not interned with the reason that names it: %+v", d)
	}
	if g.genericInstNesting != typeInstCap {
		t.Fatalf("the refusal left the count at %d, want %d", g.genericInstNesting, typeInstCap)
	}
	g.genericInstNesting = 0
	if _, built := g.genericInstance(tpl, []kind{kindString}, nil); built {
		t.Fatal("a later mention of the refused Box<String> was built")
	}
	g.genericInstNesting = typeInstCap - 1
	if _, built := g.genericInstance(tpl, []kind{kindFloat}, nil); !built {
		t.Fatalf("Box<Float> inside %d other instances, under the bound, was refused: %+v", typeInstCap-1, instanceAt(kindFloat))
	}
	if g.genericInstNesting != typeInstCap-1 {
		t.Fatalf("building Box<Float> left the count at %d, want %d", g.genericInstNesting, typeInstCap-1)
	}
}

// An impl of a generic interface at a larger instance of its receiver names
// a larger instance at each instance it is registered for, so registering
// one builds a chain that only the bounds stop (typeInstCap,
// typeInstSizeCap). The checker accepts each shape here: a chain that grows
// by one type at each step, one that doubles through a tuple, and one that
// doubles through a type of two parameters. Each is refused with the
// bounds' reason within a budget of genericInstance calls that panics past
// it. Before each argument was projected once, the growing chain took about
// 2^32 calls in a test binary, where every frame's checker types are
// projected (verifySubst); the doubling ones took as many in any build
// before typeInstSizeCap.
func TestTypeInstCap_AnImplAtALargerReceiverIsRefusedInFewSteps(t *testing.T) {
	const iface = "interface Deeper<T>\n    fn deeper(value: self) -> T\nend\n\n"
	cases := []struct {
		name, src, tpl string
		args           []analysis.Type
	}{
		{
			name: "GrowsByOne",
			src: iface + `struct Box<T>
    t: T
end

impl Deeper<Box<Box<T>>> for Box<T>
    fn deeper(b: Box<T>) -> Box<Box<T>>
        Box{t: Box{t: b.t}}
    end
end
`,
			tpl:  "Box",
			args: []analysis.Type{analysis.TypeInt},
		},
		{
			name: "DoublesThroughATuple",
			src: iface + `struct Box<T>
    t: T
end

impl Deeper<Box<(T, T)>> for Box<T>
    fn deeper(b: Box<T>) -> Box<(T, T)>
        Box{t: (b.t, b.t)}
    end
end
`,
			tpl:  "Box",
			args: []analysis.Type{analysis.TypeInt},
		},
		{
			name: "DoublesThroughTwoParameters",
			src: iface + `struct P<A, B>
    a: A
    b: B
end

impl Deeper<P<P<A, B>, P<A, B>>> for P<A, B>
    fn deeper(p: P<A, B>) -> P<P<A, B>, P<A, B>>
        P{a: p, b: p}
    end
end
`,
			tpl:  "P",
			args: []analysis.Type{analysis.TypeInt, analysis.TypeInt},
		},
	}
	const budget = 4000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := lowerableGen(t, c.src)
			tpl := g.genericTemplates[c.tpl]
			if tpl == nil {
				t.Fatalf("no `%s` template, so nothing is instantiated", c.tpl)
			}
			args := make([]kind, len(c.args))
			for i := range args {
				args[i] = kindInt
			}
			// The tuple's cost is in walks over its 2^d types (its spelling),
			// not in calls, so without typeInstSizeCap it is this fence
			// that stops it.
			defer lowersWithin(t, time.Minute)()
			g.genericInstCalls, g.genericInstBudget = 0, budget
			if _, built := g.genericInstance(tpl, args, c.args); built {
				t.Fatalf("%s<Int> was built, so its impl's chain of larger instances ended or was never started", c.tpl)
			}
			var bound *typeDef
			for _, d := range g.genericInstOrder {
				if d.why == "generic instantiation depth" {
					bound = d
				}
				if d.genericOf == tpl && !kindsHoldAtMost(d.genericArgs, 2*typeInstSizeCap+1) {
					t.Fatalf("an instance of %s was built with arguments of more than %d types, past what one step from the bound makes",
						c.tpl, 2*typeInstSizeCap+1)
				}
			}
			if bound == nil {
				t.Fatalf("no instance was refused by the bounds; built %d", len(g.genericInstOrder))
			}
			if !strings.Contains(bound.whyDetail, "the chain does not terminate") {
				t.Fatalf("the refusal does not say why: %q", bound.whyDetail)
			}
			if n := len(g.genericInstOrder); n > typeInstCap+1 {
				t.Fatalf("built %d instances, more than the chain of %d the depth bound allows", n, typeInstCap+1)
			}
			t.Logf("%d instances, %d genericInstance calls", len(g.genericInstOrder), g.genericInstCalls)
		})
	}
}

// lowersWithin panics, failing the test binary with a message, when the
// test it is deferred in runs past d: a lowering that loops ends the run
// instead of holding it. It returns the stop.
func lowersWithin(t *testing.T, d time.Duration) func() {
	name := t.Name()
	timer := time.AfterFunc(d, func() {
		panic(fmt.Sprintf("%s did not finish in %s: the lowering does not terminate", name, d))
	})
	return func() { timer.Stop() }
}

// TestEverySubstEntryHasANameAndAScope fails for an entry the census cannot
// name or whose frame does not say whether it opens a declaration.
func TestEverySubstEntryHasANameAndAScope(t *testing.T) {
	for e := substEntry(0); e < substEntries; e++ {
		info := substEntryInfo[e]
		if info.name == "" {
			t.Errorf("substitution entry %d has no census name", e)
		}
		if info.scope == substScopeUnset {
			t.Errorf("substitution entry %q does not say whether its frame opens a declaration (substScope)", info.name)
		}
	}
}
