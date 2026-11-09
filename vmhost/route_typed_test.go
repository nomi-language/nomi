package vmhost

import (
	"context"
	"strings"
	"testing"
)

// A typed route hands its handler the parameters parsed into their types,
// a route with none mixes with it in one router, a segment that does not
// parse is a 404, a link fills and escapes the parameters, and a literal
// with no expected type has a String field per wildcard.
func TestRouteLiteral_TypedParameters(t *testing.T) {
	const src = `import {
    std::http.{Body, Method, Request, Response, Route, Router}
    std::http_testing
    std::io
    std::uuid.Uuid
}

fn show(_: Request, post: .{id: Uuid}) -> Response {
    Response{body: Body.Text("post ${post.id}")}
}

fn main() {
    post: Route<.{id: Uuid}> = "GET /posts/{id}"
    page: Route<.{name: String, n: Int}> = "GET /pages/{name}/{n}"
    any = Route` + "`GET /any/{x}`" + `

    router = Router.new([
        Route.to("GET /", fn _ -> Response{body: Body.Text("home")}),
        Route.to_with(post, show),
        Route.to_with(page, fn _, p -> Response{body: Body.Text("${p.name} ${p.n + 1}")}),
        Route.to_with(any, fn _, p -> Response{body: Body.Text(p.x)}),
    ])!

    get = fn target: String -> Router.serve(router, http_testing.request(Method.Get, target))
    io.print(Response.text(get("/")))
    io.print(Response.text(get("/posts/0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a")))
    io.print(get("/posts/latest").status)
    io.print(Response.text(get("/pages/a/41")))
    io.print(get("/pages/a/x").status)
    io.print(Response.text(get("/any/7")))
    io.print(Route.path_with(page, .{name: "ann b/c?", n: 2}))
    io.print(Route.path_with(Route` + "`GET /files/{rest...}`" + `, .{rest: "a b/c"}))
    io.print(Route.path(Route` + "`GET example.com/about/{$}`" + `))
}
`
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "Ok(home)\nOk(post 0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a)\n404 Not Found\nOk(a 42)\n404 Not Found\nOk(7)\n" +
		"/pages/ann%20b%2Fc%3F/2\n/files/a%20b/c\n/about/\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A handler answering a Result whose error implements ToResponse, a user's
// type or std's Response, runs through Route.to and Route.to_with, named or
// a lambda: the Ok value is the response, and an Err renders itself.
func TestRouteHandlers_AnswerAResultOfToResponse(t *testing.T) {
	src := strings.ReplaceAll(`import {
    std::http.{Body, Method, Request, Response, Route, Router, Status, ToResponse}
    std::http_testing
    std::io
    std::uuid.Uuid
}

struct Oops {
    message: String
}

impl ToResponse for Oops {
    fn to_response(oops: Oops) -> Response {
        Response.error(Status.bad_request, oops.message)
    }
}

fn plain(_: Request) -> Response {
    Response{body: Body.Text("plain")}
}

fn fallible(request: Request) -> Result<Response, Oops> {
    if request.url.path == "/b" {
        Ok(Response{body: Body.Text("ok")})
    } else {
        Err(Oops{message: "oops"})
    }
}

fn std_error(request: Request) -> Result<Response, Response> {
    if request.url.path == "/d" {
        Ok(Response{body: Body.Text("std ok")})
    } else {
        Err(Response.error(Status.not_found, "std err"))
    }
}

fn show(_: Request, post: .{id: Uuid}) -> Result<Response, Oops> {
    if post.id == Uuid.nil() {
        Err(Oops{message: "nil post"})
    } else {
        Ok(Response{body: Body.Text("post ${post.id}")})
    }
}

fn main() {
    post: Route<.{id: Uuid}> = "GET /posts/{id}"
    item: Route<.{n: Int}> = "GET /items/{n}"
    router = Router.new([
        Route.to(Route´GET /a´, plain),
        Route.to(Route´GET /b´, fallible),
        Route.to(Route´GET /c´, fallible),
        Route.to(Route´GET /d´, std_error),
        Route.to(Route´GET /e´, std_error),
        Route.to(Route´GET /f´, fn request -> if request.url.path == "/f" { Err(Oops{message: "lambda err"}) } else { Ok(Response{body: Body.Text("no")}) }),
        Route.to(Route´GET /g´, fn request -> if request.url.path == "/g" { Ok(Response{body: Body.Text("lambda ok")}) } else { Err(Response.error(Status.not_found, "no")) }),
        Route.to_with(post, show),
        Route.to_with(item, fn _, p -> if p.n > 0 { Ok(Response{body: Body.Text("item ${p.n}")}) } else { Err(Oops{message: "bad item"}) }),
    ])!
    line = fn target: String -> {
        response = Router.serve(router, http_testing.request(Method.Get, target))
        io.print("${target} ${response.status} ${Response.text(response)}")
    }
    line("/a")
    line("/b")
    line("/c")
    line("/d")
    line("/e")
    line("/f")
    line("/g")
    line("/posts/0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a")
    line("/posts/00000000-0000-0000-0000-000000000000")
    line("/items/3")
    line("/items/0")
}
`, "´", "`")
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "/a 200 OK Ok(plain)\n/b 200 OK Ok(ok)\n/c 400 Bad Request Ok(oops\n)\n" +
		"/d 200 OK Ok(std ok)\n/e 404 Not Found Ok(std err\n)\n" +
		"/f 400 Bad Request Ok(lambda err\n)\n/g 200 OK Ok(lambda ok)\n" +
		"/posts/0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a 200 OK Ok(post 0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a)\n" +
		"/posts/00000000-0000-0000-0000-000000000000 400 Bad Request Ok(nil post\n)\n" +
		"/items/3 200 OK Ok(item 3)\n/items/0 400 Bad Request Ok(bad item\n)\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A route, an instance of std's generic Route<P>, held as Display renders
// through Route's impl: in a list, alone and as a Display parameter.
func TestRouteLiteral_HeldAsDisplay(t *testing.T) {
	src := strings.ReplaceAll(`import {
    std::http.Route
    std::io
    std::uuid.Uuid
}

fn show(d: Display) -> String {
    "<${d}>"
}

fn main() {
    post: Route<.{id: Uuid}> = "GET /posts/{id}"
    all: List<Display> = [Route´GET /{$}´, Route´GET /static/´, post, 3]
    io.print(all)
    one: Display = post
    io.print(one)
    io.print(show(Route´POST /x´))
}
`, "´", "`")
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "[GET /{$}, GET /static/, GET /posts/{id}, 3]\nGET /posts/{id}\n<POST /x>\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A route's wildcards are its parameters' fields, each a type that parses
// and displays, and a `{rest...}` one a String: each break is a compile
// error at the wildcard or the literal.
func TestRouteLiteral_ParametersMatchTheWildcards(t *testing.T) {
	for _, c := range []struct {
		line, message string
		col           int
	}{
		{`    r: Route<.{post_id: Uuid}> = "GET /posts/{id}"`, "`Route` literal \"GET /posts/{id}\" is invalid: the pattern has `{id}`, but `.{post_id: Uuid}` has no field `id`", 46},
		{`    r: Route<.{id: Uuid, slug: String}> = "GET /posts/{id}"`, "`Route` literal \"GET /posts/{id}\" is invalid: `.{id: Uuid, slug: String}` has a field `slug`, but the pattern has no `{slug}`", 43},
		{`    r: Route<.{rest: Int}> = "GET /files/{rest...}"`, "`Route` literal \"GET /files/{rest...}\" is invalid: `{rest...}` matches the rest of the path, so its field is a `String`, not `Int`", 42},
		{`    r: Route<Int> = "GET /x"`, "`Route` literal \"GET /x\" is invalid: a route's parameters are `Unit` or an anonymous struct with a field per wildcard, not `Int`", 21},
		{`    r: Route<Unit> = "GET /users/{id}"`, "`Route` literal \"GET /users/{id}\" is invalid: the pattern has `{id}`, and a `Route` has no parameters; a route with them goes to `Route.path_with` or `Route.to_with`", 34},
		{`    r: Route<.{id: Bool}> = "GET /flags/{id}"`, "a route's parameters are read from its path and written into links as text: each field's type implements `Parse` and `Display`, and `Bool`, the type of `id`, does not implement `Parse`", 29},
	} {
		src := "import {\n    std::http.Route\n    std::io\n    std::uuid.Uuid\n}\n\nfn main() {\n    _ = Uuid.nil()\n" + c.line + "\n    io.print(r)\n}\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.line, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s:\n got %q\nwant %q", c.line, ds[0].Message, c.message)
		}
		if ds[0].Line != 9 || ds[0].Col != c.col {
			t.Errorf("%s: at %d:%d, want 9:%d", c.line, ds[0].Line, ds[0].Col, c.col)
		}
	}
}

// A route with parameters given to Route.path or Route.to, and one with
// none given to Route.to_with, gets a help naming the function that fits.
func TestRouteLiteral_WrongPickNamesTheOtherFunction(t *testing.T) {
	for _, c := range []struct{ line, help string }{
		{`    _ = Route.path(r)`, "this route has parameters, `.{id: String}`; use `Route.path_with(route, params)` to fill them into the link"},
		{`    _ = Route.to(r, fn _ -> Response{})`, "this route has parameters, `.{id: String}`; use `Route.to_with(route, fn request, params -> ...)` to hand them to the handler"},
		{`    _ = Route.to_with(Route` + "`GET /a`" + `, fn _ -> Response{})` + "\n    _ = Route.path_with(r, .{id: \"7\"})", "this route has no parameters; use `Route.to(route, fn request -> ...)`, whose handler takes only the request"},
	} {
		src := "import std::http.{Response, Route}\n\nfn main() {\n    r = Route`GET /users/{id}`\n    _ = Response{}\n" + c.line + "\n}\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.line, len(ds), ds)
		}
		found := false
		for _, h := range ds[0].Hints {
			if h == c.help {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: hints %q, want %q", c.line, ds[0].Hints, c.help)
		}
	}
}

// A lambda handler whose body only answers Ok(...), or only Err(...), names
// one side of its Result and nothing the other. Against the handler's
// ToResponse result the open side is Infallible (spec §7 *Lambdas*), so
// `fn _, params -> Ok(page(params))` serves its page.
func TestRouteHandlers_OkOnlyLambdaServes(t *testing.T) {
	const src = `import {
    std::http.{Body, Method, Request, Response, Route, Router, Status}
    std::http_testing
    std::io
}

fn page(params: .{id: Int}) -> Response {
    Response{body: Body.Text("edit ${params.id}")}
}

// A named handler declares its result, so it names the error type itself.
fn named(_: Request) -> Result<Response, Infallible> {
    Ok(Response{body: Body.Text("named")})
}

fn main() {
    admin_post_edit: Route<.{id: Int}> = "POST /admin/posts/{id}/edit"
    router = Router.new([
        Route.to_with(admin_post_edit, fn _request, params -> Ok(page(params))),
        Route.to("GET /home", fn _ -> Ok(Response{body: Body.Text("home")})),
        Route.to("GET /gone", fn _ -> Err(Response.error(Status.not_found, "gone"))),
        Route.to("GET /named", named),
    ])!
    io.print(Response.text(Router.serve(router, http_testing.request(Method.Post, "/admin/posts/7/edit"))))
    io.print(Response.text(Router.serve(router, http_testing.request(Method.Get, "/home"))))
    io.print(Router.serve(router, http_testing.request(Method.Get, "/gone")).status)
    io.print(Response.text(Router.serve(router, http_testing.request(Method.Get, "/named"))))
}
`
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "Ok(edit 7)\nOk(home)\n404 Not Found\nOk(named)\n"; out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A route literal written as an argument beside a handler that names its
// parameters takes them from the handler: the literal, untagged or
// tagged, passed or piped, is checked as `Route<.{id: Int}>` and the
// request's segment is parsed into an Int. A lambda that leaves the
// parameters to the call still gets a String per wildcard.
func TestRouteLiteral_ArgumentTakesTheHandlersParameters(t *testing.T) {
	const src = `import {
    std::http.{Body, Method, Request, Response, Route, Router}
    std::http_testing
    std::io
}

fn update(_: Request, item: .{id: Int}) -> Response {
    Response{body: Body.Text("todo ${item.id + 1}")}
}

fn main() {
    router = Router.new([
        Route.to_with("PATCH /todos/{id}", update),
        Route.to_with(Route` + "`GET /todos/{id}`" + `, update),
        "PUT /todos/{id}" |> Route.to_with(update),
        Route.to_with("GET /names/{id}", fn _, p -> Response{body: Body.Text(p.id + "!")}),
        Route.to_with("GET /n/{id}", fn _: Request, p: .{id: Int} -> Response{body: Body.Text("${p.id * 2}")}),
    ])!

    serve = fn method: Method, target: String -> Router.serve(router, http_testing.request(method, target))
    io.print(Response.text(serve(Method.Patch, "/todos/41")))
    io.print(Response.text(serve(Method.Get, "/todos/1")))
    io.print(Response.text(serve(Method.Put, "/todos/9")))
    io.print(serve(Method.Get, "/todos/x").status)
    io.print(Response.text(serve(Method.Get, "/names/x")))
    io.print(Response.text(serve(Method.Get, "/n/21")))
}
`
	var out strings.Builder
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "Ok(todo 42)\nOk(todo 2)\nOk(todo 10)\n404 Not Found\nOk(x!)\nOk(42)\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A route literal whose wildcards are not the fields of the handler's
// parameters is an error at the literal, untagged or tagged, naming the
// wildcard the fields lack, at the wildcard.
func TestRouteLiteral_ArgumentRefusesAHandlerOfOtherFields(t *testing.T) {
	for _, c := range []struct {
		arg, want string
		col       int
	}{
		{`"PATCH /todos/{name}"`, "`Route` literal \"PATCH /todos/{name}\" is invalid: the pattern has `{name}`, but `.{id: Int}` has no field `name`", 37},
		{"Route`PATCH /todos/{name}`", "typed literal Route`PATCH /todos/{name}` is invalid: the pattern has `{name}`, but `.{id: Int}` has no field `name`", 42},
	} {
		src := "import std::http.{Body, Request, Response, Route}\n\n" +
			"fn update(_: Request, item: .{id: Int}) -> Response {\n    Response{body: Body.Text(\"${item.id}\")}\n}\n\n" +
			"fn main() {\n    _ = Route.to_with(" + c.arg + ", update)\n}\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.arg, len(ds), ds)
		}
		if ds[0].Message != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.arg, ds[0].Message, c.want)
		}
		if ds[0].Line != 8 || ds[0].Col != c.col {
			t.Errorf("%s: at %d:%d, want 8:%d", c.arg, ds[0].Line, ds[0].Col, c.col)
		}
	}
}
