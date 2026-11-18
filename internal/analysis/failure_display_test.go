package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The error a fixed typed literal's handler or a `!` over a Result shows
// when it fails is recorded as a hole of its type is (DisplayRender): an
// impl's function for a program type and for a std type, the type
// parameter's rendering in a generic body, which DisplayAt answers at an
// instance's types, and its parts for a tuple.
func TestFailureDisplay_IsRecordedAsAHoleOfTheErrorsTypeIs(t *testing.T) {
	fa, errs := checkEntry(t, `import
    std::bits.Bits
    std::literals.{Fragment, Literal}
end

struct Bad
    why: String
end

impl Display for Bad
    fn to_string(b: Bad) -> String
        "bad: ${b.why}"
    end
end

struct Word
    text: String
end

impl Literal for Word
    fn from_fragments(_fragments: List<Fragment<String>>) -> Result<Word, Bad>
        Err(Bad{why: "no"})
    end
end

fn half(n: Int) -> Result<Int, Bad>
    if n % 2 == 0 then Ok(n / 2) else Err(Bad{why: "odd"})
end

fn coded(n: Int) -> Result<Int, (Int, Bad)>
    if n > 0 then Ok(n) else Err((n, Bad{why: "neg"}))
end

fn force<E>(r: Result<Int, E>) -> Int where E: Display
    r!
end

fn total() -> Int
    _ = Word"x"
    _ = Bits"10"
    m: Maybe<Int> = Some(1)
    half(4)! + coded(1)! + force(half(2)) + m!
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	lits := map[string]*analysis.TypedLiteral{}
	for n, rec := range fa.TypedLiterals {
		lits[n.Tag] = rec
	}
	impl := func(what string, r *analysis.DisplayRender, name string) {
		t.Helper()
		if r == nil || r.Form != analysis.DisplayImpl {
			t.Fatalf("%s: %+v, want DisplayImpl", what, r)
		}
		fd, ok := r.Method.(*ast.FuncDef)
		if !ok || fd.Name != "to_string" || r.Type == nil || r.Type.String() != name {
			t.Errorf("%s: renders %s through %v, want %s's to_string", what, r.Type, r.Method, name)
		}
	}
	impl("Word\"x\"", lits["Word"].Failure, "Bad")
	if bits := lits["Bits"]; bits == nil || bits.Failure == nil || bits.Failure.Form != analysis.DisplayImpl || bits.Failure.Method == nil ||
		fa.StdFuncOf(bits.Failure.Method).Module != "std/bits" {
		t.Errorf("Bits\"10\": %+v, want std/bits' Error's to_string", bits)
	}

	bangs := map[int][]*ast.Unwrap{}
	for n := range fa.UnwrapFailures {
		bangs[n.Line] = append(bangs[n.Line], n)
	}
	const forceLine, mainLine = 35, 42
	if len(bangs[mainLine]) != 2 {
		t.Fatalf("line %d: %d `!` over a Result recorded, want 2 (a `!` over a Maybe shows no error): %v", mainLine, len(bangs[mainLine]), bangs)
	}
	for _, n := range bangs[mainLine] {
		r := fa.UnwrapFailures[n]
		switch r.Form {
		case analysis.DisplayImpl:
			impl("half(4)!", r, "Bad")
		case analysis.DisplayParts:
			if len(r.Parts) != 2 || r.Parts[0].Form != analysis.DisplayScalar {
				t.Fatalf("coded(1)!: %+v, want an Int's and a Bad's parts", r.Parts)
			}
			impl("coded(1)!'s Bad", r.Parts[1], "Bad")
		default:
			t.Errorf("line %d: %+v, want an impl or parts", mainLine, r)
		}
	}
	if len(bangs[forceLine]) != 1 {
		t.Fatalf("line %d: %d `!` recorded, want 1", forceLine, len(bangs[forceLine]))
	}
	r := fa.UnwrapFailures[bangs[forceLine][0]]
	if r.Form != analysis.DisplayTypeParam {
		t.Fatalf("r! in a generic body: %+v, want DisplayTypeParam", r)
	}
	var bad analysis.Type
	for n, ty := range fa.ExprTypes {
		if s, ok := n.(*ast.StructLit); ok && s.TypeName != nil && s.TypeName.TypeString() == "Bad" {
			bad = ty
		}
	}
	at, resolved := fa.DisplayAt(r, func(p *analysis.TypeParam_) (analysis.Type, bool) { return bad, p.Name_ == "E" && bad != nil })
	if !resolved {
		t.Fatalf("r! at E = Bad: not resolved")
	}
	impl("r! at E = Bad", at, "Bad")
}
