package stdhttp_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// http.reverse_proxy forwards to a Go server with X-Forwarded-For set,
// the target's path joined before the request's and hop-by-hop headers
// dropped, inside http_testing.with_server serving HTTPS with a
// certificate made for it.
func TestReverseProxy_ForwardsThroughATestServer(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		fmt.Fprintf(w, "%s %s fwd=%q host=%q conn=%q", r.Method, r.URL.RequestURI(), r.Header.Get("X-Forwarded-For"),
			r.Header.Get("X-Forwarded-Host"), r.Header.Get("Keep-Alive"))
	}))
	defer backend.Close()
	got := run(t, strings.ReplaceAll(`import std::http.{self, Client, Headers, Request, Response, ReverseProxy}
import std::http_testing
import std::io
import std::url.Url

fn main() {
    proxy = http.reverse_proxy(ReverseProxy.to(Url.parse("BACKEND/base")!))
    http_testing.with_server(proxy, tls: True, fn server -> {
        request = Request{url: Url.resolve_text(server.url, "/items?x=1")!, headers: Headers.set(Headers[], "Keep-Alive", "5")}
        response = Client.fetch(server.client, request)!
        io.print("${server.url.scheme} ${Response.text(response)!} ${Headers.get(response.headers, "Connection")}")
        io.print("cert ${server.certificate |> Maybe.map(fn c -> c.dns_names)}")
    })
}
`, "BACKEND", backend.URL))
	for _, w := range []string{
		`https GET /base/items?x=1 fwd="127.0.0.1" host="127.0.0.1:`,
		`conn="" None`,
		`cert Some([localhost])`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

// The proxy streams: a backend that writes, flushes and then waits for the
// client to have read the first line is answered through the proxy line by
// line, which a proxy reading the body whole would never finish. A failed
// upstream reaches on_error_log, and the test server speaks HTTP/2 when
// asked, plain (h2c) or over TLS, and closes its client connections.
func TestReverseProxy_StreamsAndLogs(t *testing.T) {
	first := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "one\n")
		w.(http.Flusher).Flush()
		<-first
		fmt.Fprint(w, "two\n")
	}))
	defer backend.Close()
	unblock := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(first) }))
	defer unblock.Close()
	got := run(t, strings.ReplaceAll(strings.ReplaceAll(`import std::http.{self, Client, Request, Response, ReverseProxy, StreamedResponse}
import std::http_testing
import std::http_testing.TestServer
import std::io.{self, Reader}
import std::url.Url

fn main() {
    proxy = http.reverse_proxy(ReverseProxy.to(Url.parse("BACKEND")!))
    http_testing.with_server(proxy, fn server -> {
        response = Client.stream(server.client, Request{url: server.url})!
        defer StreamedResponse.close(response)

        body = response.body
        line = Reader.read_line(body)
        io.print("first ${line}")
        _ = http.get(Url.parse("UNBLOCK")!)
        io.print("second ${Reader.read_line(body)}")
    })

    broken = http.reverse_proxy(#{..ReverseProxy.to(Url.parse("http://127.0.0.1:1")!), on_error_log: Some(fn text: String -> io.print("logged ${String.contains(text, "refused")}"))})
    http_testing.with_server(broken, fn server -> {
        io.print("broken ${Client.get(server.client, server.url) |> Result.map(fn r -> r.status)}")
    })

    http_testing.with_server(fn r -> Response{body: http.Body.Text("v${r.protocol.major}")}, http2: True, fn server -> {
        io.print("h2c ${Client.get(server.client, server.url) |> Result.map(fn r -> Response.text(r)!)}")
    })

    http_testing.with_server(fn r -> Response{body: http.Body.Text("v${r.protocol.major}")}, tls: True, http2: True, fn server -> {
        io.print("h2 ${Client.get(server.client, server.url) |> Result.map(fn r -> Response.text(r)!)}")
        TestServer.close_client_connections(server)
        // The client still pools the connection the server just closed; a
        // request on it races the client noticing, so drop it first and
        // the next request dials a new one.
        Client.close_idle(server.client)
        io.print("after close ${Client.get(server.client, server.url) |> Result.map(fn r -> Response.text(r)!)}")
    })

    http_testing.with_server(fn r -> Response{body: http.Body.Text("v${r.protocol.major}")}, tls: True, fn server -> {
        io.print("h1 ${Client.get(server.client, server.url) |> Result.map(fn r -> Response.text(r)!)}")
    })
}
`, "UNBLOCK", unblock.URL), "BACKEND", backend.URL))
	for _, w := range []string{
		"first Ok(Some(one))",
		"second Ok(Some(two))",
		"logged True",
		"broken Ok(502 Bad Gateway)",
		"h2c Ok(v2)",
		"h2 Ok(v2)",
		"after close Ok(v2)",
		"h1 Ok(v1)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}
