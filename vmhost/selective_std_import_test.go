package vmhost_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A top-level std function a selective import brings in bare runs as its
// qualified spelling does: a host fn (`read_line`, `print`, `write`,
// `inspect`), a std/fs function (`read_text`, `write_text`, which has a
// defaulted parameter), a non-generic Nomi body
// (`json.shape_error_root`), a generic one (`io.capture`) and
// `testing.check`, under its own name or an alias. Input reaches `read_line`
// through `io.capture`, so the program reads no real stdin.
func TestSelectiveStdImport_BareCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	src := strings.ReplaceAll(`import std/io.{read_line, print, write, inspect}
import std/fs.{read_text, write_text}
import std/io.{capture, capture as run_with}
import std/io.{read_line as next_line, print as say}
import std/json.{shape_error_root, Json}
import std/testing.{check, check as verify}

fn main() {
    lines = capture("one\ntwo", fn -> [read_line(), next_line(), read_line()])
    inspect(lines.value)
    printed = run_with("", fn -> {
        print("p")
        write("w")
        inspect([1])
        say("s")
    })
    write(printed.output)
    inspect(write_text("PATH", "saved"))
    inspect(read_text("PATH"))
    inspect(shape_error_root("Int", Json.Null))
    print(Result.ok?(check(1 == 1)))
    print(Result.ok?(verify(1 == 2)))
}
`, "PATH", path)
	got, err := runCaptureProgram(t, src, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `[Ok(Some("one")), Ok(Some("two")), Ok(None)]
p
w[1]
s
Ok(Unit)
Ok("saved")
#Json.ShapeError{path: [], expected: "Int", got: "null"}
True
False
`
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// The same names, bare or aliased, are function values.
func TestSelectiveStdImport_FunctionValues(t *testing.T) {
	got, err := runCaptureProgram(t, `import std/io
import std/fs.{self, read_text}
import std/io.{print, write, inspect, capture}
import std/io.{read_line as next_line, print as say}
import std/json.{shape_error_root, Json}

fn main() {
    Iter.each([1, 2], print)
    Iter.each([3], inspect)
    Iter.each(["a", "b"], write)
    Iter.each(["!"], say)
    read = io.capture("x\ny", fn -> {
        f = next_line
        [f(), f()]
    })
    inspect(read.value)
    missing = Iter.map(["/nonexistent/q"], read_text) |> Iter.to_list()
    inspect(missing)
    qualified = Iter.map(["/nonexistent/q"], fs.read_text) |> Iter.to_list()
    inspect(qualified == missing)
    root = shape_error_root
    inspect(root("Int", Json.Null).expected)
    run: (String, () -> Int) -> io.Captured<Int> = capture
    inspect(run("", fn -> 4).value)
}
`, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `1
2
3
ab!
[Ok(Some("x")), Ok(Some("y"))]
[Err(#NotFound{path: "/nonexistent/q"})]
True
"Int"
4
`
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
