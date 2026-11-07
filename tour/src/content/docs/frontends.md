---
title: "Frontends"
description: "Build the frontend of a Nomi app: server-rendered HTML templates, a React or Vue app over a Nomi JSON API, or Nomi compiled to WebAssembly in the page."
---

There are three ways to build the pages of a Nomi web app.

**Pages rendered on the server.** Handlers return `Html` built from
checked templates, and the browser gets finished pages. There is no build
step and no JavaScript toolchain. Pick this when your pages are mostly
content and forms. A page can still include whatever JavaScript it needs,
from a few lines of your own to a library such as htmx or Stimulus,
served as static files beside the pages.

**A JavaScript frontend.** React, Vue, Svelte or any other framework runs
in the browser, built by Vite, webpack or any bundler, and Nomi serves its
JSON API and its built files from one executable. Pick this when the interface holds a lot of
client-side state, or when your team already works in one of these
frameworks.

**Nomi in the page.** `nomi build --web` compiles a Nomi program to
WebAssembly, and a page runs it or calls its functions. Pick this for logic
you want to run on the client, such as a calculation the server already
does that the page should repeat offline. The download is the program and
the VM together, about 2 MB with brotli for hello world.

## Pages rendered on the server

A component is a function that returns `Html`. Below, `layout` wraps any
page's content, and `signup_form` draws the form. The form posts to
`/signup`, and its handler reads the field with `Request.form_value`. When
the input is wrong, the handler draws the form again with the error. When
it is right, it redirects with a 303, so reloading the next page does not
post the form twice. The router sits behind `http.cross_origin_protection`,
which refuses a form another site posts. `main` sends two posts through
them without a network, one good and one bad, to show the responses:

```nomi-run
import {
    std::html.Html
    std::http.{self, CrossOrigin, Request, Response, Route, Router, Status}
    std::http_testing
}

fn layout(title: String, content: Html) -> Html {
    Html"""
    <html>
      <head>
        <title>${title}</title>
        <script src='/static/htmx.min.js' defer></script>
      </head>
      <body>${content}</body>
    </html>
    """
}

fn signup_form(email: String, error: Maybe<String>) -> Html {
    Html"""
    <form method='post' action='/signup'>
      <input name='email' value='${email}'>
      ${Maybe.map(error, fn e -> Html"<p class='error'>${e}</p>")}
      <button>Sign up</button>
    </form>
    """
}

fn show_signup(Request) -> Html {
    layout("Sign up", signup_form("", None))
}

fn signup(request: Request) -> Response {
    email = Request.form_value(request, "email") |> Maybe.with_default("")

    if not String.contains(email, "@") {
        page = layout("Sign up", signup_form(email, Some("That is not an email address.")))
        return #{..Response.html(page), status: Status.unprocessable_entity}
    }

    Response.redirect(request, "/welcome", Status.see_other)
}

fn main() {
    router = Router.new([#(Route`GET /signup`, show_signup), #(Route`POST /signup`, signup)])!
    app = Router.handler(router) |> http.cross_origin_protection(CrossOrigin{})
    post = fn email: String -> app(http_testing.form_request(.Post, "/signup", [#("email", email)]))

    dbg post("ada@example.com").status
    bad = post("<ada>")
    dbg bad.status
    dbg Response.text(bad)
}
```
<!-- expect
dbg line 49: post("ada@example.com").status = Status(303)
dbg line 51: bad.status = Status(422)
dbg line 52: Response.text(bad) = Ok("<html>\n  <head>\n    <title>Sign up</title>\n    <script src='/static/htmx.min.js' defer></script>\n  </head>\n  <body><form method='post' action='/signup'>\n  <input name='email' value='&lt;ada&gt;'>\n  <p class='error'>That is not an email address.</p>\n  <button>Sign up</button>\n</form></body>\n</html>")
-->

Each `${...}` is escaped for where it stands, so the `<ada>` typed into the
form comes back as text in the `value` attribute, not as a tag. A `${...}`
holding a `Maybe` shows its value, or nothing when it is `None`. Static
files such as the htmx script and a stylesheet go in a folder the program
embeds and serves with `http.file_server_embedded`, as the next section
does with a JavaScript build.
A form that edits or deletes posts a hidden `_method` field of `PATCH`
or `DELETE`, and a router wrapped in `http.method_override()` routes it
as that method.
[HTTP](/http/#handlers-and-routes) covers templates, routes and errors in
depth, and [sessions](/http/#sessions) keep a signed-in user across pages.

## A JavaScript frontend

Only a static build can be embedded: a single-page app or a framework's
static export. Next, Nuxt, SvelteKit or Remix in server-rendering mode
need Node while they run, so they run beside the Nomi program, not inside
it.

Keep the frontend in a `web/` folder beside the Nomi code. Its bundler
writes the built files into `web/dist`, and the Nomi program embeds that
folder. The example is a React app built with Vite; the end of this
section gives the settings for other tools.

```tree
todo/
├── app.nomi
├── api.nomi
└── web/
    ├── package.json
    ├── vite.config.ts
    ├── src/
    │   ├── api.ts
    │   └── main.tsx
    └── dist/
```

`app.nomi` holds the routes, the serving and `main`, and `api.nomi` the
JSON types and their handlers. `nomi types` writes `web/src/api.ts`, and
`npm run build` writes `web/dist`.

### The API

An API handler returns a response with a `Json` body. A type the frontend reads
derives `ToJson`, and is `pub` so that `nomi types` declares it:

```nomi-run
import {
    std::http.{Request, Response}
    std::http_testing
    std::json.ToJson
}

pub struct Todo {
    id: Int
    title: String
    done: Bool
}

derive ToJson for Todo

pub fn list_todos(Request) -> Response {
    todos = [
        Todo{id: 1, title: "Write the API", done: True},
        Todo{id: 2, title: "Write the page", done: False},
    ]

    Response{body: .Json(ToJson.to_json(todos))}
}

fn main() {
    dbg Response.text(list_todos(http_testing.request(.Get, "/api/todos")))
}
```
<!-- expect
dbg line 25: Response.text(list_todos(http_testing.request(.Get, "/api/todos"))) = Ok("[{\"id\":1,\"title\":\"Write the API\",\"done\":true},{\"id\":2,\"title\":\"Write the page\",\"done\":false}]")
-->

### Shared types

`nomi types` reads the Nomi files you name and writes a TypeScript
declaration for each `pub` type in them that derives `ToJson` or
`FromJson`, and for every type those reach.
`nomi types api.nomi -o web/src/api.ts` writes:

```ts
export interface Todo {
  id: number;
  title: string;
  done: boolean;
}
```

Run it from the npm scripts that start and build the frontend, so
`api.ts` is rewritten from the Nomi code each time. After a Nomi type changes, the
next `npm run types`, `dev` or `build` rewrites `api.ts`; your editor then
shows the TypeScript code that no longer fits, and `npm run build` fails
on it. npm runs scripts from inside `web/`, so the path goes up a level:

```json
{
  "scripts": {
    "types": "nomi types ../api.nomi -o src/api.ts",
    "dev": "npm run types && vite",
    "build": "npm run types && tsc -b && vite build"
  }
}
```

The frontend imports the types and calls the API with a plain `fetch`:

```ts
import type { Todo } from "./api";

export async function listTodos(): Promise<Todo[]> {
  const response = await fetch("/api/todos");
  if (!response.ok) throw new Error(`GET /api/todos: ${response.status}`);
  return response.json();
}
```

The [tooling reference](/tooling/commands/#typescript-types-nomi-types)
says how each Nomi type maps to TypeScript.

### Developing

Run the two servers side by side: `nomi run app.nomi` serves the API on
port 8080, and `npm run dev` in `web/` serves the frontend on the dev
server's own port with hot reloading. Open that port while you work; port
8080 serves the frontend as it was at the last build. The dev server
forwards every `/api` request to Nomi, so the page calls the API on its
own origin and needs no CORS headers. A frontend served from another origin needs them:
wrap the API with [`http.cors`](/http/#middleware) and list that origin.

In Vite the forwarding is `server.proxy`. The config also adds a
compression plugin for the release build, described below. Install it
with `npm install -D vite-plugin-compression2`:

```ts
// web/vite.config.ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { compression } from "vite-plugin-compression2";

export default defineConfig({
  plugins: [react(), compression()],
  server: {
    proxy: { "/api": "http://localhost:8080" },
  },
});
```

`app.nomi` embeds `web/dist`. Until the first `npm run build` writes it,
`nomi run` warns that `web/dist` does not exist and serves the API with an
empty frontend; the next run after a build embeds it.

### Serving the build

`app.nomi` routes `/api/todos` to its handler and every other path to the
embedded build. The `App` struct and `boot` give the server a supervisor
for its connections, as in [HTTP](/http/). Paths in an embedded tree start
at the module root, so `Files.sub` makes `web/dist` the root the server
serves from:

```nomi-run compile-only
// FILE: app.nomi
import {
    std::embed.Files
    std::http.{self, Route, Router, Server}
    std::supervisors.Supervisor
    api
}

embed web `web/dist`

struct App {
    server: Supervisor
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(App{server: Supervisor.new(max_running: 256)})
}

fn main() -> Result<Unit, http.Error> {
    frontend = http.file_server_embedded(
        Files.sub(web, "web/dist")!,
        single_page: True,
        immutable: ["assets/**"],
    )

    routes = [#(Route`GET /api/todos`, api.list_todos), #(Route`/`, frontend)]
    listener = http.listen(Server{address: ":8080"})?
    http.serve(listener, Router.handler(Router.new(routes)?), App.server)
    Ok(Unit)
}

// FILE: api.nomi
import {
    std::http.{Request, Response}
    std::json.ToJson
}

pub struct Todo {
    id: Int
    title: String
    done: Bool
}

derive ToJson for Todo

pub fn list_todos(Request) -> Response {
    todos = [
        Todo{id: 1, title: "Write the API", done: True},
        Todo{id: 2, title: "Write the page", done: False},
    ]

    Response{body: .Json(ToJson.to_json(todos))}
}

// FILE: web/dist/index.html
<div id="root"></div>
```

`single_page: True` serves `index.html` for a browser's request for a
page route such as `/settings` or `/users/jane.doe`, so the client-side
router takes it from there. A file that exists is served as itself, and a
missing `/assets/x.js` stays a 404, since a browser asks for HTML only
when it navigates. Every file goes out
with its SHA-256 as its `ETag`, so a browser asking again for an
unchanged `index.html` gets a 304 with no body.
A bundler puts a hash of each file's contents in its name, and Vite
writes those files under `assets`, so `immutable: ["assets/**"]` lets a
browser keep them for a year. Each
entry is a glob over the file's path in the tree
([`path.matches`](/reference/path/#pathmatches)). An entry with no
wildcard names a file or a folder, and a folder covers everything in it,
so `["assets"]` works too. Match only files with a hash in their names,
since a browser will not ask for them again for a year. The compression
plugin writes a `.br` and a `.gz` copy beside each file, and the server
sends the compressed copy to any browser that accepts it
([reference](/reference/http/#httpserve_embedded)).

Other tools work the same way, each with its own output folder, dev
proxy and hashed files:

- webpack writes `dist/` and proxies with `devServer.proxy`. Name its
  output `assets/[name].[contenthash].js` and use `immutable: ["assets"]`.
- Angular writes `dist/<project>/browser/`, proxies with
  `proxy.conf.json`, and hashes the files at its root:
  `immutable: ["*-*.js", "*-*.css"]`.
- Create React App writes `build/`, proxies with `"proxy"` in
  `package.json`, and hashes `static/**`.
- SvelteKit's static adapter writes `build/` and hashes
  `_app/immutable/**`.
- Next's static export, `output: 'export'` in `next.config.js`, writes
  `out/` and hashes `_next/static/**`.

A static export with a page per route, from Next or a prerendering
SvelteKit, writes `/about` as `about.html`, and the server serves that
file for `/about` when no `about` file or directory exists, so such an
export works as written. Leave `single_page` off for such a site, so a
path with no page is a 404 rather than the home page.

### Releasing

`npm run build` in `web/` writes `web/dist`, and then `nomi build app.nomi`
embeds it into one executable. Build the frontend first, since
`nomi build` takes whatever `web/dist` holds at that moment, and refuses
to build while `web/dist` does not exist. The `build` script above also
runs `nomi types`:

```console
$ (cd web && npm run build)
$ nomi build app.nomi
```

Copy the executable to the server and run it; it needs no `web/` folder
beside it.

## Nomi in the page

`nomi build --web` writes a folder that any static host can serve. It
holds the program as WebAssembly and the JavaScript that runs it.
`nomi run --web` serves the same page while you work. The page's own
JavaScript can call any function marked `export fn`, passing values as
JSON. [Building &
Shipping](/building-and-shipping/#web-pages) covers both, and [Calling Nomi
from a page](/building-and-shipping/#calling-nomi-from-a-page) shows a page
calling a program's functions.

[Background Work](/background-work/) is next. It keeps work running after
the call that started it.
