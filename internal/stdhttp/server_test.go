package stdhttp_test

import (
	"strings"
	"testing"
)

// serverProgram serves handle on 127.0.0.1, port 0, under a supervisor boot
// makes, and runs main's requests against it with std/http's own client.
const serverProgram = `import std::http
import std::http.{Body, Client, ClientOptions, Cookie, CookieJar, Headers, Listener, Request, Response, Server, Status}
import std::duration.Duration
import std::io.{self, Writer}
import std::supervisors.Supervisor
import std::url.Url

struct App {
    web: Supervisor
    context: Context
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(App{web: Supervisor.new(max_running: 8), context: Context.root()})
}

fn handle(request: Request) -> Response {
    case request.url.path {
        "/hello" ->
            Response{body: Body.Text("hello ${Request.user_agent(request) |> Maybe.with_default("?")}")}

        "/echo" -> {
            text = Bytes.to_string(Body.read_all(request.body)!)!
            Response{status: Status.created, body: Body.Text("${request.method} ${text}")}
        }

        "/stream" ->
            Response{
                headers: Headers.set(Headers[], "Content-Type", "text/event-stream"),
                body: Body.Produce(fn w -> {
                    Writer.print(w, "data: one")?
                    Writer.flush(w)?
                    Writer.print(w, "data: two")
                }),
            }

        "/deadline" ->
            Response{body: Body.Text("${Context.deadline(App.context) |> Maybe.is_some()} ${Context.deadline(request.context) |> Maybe.is_some()}")}

        "/trap" ->
            todo

        "/login" ->
            Response.set_cookie(Response{body: Body.Text("in")}, Cookie{name: "user", value: "ann", path: Some("/")})

        "/whoami" ->
            Response{body: Body.Text(Request.cookie(request, "user") |> Maybe.map(fn c -> c.value) |> Maybe.with_default("nobody"))}

        _ ->
            Response{status: Status.not_found, body: Body.Text("no ${request.url.path}")}
    }
}

fn get(base: String, path: String) -> String {
    response = http.get(Url.parse("${base}${path}")!)!
    "${Status.code(response.status)} ${Response.text(response)!}"
}

fn main() {
    listener = http.listen(Server{address: "127.0.0.1:0", request_timeout: Some(Duration.seconds(30))})!
    _ = Supervisor.spawn(App.web, fn -> http.serve(listener, handle, App.web))
    base = "http://${Listener.address(listener)}"
    io.print(get(base, "/hello"))
    posted = http.post(Url.parse("${base}/echo")!, Body.Text("hi"))!
    io.print("${Status.code(posted.status)} ${Response.text(posted)!}")
    io.print(get(base, "/stream"))
    io.print(get(base, "/deadline"))
    io.print(get(base, "/trap"))
    io.print(get(base, "/nope"))
    client = Client.new(ClientOptions{cookies: Some(CookieJar.new())})
    _ = Client.get(client, Url.parse("${base}/login")!)!
    who = Client.get(client, Url.parse("${base}/whoami")!)!
    io.print("cookie ${Response.text(who)!}")
    io.print(get(base, "/whoami"))
    Listener.shutdown(listener)
}
`

// A server answers requests with a Nomi handler: a body read and echoed, a
// produced stream, the request's context under the app's Context field, a
// trapping handler's 500, and a 404 the handler chose.
func TestServer_AnswersWithAHandler(t *testing.T) {
	got := run(t, serverProgram)
	want := []string{
		"200 hello Go-http-client/1.1",
		"201 POST hi",
		"200 data: one\ndata: two",
		"200 True True",
		"500 Internal Server Error",
		"404 no /nope",
		"cookie ann",
		"200 nobody",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

// http.file_server answers from std/fs, so a sandbox's files are what it
// serves: a file with its type, a range, a directory's redirect to its
// slashed path, index.html and a listing, and a 404. /docs and
// /docs/index.html both redirect to /docs/, as Go's FileServer does.
func TestServer_FileServerReadsThroughStdFs(t *testing.T) {
	got := run(t, `import std::fs_testing.{self, Sandbox}
import std::http
import std::http.{Headers, Method, Response, Status}
import std::http_testing
import std::io

fn show(r: Response) -> String {
    "${Status.code(r.status)} ${Headers.get(r.headers, "Content-Type") |> Maybe.with_default("-")} ${Headers.get(r.headers, "Location") |> Maybe.with_default("-")} ${Response.text(r) |> Result.with_default("?")}"
}

fn main() {
    files = %{"site/a.txt" => "hello", "site/docs/index.html" => "<p>docs</p>", "site/pics/x.png" => "png", "site/pics/y z.txt" => "y"}
    ran = fs_testing.run(Sandbox{files}, fn -> {
        serve = http.file_server("site")
        get = fn target: String -> show(serve(http_testing.request(Method.Get, target)))
        ranged = http_testing.request(Method.Get, "/a.txt")
        [
            get("/a.txt"),
            show(serve(#{..ranged, headers: Headers.set(ranged.headers, "Range", "bytes=1-3")})),
            get("/docs"),
            get("/docs/"),
            get("/docs/index.html"),
            get("/pics/"),
            get("/missing.txt"),
        ]
    })
    Iter.each(ran.value, fn line -> io.print(line))
}
`)
	for _, w := range []string{
		"200 text/plain; charset=utf-8 - hello",
		"206 text/plain; charset=utf-8 - ell",
		"301 text/html; charset=utf-8 /docs/ <a href=\"/docs/\">Moved Permanently</a>",
		"200 text/html; charset=utf-8 - <p>docs</p>",
		`<a href='./x.png'>x.png</a>`,
		`<a href='./y%20z.txt'>y z.txt</a>`,
		"404 text/plain; charset=utf-8 - 404 page not found",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

// The form readers' defaults: a URL-encoded body of exactly 10 MB is read
// and one byte more refused, with no more than one byte past the limit
// taken from the stream; limit_body raises the default; a multipart body
// one byte past 32 MB is refused.
func TestServer_FormReadersHaveDefaultLimits(t *testing.T) {
	got := run(t, `import std::http.{Body, Headers, Method, Request}
import std::http_testing
import std::io.{self, Reader}

fn posted(body: Reader) -> Request {
    #{
        ..http_testing.request(Method.Post, "/"),
        headers: Headers[#("Content-Type", "application/x-www-form-urlencoded")],
        body: Body.Stream(body),
    }
}

fn form(size: Int) -> Request {
    posted(Reader.from_text("a=${String.repeat("x", size - 2)}"))
}

fn main() {
    io.print("at ${Request.post_form(form(10_485_760)) |> Result.is_ok()}")
    io.print("over ${Request.post_form(form(10_485_761))}")
    io.print("raised ${Request.post_form(Request.limit_body(form(10_485_761), 20_000_000)) |> Result.is_ok()}")

    huge = Reader.from_text("a=${String.repeat("x", 19_999_998)}")
    io.print("huge ${Request.post_form(posted(huge))}")
    io.print("left ${Bytes.length(Reader.read_all(huge)!)}")

    upload = #{
        ..http_testing.request(Method.Post, "/upload"),
        headers: Headers[#("Content-Type", "multipart/form-data; boundary=b")],
        body: Body.Stream(Reader.from_bytes(String.to_bytes(String.repeat("x", 33_554_433)))),
    }

    io.print("multipart ${Request.multipart_form(upload) |> Result.map(fn _ -> "read")}")
}
`)
	for _, w := range []string{
		"at True",
		"over Err(the body is larger than 10485760 bytes)",
		"raised True",
		"huge Err(the body is larger than 10485760 bytes)",
		"left 9514239",
		"multipart Err(the body is larger than 33554432 bytes)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

// A Server's max_body limits every request's body, as Request.limit_body
// does: a form of exactly the limit is read, one byte more is
// Error.BodyTooLarge, and a route's own limit only lowers the server's.
func TestServer_MaxBodyLimitsEveryRequest(t *testing.T) {
	got := run(t, `import std::http.{Body, Client, Request, Response, Server, Status}
import std::http_testing
import std::io
import std::url.{Query, Url}

fn echo(request: Request) -> Response {
    limited = if request.url.path == "/small" { Request.limit_body(request, 3) } else { request }

    case Request.form(limited) {
        Ok(form) -> Response{body: Body.Text(Query.get(form, "a") |> Maybe.with_default(""))}
        Err(e) -> Response.error(Status.request_entity_too_large, "${e}")
    }
}

fn main() {
    http_testing.with_server(echo, server: Server{max_body: Some(5)}, fn server -> {
        post = fn path: String, a: String -> {
            url = Url.resolve_text(server.url, path)!
            r = Client.post_form(server.client, url, Query[#("a", a)])!
            io.print("${path} ${a}: ${Status.code(r.status)} ${Response.text(r)!}")
        }

        post("/", "123")
        post("/", "1234")
        post("/small", "1")
        post("/small", "12")
    })
}
`)
	for _, w := range []string{
		"/ 123: 200 123",
		"/ 1234: 413 the body is larger than 5 bytes",
		"/small 1: 200 1",
		"/small 12: 413 the body is larger than 3 bytes",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
