package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const defaultTypeURI = "file:///defaulttype/main.nomi"

// defaultTypeActionsAt answers the code actions at the ▮ in src, which is
// otherwise the document, narrowed to only (all kinds when empty).
func defaultTypeActionsAt(t *testing.T, src string, only ...protocol.CodeActionKind) (string, []protocol.CodeAction) {
	t.Helper()
	at := strings.Index(src, "▮")
	if at < 0 {
		t.Fatal("no ▮ in the source")
	}
	content := src[:at] + src[at+len("▮"):]
	s := NewServer()
	s.docs.Open(defaultTypeURI, content)
	pos := offsetToPosition(content, at)
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(defaultTypeURI)},
		Range:        protocol.Range{Start: pos, End: pos},
		Context:      protocol.CodeActionContext{Only: only},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := res.([]protocol.CodeAction)
	return content, actions
}

// annotationAction is the action titled title among actions.
func annotationAction(actions []protocol.CodeAction, title string) (protocol.CodeAction, bool) {
	for _, a := range actions {
		if a.Title == title {
			return a, true
		}
	}
	return protocol.CodeAction{}, false
}

// applyAnnotationAction applies a's edits to content.
func applyAnnotationAction(t *testing.T, content string, a protocol.CodeAction) string {
	t.Helper()
	if a.Edit == nil {
		t.Fatalf("%q has no edit", a.Title)
	}
	return applyTextEditsLastFirst(content, a.Edit.Changes[protocol.DocumentUri(defaultTypeURI)])
}

// checksClean reports the errors of content's analysis.
func checksClean(t *testing.T, content string) {
	t.Helper()
	s := NewServer()
	s.docs.Open(defaultTypeURI, content)
	snap := s.docs.Snapshot(defaultTypeURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	if len(snap.Errors) != 0 || len(snap.Analysis.TypeErrors) != 0 {
		t.Fatalf("the edited program has errors: %v %v\n%s", snap.Errors, snap.Analysis.TypeErrors, content)
	}
}

const defaultTypeProgram = `interface Shout
    fn word(value: self) -> String

    fn shout(value: self, marks: String = "!") -> String
        "${Shout.word(value)}${marks}"
    end
end

struct Game
    word: String
    misses: Int = 0
    ratio: Float = 1.5
    tries = 3
end

enum Shape
    Circle{radius: Float = 1.0}
end

impl Shout for Game
    fn word(game: Game) -> String
        game.word
    end
end

impl Game
    fn window(game: Game, offset: Int = game.misses * 2, label = "game") -> String
        "${label} ${offset}"
    end
end

fn search(query: String, limit: Int = 10, offset: Int = 0) -> String
    "${query} ${limit} ${offset}"
end

fn main()
    add = fn a: Int, b: Int = 1 -> a + b
    _ = add(1)
    _ = search("q")
    _ = Game.window(Game{word: "w"})
    _ = Shape.Circle{}
    _ = Shout.shout(Game{word: "w"})
end
`

// The action removes `: Type` and keeps the default on each kind of site
// whose default has exactly the written type, and the program still checks.
func TestCodeActionRemoveTypeAnnotation_Offered(t *testing.T) {
	checksClean(t, defaultTypeProgram)
	cases := []struct{ name, mark, before, after string }{
		{"struct field", "    ▮misses: Int = 0", "misses: Int = 0", "misses = 0"},
		{"float field", "    ratio▮: Float = 1.5", "ratio: Float = 1.5", "ratio = 1.5"},
		{"record-variant field", "Circle{▮radius: Float = 1.0}", "Circle{radius: Float = 1.0}", "Circle{radius = 1.0}"},
		{"function parameter, second on its line", "limit: Int = 10, offset: ▮Int = 0", "offset: Int = 0", "offset = 0"},
		{"function parameter", "fn search(query: String, ▮limit: Int = 10", "limit: Int = 10, ", "limit = 10, "},
		{"impl parameter reading an earlier one", "fn window(game: Game, offset: Int ▮= game.misses", "offset: Int = game.misses", "offset = game.misses"},
		{"interface default function parameter", "fn shout(value: self, ▮marks: String", `marks: String = "!"`, `marks = "!"`},
		{"lambda parameter", "fn a: Int, ▮b: Int = 1", "b: Int = 1", "b = 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plain := strings.ReplaceAll(tc.mark, "▮", "")
			if !strings.Contains(defaultTypeProgram, plain) {
				t.Fatalf("the program has no %q", plain)
			}
			src := strings.Replace(defaultTypeProgram, plain, tc.mark, 1)
			content, actions := defaultTypeActionsAt(t, src)
			a, ok := annotationAction(actions, "Remove type annotation")
			if !ok {
				t.Fatalf("no Remove type annotation among %v", titles(actions))
			}
			if a.Kind == nil || *a.Kind != protocol.CodeActionKindRefactorRewrite || len(a.Diagnostics) != 0 {
				t.Errorf("kind %v, diagnostics %v; want refactor.rewrite with none", a.Kind, a.Diagnostics)
			}
			edited := applyAnnotationAction(t, content, a)
			line := ""
			for _, l := range strings.Split(content, "\n") {
				if strings.Contains(l, plain) {
					line = l
				}
			}
			want := strings.Replace(content, line, strings.Replace(line, tc.before, tc.after, 1), 1)
			if edited != want {
				t.Fatalf("edited:\n%s\nwant:\n%s", edited, want)
			}
			checksClean(t, edited)
		})
	}
}

func titles(actions []protocol.CodeAction) []string {
	var out []string
	for _, a := range actions {
		out = append(out, a.Title)
	}
	return out
}

// No removal is offered where the default needs the annotation to check or
// would give a different type, or away from a field or parameter.
func TestCodeActionRemoveTypeAnnotation_NotOffered(t *testing.T) {
	cases := []struct{ name, src string }{
		{"None", "fn f(▮since: Maybe<Int> = None) -> Maybe<Int>\n    since\nend\n"},
		{"empty list", "fn f(▮xs: List<Int> = []) -> List<Int>\n    xs\nend\n"},
		{"empty set", "struct S\n    ▮m: Set<Int> = Set[]\nend\n\nfn main()\n    _ = S{}\nend\n"},
		{"dot variant", "enum Mode\n    A\n    B\nend\n\nfn f(▮mode: Mode = .A) -> Mode\n    mode\nend\n"},
		{"interface typed", "fn f(▮x: Display = 5) -> String\n    Display.to_string(x)\nend\n"},
		{"embeds coercion", "struct Circle\n    r: Int\nend\n\nenum Shape\n    embeds Circle\nend\n\nfn f(▮s: Shape = Circle{r: 1}) -> Shape\n    s\nend\n"},
		{"alias", "typealias Count Int\n\nfn f(▮n: Count = 1) -> Count\n    n\nend\n"},
		{"cursor on the default", "fn f(limit: Int = ▮10) -> Int\n    limit\nend\n"},
		{"no default", "fn f(▮limit: Int) -> Int\n    limit\nend\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checksClean(t, strings.Replace(tc.src, "▮", "", 1))
			_, actions := defaultTypeActionsAt(t, tc.src)
			if a, ok := annotationAction(actions, "Remove type annotation"); ok {
				t.Fatalf("offered %+v", a)
			}
		})
	}
}

// The actions are refactors only: a request for quick fixes or for
// source.fixAll has none.
func TestCodeActionRemoveTypeAnnotation_NotAFix(t *testing.T) {
	src := "fn f(▮limit: Int = 10) -> Int\n    limit\nend\n"
	for _, only := range []protocol.CodeActionKind{protocol.CodeActionKindQuickFix, codeActionKindSourceFixAll, protocol.CodeActionKindSource} {
		_, actions := defaultTypeActionsAt(t, src, only)
		if _, ok := annotationAction(actions, "Remove type annotation"); ok {
			t.Errorf("offered under only %q", only)
		}
	}
	_, actions := defaultTypeActionsAt(t, src, protocol.CodeActionKindRefactor)
	if _, ok := annotationAction(actions, "Remove type annotation"); !ok {
		t.Errorf("not offered under only refactor: %v", titles(actions))
	}
}

// The inverse writes the type the inlay hint shows.
func TestCodeActionAddTypeAnnotation(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"field", "struct Game\n    ▮tries = 3\nend\n", "struct Game\n    tries: Int = 3\nend\n"},
		{"parameter", "fn f(_query: String, ▮limit = 10) -> Int\n    limit\nend\n", "fn f(_query: String, limit: Int = 10) -> Int\n    limit\nend\n"},
		{"enum parameter", "enum Mode\n    A\n    B\nend\n\nfn f(mo▮de = Mode.A) -> Mode\n    mode\nend\n", "enum Mode\n    A\n    B\nend\n\nfn f(mode: Mode = Mode.A) -> Mode\n    mode\nend\n"},
		{"lambda parameter", "fn main()\n    add = fn a: Int, ▮b = 1 -> a + b\n    _ = add(1)\nend\n", "fn main()\n    add = fn a: Int, b: Int = 1 -> a + b\n    _ = add(1)\nend\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, actions := defaultTypeActionsAt(t, tc.src)
			a, ok := annotationAction(actions, "Add type annotation")
			if !ok {
				t.Fatalf("no Add type annotation among %v", titles(actions))
			}
			if _, ok := annotationAction(actions, "Remove type annotation"); ok {
				t.Error("Remove type annotation offered on an unannotated site")
			}
			edited := applyAnnotationAction(t, content, a)
			if edited != tc.want {
				t.Fatalf("edited:\n%s\nwant:\n%s", edited, tc.want)
			}
			checksClean(t, edited)
			// The annotation it wrote is one the removal takes back.
			start := a.Edit.Changes[protocol.DocumentUri(defaultTypeURI)][0].Range.Start
			off := posToOffset(lineOffsets(edited), int(start.Line)+1, int(start.Character)+1)
			_, back := defaultTypeActionsAt(t, edited[:off]+"▮"+edited[off:])
			if _, ok := annotationAction(back, "Remove type annotation"); !ok {
				t.Errorf("the written annotation is not offered for removal: %v", titles(back))
			}
		})
	}
}

// The added annotation is the inlay hint's text.
func TestCodeActionAddTypeAnnotation_MatchesInlayHint(t *testing.T) {
	src := "struct Game\n    ratio = 1.5\nend\n\nfn f(limit = 10) -> Int\n    limit\nend\n"
	hints := filterByKind(getInlayHints(t, src), InlayHintKindType)
	marks := []string{"struct Game\n    ▮ratio = 1.5", "fn f(▮limit = 10)"}
	if len(hints) != len(marks) {
		t.Fatalf("hints %+v", hints)
	}
	for i, m := range marks {
		plain := strings.ReplaceAll(m, "▮", "")
		_, actions := defaultTypeActionsAt(t, strings.Replace(src, plain, m, 1))
		a, ok := annotationAction(actions, "Add type annotation")
		if !ok {
			t.Fatalf("%q: no Add type annotation", m)
		}
		if got := a.Edit.Changes[protocol.DocumentUri(defaultTypeURI)][0].NewText; got != hints[i].Label {
			t.Errorf("%q: wrote %q, the hint shows %q", m, got, hints[i].Label)
		}
	}
}

// Deciding on the removal checks the default again on a copy of the
// analysis: the document's own tables are left as they were.
func TestCodeActionRemoveTypeAnnotation_LeavesTheAnalysis(t *testing.T) {
	s := NewServer()
	s.docs.Open(defaultTypeURI, defaultTypeProgram)
	snap := s.docs.Snapshot(defaultTypeURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	fa := snap.Analysis
	exprs, refs, errs := len(fa.ExprTypes), len(fa.References), len(fa.TypeErrors)
	offset := strings.Index(defaultTypeProgram, "offset: Int = game.misses")
	pos := offsetToPosition(defaultTypeProgram, offset)
	actions := buildDefaultTypeActions(defaultTypeProgram, snap.Nodes, fa, defaultTypeURI, protocol.Range{Start: pos, End: pos})
	if _, ok := annotationAction(actions, "Remove type annotation"); !ok {
		t.Fatalf("not offered: %v", titles(actions))
	}
	if len(fa.ExprTypes) != exprs || len(fa.References) != refs || len(fa.TypeErrors) != errs {
		t.Errorf("the analysis changed: expr types %d -> %d, references %d -> %d, errors %d -> %d",
			exprs, len(fa.ExprTypes), refs, len(fa.References), errs, len(fa.TypeErrors))
	}
}
