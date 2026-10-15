package vmhost

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/format"
)

// A generator of well-typed programs, for the rule that a program the front
// end accepts lowers (lowering_fuzz_test.go). Byte-level mutation of the
// seeds mostly produces parse errors; these programs instead recombine
// constructs the checker accepts, type-directed so that most of them check:
// the same value reached through user generic functions at new type
// arguments, lambdas, named functions, nested functions, constructors and
// partial applications as callbacks, pipes and `then` stages, blocks, `if`
// and `case`, `try`, and values wrapped in Some, Ok, tuples, lists, a
// generic struct, a generic enum and a distinct type, and functions held in
// tuples and boxes and called through the field chain that reads them. A
// program's types sit at file level or inside the body that uses them, and
// statement blocks, some nested, declare types of their own, among them
// distinct types over Decimal, Bytes, Regex, Unit and containers of them
// (leafDistinctBlock), or store concrete values where Display's existential
// is expected (erasedBlock). The body is a `fn main` or a test.
//
// The generator stays clear of the shapes knownLoweringGaps lists, each at
// the line that avoids it, so every decline it produces is a new cause. When
// a gap is fixed its reproducer fails TestKnownLoweringGaps; remove the entry
// and the avoidance together.
//
// Program i is built from rand seed i, so a name (`gen/017`) always means the
// same program.

// gt is a generated value's type.
type gt struct {
	k    string // Int, String, Bool, Float, List, Maybe, Result, Tuple, Box, Opt, Meters, Shape, Fn
	args []*gt
}

func (t *gt) String() string {
	switch t.k {
	case "List", "Maybe", "Box", "Opt":
		return t.k + "<" + t.args[0].String() + ">"
	case "Result":
		return "Result<" + t.args[0].String() + ", String>"
	case "Tuple":
		return "(" + t.args[0].String() + ", " + t.args[1].String() + ")"
	case "Fn":
		return "(" + t.args[0].String() + ") -> " + t.args[1].String()
	}
	return t.k
}

func (t *gt) eq(u *gt) bool { return t.String() == u.String() }

var (
	tInt    = &gt{k: "Int"}
	tString = &gt{k: "String"}
	tBool   = &gt{k: "Bool"}
	tFloat  = &gt{k: "Float"}
	tMeters = &gt{k: "Meters"}
	tShape  = &gt{k: "Shape"}
)

func of(k string, args ...*gt) *gt { return &gt{k: k, args: args} }

// hasFn reports whether t holds a function type anywhere: `==` and the
// `same` helper take no function, and a tuple projection's dropped element
// takes none (dataTyp).
func hasFn(ts ...*gt) bool {
	for _, t := range ts {
		if t.k == "Fn" || hasFn(t.args...) {
			return true
		}
	}
	return false
}

type gvar struct {
	name string
	t    *gt
}

// gen builds one program.
type gen struct {
	r *rand.Rand
	// aux draws the choices added after the generator's shapes settled
	// (holdFn), so that adding them left every program's other choices,
	// and so the population TestFrontEndAcceptsSoItLowers checks, as they
	// were.
	aux *rand.Rand
	// div draws the blocks divergingBlock writes, and dn names their
	// bindings apart, so the rest of each program is the one it was
	// without them.
	div *rand.Rand
	// rec draws the statements recursiveBlock writes, so the rest of each
	// program is the one it was without them.
	rec *rand.Rand
	// wrap draws the blocks leafDistinctBlock writes, and wn names their
	// types apart, so the rest of each program is the one it was without
	// them.
	wrap *rand.Rand
	// dyn draws the blocks erasedBlock writes, and en names their
	// declarations apart, so the rest of each program is the one it was
	// without them.
	dyn *rand.Rand
	en  int
	// fix draws the blocks fixedKindBlock writes, and fn names their
	// declarations apart, so the rest of each program is the one it was
	// without them.
	fix     *rand.Rand
	fn      int
	// call draws the blocks callBlock writes, and cn names their
	// declarations apart, so the rest of each program is the one it was
	// without them.
	call *rand.Rand
	cn   int
	wn      int
	dn      int
	n       int
	blocks  int      // statement blocks' declared types, named apart
	helpers []string // named callback functions
	// local declares the program's non-generic types, its named
	// callbacks, the generic helpers it uses and two generic types of its
	// own (genLocalGenericTypes) inside the body that uses them. Box and
	// Opt, which the helpers and the file-level derives name, stay at file
	// level.
	local bool
}

func (g *gen) fresh(prefix string) string {
	g.n++
	return fmt.Sprintf("%s%d", prefix, g.n)
}

func (g *gen) chance(p float64) bool { return g.r.Float64() < p }

func (g *gen) pick(opts []func() string) string { return opts[g.r.Intn(len(opts))]() }

func (g *gen) typ(depth int) *gt {
	scalars := []*gt{tInt, tInt, tString, tString, tBool, tFloat, tMeters, tShape}
	if depth <= 0 || g.chance(0.45) {
		return scalars[g.r.Intn(len(scalars))]
	}
	switch g.r.Intn(7) {
	case 0:
		return of("List", g.typ(depth-1))
	case 1:
		return of("Maybe", g.typ(depth-1))
	case 2:
		return of("Result", g.typ(depth-1))
	case 3:
		return of("Tuple", g.typ(depth-1), g.typ(depth-1))
	case 4:
		return of("Box", g.typ(depth-1))
	case 5:
		return of("Opt", g.typ(depth-1))
	}
	return of("Fn", g.typ(depth-1), g.typ(depth-1))
}

// dataTyp is a type with no function in it.
func (g *gen) dataTyp(depth int) *gt {
	for {
		if t := g.typ(depth); !hasFn(t) {
			return t
		}
	}
}

// equatable reports whether `==` and the `same` helper take t. The derives
// are declared at file level only.
func (g *gen) equatable(t *gt) bool {
	switch t.k {
	case "Int", "String", "Bool", "Float":
		return true
	case "Shape":
		return !g.local
	case "List", "Maybe", "Result":
		return g.equatable(t.args[0])
	case "Box", "Opt":
		return g.equatable(t.args[0])
	case "Tuple":
		return g.equatable(t.args[0]) && g.equatable(t.args[1])
	}
	return false
}

// displayable reports whether t implements Display.
func (g *gen) displayable(t *gt) bool {
	switch t.k {
	case "Int", "String", "Bool", "Float":
		return true
	case "Box":
		return g.displayable(t.args[0])
	}
	return false
}

func (g *gen) displayType() *gt {
	for {
		if t := g.typ(1); g.displayable(t) {
			return t
		}
	}
}

func (g *gen) eqType() *gt {
	for {
		if t := g.typ(1); g.equatable(t) && !hasFn(t) {
			return t
		}
	}
}

func (g *gen) boolLit() string {
	if g.chance(0.5) {
		return "True"
	}
	return "False"
}

// pipeHead parenthesizes a lambda that heads a pipe, whose body would
// otherwise take the pipe in.
func pipeHead(e string) string {
	if strings.HasPrefix(e, "|") {
		return "(" + e + ")"
	}
	return e
}

var simpleTerm = regexp.MustCompile(`^[A-Za-z0-9_."]+$`)

// paren wraps a generated expression that is not a simple term, for an
// operand or a receiver.
func paren(e string) string {
	if simpleTerm.MatchString(e) {
		return e
	}
	return "(" + e + ")"
}

var identRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*\??`)

func uses(body, name string) bool {
	for _, w := range identRe.FindAllString(body, -1) {
		if w == name {
			return true
		}
	}
	return false
}

// leaf is a literal of t, or a variable of t in scope.
func (g *gen) leaf(t *gt, sc []gvar) string {
	var match []gvar
	for _, v := range sc {
		if v.t.eq(t) {
			match = append(match, v)
		}
	}
	if len(match) > 0 && g.chance(0.6) {
		return match[g.r.Intn(len(match))].name
	}
	switch t.k {
	case "Int":
		return fmt.Sprint(g.r.Intn(100))
	case "String":
		return fmt.Sprintf("%q", []string{"a", "bc", "", "xyz"}[g.r.Intn(4)])
	case "Bool":
		return g.boolLit()
	case "Float":
		return fmt.Sprintf("%d.5", g.r.Intn(10))
	case "Meters":
		return fmt.Sprintf("Meters(%d)", g.r.Intn(100))
	case "Shape":
		switch g.r.Intn(3) {
		case 0:
			return "Shape.Dot"
		case 1:
			return fmt.Sprintf("Shape.Circle(%d)", g.r.Intn(10))
		}
		return fmt.Sprintf("Shape.Square(%d)", g.r.Intn(10))
	case "List":
		return g.sortedStd(t.args[0], "["+g.leaf(t.args[0], sc)+"]")
	case "Maybe":
		return "Some(" + g.leaf(t.args[0], sc) + ")"
	case "Result":
		return g.okOf(t.args[0], g.leaf(t.args[0], sc))
	case "Tuple":
		return "(" + g.leaf(t.args[0], sc) + ", " + g.leaf(t.args[1], sc) + ")"
	case "Box":
		return "Box{v: " + g.leaf(t.args[0], sc) + "}"
	case "Opt":
		return "Opt.Has(" + g.leaf(t.args[0], sc) + ")"
	case "Fn":
		// A lambda standing where no function type is expected has its
		// parameter typed: the checker rejects an untyped one there
		// (`cannot infer type for parameter`).
		return g.callback(t.args[0], t.args[1], 0, sc, true)
	}
	panic("leaf: " + t.String())
}

// okOf is `Ok(e)` for a Result<t, String>: through the `ok` helper, with a
// turbofish, or bare, which leaves the error type to whatever the line
// fixes, and to nothing where no value of it is made.
func (g *gen) okOf(t *gt, e string) string {
	switch g.r.Intn(3) {
	case 0:
		return "ok(" + e + ")"
	case 1:
		return "Ok(" + e + ")"
	}
	return "Ok<" + t.String() + ", String>(" + e + ")"
}

// use is an expression of t that reads v, so that a parameter or a binding
// the generator introduced is never unused.
func (g *gen) use(t *gt, v gvar, depth int, sc []gvar) string {
	d := depth - 1
	var opts []func() string
	add := func(fs ...func() string) { opts = append(opts, fs...) }
	n := v.name
	if v.t.eq(t) {
		add(func() string { return n }, func() string { return n },
			func() string { return "ident(" + n + ")" },
			func() string { return n + " |> ident()" })
	}
	switch {
	case t.k == "String" && g.displayable(v.t):
		add(func() string { return "show(" + n + ")" },
			func() string { return `"<${` + n + `}>"` },
			func() string { return "Display.to_string(" + n + ")" })
	case t.k == "Bool" && g.equatable(v.t) && !hasFn(v.t):
		add(func() string { return n + " == " + n },
			func() string { return "same(" + n + ", " + n + ")" })
	case t.k == "Int" && v.t.k == "List":
		add(func() string { return "Iter.count(" + n + ")" },
			func() string { return n + " |> Iter.count()" })
	case t.k == "Int" && v.t.k == "Meters":
		add(func() string { return "case " + n + " {\nMeters(k) -> k\n}" },
			func() string { return "{\nMeters(n) = " + n + "\nn\n}" })
	case t.k == "Int" && v.t.k == "Shape":
		add(func() string { return "case " + n + " {\n.Circle(r) -> r\n.Square(s) -> s * s\n.Dot -> 0\n}" })
	case t.k == "Bool" && v.t.k == "Maybe":
		add(func() string { return "Maybe.some?(" + n + ")" })
	case t.k == "Bool" && v.t.k == "Result":
		add(func() string { return "Result.ok?(" + n + ")" })
	}
	switch v.t.k {
	case "Tuple":
		if v.t.args[0].eq(t) {
			add(func() string { return n + ".0" },
				func() string {
					as, read := g.asWhole()
					return "{\n(a, _)" + as + " = " + n + "\n" + read + "a\n}"
				})
		}
		if v.t.args[1].eq(t) {
			add(func() string { return n + ".1" })
		}
	case "Box":
		if v.t.args[0].eq(t) {
			add(func() string { return n + ".v" },
				func() string {
					as, read := g.asWhole()
					return "{\n{v}" + as + " = " + n + "\n" + read + "v\n}"
				})
			add(func() string { return "unbox(" + n + ")" })
		}
	case "Maybe":
		if v.t.args[0].eq(t) {
			add(func() string {
				some := "Some(x) -> x"
				if g.aux.Intn(2) == 0 {
					some = "Some(x) as whole -> Maybe.with_default(whole, x)"
				}
				return "case " + n + " {\n" + some + "\nNone -> " + g.expr(t, d, sc) + "\n}"
			},
				func() string { return "Maybe.with_default(" + n + ", " + g.expr(t, d, sc) + ")" })
		}
	case "Result":
		if v.t.args[0].eq(t) {
			add(func() string {
				ok := "Ok(x) -> x"
				if g.aux.Intn(2) == 0 {
					ok = "Ok(x) as whole -> Result.with_default(whole, x)"
				}
				return "case " + n + " {\n" + ok + "\nErr(_) -> " + g.expr(t, d, sc) + "\n}"
			},
				func() string { return "Result.with_default(" + n + ", " + g.expr(t, d, sc) + ")" })
		}
	case "Opt":
		if v.t.args[0].eq(t) {
			add(func() string { return "case " + n + " {\n.Has(x) -> x\n.Nothing -> " + g.expr(t, d, sc) + "\n}" })
		}
	case "List":
		if v.t.args[0].eq(t) {
			add(func() string { return "first_or(" + n + ", " + g.expr(t, d, sc) + ")" },
				func() string {
					init := g.expr(t, d, sc)
					return "Iter.reduce(" + n + ", |acc = " + init + ", x| pick(" + g.boolLit() + ", acc, x))"
				})
		}
	case "Fn":
		if v.t.args[1].eq(t) {
			u := v.t.args[0]
			add(func() string { return n + "(" + g.expr(u, d, sc) + ")" },
				func() string { return "apply(" + g.expr(u, d, sc) + ", " + n + ")" },
				func() string { return pipeHead(g.expr(u, d, sc)) + " |> " + n + "()" })
		}
	}
	// A call through a function the value holds, read by a field chain
	// (`n.0(x)`, `n.v.1(x)`, `n.1.0(x)`) rather than bound first.
	for _, p := range fnPaths(v.t, n, 3) {
		if p.t.args[1].eq(t) {
			p := p
			add(func() string { return p.chain + "(" + g.expr(p.t.args[0], d, sc) + ")" })
		}
	}
	if len(opts) == 0 || g.chance(0.1) {
		return "{\n_ = " + n + "\n" + g.expr(t, d, sc) + "\n}"
	}
	return g.pick(opts)
}

// asWhole is, one time in two, ` as whole` for a destructuring pattern to
// end with, and the statement that then reads the name; otherwise nothing. It
// draws from g.aux.
func (g *gen) asWhole() (as, read string) {
	if g.aux.Intn(2) != 0 {
		return "", ""
	}
	return " as whole", "_ = whole\n"
}

// tapStage is, one time in two, a `tap` stage to end a pipeline with: one
// that shows the piped value, one that discards it, or one with an empty
// body (where a layout variant can put a comment). Otherwise nothing. It
// draws from g.aux, and names its parameter apart from g.fresh's names, so
// the program around it is the one it would be without the stage.
func (g *gen) tapStage() string {
	switch g.aux.Intn(6) {
	case 0:
		return "\n|> tap |tapped| io.inspect(tapped)"
	case 1:
		return "\n|> tap |tapped| { _ = tapped }"
	case 2:
		return "\n|> tap |_| {}"
	}
	return ""
}

// holdFn wraps the function expression cb, of type ft, in one to three
// tuples and boxes. It draws from g.aux, so the program around it is the
// one it would be without the wrapping. A generic function named as a value
// is called from a typed lambda, since nothing gives it a function type
// inside a literal.
func (g *gen) holdFn(cb string, ft *gt) (string, *gt) {
	switch cb {
	case "ident", "wrap", "box", "show", "Some", "Opt.Has", "tally", "Iter.count", "Iter.first":
		cb = "|y: " + ft.args[0].String() + "| " + cb + "(y)"
	}
	holder, ht := cb, ft
	for i := 1 + g.aux.Intn(3); i > 0; i-- {
		n := fmt.Sprint(g.aux.Intn(10))
		switch g.aux.Intn(3) {
		case 0:
			holder, ht = "("+holder+", "+n+")", of("Tuple", ht, tInt)
		case 1:
			holder, ht = "("+n+", "+holder+")", of("Tuple", tInt, ht)
		default:
			holder, ht = "Box{v: "+holder+"}", of("Box", ht)
		}
	}
	return holder, ht
}

// stmtBlock is a block in statement position that shows e, of type typ:
// directly, or through a struct, an enum or a distinct type the block
// declares, sometimes with a nested block whose own type holds the outer
// one's. It draws from g.aux.
func (g *gen) stmtBlock(e, typ string, depth int) string {
	g.blocks++
	name := fmt.Sprintf("Local%d", g.blocks)
	var decl, shown string
	switch g.aux.Intn(4) {
	case 0:
		decl, shown = "struct "+name+" {\nv: "+typ+"\n}\n", name+"{v: "+e+"}"
	case 1:
		decl, shown = "enum "+name+" {\nHas "+typ+"\nEmpty\n}\n", name+".Has("+e+")"
	case 2:
		decl, shown = "type "+name+" "+typ+"\n", name+"("+e+")"
	default:
		name, shown = typ, e
	}
	out := "{\n" + decl + "io.inspect(" + shown + ")\n"
	if depth == 0 && g.aux.Intn(3) == 0 {
		out += g.stmtBlock(shown, name, 1)
	}
	return out + "}\n"
}

// fnPath is a field chain from a value to a function it holds.
type fnPath struct {
	chain string
	t     *gt
}

// fnPaths are the chains of tuple indices and Box fields, at most depth
// long, from chain (of type t) to a function it holds.
func fnPaths(t *gt, chain string, depth int) []fnPath {
	if depth == 0 {
		return nil
	}
	var out []fnPath
	step := func(field string, et *gt) {
		next := chain + "." + field
		if et.k == "Fn" {
			out = append(out, fnPath{next, et})
		}
		out = append(out, fnPaths(et, next, depth-1)...)
	}
	switch t.k {
	case "Tuple":
		step("0", t.args[0])
		step("1", t.args[1])
	case "Box":
		step("v", t.args[0])
	}
	return out
}

// lambda is `|x| body` of type (u) -> t, its parameter read by its body.
func (g *gen) lambda(u, t *gt, depth int, sc []gvar, annotate bool) string {
	x := g.fresh("x")
	body := g.use(t, gvar{x, u}, depth, append(sc, gvar{x, u}))
	if annotate {
		return "|" + x + ": " + u.String() + "| " + body
	}
	return "|" + x + "| " + body
}

// namedCallback declares a function of type (u) -> t beside the helpers and
// answers its name.
func (g *gen) namedCallback(u, t *gt, depth int) string {
	name := g.fresh("cb")
	x := g.fresh("x")
	body := g.use(t, gvar{x, u}, depth, []gvar{{x, u}})
	g.helpers = append(g.helpers, fmt.Sprintf("fn %s(%s: %s): %s {\n%s\n}", name, x, u, t, body))
	return name
}

// callback is a value of type (u) -> t in argument position.
func (g *gen) callback(u, t *gt, depth int, sc []gvar, annotate bool) string {
	opts := []func() string{
		func() string { return g.lambda(u, t, depth, sc, annotate) },
		func() string { return g.lambda(u, t, depth, sc, annotate) },
		func() string { return g.namedCallback(u, t, depth) },
	}
	if u.eq(t) {
		opts = append(opts,
			func() string { return "ident" },
			func() string { return "pick(" + g.boolLit() + ", _, " + g.expr(t, depth-1, sc) + ")" })
	}
	switch {
	case u.eq(tInt) && t.eq(tMeters):
		opts = append(opts, func() string { return "Meters" })
	case u.eq(tInt) && t.eq(tShape):
		opts = append(opts, func() string { return "Shape.Circle" })
	case t.k == "Opt" && t.args[0].eq(u):
		opts = append(opts, func() string { return "Opt.Has" })
	case t.k == "Maybe" && t.args[0].eq(u):
		opts = append(opts, func() string { return "Some" })
	case t.k == "List" && t.args[0].eq(u):
		opts = append(opts, func() string { return "wrap" })
	case t.k == "Box" && t.args[0].eq(u):
		opts = append(opts, func() string { return "box" })
	case t.k == "String" && g.displayable(u):
		opts = append(opts, func() string { return "show" })
	case u.k == "List" && t.eq(tInt):
		// A function over the wider `Iter<T>` where a `(List<T>) -> Int`
		// is expected.
		opts = append(opts, func() string { return "tally" }, func() string { return "Iter.count" })
	case u.k == "List" && t.k == "Maybe" && t.args[0].eq(u.args[0]):
		opts = append(opts, func() string { return "Iter.first" })
	}
	for _, v := range sc {
		if v.t.k == "Fn" && v.t.args[0].eq(u) && v.t.args[1].eq(t) {
			name := v.name
			opts = append(opts, func() string { return name })
		}
	}
	return g.pick(opts)
}

// expr is an expression of type t.
func (g *gen) expr(t *gt, depth int, sc []gvar) string {
	if depth <= 0 || g.chance(0.15) {
		return g.leaf(t, sc)
	}
	d := depth - 1
	other := func() *gt { return g.typ(1) }
	lit := func(t *gt) string { return g.expr(t, d, sc) }
	var forms []func() string
	add := func(fs ...func() string) { forms = append(forms, fs...) }
	add(
		func() string { return "ident(" + g.expr(t, d, sc) + ")" },
		func() string { return pipeHead(g.expr(t, d, sc)) + " |> ident()" + g.tapStage() },
		func() string { return "pick(" + g.boolLit() + ", " + g.expr(t, d, sc) + ", " + g.expr(t, d, sc) + ")" },
		func() string {
			v := gvar{g.fresh("b"), other()}
			if g.chance(0.5) {
				v.t = t
			}
			val := g.expr(v.t, d, sc)
			return "{\n" + v.name + " = " + val + "\n" + g.use(t, v, d, append(sc, v)) + "\n}"
		},
		func() string {
			return "if " + g.expr(tBool, d, sc) + " {\n" + g.expr(t, d, sc) + "\n} else {\n" + g.expr(t, d, sc) + "\n}"
		},
		func() string {
			x := g.fresh("m")
			body := g.use(t, gvar{x, t}, d, append(sc, gvar{x, t}))
			return "case Some(" + g.expr(t, d, sc) + ") {\nSome(" + x + ") -> " + body + "\nNone -> " + g.expr(t, d, sc) + "\n}"
		},
	)
	add(func() string {
		x := g.fresh("o")
		body := g.use(t, gvar{x, t}, d, append(sc, gvar{x, t}))
		return "case Opt.Has(" + g.expr(t, d, sc) + ") {\n.Has(" + x + ") -> " + body + "\n.Nothing -> " + g.expr(t, d, sc) + "\n}"
	})
	if g.local {
		add(
			func() string { return paren("Cell{c: "+lit(t)+"}") + ".c" },
			func() string { return paren("Cell({c: "+lit(t)+"})") + ".c" },
			func() string {
				x := g.fresh("o")
				body := g.use(t, gvar{x, t}, d, append(sc, gvar{x, t}))
				return "case Choice.One(" + g.expr(t, d, sc) + ") {\n.One(" + x + ") -> " + body + "\n.Zero -> " + g.expr(t, d, sc) + "\n}"
			},
		)
	}
	add(
		func() string {
			u := other()
			return "apply(" + g.expr(u, d, sc) + ", " + g.callback(u, t, d, sc, false) + ")"
		},
		func() string {
			u := other()
			x := g.fresh("p")
			body := g.use(t, gvar{x, u}, d, append(sc, gvar{x, u}))
			return pipeHead(g.expr(u, d, sc)) + "\n|> then |" + x + "| " + body + g.tapStage()
		},
		func() string { return paren("("+lit(t)+", "+lit(g.typ(1))+")") + ".0" },
		func() string { return paren("("+lit(g.typ(1))+", "+lit(t)+")") + ".1" },
		func() string {
			u := other()
			f := g.fresh("f")
			ft := of("Fn", u, t)
			held := g.aux.Intn(2) == 0
			// A held lambda's parameter is typed: the annotation on the
			// holder does not reach into a generic struct literal.
			cb := g.callback(u, t, d, sc, held)
			if held {
				// The function held in tuples and boxes, and called
				// through the chain that reads it (`f.v.0(x)`).
				holder, ht := g.holdFn(cb, ft)
				paths := fnPaths(ht, f, 4)
				return "{\n" + f + ": " + ht.String() + " = " + holder + "\n" + paths[len(paths)-1].chain + "(" + g.expr(u, d, sc) + ")\n}"
			}
			return "{\n" + f + ": " + ft.String() + " = " + cb + "\n" + f + "(" + g.expr(u, d, sc) + ")\n}"
		},
		func() string {
			f := g.fresh("partial")
			return "{\n" + f + " = pick(" + g.boolLit() + ", _, " + g.expr(t, d, sc) + ")\n" + f + "(" + g.expr(t, d, sc) + ")\n}"
		},
	)
	add(
		func() string { return "Maybe.with_default(Some(" + g.expr(t, d, sc) + "), " + g.expr(t, d, sc) + ")" },
		func() string {
			return "Result.with_default(" + g.okOf(t, g.expr(t, d, sc)) + ", " + g.expr(t, d, sc) + ")"
		},
		func() string { return "Result.with_default(Err(\"no\"), " + g.expr(t, d, sc) + ")" },
		func() string { return "first_or([" + lit(t) + ", " + lit(t) + "], " + g.expr(t, d, sc) + ")" },
		func() string { return "first_or([], " + g.expr(t, d, sc) + ")" },
		func() string { return "first_or(wrap(" + g.expr(t, d, sc) + "), " + g.expr(t, d, sc) + ")" },
		func() string { return paren("Box{v: "+lit(t)+"}") + ".v" },
		func() string {
			u := g.typ(1)
			acc, x := g.fresh("acc"), g.fresh("e")
			sc2 := append(append([]gvar{}, sc...), gvar{acc, t}, gvar{x, u})
			body := g.use(t, gvar{x, u}, d, sc2)
			if !uses(body, acc) {
				acc = "_" + acc
			}
			return "Iter.reduce([" + lit(u) + "], |" + acc + " = " + lit(t) + ", " + x + "| " + body + ")"
		},
		func() string {
			u := g.typ(1)
			return "first_or(Iter.map([" + lit(u) + "], " + g.callback(u, t, d, sc, false) + ") |> Iter.to_list(), " + g.expr(t, d, sc) + ")"
		},
		func() string {
			u := g.typ(1)
			return "Maybe.map(Some(" + g.expr(u, d, sc) + "), " + g.callback(u, t, d, sc, false) + ")\n|> Maybe.with_default(" + g.expr(t, d, sc) + ")"
		},
	)
	add(func() string { return "unbox(box(" + g.expr(t, d, sc) + "))" })
	// A block that declares a nested fn, in any expression position: a
	// list or tuple element, a struct field, an operand, a lambda or
	// `then` body, a pipe head, an interpolation.
	add(
		func() string {
			u := other()
			f, x := g.fresh("local"), g.fresh("y")
			body := g.use(t, gvar{x, u}, d, append(sc, gvar{x, u}))
			return "{\nfn " + f + "(" + x + ": " + u.String() + "): " + t.String() + " {\n" + body + "\n}\n" + f + "(" + g.expr(u, d, sc) + ")\n}"
		},
		func() string {
			f, v := g.fresh("attempt"), g.fresh("t")
			return "{\nfn " + f + "(): Result<" + t.String() + ", String> {\n" + v + " = try ok(" + g.expr(t, d, sc) + ")\nOk(" + v + ")\n}\n" +
				"case " + f + "() {\nOk(r) -> r\nErr(_) -> " + g.expr(t, d, sc) + "\n}\n}"
		},
	)
	switch t.k {
	case "Int":
		add(
			func() string { return paren(lit(tInt)) + " + " + paren(lit(tInt)) },
			func() string { return "case " + g.expr(tMeters, d, sc) + " {\nMeters(k) -> k\n}" },
			func() string { return "Iter.count(" + g.expr(of("List", g.typ(1)), d, sc) + ")" },
			// A callback over a list, where `tally` and `Iter.count` take
			// the wider `Iter<T>`.
			func() string {
				w := of("List", g.typ(1))
				return "apply(" + g.expr(w, d, sc) + ", " + g.callback(w, t, d, sc, false) + ")"
			},
			func() string {
				w := of("List", g.typ(1))
				return "first_or(Iter.map([" + lit(w) + "], " + g.callback(w, t, d, sc, false) + ") |> Iter.to_list(), " + g.expr(t, d, sc) + ")"
			},
		)
		for _, v := range sc {
			if v.t.eq(tShape) {
				v := v
				add(func() string { return g.use(t, v, d, sc) })
			}
		}
	case "String":
		add(
			func() string { return paren(lit(tString)) + " + " + paren(lit(tString)) },
			func() string { u := g.displayType(); return "show(" + g.expr(u, d, sc) + ")" },
			func() string { u := g.displayType(); return "Display.to_string(" + g.expr(u, d, sc) + ")" },
			func() string { u := g.displayType(); return `"[${` + lit(u) + `}]"` },
			func() string {
				u := g.displayType()
				return "String.join(Iter.map([" + lit(u) + "], " + g.callback(u, tString, d, sc, false) + "), \",\")"
			},
		)
	case "Bool":
		add(
			func() string { u := g.eqType(); return paren(lit(u)) + " == " + paren(lit(u)) },
			func() string { u := g.eqType(); return "same(" + g.expr(u, d, sc) + ", " + g.expr(u, d, sc) + ")" },
			func() string { return "Maybe.some?(Some(" + g.expr(g.typ(1), d, sc) + "))" },
			func() string {
				u := g.typ(1)
				return "Iter.any?([" + lit(u) + "], " + g.callback(u, tBool, d, sc, false) + ")"
			},
		)
	case "Meters":
		add(
			func() string { return "Meters(" + g.expr(tInt, d, sc) + ")" },
			func() string { return pipeHead(g.expr(tInt, d, sc)) + " |> Meters()" },
		)
	case "Shape":
		add(func() string { return "Shape.Circle(" + g.expr(tInt, d, sc) + ")" })
	case "List":
		u := t.args[0]
		add(
			func() string { return g.sortedStd(u, "["+lit(u)+", "+lit(u)+"]") },
			func() string { return "wrap(" + g.expr(u, d, sc) + ")" },
		)
		add(
			func() string {
				w := g.typ(1)
				return "Iter.map([" + lit(w) + "], " + g.callback(w, u, d, sc, false) + ") |> Iter.to_list()"
			},
			func() string {
				return "Iter.filter(" + g.expr(t, d, sc) + ", " + g.callback(u, tBool, d, sc, false) + ") |> Iter.to_list()"
			},
		)
	case "Maybe":
		u := t.args[0]
		add(func() string { return "Some(" + g.expr(u, d, sc) + ")" },
			func() string { return "pick(" + g.boolLit() + ", None, Some(" + g.expr(u, d, sc) + "))" })
	case "Result":
		u := t.args[0]
		add(func() string { return g.okOf(u, g.expr(u, d, sc)) },
			func() string { return "pick(" + g.boolLit() + ", Err(\"bad\"), Ok(" + g.expr(u, d, sc) + "))" })
	case "Tuple":
		add(func() string { return "(" + lit(t.args[0]) + ", " + lit(t.args[1]) + ")" })
	case "Box":
		u := t.args[0]
		add(func() string { return "Box{v: " + lit(u) + "}" },
			func() string { return "Box({v: " + lit(u) + "})" },
			func() string { return "box(" + g.expr(u, d, sc) + ")" })
	case "Opt":
		u := t.args[0]
		add(func() string { return "Opt.Has(" + g.expr(u, d, sc) + ")" },
			func() string { return "pick(" + g.boolLit() + ", Opt.Nothing, Opt.Has(" + g.expr(u, d, sc) + "))" })
	case "Fn":
		add(func() string { return g.callback(t.args[0], t.args[1], d, sc, true) })
	}
	return g.pick(forms)
}

// sortedStd is list, a list of u, sometimes sorted by a std function whose
// `Direction` or `Ordering` argument is a `.Variant` the program never
// imports the enum for: an owner call, a pipe, a defaulted parameter and a
// callback's result. It draws from g.aux.
func (g *gen) sortedStd(u *gt, list string) string {
	if u.k != "Int" && u.k != "String" {
		return list
	}
	dir := []string{".Ascending", ".Descending"}[g.aux.Intn(2)]
	switch g.aux.Intn(6) {
	case 0:
		return "Iter.sort(" + list + ", " + dir + ")"
	case 1:
		return list + " |> Iter.sort(" + dir + ")"
	case 2:
		x := g.fresh("k")
		return "Iter.sort_by(" + list + ", " + dir + ", |" + x + "| " + x + ")"
	case 3:
		a, b := g.fresh("a"), g.fresh("b")
		return list + " |> Iter.sort_with(|" + a + ", " + b + "| if " + a + " < " + b + " { .Greater } else { .Less })"
	}
	return list
}

// genGenericTypes are the generic types every generated program declares
// at file level, where the helpers and the derives can name them.
const genGenericTypes = `struct Box<T> {
v: T
}

enum Opt<T> {
Has T
Nothing
}
`

// genLocalGenericTypes are declared in the body of a program whose types
// are local, and built only there.
const genLocalGenericTypes = `struct Cell<T> {
c: T
}

enum Choice<T> {
One T
Zero
}
`

// genPlainTypes are declared at file level, or in the body when the
// program's types are local.
const genPlainTypes = `type Meters Int

enum Shape {
Circle Int
Square Int
Dot
}
`

const genDerives = `derive Equatable for Box<T>

derive Display for Box<T>

derive Equatable for Opt<T>
`

// genShapeDerive is declared with Shape at file level.
const genShapeDerive = `derive Equatable for Shape
`

// genHelpers are the generic functions every generated program declares:
// at file level, or, in a program whose types are local, in the body, each
// one the body uses.
const genHelpers = `fn ident<T>(x: T): T {
x
}

fn pick<T>(c: Bool, a: T, b: T): T {
if c { a } else { b }
}

fn wrap<T>(x: T): List<T> {
[x]
}

fn first_or<T>(xs: List<T>, d: T): T {
Maybe.with_default(List.head(xs), d)
}

fn apply<T, U>(x: T, f: (T) -> U): U {
f(x)
}

fn show<T>(x: T): String where T: Display {
Display.to_string(x)
}

fn tally<T>(xs: Iter<T>): Int {
Iter.count(xs)
}

fn same<T>(a: T, b: T): Bool where T: Equatable {
a == b
}

fn box<T>(x: T): Box<T> {
Box{v: x}
}

fn unbox<T>(b: Box<T>): T {
b.v
}

fn ok<T>(x: T): Result<T, String> {
Ok(x)
}
`

// genRecursiveHelpers are generic functions that call themselves: not in
// tail position, in tail position, mutually, through a lambda, and with a
// lambda whose parameter is a generic instance at the function's own type
// parameter. A program declares the ones it calls (usedRecursiveHelpers), at
// file level, since nested functions cannot call each other mutually. They
// name no other helper, so a program whose helpers are local can still call
// them.
const genRecursiveHelpers = `fn len_of<T>(xs: List<T>): Int {
case xs {
[] -> 0
[_, ..rest] -> 1 + len_of(rest)
}
}

fn count_from<T>(xs: List<T>, acc: Int): Int {
case xs {
[] -> acc
[_, ..rest] -> count_from(rest, acc + 1)
}
}

fn evens<T>(xs: List<T>): List<T> {
case xs {
[] -> []
[x, ..rest] -> [x, ..odds(rest)]
}
}

fn odds<T>(xs: List<T>): List<T> {
case xs {
[] -> []
[_, ..rest] -> evens(rest)
}
}

fn last_or<T>(xs: List<T>, d: T): T {
case xs {
[] -> d
[x, ..rest] -> last_or(rest, x)
}
}

fn drop_all<T>(xs: List<T>): Int {
case xs {
[] -> 0
[_, ..rest] -> Iter.map([rest], |r| 1 + drop_all(r)) |> Iter.reduce(|a = 0, b| a + b)
}
}

fn stack<T>(b: Box<T>, n: Int): List<Box<T>> {
1..=n |> Iter.reduce(|acc = [b], _| [b, ..acc])
}
`

// usedRecursiveHelpers is the genRecursiveHelpers body calls, each followed
// by a blank line: `evens` and `odds` together, since each calls the other.
// A program that calls none declares none, so it stays free of recursion
// and TestRunInputClassifies holds it to the loop-free rule.
func usedRecursiveHelpers(body string) string {
	var out strings.Builder
	for _, h := range strings.Split(strings.TrimSpace(genRecursiveHelpers), "\n\n") {
		name := strings.TrimPrefix(h, "fn ")
		name = name[:strings.IndexByte(name, '<')]
		used := uses(body, name)
		if name == "evens" || name == "odds" {
			used = uses(body, "evens") || uses(body, "odds")
		}
		if used {
			out.WriteString(h + "\n\n")
		}
	}
	return out.String()
}

// recursiveHelperCalled reports whether src calls one of genRecursiveHelpers.
func recursiveHelperCalled(src string) bool {
	for _, h := range strings.Split(strings.TrimSpace(genRecursiveHelpers), "\n\n") {
		name := strings.TrimPrefix(h, "fn ")
		if uses(src, name[:strings.IndexByte(name, '<')]) {
			return true
		}
	}
	return false
}

// recursiveCall is a statement showing one genRecursiveHelpers call over a
// binding the program made: the binding itself when it is a list, and
// otherwise a list of it. It draws from g.rec only, so the program around
// it is the one it would be without it.
func (g *gen) recursiveCall(sc []gvar) string {
	r := g.rec
	v := sc[r.Intn(len(sc))]
	if hasFn(v.t) {
		// gapListPatternOverFunctions: the helpers' list patterns over a
		// list of functions decline.
		v = sc[0]
	}
	list := "[" + v.name + ", " + v.name + "]"
	if v.t.k == "List" && r.Intn(2) == 0 {
		list = v.name
	}
	var call string
	switch r.Intn(7) {
	case 0:
		call = "len_of(" + list + ")"
	case 1:
		call = "count_from(" + list + ", " + fmt.Sprint(r.Intn(3)) + ")"
	case 2:
		call = "evens(" + list + ")"
	case 3:
		call = "odds(" + list + ")"
	case 4:
		call = "last_or([" + v.name + "], " + v.name + ")"
	case 5:
		call = "drop_all(" + list + ")"
	default:
		call = "stack(Box{v: " + v.name + "}, " + fmt.Sprint(r.Intn(3)) + ")"
	}
	return "io.inspect(" + call + ")\n"
}

// genLeafInners are the inner types leafDistinctBlock wraps, beside the
// types the rest of the generator draws: rt leaves (Decimal, Bytes, a
// Codepoint, Regex, Duration), Unit, and containers and distincts over
// them. Each is a type and a function of a small number giving a value of
// it; decl, when set, is a declaration the type needs, written in the same
// block.
var genLeafInners = []struct {
	typ, decl string
	val       func(n int) string
}{
	{typ: "Decimal", val: func(n int) string { return fmt.Sprintf("%d.%dd", n, n+1) }},
	{typ: "Bytes", val: func(n int) string { return fmt.Sprintf("String.to_bytes(\"b%d\")", n) }},
	{typ: "Maybe<Byte>", val: func(n int) string { return fmt.Sprintf("Bytes.at(String.to_bytes(\"b%d\"), 0)", n) }},
	{typ: "Codepoint", val: func(n int) string { return fmt.Sprintf("'%c'", 'a'+rune(n%26)) }},
	{typ: "Unit", val: func(int) string { return "Unit" }},
	{typ: "Regex", val: func(n int) string { return fmt.Sprintf("Regex`a{%d}`", n+1) }},
	{typ: "Duration", val: func(n int) string { return fmt.Sprintf("Duration.seconds(%d)", n) }},
	{typ: "List<Decimal>", val: func(n int) string { return fmt.Sprintf("[%d.5d, 0.25d]", n) }},
	{typ: "Maybe<Decimal>", val: func(n int) string { return fmt.Sprintf("Some(%d.5d)", n) }},
	{typ: "(Decimal, Bytes)", val: func(n int) string { return fmt.Sprintf("(%d.5d, String.to_bytes(\"t\"))", n) }},
	{typ: "Map<String, Decimal>", val: func(n int) string { return fmt.Sprintf("{\"k\" => %d.5d}", n) }},
	{typ: "Set<Decimal>", val: func(n int) string { return fmt.Sprintf("#{%d.5d}", n) }},
	{typ: "Vector<Bytes>", val: func(n int) string { return fmt.Sprintf("#[String.to_bytes(\"v%d\")]", n) }},
	{typ: "InnerAmount", decl: "type InnerAmount Decimal", val: func(n int) string { return fmt.Sprintf("InnerAmount(%d.5d)", n) }},
	{typ: "(Decimal) -> Decimal", val: func(n int) string { return fmt.Sprintf("|dd: Decimal| dd + %d.5d", n) }},
}

// leafDistinctBlock is a statement block that declares a distinct type over
// one of genLeafInners and builds, destructures, matches, shows and maps
// with it: the call form prefix or piped, a destructuring binding, a `case`
// arm, a destructuring lambda parameter, and the type name as a function
// value. A distinct over a function is called instead of shown. It draws
// from g.wrap only and names from g.wn, so the program around it is the one
// it would be without it.
func (g *gen) leafDistinctBlock() string {
	r := g.wrap
	g.wn++
	in := genLeafInners[r.Intn(len(genLeafInners))]
	name := fmt.Sprintf("Wrap%d", g.wn)
	v := func(i int) string { return fmt.Sprintf("w%d_%d", g.wn, i) }
	a, b := in.val(r.Intn(5)), in.val(r.Intn(5))
	var out strings.Builder
	out.WriteString("{\n")
	if in.decl != "" {
		out.WriteString(in.decl + "\n")
	}
	out.WriteString("type " + name + " " + in.typ + "\n")
	if r.Intn(2) == 0 {
		fmt.Fprintf(&out, "%s = %s(%s)\n", v(0), name, a)
	} else {
		fmt.Fprintf(&out, "%s = %s |> %s()\n", v(0), pipeHead(a), name)
	}
	fmt.Fprintf(&out, "%s(%s) = %s\n", name, v(1), v(0))
	show := func(x string) string { return x }
	if strings.HasPrefix(in.typ, "(Decimal) ->") {
		show = func(x string) string { return x + "(1.0d)" }
	}
	fmt.Fprintf(&out, "io.inspect(%s)\n", show(v(1)))
	if !strings.Contains(in.typ, "->") {
		fmt.Fprintf(&out, "io.inspect(%s)\n", v(0))
	}
	fmt.Fprintf(&out, "io.inspect(case %s {\n%s(%s) -> %s\n})\n", v(0), name, v(2), show(v(2)))
	fmt.Fprintf(&out, "%s = [%s, %s] |> Iter.map(%s) |> Iter.to_list()\n", v(3), a, b, name)
	fmt.Fprintf(&out, "io.inspect(Iter.map(%s, |%s(%s)| %s) |> Iter.to_list())\n", v(3), name, v(4), show(v(4)))
	out.WriteString("}\n")
	return out.String()
}

// divergingBlock is a statement block holding an `Iter.loop`, or an
// Iter.map, filter, each, reduce, find, any?, all?, drop_while or flat_map
// callback or a Map.map_values one, whose tail `case` or `if`
// leaves in every arm: by `break`, `break v`, `continue` and `return v`, in
// bare arms and block arms, with a `case` nested in an arm, and as a binding
// whose arms all leave followed by statements that never run. A loop counts
// up to a limit, and a `continue` sits on an arm no value takes, so every
// program ends. It reads an Int the program bound earlier when there is one.
// It draws from g.div only and names from g.dn, so the program around it is
// the one it would be without it.
func (g *gen) divergingBlock(sc []gvar) string {
	r := g.div
	g.dn++
	name := fmt.Sprintf("dv%d", g.dn)
	src := func() string {
		var ints []string
		for _, v := range sc {
			switch v.t.k {
			case "Int":
				ints = append(ints, v.name)
			case "List":
				ints = append(ints, "Iter.count("+v.name+")")
			}
		}
		if len(ints) == 0 || r.Intn(3) == 0 {
			return fmt.Sprint(r.Intn(10))
		}
		return ints[r.Intn(len(ints))]
	}
	// leave is an arm body ending in exit, sometimes a block that shows
	// shown first.
	leave := func(exit, shown string) string {
		if r.Intn(2) == 0 {
			return exit
		}
		return "{\nio.inspect(" + shown + ")\n" + exit + "\n}"
	}
	// arms are a `case` on an Int's arms: one stops with stop, one no
	// value reaches goes round with `continue`, and the wildcard, last,
	// leaves with next. The first two come in either order.
	arms := func(subject, limit, stop, next string) string {
		first := []string{
			limit + " -> " + leave(stop, subject),
			"-1 -> " + leave("continue", subject),
		}
		if r.Intn(2) == 0 {
			first[0], first[1] = first[1], first[0]
		}
		return first[0] + "\n" + first[1] + "\n_ -> " + leave(next, subject) + "\n"
	}
	limit := fmt.Sprint(1 + r.Intn(3))
	stop := "break di + " + src()
	next := "return di + 1"
	var loopBody string
	switch r.Intn(5) {
	case 0:
		loopBody = "case di {\n" + arms("di", limit, stop, next) + "}"
	case 1:
		// A `case` nested in the wildcard arm, whose arms all leave too.
		nested := "case di < " + limit + " {\nTrue -> " + leave(next, "di") + "\nFalse -> " + leave("continue", "di") + "\n}"
		loopBody = "case di {\n" + limit + " -> " + leave(stop, "di") + "\n-1 -> " + leave("continue", "di") +
			"\n_ -> {\n" + nested + "\n}\n}"
	case 2:
		loopBody = "if di == " + limit + " {\n" + leave(stop, "di") + "\n} else {\n" + leave(next, "di") + "\n}"
	case 3:
		// A binding whose arms all leave: what follows it never runs.
		loopBody = "dy = case di {\n" + arms("di", limit, stop, next) + "}\nio.inspect(di)\ndy"
	default:
		loopBody = "case di > " + limit + " {\nTrue -> " + leave("break di", "di") + "\nFalse -> " + leave(next, "di") + "\n}"
	}
	loop := "Iter.loop(|di = 0| {\n" + loopBody + "\n})"
	xs := "[1, 2, 3]"
	switch r.Intn(9) {
	case 7:
		// The other callback positions: find, any?, all?, drop_while,
		// flat_map and Map.map_values.
		pred := "|dx| case dx {\n" + arms("dx", "2", []string{"break", "break True", "break False"}[r.Intn(3)], "return dx == 1") + "}"
		switch r.Intn(6) {
		case 0, 1, 2:
			op := []string{"find", "any?", "all?"}[r.Intn(3)]
			return "{\n" + name + " = Iter." + op + "(" + xs + ", " + pred + ")\nio.inspect(" + name + ")\n}\n"
		case 3:
			return "{\n" + name + " = Iter.drop_while(" + xs + ", " + pred + ") |> Iter.to_list()\nio.inspect(" + name + ")\n}\n"
		case 4:
			cb := "|dx| case dx {\n" + arms("dx", "2", "break [dx, "+src()+"]", "return [dx, dx]") + "}"
			return "{\n" + name + " = Iter.flat_map(" + xs + ", " + cb + ") |> Iter.to_list()\nio.inspect(" + name + ")\n}\n"
		}
		cb := "|dx| case dx {\n" + arms("dx", "2", "break dx + "+src(), "return dx * 10") + "}"
		return "{\n" + name + " = Map.map_values({\"a\" => 1, \"b\" => 2, \"c\" => 3}, " + cb + ")\nio.inspect(" + name + ")\n}\n"
	case 0, 1, 2:
		return "{\n" + name + " = " + loop + "\nio.inspect(" + name + ")\n}\n"
	case 3:
		// The loop inside a lambda.
		lam := strings.ReplaceAll(loop, "|di = 0|", "|di = dn|")
		return "{\n" + name + " = |dn: Int| " + lam + "\nio.inspect(" + name + "(0))\n}\n"
	case 4:
		var cb string
		switch r.Intn(3) {
		case 0:
			cb = "|dx| case dx {\n" + arms("dx", "2", "break dx + "+src(), "return dx") + "}"
		case 1:
			cb = "|dx| if dx < 2 {\n" + leave("continue", "dx") + "\n} else {\n" + leave("break dx + "+src(), "dx") + "\n}"
		default:
			cb = "|dx| {\ndy = case dx {\n" + arms("dx", "2", "break dx", "return dx") + "}\nio.inspect(dx)\ndy\n}"
		}
		return "{\n" + name + " = Iter.map(" + xs + ", " + cb + ") |> Iter.to_list()\nio.inspect(" + name + ")\n}\n"
	case 5:
		cb := "|dx| case dx {\n" + arms("dx", "2", "break", "return dx == 1") + "}"
		return "{\n" + name + " = Iter.filter(" + xs + ", " + cb + ") |> Iter.to_list()\nio.inspect(" + name + ")\n}\n"
	case 6:
		cb := "|dx| case dx {\n" + arms("dx", "2", "break", "continue") + "}"
		return "{\nIter.each(" + xs + ", " + cb + ")\n}\n"
	}
	cb := "|dacc = " + src() + ", dx| case dx {\n" + arms("dx", "3", "break dacc + dx", "return dacc + dx") + "}"
	return "{\n" + name + " = Iter.reduce(" + xs + ", " + cb + ")\nio.inspect(" + name + ")\n}\n"
}

// genDisplayValues are concrete values of types that implement Display, as
// functions of a small number: scalars in each register bank, a String, a
// derived generic struct and a list.
var genDisplayValues = []func(n int) string{
	func(n int) string { return fmt.Sprint(n) },
	func(n int) string { return fmt.Sprintf("\"e%d\"", n) },
	func(n int) string { return []string{"True", "False"}[n%2] },
	func(n int) string { return fmt.Sprintf("%d.5", n) },
	func(n int) string { return fmt.Sprintf("Box{v: %d}", n) },
}

// erasedBlock is a statement block that stores concrete values where
// Display's existential is expected, in every position that moves a value
// into it: a nested fn's result and early `return`, `if` and `case` arms as
// a result and under a binding annotation, an annotated binding, an
// argument, a partial application's bound argument, a struct field, a
// list's elements, a lambda's result under an annotated function type, a
// list held as Display itself, a tuple's part, a map's values and a
// record's field paired by `Iter.with_index`. Each value is then shown
// through the existential. It draws from g.dyn
// only and names from g.en, so the program around it is the one it would be
// without it.
func (g *gen) erasedBlock() string {
	r := g.dyn
	g.en++
	name := func(s string) string { return fmt.Sprintf("%s%d", s, g.en) }
	val := func() string { return genDisplayValues[r.Intn(len(genDisplayValues))](r.Intn(10)) }
	show := func(e string) string { return "io.print(Display.to_string(" + e + "))\n" }
	var out strings.Builder
	out.WriteString("{\n")
	switch r.Intn(13) {
	case 9:
		a := genDisplayValues[r.Intn(len(genDisplayValues))]
		fmt.Fprintf(&out, "%s: Display = [%s, %s]\n", name("ev"), a(1), a(2))
		out.WriteString(show(name("ev")))
	case 10:
		fmt.Fprintf(&out, "%s: (Display, Int) = (%s, %d)\n(%s, %s) = %s\n", name("ev"), val(), r.Intn(10),
			name("ea"), name("eb"), name("ev"))
		fmt.Fprintf(&out, "io.print(\"${Display.to_string(%s)} ${Int.to_string(%s)}\")\n", name("ea"), name("eb"))
	case 11:
		fmt.Fprintf(&out, "%s: Map<String, Display> = Map.put(Map.put(Map.empty(), \"a\", %s), \"b\", %s)\n", name("ev"), val(), val())
		fmt.Fprintf(&out, "io.print(Map.values(%s) |> Iter.map(Display.to_string) |> String.join(\",\"))\n", name("ev"))
	case 12:
		fmt.Fprintf(&out, "%s: Display = %s\n%s = [{n: 1, d: %s}]\n", name("ed"), val(), name("ev"), name("ed"))
		fmt.Fprintf(&out, "io.print(%s |> Iter.with_index() |> Iter.map(|(i, x)| \"${Int.to_string(i)}:${Display.to_string(x.d)}\") |> String.join(\",\"))\n", name("ev"))
	case 0:
		fmt.Fprintf(&out, "fn %s(): Display {\n%s\n}\n", name("er"), val())
		out.WriteString(show(name("er") + "()"))
	case 1:
		fmt.Fprintf(&out, "fn %s(c: Bool): Display {\nif c {\nreturn %s\n}\n%s\n}\n", name("er"), val(), val())
		out.WriteString(show(name("er") + "(" + g.boolLitFrom(r) + ")"))
	case 2:
		a, b := genDisplayValues[r.Intn(len(genDisplayValues))], r.Intn(10)
		fmt.Fprintf(&out, "fn %s(c: Bool): Display {\nif c { %s } else { %s }\n}\n", name("er"), a(b), a(b+1))
		out.WriteString(show(name("er") + "(" + g.boolLitFrom(r) + ")"))
	case 3:
		a := genDisplayValues[r.Intn(len(genDisplayValues))]
		fmt.Fprintf(&out, "%s: Display = case %d {\n0 -> %s\n_ -> %s\n}\n", name("ev"), r.Intn(2), a(1), a(2))
		out.WriteString(show(name("ev")))
	case 4:
		a := genDisplayValues[r.Intn(len(genDisplayValues))]
		fmt.Fprintf(&out, "%s: Display = if %s { %s } else { %s }\n", name("ev"), g.boolLitFrom(r), a(3), a(4))
		out.WriteString(show(name("ev")))
	case 5:
		fmt.Fprintf(&out, "fn %s(d: Display, n: Int): String {\n\"${Display.to_string(d)}/${Int.to_string(n)}\"\n}\n", name("ef"))
		if r.Intn(2) == 0 {
			fmt.Fprintf(&out, "io.print(%s(%s, 1))\n", name("ef"), val())
		} else {
			fmt.Fprintf(&out, "%s = %s(%s, _)\nio.print(%s(2))\n", name("ep"), name("ef"), val(), name("ep"))
		}
	case 6:
		fmt.Fprintf(&out, "struct %s {\nd: Display\n}\n", name("Held"))
		fmt.Fprintf(&out, "%s = %s{d: %s}\n", name("ev"), name("Held"), val())
		out.WriteString(show(name("ev") + ".d"))
	case 7:
		fmt.Fprintf(&out, "%s: List<Display> = [%s, %s]\n", name("ev"), val(), val())
		fmt.Fprintf(&out, "io.print(%s |> Iter.map(Display.to_string) |> String.join(\",\"))\n", name("ev"))
	default:
		a := genDisplayValues[r.Intn(len(genDisplayValues))]
		fmt.Fprintf(&out, "%s: (Bool) -> Display = |c| if c { %s } else { %s }\n", name("ev"), a(5), a(6))
		out.WriteString(show(name("ev") + "(" + g.boolLitFrom(r) + ")"))
	}
	out.WriteString("}\n")
	return out.String()
}

// fixedKindBlock is a statement block that writes a value where its type
// is already fixed by what surrounds it: an empty list literal after or
// before a typed one (a lambda's and a nested fn's returns, a recursive
// nested fn's tail, `break` in an `Iter.map` callback, `break`, `return`
// and the tail of an `Iter.loop` over `List<Int>` state, a tuple compared
// with another, a generic struct's field), or a value of an embedded type
// where its embedding enum's `Iter` is expected (an annotated binding, a
// distinct tuple's payload, a field default, a spread, `Struct.update`, a
// Vector's elements, and Captured holding the embedded type). It draws
// from g.fix only and names from g.fn, so the program around it is the one
// it would be without it.
func (g *gen) fixedKindBlock() string {
	r := g.fix
	g.fn++
	name := func(s string) string { return fmt.Sprintf("%s%d", s, g.fn) }
	n := r.Intn(4)
	var out strings.Builder
	out.WriteString("{\n")
	fig, pt, fc := name("Fig"), name("Fig")+"."+name("Pt"), name("fc")
	embeds := func() {
		fmt.Fprintf(&out, "struct %s {\nr: Int\n}\n", name("Ring"))
		fmt.Fprintf(&out, "enum %s {\nembeds %s\n%s\n}\n", fig, name("Ring"), name("Pt"))
		fmt.Fprintf(&out, "%s = %s{r: %d}\n", fc, name("Ring"), n)
	}
	switch r.Intn(12) {
	case 0:
		fmt.Fprintf(&out, "%s = |x: Int| {\nif x > 1 {\nreturn []\n}\nif x < 1 {\nreturn [x]\n}\n[x, x]\n}\n", name("fl"))
		fmt.Fprintf(&out, "io.inspect(%s(%d))\n", name("fl"), n)
	case 1:
		fmt.Fprintf(&out, "fn %s(x: Int): List<Int> {\nif x > 1 {\nreturn [x]\n}\n[]\n}\n", name("fe"))
		fmt.Fprintf(&out, "io.inspect(%s(%d))\n", name("fe"), n)
	case 2:
		fmt.Fprintf(&out, "fn %s(x: Int): List<Int> {\nif x > 0 {\nio.inspect(%s(x - 1))\n}\n[]\n}\n", name("fr"), name("fr"))
		fmt.Fprintf(&out, "io.inspect(%s(%d))\n", name("fr"), n)
	case 3:
		fmt.Fprintf(&out, "%s = [1, 2, 3]\n|> Iter.map(|x| {\nif x > %d {\nbreak []\n}\nif x > 1 {\nbreak [x]\n}\n[x, x]\n})\n|> Iter.to_list()\n", name("fm"), n)
		fmt.Fprintf(&out, "io.inspect(%s)\n", name("fm"))
	case 4:
		exits := []string{"break []", "return []", "break acc"}
		fmt.Fprintf(&out, "%s = Iter.loop(|acc: List<Int> = [1]| {\nif Iter.count(acc) > %d {\nbreak []\n}\nif Iter.count(acc) == 0 {\n%s\n}\n[1, ..acc]\n})\n", name("fo"), n, exits[r.Intn(len(exits))])
		fmt.Fprintf(&out, "io.inspect(%s)\n", name("fo"))
	case 5:
		rhs := []string{"[]", fmt.Sprintf("[%d]", n)}[r.Intn(2)]
		fmt.Fprintf(&out, "io.print((%d, []) == (%d, %s))\n", n, n, rhs)
	case 6:
		fmt.Fprintf(&out, "%s: Captured<List<Int>> = Captured{value: [], output: \"\", transcript: \"\"}\n", name("fk"))
		fmt.Fprintf(&out, "io.inspect(%s.value)\n", name("fk"))
	case 7:
		embeds()
		fmt.Fprintf(&out, "%s: Iter<%s> = [%s, %s]\n", name("fi"), fig, fc, pt)
		fmt.Fprintf(&out, "io.print(Iter.count(%s))\n", name("fi"))
	case 8:
		embeds()
		fmt.Fprintf(&out, "type %s (Iter<%s>, Int)\n", name("Pair"), fig)
		fmt.Fprintf(&out, "%s(%s, %s) = %s([%s], %d)\n", name("Pair"), name("fi"), name("fq"), name("Pair"), fc, n)
		fmt.Fprintf(&out, "io.print(Iter.count(%s) + %s)\n", name("fi"), name("fq"))
	case 9:
		embeds()
		fmt.Fprintf(&out, "struct %s {\nit: Iter<%s> = [%s{r: 1}]\nn: Int = 0\n}\n", name("Held"), fig, name("Ring"))
		fmt.Fprintf(&out, "%s = %s{}\n", name("fh"), name("Held"))
		switch r.Intn(3) {
		case 0:
			fmt.Fprintf(&out, "%s = {..%s, it: [%s, %s]}\n", name("fg"), name("fh"), fc, fc)
		case 1:
			fmt.Fprintf(&out, "%s = Struct.update(updates: {it: [%s, %s]}, original: %s)\n", name("fg"), fc, fc, name("fh"))
		default:
			fmt.Fprintf(&out, "%s = %s{it: [%s], n: Iter.count(%s.it)}\n", name("fg"), name("Held"), fc, name("fh"))
		}
		fmt.Fprintf(&out, "io.print(Iter.count(%s.it))\n", name("fg"))
	case 10:
		embeds()
		if r.Intn(2) == 0 {
			fmt.Fprintf(&out, "%s: Vector<Iter<%s>> = #[[%s], [%s, %s]]\n", name("fv"), fig, fc, pt, fc)
		} else {
			fmt.Fprintf(&out, "%s: Vector<Captured<%s>> = #[Captured{value: %s, output: \"\", transcript: \"\"}]\n", name("fv"), fig, fc)
		}
		fmt.Fprintf(&out, "io.print(Vector.length(%s))\n", name("fv"))
	default:
		fmt.Fprintf(&out, "%s: Vector<Iter<Int>> = #[[%d], [1, 2]]\n", name("fv"), n)
		fmt.Fprintf(&out, "io.print(%s |> Iter.map(Iter.count) |> Iter.reduce(|t = 0, c| t + c))\n", name("fv"))
	}
	out.WriteString("}\n")
	return out.String()
}

// callBlock is a statement block of calls placed by name, functions passed
// as values and the sources std's concurrency functions take: named
// arguments to the concurrency functions in any order, a named function
// spawned as a task, and `Task.spawn_all` over each kind of Iter.
func (g *gen) callBlock() string {
	r := g.call
	g.cn++
	name := func(s string) string { return fmt.Sprintf("%s%dc", s, g.cn) }
	n := r.Intn(4)
	var out strings.Builder
	out.WriteString("{\n")
	switch r.Intn(8) {
	case 6:
		// A lambda with a default, captured by another closure and called
		// there short, by name and partially.
		calls := []string{"%s(2)", "%s(y: 3, x: 2)", "%s(_, 3)(2)", "%s(2, _)(4)"}
		call := fmt.Sprintf(calls[r.Intn(len(calls))], name("cf"))
		dflt := fmt.Sprintf("x + %d", n)
		if strings.Contains(call, "_") {
			// A partial over a slot whose default is not a literal is
			// gapPartialOpenNonliteralDefault.
			dflt = fmt.Sprint(n)
		}
		fmt.Fprintf(&out, "%s = |x: Int, y: Int = %s| x * y\n", name("cf"), dflt)
		if strings.Contains(call, "_") {
			// A call through a call's result is its own gap; bind first.
			call = strings.Replace(call, ")(", ")\n"+name("cq")+"(", 1)
			call = name("cq") + " = " + call
			fmt.Fprintf(&out, "%s = || {\n%s\n}\n", name("cg"), call)
		} else {
			fmt.Fprintf(&out, "%s = || %s\n", name("cg"), call)
		}
		fmt.Fprintf(&out, "io.inspect(%s())\n", name("cg"))
	case 7:
		// A partial over a generic helper whose instantiation leaves a
		// hole: the Result's error type.
		fmt.Fprintf(&out, "%s = pick(%s, _, Some(Ok(%d)))\n", name("cp"), g.boolLitFrom(r), n)
		fmt.Fprintf(&out, "io.inspect(%s(None))\n", name("cp"))
	case 5:
		// A generic helper with a destructuring parameter and a default
		// that reads its names, called with named arguments. A nested fn
		// cannot destructure yet, so a local program skips it.
		if g.local {
			break
		}
		g.helpers = append(g.helpers, fmt.Sprintf("fn %s<A, B>((a, b): (A, B), keep: Bool = False, fallback: A = a): (B, A) {\n"+
			"if keep {\n(b, fallback)\n} else {\n(b, a)\n}\n}", name("csw")))
		fmt.Fprintf(&out, "io.inspect(%s(keep: True, (%d, \"x\")))\n", name("csw"), n)
		fmt.Fprintf(&out, "io.inspect(%s((%d, %s), fallback: 9, keep: %s))\n", name("csw"), n, g.boolLitFrom(r), g.boolLitFrom(r))
	case 0:
		fmt.Fprintf(&out, "%s = concurrent {\n%s = Task.spawn(body: || %d)\nTask.await(task: %s)\n}\n", name("ct"), name("tk"), n, name("tk"))
		fmt.Fprintf(&out, "io.inspect(%s)\n", name("ct"))
	case 1:
		fmt.Fprintf(&out, "%s = Channel.buffered<Int>(1)\n", name("ch"))
		if r.Intn(2) == 0 {
			fmt.Fprintf(&out, "_ = Sender.send(value: %d, sender: %s.sender)\n", n, name("ch"))
		} else {
			fmt.Fprintf(&out, "_ = Sender.send(%s.sender, value: %d)\n", name("ch"), n)
		}
		fmt.Fprintf(&out, "io.inspect(Receiver.receive(receiver: %s.receiver))\n", name("ch"))
	case 2:
		fmt.Fprintf(&out, "%s = Context.with_value(value: %d, c: Context.root())\n", name("cx"), n)
		fmt.Fprintf(&out, "io.inspect(Context.value(value_type: Int, c: %s))\n", name("cx"))
	case 3:
		fmt.Fprintf(&out, "fn %s(): Int {\n%d\n}\n", name("cw"), n)
		fmt.Fprintf(&out, "%s = concurrent {\nTask.await(Task.spawn(%s))\n}\n", name("ct"), name("cw"))
		fmt.Fprintf(&out, "io.inspect(%s)\n", name("ct"))
	default:
		sources := []string{fmt.Sprintf("#[%d, 2]", n), fmt.Sprintf("#{%d}", n), fmt.Sprintf("1..=%d", n+1),
			fmt.Sprintf("[%d] |> Iter.map(|x: Int| x + 1)", n), fmt.Sprintf("[%d]", n)}
		fmt.Fprintf(&out, "fn %s(x: Int): Int {\nx * 2\n}\n", name("cd"))
		fmt.Fprintf(&out, "%s = concurrent {\nTask.await_all(Task.spawn_all(%s, %s, max_running: %d))\n}\n",
			name("ct"), sources[r.Intn(len(sources))], name("cd"), n+1)
		fmt.Fprintf(&out, "io.inspect(%s)\n", name("ct"))
	}
	out.WriteString("}\n")
	return out.String()
}

// boolLitFrom is a Bool literal drawn from r.
func (g *gen) boolLitFrom(r *rand.Rand) string {
	return []string{"True", "False"}[r.Intn(2)]
}

// generatedProgram is program i.
func generatedProgram(i int) loweringSeed { return generatedProgramSized(i, 3, 3) }

// generatedProgramSized is program i with up to bindings bindings, each an
// expression up to depth deep. Small sizes make readable reproducers.
func generatedProgramSized(i, bindings, depth int) loweringSeed {
	g := &gen{r: rand.New(rand.NewSource(int64(i))), aux: rand.New(rand.NewSource(int64(i) + 1<<32)),
		div: rand.New(rand.NewSource(int64(i) + 2<<32)), rec: rand.New(rand.NewSource(int64(i) + 3<<32)),
		wrap: rand.New(rand.NewSource(int64(i) + 4<<32)), dyn: rand.New(rand.NewSource(int64(i) + 5<<32)),
		fix: rand.New(rand.NewSource(int64(i) + 6<<32)), call: rand.New(rand.NewSource(int64(i) + 7<<32))}
	g.local = g.chance(0.3)
	asTest := g.chance(0.3)
	var body strings.Builder
	sc := []gvar{{"s0", tShape}}
	body.WriteString("s0 = " + g.leaf(tShape, nil) + "\n")
	n := 1 + g.r.Intn(bindings)
	for j := 0; j < n; j++ {
		t := g.typ(2)
		name := fmt.Sprintf("v%d", j+1)
		val := g.expr(t, depth, sc)
		if g.chance(0.4) {
			fmt.Fprintf(&body, "%s: %s = %s\n", name, t, val)
		} else {
			fmt.Fprintf(&body, "%s = %s\n", name, val)
		}
		fmt.Fprintf(&body, "io.inspect(%s)\n", name)
		if g.aux.Intn(3) == 0 {
			body.WriteString(g.stmtBlock(name, t.String(), 0))
		}
		sc = append(sc, gvar{name, t})
		if g.div.Intn(3) == 0 {
			body.WriteString(g.divergingBlock(sc))
		}
		if g.rec.Intn(2) == 0 {
			body.WriteString(g.recursiveCall(sc))
		}
		if g.wrap.Intn(3) == 0 {
			body.WriteString(g.leafDistinctBlock())
		}
		if g.dyn.Intn(3) == 0 {
			body.WriteString(g.erasedBlock())
		}
		if g.fix.Intn(3) == 0 {
			body.WriteString(g.fixedKindBlock())
		}
		if g.call.Intn(3) == 0 {
			body.WriteString(g.callBlock())
		}
	}
	body.WriteString("io.inspect(s0)\n")
	// One program in four spells its output through an aliased selective
	// import (`import std/io.{inspect as peek}`), called bare and passed as
	// a function value. Decided by i alone, so no other program changes.
	selective := i%4 == 3
	if selective {
		text := strings.ReplaceAll(body.String(), "io.inspect(", "peek(")
		body.Reset()
		body.WriteString(text + "Iter.each([s0], peek)\n")
	}
	if asTest {
		body.WriteString("assert Iter.count([s0]) == 1\n")
	}
	var src strings.Builder
	helpers, localHelpers := genHelpers, ""
	if g.local {
		// A nested fn nothing reads is an error, so only the used ones.
		var used []string
		for _, h := range strings.Split(strings.TrimSpace(genHelpers), "\n\n") {
			name := strings.TrimPrefix(h, "fn ")
			name = name[:strings.IndexByte(name, '<')]
			if uses(body.String(), name) || uses(strings.Join(g.helpers, "\n"), name) {
				used = append(used, h)
			}
		}
		helpers, localHelpers = "", strings.Join(used, "\n\n")+"\n\n"
	}
	src.WriteString(helpers + "\n" + usedRecursiveHelpers(body.String()) + genGenericTypes + "\n" + genDerives + "\n")
	if !g.local {
		src.WriteString(genPlainTypes + "\n" + genShapeDerive + "\n" + strings.Join(g.helpers, "\n\n") + "\n\n")
	}
	if asTest {
		src.WriteString("test \"generated\" {\n")
	} else {
		src.WriteString("fn main() {\n")
	}
	if g.local {
		src.WriteString(genPlainTypes + "\n" + genLocalGenericTypes + "\n" + localHelpers + strings.Join(g.helpers, "\n\n") + "\n\n")
	}
	src.WriteString(body.String() + "}\n")
	out := src.String()
	switch {
	case !selective:
		out = "import std/io\n\n" + out
	case uses(out, "io"):
		out = "import std/io\nimport std/io.{inspect as peek}\n\n" + out
	default:
		out = "import std/io.{inspect as peek}\n\n" + out
	}
	// leafDistinctBlock's std types outside the prelude.
	if strings.Contains(out, "Regex`") {
		out = "import std/regex.Regex\n" + out
	}
	// fixedKindBlock's Captured.
	if strings.Contains(out, "Captured{") {
		out = "import std/io.Captured\n" + out
	}
	// callBlock's std types outside the prelude.
	if strings.Contains(out, "Task.") {
		out = "import std/tasks.Task\n" + out
	}
	if strings.Contains(out, "Channel.") {
		out = "import std/channels.{Channel, Receiver, Sender}\n" + out
	}
	if strings.Contains(out, "Duration.seconds(") {
		out = "import std/duration.Duration\n" + out
	}
	if formatted, err := format.Format(out); err == nil {
		out = formatted
	}
	return loweringSeed{name: fmt.Sprintf("gen/%03d", i), src: out}
}

// generatedCount is how many generated programs plain `go test` checks.
const generatedCount = 150

func generatedPrograms() []loweringSeed {
	out := make([]loweringSeed, generatedCount)
	for i := range out {
		out[i] = generatedProgram(i)
	}
	return out
}
