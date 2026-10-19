package analysis_test

import (
	"strings"
	"testing"
)

// std's Task, Sender, Receiver, Context and Supervisor are handles to running
// state with no value to compare, and the runtime answers no equality for
// one. `==` over one used to pass the checker and stop the program with "not
// supported yet"; the checker now rejects it as it rejects `==` on a
// function (spec §30).
func TestHandleEquality_IsRejected(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"sender", "ch: Channel<Int> = Channel.unbuffered()\n    _ = ch.sender == ch.sender",
			"`==` cannot compare `Sender<Int>`: a handle to running state has no equality"},
		{"receiver", "ch: Channel<Int> = Channel.unbuffered()\n    _ = ch.receiver != ch.receiver",
			"`!=` cannot compare `Receiver<Int>`: a handle to running state has no equality"},
		{"context", "c = Context.root()\n    _ = c == c",
			"`==` cannot compare `Context`: a handle to running state has no equality"},
		{"task", "_ = concurrent {\n        t = Task.spawn(fn -> 1)\n        t == t\n    }",
			"`==` cannot compare `Task<Int>`: a handle to running state has no equality"},
		{"held in a Maybe", "c = Some(Context.root())\n    _ = c == c",
			"`==` cannot compare `Maybe<Context>`: it holds `Context`, and a handle to running state has no equality"},
		{"map key", "m = %{Context.root() => 1}\n    _ = m",
			"`Context` cannot be a Map key: a handle to running state has no equality or hashing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import {\n    std/channels.Channel\n    std/tasks.Task\n}\n\nfn main() {\n    " + tc.body + "\n}\n"
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

// A struct with its own `impl Equatable` compares through that impl, so a
// handle among its fields is no error.
func TestHandleEquality_AdmitsATypeWithItsOwnImpl(t *testing.T) {
	src := `struct Job {
    id: Int
    ctx: Context
}

impl Equatable for Job {
    fn equal?(a: Job, b: Job) -> Bool {
        a.id == b.id
    }
}

fn main() {
    j = #Job{id: 1, ctx: Context.root()}
    _ = j == j
}
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "cannot compare") {
			t.Errorf("rejected: %s", e.Message)
		}
	}
}
