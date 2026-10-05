package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A file that starts with a `#!` line gets diagnostics, hover and semantic
// tokens at the positions of the source as written, and nothing on the
// shebang line itself.
func TestShebangFile_PositionsAreUnshifted(t *testing.T) {
	const uri = "file:///shebang_script.nomi"
	src := "#!/usr/bin/env nomi\n" +
		"import std/io\n" +
		"\n" +
		"fn double(n: Int): Int {\n" +
		"    n + n\n" +
		"}\n" +
		"\n" +
		"fn main() {\n" +
		"    io.print(double(\"x\"))\n" +
		"}\n"
	s := NewServer()
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap == nil {
		t.Fatal("no snapshot")
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("parse errors on a file with a shebang: %+v", snap.Errors)
	}

	var published *protocol.PublishDiagnosticsParams
	s.publishSnapshot(func(method string, params any) {
		if p, ok := params.(*protocol.PublishDiagnosticsParams); ok {
			published = p
		}
	}, snap)
	if published == nil || len(published.Diagnostics) == 0 {
		t.Fatal("want a type-mismatch diagnostic for double(\"x\")")
	}
	for _, d := range published.Diagnostics {
		if d.Range.Start.Line == 0 {
			t.Errorf("diagnostic on the shebang line: %+v", d)
		}
	}
	if d := published.Diagnostics[0]; d.Range.Start.Line != 8 || d.Range.Start.Character != 20 {
		t.Errorf("diagnostic at %d:%d, want 8:20 (the \"x\" argument): %s",
			d.Range.Start.Line, d.Range.Start.Character, d.Message)
	}

	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 3, Character: 4},
		},
	})
	if err != nil || hover == nil {
		t.Fatalf("no hover on `double` at 3:4: %v", err)
	}
	if mc, ok := hover.Contents.(protocol.MarkupContent); !ok || !strings.Contains(mc.Value, "fn double(n: Int): Int") {
		t.Errorf("hover on `double` = %+v", hover.Contents)
	}

	toks, err := s.textDocumentSemanticTokensFull(nil, &protocol.SemanticTokensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	})
	if err != nil || toks == nil || len(toks.Data) < 5 {
		t.Fatalf("no semantic tokens: %v", err)
	}
	// The first token's line is absolute: `io` in `import std/io` on line 1
	// (0-based) at the earliest, never the shebang's line 0.
	if toks.Data[0] == 0 {
		t.Errorf("a semantic token on the shebang line: %v", toks.Data[:5])
	}
	found := false
	line, col := uint32(0), uint32(0)
	for i := 0; i+4 < len(toks.Data); i += 5 {
		if toks.Data[i] > 0 {
			line += toks.Data[i]
			col = toks.Data[i+1]
		} else {
			col += toks.Data[i+1]
		}
		if line == 3 && col == 3 && toks.Data[i+2] == 6 {
			found = true
		}
	}
	if !found {
		t.Errorf("no semantic token for `double` at 3:3")
	}
}

// A file's first import goes below its shebang, which must stay line 1.
func TestImportInsertEdit_KeepsShebangFirst(t *testing.T) {
	cases := map[string]struct{ content, want string }{
		"no shebang":         {"fn main() {}\n", "import std/io\n\nfn main() {}\n"},
		"shebang":            {"#!/usr/bin/env nomi\nfn main() {}\n", "#!/usr/bin/env nomi\nimport std/io\n\nfn main() {}\n"},
		"only a shebang":     {"#!/usr/bin/env nomi", "#!/usr/bin/env nomi\nimport std/io\n"},
		"shebang then blank": {"#!/usr/bin/env nomi\n\nfn main() {}\n", "#!/usr/bin/env nomi\nimport std/io\n\n\nfn main() {}\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			edit := importInsertEdit(tc.content, lineOffsets(tc.content), nil, "import std/io")
			got := applyEditsForShebangTest(tc.content, edit)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func applyEditsForShebangTest(content string, edit protocol.TextEdit) string {
	offs := lineOffsets(content)
	start := offs[edit.Range.Start.Line] + int(edit.Range.Start.Character)
	end := offs[edit.Range.End.Line] + int(edit.Range.End.Character)
	return content[:start] + edit.NewText + content[end:]
}
