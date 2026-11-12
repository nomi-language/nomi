package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
	path, out, err := runTodo(t, `import std::io

struct Header
    name: String
    size: Int
end

fn parse(text: String) -> Header
    todo "parse the header"
end

fn main()
    io.print("before")
    h = parse("abc")
    io.print(h.name)
end
`)
	if out != "before\n" {
		t.Errorf("output %q, want only what ran before the todo", out)
	}
	if want := "todo reached at " + path + ":9: parse the header"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

func TestTodo_ABareTodoNamesOnlyItsPlace(t *testing.T) {
	path, _, err := runTodo(t, `fn main()
    _ = 1
    todo
end
`)
	if want := "todo reached at " + path + ":3"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

// A `todo` takes the type its position expects, in every position below, and
// one on a path the run does not take never traps.
func TestTodo_UntakenTodosDoNotTrapInAnyPosition(t *testing.T) {
	_, out, err := runTodo(t, `import std::io

struct Header
    name: String
    size: Int
end

enum Shape
    Circle(Int)
    Square(Int)
end

fn area(s: Shape) -> Int
    case s
        .Square(_) -> todo
        .Circle(r) -> r * r * 3
    end
end

fn first<T>(xs: List<T>, d: T) -> T
    case List.head(xs)
        Some(x) -> x
        None -> todo "empty"
    end
end

fn header(ready: Bool) -> Header
    if ready then
        Header{name: "h", size: 1}
    else
        Header{name: "h", size: todo "size"}
    end
end

fn twice(n: Int) -> Int
    n * 2
end

fn pick(flag: Bool) -> Int
    if flag then
        twice(todo)
    else
        3
    end
end

fn piped(flag: Bool) -> Int
    if flag then
        5 |> todo
    else
        4
    end
end

fn main()
    io.print(Int.to_string(area(Shape.Circle(1))))
    io.print(first(["a"], "b"))
    io.print(Int.to_string(first([7], 0)))
    io.print(Int.to_string(header(True).size))
    io.print(Int.to_string(pick(False)))
    io.print(Int.to_string(piped(False)))
    f = fn x: Int -> if x > 10 then todo else x + 1
    io.print(Int.to_string(f(1)))
    ys = [1, 2] |> Iter.map(fn x -> if x > 5 then todo else x * 10) |> Iter.to_list()
    io.print(Int.to_string(Iter.count(ys)))
    n: Int = if False then todo else 6
    io.print(Int.to_string(n))
    if False then
        todo "never"
    end
end
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
	path, _, err := runTodo(t, `fn first<T>(xs: List<T>) -> T
    _ = xs
    todo "first"
end

fn main()
    _ = first([1])
end
`)
	if want := "todo reached at " + path + ":3: first"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
	path, out, err := runTodo(t, `import std::io

fn loud(n: Int) -> Int
    io.print("piped")
    n
end

fn main()
    n: Int = 5 |> loud() |> todo
    io.print(Int.to_string(n))
end
`)
	if out != "piped\n" {
		t.Errorf("output %q, want the piped value computed before the trap", out)
	}
	if want := "todo reached at " + path + ":9"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

// A `todo` as an operand has the type its operator gives that operand (`not`
// and `and` take Bool, `==` the other side's type, `+` the type it takes on
// its right, an `if` condition Bool), so each form lowers and traps at the
// `todo`, after what ran before it.
func TestTodo_AnOperandTodoTrapsAtTheTodo(t *testing.T) {
	forms := []string{
		"x = not todo",
		"x = not (todo)",
		"x = not not todo",
		"x: Bool = not todo",
		"x = g(not todo)",
		"x = not (if True then todo else todo)",
		"x = todo and True",
		"x = False or todo",
		"x = True and not todo",
		"x = todo == 1",
		"x = 1 == todo",
		"x = todo != \"a\"",
		"x = 1 > todo",
		"x = 1 + todo",
		"x = 2.0 - todo",
		"x = \"a\" + todo",
		"x = loud(1) * todo",
		"x = if todo then 1 else 2",
		"x = if not todo then 1 else 2",
	}
	for _, form := range forms {
		t.Run(form, func(t *testing.T) {
			path, out, err := runTodo(t, `import std::io

fn g(b: Bool) -> Bool
    b
end

fn loud(n: Int) -> Int
    io.print("loud")
    n
end

fn main()
    io.print("before")
    `+form+`
    _ = x
end
`)
			want := "before\n"
			if strings.Contains(form, "loud") {
				want += "loud\n"
			}
			if out != want {
				t.Errorf("output %q, want %q", out, want)
			}
			if want := "todo reached at " + path + ":14"; err == nil || err.Error() != want {
				t.Fatalf("error %v, want %q", err, want)
			}
		})
	}
	path, _, err := runTodo(t, `fn main()
    _ = not todo "negate"
end
`)
	if want := "todo reached at " + path + ":2: negate"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

// An operand `todo` on a path the run does not take never traps: the
// short-circuit operators skip it, and so does an untaken branch.
func TestTodo_UntakenOperandTodosDoNotTrap(t *testing.T) {
	_, out, err := runTodo(t, `import std::io

fn main()
    a = False and todo
    b = True or not todo
    c = if False then todo == 1 else False
    d = if False then 1 + todo else 7
    e = if False then not todo else True
    if False then
        if not todo then
            io.print("never")
        end
    end
    io.print("${a} ${b} ${c} ${d} ${e}")
end
`)
	if err != nil {
		t.Fatalf("an untaken todo trapped: %v", err)
	}
	if want := "False True False 7 True\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// `todo` is a contextual keyword: it names parameters, bindings, fields and
// pattern bindings, and a bare `todo` inside the scope of one reads it. Out
// of every such scope it is the placeholder again, as the `None` arm's is.
func TestTodo_ANameTodoIsAnOrdinaryName(t *testing.T) {
	path, out, err := runTodo(t, `import std::io

struct Todo
    title: String
    done: Bool
end

struct Box
    todo: Todo
end

fn apply(todo: Todo) -> Todo
    todo
end

fn title_of(todo: Todo) -> String
    todo.title
end

fn pick(m: Maybe<Int>) -> Int
    case m
        Some(todo) -> todo
        None -> todo
    end
end

fn main()
    t = Todo{title: "milk", done: False}
    io.print(apply(t).title)
    io.print(title_of(t))
    todo = 1
    io.print(todo + 1)
    box = Box{todo: t}
    io.print(box.todo.title)
    io.print({todo:}.todo)
    f = fn todo: Int -> todo * 10
    io.print(f(4))
    io.print(pick(Some(7)))
    io.print(pick(None))
end
`)
	if want := "milk\nmilk\n2\nmilk\n1\n40\n7\n"; out != want {
		t.Errorf("output %q, want %q", out, want)
	}
	if want := "todo reached at " + path + ":23"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

// A function named `todo` is a binding like any other: in its file a bare
// `todo` names it, and `todo "reason"` is still the placeholder.
func TestTodo_AFunctionNamedTodoShadowsTheBarePlaceholder(t *testing.T) {
	path, out, err := runTodo(t, `import std::io

fn todo() -> Int
    5
end

fn main()
    io.print(todo())
    io.print([1, 2] |> Iter.map(fn _ -> todo()) |> Iter.sum())
    todo "still the placeholder"
end
`)
	if want := "5\n10\n"; out != want {
		t.Errorf("output %q, want %q", out, want)
	}
	if want := "todo reached at " + path + ":10: still the placeholder"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}

// A module named `todo` (a file `todo.nomi` beside the program's) is no
// value: `todo.label()` names a member of it, and a bare `todo` read as a
// value is still the placeholder.
func TestTodo_AModuleNamedTodoLeavesTheBarePlaceholder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "todo.nomi"), []byte("pub fn label() -> String\n    \"from todo\"\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.nomi")
	src := "import std::io\nimport todo\n\nfn later() -> Int\n    todo\nend\n\nfn main()\n    io.print(todo.label())\n    io.inspect(later())\nend\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	if want := "from todo\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	if want := "todo reached at " + path + ":5"; err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}
