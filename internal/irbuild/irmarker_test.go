package irbuild

import "testing"

func TestIRMarker_CompleteProgram(t *testing.T) {
	verifyLambdaProgram(t, `type Expired
type Online
fn main()
 state = Expired
 dbg state
 dbg Online
end
`, "dbg line 5: state = Expired\ndbg line 6: Online = Online\n")
}

func TestIRMarker_CustomDebugAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
type Ready
impl Debug for Ready
 fn inspect(value: Ready) -> String
     _ = value
     io.print("inspect")
     "ready!"
 end
end
fn echo(value: Ready) -> Ready
    value
end
fn make() -> Ready
    io.print("make")
    Ready
end
fn main()
 ready = make()
 get = fn -> echo(ready)
 dbg get()
end
`, "make\ninspect\ndbg line 20: get() = ready!\n")
}

func TestIRMarker_ImportedDeclaration(t *testing.T) {
	verifyLambdaProgram(t, `import tokens.Ready
fn identity(value: Ready) -> Ready
    value
end
fn main()
    _ = identity(Ready)
end
`, "", map[string]string{"tokens.nomi": "pub type Ready\n"})
}

// A marker named through its file's qualifier, plain and aliased, is the
// same value as the selectively imported name: it satisfies an interface,
// equals the bare spelling, and renders as the type's name.
func TestIRMarker_ThroughItsFileQualifier(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
import fakes
import fakes as f
import fakes.Quiet
import greeter.Greeter

fn main()
  g: Greeter = fakes.Quiet
  io.print(Greeter.greet(g, "a"))
  q = f.Quiet
  io.print(Greeter.greet(q, "b"))
  io.print(q == Quiet)
  io.print(Debug.inspect(fakes.Quiet))
end
`, "(a)\n(b)\nTrue\nQuiet\n", map[string]string{
		"greeter.nomi": "pub interface Greeter\n  fn greet(g: self, name: String) -> String\nend\n",
		"fakes.nomi": "import greeter.Greeter\n\npub type Quiet\n\n" +
			"impl Greeter for Quiet\n  fn greet(_g: Quiet, name: String) -> String\n      \"(${name})\"\n  end\nend\n",
	})
}

func TestIRMarker_StructFields(t *testing.T) {
	verifyLambdaProgram(t, `type Ready
struct Box
    ready: Ready
end
fn read(box: Box) -> Ready
    box.ready
end
fn main()
 dbg read(Box{ready: Ready})
end
`, "dbg line 9: read(Box{ready: Ready}) = Ready\n")
}
