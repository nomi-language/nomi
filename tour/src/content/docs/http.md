---
title: "HTTP"
description: "Send HTTP requests, write handlers, route requests by checked patterns, serve under a supervisor, and test it all with no network."
---

## Making requests

[`std/http`](/reference/http/) sends HTTP requests and answers them.
`http.get` fetches a URL and answers a `Response` with its whole body read,
`http.post` sends a body, and `http.fetch` sends a `Request` you build. A
status is not an error: a 404 is a `Response` like a 200, and
`Status.is_success` says which it is. An `Err` means no response arrived,
such as a refused connection or a timeout:

```nomi-run
import {
    std/http.{self, Response, Status}
    std/http_testing
    std/url.Url
}

fn greeting(name: String) -> Result<String, http.Error> {
    response = http.get(Url"https://api.example.com/greet/${name}")?
    if not Status.is_success(response.status) { return Ok("no greeting: ${response.status}") }
    Response.text(response)
}

fn main() {
    ran = http_testing.run(fn request -> {
        Ok(Response{body: .Text("hello from ${request.url.path}")})
    }, fn -> {
        greeting("ann b")
    })

    dbg ran.value
}
```
<!-- expect
dbg line 20: ran.value = Ok("hello from /greet/ann b")
-->

The page cannot reach the network, so `main` runs `greeting` under
[`http_testing`](/reference/http_testing/), which answers each request with
a function instead, as a test does. `Url"..."` is a [checked
literal](/typed-literals/#checked-literals): it is a `Url`, not a `Result`,
and it escapes each `${...}` for where it stands. `ann b` goes out as the
path segment `ann%20b`, and the handler's `request.url.path` reads it back
decoded. A request runs under the context in force, so a deadline or a
cancelled task stops it.

## Handlers and routes

A handler is a plain function from a `Request` to a `Response`. A
[`Router`](/reference/http/#struct-router) picks the handler for each
request by a `Route`, a method and a path pattern:
``Route`GET /users/{id}` `` matches a `GET` of `/users/7`, and
`Request.path_value` reads what `{id}` matched. A request no route matches
gets a 404, and one that matches only under another method a 405. Since a
handler is a function, a test calls it, or the router, with a request and
no network:

```nomi-run
import {
    std/http.{Request, Response, Route, Router}
    std/http_testing
}

fn show(request: Request) -> Response {
    id = Request.path_value(request, "id") |> Maybe.with_default("?")
    Response{body: .Text("user ${id}")}
}

fn main() {
    router = Router.new([#(Route`GET /users/{id}`, show)])!
    found = Router.serve(router, http_testing.request(.Get, "/users/7"))
    dbg Response.text(found)
    missing = Router.serve(router, http_testing.request(.Get, "/teams/1"))
    dbg missing.status
}
```
<!-- expect
dbg line 14: Response.text(found) = Ok("user 7")
dbg line 16: missing.status = Status(404)
-->

A route is checked when the program is checked, so a pattern Go would
refuse is a compile error at the literal:

```nomi-run error
import std/http.Route

fn main() {
    dbg Route`GET /users/{id}/posts/{id}`
}
```
<!-- expect
main.nomi:4:9: typed literal Route`GET /users/{id}/posts/{id}` is invalid: pattern GET /users/{id}/posts/{id}: at offset 22: duplicate wildcard name "id"
-->

`Router.new` still answers an `Err` for two routes that match the same
requests, since each literal sees only its own pattern. `Route.parse`
checks a pattern built at run time.

A handler answers with a page through `Response.html`, which takes an
[`Html`](/reference/html/). Write one with an `Html"..."` literal, an HTML
template the compiler checks. Each `${...}` is escaped for where it stands,
so the `<nobody>` below arrives as text, and an unclosed tag or a slot in a
`<script>` is a compile error. A one-line fragment quotes its attributes
with `'`, since a `"` would end the literal. Markup of several lines goes in
triple quotes:

```nomi-run
import {
    std/html.Html
    std/http.{Request, Response}
    std/http_testing
}

fn badge(count: Int) -> Html {
    Html"<span class='badge'>${count}</span>"
}

fn profile(request: Request) -> Response {
    name = Request.path_value(request, "name") |> Maybe.with_default("<nobody>")

    page = Html"""
        <main>
          <h1>${name} ${badge(3)}</h1>
        </main>
        """

    Response.html(page)
}

fn main() {
    dbg Response.text(profile(http_testing.request(.Get, "/")))
}
```
<!-- expect
dbg line 24: Response.text(profile(http_testing.request(.Get, "/"))) = Ok("<main>\n  <h1>&lt;nobody&gt; <span class='badge'>3</span></h1>\n</main>")
-->

What a slot in an attribute, a URL or a `style` takes is in the spec's
[HTML templates](/spec/typed-literals-and-keyword-declarations/#html-templates).

## Serving

`http.listen` binds the server's address, and `http.serve` answers each
request as a task of a [supervisor](/background-work/), so its
`max_running` bounds the requests answered at once. Each handler runs
under its request's context, and its task stops if the client goes away.
When the program shuts down, at a signal or when `main` returns, the
server stops accepting at once, and the requests it already took finish
within its `shutdown_timeout`:

```nomi-run compile-only
import {
    std/http.{self, Request, Response, Route, Router, Server}
    std/supervisors.Supervisor
}

struct App {
    web: Supervisor
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(App{web: Supervisor.new(max_running: 256)})
}

fn show(request: Request) -> Response {
    id = Request.path_value(request, "id") |> Maybe.with_default("?")
    Response{body: .Text("user ${id}")}
}

fn main() -> Result<Unit, http.Error> {
    router = Router.new([#(Route`GET /users/{id}`, show)])?
    listener = http.listen(Server{address: ":8080"})?
    http.serve(listener, Router.handler(router), App.web)
    Ok(Unit)
}
```

The page cannot listen on a port, so this one does not run here.
`nomi run` serves it on port 8080.

## Storing files

Uploaded files go in a [`Store`](/reference/storage/), which keeps each
file whole under a key. `storage.local(root)` keeps them in a directory,
`lib/storage/s3` in a bucket on any S3-protocol server, and
`storage_testing.Memory` in memory for tests. Code that stores files takes
a `Store`, so boot picks the backend and the rest of the code doesn't
change. `Store.url` answers where a file can be fetched, and
`storage.handler` serves a store's files through the app with their stored
content type:

```nomi-run
import {
    std/http.{Headers, Route, Router}
    std/http_testing
    std/storage.{self, Store}
    std/storage_testing.Memory
    std/url.Url
}

fn save_avatar(store: Store, user: Int, image: Bytes) -> Result<Url, storage.Error> {
    key = "avatars/${user}.png"
    Store.put(store, key, image, "image/png")?
    Store.url(store, key)
}

fn main() {
    store = Memory.new(public_base: Url`/files/`)
    dbg "${save_avatar(store, 7, String.to_bytes("png"))!}"

    router = Router.new([#(Route`GET /files/{key...}`, storage.handler(store))])!
    response = Router.serve(router, http_testing.request(.Get, "/files/avatars/7.png"))
    dbg Headers.get(response.headers, "Content-Type")
}
```
<!-- expect
dbg line 17: "${save_avatar(store, 7, String.to_bytes("png"))!}" = "/files/avatars/7.png"
dbg line 21: Headers.get(response.headers, "Content-Type") = Some("image/png")
-->

A key is ASCII letters, digits, `-`, `_`, `.` and `~` in segments
separated by `/`, and no segment starts with a dot. So a key taken from a
request cannot reach outside a local store's directory.

## Testing against a real server

A test that needs a real connection, for a client's redirects, cookies or
timeouts, runs a server with `http_testing.with_server`. It answers with a
handler on a free port of 127.0.0.1 while its body runs, then shuts down:

```nomi-run compile-only
import {
    std/http.{Client, Request, Response}
    std/http_testing
}

fn hello(_request: Request) -> Response {
    Response{body: .Text("hi")}
}

test "a client reaches the server" {
    said = http_testing.with_server(hello, fn server -> {
        Client.get(server.client, server.url) |> Result.map(Response.text)
    })

    assert said == Ok(Ok("hi"))
}
```

## HTTPS

`Server{address: ":8443", tls: Some(ServerConfig{certificates: [certificate]})}`
serves HTTPS, with a [`tls.Certificate`](/reference/tls/) loaded from PEM
files. `with_server(handler, tls: True, ...)` makes a certificate for the
test, and `server.client` trusts it.

[Background Work](/background-work/) is next. It keeps work running after
the call that started it.
