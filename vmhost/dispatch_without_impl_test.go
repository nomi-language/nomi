package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A dispatched call to an interface no type implements runs over no value,
// since no value of an implementing type exists, so it does not keep the
// program from running: over a program's own interface, over a std
// interface, and inside std's own code (std/jobs' Worker.start dispatching
// on Handler).
func TestRun_ADispatchWithNoImplementationAnywhereRuns(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{
			name: "program interface",
			src: `import std::io

interface Named {
    fn name(n: self) -> String
}

fn all() -> List<Named> {
    []
}

fn main() {
    Iter.each(all(), fn n -> io.print(Named.name(n)))
    io.print("done")
}
`,
			want: "done\n",
		},
		{
			name: "std interface",
			src: `import {
    std::io
    std::jobs.Handler
}

fn handlers() -> List<Handler> {
    []
}

fn main() {
    Iter.each(handlers(), fn h -> io.print(Handler.kind(h)))
    io.print("done")
}
`,
			want: "done\n",
		},
		{
			name: "dispatch in std",
			src: `import {
    std::io
    std::jobs.Worker
    std::jobs_testing.Inline
}

pub struct App {
    worker: Worker
}

pub fn boot() -> Result<App, String> {
    Ok(App{worker: Worker.new(Inline.new(), max_running: 1)})
}

fn main() {
    Worker.start(App.worker, [])
    io.print("started")
}
`,
			want: "started\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runOutput(t, c.src); got != c.want {
				t.Fatalf("output %q, want %q", got, c.want)
			}
		})
	}
}

// An interface a type implements with a body the builder declined still
// blocks the program before it runs: a value of that type may reach the
// dispatch. The body declines on a call to `planted_decline`
// (plantDecline).
func TestRun_ADispatchWhoseOnlyImplementationDeclinedIsBlocked(t *testing.T) {
	plantDecline(t)
	out, err := runVM(t, `import std::io

interface Named {
    fn name(n: self) -> String
}

struct Cat {
    lives: Int
}

fn planted_decline(a: Int) -> Int {
    a
}

impl Named for Cat {
    fn name(c: Cat) -> String {
        "cat ${planted_decline(c.lives)}"
    }
}

fn all() -> List<Named> {
    [Cat{lives: 9}]
}

fn main() {
    io.print("start")
    Iter.each(all(), fn n -> io.print(Named.name(n)))
}
`)
	b, blocked := vmhost.IsBlocked(err)
	if !blocked {
		t.Fatalf("not blocked: output %q, error %v", out, err)
	}
	if out != "" {
		t.Fatalf("the blocked program printed %q before it stopped", out)
	}
	if got := strings.Join(b.Reasons, "\n"); !strings.Contains(got, "[Named.name] dispatched, and no implementation was retained") {
		t.Fatalf("reasons %q", got)
	}
}
