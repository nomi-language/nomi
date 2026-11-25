package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
)

// effectErrorsMain has a function that does not check, `broken`, beside
// functions that do: main, which prints and calls noted and broken, and
// noted, which holds a `dbg`.
const effectErrorsMain = `import std::io

fn noted(n: Int) -> Int
    dbg n
end

fn broken(n: Int) -> Int
    io.print("lost")
    label: Int = "n"
    n + label
end

fn main()
    io.print("hi")
    _ = noted(1)
    _ = broken(2)
end
`

// A function with a compile error leaves the marks and hover lines of the
// functions that check as they are in a program with none: main's call of
// io.print and of noted are bold and say their effects, and so are main
// and noted, while the `dbg` keyword in noted says its effect and is not
// bold. The function that does not check has no mark and no line, its own
// io.print included, and the call of it from main says nothing of what it
// may do.
func TestSemanticTokens_MarksStayBesideAFunctionThatDoesNotCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{"main.nomi": effectErrorsMain, "nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n"})
	s, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectErrorsMain})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	requireRefused(t, c.waitDiagnostics(uri), "type mismatch: expected Int, got String")
	waitReachAt(t, s, c, uri, effectErrorsMain, `print("hi")`, "Has effects: `std::io`.")
	waitReachAt(t, s, c, uri, effectErrorsMain, "noted(1)", "Has effects: `dbg`.")
	waitReachAt(t, s, c, uri, effectErrorsMain, "dbg n", "Has effects: `dbg`.")
	waitReachAt(t, s, c, uri, effectErrorsMain, "broken(2)")
	waitReachAt(t, s, c, uri, effectErrorsMain, `print("lost")`)
	waitReachAt(t, s, c, uri, effectErrorsMain, "broken(n")
	toks := semanticTokensOf(t, c, uri)
	for _, tc := range []struct {
		needle, word string
		want         bool
	}{
		{`io.print("hi")`, "print", true},
		{"noted(1)", "noted", true},
		{"fn noted", "noted", true},
		{"fn main", "main", true},
		{"dbg n", "dbg", false},
		{"fn broken", "broken", false},
		{`io.print("lost")`, "print", false},
		{"broken(2)", "broken", false},
	} {
		if got := effectAt(t, toks, effectErrorsMain, tc.needle, tc.word); got != tc.want {
			t.Errorf("%q in %q: effect is %v, want %v", tc.word, tc.needle, got, tc.want)
		}
	}
}

// requireRefused fails the test unless got, a document's diagnostics, holds
// one whose message starts with want: the program must stay refused, as
// `nomi check` refuses it, for the marks beside the error to be tested.
func requireRefused(t *testing.T, got published, want string) {
	t.Helper()
	for _, msg := range got.msgs {
		if strings.HasPrefix(msg, want) {
			return
		}
	}
	t.Fatalf("the document's diagnostics are %q; want one that starts %q", got.msgs, want)
}

// effectErrorsPayment is the tour home page's exhaustiveness block: describe
// misses a variant, and main, which checks, debugs two of its calls.
const effectErrorsPayment = `enum Payment
    Card String
    Transfer (String, Int)
    Cash
end

fn describe(payment: Payment) -> String
    case payment
        .Card(last4)                        -> "card ${last4}"
        .Transfer(bank, days) when days > 2 -> "slow: ${bank}"
        .Transfer(bank, _)                  -> "from ${bank}"
    end
end

fn main()
    dbg describe(.Card("4242"))
    dbg describe(.Transfer("Acme", 5))
end
`

// In the tour's Payment block each `dbg` in main says its effect though
// describe does not compile, and neither `dbg` is bold; the calls of
// describe say nothing of what it may do, and main, which holds the
// `dbg`s, is bold.
func TestSemanticTokens_ADbgBesideAFunctionThatDoesNotCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{"main.nomi": effectErrorsPayment, "nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n"})
	s, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectErrorsPayment})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	requireRefused(t, c.waitDiagnostics(uri), "non-exhaustive case on Payment: missing Cash")
	waitReachAt(t, s, c, uri, effectErrorsPayment, "dbg describe(.Card", "Has effects: `dbg`.")
	waitReachAt(t, s, c, uri, effectErrorsPayment, "dbg describe(.Transfer", "Has effects: `dbg`.")
	waitReachAt(t, s, c, uri, effectErrorsPayment, "describe(.Card")
	toks := semanticTokensOf(t, c, uri)
	for _, tc := range []struct {
		needle, word string
		want         bool
	}{
		{"dbg describe(.Card", "dbg", false},
		{"dbg describe(.Transfer", "dbg", false},
		{"describe(.Card", "describe", false},
		{"fn describe", "describe", false},
		{"fn main", "main", true},
	} {
		if got := effectAt(t, toks, effectErrorsPayment, tc.needle, tc.word); got != tc.want {
			t.Errorf("%q in %q: effect is %v, want %v", tc.word, tc.needle, got, tc.want)
		}
	}
}
