---
title: "HTTP"
description: "Send HTTP requests, write handlers, route requests by checked patterns, serve under a supervisor, and test it all with no network."
---

## Making requests

[`std/http`](/reference/http/) sends requests with Go's HTTP client.
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
    response = http.get(Url"https://api.example.com/greet/${name}"!)?
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
a function instead, as a test does. The `Url"..."` literal escapes each
`${...}` for where it stands, so `ann b` becomes the one path segment
`ann%20b`. A request runs under the context in force, so a deadline or a
cancelled task stops it.

## Handlers and routes

A handler is a plain function from a `Request` to a `Response`. A
[`Router`](/reference/http/#struct-router) picks the handler for each
request by a `Route`, a pattern with the rules of Go's `ServeMux`:
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
