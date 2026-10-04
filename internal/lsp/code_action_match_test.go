package lsp

import (
	"testing"
)

const matchDecls = "enum Shape {\n    Circle Float\n    Rect {w: Float, h: Float}\n    Empty\n}\n\nfn make(): Shape {\n    .Empty\n}\n\n"

func TestPatternMatch_OnBinding(t *testing.T) {
	src := fnMainAfter(matchDecls, "sh‸ape = make()\nio.inspect(shape)\n")
	got := checkRefactor(t, src, "Pattern match on 'shape'", rewrite)
	want := fnMainAfter(matchDecls, "shape = make()\n\ncase shape {\n    .Circle(value) -> todo\n    .Rect{w, h} -> todo\n    .Empty -> todo\n}\n\nio.inspect(shape)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatternMatch_GenericPayloads(t *testing.T) {
	src := fnMain("n‸ = String.to_int(\"4\")\nio.inspect(n)\n")
	got := checkRefactor(t, src, "Pattern match on 'n'", rewrite)
	want := fnMain("n = String.to_int(\"4\")\n\ncase n {\n    .Some(value) -> todo\n    .None -> todo\n}\n\nio.inspect(n)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatternMatch_WrapsCall(t *testing.T) {
	decls := "fn parse(s: String): Result<Int, String> {\n    Maybe.with_default(String.to_int(s), 0) |> Ok()\n}\n\n"
	src := fnMainAfter(decls, "io.inspect(par‸se(\"4\"))\n")
	got := checkRefactor(t, src, "Pattern match on the result of parse", rewrite)
	want := fnMainAfter(decls, "io.inspect(\n    case parse(\"4\") {\n        .Ok(value) -> todo\n        .Err(value) -> todo\n    }\n)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPatternMatch_Refused(t *testing.T) {
	// Not an enum.
	refuseRefactor(t, fnMain("n‸ = 4\nio.inspect(n)\n"), "Pattern match")
	refuseRefactor(t, fnMain("io.inspect(String.len‸gth(\"a\"))\n"), "Pattern match")
}
