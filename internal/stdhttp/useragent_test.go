package stdhttp_test

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// agentSeen is the User-Agent a request carried, or "none".
func agentSeen(h http.Header) string {
	if v, ok := h["User-Agent"]; ok {
		return fmt.Sprintf("%q", strings.Join(v, ","))
	}
	return "none"
}

// refuser answers every request it reads on a raw listener with a 403 and
// records its method and User-Agent: it stands in for a proxy refusing a
// CONNECT and a server refusing a WebSocket handshake.
func refuser(t *testing.T) (string, func() []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var mu sync.Mutex
	var seen []string
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil {
					return
				}
				mu.Lock()
				seen = append(seen, r.Method+" "+agentSeen(r.Header))
				mu.Unlock()
				fmt.Fprint(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			}()
		}
	}()
	return l.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// A request that names no User-Agent sends nomi's, never Go's, over
// HTTP/1.1, HTTP/2 over TLS and unencrypted HTTP/2, after a redirect, in a
// proxy's CONNECT, in a WebSocket handshake and as Request.write_to and
// dump_outgoing write it. One that sets it sends exactly that, and one
// that sets it to "" sends none. A reverse proxy forwards a request that
// came with none with none.
func TestUserAgent_NomiNamesItselfOrNothing(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/ua", http.StatusFound)
			return
		}
		fmt.Fprint(w, agentSeen(r.Header))
	}))
	defer backend.Close()
	proxyAddr, proxySeen := refuser(t)
	wsAddr, wsSeen := refuser(t)
	src := `import std::http.{self, Client, ClientOptions, Headers, Proxy, Request, Response, ReverseProxy, Transport}
import std::http::testing
import std::io
import std::url.Url
import std::websocket.{self, ConnectOptions}

fn agent(r: Request) -> Response
    Response{body: http.Body.Text("v${r.protocol.major} ${Headers.get(r.headers, "User-Agent")}")}
end

fn fetched(request: Request) -> String
    Response.text(http.fetch(request)!)!
end

fn main()
    base = Url.parse("BACKEND")!
    ua = Url.resolve_text(base, "/ua")!
    moved = Url.resolve_text(base, "/moved")!
    io.print("default ${fetched(Request{url: ua})}")
    io.print("set ${fetched(Request{url: ua, headers: Headers[("User-Agent", "probe/2")]})}")
    io.print("empty ${fetched(Request{url: ua, headers: Headers[("User-Agent", "")]})}")
    io.print("redirect default ${fetched(Request{url: moved})}")
    io.print("redirect empty ${fetched(Request{url: moved, headers: Headers[("User-Agent", "")]})}")

    testing.with_server(agent, tls: True, http2: True, fn server ->
        io.print("h2 default ${Response.text(Client.get(server.client, server.url)!)!}")
        empty = Request{url: server.url, headers: Headers[("User-Agent", "")]}
        io.print("h2 empty ${Response.text(Client.fetch(server.client, empty)!)!}")
    end)
    testing.with_server(agent, http2: True, fn server ->
        io.print("h2c default ${Response.text(Client.get(server.client, server.url)!)!}")
    end)

    proxy = Proxy.Fixed(Url.parse("http://PROXY")!)
    target = Url.parse("https://tunnel.invalid/")!
    _ = Client.get(Client.new(ClientOptions{transport: {..Transport.default, proxy:}}), target)
    _ = Client.get(Client.new(ClientOptions{transport: {..Transport.default, proxy:, proxy_connect_headers: Headers[("User-Agent", "")]}}), target)

    socket = Url.parse("ws://WEBSOCKET/")!
    _ = websocket.connect(socket)
    _ = websocket.connect(socket, ConnectOptions{headers: Headers[("User-Agent", "")]})

    outgoing = Request.dump_outgoing(Request{url: ua})!
    io.print("dump_outgoing ${String.contains(outgoing, "User-Agent: nomi\r\n")}")
    received = Request.dump(Request{url: ua})!
    io.print("dump ${String.contains(received, "User-Agent")}")

    forward = http.reverse_proxy(ReverseProxy.to(base))
    testing.with_server(forward, fn server ->
        none = Request{url: Url.resolve_text(server.url, "/ua")!, headers: Headers[("User-Agent", "")]}
        io.print("proxied none ${Response.text(Client.fetch(server.client, none)!)!}")
        io.print("proxied default ${Response.text(Client.get(server.client, Url.resolve_text(server.url, "/ua")!)!)!}")
    end)
end
`
	got := run(t, strings.NewReplacer("BACKEND", backend.URL, "PROXY", proxyAddr, "WEBSOCKET", wsAddr).Replace(src))
	want := strings.Join([]string{
		`default "nomi"`,
		`set "probe/2"`,
		`empty none`,
		`redirect default "nomi"`,
		`redirect empty none`,
		`h2 default v2 Some(nomi)`,
		`h2 empty v2 None`,
		`h2c default v2 Some(nomi)`,
		`dump_outgoing True`,
		`dump False`,
		`proxied none none`,
		`proxied default "nomi"`,
	}, "\n") + "\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, c := range []struct {
		name string
		seen []string
		want string
	}{
		{"CONNECT", proxySeen(), `CONNECT "nomi"|CONNECT none`},
		{"WebSocket handshake", wsSeen(), `GET "nomi"|GET none`},
	} {
		if strings.Join(c.seen, "|") != c.want {
			t.Errorf("%s: saw %q, want %q", c.name, c.seen, c.want)
		}
	}
}
