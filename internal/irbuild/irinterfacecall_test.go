package irbuild

import "testing"

func TestIRInterfaceCall_Tour(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

interface Speech {
  fn speak(animal: self): String
}

struct Dog {
  name: String
}

impl Speech for Dog {
  fn speak(d: Dog): String {
    "${d.name} says woof"
  }
}

fn main() {
  rex = Dog{name: "Rex"}

  // Type-qualified — names the implementing type.
  io.print(Dog.speak(rex))

  // Interface-qualified — same dispatch through the contract.
  io.print(Speech.speak(rex))

  // Most printing does not need explicit stringification.
  io.print(42)
}

`, "Rex says woof\nRex says woof\n42\n")
}

func TestIRInterfaceCall_NonFirstReceiverAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
interface Speech {
 fn speak(prefix: String, animal: self): String
}
struct Cat { name: String }
struct Dog { name: String }
impl Speech for Cat {
 fn speak(prefix: String, animal: Cat): String { prefix + " " + animal.name + " meow" }
}
impl Speech for Dog {
 fn speak(prefix: String, animal: Dog): String { prefix + " " + animal.name + " woof" }
}
fn prefix(): String { io.print("prefix"); "hello" }
fn cat(): Cat { io.print("cat"); Cat{name: "Milo"} }
fn main() {
 io.print(Speech.speak(prefix(), cat()))
 dog = Dog{name: "Rex"}
 speak = |prefix: String| Speech.speak(prefix, dog)
 io.print(speak("hi"))
}
`, "prefix\ncat\nhello Milo meow\nhi Rex woof\n")
}

func TestIRInterfaceCall_MultilineInterpolationCursor(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn message(name: String): String {
 """
 hello
 ${name}
 done
 """
}
fn main() { io.print(message("Nomi")) }
`, "hello\nNomi\ndone\n")
}

func TestIRInterfaceCall_MultilineInterpolationEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn piece(s: String): String { io.print(s); s }
fn message(): String {
 """
 ${piece("first")}
 ${piece("second")}
 """
}
fn main() { io.print(message()) }
`, "first\nsecond\nfirst\nsecond\n")
}
