package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
	"github.com/sourcegraph/jsonrpc2"
)

// incrementalProject is a three-file project: an entry, a library it
// imports, and a test file that imports the library.
var incrementalProject = map[string]string{
	"main.nomi": `import {
    std/io
    shapes.{self, Shape, Circle, Square}
}

fn main() {
    io.print(Shape.area(Circle{radius: 2}))
    io.print(shapes.describe(Square{side: 3}))
}
`,
	"shapes.nomi": `pub interface Shape {
    fn area(value: self) -> Int
}

pub struct Circle {
    radius: Int
}

pub struct Square {
    side: Int
}

impl Shape for Circle {
    fn area(value: Circle) -> Int {
        value.radius * value.radius * 3
    }
}

impl Shape for Square {
    fn area(value: Square) -> Int {
        value.side * value.side
    }
}

pub fn describe(shape: Shape) -> String {
    size = Shape.area(shape)
    "a shape of area ${size}"
}
`,
	"shapes_test.nomi": `import shapes.{Shape, Circle}

test "a circle's area" {
    assert Shape.area(Circle{radius: 1}) == 3
}
`,
}

// TestIncremental_BodyEditsConcurrentWithRequests edits a function body
// over and over through the language server while requests read the
// analyses the edits rewrite in place, and the lowering check lowers the
// entry from its analysis. Each edit rebuilds only the edited file's bodies
// (DocumentManager.ProgramChecks), and the last edit's diagnostics are those
// of a fresh analysis. Run with -race.
func TestIncremental_BodyEditsConcurrentWithRequests(t *testing.T) {
	dir := t.TempDir()
	for name, text := range incrementalProject {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	uri := func(name string) string { return "file://" + filepath.Join(dir, name) }
	// The process's first lowering lowers the whole stdlib, holding the
	// view for as long; it is done before the edits.
	if _, err := vmhost.CheckLowering(context.Background(), filepath.Join(dir, "main.nomi"), incrementalProject["main.nomi"]); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.sched.delay = time.Millisecond
	s.lowering.delay = 5 * time.Millisecond
	c := startRPC(t, s, dir)
	for name, text := range incrementalProject {
		c.open(uri(name), text)
	}
	c.waitDiagnostics(uri("main.nomi"))
	ctx := context.Background()
	request := func(method string, params any) {
		var res any
		if err := c.conn.Call(ctx, method, params, &res); err != nil {
			var rpcErr *jsonrpc2.Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != codeContentModified {
				t.Errorf("%s: %v", method, err)
			}
		}
	}
	base := incrementalProject["shapes.nomi"]
	const edits = 40
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := range edits {
			text := strings.Replace(base, "value.side * value.side", fmt.Sprintf("value.side * value.side + %d", i), 1)
			if i%3 == 0 {
				// A line more: every later position moves.
				text = strings.Replace(text, "    size = Shape.area(shape)\n", "    size = Shape.area(shape)\n\n", 1)
			}
			c.change(uri("shapes.nomi"), i+2, text)
			time.Sleep(3 * time.Millisecond)
		}
	}()
	for _, name := range []string{"shapes.nomi", "main.nomi", "shapes_test.nomi"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc := map[string]any{"uri": uri(name)}
			for line := 0; ; line = (line + 1) % 20 {
				select {
				case <-done:
					return
				default:
				}
				pos := map[string]any{"line": line, "character": 8}
				request("textDocument/hover", map[string]any{"textDocument": doc, "position": pos})
				request("textDocument/definition", map[string]any{"textDocument": doc, "position": pos})
				request("textDocument/documentHighlight", map[string]any{"textDocument": doc, "position": pos})
				request("textDocument/semanticTokens/full", map[string]any{"textDocument": doc})
				request("textDocument/references", map[string]any{"textDocument": doc, "position": pos, "context": map[string]any{"includeDeclaration": true}})
			}
		}()
	}
	wg.Wait()
	// The last edit's analysis, once it is installed.
	if !s.awaitAnalysis(ctx, uri("shapes.nomi"), 10*time.Second) {
		t.Fatal("the last edit was not analyzed")
	}
	bodies, dependents, full, reused := s.docs.ProgramChecks()
	t.Logf("%d analyses rebuilt bodies, %d checked dependents, %d checked whole programs, %d reused one", bodies, dependents, full, reused)
	t.Logf("whole programs checked because: %v", s.docs.FullCheckReasons())
	if bodies == 0 {
		t.Errorf("no edit rebuilt only bodies")
	}
	snap := s.docs.Snapshot(uri("shapes.nomi"))
	fresh := NewServer()
	fresh.docs.Open(uri("shapes.nomi"), snap.Content)
	want := fresh.docs.Snapshot(uri("shapes.nomi")).Analysis.TypeErrors
	release := s.docs.View()
	got := snap.Analysis.TypeErrors
	release()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("after the edits the document's diagnostics are\n%v\nand a fresh analysis's\n%v", got, want)
	}
}
