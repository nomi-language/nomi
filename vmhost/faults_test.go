package vmhost_test

import (
	"bytes"
	"context"
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

// A cyclic `once` is the program's fault, with rt's text, not a limit of the
// machine.
func TestRun_ACyclicOnceIsTheProgramsFault(t *testing.T) {
	out, err := runVM(t, `import std/io

fn read_seed(): Int {
  seed
}

once seed: Int = read_seed() + 1

fn main() {
  io.print("start")
  io.print("seed ${seed}")
}
`)
	if b, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("a cyclic once reported as the VM's limit: %q", b.Reasons)
	}
	if out != "start\n" || err == nil || err.Error() != "cyclic 'once' binding: 'seed' depends on itself" {
		t.Fatalf("output %q, error %v", out, err)
	}
}

// A field read on an enum value whose variant lacks the field is the
// program's fault, with rt's text and the read's line.
func TestRun_AnEnumFieldReadTrapIsTheProgramsFault(t *testing.T) {
	_, err := runVM(t, `enum Shape {
  Rect {height: Int}
  Round {radius: Float}
}

fn main() {
  s = Shape.Rect{height: 2}
  _ = s.radius
}
`)
	if b, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("an enum field trap reported as the VM's limit: %q", b.Reasons)
	}
	if err == nil || err.Error() != "line 8: variant 'Rect' has no field 'radius'" {
		t.Fatalf("error %v", err)
	}
}

// A pattern `if` with no `else` in a Unit position is retained. (One whose
// pattern always matches is a front-end error; see
// internal/analysis/if_pattern_irrefutable_test.go.)
func TestRun_ElselessPatternIfRuns(t *testing.T) {
	out, err := runVM(t, `import std/io

fn side_effect(m: Maybe<Int>): Bool {
  result = if Some(n) = m {
    io.print("hit ${n}")
  }

  result == Unit
}

fn main() {
  io.print("${side_effect(Some(1))} ${side_effect(None)}")
}
`)
	want := "hit 1\nTrue True\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// A boot answering a struct whose interface-typed field holds a concrete
// value is retained: the field enters by erasure.
func TestRun_ABootRecordWithAnErasedFieldRuns(t *testing.T) {
	out, err := runVM(t, `import {std/io}
interface Logger {fn log(logger:self,msg:String):Unit}
struct Console {}
impl Logger for Console {fn log(logger:Console,msg:String):Unit{ _ = logger;io.print("console ${msg}")}}
struct App {life:Context
logger:Logger}
fn boot():App{App{life:Context.root(),logger:Console{}}}
fn main(){Logger.log(App.logger,"hi")}
`)
	if err != nil || out != "console hi\n" {
		t.Fatalf("output %q, error %v", out, err)
	}
}

// A fault inside a hand-written Hashable body a Set construction runs is the
// program's fault, with the body's line.
func TestRun_AFaultInAKeyHashIsTheProgramsFault(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Bad {
  n: Int
}

impl Hashable for Bad {
  fn hash(b: Bad): Int {
    b.n * 4611686018427387904
  }
}

fn main() {
  io.print("start")
  io.print(Set.size(#{Bad{n: 1}, Bad{n: 4}}))
}
`)
	if b, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("a key fault reported as the VM's limit: %q", b.Reasons)
	}
	if out != "start\n" || err == nil || err.Error() != "line 9: integer overflow: 4 * 4611686018427387904" {
		t.Fatalf("output %q, error %v", out, err)
	}
}
