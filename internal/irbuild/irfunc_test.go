package irbuild

// Which function bodies the builder retains, one construct at a time.
//
// A body `irScalarBuild` declines retains nothing, so a regression in what
// the builder takes shows up here as a row that flips.

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// irFuncRetentionCount installs the observation hook and reports what one
// lowering retained against what it walked, for one of the two populations.
//
// `want` filters by origin because `stdlibLowering()` is a process-wide
// `sync.Once`: whether a hook also sees the stdlib bodies depends on whether
// an earlier test in the same binary already ran it, so a counter that
// filtered on nothing would read differently depending on test order. See
// irstdbody.go.
type irFuncRetentionCount struct {
	retained []string
	// readBack is the subset the reader read back off the graph, kept apart
	// from retained because a body can be retained without being read back.
	readBack []string
	// notReadBack is the complement, named rather than counted, so a body
	// retained without read-back is identified.
	notReadBack []string
	// unreadSource are bodies left unread whose declaration is not
	// derive-synthesized. Only synthesized bodies may skip read-back.
	unreadSource []string
	walked       []string
	temps        int
	instrs       int
	blocks       int
}

func (c *irFuncRetentionCount) observe(t *testing.T, want irFuncOrigin) func() {
	t.Helper()
	prev := irFuncObserved
	irFuncObserved = func(origin irFuncOrigin, name string, f *ir.Func, readBack bool) {
		if origin != want {
			return
		}
		if f == nil {
			c.walked = append(c.walked, name)
			return
		}
		c.retained = append(c.retained, name)
		if readBack {
			c.readBack = append(c.readBack, name)
		} else {
			c.notReadBack = append(c.notReadBack, name)
			if blocks := f.Blocks(); len(blocks) == 0 || !blocks[0].Pos().Synthesized() {
				c.unreadSource = append(c.unreadSource, name)
			}
		}
		c.temps += f.NumTemps()
		c.blocks += len(f.Blocks())
		for _, b := range f.Blocks() {
			c.instrs += len(b.Instrs())
		}
	}
	return func() { irFuncObserved = prev }
}

// TestIRFunc_TheShapeAdmitsAndDeclines is the shape's boundary, pinned one
// construct at a time, because a corpus count says how many matched and not
// which rule decided.
//
// Every declining row is a near miss: one construct away from the shape, and
// each names the class that owns it. That is what stops the shape from
// silently widening into a class somebody else is routing.
func TestIRFunc_TheShapeAdmitsAndDeclines(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		retained bool
		why      string
	}{
		{"int literal and local", "fn f(x: Int): Int {\n  x * 2\n}\n", true, ""},
		{"a tree with a hoisted left operand", "fn f(a: Int): Int {\n  a + (a % 7) + 1\n}\n", true, ""},
		{"float division", "fn f(x: Float): Float {\n  x / 2.0\n}\n", true, ""},
		{"unary minus", "fn f(x: Int): Int {\n  -x\n}\n", true, ""},
		{"parenthesised", "fn f(a: Int, b: Int): Int {\n  (a + b) * 2\n}\n", true, ""},
		{"float modulo", "fn f(x: Float): Float {\n  x % 2.0\n}\n", true, ""},
		{"a binding and then the expression", "fn f(x: Int): Int {\n  y = x * 2\n  y + 1\n}\n", true, ""},
		{"two bindings", "fn f(a: Int, b: Int): Int {\n  p = a + 1\n  q = b * 2\n  p + q\n}\n", true, ""},
		{"a binding read twice", "fn f(a: Int): Int {\n  h = a % 3\n  h * h\n}\n", true, ""},
		{"a binding of a literal", "fn f(a: Int): Int {\n  k = 7\n  a + k\n}\n", true, ""},
		{"a binding whose value reads an earlier binding", "fn f(a: Int): Int {\n  p = a + 1\n  q = p * 2\n  p + q\n}\n", true, ""},

		{"a block-valued binding", "fn f(x: Int): Int { y = { a = x + 1 a * 2 } y + 1 }", true,
			"a typed result slot outlives the block's fresh local bindings"},
		{"a block binding with a conditional tail", "fn f(x: Int): Int { y = { if x < 0 { 0 } else { x } } y }", false,
			"block result subregions remain outside straight block retention"},
		{"an ad-hoc case", "fn f(x: Int): Int { case { x < 0 -> 0 _ -> x } }", true,
			"ordinary Bool selectors share the case chain"},
		{"an ad-hoc case with branching condition", "fn f(a: Bool, b: Bool): Int { case { a and b -> 1 _ -> 0 } }", true,
			"a short-circuit condition ends in its own join block, whose terminator is the arm's branch"},
		{"an annotated binding", "fn f(x: Int): Int {\n  y: Int = x * 2\n  y + 1\n}\n", true,
			"the annotated initializer uses the same retained value and coercion path"},
		{"a block result requiring empty-list coercion", "fn f(): List<Int> { xs: List<Int> = { [] } xs }", false,
			"the block's own result type differs from the binding's contextual type"},
		{"a branch default requiring result coercion", "fn f(flag: Bool): List<Int> { make = |xs: List<Int> = if flag { [] } else { [] }| xs make() }", false,
			"the supplier's inferred branch slot differs from its parameter kind"},
		{"a discarded binding", "fn f(x: Int): Int {\n  _ = x * 2\n  x + 1\n}\n", true,
			"a discard declares nothing, so it is the value plus two drops, the value's and the statement's own Unit. See irdiscard.go"},
		{"an ANNOTATED discarded binding", "fn f(x: Int): Int {\n  _y: Int = x * 2\n  x + 1\n}\n", true,
			"an annotated discard checks its value without declaring a local"},
		{"a tail Unit", "fn f(): Unit {\n  Unit\n}\n", true,
			"`ir.ConstUnit`, one of the names `lower`'s TypeIdent arm answers"},
		{"a binding shadowing a parameter", "fn f(x: Int): Int {\n  x = x * 2\n  x + 1\n}\n", true,
			"the body scope shadows the parameter with a fresh IR identity"},
		{"a binding rebound in the body", "fn f(a: Int): Int {\n  y = a * 2\n  y = y + 1\n  y\n}\n", true,
			"each local binding has a fresh IR identity and native storage delivery"},
		{"a binding of a field access", "struct P {\n  x: Int\n}\n\nfn f(p: P): Int {\n  y = p.x\n  y + 1\n}\n", true,
			"`proj` at a binding's value; the arm is `lower`'s and a binding's value goes through it"},
		{"a leading non-binding statement", "fn f(x: Int): Int {\n  io.print(\"hi\")\n  x + 1\n}\n", true,
			"a Unit-valued expression statement, which is what a program that prints several lines is"},
		{"a call at a leaf in a binding", "fn g(n: Int): Int {\n  n\n}\nfn f(x: Int): Int {\n  y = g(x)\n  y + 1\n}\n", true,
			"`call`, direct, all operands positional"},
		{"a call at a leaf", "fn g(n: Int): Int {\n  n\n}\nfn f(x: Int): Int {\n  g(x) + 1\n}\n", true,
			"the same call in an operand position, hoisted by the ir.Copy gen.operand's rule builds"},
		{"a named argument", "fn g(n: Int): Int {\n  n\n}\nfn f(x: Int): Int {\n  g(n: x) + 1\n}\n", true,
			"argSlotPlan places named operands before constructing the call"},
		{"an if body", "fn f(a: Int, b: Int): Int {\n  if a > b { a } else { b }\n}\n", true,
			"the condition is a comparison, an `ir.Compare`"},
		{"a tail if over a Bool", "fn f(c: Bool, a: Int, b: Int): Int {\n  if c { a } else { b }\n}\n", true,
			"a Branch over two arms, each writing the function's result slot and jumping to one exit"},
		{"a tail if with no else", "fn f(c: Bool): Unit {\n  if c { Unit }\n}\n", true,
			"admitted when the region is Unit-valued. An `if` with no `else` is Unit whichever way it goes, so the else edge writes Unit to the result slot and the arms agree"},
		{"a comparison", "fn f(a: Int, b: Int): Bool {\n  a > b\n}\n", true,
			"`ir.Compare`, a separate node from arithmetic whose destination is always a Bool"},
		{"string concatenation", "fn f(s: String): String {\n  s + \"!\"\n}\n", true,
			"`ir.Concat`, N-ary"},
		{"a tail case over Int literals", "fn f(n: Int): String {\n  case n {\n    0 -> \"z\"\n    _ -> \"m\"\n  }\n}\n", true,
			"ir.NewMatchLitInto, whose answer a Branch reads"},
		{"a tail case with no wildcard", "fn f(c: Bool): String {\n  case c {\n    True -> \"t\"\n    False -> \"f\"\n  }\n}\n", true,
			"Boolean variants share answering MatchVariant tests and the native predicate reader"},
		{"a tail case over an impure scrutinee", "fn f(n: Int): String {\n  case n + 1 {\n    0 -> \"z\"\n    _ -> \"m\"\n  }\n}\n", true,
			"the shared hold delivery evaluates a computed subject once"},
		{"decimal", "fn f(d: Decimal): Decimal {\n  d + 1.5d\n}\n", true,
			"exact-text constants and shared Decimal arithmetic"},
		{"a field access at a leaf", "struct P {\n  x: Int\n}\n\nfn f(p: P): Int {\n  p.x * 2\n}\n", true,
			"`ir.ProjField` at a leaf"},
		{"a named type", "type M Int\n\nimpl Add<M, M> for M {\n  fn add(lhs: M, rhs: M): M { _ = rhs;\n    lhs\n  }\n}\n\nfn f(m: M): M {\n  m + m\n}\n", true,
			"local Add impl dispatch names its retained declaration"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import std/io\n\n" + tc.body + "\nfn main() {\n  io.print(\"go\")\n}\n"
			var c irFuncRetentionCount
			restore := c.observe(t, irFromModule)
			defer restore()
			lowerIROnly(t, src)

			retained := false
			for _, n := range c.retained {
				if n == "f" {
					retained = true
				}
			}
			seen := false
			for _, n := range append(append([]string(nil), c.retained...), c.walked...) {
				if n == "f" {
					seen = true
				}
			}
			if !seen {
				t.Fatalf("`f` never reached funcDecl's emit path, so this row measures "+
					"nothing: retained %v, walked %v", c.retained, c.walked)
			}
			if retained != tc.retained {
				if tc.retained {
					t.Errorf("`f` should be %s and was walked instead", irScalarArithShape)
					return
				}
				t.Errorf("`f` was retained as %s, but it is one construct outside it: %s",
					irScalarArithShape, tc.why)
			}
		})
	}
}

// TestIRFunc_ARetainedFunctionAnswersForItsOwnTemporaries is the "a Temp is a
// value" claim, checked on the production producer rather than on a
// hand-built function.
//
// internal/ir's own TestFunc_DefIsWhatMakesATempAValue checks the mechanism.
// This checks that `internal/irbuild` actually uses it: every temporary an
// instruction reads in a retained corpus-shaped function is defined by an
// instruction in that same function, and `Func.Def` names it.
func TestIRFunc_ARetainedFunctionAnswersForItsOwnTemporaries(t *testing.T) {
	src := `import std/io

fn step(acc: Int): Int {
  acc + (acc % 7) + 1
}

fn many(a: Int, b: Int, c: Int, d: Int): Int {
  a * 1000 + b * 100 + c * 10 + d
}

fn main() {
  io.print("v = ${step(3)} ${many(1, 2, 3, 4)}")
}
`
	var funcs []*ir.Func
	prev := irFuncObserved
	irFuncObserved = func(_ irFuncOrigin, _ string, f *ir.Func, _ bool) {
		if f != nil {
			funcs = append(funcs, f)
		}
	}
	defer func() { irFuncObserved = prev }()
	lowerIROnly(t, src)

	// `main` is retained as well: its body is `io.print` over an
	// interpolation, so it also carries the `ir.Render` and `ir.Concat` this
	// check walks.
	if len(funcs) != 3 {
		t.Fatalf("expected three retained functions, got %d", len(funcs))
	}
	// A parameter's temporary is defined at entry and by no instruction, so
	// `Func.Def` answers nil for one, which is why the check below is over
	// what instructions read. Nothing in a body reads a parameter's register
	// directly: the builder emits an `ir.Ref` whose own destination is the
	// operand, which is what `ir.Func.Params()` and `RefLocal` divide
	// between them.
	uses := 0
	for _, f := range funcs {
		for _, b := range f.Blocks() {
			var buf []ir.Temp
			for _, in := range b.Instrs() {
				for _, u := range in.AppendUses(buf[:0]) {
					uses++
					if f.Def(u) == nil {
						t.Errorf("%s: %s reads %s and nothing in the function defines it",
							f.Name(), in.String(), u)
					}
				}
			}
			if b.Term() == nil {
				t.Errorf("%s: %s has no terminator", f.Name(), b.ID())
				continue
			}
			for _, u := range b.Term().AppendUses(nil) {
				uses++
				if f.Def(u) == nil {
					t.Errorf("%s: %s reads %s and nothing in the function defines it",
						f.Name(), b.Term().String(), u)
				}
			}
		}
	}
	if uses < 10 {
		t.Fatalf("only %d operand reads across two retained functions, which cannot be "+
			"right — the check above would be nearly vacuous", uses)
	}
	t.Logf("%d operand reads, every one answered by Func.Def", uses)
}
