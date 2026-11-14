package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/vmhost"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// lowerAsARun lowers uri's text content as a lowering run does: from the
// installed analysis, or from the record's kept lowering for the entry of
// a program opened from its record (analysis.StoredLowering), or from the
// text.
func lowerAsARun(s *Server, uri, content string) []protocol.Diagnostic {
	stored := s.docs.StoredLowering(uri, content)
	defer s.docs.View()()
	a, _ := s.installedLoweringAnalysis(uri, content)
	if a == nil && stored != nil {
		a = &analyzedlowering.Analyzed{Stored: stored}
	}
	return s.safeLoweringDiagnostics(context.Background(), uri, content, a)
}

// renderDiags renders diagnostics by range, severity, code and message.
func renderDiags(ds []protocol.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		sev, code := 0, ""
		if d.Severity != nil {
			sev = int(*d.Severity)
		}
		if d.Code != nil {
			code = fmt.Sprint(d.Code.Value)
		}
		fmt.Fprintf(&b, "%v %d %s %s\n", d.Range, sev, code, d.Message)
	}
	return b.String()
}

// TestStoredLoweringMatchesTheText: the entry of a program opened from its
// record is lowered after each edit from the record's kept lowering, the
// files whose texts changed lowered on their own (vmhost's
// checklowering_stored.go), and answers what a lowering of its text does.
// A body edit that leaves nothing to report must be lowered so; an edit
// whose lowering finds something (an invalid typed literal) answers as the
// text's lowering does.
func TestStoredLoweringMatchesTheText(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	dir := writeProject(t, storedProjectFiles)
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	text := storedProjectFiles["main.nomi"]
	writer := NewServer()
	writer.docs.SetWorkspaceRoot(dir)
	writer.docs.Open(uri, text)
	func() {
		defer writer.docs.View()()
		a, _ := writer.installedLoweringAnalysis(uri, text)
		if a == nil || !analyzedlowering.Offered(fileuri.ToPath(uri), text, a) {
			t.Fatalf("the writer's analysis is not offered for lowering: %v", a != nil)
		}
	}()
	if diags := lowerAsARun(writer, uri, text); len(diags) != 0 {
		t.Fatalf("the entry's lowering reports %v", diags)
	}
	writer.docs.NoteLoweredClean(uri, text, analyzedlowering.Kept)

	literal := strings.Replace(text, "fn main()\n", "fn main()\n    _ = Regex\"[\"\n", 1)
	literal = strings.Replace(literal, "    shapes\nend", "    shapes\n    std::regex.Regex\nend", 1)
	edits := []struct {
		what, text string
		relowered  bool
	}{
		{"a body edit", strings.Replace(text, "Circle(2)", "Circle(3)", 1), true},
		{"another body edit", strings.Replace(text, "Circle(2)", "Square(4)", 1), true},
		{"an invalid literal", literal, false},
		{"the text back", text, false},
	}
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.Open(uri, text)
	if opened, _ := s.docs.StoredPrograms(); opened != 1 {
		t.Fatalf("the server opened %d programs from records, want 1", opened)
	}
	for _, e := range edits {
		s.docs.Update(uri, e.text)
		was := vmhost.StoredRelowersForTest()
		got := lowerAsARun(s, uri, e.text)
		relowered := vmhost.StoredRelowersForTest() > was
		problems, err := vmhost.CheckLowering(context.Background(), fileuri.ToPath(uri), e.text)
		want := problemDiagnostics(fileuri.ToPath(uri), e.text, problems, err)
		if renderDiags(got) != renderDiags(want) {
			t.Errorf("after %s the lowering reports\n%s\nwhere the text's reports\n%s", e.what, renderDiags(got), renderDiags(want))
		}
		if e.what == "an invalid literal" && len(want) == 0 {
			t.Errorf("the invalid literal's lowering reports nothing, so the edit says nothing")
		}
		if e.relowered && !relowered {
			t.Errorf("after %s the entry was not lowered from a kept lowering (stored edits %d, reasons %v)", e.what, s.docs.StoredEdits(), s.docs.FullCheckReasons())
		}
	}
}

// TestStoredLoweringMatchesTheTextGenerated is TestStoredLoweringMatchesTheText
// over a generated project of about 2k lines and each of projectgen's edit
// kinds, made in a server that opened the project from its record and
// then undone. Some edit must be lowered from a kept lowering, and so must
// each impl added to a leaf and its undoing: the files that see the impl are
// lowered again with the leaf.
func TestStoredLoweringMatchesTheTextGenerated(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	gen := projectgen.Generate(projectgen.Config{Seed: 3, Lines: 2000})
	dir := t.TempDir()
	if err := gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	uriOf := func(key string) string { return fileuri.FromPath(filepath.Join(dir, filepath.FromSlash(key)+".nomi")) }
	entry := uriOf(gen.Entry)
	writer := NewServer()
	writer.docs.SetWorkspaceRoot(dir)
	writer.docs.Open(entry, gen.Files[gen.Entry])
	if diags := lowerAsARun(writer, entry, gen.Files[gen.Entry]); len(diags) != 0 {
		t.Fatalf("the entry's lowering reports %v", diags)
	}
	writer.docs.NoteLoweredClean(entry, gen.Files[gen.Entry], analyzedlowering.Kept)

	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.Open(entry, gen.Files[gen.Entry])
	if opened, _ := s.docs.StoredPrograms(); opened != 1 {
		t.Fatalf("the server opened %d programs from records, want 1", opened)
	}
	relowered := 0
	for _, kind := range projectgen.EditKinds {
		key, text := gen.Edit(kind, 0)
		for _, step := range []struct{ what, text string }{{string(kind), text}, {string(kind) + ", undone", gen.Files[key]}} {
			doc := uriOf(key)
			if doc != entry && !s.docs.IsOpen(doc) {
				s.docs.Open(doc, gen.Files[key])
			}
			s.docs.Update(doc, step.text)
			s.docs.UpdateImportEdges(doc)
			s.docs.PropagateChange(doc)
			content := gen.Files[gen.Entry]
			if doc == entry {
				content = step.text
			}
			was := vmhost.StoredRelowersForTest()
			got := lowerAsARun(s, entry, content)
			if vmhost.StoredRelowersForTest() > was {
				relowered++
			} else if kind == projectgen.ImplLeaf {
				t.Errorf("after %s the entry was not lowered from a kept lowering", step.what)
			}
			// The text's lowering reads the other files from disk.
			if doc != entry {
				writeFile(t, fileuri.ToPath(doc), step.text)
			}
			problems, err := vmhost.CheckLowering(context.Background(), fileuri.ToPath(entry), content)
			if doc != entry {
				writeFile(t, fileuri.ToPath(doc), gen.Files[key])
			}
			want := problemDiagnostics(fileuri.ToPath(entry), content, problems, err)
			if renderDiags(got) != renderDiags(want) {
				t.Errorf("after %s the entry's lowering reports\n%s\nwhere its text's reports\n%s", step.what, renderDiags(got), renderDiags(want))
			}
		}
	}
	if relowered == 0 {
		t.Errorf("no lowering was made from a kept lowering (stored edits %d, reasons %v)", s.docs.StoredEdits(), s.docs.FullCheckReasons())
	}
	t.Logf("%d lowerings from a kept lowering", relowered)
}

// writeFile writes text at path.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
