package irbuild

import "testing"

// A parameter written as its type alone, `fn stats(Request)`, takes a value
// of that type and binds nothing: in a file function, an impl's function, an
// interface's function, a nested function and a lambda, over a struct, a
// multi-variant enum, a distinct type, an alias and a type parameter.
func TestTypeOnlyParams_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const main = `import std::io

struct Request {
    path: String
}

enum Method {
    Get
    Post
}

type Cents Int

typealias Id Int

interface Handler {
    fn handle(h: self, Request) -> String
}

struct Static {}

impl Handler for Static {
    fn handle(Static, r: Request) -> String {
        "static " + r.path
    }
}

fn stats(Request) -> String {
    "ok"
}

fn verb(Method, n: Int) -> Int {
    n + 1
}

fn cents(Cents) -> Int {
    2
}

fn id(Id) -> Int {
    3
}

fn second<T>(T, b: T) -> T {
    b
}

fn apply(f: (Request) -> String) -> String {
    f(Request{path: "/"})
}

fn main() {
    fn inner(Int) -> Int {
        4
    }
    io.print(stats(Request{path: "/"}))
    io.print(verb(.Post, 1))
    io.print(cents(Cents(5)))
    io.print(id(7))
    io.print(second(1, 5))
    io.print(inner(0))
    io.print(apply(fn Request -> "lambda"))
    g = fn Request -> "free"
    io.print(g(Request{path: "/x"}))
    io.print(Handler.handle(Static{}, Request{path: "/h"}))
}
`
	verifyLambdaProgram(t, main, "ok\n2\n2\n3\n5\n4\nlambda\nfree\nstatic /h\n")
}
