package vmhost_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// buildRefusal loads src as main.nomi, requires BuildImage to refuse it with
// *LeftoversRemain, and answers the refusal's text and the file's path.
func buildRefusal(t *testing.T, src string) (string, string) {
	t.Helper()
	path := writeProgram(t, src)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = p.BuildImage()
	var left *vmhost.LeftoversRemain
	if !errors.As(err, &left) {
		t.Fatalf("BuildImage = %v, want *LeftoversRemain", err)
	}
	return err.Error(), path
}

// `nomi build` refuses a program with any `todo` in its files, reached or
// not, and lists each one in source order.
func TestTodo_BuildImageRefusesAProgramWithTodos(t *testing.T) {
	got, path := buildRefusal(t, `fn parse(text: String): Int {
    todo "parse the header"
}

fn main() {
    if False {
        _ = parse("x")
    }
    _ = 1
    if False {
        todo
    }
}
`)
	want := strings.Join([]string{
		"2 todos remain:",
		"  " + path + ":2:5 todo \"parse the header\"",
		"  " + path + ":11:9 todo",
	}, "\n")
	if got != want {
		t.Fatalf("error\n%s\nwant\n%s", got, want)
	}

	clean := writeProgram(t, "fn main() {\n    _ = 1\n}\n")
	p, err := vmhost.Load(clean)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.BuildImage(); err != nil {
		t.Fatalf("a program with no todo or dbg is refused: %v", err)
	}
}

// `nomi build` refuses a program with any `dbg`, reached or not, and shows
// each one's operand on one line: a statement, a pipe stage, one in a
// function nothing calls, and a long multi-line operand, compacted and cut.
func TestDbg_BuildImageRefusesAProgramWithDbgs(t *testing.T) {
	got, path := buildRefusal(t, `fn unused(n: Int): Int {
    dbg n + 1
}

fn main() {
    total = [1, 2, 3] |> Iter.reduce(|sum = 0, n| sum + n) |> dbg
    _ = dbg total
    _ = dbg [
        "a fairly long first element",
        "and a second one",
    ]
}
`)
	want := strings.Join([]string{
		"4 dbgs remain:",
		"  " + path + ":2:5 dbg n + 1",
		"  " + path + ":6:63 |> dbg",
		"  " + path + ":7:9 dbg total",
		"  " + path + ":8:9 dbg [\"a fairly long first element\", \"and a s...",
	}, "\n")
	if got != want {
		t.Fatalf("error\n%s\nwant\n%s", got, want)
	}

	one, path := buildRefusal(t, "fn main() {\n    _ = dbg 1\n}\n")
	if want := "1 dbg remains:\n  " + path + ":2:9 dbg 1"; one != want {
		t.Fatalf("error\n%s\nwant\n%s", one, want)
	}
}

// A program with both is refused once, with one header counting each kind
// and one list in source order.
func TestLeftovers_BuildImageListsTodosAndDbgsTogether(t *testing.T) {
	got, path := buildRefusal(t, `fn later(): Int {
    todo "later"
}

fn main() {
    _ = dbg 1
    if False {
        _ = later()
    }
}
`)
	want := strings.Join([]string{
		"1 todo and 1 dbg remain:",
		"  " + path + ":2:5 todo \"later\"",
		"  " + path + ":6:9 dbg 1",
	}, "\n")
	if got != want {
		t.Fatalf("error\n%s\nwant\n%s", got, want)
	}
}
