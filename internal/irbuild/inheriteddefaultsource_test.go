package irbuild

import "testing"

// An interface default written in another file is lowered in the
// implementing file's gen, under the declaring file's names (lowerNamesFrom).
// Every fact the checker keyed by one of the default's nodes or positions is
// in the declaring file's analysis, so each lookup reads srcFA(): a brace
// literal's target struct, a field accessor's type, an application-field
// read, and a bare name bound by an `import` at the top of the default's
// block. Read from the implementing file's analysis, each missed and the
// implementor's copy of the default declined.
func TestInheritedDefault_ReadsTheDeclaringFilesFacts(t *testing.T) {
	widget := `struct Widget {
    name: String
}

impl Powered for Widget {
    fn label(w: Widget) -> String {
        w.name
    }
}
`
	for _, tc := range []struct {
		name, lib, main, want string
	}{
		{
			name: "a brace literal typed by the default's result",
			lib: `pub struct Point {
    x: Int
    y: Int
}

pub interface Powered {
    fn label(w: self) -> String

    fn origin(w: self) -> Point {
        #{x: 7, y: String.length(Powered.label(w))}
    }
}
`,
			main: `import {
    std/io
    lib.{Powered, Point}
}

` + widget + `
fn main() {
    p: Point = Powered.origin(#Widget{name: "abc"})
    io.print("${p.x},${p.y}")
}
`,
			want: "7,3\n",
		},
		{
			name: "a field accessor",
			lib: `pub struct Point {
    x: Int
    y: Int
}

pub interface Powered {
    fn label(w: self) -> String

    fn xs(w: self) -> String {
        [#Point{x: 3, y: 4}] |> Iter.map(.x) |> Iter.map(fn n -> Powered.label(w) + "${n}") |> String.join(",")
    }
}
`,
			main: `import {
    std/io
    lib.{Powered, Point}
}

` + widget + `
fn main() {
    io.print(Powered.xs(#Widget{name: "w"}))
    io.print(#Point{x: 1, y: 1}.x)
}
`,
			want: "w3\n1\n",
		},
		{
			name: "an application-field read",
			lib: `import main.App

pub interface Powered {
    fn label(w: self) -> String

    fn greet(w: self) -> String {
        App.greeting + " " + Powered.label(w)
    }
}
`,
			main: `import {
    std/io
    lib.Powered
}

pub struct App {
    context: Context
    greeting: String
}

fn boot() -> App {
    #App{context: Context.root(), greeting: "hello"}
}

` + widget + `
fn main() {
    io.print(Powered.greet(#Widget{name: "w"}))
}
`,
			want: "hello w\n",
		},
		{
			name: "a bare call bound by a block import",
			lib: `pub interface Powered {
    fn label(w: self) -> String

    fn shout(w: self) {
        import std/io.print
        print(Powered.label(w) + "!")
    }
}
`,
			main: `import lib.Powered

` + widget + `
fn main() {
    Powered.shout(#Widget{name: "w"})
}
`,
			want: "w!\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifyLambdaProgram(t, tc.main, tc.want, map[string]string{"lib.nomi": tc.lib})
		})
	}
}
