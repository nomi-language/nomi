package stdhttp_test

import (
	"strings"
	"testing"
)

const webSocketProgram = `import std::http
import std::http.{Headers, Listener, Request, Response, Server}
import std::io
import std::log.Logger
import std::duration.Duration
import std::supervisors.Supervisor
import std::tasks.Task
import std::timer
import std::tls.{Certificate, ClientConfig, Roots, ServerConfig}
import std::url.Url
import std::websocket.{self, AcceptOptions, ConnectOptions, Message, WebSocket}

struct App {
    web: Supervisor
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(App{web: Supervisor.new(max_running: 8)})
}

// Each message comes back twice, sent by two tasks at once.
fn chat(request: Request) -> Response {
    options = AcceptOptions{protocols: ["chat"], ping_interval: Some(Duration.milliseconds(5))}

    websocket.accept(request, options, fn ws -> {
        _ = WebSocket.each(ws, fn message -> {
            concurrent {
                a = Task.spawn(fn -> WebSocket.send(ws, message))
                b = Task.spawn(fn -> WebSocket.send(ws, message))
                _ = Task.await(a)
                _ = Task.await(b)
            }
        })
    })
}

fn main() {
    certificate = Certificate.load("CERT", "KEY")!

    app =
        chat
        |> http.timeout(Duration.milliseconds(20), "slow")
        |> http.limit_body(10)
        |> http.compress()
        |> http.security_headers()
        |> http.log_requests(Logger.discard())

    listener = http.listen(Server{address: "127.0.0.1:0", tls: Some(ServerConfig{certificates: [certificate]})})!
    _ = Supervisor.spawn(App.web, fn -> http.serve(listener, app, App.web))

    options = ConnectOptions{
        protocols: ["other", "chat"],
        headers: Headers[#("Accept-Encoding", "gzip")],
        tls: ClientConfig{roots: Roots.Files(["CERT"]), server_name: Some("localhost")},
    }

    ws = websocket.connect(Url.parse("wss://${Listener.address(listener)}/chat")!, options)!
    io.print("protocol ${WebSocket.protocol(ws)}")
    // Past the timeout middleware's 20 ms: the connection lives on.
    timer.sleep(Duration.milliseconds(60))
    _ = WebSocket.send_text(ws, "after the timeout")
    first = WebSocket.receive(ws) == Ok(Some(Message.Text("after the timeout")))
    second = WebSocket.receive(ws) == Ok(Some(Message.Text("after the timeout")))
    io.print("echoed twice ${first} ${second}")
    _ = WebSocket.close(ws)
    io.print("after close ${WebSocket.receive(ws) == Ok(None)}")
    io.print("send after close ${WebSocket.send_text(ws, "late") |> Result.is_err()}")

    plain = websocket.connect(Url.parse("ws://${Listener.address(listener)}/chat")!)
    io.print("plain to a TLS server ${plain |> Result.is_err()}")
    Listener.shutdown(listener)
}
`

// A WebSocket over TLS through every middleware that could break an
// upgrade, with a ping interval and concurrent sends, end to end on
// 127.0.0.1.
func TestWebSocket_EndToEnd(t *testing.T) {
	certPath, keyPath := selfSigned(t, t.TempDir())
	src := strings.ReplaceAll(strings.ReplaceAll(webSocketProgram, `"CERT"`, `"`+certPath+`"`), `"KEY"`, `"`+keyPath+`"`)
	got := run(t, src)
	for _, w := range []string{
		"protocol Some(chat)",
		"echoed twice True True",
		"after close True",
		"send after close True",
		"plain to a TLS server True",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
