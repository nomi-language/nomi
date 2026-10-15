package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/frontend"
)

// nonASCIILines each put non-ASCII text before an `x`: `°`, a precomposed
// and a combining `é`, a thumbs-up with a skin-tone modifier, and CJK.
var nonASCIILines = []string{
	`"°" x`,
	`"é" x`,
	"\"é\" x",
	`"👍🏽" x`,
	`"日本" x`,
}

// TestOffsetToPosition_CountsUTF16 pins the position of a byte offset,
// which every edit built from offsets (import fixes, organize imports, the
// refactors' line edits, literal completions) goes through.
func TestOffsetToPosition_CountsUTF16(t *testing.T) {
	for _, line := range nonASCIILines {
		content := "first\n" + line + "\n"
		off := strings.Index(content, "x")
		got := offsetToPosition(content, off)
		if want := uint32(utf16Col(line, strings.Index(line, "x"))); got.Line != 1 || got.Character != want {
			t.Errorf("%q: position of x = %+v, want 1:%d", line, got, want)
		}
	}
}

// TestFullDocumentRange_EndsAtTheUTF16End pins the formatting edit's end on a
// last line without a newline after it.
func TestFullDocumentRange_EndsAtTheUTF16End(t *testing.T) {
	for _, line := range nonASCIILines {
		r := fullDocumentRange("first\n" + line)
		if want := uint32(utf16Col(line, len(line))); r.End.Line != 1 || r.End.Character != want {
			t.Errorf("%q: end = %+v, want 1:%d", line, r.End, want)
		}
	}
}

// TestSignatureHelp_CursorAfterNonASCII pins the active parameter when the
// cursor's UTF-16 column, read as a byte column, would fall inside the
// string argument before it.
func TestSignatureHelp_CursorAfterNonASCII(t *testing.T) {
	for _, s := range []string{"°°°°", "éééé", "éé", "👍🏽", "日本語"} {
		line := `  f(1, "` + s + `", 3)`
		char := utf16Col(line, strings.Index(line, "3"))
		name, param := findCallContext("fn main() {\n"+line+"\n}\n", 1, char)
		if name != "f" || param != 2 {
			t.Errorf("%q: findCallContext = %q, %d; want f, 2", line, name, param)
		}
	}
}

// TestDiagnostics_RelatedInAnotherFileIsUTF16 pins a related location in
// another file in that file's UTF-16 columns.
func TestDiagnostics_RelatedInAnotherFileIsUTF16(t *testing.T) {
	dir := t.TempDir()
	for _, line := range nonASCIILines {
		other := filepath.Join(dir, "other.nomi")
		if err := os.WriteFile(other, []byte("// "+line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		text := "// " + line
		b := strings.Index(text, "x")
		errs := []analysis.TypeError{{Line: 1, Col: 1, Message: "m", Related: []analysis.RelatedInfo{
			{File: other, Line: 1, Col: b + 1, EndLine: 1, EndCol: b + 2, Message: "there"},
		}}}
		diags := buildPublishedDiagnostics(pathToURI(filepath.Join(dir, "main.nomi")), "x = 1\n", nil, nil, errs)
		if len(diags) != 1 || len(diags[0].RelatedInformation) != 1 {
			t.Fatalf("%q: diagnostics %+v, want one with one related location", line, diags)
		}
		r := diags[0].RelatedInformation[0].Location.Range
		if want := uint32(utf16Col(text, b)); r.Start.Character != want || r.End.Character != want+1 {
			t.Errorf("%q: related range %+v, want %d..%d", line, r, want, want+1)
		}
	}
}

// TestLoweringDiagnostics_RelatedInAnotherFileIsUTF16 pins the related
// location of a lowering blocker in another file of the program in that
// file's UTF-16 columns.
func TestLoweringDiagnostics_RelatedInAnotherFileIsUTF16(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.nomi")
	text := `  y = "°👍🏽日" x`
	if err := os.WriteFile(other, []byte(text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := strings.Index(text, "x")
	saved := checkLoweringFn
	checkLoweringFn = func(_ context.Context, path, src string, _ *analyzedlowering.Analyzed, _ *analyzedlowering.Memo) (error, error) {
		return frontend.Diagnostics{{Path: other, Line: 1, Col: b + 1, EndLine: 1, EndCol: b + 2, Message: "m"}}, nil
	}
	t.Cleanup(func() { checkLoweringFn = saved })
	diags := loweringDiagnostics(filepath.Join(dir, "main.nomi"), "fn main() {}\n", nil)
	if len(diags) != 1 || len(diags[0].RelatedInformation) != 1 {
		t.Fatalf("diagnostics %+v, want one with one related location", diags)
	}
	r := diags[0].RelatedInformation[0].Location.Range
	if want := uint32(utf16Col(text, b)); r.Start.Character != want || r.End.Character != want+1 {
		t.Errorf("related range %+v, want %d..%d", r, want, want+1)
	}
}

// utf16Col is the UTF-16 column of byte offset off in line, from Go's own
// encoder, independent of the conversion under test.
func utf16Col(line string, off int) int {
	return len(utf16.Encode([]rune(line[:off])))
}

// TestSemanticTokens_ColumnsAreUTF16 pins token columns and lengths in
// UTF-16 code units when non-ASCII text precedes a token on its line: `°`
// and a precomposed `é` (2 bytes, 1 unit), `e` with a combining acute
// (3 bytes, 2 units), a thumbs-up with a skin-tone modifier (8 bytes, 4
// units, two surrogate pairs) and CJK (3 bytes, 1 unit each), each between
// two tokens so the same-line deltas cross it. Counted in bytes, `unit`
// after `°` lit up one column right, its `u` uncoloured.
func TestSemanticTokens_ColumnsAreUTF16(t *testing.T) {
	src := "fn label(n: Int, unit: String): String {\n" +
		"  \"${n} °${unit} é${n} é${unit} 👍🏽${n} 日本${unit} 語\"\n" +
		"}\n"
	s := NewServer()
	uri := pathToURI(filepath.Join(t.TempDir(), "main.nomi"))
	doc := s.docs.Open(uri, src)
	toks := decodeSemanticTokenData(encodeSemanticTokens(doc.Analysis, doc.Nodes, doc.Content))

	line := strings.Split(src, "\n")[1]
	var want []decodedSemanticToken
	for i := 0; i < len(line); i++ {
		if !strings.HasPrefix(line[i:], "${") {
			continue
		}
		name := line[i+2 : i+2+strings.IndexByte(line[i+2:], '}')]
		want = append(want, decodedSemanticToken{line: 1, col: utf16Col(line, i+2), length: len(name), tokenType: 6})
	}
	if len(want) != 6 {
		t.Fatalf("test source has %d interpolations, want 6", len(want))
	}
	// The reported case, spelled out: `unit` after `°` starts at unit 11.
	if want[1].col != 11 {
		t.Fatalf("expected `unit` after ° at UTF-16 column 11, computed %d", want[1].col)
	}
	var got []decodedSemanticToken
	for _, tok := range toks {
		if tok.line == 1 {
			got = append(got, tok)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("line 2 tokens = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
