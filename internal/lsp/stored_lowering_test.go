package lsp

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// storedProjectFiles is a project of three files the entry reaches.
var storedProjectFiles = map[string]string{
	"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	"main.nomi":   "import {\n    std::io\n    shapes\n}\n\nfn main() {\n    io.print(shapes.describe(shapes.Shape.Circle(2)))\n}\n",
	"shapes.nomi": "import util\n\n/// A shape.\npub enum Shape {\n    Circle(Int)\n    Square(Int)\n}\n\n/// What the shape is.\n//! assert describe(Shape.Square(1)) == \"square 1\"\npub fn describe(s: Shape) -> String {\n    case s {\n        .Circle(r) -> \"circle ${util.twice(r)}\"\n        .Square(n) -> \"square ${n}\"\n    }\n}\n",
	"util.nomi":   "/// n twice.\npub fn twice(n: Int) -> Int {\n    n * 2\n}\n",
}

// waitStored waits until the server's document manager has written n
// records.
func waitStored(t *testing.T, s *Server, n int64) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, stored := s.docs.StoredPrograms(); stored >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no record written")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A server whose lowering of the open entry finds nothing writes the
// program's record; the next server opens the entry from it, takes its
// lowering's answer from the record without lowering, and publishes no
// diagnostic. Its first edit is taken in the program opened from the
// record, with no check of the whole program.
func TestServerOpensAProgramFromItsRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	dir := writeProject(t, storedProjectFiles)
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	text := storedProjectFiles["main.nomi"]

	first := NewServer()
	first.lowering.delay = 10 * time.Millisecond
	c := startRPC(t, first, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)
	waitLoweringRuns(t, first, 1, 0)
	waitStored(t, first, 1)
	if opened, _ := first.docs.StoredPrograms(); opened != 0 {
		t.Fatalf("the first server opened %d programs from records", opened)
	}

	second := NewServer()
	second.lowering.delay = 10 * time.Millisecond
	c2 := startRPC(t, second, dir)
	c2.open(uri, text)
	if p := c2.waitDiagnostics(uri); len(p.msgs) != 0 {
		t.Fatalf("opened from its record, the entry publishes %q", p.msgs)
	}
	if opened, _ := second.docs.StoredPrograms(); opened != 1 {
		t.Fatalf("the second server opened %d programs from records, want 1", opened)
	}
	deadline := time.Now().Add(30 * time.Second)
	for second.lowering.recorded.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the record did not answer the entry's lowering")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := second.lowering.lowered.Load(); n != 0 {
		t.Fatalf("the second server lowered %d times", n)
	}
	if built := second.docs.FilesBuilt(); built != 1 {
		t.Fatalf("opening from the record built %d files, want the entry alone", built)
	}

	c2.change(uri, 2, text+"\nfn unused_helper() -> Int {\n    1\n}\n")
	deadline = time.Now().Add(30 * time.Second)
	for second.docs.StoredEdits() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the first edit was not taken in the program opened from its record: %v", second.docs.FullCheckReasons())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, _, full, _ := second.docs.ProgramChecks(); full != 0 {
		t.Fatalf("the first edit checked the program whole: %v", second.docs.FullCheckReasons())
	}
}
