package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/std"
)

const calleeStdSrc = `import std::option.Option.{with_default}
import std::option.Option as Optional
import std::duration.Duration

fn qualified() -> Int
    Option.with_default(Some(1), 0)
end

fn selective() -> Int
    with_default(Some(1), 0)
end

fn aliased() -> Int
    Optional.with_default(Some(1), 0)
end

fn module_owner() -> Duration
    Duration.seconds(1)
end

fn own_default(m: Int) -> Int
    m
end

fn own() -> Int
    own_default(1)
end
`

// TestCalleeStd_IsTheDeclarations: a call of a std function records which
// std function it is from the declaration, so the qualified spelling, a
// selective import and an alias name the same one; a call of a program's
// own function records none.
func TestCalleeStd_IsTheDeclarations(t *testing.T) {
	fa, errs := checkEntry(t, calleeStdSrc)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	byLine := map[int]analysis.StdFunc{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok && rc.Callee.Kind == analysis.CalleeFunction {
			byLine[call.Line] = rc.Callee.Std
		}
	}
	withDefault := analysis.StdFunc{Module: "std/option", Owner: "Option", Name: "with_default"}
	want := map[int]analysis.StdFunc{
		6:  withDefault,
		10: withDefault,
		14: withDefault,
		18: {Module: "std/duration", Owner: "Duration", Name: "seconds"},
		26: {},
	}
	for line, w := range want {
		if got, ok := byLine[line]; !ok || got != w {
			t.Errorf("line %d: Callee.Std %+v (recorded %v), want %+v", line, got, ok, w)
		}
	}
}

// TestCalleeQualifier_IsTheDeclarations: a qualified call records what its
// qualifier resolves to by declaration, so an import alias names the type
// it aliases; the method is the declared name the call reaches there.
func TestCalleeQualifier_IsTheDeclarations(t *testing.T) {
	fa, errs := checkEntry(t, calleeStdSrc)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	type got struct {
		kind   analysis.QualifierKind
		std    analysis.StdType
		method string
	}
	byLine := map[int]got{}
	for n, rc := range fa.Calls {
		if call, ok := n.(*ast.Call); ok && rc.Callee.Kind == analysis.CalleeFunction {
			byLine[call.Line] = got{rc.Callee.Qualifier.Kind, rc.Callee.Qualifier.Std, rc.Callee.Method}
		}
	}
	option := got{analysis.QualType, analysis.StdType{Module: "std/option", Name: "Option"}, "with_default"}
	want := map[int]got{
		6:  option,
		10: {method: "with_default"},
		14: option,
		18: {analysis.QualType, analysis.StdType{Module: "std/duration", Name: "Duration"}, "seconds"},
		26: {},
	}
	for line, w := range want {
		if g := byLine[line]; g != w {
			t.Errorf("line %d: qualifier %+v, want %+v", line, g, w)
		}
	}
}

// TestCalleeOwnerAndFor: a bare call of a type's function records the type
// it is declared under as its owner, which is what it is routed by; an
// interface function called through the interface records the type it is
// called for.
func TestCalleeOwnerAndFor(t *testing.T) {
	fa, errs := checkEntry(t, `import std::option.Option.{with_default}
import std::json.{Json, FromJson}

fn bare() -> Int
    with_default(Some(1), 0)
end

fn turbofish(j: Json) -> Bool
    FromJson.from_json<Int>(j) == Ok(1)
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	var owner analysis.Qualifier
	var forType string
	for n, rc := range fa.Calls {
		call, ok := n.(*ast.Call)
		if !ok {
			continue
		}
		switch call.Line {
		case 5:
			if rc.Callee.Kind == analysis.CalleeFunction {
				owner = rc.Callee.RouteOwner()
			}
		case 9:
			if rc.Callee.For != nil {
				forType = rc.Callee.For.String()
			}
		}
	}
	if owner.Kind != analysis.QualType || owner.Std != (analysis.StdType{Module: "std/option", Name: "Option"}) {
		t.Errorf("bare with_default: routed by %+v, want std/option's Option", owner)
	}
	if forType != "Int" {
		t.Errorf("FromJson.from_json<Int>: for %q, want Int", forType)
	}
}

// TestCalleeStd_AStdModuleCheckedFromEditedText: std/hex checked from its
// own text with a test appended, as `nomi test std/hex.nomi` and a
// reference editor check it. Its attached tests' `import std::hex` names
// the shared stdlib's parse of std/hex, and its test calls std/strings'
// String.to_bytes; each call records which std function it reaches,
// though the program holds the edited parse under std/hex.
func TestCalleeStd_AStdModuleCheckedFromEditedText(t *testing.T) {
	text, ok := std.ReadFile("hex")
	if !ok {
		t.Fatal("no std/hex")
	}
	src := strings.TrimRight(string(text), "\n") + "\n\ntest \"edited\"\n    assert encode(String.to_bytes(\"f\")) == \"66\"\nend\n"
	_, fa, err := frontend.New(frontend.Config{}).CheckStdlibSource("hex", src, frontend.Mode{Tests: true})
	if err != nil {
		t.Fatalf("the front end rejects the edited module: %v", err)
	}
	want := map[string]analysis.StdFunc{
		"encode":   {Module: "std/hex", Name: "encode"},
		"to_bytes": {Module: "std/strings", Owner: "String", Name: "to_bytes"},
	}
	seen := map[string]int{}
	for _, rc := range fa.Calls {
		d, ok := rc.Callee.Decl.(*ast.ExternFunc)
		if !ok {
			continue
		}
		w, named := want[d.Name]
		if !named {
			continue
		}
		seen[d.Name]++
		if rc.Callee.Std != w {
			t.Errorf("a call of %s (declared at line %d) records Callee.Std %+v, want %+v", d.Name, d.Line, rc.Callee.Std, w)
		}
	}
	// encode: the appended test's and the three attached tests'.
	if seen["encode"] < 4 || seen["to_bytes"] < 4 {
		t.Errorf("calls checked: %v, want at least 4 of each", seen)
	}
}
