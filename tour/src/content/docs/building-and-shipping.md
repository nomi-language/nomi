---
title: "Building & Shipping"
description: "Build a Nomi program into an executable, a web page, or a WebAssembly module another runner calls."
---

`nomi run` is for working on a program. To hand it to someone else, `nomi
build` packages it: as an executable, as a page that runs in the browser, or
as a WebAssembly module that another program loads and calls.

## Executables

```
nomi build app.nomi                        # ./app, for this machine
nomi build app.nomi --target linux/amd64   # for another platform
```

`nomi build` writes a **single executable** that needs nothing installed to
run: the [Nomi VM](/#the-nomi-vm) with your program attached.
`--target goos/goarch` builds for another platform, as Go does.

`nomi build --no-vm app.nomi` writes the compiled program alone, as
`app.nomic`. It is small (about 125 KB for hello world), and anything with
`nomi-vm` installed runs it with `nomi-vm app.nomic`; `nomi run app.nomic`
runs it too.

A file can also run as a script. Start it with a `#!/usr/bin/env nomi` line,
make it executable, and `./hi.nomi a b` runs it as `nomi run hi.nomi a b`
would; `nomi hi.nomi a b` does the same.

## Web pages

`nomi build app.nomi --web` writes a folder instead: a page any static host
can serve that runs the program in the browser, the way the examples on this
site run. The folder holds `app.wasm` (the VM and your program), `nomi.js`
(the loader) and `index.html`.

`nomi run --web app.nomi` serves that page on `http://localhost:8000/` while
you work on it: each time you save, it rebuilds and the page reloads, and a
build error shows on the page until you fix it. `--open` opens the page in
your browser.

Your own pages, scripts and images go in a `web/` folder beside the program:
both commands include them, and a `web/index.html` replaces the generated
page. A page of your own runs the program with `run` from `nomi.js`, passing
callbacks for what it prints and reads.

## Calling Nomi from a page

A page can also keep a program loaded and call its functions. Mark each one
with `export`; its arguments and result cross as JSON, so their types derive
`ToJson` and `FromJson`. Such a program needs no `main`.

```nomi
import std/json.{ToJson, FromJson}

struct Game {
    word: String
    guesses: List<String>
}

derive ToJson for Game
derive FromJson for Game

export fn new_game(word: String): Game {
    Game{word, guesses: []}
}

export fn guess(game: Game, letter: String): Game {
    {..game, guesses: [letter, ..game.guesses]}
}
```

The page's script loads the program once and calls it as often as it likes. A
struct arrives as a plain object and goes back the same way:

```js
import { load } from "./nomi.js";

const nomi = await load("./app.wasm");
let game = await nomi.call("new_game", "pipeline");
game = await nomi.call("guess", game, "e"); // { word: "pipeline", guesses: ["e"] }
```

A call that fails rejects with the Nomi error, and the program stays loaded
for the next one. The build also writes `nomi.d.ts`, so an editor checks each
`call` against the function's Nomi signature.

An exported function is a contract with a caller the compiler can't see:
renaming it or changing a parameter breaks the page without a build error. A
program that wants its logic free of that keeps the exported functions in a
small entry file of their own, a few wrappers over ordinary files.

## WebAssembly runners

A runner that loads WebAssembly itself, such as wasmtime, Node's `node:wasi`
or a browser's WASI shim, runs Nomi without `nomi.js`.
`nomi build --target wasip1/wasm app.nomi` writes the module, and what the
program declares decides how a runner uses it:

| The program has | The module is | The runner |
|---|---|---|
| no `export fn` | a WASI command module | starts it, and `main` runs |
| at least one `export fn` | a WASI reactor | calls its exported functions |

Either way the module carries its program in its own memory, so the runner
needs to give it no file access.

### Running a program

A program with no `export fn` builds a command module, which runs like any
WASI program: `main` reads the runner's stdin and arguments and writes to its
stdout. Given this `greet.nomi`:

```nomi
import std/io

fn main() {
    Ok(name) = io.read_line() else { return }
    io.print("Hello, ${String.trim(name)}!")
}
```

build it with `nomi build --target wasip1/wasm greet.nomi -o greet.wasm`.
`echo Ada | wasmtime greet.wasm` prints `Hello, Ada!`, and so does this
script under Node:

```js
import { readFile } from "node:fs/promises";
import { WASI } from "node:wasi";

const wasi = new WASI({ version: "preview1" });
const module = await WebAssembly.compile(await readFile("greet.wasm"));
const instance = await WebAssembly.instantiate(module, wasi.getImportObject());
process.exitCode = wasi.start(instance);
```

### Calling functions

A program with an `export fn`, like the game in
[Calling Nomi from a page](#calling-nomi-from-a-page), builds a reactor. It
exports `nomi_alloc`, `nomi_call` and `nomi_free`, plus `nomi_main`, which runs
`main` if the program has one.

The runner calls `_initialize` once, writes the function's
name and its JSON arguments into memory the module hands out, and reads back
`{"ok": value}` or `{"error": "message"}`:

```js
wasi.initialize(instance);
const x = instance.exports;
const put = (text) => {
  const bytes = new TextEncoder().encode(text);
  const ptr = x.nomi_alloc(bytes.length);
  new Uint8Array(x.memory.buffer, ptr, bytes.length).set(bytes);
  return [ptr, bytes.length];
};
const [name, nameLen] = put("new_game");
const [args, argsLen] = put(JSON.stringify(["pipeline"]));
const packed = BigInt.asUintN(64, x.nomi_call(name, nameLen, args, argsLen));
const ptr = Number(packed >> 32n), len = Number(packed & 0xffffffffn);
const answer = JSON.parse(new TextDecoder().decode(new Uint8Array(x.memory.buffer, ptr, len)));
// { ok: { word: "pipeline", guesses: [] } }
for (const [p, n] of [[name, nameLen], [args, argsLen], [ptr, len]]) x.nomi_free(p, n);
```

`nomi_call` returns where the answer is, its address in the high 32 bits and
its length in the low 32, and `nomi_free` releases each buffer. A failed call
leaves the module ready for the next one.
