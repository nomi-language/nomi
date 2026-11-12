package stdhttp_test

import (
	"strings"
	"testing"
)

const hooksProgram = `import std::http
import std::http.{Body, Client, ClientOptions, ConnState, Listener, Request, Response, Server, Headers, Tracer, TraceEvent}
import std::io.{self, Reader, Writer}
import std::net.{self, Connection}
import std::supervisors.Supervisor
import std::tls.{self, Certificate, ClientConfig, ClientHello, ConnectionState, Roots, ServerConfig}
import std::url.Url
import std::channels.{Channel, Receiver, Sender}
import std::duration.Duration

struct App
    web: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{web: Supervisor.new(max_running: 8)})
end

fn who(request: Request) -> Response
    secure = request.tls |> Maybe.map(fn s -> "${s.server_name} ${Maybe.is_some(s.local_certificate)}") |> Maybe.with_default("plain")
    extended = Request.extend_read_deadline(request, Duration.seconds(5)) |> Result.is_ok()
    Response{body: Body.Text("${secure} ${request.local_address != ""} ${extended}")}
end

fn main()
    certificate = Certificate.load("CERT", "KEY")!
    trusted = ClientConfig{roots: Roots.Files(["CERT"])}
    states: Channel<String> = Channel.buffered(64)
    shutdowns: Channel<String> = Channel.buffered(4)

    server = Server{
        address: "127.0.0.1:0",
        tls: Some(ServerConfig{
            certificates: [],
            certificate_for: Some(fn hello: ClientHello ->
                _ = Sender.send(states.sender, "hello ${hello.server_name} ${ClientHello.supports(hello, certificate)}")
                Some(certificate)
            end),
        }),
        on_connection_state: Some(fn _address: String, state: ConnState ->
            _ = Sender.send(states.sender, "state ${state}")
        end),
        on_shutdown: Some(fn ->
            _ = Sender.send(shutdowns.sender, "shutdown")
        end),
    }

    listener = http.listen(server)!
    _ = Supervisor.spawn(App.web, fn -> http.serve(listener, who, App.web))
    tracer = Tracer.new()

    client = Client.new(ClientOptions{
        tls: Some({
            ..trusted,
            server_name: Some("localhost"),
            verify: Some(fn s: ConnectionState -> if s.server_name == "localhost" then Ok(Unit) else Err("wrong name")),
        }),
    })

    url = Url.parse("https://${Listener.address(listener)}/")!
    response = Client.fetch(client, Request{url:, trace: Some(tracer)})!
    io.print("served ${Response.text(response)!}")
    io.print("response tls ${response.tls |> Maybe.map(fn s -> s.handshake_complete)}")
    events = Tracer.events(tracer)
    io.print("traced ${Iter.any_match(events, fn e -> e == TraceEvent.TlsStart)} ${Iter.any_match(events, fn e -> e == TraceEvent.FirstResponseByte)}")

    refusing = Client.new(ClientOptions{
        tls: Some({..trusted, server_name: Some("localhost"), verify: Some(fn _s: ConnectionState -> Err("no thanks"))}),
    })

    refused = Client.fetch(refusing, Request{url:}) |> Result.map(fn _ -> "fetched") |> Result.map_err(fn e -> String.contains("${e}", "no thanks"))
    io.print("refused ${refused}")
    Listener.shutdown(listener)
    io.print("hello seen ${Receiver.receive(states.receiver)}")
    io.print("shutdown seen ${Receiver.receive_within(shutdowns.receiver, Duration.seconds(5)) |> Result.is_ok()}")

    upgrades = http.listen(Server{address: "127.0.0.1:0"})!
    _ = Supervisor.spawn(App.web, fn ->
        http.serve(upgrades, fn _r: Request ->
            Response.upgrade(headers: Headers[("Upgrade", "echo")], on_connection: fn c: Connection ->
                line = Reader.read_line(Connection.reader(c))! |> Maybe.with_default("")
                _ = Writer.write_text(Connection.writer(c), "upgraded ${line}\n")
            end)
        end, App.web)
    end)

    conn = net.dial(Listener.address(upgrades))!
    _ = Writer.write_text(Connection.writer(conn), "GET / HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\nping\n")
    status_line = Reader.read_line(Connection.reader(conn))! |> Maybe.with_default("")
    io.print("upgrade ${status_line}")
    _ = Reader.read_line(Connection.reader(conn))
    _ = Reader.read_line(Connection.reader(conn))
    io.print("over it ${Reader.read_line(Connection.reader(conn))! |> Maybe.with_default("-")}")
    Connection.close(conn)
    Listener.shutdown(upgrades)

    plain = net.listen("127.0.0.1:0")!
    secure = tls.wrap_listener(plain, ServerConfig{certificates: [certificate]})!
    _ = Supervisor.spawn(App.web, fn -> net.serve(secure, fn c: Connection ->
        _ = Writer.write_text(Connection.writer(c), "wrapped\n")
    end, App.web))
    (sconn, keys) = Writer.collect(fn log -> tls.dial(net.Listener.address(secure), {..trusted, server_name: Some("localhost"), key_log: Some(log)})!)
    io.print("key log ${String.contains(Bytes.to_string(keys)!, "SECRET")}")
    io.print("wrap ${Reader.read_line(Connection.reader(sconn))! |> Maybe.with_default("-")}")
    Connection.close(sconn)
    net.Listener.close(secure)
end
`

// Go's hooks into a program run on the task waiting for the work that set
// them off: a server's certificate_for, connection states and on_shutdown,
// a client's verify, a tracer's events, an upgraded connection, and a
// wrapped TLS listener, end to end on 127.0.0.1.
func TestHooks_RunOnTheWaitingTask(t *testing.T) {
	certPath, keyPath := selfSigned(t, t.TempDir())
	src := strings.ReplaceAll(strings.ReplaceAll(hooksProgram, `"CERT"`, `"`+certPath+`"`), `"KEY"`, `"`+keyPath+`"`)
	got := run(t, src)
	for _, w := range []string{
		"served localhost True True True",
		"response tls Some(True)",
		"traced True True",
		"refused Err(True)",
		"hello seen",
		"shutdown seen True",
		"upgrade HTTP/1.1 101 Switching Protocols",
		"over it upgraded ping",
		"key log True",
		"wrap wrapped",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
