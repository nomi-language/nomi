package irbuild

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// Tests for the `match` and `destructure` classes (irmatch.go,
// irdestructure.go).

// TestIRMatchDestructure_EveryPatternSiteHasAPosition measures the premise
// `ir.At` panics on, over the corpus and the stdlib, for the position every
// routed pattern node takes.
//
// The premise is stronger here than for `make` and `proj`, and that is why it
// is measured rather than inherited. Those classes took the position of an
// EXPRESSION, which the parser always gives a line. A pattern is a different
// grammar: `*ast.WildcardPattern`, `*ast.IdentPattern`, `*ast.EnumPattern`,
// `*ast.StructPattern`, `*ast.TuplePattern`, `*ast.ListPattern` and
// `*ast.MapPattern` all carry their own Line and Col, and `structDestructure`
// SYNTHESIZES an `*ast.StructPattern` from a statement's fields — so this
// counts every one of them instead of assuming the parser filled them in.
//
// A SUPERSET of the sites the builder reaches, for logicNodesUnder's reason:
// the claim is about the trees, and a claim about the trees stays true when
// more of them starts lowering.
//
// Over BOTH trees, because they are lowered by different gens: the corpus
// through `Analyze`, the stdlib through `std.Load`.
func TestIRMatchDestructure_EveryPatternSiteHasAPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	byKind := map[string]int{}
	var unpositioned []string

	check := func(where string, root ast.Node) {
		for _, n := range patternNodesUnder(root) {
			byKind[n.NodeType()]++
			if line := firstInt(nodePos(n)); line < 1 {
				unpositioned = append(unpositioned,
					fmt.Sprintf("%s: %s at line %d has no 1-based line", where, n.NodeType(), line))
			}
		}
	}

	_, files := corpusAnalysis(t)
	for _, f := range files {
		if f.Prog == nil {
			continue
		}
		for i := range f.Prog.Modules {
			for _, n := range f.Prog.Modules[i].Nodes {
				check(f.Rel, n)
			}
		}
	}
	lib := std.Load()
	for module, nodes := range lib.Nodes {
		for _, n := range nodes {
			check("std/"+module, n)
		}
	}

	// PLANT A POSITIVE, on every pattern kind the two classes route. A walk
	// that found nothing would report zero unpositioned sites, and so would a
	// correct one — and a walk that found only the two commonest kinds would
	// say nothing about a list or a map pattern.
	total := 0
	for _, want := range []string{"EnumPattern", "StructPattern", "TuplePattern",
		"ListPattern", "MapPattern", "IdentPattern", "WildcardPattern"} {
		if byKind[want] < 1 {
			t.Fatalf("the walk found no %s across the corpus and the stdlib, so the zero "+
				"below would be a false one for that kind. Found: %v", want, byKind)
		}
		total += byKind[want]
	}
	if total < 500 {
		t.Fatalf("only %d pattern nodes found in total, which cannot be right for a corpus "+
			"of 222 programs: %v", total, byKind)
	}
	if len(unpositioned) > 0 {
		t.Errorf("%d pattern node(s) carry no 1-based line, so `ir.At` would panic on "+
			"them:\n%s", len(unpositioned), strings.Join(unpositioned, "\n"))
	}
	t.Logf("%d pattern nodes over the corpus and the stdlib, none without a line: %v",
		total, byKind)
}

// patternNodesUnder collects every pattern node reachable from root.
//
// The seven are the ones `matchArm` and `destructure` dispatch on. A literal
// in pattern position is an `*ast.IntLit` / `*ast.StringLit` /
// `*ast.FloatLit` / `*ast.DecimalLit` and is not here, because those are
// expression nodes and a walk cannot tell the two positions apart — their
// positions are covered by the const and arith classes' own readings.
//
// It cannot use `childNodes`: `appendChildNode` drops every node whose
// `NodeType()` ends in "Pattern", because pattern kinds are the matching
// construct's internals. A walk over `childNodes` sees zero pattern nodes in
// the corpus and the stdlib, so this is a second, reflective walk.
func patternNodesUnder(root ast.Node) []ast.Node {
	var out []ast.Node
	seen := map[ast.Node]bool{}
	var walk func(v reflect.Value)
	fields := func(v reflect.Value) {
		if v.Kind() != reflect.Struct || v.Type() == triviaCarrierType {
			return
		}
		for i := range v.Type().NumField() {
			if v.Type().Field(i).IsExported() {
				walk(v.Field(i))
			}
		}
	}
	visit := func(n ast.Node) {
		if isNilNode(n) || seen[n] {
			return
		}
		seen[n] = true
		switch n.(type) {
		case *ast.WildcardPattern, *ast.IdentPattern, *ast.EnumPattern, *ast.StructPattern,
			*ast.TuplePattern, *ast.ListPattern, *ast.MapPattern:
			out = append(out, n)
		}
		// The node's FIELDS, not the node again: going back through walk would
		// re-detect the struct as a node and stop at `seen`.
		fields(reflect.Indirect(reflect.ValueOf(n)))
	}
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if n, isNode := v.Interface().(ast.Node); isNode {
				visit(n)
				return
			}
			walk(v.Elem())
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				walk(v.Index(i))
			}
		case reflect.Struct:
			if v.CanAddr() {
				if n, isNode := v.Addr().Interface().(ast.Node); isNode {
					visit(n)
					return
				}
			}
			fields(v)
		}
	}
	visit(root)
	return out
}
