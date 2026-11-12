package irbuild

import (
	"testing"
)

func TestIRInherited_Defaults(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

interface Formatted
  fn label(value: self) -> String

  // Final default — overriding it is a compile error.
  fn shout(value: self) -> String
    "${label(value)}!"
  end

  // Open default — implementors MAY override.
  open fn brief(value: self) -> String
    label(value)
  end
end

struct User
  name: String
end

impl Formatted for User
  fn label(u: User) -> String
    u.name
  end

  // The open brief default permits this override.
  fn brief(u: User) -> String
    "user:${u.name}"
  end
end

fn main()
  alice = User{name: "Alice"}
  io.print(Formatted.label(alice))
  io.print(Formatted.shout(alice))
  io.print(Formatted.brief(alice))
end

`, "Alice\nAlice!\nuser:Alice\n")
}

func TestIRInherited_DefaultReadsARequiredFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

interface HasName
  fn name(value: self) -> String

  // Default — reads the name through the required function.
  fn greet(value: self) -> String
    "Hello, ${name(value)}"
  end
end

struct User
  name: String
  age: Int
end

impl HasName for User
  fn name(user: User) -> String
      user.name
  end
end

struct Pet
  name: String
  species: String
end

impl HasName for Pet
  fn name(pet: Pet) -> String
      pet.name
  end
end

fn main()
  alice = User{name: "Alice", age: 30}
  rex = Pet{name: "Rex", species: "Dog"}
  io.print(HasName.greet(alice))
  io.print(HasName.greet(rex))
end

`, "Hello, Alice\nHello, Rex\n")
}

func TestIRInherited_SpecializedDefaultsAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
interface Label
 fn label(value: self) -> String
 fn format(prefix: String, value: self) -> String
     prefix + label(value)
 end
 open fn suffix(value: self) -> String
     label(value) + "!"
 end
end
struct Box<T>
    value: T
end
impl Label for Box<T>
 fn label(value: Box<T>) -> String
     _ = value
     "box"
 end
end
struct Named
    name: String
end
impl Label for Named
 fn label(value: Named) -> String
     value.name
 end
 fn suffix(value: Named) -> String
     "override " + value.name
 end
end
fn prefix() -> String
    io.print("prefix")
    "hello "
end
fn named() -> Named
    io.print("named")
    Named{name: "Nomi"}
end
fn main()
 io.print(Label.format(prefix(), named()))
 io.print(Label.suffix(Box{value: 1}))
 io.print(Label.suffix(Box{value: "text"}))
 io.print(Label.suffix(Named{name: "Nomi"}))
end
`, "prefix\nnamed\nhello Nomi\nbox!\nbox!\noverride Nomi\n")
}

func TestIRInherited_SameOwnerAndLocalClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Number
    value: Int
end
impl Number
 fn read(n: Number) -> Int
     n.value
 end
 fn double(n: Number) -> Int
     read(n) + read(n)
 end
 fn local(n: Number) -> Int
     plus = fn v: Number -> v.value + 10
     plus(n)
 end
end
fn main()
 io.print(Number.double(Number{value: 3}))
 io.print(Number.local(Number{value: 3}))
end
`, "6\n13\n")
}

// An interface default ending in `_ = dbg value` retains: a trailing discard
// binding is a statement and the body answers Unit, and the Point's Debug is
// its synthesized impl.
func TestIRInherited_DbgDefaultOverANominalRuns(t *testing.T) {
	verifyLambdaProgram(t, `
interface Trace
    fn trace(value: self)
        _ = dbg value
    end
end
struct Point
    x: Int
end
impl Trace for Point end
fn main()
    Trace.trace(Point{x: 1})
end
`, "dbg line 4: value = Point{x: 1}\n")
}
