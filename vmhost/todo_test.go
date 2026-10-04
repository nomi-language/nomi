package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// runTodo loads src from a file, runs it, and answers the file's path, the
// program's output and its error. A program the VM blocks fails the test: a
// `todo` must lower wherever the checker accepts it.
func runTodo(t *testing.T, src string) (string, string, error) {
	t.Helper()
	path := writeProgram(t, src)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	if b, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("a todo program is blocked: %q", b.Reasons)
	}
	return path, out.String(), err
}

// Reaching a `todo` stops the program with rt's text: the file and line, then
// the reason when there is one. What ran before it ran.
func TestTodo_ReachingOneStopsTheProgram(t *testing.T) {
	path, out, err := runTodo(t, `import std/io

struct Header {
    name: String
    size: Int
}

fn parse(text: String): Header {
    todo "parse the header"
}

fn main() {
    io.print("before")
    h = parse("abc")
    io.print(h.name)
}
`)
	if out != "before\n" {
		t.Errorf("output %q, want only what ran before the todo", out)
	}
	if want := "todo reached at " + path + ":9: parse the header"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

func TestTodo_ABareTodoNamesOnlyItsPlace(t *testing.T) {
	path, _, err := runTodo(t, `fn main() {
    _ = 1
    todo
}
`)
	if want := "todo reached at " + path + ":3"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

// A `todo` takes the type its position expects, in every position below, and
// one on a path the run does not take never traps.
func TestTodo_UntakenTodosDoNotTrapInAnyPosition(t *testing.T) {
	_, out, err := runTodo(t, `import std/io

struct Header {
    name: String
    size: Int
}

enum Shape {
    Circle(Int)
    Square(Int)
}

fn area(s: Shape): Int {
    case s {
        .Square(_) -> todo
        .Circle(r) -> r * r * 3
    }
}

fn first<T>(xs: List<T>, d: T): T {
    case List.head(xs) {
        Some(x) -> x
        None -> todo "empty"
    }
}

fn header(ready: Bool): Header {
    if ready {
        Header{name: "h", size: 1}
    } else {
        Header{name: "h", size: todo "size"}
    }
}

fn twice(n: Int): Int {
    n * 2
}

fn pick(flag: Bool): Int {
    if flag {
        twice(todo)
    } else {
        3
    }
}

fn piped(flag: Bool): Int {
    if flag {
        5 |> todo
    } else {
        4
    }
}

fn main() {
    io.print(Int.to_string(area(Shape.Circle(1))))
    io.print(first(["a"], "b"))
    io.print(Int.to_string(first([7], 0)))
    io.print(Int.to_string(header(True).size))
    io.print(Int.to_string(pick(False)))
    io.print(Int.to_string(piped(False)))
    f = |x: Int| if x > 10 { todo } else { x + 1 }
    io.print(Int.to_string(f(1)))
    ys = [1, 2] |> Iter.map(|x| if x > 5 { todo } else { x * 10 }) |> Iter.to_list()
    io.print(Int.to_string(Iter.count(ys)))
    n: Int = if False { todo } else { 6 }
    io.print(Int.to_string(n))
    if False {
        todo "never"
    }
}
`)
	if err != nil {
		t.Fatalf("an untaken todo trapped: %v", err)
	}
	if want := "3\na\n7\n1\n3\n4\n2\n2\n6\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// A `todo` in a generic body traps in the instance that reaches it, and one
// as a pipe stage traps after the piped value is computed.
func TestTodo_GenericAndPipeStageTodosTrap(t *testing.T) {
	path, _, err := runTodo(t, `fn first<T>(xs: List<T>): T {
    _ = xs
    todo "first"
}

fn main() {
    _ = first([1])
}
`)
	if want := "todo reached at " + path + ":3: first"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
	path, out, err := runTodo(t, `import std/io

fn loud(n: Int): Int {
    io.print("piped")
    n
}

fn main() {
    n: Int = 5 |> loud() |> todo
    io.print(Int.to_string(n))
}
`)
	if out != "piped\n" {
		t.Errorf("output %q, want the piped value computed before the trap", out)
	}
	if want := "todo reached at " + path + ":9"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}
