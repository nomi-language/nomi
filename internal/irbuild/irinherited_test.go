package irbuild

import (
	"testing"
)

func TestIRInherited_Defaults(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

interface Formatted {
  fn label(value: self): String

  // Final default — overriding it is a compile error.
  fn shout(value: self): String {
    "${label(value)}!"
  }

  // Open default — implementors MAY override.
  open fn brief(value: self): String {
    label(value)
  }
}

struct User {
  name: String
}

impl Formatted for User {
  fn label(u: User): String {
    u.name
  }

  // The open brief default permits this override.
  fn brief(u: User): String {
    "user:${u.name}"
  }
}

fn main() {
  alice = User{name: "Alice"}
  io.print(Formatted.label(alice))
  io.print(Formatted.shout(alice))
  io.print(Formatted.brief(alice))
}

`, "Alice\nAlice!\nuser:Alice\n")
}

func TestIRInherited_DefaultReadsARequiredFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

interface HasName {
  fn name(value: self): String

  // Default — reads the name through the required function.
  fn greet(value: self): String {
    "Hello, ${name(value)}"
  }
}

struct User {
  name: String
  age: Int
}

impl HasName for User {
  fn name(user: User): String { user.name }
}

struct Pet {
  name: String
  species: String
}

impl HasName for Pet {
  fn name(pet: Pet): String { pet.name }
}

fn main() {
  alice = User{name: "Alice", age: 30}
  rex = Pet{name: "Rex", species: "Dog"}
  io.print(HasName.greet(alice))
  io.print(HasName.greet(rex))
}

`, "Hello, Alice\nHello, Rex\n")
}

func TestIRInherited_SpecializedDefaultsAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
interface Label {
 fn label(value: self): String
 fn format(prefix: String, value: self): String { prefix + label(value) }
 open fn suffix(value: self): String { label(value) + "!" }
}
struct Box<T> { value: T }
impl Label for Box<T> {
 fn label(value: Box<T>): String { _ = value; "box" }
}
struct Named { name: String }
impl Label for Named {
 fn label(value: Named): String { value.name }
 fn suffix(value: Named): String { "override " + value.name }
}
fn prefix(): String { io.print("prefix"); "hello " }
fn named(): Named { io.print("named"); Named{name: "Nomi"} }
fn main() {
 io.print(Label.format(prefix(), named()))
 io.print(Label.suffix(Box{value: 1}))
 io.print(Label.suffix(Box{value: "text"}))
 io.print(Label.suffix(Named{name: "Nomi"}))
}
`, "prefix\nnamed\nhello Nomi\nbox!\nbox!\noverride Nomi\n")
}

func TestIRInherited_SameOwnerAndLocalClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Number { value: Int }
impl Number {
 fn read(n: Number): Int { n.value }
 fn double(n: Number): Int { read(n) + read(n) }
 fn local(n: Number): Int { plus = |v: Number| v.value + 10; plus(n) }
}
fn main() {
 io.print(Number.double(Number{value: 3}))
 io.print(Number.local(Number{value: 3}))
}
`, "6\n13\n")
}

// An interface default ending in `_ = dbg value` retains: a trailing discard
// binding is a statement and the body answers Unit, and the Point's Debug is
// its synthesized impl.
func TestIRInherited_DbgDefaultOverANominalRuns(t *testing.T) {
	verifyLambdaProgram(t, `
interface Trace { fn trace(value: self) { _ = dbg value } }
struct Point { x: Int }
impl Trace for Point {}
fn main() { Trace.trace(Point{x: 1}) }
`, "dbg line 2: value = Point{x: 1}\n")
}
