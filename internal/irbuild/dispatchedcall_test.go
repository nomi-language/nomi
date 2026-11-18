package irbuild

import "testing"

// A call the checker leaves to dispatch where it runs, on a receiver held
// as an interface value, is planned from the interface function its record
// names (dispatchedCallPlan): on a parameter, a binding, a list element and
// a field; through a conjunction; leaving defaults out, naming an
// argument, piped and in a capture; on an interface another file declares;
// and inside a generic instance whose type argument is an interface type.
func TestDispatchedCall_InterfaceValueReceivers(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
import shapes.{Sized}

interface Named
    fn name(value: self) -> String
end

interface Greeter
    open fn greet(value: self, greeting: String = "hi", mark: String = "!") -> String
        "${greeting} ${Greeter.name_of(value)}${mark}"
    end
    fn name_of(value: self) -> String
end

struct Dog
    name: String
end

struct Cat
    name: String
end

impl Named for Dog
    fn name(d: Dog) -> String
        d.name
    end
end

impl Named for Cat
    fn name(c: Cat) -> String
        "the cat ${c.name}"
    end
end

impl Sized for Dog
    fn size(_d: Dog) -> Int
        3
    end
end

impl Greeter for Dog
    fn name_of(d: Dog) -> String
        d.name
    end
end

struct Holder
    item: Named
end

fn of_param(n: Named) -> String
    Named.name(n)
end

fn both(x: Named and Sized) -> String
    "${Named.name(x)} ${Sized.size(x)}"
end

fn label<T>(x: T) -> String where T: Named
    T.name(x)
end

fn main()
    d = Dog{name: "Rex"}
    n: Named = Cat{name: "Tom"}
    io.print(of_param(d))
    io.print(Named.name(n))
    items: List<Named> = [d, Cat{name: "Kit"}]
    Iter.each(items, fn item -> io.print(Named.name(item)))
    h = Holder{item: Cat{name: "Hal"}}
    io.print(Named.name(h.item))
    io.print(both(d))
    g: Greeter = d
    io.print(Greeter.greet(g))
    io.print(Greeter.greet(g, mark: "?"))
    io.print(g |> Greeter.greet("yo"))
    io.print(Iter.map(items, &Named.name(&1)) |> String.join(", "))
    s: Sized = d
    io.print("${Sized.size(s)}")
    io.print(label(n))
end
`, "Rex\nthe cat Tom\nRex\nthe cat Kit\nthe cat Hal\nRex 3\nhi Rex!\nhi Rex?\nyo Rex!\nRex, the cat Kit\n3\nthe cat Tom\n",
		map[string]string{"shapes.nomi": `pub interface Sized
    fn size(value: self) -> Int
end
`})
}

// A std interface's function called on a value held as that interface, in
// a program's function and in an instance of a generic one whose type
// argument is the interface.
func TestDispatchedCall_StdInterfaceValue(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
import std::json.{Json, ToJson}

struct Point
    x: Int
end

impl ToJson for Point
    fn to_json(p: Point) -> Json
        ToJson.to_json(p.x)
    end
end

fn encode(v: ToJson) -> String
    Display.to_string(ToJson.to_json(v))
end

fn encode_all<T>(xs: List<T>) -> String where T: ToJson
    Iter.map(xs, fn x -> Display.to_string(T.to_json(x))) |> String.join(" ")
end

fn main()
    io.print(encode(Point{x: 4}))
    io.print(encode(7))
    v: ToJson = "s"
    io.print(Display.to_string(ToJson.to_json(v)))
    vs: List<ToJson> = [Point{x: 1}, 2]
    io.print(encode_all(vs))
end
`, "4\n7\n\"s\"\n1 2\n")
}

// A generic interface's function on values held as two of its
// instantiations, and a std interface's named through its module.
func TestDispatchedCall_InterfaceInstancesAndModuleQualified(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
import std::json

interface Gets<T>
    fn got(holder: self) -> T
end

struct Box
    n: Int
end

impl Gets<String> for Box
    fn got(b: Box) -> String
        "box ${b.n}"
    end
end

impl Gets<Int> for Box
    fn got(b: Box) -> Int
        b.n * 10
    end
end

fn encode(v: json.ToJson) -> String
    Display.to_string(json.ToJson.to_json(v))
end

fn main()
    h: Gets<String> = Box{n: 2}
    io.print(Gets.got(h))
    i: Gets<Int> = Box{n: 3}
    io.print("${Gets.got(i)}")
    io.print(encode(5))
end
`, "box 2\n30\n5\n")
}

// A std interface's function named through its module, in a file that
// declares an interface of the same name, on a value held as the std one
// (stdModuleIfaceCall).
func TestDispatchedCall_StdInterfaceThroughItsModuleWhereTheNameIsShadowed(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
import std::json
import other

interface ToJson
    fn to_json(value: self) -> String
end

struct Point
    x: Int
end

impl ToJson for Point
    fn to_json(p: Point) -> String
        "p${p.x}"
    end
end

fn encode(v: json.ToJson) -> String
    Display.to_string(json.ToJson.to_json(v))
end

fn mine(v: ToJson) -> String
    ToJson.to_json(v)
end

fn main()
    io.print(encode(other.make()))
    io.print(mine(Point{x: 1}))
end
`, "5\np1\n", map[string]string{"other.nomi": `import std::json.{ToJson}

pub fn make() -> ToJson
    5
end
`})
}
