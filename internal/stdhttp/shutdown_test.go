package stdhttp_test

import "testing"

// A handler still returning after its client closed: the shutdown with_server
// runs when its body returns waits for it, as for any request it is serving.
// Go's own Shutdown waits for no hijacked connection, and the handler's task
// was cancelled, failing with_server, whenever it was slower than the client.
const webSocketShutdownProgram = `import std::duration.Duration
import std::http.Request
import std::http::testing
import std::io
import std::timer
import std::url.Url
import std::websocket.{self, Message, WebSocket}

fn main()
    echo =
        fn request: Request ->
            websocket.accept(request, fn ws ->
                _ = WebSocket.each(ws, fn message ->
                    _ = WebSocket.send(ws, message)
                end)
                timer.sleep(Duration.milliseconds(50))
                io.print("the handler returns")
            end)
        end

    said = testing.with_server(echo, fn server ->
        ws = websocket.connect(Url.parse("ws://${server.url.host}/echo")!)!
        _ = WebSocket.send_text(ws, "hi")
        reply = WebSocket.receive(ws)
        _ = WebSocket.close(ws)
        reply
    end)

    io.print("echoed ${said == Ok(Some(Message.Text("hi")))}")
end
`

func TestShutdown_WaitsForAWebSocketHandlerToReturn(t *testing.T) {
	if got, want := run(t, webSocketShutdownProgram), "the handler returns\nechoed True\n"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}
}
