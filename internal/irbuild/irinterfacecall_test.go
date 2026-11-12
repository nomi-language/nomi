package irbuild

import "testing"

func TestIRInterfaceCall_Tour(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

interface Speech
  fn speak(animal: self) -> String
end

struct Dog
  name: String
end

impl Speech for Dog
  fn speak(d: Dog) -> String
    "${d.name} says woof"
  end
end

fn main()
  rex = Dog{name: "Rex"}

  // Type-qualified — names the implementing type.
  io.print(Dog.speak(rex))

  // Interface-qualified — same dispatch through the contract.
  io.print(Speech.speak(rex))

  // Most printing does not need explicit stringification.
  io.print(42)
end

`, "Rex says woof\nRex says woof\n42\n")
}

func TestIRInterfaceCall_NonFirstReceiverAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
interface Speech
 fn speak(prefix: String, animal: self) -> String
end
struct Cat
    name: String
end
struct Dog
    name: String
end
impl Speech for Cat
 fn speak(prefix: String, animal: Cat) -> String
     prefix + " " + animal.name + " meow"
 end
end
impl Speech for Dog
 fn speak(prefix: String, animal: Dog) -> String
     prefix + " " + animal.name + " woof"
 end
end
fn prefix() -> String
    io.print("prefix")
    "hello"
end
fn cat() -> Cat
    io.print("cat")
    Cat{name: "Milo"}
end
fn main()
 io.print(Speech.speak(prefix(), cat()))
 dog = Dog{name: "Rex"}
 speak = fn prefix: String -> Speech.speak(prefix, dog)
 io.print(speak("hi"))
end
`, "prefix\ncat\nhello Milo meow\nhi Rex woof\n")
}

func TestIRInterfaceCall_MultilineInterpolationCursor(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn message(name: String) -> String
 """
 hello
 ${name}
 done
 """
end
fn main()
    io.print(message("Nomi"))
end
`, "hello\nNomi\ndone\n")
}

func TestIRInterfaceCall_MultilineInterpolationEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn piece(s: String) -> String
    io.print(s)
    s
end
fn message() -> String
 """
 ${piece("first")}
 ${piece("second")}
 """
end
fn main()
    io.print(message())
end
`, "first\nsecond\nfirst\nsecond\n")
}
