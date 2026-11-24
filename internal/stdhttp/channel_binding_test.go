package stdhttp_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/strlit"
)

const channelBindingProgram = `import std::hex
import std::io.{self, Reader, Writer}
import std::net.{self, Connection}
import std::supervisors.Supervisor
import std::tls.{self, Certificate, ChannelBinding, ClientConfig, Roots, ServerConfig, Version}

struct App
    web: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{web: Supervisor.new(max_running: 8)})
end

fn shown(result: Result<Bytes, String>) -> String
    case result
        Ok(data)    -> hex.encode(data)
        Err(reason) -> "error: ${reason}"
    end
end

fn bindings(conn: Connection) -> String
    end_point = shown(tls.channel_binding(conn, ChannelBinding.ServerEndPoint))
    exporter = shown(tls.channel_binding(conn, ChannelBinding.Exporter))
    plain = shown(tls.export_keying_material(conn, "EXPERIMENTAL-nomi", None, 16))
    empty = shown(tls.export_keying_material(conn, "EXPERIMENTAL-nomi", Some(Bytes.from_list([])), 16))
    "${end_point}|${exporter}|${plain}|${empty}"
end

fn answer(conn: Connection) -> Unit
    _ = Reader.read_line(Connection.reader(conn))!
    _ = Writer.write_text(Connection.writer(conn), "${bindings(conn)}\n")
end

fn over(version: Version, certificate: Certificate, trusted: ClientConfig) -> Unit
    server = ServerConfig{certificates: [certificate], max_version: Some(version)}
    secure = tls.listen("127.0.0.1:0", server)!
    _ = Supervisor.spawn(App.web, fn -> net.serve(secure, answer, App.web))
    conn = tls.dial(net.Listener.address(secure), trusted)!
    _ = Writer.write_text(Connection.writer(conn), "go\n")
    io.print("${version}|server|${Reader.read_line(Connection.reader(conn))! |> Option.with_default("-")}")
    io.print("${version}|client|${bindings(conn)}")
    Connection.close(conn)
    net.Listener.close(secure)
end

fn main()
    certificate = Certificate.load("CERT", "KEY")!
    trusted = ClientConfig{roots: Roots.Files(["CERT"])}
    over(Version.Tls13, certificate, trusted)
    over(Version.Tls12, certificate, trusted)

    plain = net.listen("127.0.0.1:0")!
    conn = net.dial(net.Listener.address(plain))!
    io.print("plain ${shown(tls.channel_binding(conn, ChannelBinding.ServerEndPoint))}")
    io.print("plain ${shown(tls.export_keying_material(conn, "EXPERIMENTAL-nomi", None, 16))}")
    Connection.close(conn)
    net.Listener.close(plain)
end
`

// Both ends of a TLS connection compute the same channel bindings and
// exported keying material: tls-server-end-point is the SHA-256 of the
// server's certificate (an ECDSA-with-SHA-256 one), from the peer's on the
// client and from its own on the server; tls-exporter is there over TLS
// 1.3 and refused over TLS 1.2; an export with no context and one with an
// empty context agree over TLS 1.3, where RFC 8446 makes them one, and
// differ over TLS 1.2; and a connection that is not TLS has none.
func TestTLS_ChannelBindingsAgreeOnBothSides(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := selfSigned(t, dir)
	src := strings.NewReplacer(
		`"CERT"`, `"`+strlit.EncodeStringBody(certPath)+`"`,
		`"KEY"`, `"`+strlit.EncodeStringBody(keyPath)+`"`,
	).Replace(channelBindingProgram)
	got := run(t, src)

	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	sum := sha256.Sum256(block.Bytes)
	endPoint := hex.EncodeToString(sum[:])

	lines := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if parts := strings.Split(line, "|"); len(parts) == 6 {
			lines[parts[0]+" "+parts[1]] = parts[2:]
		}
	}
	for _, version := range []string{"TLS 1.3", "TLS 1.2"} {
		server, client := lines[version+" server"], lines[version+" client"]
		if len(server) != 4 || strings.Join(server, " ") != strings.Join(client, " ") {
			t.Fatalf("%s: the server computes %v and the client %v\n%s", version, server, client, got)
		}
		if server[0] != endPoint {
			t.Errorf("%s: tls-server-end-point is %s, want the certificate's SHA-256 %s", version, server[0], endPoint)
		}
		if len(server[2]) != 32 || len(server[3]) != 32 {
			t.Errorf("%s: an export of 16 bytes is %s and %s", version, server[2], server[3])
		}
	}
	if x := lines["TLS 1.3 server"]; len(x[1]) != 64 || x[2] != x[3] {
		t.Errorf("TLS 1.3: tls-exporter %s, exports %s and %s, want 32 bytes and two equal exports", x[1], x[2], x[3])
	}
	if x := lines["TLS 1.2 server"]; x[1] != "error: tls-exporter needs TLS 1.3, and the connection is TLS 1.2" {
		t.Errorf("TLS 1.2's tls-exporter is not refused:\n%s", got)
	}
	if x := lines["TLS 1.2 server"]; x[2] == x[3] {
		t.Errorf("TLS 1.2: no context and an empty context export the same %s", x[2])
	}
	for _, want := range []string{"plain error: the connection is not TLS\nplain error: the connection is not TLS"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}
