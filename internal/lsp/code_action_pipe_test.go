package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const rewrite = protocol.CodeActionKindRefactorRewrite

func TestToPipe_NestedCalls(t *testing.T) {
	src := fnMain("xs = [1, 2, 3]\nys = Iter.to_list(Iter.map‸(xs, |x| x * 2))\nio.inspect(ys)\n")
	got := checkRefactor(t, src, "Convert to pipe", rewrite)
	want := fnMain("xs = [1, 2, 3]\n\nys =\n    xs\n    |> Iter.map(|x| x * 2)\n    |> Iter.to_list()\n\nio.inspect(ys)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// And back: the pipeline folds into the calls it came from.
	back := checkRefactor(t, strings.Replace(got, "|> Iter.map", "|> Iter.map‸", 1), "Convert from pipe", rewrite)
	// The blank lines the pipe statement was set apart with stay.
	if want := fnMain("xs = [1, 2, 3]\n\nys = Iter.to_list(Iter.map(xs, |x| x * 2))\n\nio.inspect(ys)\n"); back != want {
		t.Fatalf("back:\n%s\nwant\n%s", back, want)
	}
}

func TestToPipe_LiteralSubjectLeads(t *testing.T) {
	src := fnMain("s = String.trim(String.to_upper‸(\" a \"))\nio.print(s)\n")
	got := checkRefactor(t, src, "Convert to pipe", rewrite)
	if want := fnMain("s =\n    \" a \"\n    |> String.to_upper()\n    |> String.trim()\n\nio.print(s)\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// One stage stays on its line.
func TestToPipe_InnermostCallWithoutArgumentsLeads(t *testing.T) {
	decls := "fn seed(): List<Int> {\n    [1, 2]\n}\n\n"
	got := checkRefactor(t, fnMainAfter(decls, "n = Iter.count(se‸ed())\nio.inspect(n)\n"), "Convert to pipe", rewrite)
	if want := fnMainAfter(decls, "n = seed() |> Iter.count()\n\nio.inspect(n)\n"); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A lambda's one-expression body runs past a `|>`, so the pipeline stands
// there without parentheses.
func TestToPipe_InLambdaBody(t *testing.T) {
	src := fnMain("n = Iter.map([[1]], |xs| Iter.count(Iter.filter‸(xs, |x| x > 0)))\nio.inspect(Iter.to_list(n))\n")
	got := checkRefactor(t, src, "Convert to pipe", rewrite)
	if !strings.Contains(got, "|xs|\n") || !strings.Contains(got, "|> Iter.count()") || strings.Contains(got, "(xs") {
		t.Fatalf("got\n%s", got)
	}
	back := checkRefactor(t, strings.Replace(got, "|> Iter.count(", "|> Iter.co‸unt(", 1), "Convert from pipe", rewrite)
	if !strings.Contains(back, "|xs| Iter.count(Iter.filter(xs, |x| x > 0))") {
		t.Fatalf("back:\n%s", back)
	}
}

// As an operand the pipeline keeps its shape in parentheses.
func TestToPipe_Operand(t *testing.T) {
	src := fnMain("xs = [1, 2]\nn = Iter.count(Iter.filter‸(xs, |x| x > 1)) + 1\nio.inspect(n)\n")
	got := checkRefactor(t, src, "Convert to pipe", rewrite)
	want := fnMain("xs = [1, 2]\nn = (xs |> Iter.filter(|x| x > 1) |> Iter.count()) + 1\nio.inspect(n)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	back := checkRefactor(t, strings.Replace(got, "|> Iter.count(", "|> Iter.co‸unt(", 1), "Convert from pipe", rewrite)
	if !strings.Contains(back, "n = Iter.count(Iter.filter(xs, |x| x > 1)) + 1\n") {
		t.Fatalf("back:\n%s", back)
	}
}

func TestToPipe_Refused(t *testing.T) {
	for name, body := range map[string]string{
		// A lone call is never a pipe (style.md §1).
		"lone call": "n = Iter.count‸([1, 2])\nio.inspect(n)\n",
		// A call in a later argument is a value, not the subject.
		"later argument": "m = Map.put({\"a\" => 1}, \"k\", Iter.count‸([1]))\nio.inspect(m)\n",
		// Partial application: the pipe would fill the `_`.
		"placeholder": "f = Iter.map‸(Iter.filter([1], |x| x > 0), _)\nio.inspect(f(|x| x + 1))\n",
	} {
		t.Run(name, func(t *testing.T) {
			refuseRefactor(t, fnMain(body), "Convert to pipe")
		})
	}
	// Inside a stage, the text's first argument is not the call's first.
	refuseRefactor(t, fnMain("n = [[1]] |> Iter.map‸(Iter.count(_))\nio.inspect(n)\n"), "Convert to pipe")
}

func TestFromPipe_Stages(t *testing.T) {
	decls := "fn parse(s: String): Result<Int, String> {\n    Maybe.with_default(String.to_int(s), 0) |> Ok()\n}\n\nfn divide(a: Int, b: Int): Int {\n    a / b\n}\n\n"
	cases := []struct{ name, body, want string }{
		{
			"placeholder",
			"fn f(): Int {\n    10 |> div‸ide(100, _)\n}\n",
			"fn f(): Int {\n    divide(100, 10)\n}\n",
		},
		{
			"try stage",
			"fn f(): Result<Int, String> {\n    n = \"4\"\n    |> try‸ parse()\n\n    Ok(n)\n}\n",
			"fn f(): Result<Int, String> {\n    n = try parse(\"4\")\n\n    Ok(n)\n}\n",
		},
		{
			"case stage",
			"fn f(): Int {\n    \"4\"\n    |> case pa‸rse() {\n            Ok(n) -> n\n            Err(_) -> 0\n        }\n}\n",
			"fn f(): Int {\n    case parse(\"4\") {\n        Ok(n) -> n\n        Err(_) -> 0\n    }\n}\n",
		},
		{
			"bare case stage",
			"fn f(): Int {\n    parse(\"4\")\n    |> ca‸se {\n            Ok(n) -> n\n            Err(_) -> 0\n        }\n}\n",
			"fn f(): Int {\n    case parse(\"4\") {\n        Ok(n) -> n\n        Err(_) -> 0\n    }\n}\n",
		},
		{
			"if stage",
			"fn f(): Int {\n    [1]\n    |> if Iter.empty‸?() { 0 } else { 1 }\n}\n",
			"fn f(): Int {\n    if Iter.empty?([1]) { 0 } else { 1 }\n}\n",
		},
		{
			"comparison",
			"fn f(): Bool {\n    [1] |> Iter.to_‸list() == [1]\n}\n",
			"fn f(): Bool {\n    Iter.to_list([1]) == [1]\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := checkRefactor(t, decls+c.body, "Convert from pipe", rewrite)
			if got != decls+c.want {
				t.Fatalf("got\n%s\nwant\n%s", got, decls+c.want)
			}
		})
	}
}

// In a `then` lambda's one-expression body a pipe would end the lambda at
// its first `|>`, so the pipeline is grouped.
func TestToPipe_InThenBody(t *testing.T) {
	src := fnMain("n =\n    [1, 2]\n    |> then |xs| Iter.count(Iter.filter‸(xs, |x| x > 0))\n\nio.inspect(n)\n")
	got := checkRefactor(t, src, "Convert to pipe", rewrite)
	if !strings.Contains(got, "then |xs| (xs |> Iter.filter(|x| x > 0) |> Iter.count())") {
		t.Fatalf("got\n%s", got)
	}
}

func TestFromPipe_RefusesThenStage(t *testing.T) {
	refuseRefactor(t, fnMain("n =\n    [1, 2]\n    |> then |v| Iter.co‸unt(v)\n\nio.inspect(n)\n"), "Convert from pipe")
}

// A pipe nested in a lambda inside a pipe: the cursor picks the inner one.
func TestFromPipe_InnermostPipeline(t *testing.T) {
	src := fnMain("n =\n    [[1, 2]]\n    |> Iter.map(|xs| { xs |> Iter.cou‸nt() })\n    |> Iter.to_list()\n\nio.inspect(n)\n")
	got := checkRefactor(t, src, "Convert from pipe", rewrite)
	// `nomi fmt` drops the braces a one-expression body no longer needs.
	want := fnMain("n =\n    [[1, 2]]\n    |> Iter.map(|xs| Iter.count(xs))\n    |> Iter.to_list()\n\nio.inspect(n)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
