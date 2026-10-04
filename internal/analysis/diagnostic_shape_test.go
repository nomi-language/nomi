package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The common diagnostics carry the span they are about, their advice as
// hints rather than message text, and the declarations they refer to as
// related locations.

type wantShape struct {
	name    string
	src     string
	message string
	span    string // "line:col-endLine:endCol" after CompleteSpans
	hints   []string
	related []string // "file|line:col-endLine:endCol|message"; file "" is the same file
}

func shapeOf(e analysis.TypeError) string {
	return fmt.Sprintf("%d:%d-%d:%d", e.Line, e.Col, e.EndLine, e.EndCol)
}

func relatedOf(e analysis.TypeError) []string {
	var out []string
	for _, r := range e.Related {
		out = append(out, fmt.Sprintf("%s|%d:%d-%d:%d|%s", r.File, r.Line, r.Col, r.EndLine, r.EndCol, r.Message))
	}
	return out
}

func TestDiagnosticShape(t *testing.T) {
	for _, tc := range []wantShape{
		{
			name:    "type mismatch spans the value",
			src:     "fn f(): Int {\n  x: Int = \"s\"\n  x\n}\n",
			message: "type mismatch: expected Int, got String",
			span:    "2:12-2:15",
		},
		{
			name:    "argument mismatch spans the argument",
			src:     "fn g(n: Int): Int { n }\n\nfn f(): Int {\n  g(\"s\")\n}\n",
			message: "argument 1: expected Int, got String",
			span:    "4:5-4:8",
		},
		{
			name:    "return mismatch spans the tail and points at the return type",
			src:     "fn f(): Int {\n  \"s\"\n}\n",
			message: "return type mismatch: expected Int, got String",
			span:    "2:3-2:6",
			related: []string{"|1:9-1:12|the return type is declared here"},
		},
		{
			name:    "undefined name suggests a close one",
			src:     "fn f(): Int {\n  count = 1\n  count + cuont\n}\n",
			message: "undefined variable 'cuont'",
			span:    "3:11-3:16",
			hints:   []string{"did you mean 'count'?"},
		},
		{
			name:    "unknown field suggests a close one",
			src:     "struct Person {\n  name: String\n}\n\nfn f(p: Person): String {\n  p.nmae\n}\n",
			message: "struct 'Person' has no field 'nmae'",
			span:    "6:5-6:9",
			hints:   []string{"did you mean 'name'?"},
		},
		{
			name:    "missing field spans the type name",
			src:     "struct Person {\n  name: String\n  age: Int\n}\n\nfn f(): Person {\n  Person{name: \"a\"}\n}\n",
			message: "missing field 'age' of Person",
			span:    "7:3-7:9",
		},
		{
			name:    "non-exhaustive case spans its head",
			src:     "enum Color {\n  Red\n  Green\n}\n\nfn f(c: Color): Int {\n  case c {\n    Color.Red -> 1\n  }\n}\n",
			message: "non-exhaustive case on Color: missing Green",
			span:    "7:3-7:9",
			hints:   []string{"add an arm for each missing variant, or a `_` arm"},
		},
		{
			name:    "missing impl function points at the interface function",
			src:     "interface Shape {\n  fn area(shape: self): Int\n  fn label(shape: self): String\n}\n\nstruct Sq {\n  side: Int\n}\n\nimpl Shape for Sq {\n  fn area(s: Sq): Int {\n    s.side\n  }\n}\n",
			message: "impl 'Shape' for 'Sq': missing function 'label' required by interface 'Shape'",
			span:    "10:1-10:18",
			related: []string{"|3:6-3:11|'label' is declared here"},
		},
		{
			name:    "parameter name mismatch points at the interface function",
			src:     "interface Scaler {\n  fn scale(s: self, factor: Int): Int\n}\n\nstruct Sq {\n  side: Int\n}\n\nimpl Scaler for Sq {\n  fn scale(s: Sq, by: Int): Int {\n    s.side * by\n  }\n}\n",
			message: "impl function 'scale': parameter name 'by' does not match interface declaration 'factor'",
			span:    "10:19-10:21",
			hints:   []string{"rename it 'factor'"},
			related: []string{"|2:6-2:11|interface function 'scale' is declared here"},
		},
		{
			name:    "duplicate declaration points at the first",
			src:     "fn dup(): Int {\n  1\n}\n\nfn dup(): Int {\n  2\n}\n",
			message: "'dup' is already defined in this scope as a function",
			span:    "5:4-5:7",
			hints:   []string{"pick a different name"},
			related: []string{"|1:4-1:7|'dup' is first defined here"},
		},
		{
			name:    "unread binding",
			src:     "fn f(): Int {\n  unused = 1\n  2\n}\n",
			message: "binding 'unused' is never read",
			span:    "2:3-2:9",
			hints:   []string{"prefix it with '_' if the value is intentionally ignored"},
		},
		{
			name:    "placeholder as a value",
			src:     "fn f(): Int {\n  x = _\n  x\n}\n",
			message: "`_` has no value here",
			span:    "2:7-2:8",
			hints:   []string{"`_` stands for a missing argument only in a call, as in add(1, _)"},
		},
		{
			name:    "lambda stage boundary",
			src:     "fn f(): Int {\n  [1, 2]\n  |> |xs| xs |> Iter.filter(|x| x > Iter.count(xs))\n  |> Iter.count()\n}\n",
			message: "'xs' is the parameter of the lambda stage on line 3, whose body ends at the next `|>`",
			span:    "3:48-3:50",
			hints:   []string{"write `|xs| { ... }` to keep the pipe inside it"},
		},
		{
			name:    "self render",
			src:     "struct Foo {\n  name: String\n}\n\nimpl Display for Foo {\n  fn to_string(foo: Foo): String {\n    Display.to_string(foo)\n  }\n}\n",
			message: "Display.to_string(foo) calls the function being defined with its own receiver, so it never returns",
			span:    "7:5-7:27",
			hints:   []string{"Debug.inspect(foo) renders its fields"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			errs = analysis.CompleteSpans(tc.src, errs)
			var hit *analysis.TypeError
			for i := range errs {
				if errs[i].Message == tc.message {
					hit = &errs[i]
				}
			}
			if hit == nil {
				var all []string
				for _, e := range errs {
					all = append(all, e.Error())
				}
				t.Fatalf("no error %q; got:\n  %s", tc.message, strings.Join(all, "\n  "))
			}
			if got := shapeOf(*hit); got != tc.span {
				t.Errorf("span = %s, want %s", got, tc.span)
			}
			if strings.Join(hit.Hints, "|") != strings.Join(tc.hints, "|") {
				t.Errorf("hints = %q, want %q", hit.Hints, tc.hints)
			}
			if got := relatedOf(*hit); strings.Join(got, "\n") != strings.Join(tc.related, "\n") {
				t.Errorf("related = %q, want %q", got, tc.related)
			}
		})
	}
}

// An error inside a lambda, a tuple element or a call argument makes the
// enclosing value's type unknown, so no second mismatch follows from it.
func TestDiagnosticShape_NoFollowOnMismatch(t *testing.T) {
	for _, src := range []string{
		"fn d(): Maybe<(Int) -> Int> {\n  Some(|x| x + nope)\n}\n",
		"fn d(): (Int) -> Int {\n  |_n: Int| _\n}\n",
		"fn d(): (Int, Int) {\n  (1, _)\n}\n",
	} {
		_, errs := checkSourceWithStdlib(src)
		if len(errs) != 1 {
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			t.Errorf("%s: want one error, got:\n  %s", src, strings.Join(all, "\n  "))
		}
	}
}
