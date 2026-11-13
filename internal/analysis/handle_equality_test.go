package analysis_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// std's handles, Task, Sender, Context and the rest of rt.StdHandleTypes,
// have no value to compare, and the runtime answers no equality for one. `==` over one used to pass the checker and stop the program with "not
// supported yet"; the checker now rejects it as it rejects `==` on a
// function (spec §5 *Equality and Ordering Semantics*).
func TestHandleEquality_IsRejected(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"sender", "ch: Channel<Int> = Channel.unbuffered()\n    _ = ch.sender == ch.sender",
			"`==` cannot compare `Sender<Int>`: a handle to running state has no equality"},
		{"receiver", "ch: Channel<Int> = Channel.unbuffered()\n    _ = ch.receiver != ch.receiver",
			"`!=` cannot compare `Receiver<Int>`: a handle to running state has no equality"},
		{"context", "c = Context.root()\n    _ = c == c",
			"`==` cannot compare `Context`: a handle to running state has no equality"},
		{"task", "_ = concurrent\n        t = Task.spawn(fn -> 1)\n        t == t\n    end",
			"`==` cannot compare `Task<Int>`: a handle to running state has no equality"},
		{"held in a Maybe", "c = Some(Context.root())\n    _ = c == c",
			"`==` cannot compare `Maybe<Context>`: it holds `Context`, and a handle to running state has no equality"},
		{"held in a Vector", "v = Vector[Context.root()]\n    _ = v == v",
			"`==` cannot compare `Vector<Context>`: it holds `Context`, and a handle to running state has no equality"},
		{"Iter held in a Vector", "v = Vector[Iter.from(1)]\n    _ = v == v",
			"`==` cannot compare `Vector<Iter<Int>>`: it holds `Iter<Int>`, and an Iter has no equality; materialize it with `Iter.to_list` to compare its elements"},
		{"map key", "m = {Context.root() => 1}\n    _ = m",
			"`Context` cannot be a Map key: a handle to running state has no equality or hashing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import\n    std::channels.Channel\n    std::tasks.Task\nend\n\nfn main()\n    " + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			var got []string
			for _, e := range errs {
				got = append(got, e.Message)
				if e.Message == tc.want {
					return
				}
			}
			t.Errorf("the checker admits this source or reports something else; want %q, got %v\n%s",
				tc.want, got, src)
		})
	}
}

// Every std handle (rt.StdHandleTypes), a stream, a file, a process, a
// connection among them, has no equality: `==` over one, alone or held in a
// List, is rejected with the message a Sender's gets. Each is named by a
// parameter's annotation, so no value of it needs building.
func TestHandleEquality_EveryStdHandleIsRejected(t *testing.T) {
	generic := map[string]string{"Task": "<Int>", "Sender": "<Int>", "Receiver": "<Int>", "Arm": "<Int>"}
	for _, h := range rt.StdHandleTypes() {
		t.Run(h.Name, func(t *testing.T) {
			typ := h.Name + generic[h.Name]
			body := "fn same(a: " + typ + ", b: " + typ + ") -> Bool\n    a == b\nend\n\n" +
				"fn both(a: List<" + typ + ">) -> Bool\n    a == a\nend\n"
			src := "import " + strings.Replace(h.Origin, "/", "::", 1) + "." + h.Name + "\n\n" + body
			_, errs := checkSourceWithStdlib(src)
			for _, e := range errs {
				if strings.Contains(e.Message, "already in scope from the prelude") {
					// A prelude name (Context, Dynamic) is not imported.
					src = body
					_, errs = checkSourceWithStdlib(src)
					break
				}
			}
			want := []string{
				"`==` cannot compare `" + typ + "`: a handle to running state has no equality",
				"`==` cannot compare `List<" + typ + ">`: it holds `" + typ + "`, and a handle to running state has no equality",
			}
			var got []string
			for _, e := range errs {
				got = append(got, e.Message)
			}
			for _, w := range want {
				if !slices.Contains(got, w) {
					t.Errorf("want %q, got %v\n%s", w, got, src)
				}
			}
		})
	}
}

// A handle is no Set element or Map key, and meets no Hashable bound.
func TestHandleEquality_AReaderIsNoKey(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"set":         {"_ = Set[r]", "`Reader` cannot be a Set element: a handle to running state has no equality or hashing"},
		"map key":     {"_ = {r => 1}", "`Reader` cannot be a Map key: a handle to running state has no equality or hashing"},
		"hash":        {"_ = Hashable.hash(Some(r))", "Maybe<Reader> does not implement Hashable (required by `Hashable.hash`)"},
		"tuple equal": {"_ = (1, r) == (1, r)", "`==` cannot compare `(Int, Reader)`: it holds `Reader`, and a handle to running state has no equality"},
	} {
		src := "import std::io.Reader\n\nfn main()\n    r = Reader.from_text(\"x\")\n    " + tc.body + "\nend\n"
		_, errs := checkSourceWithStdlib(src)
		var got []string
		for _, e := range errs {
			got = append(got, e.Message)
		}
		if !slices.Contains(got, tc.want) {
			t.Errorf("%s: want %q, got %v", name, tc.want, got)
		}
	}
}

// A struct with its own `impl Equatable` compares through that impl, so a
// handle among its fields is no error.
func TestHandleEquality_AdmitsATypeWithItsOwnImpl(t *testing.T) {
	src := `struct Job
    id: Int
    ctx: Context
end

impl Equatable for Job
    fn equal(a: Job, b: Job) -> Bool
        a.id == b.id
    end
end

fn main()
    j = Job{id: 1, ctx: Context.root()}
    _ = j == j
end
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "cannot compare") {
			t.Errorf("rejected: %s", e.Message)
		}
	}
}
