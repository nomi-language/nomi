package irbuild

import "testing"

// An interface written through its module, `sql.Conn`, is the same type as
// `Conn` imported by name, in every position a type is written: a struct
// field, a parameter, a return and a list element.
func TestQualifiedTypeName_StdInterfaceThroughItsModule(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::sql
import std::sql_testing.Fake

struct Holder
    db: sql.Conn
end

fn pass(c: sql.Conn) -> sql.Conn
    c
end

fn main()
    h = Holder{db: Fake.new()}
    held: List<sql.Conn> = [pass(h.db)]
    io.print("${Iter.count(held)}")
    sql.close_quietly(h.db)
end
`, "1\n")
}

// An application struct with such a field builds its `boot`: the shape
// every app with a database writes.
func TestQualifiedTypeName_AppFieldThroughItsModule(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::sql
import std::sql_testing.Fake

struct App
    db: sql.Conn
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{db: Fake.new()})
end

fn main()
    sql.close_quietly(App.db)
    io.print("closed")
end
`, "closed\n")
}

// A program interface from another module, written through that module, in
// the same four positions, with calls dispatched on the values it holds.
func TestQualifiedTypeName_ProgramInterfaceThroughItsModule(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"nomi.toml": "[module]\nname = \"held\"\nentry_points = [\"main\"]\n",
		"main.nomi": `import shapes
import std::io

struct Holder
    s: shapes.Shape
end

fn pass(s: shapes.Shape) -> shapes.Shape
    s
end

fn main()
    h = Holder{s: shapes.Square{side: 3}}
    all: List<shapes.Shape> = [pass(h.s), shapes.Square{side: 2}]
    io.print("${shapes.total(all)}")
end
`,
		"shapes.nomi": `pub interface Shape
    fn area(s: self) -> Int
end

pub struct Square
    side: Int
end

impl Shape for Square
    fn area(s: Square) -> Int
        s.side * s.side
    end
end

pub fn total(all: List<Shape>) -> Int
    Iter.sum_by(all, &Shape.area(&1))
end
`,
	}, "13\n")
}
