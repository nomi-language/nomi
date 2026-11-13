package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// checkWithStd checks src against the stdlib and fails the test on an
// error.
func checkWithStd(t *testing.T, src string) (*analysis.FileAnalysis, []ast.Node) {
	t.Helper()
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	if errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)); len(errs) > 0 {
		t.Fatalf("the checker rejects the source: %v", errs)
	}
	return fa, nodes
}

const varianceSrc = `struct Holds<A>
    item: A
    items: List<A>
    pair: (Int, Maybe<A>)
end

struct Takes<A>
    f: (A) -> String
end

struct Both<A>
    get: () -> A
    put: (A) -> Unit
end

struct Phantom<A>
    n: Int
end

struct Twice<A>
    sink: Takes<Takes<A>>
end

struct Mixed<A, B>
    a: A
    b: (B) -> Int
end

enum Chain<A>
    Link{head: A, tail: Chain<A>}
    End
end

enum Loop<A>
    Next(Loop<A>, (A) -> Int)
    Stop
end

type Ids<A> List<A>

type Handler<A> (A) -> A

type Taker<A> (A) -> String

struct Keyed<A>
    by: Map<A, Int>
    within: Set<A>
end

fn run(h: Holds<Int>, t: Takes<Int>, b: Both<Int>, p: Phantom<Int>, w: Twice<Int>, m: Mixed<Int, Int>, c: Chain<Int>, l: Loop<Int>, k: Keyed<Int>, mb: Maybe<Int>, r: Result<Int, String>, ids: Ids<Int>, hd: Handler<Int>, tk: Taker<Int>) -> Int
    0
end
`

// TestTypeParamVariances_FromTheFields: a parameter held only is covariant,
// one taken only (a function's parameter) contravariant, one both held and
// taken invariant, one never named covariant; a contravariant position inside
// another flips back, a recursive type reads its own variance, a Map key and
// a Set element are covariant, std's Maybe and Result are covariant, and a
// distinct type's parameters are its inner type's.
func TestTypeParamVariances_FromTheFields(t *testing.T) {
	fa, _ := checkWithStd(t, varianceSrc)
	sym := fa.ModuleScope.Lookup("run")
	if sym == nil {
		t.Fatal("no fn run in the source")
	}
	ft, ok := sym.Type.(*analysis.FuncType)
	if !ok {
		t.Fatalf("run's type is %T", sym.Type)
	}
	co, contra, inv := analysis.VarianceCovariant, analysis.VarianceContravariant, analysis.VarianceInvariant
	want := [][]analysis.Variance{
		{co}, {contra}, {inv}, {analysis.VarianceUnused}, {co}, {co, contra}, {co}, {contra}, {co}, {co}, {co, co}, {co}, {inv}, {contra},
	}
	for i, p := range ft.Params {
		got := analysis.TypeParamVariances(p)
		if len(got) != len(want[i]) {
			t.Errorf("%s: variances %v, want %v", p, got, want[i])
			continue
		}
		for j := range got {
			if got[j] != want[i][j] {
				t.Errorf("%s: parameter %d is %s, want %s", p, j, got[j], want[i][j])
			}
		}
	}
	if got := analysis.TypeParamVariances(&analysis.ListType{Elem: analysis.TypeInt}); got != nil {
		t.Errorf("a List has variances %v, want none: its element is covariant by argVariance", got)
	}
}

const conversionSrc = `interface Show
    fn show(v: self) -> Int
end

impl Show for List<Int>
    fn show(_v: List<Int>) -> Int
        1
    end
end

impl Show for List<String>
    fn show(_v: List<String>) -> Int
        2
    end
end

fn take(m: Maybe<Show>) -> Int
    case m
        Some(s) -> Show.show(s)
        None -> 0
    end
end

fn run() -> Int
    m = Some([1])
    take(m)
end
`

// TestConversions_RecordEachPartThatEntersAnInterface: a `Maybe<List<Int>>`
// passed where a `Maybe<Show>` is expected has a record of the conversion,
// whose one box names the List<Int> impl, since a List's name does not
// select it; the verifier reports the expression once the record is gone.
func TestConversions_RecordEachPartThatEntersAnInterface(t *testing.T) {
	fa, nodes := checkWithStd(t, conversionSrc)
	var impl *ast.ImplBlock
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok && ib.Receiver != nil && ib.Receiver.TypeString() == "List<Int>" {
			impl = ib
		}
	}
	var arg ast.Node
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if call, ok := n.(*ast.Call); ok {
				if id, ok := call.Func.(*ast.Ident); ok && id.Name == "take" && len(call.Args) == 1 {
					arg = call.Args[0]
				}
			}
			return true
		})
	}
	if arg == nil || impl == nil {
		t.Fatal("want a call of take and an impl for List<Int> in the source")
	}
	rec := fa.Conversions[arg]
	switch {
	case rec == nil:
		t.Fatal("no conversion recorded for take's argument")
	case rec.From.String() != "Maybe<List<Int>>" || rec.To.String() != "Maybe<Show>":
		t.Fatalf("recorded %s to %s, want Maybe<List<Int>> to Maybe<Show>", rec.From, rec.To)
	case len(rec.Boxes) != 1:
		t.Fatalf("recorded %d boxes, want 1", len(rec.Boxes))
	case rec.Boxes[0].Iface.Name != "Show" || rec.Boxes[0].Value.String() != "List<Int>" || rec.Boxes[0].Impl != impl:
		t.Fatalf("recorded box %s into %s by %p, want List<Int> into Show by %p", rec.Boxes[0].Value, rec.Boxes[0].Iface, rec.Boxes[0].Impl, impl)
	}
	if got := analysis.VerifyElaboration(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
	delete(fa.Conversions, arg)
	got := analysis.VerifyElaboration(fa, nodes)
	reported := false
	for _, v := range got {
		if v.Node == arg && v.Category == analysis.ElabConversionUnrecorded {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("an unrecorded conversion of take's argument is not reported: %v", got)
	}
}
