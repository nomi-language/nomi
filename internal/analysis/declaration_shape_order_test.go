package analysis_test

import "testing"

// A declaration that reads the shape of one declared in another file reads
// the same shape whichever file is built first: a struct field, an enum
// payload and an alias naming a generic struct or enum, an enum embedding a
// struct and a distinct type, a distinct type over a generic struct, an impl
// taking an interface's defaults, a derive over a struct whose fields are
// the other file's types, and an impl over the other file's generic struct.
// The last one failed: built before Wrap's file, `impl Greets for Wrap<T>`
// read a shell with no type parameters and its `fn name(value: Wrap<T>)`
// was "unknown type "T"", and an impl over a bounded generic struct did not
// take the bound.
func TestDeclarationOrder_DeclarationsReadOtherFilesShapes(t *testing.T) {
	checkEveryOrder(t, map[string]string{
		"%s": `pub interface Greets {
    fn name(value: self) -> String

    fn greet(value: self) -> String {
        "hi " + name(value)
    }

    open fn loud(value: self, mark: String = "!") -> String {
        name(value) + mark
    }
}

pub struct Wrap<T> {
    item: T
    count: Int = 1
}

pub struct Shown<T> where T: Display {
    item: T
}

pub enum Tree<T> {
    Leaf(T)
    Empty
}

pub type Meters Int

pub type Tag Wrap<String>

pub struct Circle {
    radius: Int
}

derive Equatable for Circle

derive Equatable for Wrap

derive Equatable for Tree

derive Equatable for Meters
`,
		"uses": `import %s.{Greets, Wrap, Shown, Tree, Meters, Tag, Circle}

pub typealias Pair Wrap<Int>

pub struct Holder {
    w: Wrap<Int>
    t: Tree<String>
    m: Meters
    c: Circle
}

derive Equatable for Holder

pub enum Shape {
    embeds Circle
    embeds Meters
    Dot
    Boxed(Wrap<Bool>)
}

pub struct Person {
    first: String
}

impl Greets for Person {
    fn name(value: Person) -> String {
        value.first
    }
}

impl Greets for Wrap<T> {
    fn name(value: Wrap<T>) -> String {
        "wrap ${value.count}"
    }
}

impl Greets for Shown<T> {
    fn name(value: Shown<T>) -> String {
        "shown ${value.item}"
    }
}

pub fn tag_item(t: Tag) -> String {
    Tag(w) = t
    w.item
}
`,
		"main": `import {
    uses.{Pair, Holder, Shape, Person, tag_item}
    %s.{Greets, Wrap, Shown, Tree, Meters, Tag, Circle}
}

fn size(s: Shape) -> Int {
    case s {
        Shape.Circle(c) -> c.radius
        Shape.Meters(m) -> m
        Shape.Dot -> 0
        Shape.Boxed(w) -> w.count
    }
}

fn main() {
    h = #Holder{w: #Wrap{item: 1}, t: Tree.Leaf("x"), m: Meters(3), c: #Circle{radius: 2}}
    dbg h.w.item + h.w.count
    dbg case h.t {
        .Leaf(s) -> s
        .Empty -> ""
    }
    dbg h == h
    p: Pair = #Wrap{item: 4}
    dbg p.item + 1
    dbg size(#Circle{radius: 2}) + size(Meters(5)) + size(Shape.Boxed(#Wrap{item: True}))
    dbg Greets.greet(#Person{first: "a"})
    dbg Person.loud(#Person{first: "a"})
    dbg Greets.greet(#Wrap{item: 1.5})
    dbg Greets.greet(#Shown{item: 7})
    dbg tag_item(Tag(#Wrap{item: "t"}))
}
`,
	})
}

// The same impls over a generic struct declared below them in one file:
// the impl was typed before the struct was built.
func TestDeclarationOrder_ImplAboveTheGenericStructItIsFor(t *testing.T) {
	impls := `interface Greets {
    fn name(value: self) -> String
}

impl Greets for Wrap<T> {
    fn name(value: Wrap<T>) -> String {
        "wrap ${value.count}"
    }
}

impl Greets for Shown<T> {
    fn name(value: Shown<T>) -> String {
        "shown ${value.item}"
    }
}
`
	structs := `struct Wrap<T> {
    item: T
    count: Int = 1
}

struct Shown<T> where T: Display {
    item: T
}
`
	use := `fn main() {
    dbg Greets.name(#Wrap{item: 1.5})
    dbg Greets.name(#Shown{item: 7})
}
`
	for name, src := range map[string]string{
		"structs first": structs + "\n" + impls + "\n" + use,
		"impls first":   impls + "\n" + structs + "\n" + use,
	} {
		for file, errs := range checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""}) {
			if len(errs) > 0 {
				t.Errorf("%s: %s: %v", name, file, messages(errs))
			}
		}
	}
}
