package stdhttp_test

import (
	"strings"
	"testing"
)

// serverProgram serves handle on 127.0.0.1, port 0, under a supervisor boot
// makes, and runs main's requests against it with std/http's own client.
const serverProgram = `import std/http
import std/http.{Body, Client, ClientOptions, Cookie, CookieJar, Headers, Listener, Request, Response, Server, Status}
import std/duration.Duration
import std/io.{self, Writer}
import std/supervisors.Supervisor
import std/url.Url

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
	got := run(t, `import std/fs_testing.{self, Sandbox}
import std/http
import std/http.{Headers, Method, Response, Status}
import std/http_testing
import std/io

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
		`<a href="x.png">x.png</a>`,
		`<a href="y%20z.txt">y z.txt</a>`,
		"404 text/plain; charset=utf-8 - 404 page not found",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
