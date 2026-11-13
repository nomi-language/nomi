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

interface Named
    fn name(n: self) -> String
end

fn all() -> List<Named>
    []
end

fn main()
    Iter.each(all(), fn n -> io.print(Named.name(n)))
    io.print("done")
end
`,
			want: "done\n",
		},
		{
			name: "std interface",
			src: `import
    std::io
    std::jobs.Handler
end

fn handlers() -> List<Handler>
    []
end

fn main()
    Iter.each(handlers(), fn h -> io.print(Handler.kind(h)))
    io.print("done")
end
`,
			want: "done\n",
		},
		{
			name: "dispatch in std",
			src: `import
    std::io
    std::jobs.Worker
    std::jobs::testing.Inline
end

pub struct App
    worker: Worker
end

pub fn boot() -> Result<App, String>
    Ok(App{worker: Worker.new(Inline.new(), max_running: 1)})
end

fn main()
    Worker.start(App.worker, [])
    io.print("started")
end
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

interface Named
    fn name(n: self) -> String
end

struct Cat
    lives: Int
end

fn planted_decline(a: Int) -> Int
    a
end

impl Named for Cat
    fn name(c: Cat) -> String
        "cat ${planted_decline(c.lives)}"
    end
end

fn all() -> List<Named>
    [Cat{lives: 9}]
end

fn main()
    io.print("start")
    Iter.each(all(), fn n -> io.print(Named.name(n)))
end
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
