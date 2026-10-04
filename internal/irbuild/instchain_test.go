package irbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Does this repository contain an unbounded generic instantiation chain?
//
// A generic that recurses at a strictly larger type argument has an infinite
// instantiation set:
//
//	fn tail_grow<T>(x: T, depth: Int): Int {
//	  if depth <= 0 { 0 } else { tail_grow(Box{inner: x}, depth - 1) }
//	}
//	fn deep_grow<T>(x: T, depth: Int): Int {
//	  if depth <= 0 { 0 } else { deep_grow(Box{inner: x}, depth - 1) + 1 }
//	}
//
// Both `nomi check` clean and have an answer, and a builder that instantiates
// generics per program cannot build them without a bound (`monoInstCap`). The
// generic functions this repository ships that self-recurse all recurse at
// their own type argument, so each instantiates once and keeps its self-call.
//
// This test fails when a committed generic grows its own type argument, which
// is when the bound starts refusing real programs. It is re-derived on every
// run rather than pinned to a list of names.
//
// The test asserts an absence, so the detector must be shown to fire:
// TestGenerics_InstantiationChainDetectorControls feeds it the two programs
// above and requires a hit on each. The two halves are one mechanism used
// twice, which is what makes the zero a measurement.

// growthSite is one recursive call that enlarges its own type argument.
type growthSite struct {
	where string
	fn    string
	param string
	arg   string
	line  int
}

func (g growthSite) String() string {
	return fmt.Sprintf("%s: fn %s, parameter %q receives %s at line %d",
		g.where, g.fn, g.param, g.arg, g.line)
}

// TestGenerics_NoUnboundedInstantiationChainInTheRepository is the measurement.
func TestGenerics_NoUnboundedInstantiationChainInTheRepository(t *testing.T) {
	sites, scanned, recursive := instantiationChainScan(t)

	if scanned == 0 {
		t.Fatal("scanned zero generic functions; the walk is broken, and a broken " +
			"walk is indistinguishable from a clean repository")
	}
	t.Logf("scanned %d generic function declarations; %d self-recurse",
		scanned, len(recursive))
	sort.Strings(recursive)
	for _, r := range recursive {
		t.Logf("    self-recursive (same type argument): %s", r)
	}

	if len(sites) != 0 {
		var b strings.Builder
		for _, s := range sites {
			fmt.Fprintf(&b, "\n  %s", s)
		}
		t.Fatalf("%d generic function(s) recurse at a STRICTLY LARGER type argument:%s\n\n"+
			"Such a function has an infinite instantiation set, so a builder that "+
			"instantiates generic functions per program refuses it at monoInstCap. "+
			"Decide whether the program(s) above should be refused before accepting "+
			"this.", len(sites), b.String())
	}
}

// TestGenerics_InstantiationChainDetectorControls is the control table, and it
// is not optional: the test above asserts an ABSENCE, so without controls a
// detector that matched nothing would pass it forever.
//
// The negative rows attribute each clause. The repo scan and the tail_grow
// witness share the whole detector, so together they cannot tell which clause
// is load-bearing. Each row below exercises exactly one clause, and the
// `clause` field names the code that must fail if the row does:
//
//	growingArgs' annotation test   — the `List<T>` accumulator row
//	kindOfConstruction             — the bare-`T` identity row
//	selfCalls' *ast.Ident test     — the qualified look-alike row
//
// The negative rows are shapes that occur in std: the accumulator row is
// `random.draw_list`, and the qualified look-alike is `sets.remove` calling
// `Map.remove`.
func TestGenerics_InstantiationChainDetectorControls(t *testing.T) {
	box := "struct Box<T> {\n  inner: T\n}\n\n"
	for _, tc := range []struct {
		name      string
		clause    string
		src       string
		wantFire  bool
		wantRecur int
	}{{
		// The tail-position witness from the file header.
		name:      "tail_grow fires",
		clause:    "the whole detector",
		src:       box + "fn tail_grow<T>(x: T, depth: Int): Int {\n  if depth <= 0 { 0 } else { tail_grow(Box{inner: x}, depth - 1) }\n}\n",
		wantFire:  true,
		wantRecur: 1,
	}, {
		// The non-tail witness, so tail-call handling is ruled out as a
		// confounder: a detector that only saw the tail shape would miss it.
		name:      "deep_grow fires",
		clause:    "the whole detector, non-tail",
		src:       box + "fn deep_grow<T>(x: T, depth: Int): Int {\n  if depth <= 0 { 0 } else { deep_grow(Box{inner: x}, depth - 1) + 1 }\n}\n",
		wantFire:  true,
		wantRecur: 1,
	}, {
		// `random.draw_list`'s shape: a construction at a `List<T>` position
		// rebuilds the SAME outer constructor, so `T` never changes. If
		// growingArgs stopped reading the annotation this row would fire and
		// std's accumulator loops would be called unbounded.
		name:      "same-T accumulator does NOT fire",
		clause:    "growingArgs' annotation test",
		src:       "fn drain<T>(items: List<T>, acc: List<T>): List<T> {\n  case items {\n    [] -> acc\n    [head, ..tail] -> drain(tail, [head, ..acc])\n  }\n}\n",
		wantFire:  false,
		wantRecur: 1,
	}, {
		// Self-recursive at a BARE `T`, but passing the value through unchanged.
		// If kindOfConstruction admitted an identifier this would fire.
		name:      "bare-T identity recursion does NOT fire",
		clause:    "kindOfConstruction",
		src:       "fn again<T>(x: T, n: Int): T {\n  if n <= 0 { x } else { again(x, n - 1) }\n}\n",
		wantFire:  false,
		wantRecur: 1,
	}, {
		// `sets.remove`'s shape: the body calls `Map.remove`, a QUALIFIED call to
		// another type's method that merely shares the name. It must not even be
		// counted as recursion — hence wantRecur 0, which is the assertion that
		// isolates selfCalls rather than growingArgs.
		name:      "qualified look-alike is not recursion at all",
		clause:    "selfCalls' *ast.Ident test",
		src:       "fn wrap<T>(x: T): Box<T> {\n  Box{inner: Box.wrap(x)}\n}\n",
		wantFire:  false,
		wantRecur: 0,
	}, {
		// A turbofish grows the chain with no argument at any position, so
		// `range c.Args` iterates zero times; without the turbofish arm the
		// scan would answer "no growth" by never asking.
		name:      "turbofish composed over the type parameter fires",
		clause:    "growingArgs' turbofish arm (the argument loop is empty for a turbofish)",
		src:       "fn grow<T>(): Int {\n  grow<Box<T>>()\n}\n",
		wantFire:  true,
		wantRecur: 1,
	}, {
		// The same spelling at a BARE type argument is the SAME instantiation,
		// so it must not fire. Without this row, a turbofish arm that flagged
		// every explicit type argument would pass the row above and be wrong.
		name:      "turbofish at a bare type argument does NOT fire",
		clause:    "composedOverTypeParam's bare-SimpleType arm",
		src:       "fn same<T>(): Int {\n  same<T>()\n}\n",
		wantFire:  false,
		wantRecur: 1,
	}, {
		// A composed turbofish whose text merely contains the type parameter's
		// name as a substring (`T` inside `Toml`). The spelling `conc<Toml>()`
		// would not test this: it is a bare *ast.SimpleType, so
		// composedOverTypeParam returns at its first arm and
		// mentionsTypeParamName is never reached. `List<Toml>` is composed, so
		// it reaches the whole-token test, and a substring check fires here and
		// is wrong.
		name:      "composed turbofish merely CONTAINING the parameter name does NOT fire",
		clause:    "mentionsTypeParamName's whole-token test",
		src:       "fn conc<T>(): Int {\n  conc<List<Toml>>()\n}\n",
		wantFire:  false,
		wantRecur: 1,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			nodes, err := parser.Parse(lexer.Lex(tc.src))
			if err != nil {
				t.Fatalf("control source does not parse: %v", err)
			}
			sites, scanned, recursive := scanNodesForGrowth("control", nodes)
			if scanned == 0 {
				t.Fatal("the control declares a generic function and the walk found none")
			}
			if got := len(recursive); got != tc.wantRecur {
				t.Errorf("self-recursive functions = %d, want %d (%v); the clause under "+
					"test is %s", got, tc.wantRecur, recursive, tc.clause)
			}
			if fired := len(sites) > 0; fired != tc.wantFire {
				t.Fatalf("detector fired = %v, want %v. THE CLAUSE UNDER TEST IS %s — "+
					"if this row regressed, that is the code that changed meaning. "+
					"sites=%v", fired, tc.wantFire, tc.clause, sites)
			}
		})
	}
}

// instantiationChainScan walks every .nomi source this repository ships.
//
// std comes through std.Load() (the embedded copy the compiler actually uses),
// and tests off disk. PARSED rather than analyzed, because the
// question is about what is WRITTEN: a file the checker rejects for an unrelated
// reason still declares its generics, and a silently dropped file is the
// undercount that would turn this guard into decoration. A parse failure is
// therefore reported loudly.
func instantiationChainScan(t *testing.T) (sites []growthSite, scanned int, recursive []string) {
	t.Helper()

	lib := std.Load()
	var mods []string
	for name := range lib.Nodes {
		mods = append(mods, name)
	}
	sort.Strings(mods)
	for _, m := range mods {
		s, n, r := scanNodesForGrowth("std/"+m, lib.Nodes[m])
		sites, scanned, recursive = append(sites, s...), scanned+n, append(recursive, r...)
	}

	for _, dir := range []string{"tests"} {
		root, err := filepath.Abs(filepath.Join("..", "..", dir))
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".nomi") {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		sort.Strings(paths)
		for _, path := range paths {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			nodes, perr := parser.Parse(lexer.Lex(string(src)))
			if perr != nil {
				t.Errorf("PARSE FAILED, so this file was scanned as zero generic "+
					"declarations and the total below is an undercount: %s: %v", path, perr)
				continue
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			s, n, r := scanNodesForGrowth(dir+"/"+rel, nodes)
			sites, scanned, recursive = append(sites, s...), scanned+n, append(recursive, r...)
		}
	}
	return sites, scanned, recursive
}

// scanNodesForGrowth reports every growing recursive call among the generic
// functions declared anywhere in nodes.
func scanNodesForGrowth(where string, nodes []ast.Node) (sites []growthSite, scanned int, recursive []string) {
	for _, fd := range allFuncDefs(nodes) {
		if len(fd.TypeParams) == 0 || fd.Body == nil {
			continue
		}
		scanned++
		params := map[string]bool{}
		for _, tp := range fd.TypeParams {
			params[tp.Name] = true
		}
		calls := selfCalls(fd)
		if len(calls) == 0 {
			continue
		}
		recursive = append(recursive, where+": fn "+fd.Name)
		for _, c := range calls {
			sites = append(sites, growingArgs(where, fd, params, c)...)
		}
	}
	return sites, scanned, recursive
}

// growingArgs is the arguments of one recursive call that enlarge a type
// argument.
//
// The rule: a chain grows when a parameter annotated with a bare type
// parameter receives a construction. `tail_grow`'s `x: T` receiving
// `Box{inner: x}` binds `T` to `Box<T>` at every hop, so the set is
// `Int`, `Box<Int>`, `Box<Box<Int>>`, … without end.
//
// A construction at a position annotated `List<T>` is NOT growth and must not be
// reported as such: `draw_list`'s `acc: List<T>` receiving `[x, ..acc]` rebuilds
// the SAME outer constructor, so `T` is unchanged and the function specializes
// once. That distinction is why this reads the annotation and not merely the
// argument: flagging a construction at any type-parameter-mentioning position
// would report std's accumulator loops as unbounded.
//
// A different outer constructor at a `C<T>` position would also grow, but the
// checker rejects that, so it is not enumerated here.
//
// # The turbofish is read separately
//
// `grow<Box<T>>()` binds `T` to `Box<T>` through `ast.Call.TypeArgs` with no
// argument at any position, so a loop over `c.Args` alone would answer "no
// growth" by never asking. The builder refuses a zero-argument generic
// (`genericSignature` requires every type parameter in a bare parameter
// position), but this scan reads source rather than what lowers.
func growingArgs(where string, fd *ast.FuncDef, params map[string]bool, c *ast.Call) []growthSite {
	var out []growthSite
	// The turbofish, first, because it needs no arguments to grow a chain.
	for _, ta := range c.TypeArgs {
		if composedOverTypeParam(ta, params) == "" {
			continue
		}
		line, _ := nodePos(c)
		out = append(out, growthSite{
			where: where, fn: fd.Name, param: "explicit type argument",
			arg: composedOverTypeParam(ta, params), line: line,
		})
	}
	for i, arg := range c.Args {
		if i >= len(fd.Params) {
			break
		}
		p := fd.Params[i]
		st, isSimple := p.TypeAnnotation.(*ast.SimpleType)
		if !isSimple || !params[st.Name] {
			continue
		}
		if kindOfConstruction(arg) == "" {
			continue
		}
		line, _ := nodePos(arg)
		out = append(out, growthSite{
			where: where, fn: fd.Name, param: p.Name + ": " + st.Name,
			arg: kindOfConstruction(arg), line: line,
		})
	}
	return out
}

// composedOverTypeParam names the type argument's shape when it MENTIONS one of
// the function's own type parameters inside a larger type, or "".
//
// A bare `T` is the same type argument the activation already has, so
// `again<T>(x)` inside `fn again<T>` is not growth — that is the same
// same-`T` distinction the annotation test makes on the argument side, and the
// `bare type argument` control row pins it. Anything composed — `Box<T>`,
// `List<T>`, `(T, Int)` — is a strictly larger instance at every hop.
func composedOverTypeParam(te ast.TypeExpr, params map[string]bool) string {
	if st, isSimple := te.(*ast.SimpleType); isSimple {
		// Bare: either a type parameter (same argument) or a concrete type.
		_ = st
		return ""
	}
	text := typeText(te)
	for name := range params {
		if !mentionsTypeParamName(text, name) {
			continue
		}
		return "an explicit type argument composed over " + name + " (" + text + ")"
	}
	return ""
}

// mentionsTypeParamName reports whether text uses name as a whole identifier.
//
// Whole-token rather than substring, because a type parameter `T` would
// otherwise match `Toml`, `Task` and every other capitalised name in a rendered
// type — the false-positive direction, which for a guard that FAILS a build is
// as damaging as the false negative.
func mentionsTypeParamName(text, name string) bool {
	for i := 0; i+len(name) <= len(text); i++ {
		if text[i:i+len(name)] != name {
			continue
		}
		beforeOK := i == 0 || !isIdentByte(text[i-1])
		afterOK := i+len(name) == len(text) || !isIdentByte(text[i+len(name)])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// kindOfConstruction names the construction arg is, or "" when arg cannot
// introduce a new outer type constructor.
//
// An identifier, a field access, a call result or a binding all denote a value
// whose type the annotation already fixes; only a LITERAL of a composite shape
// wraps its operand in a new constructor. `*ast.Call` is deliberately absent:
// `f(x)`'s result type is `f`'s declared return type, so it cannot be a strictly
// larger instance of the CALLER's own type parameter unless `f` is itself
// growing, which this scan catches at `f`'s own declaration.
func kindOfConstruction(n ast.Node) string {
	switch n.(type) {
	case *ast.StructLit:
		return "a struct literal"
	case *ast.ListLit:
		return "a list literal"
	case *ast.ListSpreadLit:
		return "a list spread literal"
	case *ast.TupleLit:
		return "a tuple literal"
	case *ast.MapLit:
		return "a map literal"
	case *ast.SetLit:
		return "a set literal"
	}
	return ""
}

// selfCalls is every call to fd's own name inside fd's body.
//
// A bare `*ast.Ident` callee only. `Owner.name(...)` is a qualified call to
// another method that happens to share a name, such as `std/sets.remove`'s
// call to `Map.remove`.
func selfCalls(fd *ast.FuncDef) []*ast.Call {
	var out []*ast.Call
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if c, isCall := n.(*ast.Call); isCall {
			if id, isIdent := c.Func.(*ast.Ident); isIdent && id.Name == fd.Name {
				out = append(out, c)
			}
		}
		for _, ch := range childNodes(n) {
			walk(ch)
		}
	}
	walk(fd.Body)
	return out
}

// allFuncDefs is every function declaration reachable from nodes, at any depth.
//
// Any depth because a generic `fn` is declared at top level, inside an `impl`
// block, inside an `interface`, and nested in another body, and a scan with one
// arm covers one provenance and misses the rest.
func allFuncDefs(nodes []ast.Node) []*ast.FuncDef {
	var out []*ast.FuncDef
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if fd, isFn := n.(*ast.FuncDef); isFn {
			out = append(out, fd)
		}
		for _, ch := range childNodes(n) {
			walk(ch)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}
