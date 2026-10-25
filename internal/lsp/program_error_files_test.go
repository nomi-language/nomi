package lsp

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// orphanChild is an imported file with an orphan impl at 6:5..6:8 (the
// function `to_string`, line 6, column 8).
const orphanChild = `pub struct Box {
    n: Int
}

impl Display for Bool {
    fn to_string(_b: Bool) -> String {
        "b"
    }
}
`

// fixedChild is orphanChild without the orphan impl, its other lines kept.
const fixedChild = `pub struct Box {
    n: Int
}
`

const orphanMain = `import {
    std/io
    child.Box
}

fn main() {
    io.inspect(#Box{n: 1})
}
`

// orphanDiags are the orphan-impl diagnostics among diags.
func orphanDiags(diags []protocol.Diagnostic) []protocol.Diagnostic {
	var out []protocol.Diagnostic
	for _, d := range diags {
		if strings.HasPrefix(d.Message, "orphan impl:") {
			out = append(out, d)
		}
	}
	return out
}

// publishedOrphans are the orphan-impl diagnostics the server publishes for
// the open document uri.
func publishedOrphans(t *testing.T, s *Server, uri string) []protocol.Diagnostic {
	t.Helper()
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		t.Fatalf("%s: no analysis", uri)
	}
	return orphanDiags(buildPublishedDiagnostics(snap.URI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors))
}

// wantOneAt fails unless diags is one diagnostic starting at the 1-based
// line and column.
func wantOneAt(t *testing.T, what string, diags []protocol.Diagnostic, line, col uint32) {
	t.Helper()
	if len(diags) != 1 || diags[0].Range.Start.Line != line-1 || diags[0].Range.Start.Character != col-1 {
		t.Fatalf("%s: %+v, want one orphan impl at %d:%d", what, diags, line, col)
	}
}

// An orphan impl in an imported file is the program's error, found while the
// entry is checked. The server publishes it on the imported file, open or
// not, and not on the entry, where it was shown at the imported file's line;
// it goes away from the imported file when the impl is removed.
func TestOrphanImplInAnImportedFileIsPublishedOnThatFile(t *testing.T) {
	files := map[string]string{"main.nomi": orphanMain, "child.nomi": orphanChild}

	t.Run("open", func(t *testing.T) {
		s, mainURI := openProject(t, files, "main.nomi")
		childURI := strings.TrimSuffix(mainURI, "main.nomi") + "child.nomi"
		if ds := publishedOrphans(t, s, mainURI); len(ds) != 0 {
			t.Errorf("main.nomi shows child.nomi's orphan impl: %+v", ds)
		}
		s.docs.Open(childURI, files["child.nomi"])
		wantOneAt(t, "child.nomi", publishedOrphans(t, s, childURI), 6, 8)
		if ds := publishedOrphans(t, s, mainURI); len(ds) != 0 {
			t.Errorf("main.nomi shows child.nomi's orphan impl: %+v", ds)
		}
		s.docs.Update(childURI, fixedChild)
		if ds := publishedOrphans(t, s, childURI); len(ds) != 0 {
			t.Errorf("child.nomi still shows the orphan impl it no longer has: %+v", ds)
		}
	})

	t.Run("unopened", func(t *testing.T) {
		root := writeProject(t, files)
		s, published := recordingServer()
		s.docs.SetWorkspaceRoot(root)
		s.scanWorkspace()
		waitClosedDiagnostics(t, s, time.Minute)
		pubs := published()
		mainURI, childURI := pathToURI(filepath.Join(root, "main.nomi")), pathToURI(filepath.Join(root, "child.nomi"))
		if _, ok := pubs[mainURI]; !ok {
			t.Fatal("no diagnostics published for main.nomi")
		}
		if ds := orphanDiags(pubs[mainURI]); len(ds) != 0 {
			t.Errorf("main.nomi shows child.nomi's orphan impl: %+v", ds)
		}
		wantOneAt(t, "child.nomi", orphanDiags(pubs[childURI]), 6, 8)
	})
}

// A file two entries reach is analyzed through one program, and shows the
// orphan impl in it once; neither entry shows it.
func TestOrphanImplInAFileTwoEntriesReachIsPublishedOnce(t *testing.T) {
	files := map[string]string{
		"nomi.toml":  "[module]\nname = \"app\"\nentry_points = [\"main\", \"tool\"]\n",
		"main.nomi":  orphanMain,
		"tool.nomi":  orphanMain,
		"child.nomi": orphanChild,
	}
	s, mainURI := openProject(t, files, "main.nomi")
	dir := strings.TrimSuffix(mainURI, "main.nomi")
	toolURI, childURI := dir+"tool.nomi", dir+"child.nomi"
	s.docs.Open(toolURI, files["tool.nomi"])
	s.docs.Open(childURI, files["child.nomi"])
	for _, uri := range []string{mainURI, toolURI} {
		if ds := publishedOrphans(t, s, uri); len(ds) != 0 {
			t.Errorf("%s shows child.nomi's orphan impl: %+v", filepath.Base(uri), ds)
		}
	}
	wantOneAt(t, "child.nomi", publishedOrphans(t, s, childURI), 6, 8)
}

// An orphan impl in another module's file is published on that file, which
// is checked as its own module's, and not on the entry that imports it.
func TestOrphanImplInAnotherModuleIsPublishedOnItsFile(t *testing.T) {
	files := map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"app/go.mod":    "module app\n\ngo 1.27.0\n\nrequire fx v0.0.0\n\nreplace fx => ../fx\n",
		"app/main.nomi": "import {\n    std/io\n    fx/rate.Rate\n}\n\nfn main() {\n    io.inspect(#Rate{per_unit: 4})\n}\n",
		"fx/nomi.toml":  "[module]\nname = \"fx\"\n",
		"fx/go.mod":     "module fx\n\ngo 1.27.0\n",
		"fx/rate.nomi":  strings.Replace(orphanChild, "Box {\n    n: Int", "Rate {\n    per_unit: Int", 1),
	}
	s, mainURI := openProject(t, files, "app/main.nomi")
	if ds := publishedOrphans(t, s, mainURI); len(ds) != 0 {
		t.Errorf("app/main.nomi shows fx/rate.nomi's orphan impl: %+v", ds)
	}
	rateURI := strings.TrimSuffix(mainURI, "app/main.nomi") + "fx/rate.nomi"
	s.docs.Open(rateURI, files["fx/rate.nomi"])
	wantOneAt(t, "fx/rate.nomi", publishedOrphans(t, s, rateURI), 6, 8)
}
