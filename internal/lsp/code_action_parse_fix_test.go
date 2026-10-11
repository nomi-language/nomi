package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const parseFixURI = "file:///parsefix/main.nomi"

// parseFixActions opens src and returns the fixes titled title offered over
// every diagnostic the client holds.
func parseFixActions(t *testing.T, s *Server, src, title string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(parseFixURI, src)
	snap := s.docs.Snapshot(parseFixURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(parseFixURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(parseFixURI)},
		Context: protocol.CodeActionContext{
			Diagnostics: diags,
			Only:        []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
		},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	actions, _ := res.([]protocol.CodeAction)
	var out []protocol.CodeAction
	for _, a := range actions {
		if a.Title == title {
			out = append(out, a)
		}
	}
	return out
}

// applyAllEdits applies every edit of action, last first.
func applyAllEdits(content string, action protocol.CodeAction) string {
	edits := action.Edit.Changes[protocol.DocumentUri(parseFixURI)]
	for i := len(edits) - 1; i >= 0; i-- {
		content = applyTextEdit(content, edits[i])
	}
	return content
}

// Each fix gives the program the user meant: it checks with no
// diagnostics, and `nomi fmt` leaves it as it is. (A case holding a braced
// arm is one `nomi fmt` writes with every arm broken and a blank line
// between arms, so the sibling arms here are written that way already; the
// fix itself only touches the arm it braces.)
func TestParseFix_RepairsTheProgram(t *testing.T) {
	for _, tc := range []struct{ name, title, src, want string }{
		{
			"case arm, body on the next line", "Put the arm's body in braces",
			`import std/io

pub fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) ->
            io.print("got ${n}")
            n

        Err(_) ->
            0
    }
}
`,
			`import std/io

pub fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) -> {
            io.print("got ${n}")
            n
        }

        Err(_) ->
            0
    }
}
`,
		},
		{
			"case arm, body on the arrow's line", "Put the arm's body in braces",
			`import std/io

pub fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) -> io.print("got ${n}")
              n

        Err(_) ->
            0
    }
}
`,
			`import std/io

pub fn f(r: Result<Int, String>): Int {
    case r {
        Ok(n) -> {
            io.print("got ${n}")
            n
        }

        Err(_) ->
            0
    }
}
`,
		},
		{
			"else arm", "Put the arm's body in braces",
			`import std/io

pub fn f(r: Result<Int, String>): Int {
    Ok(n) = r else {
        Err(msg) ->
            io.print(msg)
            0
    }

    n
}
`,
			`import std/io

pub fn f(r: Result<Int, String>): Int {
    Ok(n) = r else {
        Err(msg) -> {
            io.print(msg)
            0
        }
    }

    n
}
`,
		},
		{
			"lambda argument", "Put the lambda's body in braces",
			`import std/io

pub fn f(xs: List<Int>): Int {
    xs
    |> Iter.reduce(|acc = 0, x|
        io.print("x")
        acc + x)
}
`,
			`import std/io

pub fn f(xs: List<Int>): Int {
    xs
    |> Iter.reduce(|acc = 0, x| {
        io.print("x")
        acc + x
    })
}
`,
		},
		{
			"lambda argument closed below", "Put the lambda's body in braces",
			`import std/io

pub fn f(xs: List<Int>) {
    xs
    |> Iter.each(|x|
        io.print("x")
        io.print("${x}")
    )
}
`,
			`import std/io

pub fn f(xs: List<Int>) {
    xs
    |> Iter.each(|x| {
        io.print("x")
        io.print("${x}")
    })
}
`,
		},
		{
			"doc comment run", "Use `//` comments",
			`pub fn f(text: String): Int {
    /// if text == "" { 0 }
    /// else
    String.length(text)
}
`,
			`pub fn f(text: String): Int {
    // if text == "" { 0 }
    // else
    String.length(text)
}
`,
		},
		{
			"prompt before a statement", "Use `//` comments",
			`pub fn f(x: Int): Int {
    //! assert f(1) == 1
    x
}
`,
			`pub fn f(x: Int): Int {
    // assert f(1) == 1
    x
}
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := parseFixActions(t, s, tc.src, tc.title)
			if len(actions) != 1 {
				t.Fatalf("got %d fixes, want 1", len(actions))
			}
			a := actions[0]
			if a.Kind == nil || *a.Kind != protocol.CodeActionKindQuickFix || a.IsPreferred == nil || !*a.IsPreferred || len(a.Diagnostics) != 1 {
				t.Errorf("fix is not a preferred quick fix naming its diagnostic")
			}
			got := applyAllEdits(tc.src, a)
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(parseFixURI, got)
			snap := s.docs.Snapshot(parseFixURI)
			if diags := buildPublishedDiagnostics(parseFixURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors); len(diags) != 0 {
				t.Errorf("fixed program has diagnostics: %v", diags)
			}
			formatted, err := format.Format(got)
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			if formatted != got {
				t.Errorf("format changes the fixed program:\n%s", formatted)
			}
		})
	}
}

// A parse error with no fix gets none.
func TestParseFix_NoneForOtherErrors(t *testing.T) {
	s := NewServer()
	src := "pub fn f(): Int {\n    1 +\n}\n"
	for _, title := range []string{"Put the arm's body in braces", "Put the lambda's body in braces", "Use `//` comments"} {
		if got := parseFixActions(t, s, src, title); len(got) != 0 {
			t.Errorf("offered %q for an unrelated error", title)
		}
	}
}
