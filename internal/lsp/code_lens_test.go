package lsp

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/vmhost"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const codeLensSrc = `//! assert double(2) == 4
fn double(n: Int) -> Int
    n * 2
end

struct Box
    n: Int
end

impl Box
    //! assert Box.get(Box{n: 1}) == 1
    fn get(b: Box) -> Int
        b.n
    end
end

fn main()
    _ = double(1)
end

test "doubles"
    assert double(3) == 6
end

group "boxes"
    test "get"
        assert Box.get(Box{n: 2}) == 2
    end

    test "wrong"
        assert Box.get(Box{n: 2}) == 3
    end
end
`

func TestCodeLens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	uri := pathToURI(path)
	s := NewServer()
	s.docs.Open(uri, codeLensSrc)
	lenses, err := s.textDocumentCodeLens(nil, &protocol.CodeLensParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lenses {
		if l.Command == nil {
			t.Fatalf("lens without a command at %s", fmtRange(l.Range))
		}
		args := fmt.Sprint(l.Command.Arguments[1:]...)
		if l.Command.Arguments[0] != uri {
			t.Errorf("lens %q: argument %v, want the document's URI", l.Command.Title, l.Command.Arguments[0])
		}
		got = append(got, fmt.Sprintf("%s %s %s %s", fmtRange(l.Range), l.Command.Title, l.Command.Command, args))
	}
	want := []string{
		"0:0-0:3 Run attached test nomi.runTest 1",
		"0:0-0:3 Debug attached test nomi.debugTest 1",
		"10:4-10:7 Run attached test nomi.runTest 11",
		"10:4-10:7 Debug attached test nomi.debugTest 11",
		"16:3-16:7 Run nomi.runMain ",
		"16:3-16:7 Debug nomi.debugMain ",
		"20:0-20:4 Run test nomi.runTest 21",
		"20:0-20:4 Debug test nomi.debugTest 21",
		"24:0-24:5 Run group nomi.runTest 25",
		"24:0-24:5 Debug group nomi.debugTest 25",
		"25:4-25:8 Run test nomi.runTest 26",
		"25:4-25:8 Debug test nomi.debugTest 26",
		"29:4-29:8 Run test nomi.runTest 30",
		"29:4-29:8 Debug test nomi.debugTest 30",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lenses:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Each runTest lens's line selects exactly the tests it sits above when
// handed to `nomi test --line`, as the clients run it.
func TestCodeLens_LinesSelectTheirTests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	prog, err := vmhost.LoadFileSource(path, codeLensSrc)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(codeLensSrc))
	want := map[int]string{
		1:  "pass",
		11: "pass",
		21: "pass",
		25: "pass fail",
		26: "pass",
		30: "fail",
	}
	for _, l := range codeLenses(pathToURI(path), nodes) {
		if l.Command.Command != commandRunTest {
			continue
		}
		line := l.Command.Arguments[1].(int)
		var got []string
		for _, c := range prog.Cases(io.Discard, vmhost.TestOptions{Line: line, LineSet: true}) {
			if c.Blocked != nil {
				t.Fatalf("--line %d: %s blocked: %v", line, c.Name, c.Blocked)
			}
			if c.Err == nil {
				got = append(got, "pass")
			} else {
				got = append(got, "fail")
			}
		}
		if strings.Join(got, " ") != want[line] {
			t.Errorf("--line %d selects %v, want %s", line, got, want[line])
		}
	}
}
