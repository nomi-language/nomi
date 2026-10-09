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
// statement blocks, some nested, declare types of their own. The body is a
// `fn main` or a test.
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
	aux     *rand.Rand
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
		add(func() string { return "Int(" + n + ")" },
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

// tapStage is, one time in three, a `tap` stage to end a pipeline with: one
// that shows the piped value or one that discards it. Otherwise nothing. It
// draws from g.aux, and names its parameter apart from g.fresh's names, so
// the program around it is the one it would be without the stage.
func (g *gen) tapStage() string {
	switch g.aux.Intn(6) {
	case 0:
		return "\n|> tap |tapped| io.inspect(tapped)"
	case 1:
		return "\n|> tap |tapped| { _ = tapped }"
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
			func() string { return "Int(" + g.expr(tMeters, d, sc) + ")" },
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

// generatedProgram is program i.
func generatedProgram(i int) loweringSeed { return generatedProgramSized(i, 3, 3) }

// generatedProgramSized is program i with up to bindings bindings, each an
// expression up to depth deep. Small sizes make readable reproducers.
func generatedProgramSized(i, bindings, depth int) loweringSeed {
	g := &gen{r: rand.New(rand.NewSource(int64(i))), aux: rand.New(rand.NewSource(int64(i) + 1<<32))}
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
	src.WriteString(helpers + "\n" + genGenericTypes + "\n" + genDerives + "\n")
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
