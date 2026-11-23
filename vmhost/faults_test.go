package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// runVM loads src and runs it, answering its output and its error.
func runVM(t *testing.T, src string) (string, error) {
	t.Helper()
	p, err := vmhost.Load(writeProgram(t, src))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	return out.String(), err
}

// A cyclic `once` the compile-time check cannot see, closed through a
// function value, is the program's fault, with rt's text at the line of the
// read that re-entered it, not a limit of the machine.
func TestRun_ACyclicOnceIsTheProgramsFault(t *testing.T) {
	out, err := runVM(t, `import std::io

fn read_seed() -> Int
  seed
end

fn apply(f: () -> Int) -> Int
  f()
end

once seed: Int = apply(read_seed) + 1

fn main()
  io.print("start")
  io.print("seed ${seed}")
end
`)
	if b, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("a cyclic once reported as the VM's limit: %q", b.Reasons)
	}
	if out != "start\n" || err == nil || err.Error() != "line 4: cyclic 'once' binding: 'seed' depends on itself" {
		t.Fatalf("output %q, error %v", out, err)
	}
}

// The same cycle closed through a direct call is a compile error at the
// `once`, naming the chain, and the program never starts.
func TestLoad_ACyclicOnceThroughACallIsACompileError(t *testing.T) {
	_, err := vmhost.Load(writeProgram(t, `import std::io

fn read_seed() -> Int
  seed
end

once seed: Int = read_seed() + 1

fn main()
  io.print("start")
  io.print("seed ${seed}")
end
`))
	want := "cyclic 'once' binding: `seed` depends on itself: it calls `read_seed` (main.nomi:7), which reads `once seed` (main.nomi:4)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("load error %v, want one holding %q", err, want)
	}
}

// A field read on an enum value that some variant lacks is a compile error
// at the field, and the program never starts.
func TestLoad_AnEnumFieldSomeVariantLacksIsACompileError(t *testing.T) {
	_, err := vmhost.Load(writeProgram(t, `enum Shape
  Rect {height: Int}
  Round {radius: Float}
end

fn main()
  s = Shape.Rect{height: 2}
  _ = s.radius
end
`))
	want := "main.nomi:8:9: field 'radius' is not in every variant of Shape: Rect has none"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("load error %v, want one holding %q", err, want)
	}
}

// A pattern `if` with no `else` in a Unit position is retained. (One whose
// pattern always matches is a front-end error; see
// internal/analysis/if_pattern_irrefutable_test.go.)
func TestRun_ElselessPatternIfRuns(t *testing.T) {
	out, err := runVM(t, `import std::io

fn side_effect(m: Option<Int>) -> Bool
  result = if Some(n) = m then
    io.print("hit ${n}")
  end

  result == Unit
end

fn main()
  io.print("${side_effect(Some(1))} ${side_effect(None)}")
end
`)
	want := "hit 1\nTrue True\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// A boot answering a struct whose interface-typed field holds a concrete
// value is retained: the field enters by erasure.
func TestRun_ABootRecordWithAnErasedFieldRuns(t *testing.T) {
	out, err := runVM(t, `import
    std::io
end
interface Logger
    fn log(logger:self,msg:String) ->Unit
end
struct Console end
impl Logger for Console
    fn log(logger:Console,msg:String) ->Unit
        _ = logger
        io.print("console ${msg}")
    end
end
struct App
    life:Context
logger:Logger
end
fn boot() ->Result<App, String>
    Ok(App{life:Context.root(),logger:Console{}})
end
fn main()
    Logger.log(App.logger,"hi")
end
`)
	if err != nil || out != "console hi\n" {
		t.Fatalf("output %q, error %v", out, err)
	}
}

// A fault inside a hand-written Hashable body a Set construction runs is the
// program's fault, with the body's line.
func TestRun_AFaultInAKeyHashIsTheProgramsFault(t *testing.T) {
	out, err := runVM(t, `import std::io

struct Bad
  n: Int
end

impl Hashable for Bad
  fn hash(b: Bad) -> Int
    b.n / NonZeroInt.try_from(b.n - b.n)!
  end
end

fn main()
  io.print("start")
  io.print(Set.size(Set[Bad{n: 1}, Bad{n: 4}]))
end
`)
	if b, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("a key fault reported as the VM's limit: %q", b.Reasons)
	}
	if out != "start\n" || err == nil || err.Error() != "line 9: `NonZeroInt.try_from(b.n - b.n)` found nothing" {
		t.Fatalf("output %q, error %v", out, err)
	}
}
