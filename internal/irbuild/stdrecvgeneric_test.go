package irbuild

import (
	"sort"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestStdSubsetKeyNeverNamesATypeParameter is the INVARIANT the receiver-generic
// arm exists to hold: `stdlib function outside the scalar subset` says a
// REPRESENTATION is missing, so no declaration refused under it may have a
// signature position holding a TYPE PARAMETER — there is no type there to
// represent, and no anchor family can ever anchor one.
//
// ASKS THE GRAPH, NOT THE FUNCTION. The population is every impl block std
// declares, walked here and re-derived from `analysis.ReceiverTypeParamNames`,
// not a hand-written list of declarations. A list would pass
// by omission the moment std grows a `impl Hashable for Deque<T>`, which is the
// width problem a table-driven agreement guard has by construction: it is only
// as wide as its table. This one has no table.
//
// The check runs against `stdlibLowering().byKey`, i.e. against what the builder
// BELIEVES after the whole index is built, rather than against a re-derivation of
// the genericity rule. Re-deriving it would compare the rule with itself.
func TestStdSubsetKeyNeverNamesATypeParameter(t *testing.T) {
	const subsetKey = "stdlib function outside the scalar subset"
	idx := stdlibLowering()
	lib := std.Load()

	modules := make([]string, 0, len(lib.Nodes))
	for m := range lib.Nodes {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	// generic counts the declarations the arm is responsible for, so a rule that
	// stopped firing altogether cannot pass this test by having nothing to check.
	generic := 0
	for _, module := range modules {
		fa := lib.Files[module]
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		declared := func(n string) bool { return fa.ModuleScope.Lookup(n) != nil }
		for _, n := range lib.Nodes[module] {
			ib, isImpl := n.(*ast.ImplBlock)
			if !isImpl {
				continue
			}
			tp := map[string]bool{}
			for _, name := range analysis.ReceiverTypeParamNames(ib.Receiver, declared) {
				if name != "" {
					tp[name] = true
				}
			}
			if len(tp) == 0 {
				continue
			}
			recv := analysis.TypeExprBaseName(ib.Receiver)
			ifaceKey := stdImplKey(ib.Interface)
			for _, item := range ib.Items {
				var name string
				var params []ast.Param
				var ret ast.TypeExpr
				switch d := item.(type) {
				case *ast.FuncDef:
					name, params, ret = d.Name, d.Params, d.ReturnTypeExpr
				case *ast.ExternFunc:
					name, params, ret = d.Name, d.Params, d.ReturnTypeExpr
				default:
					continue
				}
				if !sigNamesTypeParam(params, ret, tp) {
					continue
				}
				generic++
				key := stdKey(module, recv, ifaceKey, name)
				f := idx.byKey[key]
				if f == nil {
					// A key collapse absorbed it: `addOverload` keeps ONE entry
					// per key by construction, so a spelling several
					// declarations answer to holds only one of them.
					//
					// This population is empty in today's std. The arm is kept,
					// and its emptiness is NOT asserted: that would make an
					// unrelated std edit fail here, and a collapse is `stdKey`'s
					// property rather than this test's.
					//
					// It is **UNREACHED IN PRACTICE, LIVE IN PRINCIPLE**, not
					// unreachable by construction:
					// `bytes.Bytes.to_string` is a genuine two-claimant key
					// today, so a std edit could populate this arm tomorrow
					// without anything here changing. **Watch the POPULATION.**
					//
					// The event that voids the reason above: `addOverload`
					// gaining a second slot per key, or `stdKey` growing enough
					// qualification that no two std declarations share a
					// spelling. Either makes this arm dead rather than merely
					// unreached, and it should then be deleted, not re-explained.
					continue
				}
				if f.why == subsetKey {
					t.Errorf("%s is refused as %q, but its signature names the type parameter(s) %v "+
						"contributed by `impl … for %s`. That key claims a REPRESENTATION is missing; "+
						"a type parameter is not a missing representation and no anchor can anchor one. "+
						"It belongs on `stdlib generic function`, which stdCandidateFor tests FIRST for "+
						"exactly this reason. See stdrecvgeneric.go.",
						key, f.why, tpNames(tp), typeText(ib.Receiver))
				}
			}
		}
	}
	// ANTI-VACUITY. The population this loop walks, every std declaration whose
	// signature names a receiver-contributed type parameter, is about 161.
	//
	// The floor of 31 is deliberately low, and its job is narrow: it catches the
	// rule collapsing ENTIRELY, which is the failure that would make the
	// invariant above unfalsifiable. It does NOT catch partial degradation, and
	// a floor at the full population would churn on every std addition. The
	// wide check is the LOOP, which visits every declaration individually;
	// TestReceiverGenericityIsPerSignatureNotPerBlock pins three named shapes
	// (bare parameter, container, function type) so a rule that stopped seeing
	// one of them fails by name rather than by count.
	if generic < 31 {
		t.Errorf("only %d std declaration(s) name a receiver-contributed type parameter; "+
			"the floor here is 31. Either ReceiverTypeParamNames "+
			"stopped deriving them or sigNamesTypeParam stopped seeing them, and the "+
			"invariant above is then vacuous rather than satisfied", generic)
	}
}

func tpNames(tp map[string]bool) []string {
	out := make([]string, 0, len(tp))
	for n := range tp {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TestReceiverGenericityIsPerSignatureNotPerBlock pins the one design choice in
// stdrecvgeneric.go that a reviewer would plausibly simplify away.
//
// The obvious implementation marks the whole BLOCK generic when its receiver
// contributes a type parameter — which is what the front end's SCOPE does. It is
// wrong here, and `impl Range<T>` is the witness: `from` and `naturals` return a
// CONCRETE `Range<Int>` and mention no parameter, so a block-level rule would
// move them onto `stdlib generic function` and trade one false reason for
// another. They are admitted by genStructSigKind instead.
//
// So the two halves of this test are not two examples of one thing. `Map.hash`
// is the positive: the rule must fire. `Range.from` is the negative: the rule
// must NOT fire on a concrete signature in a generic block, and it is the only
// thing separating this implementation from the simpler wrong one.
func TestReceiverGenericityIsPerSignatureNotPerBlock(t *testing.T) {
	idx := stdlibLowering()
	for _, c := range []struct {
		key  string
		want string
		why  string
	}{
		{"maps.Map.hash", "stdlib generic function",
			"`hash(m: Map<K, V>): Int` names both of `impl Hashable for Map<K, V>`'s " +
				"receiver-contributed parameters, so the rule must fire. The corpus reaches " +
				"it from 12-derives-and-standard-interfaces/collections_test.nomi."},
		{"lists.List.equal?", "stdlib generic function",
			"`equal?(a: List<T>, b: List<T>): Bool` — a CONTAINER over the parameter " +
				"rather than the bare parameter, which is what the population is dominated " +
				"by and what a head-only mention test would miss."},
		{"iter.Seq.each_while", "stdlib generic function",
			"`each_while(s: Seq<T>, yield: (T) -> Bool): Bool` — the parameter reached " +
				"through a FUNCTION type, the third shape typeNamesAny has to walk."},
	} {
		f := idx.byKey[c.key]
		if f == nil {
			t.Errorf("%s: absent from the index; std moved or renamed it, and this row's "+
				"claim is unchecked (%s)", c.key, c.why)
			continue
		}
		if f.why != c.want {
			t.Errorf("%s is refused as %q, want %q.\n%s", c.key, f.why, c.want, c.why)
		}
	}
	// The negative, and the reason it is in this test rather than in a separate
	// one: it is the SAME rule asked about a signature in the same generic block.
	// Splitting them lets one be deleted without the other looking incomplete.
	for _, key := range []string{"ranges.Range.from", "ranges.Range.naturals"} {
		f := idx.byKey[key]
		if f == nil {
			t.Errorf("%s: absent from the index", key)
			continue
		}
		if f.why == "stdlib generic function" {
			t.Errorf("%s is refused as %q. Its signature is `(): Range<Int>` / `(Int): Range<Int>` "+
				"— CONCRETE, naming no type parameter — and `T` is merely in scope from "+
				"`impl Range<T>`. Reaching this means the receiver-generic rule became "+
				"BLOCK-level, which trades one false reason for another. See "+
				"stdrecvgeneric.go's per-signature argument.", key, f.why)
		}
	}
}

// TestReceiverTypeParamsComeFromTheFrontEnd is the CROSS-PATH audit, and it is
// here because the pair it audits cannot audit itself.
//
// `sigNamesTypeParam` consumes `analysis.ReceiverTypeParamNames`, so a test that
// compared the two would compare a function with its own input. The third path is
// the FRONT END's other answer to the same question: `*ast.ImplBlock.Generics`.
// Where a block carries an explicit header, the two must name the same
// parameters — and where it does not, the receiver-derived answer must be
// non-empty for exactly the generic-receiver blocks. Generics is populated for
// a derive-synthesized block and empty for a hand-written one, so a test of
// Generics alone answers correctly for exactly the half that does not matter.
func TestReceiverTypeParamsComeFromTheFrontEnd(t *testing.T) {
	lib := std.Load()
	modules := make([]string, 0, len(lib.Nodes))
	for m := range lib.Nodes {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	explicitAgreed, implicitOnly := 0, 0
	for _, module := range modules {
		fa := lib.Files[module]
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		declared := func(n string) bool { return fa.ModuleScope.Lookup(n) != nil }
		for _, n := range lib.Nodes[module] {
			ib, isImpl := n.(*ast.ImplBlock)
			if !isImpl {
				continue
			}
			if _, hasArgs := ib.Receiver.(*ast.GenericType); !hasArgs {
				continue
			}
			derived := map[string]bool{}
			for _, name := range analysis.ReceiverTypeParamNames(ib.Receiver, declared) {
				if name != "" {
					derived[name] = true
				}
			}
			if len(ib.Generics) > 0 {
				for _, g := range ib.Generics {
					if !derived[g.Name] {
						t.Errorf("%s: `impl … for %s` declares type parameter %q in its header, "+
							"but ReceiverTypeParamNames does not derive it from the receiver. The two "+
							"front-end answers disagree, so one of them is wrong about this block",
							module, typeText(ib.Receiver), g.Name)
					}
				}
				explicitAgreed++
				continue
			}
			if len(derived) == 0 {
				// Every argument is a concrete type — `impl ToJson for
				// Map<String, V>` has one of each, so this is only reached by a
				// receiver with NO parameter argument at all.
				continue
			}
			implicitOnly++
		}
	}
	// Both populations must be non-empty or the comparison above is one-sided.
	// The IMPLICIT one is the whole point: those are the blocks Generics alone
	// cannot see.
	if explicitAgreed == 0 {
		t.Error("no std impl block carries an explicit generic header over a generic receiver, " +
			"so the agreement half of this test checked nothing")
	}
	if implicitOnly == 0 {
		t.Error("no std impl block contributes type parameters through its RECEIVER ALONE. " +
			"Those are the blocks *ast.ImplBlock.Generics cannot see and the entire reason " +
			"ReceiverTypeParamNames is consulted; with none, the defect this guards is unreachable " +
			"and the guard is vacuous")
	}
}

// TestTypeNamesAnyWalksEveryCompositeForm covers the shapes std's
// receiver-generic signatures use, at the level of the walk rather than
// through std.
//
// Written as source text and analysed, so the AST shapes are the parser's own
// rather than hand-built nodes that might not match what std produces. The
// negative rows are the ones that matter: a walk that answered `true` for
// everything would satisfy the invariant test above by over-firing, and would
// move concrete declarations onto the generics key.
func TestTypeNamesAnyWalksEveryCompositeForm(t *testing.T) {
	tp := map[string]bool{"T": true, "K": true}
	for _, c := range []struct {
		src  string
		want bool
	}{
		{"T", true},
		{"Int", false},
		{"List<T>", true},
		{"List<Int>", false},
		{"Map<K, Int>", true},
		{"Map<String, Int>", false},
		{"Maybe<List<T>>", true},
		{"Maybe<List<Int>>", false},
		{"(T) -> Bool", true},
		{"(Int) -> Bool", false},
		{"(Int) -> T", true},
		{"((K, Int)) -> Bool", true},
		{"{name: T}", true},
		{"{name: String}", false},
	} {
		te := parseStdTypeExpr(t, c.src)
		if got := typeNamesAny(te, tp); got != c.want {
			t.Errorf("typeNamesAny(%q) = %v, want %v", c.src, got, c.want)
		}
	}
	if typeNamesAny(nil, tp) {
		t.Error("typeNamesAny(nil) = true; a parameter with no annotation is stdParamKind's " +
			"refusal to report, not a mention of a type parameter")
	}
}

// parseStdTypeExpr reads one type expression out of an analysed declaration, so
// the nodes under test are the front end's own rather than hand-built ones that
// might not match what std produces. `fn f<T, K>(x: …)` declares the two
// parameters so the annotation is front-end valid; the WALK does not consult
// them, which is exactly what makes the negative rows meaningful.
//
// No import, which is correct for every caller of THIS form: the shapes above
// name only prelude-exported constructors. A caller whose type comes from a
// spec TABLE must use parseStdTypeExprImporting instead; see its header.
func parseStdTypeExpr(t *testing.T, src string) ast.TypeExpr {
	t.Helper()
	return parseStdTypeExprImporting(t, "", src)
}

// parseStdTypeExprImporting is parseStdTypeExpr with one `import` item, for an
// annotation naming a type the prelude does not export.
//
// WHY THIS EXISTS: a guard may derive its POPULATION correctly from a table
// and still carry a hidden precondition in how it INSTANTIATES that population.
// This helper's caller iterates `stdGenStructSpecs` so an appended row needs no
// edit, and an import-free source resolves only for the prelude-exported `Set`
// and `Range`; a row such as `Channel<T>` would fail with
// `unknown type "Channel"`.
//
// So the rule is: derive the instantiation from the same place as the
// population. `item` is `"<origin>.<nomi>"` straight off the spec, which is
// already the exact spelling an import needs.
func parseStdTypeExprImporting(t *testing.T, item, src string) ast.TypeExpr {
	t.Helper()
	prog := ""
	if item != "" {
		prog = "import {\n  " + item + "\n}\n\n"
	}
	prog += "fn f<T, K>(_x: " + src + "): Unit {\n}\n"
	p, err := AnalyzeSource("main", prog)
	if err != nil {
		t.Fatalf("front end refused `x: %s` (import %q): %v", src, item, err)
	}
	for _, n := range p.Entry().Nodes {
		fd, isFunc := n.(*ast.FuncDef)
		if !isFunc || fd.Name != "f" || len(fd.Params) != 1 {
			continue
		}
		return fd.Params[0].TypeAnnotation
	}
	t.Fatalf("no `fn f` in the analysed program for `x: %s`", src)
	return nil
}

// parseStdTypeExprUnchecked is the same node from the same front end with the
// CHECKER left out, for a spelling the checker rejects.
//
// WHY A SECOND HELPER RATHER THAN A FLAG ON THE FIRST: the two are not the same
// question. Every existing caller wants an analysed node and should keep
// fataling when the checker refuses, because a refused witness measures
// nothing. This helper is for the
// one case where the checker's refusal is the POINT: a unit test of a builder
// arm's behaviour over an input the arm must own defensively.
//
// Its caller is `genStructSigKind`'s wrong-ARITY row. The checker rejects
// `Range<Int, Int>`, so the row is not expressible through the analysed route,
// but the arm's contract still covers it, so the row gets its node here.
//
// Still the PARSER's own node rather than a hand-built one, which is the
// property parseStdTypeExpr's header is about: `parseEnumVariant` and friends
// build shapes a literal `&ast.GenericType{…}` can get subtly wrong, and the
// arm switches on those shapes.
func parseStdTypeExprUnchecked(t *testing.T, src string) ast.TypeExpr {
	t.Helper()
	nodes, err := parser.Parse(lexer.Lex("fn f<T, K>(_x: " + src + "): Unit {\n}\n"))
	if err != nil {
		t.Fatalf("`x: %s` does not PARSE, so there is no node to test the arm with: %v", src, err)
	}
	for _, n := range nodes {
		fd, isFunc := n.(*ast.FuncDef)
		if !isFunc || fd.Name != "f" || len(fd.Params) != 1 {
			continue
		}
		return fd.Params[0].TypeAnnotation
	}
	t.Fatalf("no `fn f` in the parsed program for `x: %s`", src)
	return nil
}
