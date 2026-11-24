package stdhttp_test

import (
	"strings"
	"testing"
)

const unixHTTPProgram = `import std::fs
import std::http.{self, Body, Client, ClientOptions, Listener, Request, Response, Server, Transport}
import std::io
import std::net
import std::supervisors.Supervisor
import std::url.Url

struct App
    web: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{web: Supervisor.new(max_running: 8)})
end

fn show(request: Request) -> Response
    Response{body: Body.Text("host=${request.url.host} path=${request.url.path}")}
end

fn main()
    path = "DIR/api.sock"
    listener = http.listen(Server{listener: Some(net.listen_unix(path)!)})!
    _ = Supervisor.spawn(App.web, fn -> http.serve(listener, show, App.web))
    io.print("address ${Listener.address(listener) == path}")

    client = Client.new(ClientOptions{transport: {..Transport.default, unix_socket: Some(path)}})
    response = Client.get(client, Url.parse("http://docker/v1.43/info")!)!
    io.print("unix ${Response.text(response)!}")

    elsewhere = Client.new(ClientOptions{transport: {..Transport.default, unix_socket: Some("DIR/none.sock")}})
    io.print("no socket ${Client.get(elsewhere, Url.parse("http://docker/")!) |> Result.is_err()}")

    Listener.shutdown(listener)
    io.print("file ${fs.exists(path)}")
end
`

// An HTTP server serves on a std::net Unix listener, and a client whose
// transport names a Unix socket sends every request there, the URL's host
// only its Host header. A socket nothing listens on fails the request, and
// the server's shutdown closes the listener, which removes its file.
func TestServe_HTTPOverAUnixSocket(t *testing.T) {
	got := run(t, withDir(unixHTTPProgram, shortDir(t)))
	for _, w := range []string{
		"address True",
		"unix host=docker path=/v1.43/info",
		"no socket True",
		"file Ok(False)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
