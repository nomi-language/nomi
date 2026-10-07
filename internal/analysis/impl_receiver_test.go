package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An impl block's receiver must name a type, and an impl function's
// self-position parameter must be that receiver. Neither was checked: with
// `impl Greeter for Peon { fn greet(p: Person) }` and no `Peon` declared, the
// block registered a Person function nothing could reach by its interface,
// and a same-owner call `greet(p)` from another Person impl resolved to it.
// The front end accepted the program and the IR builder declined it ("a
// direct callee outside this module's fn table: greet").
const implReceiverDecls = `interface Greeter {
  fn greet(value: self): String
}

interface Message {
  fn message(value: self): String
}

struct Person {
  name: String
}

struct Peon {
  n: Int
}

`

const implReceiverMain = `
impl Message for Person {
  fn message(p: Person): String {
    greet(p)
  }
}

fn main() {
  _ = Message.message(Person{name: "Ada"})
}
`

func hasErrorAt(errs []analysis.TypeError, want string, line, col int) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, want) && e.Line == line && e.Col == col {
			return true
		}
	}
	return false
}

func TestImplReceiver_UndeclaredTypeIsRejectedAtTheName(t *testing.T) {
	for _, tc := range []struct {
		name, block string
		line, col   int
	}{
		{"an interface impl", "impl Greeter for Ghost {\n  fn greet(p: Person): String {\n    p.name\n  }\n}\n", 17, 18},
		{"a generic interface impl", "impl<T> Greeter for Ghost<T> {\n  fn greet(p: Ghost<T>): String {\n    \"x\"\n  }\n}\n", 17, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(implReceiverDecls + tc.block + implReceiverMain)
			if !hasErrorAt(errs, `unknown type "Ghost"`, tc.line, tc.col) {
				t.Fatalf("want `unknown type \"Ghost\"` at the receiver name %d:%d, got %v", tc.line, tc.col, errs)
			}
		})
	}
}

func TestImplReceiver_SelfParameterMustBeTheReceiver(t *testing.T) {
	block := "impl Greeter for Peon {\n  fn greet(p: Person): String {\n    p.name\n  }\n}\n"
	_, errs := checkSourceWithStdlib(implReceiverDecls + block + implReceiverMain)
	expectStdlibError(t, errs, "impl function 'greet': parameter 1 has type Person, but it stands for `self`, which this block implements for Peon")
}

// The mirror: the receiver itself, a generic receiver, a prelude receiver and
// a blanket type-parameter receiver stay accepted, and so does a same-owner
// call into one of them.
func TestImplReceiver_TheReceiverIsAccepted(t *testing.T) {
	for _, src := range []string{
		implReceiverDecls + "impl Greeter for Person {\n  fn greet(p: Person): String {\n    p.name\n  }\n}\n" + implReceiverMain,
		"interface Sized {\n  fn size(value: self): Int\n}\n\nstruct Bag<T> {\n  items: List<T>\n}\n\n" +
			"impl<T> Sized for Bag<T> {\n  fn size(b: Bag<T>): Int {\n    Iter.count(b.items)\n  }\n}\n\n" +
			"fn main() {\n  _ = Sized.size(Bag{items: [1]})\n}\n",
		"interface Twice {\n  fn twice(value: self): Int\n}\n\nimpl Twice for Int {\n  fn twice(n: Int): Int {\n    n * 2\n  }\n}\n\n" +
			"fn main() {\n  _ = Twice.twice(2)\n}\n",
	} {
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}
