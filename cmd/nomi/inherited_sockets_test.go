package main

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// activated starts `nomi run` on src as a service manager starts a
// socket-activated program: files are its descriptors from 3 on, and
// LISTEN_FDS, LISTEN_FDNAMES and LISTEN_PID say so. A shell sets
// LISTEN_PID to its own id and then execs nomi, which keeps it, since a
// child's id is known only once it has started. The parent's copies of
// files are closed once the child holds them.
func activated(t *testing.T, src, names string, files ...*os.File) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no socket activation: inherited_listeners and inherited_sockets answer an empty list there")
	}
	prog := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `LISTEN_PID=$$ exec "$0" run "$1"`, nomiBin, prog)
	cmd.Env = append(os.Environ(), "LISTEN_FDS="+strconv.Itoa(len(files)), "LISTEN_FDNAMES="+names)
	cmd.ExtraFiles = files
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		_ = f.Close()
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, &out
}

// fileOf is a copy of a socket's descriptor, for a child to inherit; the
// socket itself is closed.
func fileOf(t *testing.T, c interface {
	File() (*os.File, error)
	Close() error
}) *os.File {
	t.Helper()
	f, err := c.File()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	return f
}

// waitFor waits for cmd to end with the exit status code, failing the
// test past a minute.
func waitFor(t *testing.T, cmd *exec.Cmd, out *bytes.Buffer, code int) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		if got := cmd.ProcessState.ExitCode(); got != code {
			t.Fatalf("nomi run exited with %d, not %d:\n%s", got, code, out.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("nomi run did not end:\n%s", out.String())
	}
}

const inheritedProgram = `import std::io.{self, Reader, Writer}
import std::net.{self, Connection, Listener}
import std::udp.{self, Socket}

fn count<T>(found: Result<List<T>, net.Error>) -> String
    found |> Result.map(List.length) |> Debug.inspect()
end

fn main()
    listeners = net.inherited_listeners()!
    io.print("listeners ${List.length(listeners)} web ${count(net.inherited_listeners(name: Some("web")))} stats ${count(net.inherited_listeners(name: Some("stats")))}")
    io.print("sockets ${count(udp.inherited_sockets())} stats ${count(udp.inherited_sockets(name: Some("stats")))}")

    listener = List.first(listeners)!
    conn = Listener.accept(listener)!
    line = Reader.read_line(Connection.reader(conn))! |> Option.with_default("")
    _ = Writer.write_text(Connection.writer(conn), "echo ${line}\n")
    Connection.close(conn)

    socket = List.first(udp.inherited_sockets()!)!
    (data, from) = Socket.receive(socket)!
    Socket.send_to(socket, data, from)!

    Listener.close(listener)
    again = List.first(net.inherited_listeners()!)!
    io.print("again ${Listener.address(again) == Listener.address(listener)} ${Listener.accept(again) |> Result.map(fn _ -> "accepted")}")
end
`

// A program started with a listening TCP socket and a UDP socket, as
// systemd's socket activation starts one, finds each by kind and by name,
// accepts a connection on the listener and answers a datagram on the
// socket. A second call answers the same listener, closed once the first
// is, never a second one over the same descriptor.
func TestInheritedSockets_AnActivatedProgramServesThem(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpAddress := pc.LocalAddr().String()
	cmd, out := activated(t, inheritedProgram, "web:stats",
		fileOf(t, ln.(*net.TCPListener)), fileOf(t, pc.(*net.UDPConn)))

	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || reply != "echo ping\n" {
		t.Fatalf("the inherited listener answered %q, %v:\n%s", reply, err, out.String())
	}

	client, err := net.Dial("udp", udpAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(time.Minute))
	if _, err := client.Write([]byte("datagram")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil || string(buf[:n]) != "datagram" {
		t.Fatalf("the inherited socket answered %q, %v:\n%s", buf[:n], err, out.String())
	}

	waitFor(t, cmd, out, 0)
	for _, w := range []string{
		"listeners 1 web Ok(1) stats Ok(0)",
		"sockets Ok(1) stats Ok(1)",
		"again True Err(the connection is closed)",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
}

const inheritedServerProgram = `import std::http.{self, Body, Request, Response, Server}
import std::net
import std::supervisors.Supervisor

struct App
    web: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{web: Supervisor.new(max_running: 8)})
end

fn show(request: Request) -> Response
    Response{body: Body.Text("inherited ${request.url.path}")}
end

fn main()
    inherited = List.first(net.inherited_listeners(name: Some("web"))!)!
    listener = http.listen(Server{listener: Some(inherited)})!
    http.serve(listener, show, App.web)
end
`

// An HTTP server serves on a listener it was started with, and SIGTERM's
// drain shuts it down as it does one that bound its own address.
func TestInheritedSockets_AnHTTPServerServesAnInheritedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	cmd, out := activated(t, inheritedServerProgram, "web", fileOf(t, ln.(*net.TCPListener)))

	client := &http.Client{Timeout: time.Minute}
	res, err := client.Get("http://" + address + "/hello")
	if err != nil {
		t.Fatalf("GET: %v\n%s", err, out.String())
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "inherited /hello" {
		t.Fatalf("the server answered %q:\n%s", body, out.String())
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// The program drains its supervisors, which shuts the server down,
	// and exits with 128 plus the signal's number.
	waitFor(t, cmd, out, 128+int(syscall.SIGTERM))
	if out.Len() != 0 {
		t.Errorf("the drain printed:\n%s", out.String())
	}
}
