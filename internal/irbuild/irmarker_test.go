package irbuild

import "testing"

func TestIRMarker_CompleteProgram(t *testing.T) {
	verifyLambdaProgram(t, `type Expired
type Online
fn main(): Online {
 state = Expired
 dbg state
 dbg Online
}
`, "dbg line 5: state = Expired\ndbg line 6: Online = Online\n")
}

func TestIRMarker_CustomDebugAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Ready
impl Debug for Ready {
 fn inspect(value: Ready): String { _ = value; io.print("inspect"); "ready!" }
}
fn echo(value: Ready): Ready { value }
fn make(): Ready { io.print("make"); Ready }
fn main(): Ready {
 ready = make()
 get = || echo(ready)
 dbg get()
}
`, "make\ninspect\ndbg line 11: get() = ready!\n")
}

func TestIRMarker_ImportedDeclaration(t *testing.T) {
	verifyLambdaProgram(t, `import tokens.Ready
fn identity(value: Ready): Ready { value }
fn main(): Ready { identity(Ready) }
`, "", map[string]string{"tokens.nomi": "pub type Ready\n"})
}

// A marker named through its file's qualifier, plain and aliased, is the
// same value as the selectively imported name: it satisfies an interface,
// equals the bare spelling, and renders as the type's name.
func TestIRMarker_ThroughItsFileQualifier(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
import fakes
import fakes as f
import fakes.Quiet
import greeter.Greeter

fn main() {
  g: Greeter = fakes.Quiet
  io.print(Greeter.greet(g, "a"))
  q = f.Quiet
  io.print(Greeter.greet(q, "b"))
  io.print(q == Quiet)
  io.print(Debug.inspect(fakes.Quiet))
}
`, "(a)\n(b)\nTrue\nQuiet\n", map[string]string{
		"greeter.nomi": "pub interface Greeter {\n  fn greet(g: self, name: String): String\n}\n",
		"fakes.nomi": "import greeter.Greeter\n\npub type Quiet\n\n" +
			"impl Greeter for Quiet {\n  fn greet(_g: Quiet, name: String): String { \"(${name})\" }\n}\n",
	})
}

func TestIRMarker_StructFields(t *testing.T) {
	verifyLambdaProgram(t, `type Ready
struct Box { ready: Ready }
fn read(box: Box): Ready { box.ready }
fn main(): Ready {
 dbg read(Box{ready: Ready})
}
`, "dbg line 5: read(Box{ready: Ready}) = Ready\n")
}
