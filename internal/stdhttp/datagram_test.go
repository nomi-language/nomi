package stdhttp_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/strlit"
)

// shortDir is a fresh directory with a short path, for Unix sockets, whose
// path a system holds to about 100 bytes: a test's own temporary directory
// is often longer.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nomi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// withDir is program with dir in place of its "DIR" literals' text, as a
// Nomi string's body.
func withDir(program, dir string) string {
	return strings.ReplaceAll(program, "DIR", strlit.EncodeStringBody(dir))
}

const datagramProgram = `import std::duration.Duration
import std::fs
import std::instant.Instant
import std::io.{self, Reader, Writer}
import std::net.{self, Connection, Error}
import std::supervisors.Supervisor
import std::tasks.Task
import std::timer
import std::udp.{self, Address, Socket}

struct App
    work: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{work: Supervisor.new(max_running: 8)})
end

fn answer(socket: Socket, data: Bytes, from: Address) -> Unit
    _ = Socket.send_to(socket, data, from)
end

fn echo(conn: Connection) -> Unit
    line = Reader.read_line(Connection.reader(conn))! |> Option.with_default("")
    _ = Writer.write_text(Connection.writer(conn), "echo ${line}\n")
end

fn done(r: Result<Unit, Error>) -> String
    case r
        Ok(_)  -> "ok"
        Err(e) -> "${e}"
    end
end

fn wait_on(socket: Socket) -> Unit
    _ = Socket.receive(socket)
end

fn text(received: Result<(Bytes, Address), Error>) -> String
    received
    |> Result.map(fn (data, _) -> Bytes.to_string_lossy(data))
    |> Result.with_default("-")
end

fn main()
    soon = Instant.now() + Duration.seconds(10)

    server = udp.bind("127.0.0.1:0")!
    _ = Supervisor.spawn(App.work, fn -> udp.serve(server, fn data, from -> answer(server, data, from), App.work))
    client = udp.bind("127.0.0.1:0")!
    Socket.set_read_deadline(client, Some(soon))
    Socket.send_to(client, String.to_bytes("ping"), Socket.local_address(server))!
    (reply, from) = Socket.receive(client)!
    io.print("udp echo ${Bytes.to_string_lossy(reply)} ${from == Socket.local_address(server)}")

    dialed = udp.dial("${Socket.local_address(server)}")!
    Socket.set_read_deadline(dialed, Some(soon))
    Socket.send(dialed, String.to_bytes("connected"))!
    io.print("dialed ${text(Socket.receive(dialed))} ${Socket.remote_address(dialed) == Some(Socket.local_address(server))}")
    Socket.close(dialed)

    Socket.send_to(client, String.to_bytes("0123456789"), Socket.local_address(client))!
    io.print("truncated ${Socket.receive(client, max: 4) |> Result.map(fn _ -> "fit")}")
    Socket.send_to(client, String.to_bytes("next"), Socket.local_address(client))!
    io.print("then ${text(Socket.receive(client, max: 4))}")

    Socket.set_read_deadline(client, Some(Instant.now()))
    io.print("deadline ${Socket.receive(client) |> Result.map(fn _ -> "got") |> Result.map_err(Error.is_timeout)}")
    Socket.set_read_deadline(client, Some(soon))

    io.print("broadcast ${done(Socket.set_broadcast(client, False))} ${Socket.broadcast(client)} ${done(Socket.set_broadcast(client, True))} ${Socket.broadcast(client)}")
    io.print("multicast ${done(Socket.set_multicast_ttl(client, 3))} ${Socket.multicast_ttl(client)} ${done(Socket.set_multicast_loopback(client, False))} ${Socket.multicast_loopback(client)}")
    io.print("ttl ${done(Socket.set_ttl(client, 9))} ${Socket.ttl(client)}")
    io.print("buffers ${done(Socket.set_read_buffer(client, 65_536))} ${done(Socket.set_write_buffer(client, 65_536))}")
    io.print("buffer sizes ${Socket.read_buffer(client) |> Result.map(fn n -> n >= 65_536)} ${Socket.write_buffer(client) |> Result.map(fn n -> n >= 65_536)}")
    io.print("in use ${udp.bind("${Socket.local_address(client)}") |> Result.map(fn _ -> "bound")}")

    idle = udp.bind("127.0.0.1:0")!
    waiting = Supervisor.spawn(App.work, fn -> wait_on(idle))
    timer.sleep(Duration.milliseconds(100))
    Task.cancel(waiting)
    io.print("stopped ${Debug.inspect(Task.outcome(waiting))}")
    Socket.set_read_deadline(idle, Some(soon))
    Socket.send_to(client, String.to_bytes("after"), Socket.local_address(idle))!
    io.print("after stop ${text(Socket.receive(idle))}")
    Socket.close(idle)
    io.print("closed ${Socket.receive(idle) |> Result.map(fn _ -> "got")}")

    path = "DIR/s.sock"
    listener = net.listen_unix(path)!
    _ = Supervisor.spawn(App.work, fn -> net.serve(listener, echo, App.work))
    conn = net.dial_unix(path)!
    io.print("unix buffers ${done(Connection.set_read_buffer(conn, 8_192))} ${done(Socket.set_ttl(udp.bind_unix("DIR/g.sock")!, 3))}")
    io.print("unix buffer sizes ${Connection.read_buffer(conn) |> Result.map(fn n -> n > 0)} ${Connection.write_buffer(conn) |> Result.map(fn n -> n > 0)}")
    _ = Writer.write_text(Connection.writer(conn), "ping\n")
    io.print("unix ${Reader.read_line(Connection.reader(conn))! |> Option.with_default("-")} ${Connection.remote_address(conn) == path} ${net.Listener.address(listener) == path}")
    Connection.close(conn)
    net.Listener.close(listener)
    io.print("unix file ${fs.exists(path)}")
    io.print("unix refused ${net.dial_unix(path) |> Result.map(fn _ -> "connected") |> Result.is_err()}")
end
`

// UDP and Unix stream sockets end to end: an echo through udp.serve on
// 127.0.0.1, a dialed socket, a datagram longer than max, a read deadline,
// each socket option set and read back, an address in use, a receive whose
// task is stopped and the socket after it, a Unix stream echo through
// net.serve, and the socket file gone when its listener closes. The
// program ends with udp.serve still receiving: the drain closes its socket.
func TestDatagram_UDPAndUnixStreamEndToEnd(t *testing.T) {
	src := datagramProgram
	if runtime.GOOS == "windows" {
		// Windows has no Unix datagram sockets; its answer is checked below.
		src = strings.Replace(src, `${done(Socket.set_ttl(udp.bind_unix("DIR/g.sock")!, 3))}`, `set_ttl: a Unix socket has no ttl option`, 1)
	}
	got := run(t, withDir(src, shortDir(t)))
	for _, w := range []string{
		"udp echo ping True",
		"dialed connected True",
		"truncated Err(a datagram ",
		" is longer than 4 bytes)",
		"then next",
		"deadline Err(True)",
		"broadcast ok Ok(False) ok Ok(True)",
		"multicast ok Ok(3) ok Ok(False)",
		"ttl ok Ok(9)",
		"buffers ok ok",
		"buffer sizes Ok(True) Ok(True)",
		"unix buffer sizes Ok(True) Ok(True)",
		"is already in use)",
		"after stop after",
		"closed Err(the connection is closed)",
		"unix echo ping True True",
		"unix buffers ok set_ttl: a Unix socket has no ttl option",
		"unix file Ok(False)",
		"unix refused True",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
	// The stopped receive returns Closed, so its task either unwinds as
	// cancelled or finishes its body first: either way it has ended.
	if !strings.Contains(got, "stopped Cancelled") && !strings.Contains(got, "stopped Completed(Unit)") {
		t.Errorf("the task stopped in a receive did not end:\n%s", got)
	}
}

const unixDatagramProgram = `import std::io
import std::udp.{self, Address, Socket}

fn main()
    a = udp.bind_unix("DIR/a.sock")!
    b = udp.bind_unix("DIR/b.sock")!
    Socket.send_to(a, String.to_bytes("ready"), Address.Unix{path: "DIR/b.sock"})!
    (data, from) = Socket.receive(b)!
    io.print("unixgram ${Bytes.to_string_lossy(data)} ${from}")
    io.print("in use ${udp.bind_unix("DIR/b.sock") |> Result.map(fn _ -> "bound")}")
    notify = udp.dial_unix("DIR/b.sock")!
    Socket.send(notify, String.to_bytes("READY=1"))!
    (sent, anonymous) = Socket.receive(b)!
    io.print("notify ${Bytes.to_string_lossy(sent)} ${anonymous == Address.Unix{path: ""}}")
    io.print("wrong family ${Socket.send_to(a, String.to_bytes("x"), Address.Ip{host: "127.0.0.1", port: 9}) |> Result.is_err()}")
    Socket.close(b)
    Socket.close(a)
end
`

// A pair of Unix datagram sockets, one answering a socket bound to no
// path as systemd's notify socket is written to, and the bound socket's
// file in use until it closes. Windows has no Unix datagram sockets.
func TestDatagram_UnixDatagramPair(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix datagram sockets; std/udp's bind_unix prompt checks its Unsupported error")
	}
	dir := shortDir(t)
	got := run(t, withDir(unixDatagramProgram, dir))
	for _, w := range []string{
		"unixgram ready " + filepath.Join(dir, "a.sock"),
		"is already in use)",
		"notify READY=1 True",
		"wrong family True",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
	for _, name := range []string{"a.sock", "b.sock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s is left after its socket closed: %v", name, err)
		}
	}
}

const multicastProgram = `import std::duration.Duration
import std::instant.Instant
import std::io
import std::net.Error
import std::udp.{self, Address, BindOptions, Socket}

fn done(r: Result<Unit, Error>) -> String
    case r
        Ok(_)  -> "ok"
        Err(e) -> "${e}"
    end
end

fn main()
    receiver = udp.bind("0.0.0.0:0", BindOptions{reuse_address: True})!
    port =
        case Socket.local_address(receiver)
            .Ip{host: _, port:} -> port
            .Unix{path: _}      -> 0
        end

    case Socket.join_group(receiver, "239.255.42.99")
        Err(e) ->
            io.print("no group: ${e}")

        Ok(_) ->
            sender = udp.bind("0.0.0.0:0")!
            _ = Socket.set_multicast_loopback(sender, True)
            sent = Socket.send_to(sender, String.to_bytes("hello group"), Address.Ip{host: "239.255.42.99", port:})
            Socket.set_read_deadline(receiver, Some(Instant.now() + Duration.seconds(5)))
            got = Socket.receive(receiver) |> Result.map(fn (data, _) -> Bytes.to_string_lossy(data))
            io.print("multicast ${done(sent)} ${got}")
            io.print("left ${done(Socket.leave_group(receiver, "239.255.42.99"))}")
    end

    io.print("not a group ${done(Socket.join_group(receiver, "10.0.0.1"))}")
    io.print("interface ${done(Socket.set_multicast_interface(receiver, Some("IFACE")))} ${done(Socket.set_multicast_interface(receiver, None))}")
    io.print("no interface ${Socket.set_multicast_interface(receiver, Some("no-such-interface")) |> Result.is_err()}")
end
`

// A datagram sent to a multicast group comes back to a socket on this host
// that joined it, and the interface multicast sends go out of is set by
// name and given back to the system. A machine with no multicast route (no interface that
// carries multicast, as some CI containers have) cannot join or send, and
// the test skips there.
func TestDatagram_MulticastLoopsBack(t *testing.T) {
	iface := multicastInterface()
	if iface == "" {
		t.Skip("no up interface with an IPv4 address carries multicast here")
	}
	got := run(t, strings.ReplaceAll(multicastProgram, "IFACE", iface))
	for _, w := range []string{
		"not a group invalid address 10.0.0.1: not a multicast address",
		"interface ok ok",
		"no interface True",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "no group:") || !strings.Contains(got, "multicast ok Ok(") {
		t.Skipf("this machine has no multicast route: %s", got)
	}
	for _, w := range []string{"multicast ok Ok(hello group)", "left ok"} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

// multicastInterface is the name of an up interface that carries
// multicast and has an IPv4 address, which names it to an IPv4 socket's
// multicast options, or "" for none.
func multicastInterface() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				return i.Name
			}
		}
	}
	return ""
}
