package stdhttp_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// run loads src and runs its main, answering what it printed.
func run(t *testing.T, src string) string {
	t.Helper()
	var errs bytes.Buffer
	p, err := vmhost.LoadSource("main.nomi", src, vmhost.WithErrorOutput(&errs))
	if err != nil {
		t.Fatalf("load: %v\n%s", err, errs.String())
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.Run(ctx, &out, nil, false); err != nil {
		t.Fatalf("run: %v\nstderr: %s\noutput: %s", err, errs.String(), out.String())
	}
	return out.String()
}

// server answers on 127.0.0.1 with a handful of paths the tests ask for.
func server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Agent", r.UserAgent())
		w.Header().Add("X-Many", "a")
		w.Header().Add("X-Many", "b")
		fmt.Fprintf(w, "hello %s", r.URL.Query().Get("name"))
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.Header.Get("Content-Type"), body)
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/hello?name=moved", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	mux.HandleFunc("/post-moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/echo", http.StatusSeeOther)
	})
	mux.HandleFunc("/post-kept", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/echo", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/multipart", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f, fh, err := r.FormFile("doc")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content, _ := io.ReadAll(f)
		fmt.Fprintf(w, "%s %s %s %s", r.FormValue("name"), fh.Filename, fh.Header.Get("Content-Type"), content)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 100))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// A request to a local server answers its status, headers and body, and a
// 404 is a response, not an error.
func TestClient_GetPostAndStatus(t *testing.T) {
	s := server(t)
	got := run(t, strings.ReplaceAll(`import std::http
import std::http.{Body, Headers, Request, Response, Status}
import std::io
import std::json.Json
import std::url.{Query, Url}

fn main() -> Result<Unit, http.Error>
    base = Url.parse("BASE")!
    r = http.fetch(Request{url: Url.join_path(base, ["hello"]) |> Url.with_query(Query[("name", "ann")]), headers: Headers[("User-Agent", "nomi-test")]})?
    io.print(Status.line(r.status))
    io.print(Response.text(r)?)
    io.print(Headers.get(r.headers, "x-seen-agent") |> Maybe.with_default("-"))
    io.print(String.join(Headers.values(r.headers, "X-Many"), ","))
    io.print("${r.protocol}")
    p = http.post(Url.join_path(base, ["echo"]), Body.Json(Json.String("hi")))?
    io.print(Response.text(p)?)
    f = http.post_form(Url.join_path(base, ["echo"]), Query[("a", "1 2")])?
    io.print(Response.text(f)?)
    m = http.get(Url.join_path(base, ["missing"]))?
    io.print("${m.status} ${Status.is_client_error(m.status)}")
    Ok(Unit)
end
`, "BASE", s.URL))
	want := "200 OK\nhello ann\nnomi-test\na,b\nHTTP/1.1\nPOST application/json \"hi\"\nPOST application/x-www-form-urlencoded a=1+2\n404 Not Found True\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Redirects are followed up to the client's limit, or not at all; a
// client's timeout and its max_body answer errors; a refused connection is
// Connect; Client.stream streams the body.
func TestClient_OptionsAndErrors(t *testing.T) {
	s := server(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + l.Addr().String()
	l.Close()
	got := run(t, strings.ReplaceAll(strings.ReplaceAll(`import std::duration.Duration
import std::http
import std::http.{Client, ClientOptions, Error, Redirects, Request, Response, StreamedResponse}
import std::io
import std::url.Url

fn kind(e: Error) -> String
    case e
        .TooManyRedirects{count:} -> "too many ${count}"
        .Timeout{during: _} -> "timeout ${Error.is_timeout(e)}"
        .BodyTooLarge{limit:} -> "too large ${limit}"
        .Connect{address: _, reason: _} -> "connect"
        other -> "other ${other}"
    end
end

fn failure(r: Result<Response, Error>) -> String
    case r
        Ok(response) -> "no error: ${response.status}"
        Err(e) -> kind(e)
    end
end

fn main()
    base = Url.parse("BASE")!
    moved = http.get(Url.join_path(base, ["moved"]))!
    io.print("${moved.status} ${Response.text(moved)!} ${moved.url |> Maybe.map(fn u -> u.path)}")
    never = Client.new(ClientOptions{redirects: Redirects.Never})
    stay = Client.get(never, Url.join_path(base, ["moved"]))!
    io.print("${stay.status} ${Response.location(stay)! |> Maybe.map(fn u -> u.path)}")
    io.print(failure(Client.get(Client.new(ClientOptions{redirects: Redirects.Follow{max: 3}}), Url.join_path(base, ["loop"]))))
    quick = Client.new(ClientOptions{timeout: Some(Duration.milliseconds(50))})
    io.print(failure(Client.get(quick, Url.join_path(base, ["slow"]))))
    small = Client.new(ClientOptions{max_body: Some(10)})
    io.print(failure(Client.get(small, Url.join_path(base, ["big"]))))
    io.print(failure(http.get(Url.parse("CLOSED")!)))
    streamed = Client.stream(small, Request{url: Url.join_path(base, ["big"])})!
    defer StreamedResponse.close(streamed)
    io.print("streamed ${io.Reader.read_all(streamed.body) |> Result.map(Bytes.length)}")
    Client.close_idle(never)
end
`, "BASE", s.URL), "CLOSED", closed))
	want := "200 OK hello moved Some(/hello)\n302 Found Some(/hello)\ntoo many 4\ntimeout True\ntoo large 10\nconnect\nstreamed Ok(100)\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A Redirects.Decide function is asked before each redirect, with the
// request it would send and the requests already sent; it can follow,
// stop at the redirect response, or refuse. A 303 sends a GET with no
// body, and a 307 sends the same method and body again.
func TestClient_RedirectsDecide(t *testing.T) {
	s := server(t)
	got := run(t, strings.ReplaceAll(`import std::http
import std::http.{Body, Client, ClientOptions, Redirect, Redirects, Request, Response, Status}
import std::io
import std::url.Url

fn log_and_follow(next: Request, via: List<Request>) -> Redirect
    io.print("to ${next.method} ${next.url.path} after ${Iter.count(via)}")
    Redirect.Follow
end

fn main() -> Result<Unit, http.Error>
    base = Url.parse("BASE")!
    following = Client.new(ClientOptions{redirects: Redirects.Decide(log_and_follow)})
    r = Client.get(following, Url.join_path(base, ["moved"]))?
    io.print(Response.text(r)?)
    p = Client.post(following, Url.join_path(base, ["post-moved"]), Body.Text("x"))?
    io.print(Response.text(p)?)
    k = Client.post(following, Url.join_path(base, ["post-kept"]), Body.Text("x"))?
    io.print(Response.text(k)?)
    stopping = Client.new(ClientOptions{redirects: Redirects.Decide(fn _, _ -> Redirect.Stop)})
    stopped = Client.get(stopping, Url.join_path(base, ["moved"]))?
    io.print("${Status.code(stopped.status)}")
    refusing = Client.new(ClientOptions{redirects: Redirects.Decide(fn _, via -> if Iter.count(via) < 3 then Redirect.Follow else Redirect.Refuse("too deep"))})
    refused = case Client.get(refusing, Url.join_path(base, ["loop"]))
        Ok(_) -> "followed"
        Err(e) -> "${e}"
    end

    io.print(refused)
    Ok(Unit)
end
`, "BASE", s.URL))
	want := "to GET /hello after 1\nhello moved\nto GET /echo after 1\nGET  \nto POST /echo after 1\nPOST text/plain; charset=utf-8 x\n302\nthe redirect to " + s.URL + "/loop was refused: too deep\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Body.Multipart sends a multipart/form-data form that Go's own parser
// reads: its fields and its files with their names and content types.
func TestClient_MultipartBody(t *testing.T) {
	s := server(t)
	got := run(t, strings.ReplaceAll(`import std::http
import std::http.{Body, FormPart, Response}
import std::io
import std::url.Url

fn main() -> Result<Unit, http.Error>
    parts = [
        FormPart.Field{name: "name", value: "ann"},
        FormPart.File{field: "doc", filename: "a.txt", content_type: "text/plain", content: String.to_bytes("hello")},
    ]

    r = http.post(Url.join_path(Url.parse("BASE")!, ["multipart"]), Body.Multipart(parts))?
    io.print(Response.text(r)?)
    Ok(Unit)
end
`, "BASE", s.URL))
	if want := "ann a.txt text/plain hello\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
