package stdhttp_test

import (
	"strings"
	"testing"
)

const targetProgram = `import std::http
import std::http.{Body, Listener, Request, Response, Server}
import std::io.{self, Reader, Writer}
import std::net.{self, Connection}
import std::supervisors.Supervisor

struct App
    web: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{web: Supervisor.new(max_running: 8)})
end

fn show(request: Request) -> Response
    Response{body: Body.Text("host=${request.url.host} path=${request.url.path} query=${request.url.query} fragment=${request.url.fragment}")}
end

// The response's last line: the body show wrote.
fn ask(address: String, method: String, target: String) -> String
    conn = net.dial(address)!
    _ = Writer.write_text(Connection.writer(conn), "${method} ${target} HTTP/1.1\r\nHost: served.example\r\nConnection: close\r\n\r\n")
    text = Reader.read_all_text(Connection.reader(conn))!
    Connection.close(conn)
    String.split(text, "\n") |> Iter.last() |> Maybe.with_default("")
end

fn main()
    listener = http.listen(Server{address: "127.0.0.1:0"})!
    _ = Supervisor.spawn(App.web, fn -> http.serve(listener, show, App.web))
    address = Listener.address(listener)
    io.print("hash " + ask(address, "GET", "/x?q=#%zz"))
    io.print("slashes " + ask(address, "GET", "//elsewhere.example/x"))
    io.print("connect " + ask(address, "CONNECT", "tunnel.example:443"))
    Listener.shutdown(listener)
end
`

// A served request's URL is the target as Go's server read it, so every
// target Go accepts reaches the handler: a '#' in the query is the query's
// (url.Parse, reading Go's URL back as text, took "%zz" for a fragment and
// refused it, which trapped the handler), a path starting "//" names no
// host (url.Parse read the host as elsewhere.example), and a CONNECT's
// authority is the host.
func TestServe_ReadsTheTargetAsGoDoes(t *testing.T) {
	got := run(t, targetProgram)
	for _, w := range []string{
		"hash host=served.example path=/x query=Some(q=#%zz) fragment=None",
		"slashes host=served.example path=//elsewhere.example/x query=None fragment=None",
		"connect host=tunnel.example:443 path= query=None fragment=None",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
