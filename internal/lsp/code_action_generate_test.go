package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const quickfix = protocol.CodeActionKindQuickFix

func TestGenerateFunction_FromCall(t *testing.T) {
	src := fnMain("count = 3\nlabel: String = for‸mat_count(count, \"items\", count + 1)\nio.print(label)\n")
	got := checkRefactor(t, src, "Generate function 'format_count'", quickfix)
	want := fnMain("count = 3\nlabel: String = format_count(count, \"items\", count + 1)\nio.print(label)\n") +
		"\nfn format_count(count: Int, arg2: String, arg3: Int): String {\n    todo\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestGenerateFunction_DiscardedAndPiped(t *testing.T) {
	src := fnMain("[1, 2] |> rep‸ort(label: \"n\")\n\nio.print(\"done\")\n")
	got := checkRefactor(t, src, "Generate function 'report'", quickfix)
	want := fnMain("[1, 2] |> report(label: \"n\")\n\nio.print(\"done\")\n") +
		"\nfn report(arg1: List<Int>, label: String) {\n    todo\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestGenerateFunction_InherentImpl(t *testing.T) {
	user := "struct User {\n    name: String\n}\n\n"
	body := "u = User{name: \"a\"}\ngreeting: String = User.gre‸et(u)\nio.print(greeting)\n"
	// No inherent block yet: a new one follows the calling declaration.
	got := checkRefactor(t, fnMainAfter(user, body), "Generate function 'User.greet'", quickfix)
	want := fnMainAfter(user, "u = User{name: \"a\"}\ngreeting: String = User.greet(u)\nio.print(greeting)\n") +
		"\nimpl User {\n    fn greet(u: User): String {\n        todo\n    }\n}\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	// An existing block gains the function at its end.
	impl := "impl User {\n    fn new(name: String): User {\n        User{name}\n    }\n}\n\n"
	got = checkRefactor(t, fnMainAfter(user+impl, body), "Generate function 'User.greet'", quickfix)
	want = fnMainAfter(user+"impl User {\n    fn new(name: String): User {\n        User{name}\n    }\n\n    fn greet(u: User): String {\n        todo\n    }\n}\n\n", "u = User{name: \"a\"}\ngreeting: String = User.greet(u)\nio.print(greeting)\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// The fix names the diagnostic the client sent, and is offered without it
// anywhere in the call; outside the call it is not.
func TestGenerateFunction_Diagnostics(t *testing.T) {
	src := fnMain("label: String = shout(\"a\")\nio.print(label)\n")
	at := func(marked string) []protocol.CodeAction {
		actions, _ := refactorActions(t, NewServer(), marked)
		var out []protocol.CodeAction
		for _, a := range actions {
			if a.Title == "Generate function 'shout'" {
				out = append(out, a)
			}
		}
		return out
	}
	if got := at(strings.Replace(src, "shout(", "‸shout(", 1)); len(got) != 1 || len(got[0].Diagnostics) != 1 {
		t.Fatalf("on the name: %+v", got)
	}
	s := NewServer()
	clean, rng := refactorSource(t, strings.Replace(src, "\"a\"", "\"‸a\"", 1))
	s.docs.Open(refactorURI, clean)
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(refactorURI)},
		Range:        rng,
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := res.([]protocol.CodeAction)
	found := false
	for _, a := range actions {
		found = found || a.Title == "Generate function 'shout'"
	}
	if !found {
		t.Fatalf("not offered in the arguments without diagnostics: %q", actionTitles(actions))
	}
	if got := at(strings.Replace(src, "io.print", "io.pr‸int", 1)); len(got) != 0 {
		t.Fatal("offered outside the call")
	}
}

func TestGenerateFunction_Refused(t *testing.T) {
	// Nothing says what the call returns.
	refuseRefactor(t, fnMain("x = mys‸tery(1)\nio.inspect(x)\n"), "Generate function", "undefined variable 'mystery'")
	// A type the file does not declare.
	refuseRefactor(t, fnMain("n: Int = String.wid‸th(\"a\")\nio.inspect(n)\n"), "Generate function", "type 'String' has no member 'width'")
}
