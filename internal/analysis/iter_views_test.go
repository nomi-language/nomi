package analysis_test

import (
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

const iterViewSrc = `struct Countdown
    from: Int
end

impl Iter for Countdown
    fn each_while(c: Countdown, yield: (Int) -> Bool) -> Bool
        c.from <= 0 or yield(c.from) and each_while(Countdown{from: c.from - 1}, yield)
    end
end

struct Stepped<T, S>
    start: T
    by: S
end

impl Iter for Stepped<T, S> where T: Comparable
    fn each_while(s: Stepped<T, S>, yield: (T) -> Bool) -> Bool
        yield(s.start)
    end
end

fn stepped<T, S>(start: T, by: S) -> Iter<T> where T: Comparable
    Stepped{start:, by:}
end

fn total<C>(c: C) -> Int where C: Iter<Int>
    Iter.count(c)
end

fn run() -> Int
    m = {"a" => 1}
    s: Iter<Int> = [1, 2]
    Iter.count([1, 2]) + Iter.count("ab") + Iter.count(1..3) + Iter.count(m) +
        Iter.count(Countdown{from: 3}) + Iter.count(s) + Iter.count(Some(1)) + total(Countdown{from: 2})
end
`

// TestIterViews_RecordEachSource: each source at an `Iter<T>` position has
// a record of its walk, a type's own impl names its block (a generic one
// bounded by a where clause among them, from inside a generic body), a type
// parameter bounded by Iter is left to its instances, and a value that is
// an Iter already has none.
func TestIterViews_RecordEachSource(t *testing.T) {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(iterViewSrc))
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	if errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)); len(errs) > 0 {
		t.Fatalf("the checker rejects the source: %v", errs)
	}
	impls := map[string]*ast.ImplBlock{}
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok && ib.Receiver != nil {
			impls[ib.Receiver.TypeString()] = ib
		}
	}
	got := map[string]*analysis.IterView{}
	for n, v := range fa.IterViews {
		got[fmt.Sprintf("%s@%d", n.NodeType(), n.LineNum())] = v
	}
	want := map[string]struct {
		dom  analysis.IterViewDomain
		elem string
		impl *ast.ImplBlock
	}{
		"ListLit@33":   {analysis.IterViewList, "Int", nil},
		"StringLit@33": {analysis.IterViewString, "String", nil},
		"RangeLit@33":  {analysis.IterViewRange, "Int", nil},
		"Ident@33":     {analysis.IterViewMap, "(String, Int)", nil},
		"StructLit@34": {analysis.IterViewImpl, "Int", impls["Countdown"]},
		"Call@34":      {analysis.IterViewMaybe, "Int", nil},
		"Ident@27":     {analysis.IterViewBound, "", nil},
		"StructLit@23": {analysis.IterViewImpl, "T", impls["Stepped<T, S>"]},
	}
	for label, w := range want {
		v := got[label]
		if v == nil {
			t.Errorf("%s: no view recorded; recorded: %v", label, keys(got))
			continue
		}
		elem := ""
		if v.Elem != nil {
			elem = v.Elem.String()
		}
		if v.Domain != w.dom || elem != w.elem || v.Impl != w.impl {
			t.Errorf("%s: got domain %d elem %v impl %p, want %d %s %p", label, v.Domain, v.Elem, v.Impl, w.dom, w.elem, w.impl)
		}
	}
	if v := got["Ident@34"]; v != nil {
		t.Errorf("an Iter value has a view recorded: %+v", v)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
