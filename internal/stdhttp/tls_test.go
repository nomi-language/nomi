package stdhttp_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selfSigned writes a certificate for 127.0.0.1 and localhost, made now
// and valid for an hour, and its key, under dir; it answers their paths.
func selfSigned(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(42),
		Subject:               pkix.Name{CommonName: "nomi test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

const tlsProgram = `import std::http
import std::http.{Body, Client, ClientOptions, Listener, Request, Response, Server, Status}
import std::io.{self, Reader, Writer}
import std::net.{self, Connection}
import std::supervisors.Supervisor
import std::tls.{self, Certificate, ClientConfig, Roots, ServerConfig, Version}
import std::url.Url
import std::duration.Duration
import std::instant.Instant

struct App
    web: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{web: Supervisor.new(max_running: 8)})
end

fn hello(request: Request) -> Response
    Response{body: Body.Text("secure ${request.url.path}")}
end

fn echo(conn: Connection) -> Unit
    line = Reader.read_line(Connection.reader(conn))! |> Maybe.with_default("")
    _ = Writer.write_text(Connection.writer(conn), "echo ${line}\n")
end

fn main()
    certificate = Certificate.load("CERT", "KEY")!
    io.print("subject ${Certificate.info(certificate).subject} ${Certificate.info(certificate).dns_names}")
    trusted = ClientConfig{roots: Roots.Files(["CERT"])}

    listener = http.listen(Server{address: "127.0.0.1:0", tls: Some(ServerConfig{certificates: [certificate]})})!
    _ = Supervisor.spawn(App.web, fn -> http.serve(listener, hello, App.web))
    base = "https://${Listener.address(listener)}"
    client = Client.new(ClientOptions{tls: Some(trusted)})
    response = Client.get(client, Url.parse("${base}/hi")!)!
    io.print("https ${Status.code(response.status)} ${Response.text(response)!} HTTP/${response.protocol.major}")
    untrusted = http.get(Url.parse("${base}/hi")!)
    io.print("default roots ${Result.is_err(untrusted)}")
    Listener.shutdown(listener)

    plain = net.listen("127.0.0.1:0")!
    _ = Supervisor.spawn(App.web, fn -> net.serve(plain, echo, App.web))
    conn = net.dial(net.Listener.address(plain))!
    _ = Writer.write_text(Connection.writer(conn), "ping\n")
    Connection.close_write(conn)
    Connection.set_deadline(conn, None)
    io.print("tcp ${Reader.read_line(Connection.reader(conn))! |> Maybe.with_default("-")}")
    io.print("addresses ${Connection.local_address(conn) != ""} ${Connection.remote_address(conn) == net.Listener.address(plain)}")
    Connection.close(conn)
    net.Listener.close(plain)

    starttls = net.listen("127.0.0.1:0")!
    _ = Supervisor.spawn(App.web, fn -> net.serve(starttls, fn c -> echo(tls.server(c, ServerConfig{certificates: [certificate]})!), App.web))
    upgraded = tls.client(net.dial(net.Listener.address(starttls))!, {..trusted, server_name: Some("localhost")})!
    _ = Writer.write_text(Connection.writer(upgraded), "starttls\n")
    io.print("upgraded ${Reader.read_line(Connection.reader(upgraded))! |> Maybe.with_default("-")} ${Iter.count(tls.state(upgraded)!.verified_chains)}")
    Connection.close(upgraded)
    net.Listener.close(starttls)
    io.print("bad pem ${Certificate.from_pem("x", "y") |> Result.is_err()}")

    tuned = net.listen("127.0.0.1:0", net.ListenOptions{keep_alive: net.KeepAlive{enabled: False}})!
    tuned_address = net.Listener.address(tuned)
    net.Listener.set_deadline(tuned, Some(Instant.now()))
    io.print("accept deadline ${net.Listener.accept(tuned) |> Result.map(fn _ -> "accepted") |> Result.map_err(net.Error.is_timeout)}")
    net.Listener.set_deadline(tuned, None)
    client_conn = net.dial(tuned_address, net.DialOptions{keep_alive: net.KeepAlive{interval: Some(Duration.seconds(5))}})!
    Connection.set_no_delay(client_conn, False)
    Connection.set_linger(client_conn, Some(Duration.seconds(0)))
    Connection.set_keep_alive(client_conn, net.KeepAlive{idle: Some(Duration.seconds(30))})
    Connection.close_read(client_conn)
    io.print("tuned ${Connection.is_multipath(client_conn)}")
    Connection.close(client_conn)
    net.Listener.close(tuned)
    io.print("refused ${net.dial(tuned_address) |> Result.map(fn _ -> "connected")}")

    secure = tls.listen("127.0.0.1:0", ServerConfig{certificates: [certificate], next_protocols: ["echo/1"]})!
    _ = Supervisor.spawn(App.web, fn -> net.serve(secure, echo, App.web))
    sconn = tls.dial(net.Listener.address(secure), {..trusted, next_protocols: ["echo/1"]})!
    _ = Writer.write_text(Connection.writer(sconn), "pong\n")
    io.print("tls ${Reader.read_line(Connection.reader(sconn))! |> Maybe.with_default("-")}")
    state = tls.state(sconn)!
    io.print("state ${state.version} ${state.protocol} ${state.handshake_complete} ${Iter.count(state.peer_certificates)}")
    io.print("hostname ${tls.verify_hostname(sconn, "localhost") |> Result.is_ok()} ${tls.verify_hostname(sconn, "example.com") |> Result.is_ok()}")
    Connection.close(sconn)
    refused = tls.dial(net.Listener.address(secure))
    io.print("refused ${refused |> Result.is_err()}")
    net.Listener.close(secure)
    io.print("plain state ${tls.state(conn) |> Maybe.is_some()} ${Version.Tls12}")
end
`

// HTTPS, TCP and TLS over TCP end to end on 127.0.0.1: a server with a
// certificate made for the test, a client that trusts it and one that does
// not, a TCP echo through net.serve, and a TLS echo through tls.listen and
// tls.dial with ALPN, its connection state and hostname checks.
func TestTLS_HTTPSAndTCPEndToEnd(t *testing.T) {
	certPath, keyPath := selfSigned(t, t.TempDir())
	src := strings.ReplaceAll(strings.ReplaceAll(tlsProgram, `"CERT"`, `"`+certPath+`"`), `"KEY"`, `"`+keyPath+`"`)
	got := run(t, src)
	for _, w := range []string{
		"subject CN=nomi test [localhost]",
		"https 200 secure /hi HTTP/2",
		"default roots True",
		"tcp echo ping",
		"addresses True True",
		"upgraded echo starttls 1",
		"bad pem True",
		"accept deadline Err(True)",
		"tuned False",
		"refused Err(connection refused by 127.0.0.1:",
		"tls echo pong",
		"state TLS 1.3 Some(echo/1) True 1",
		"hostname True False",
		"refused True",
		"plain state False TLS 1.2",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
