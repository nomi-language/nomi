---
title: "Building & Shipping"
description: "Build a Nomi program into an executable, a web page, or a WebAssembly module another program runs."
---

`nomi run` runs a program while you work on it. When you want to give the
program to someone else, `nomi build` packages it. This chapter covers each
way to package it, starting with the most common.

## Executables

```console
$ nomi build app.nomi                        # writes ./app
$ nomi build app.nomi --target linux/amd64   # for another platform
```

`nomi build` writes one executable file. It needs nothing else installed to
run, because it holds both your program and the [Nomi VM](/#the-nomi-vm)
that runs it. `--target` builds for another platform, the way Go does.

You can also build the program without the VM:

```console
$ nomi build --no-vm app.nomi                # writes app.nomic
```

`app.nomic` is small, about 1 KB for hello world, because it holds only the
code your program uses. To run it, you need the VM: `nomi-vm app.nomic`, or
`nomi run app.nomic`. `nomi build-vm` writes `nomi-vm` for you.

A `.nomi` file can also run as a script. Make its first line
`#!/usr/bin/env nomi` and make the file executable. Then `./hi.nomi a b`
runs it, the same as `nomi run hi.nomi a b`.

## Web pages

```console
$ nomi build app.nomi --web                  # writes a folder
```

This writes a folder you can put on any static web host. Opening its page
runs your program in the browser, the way the examples on this site run. The
folder holds:

- `app.wasm`: your program and the VM, as WebAssembly
- `nomi.js`: the JavaScript that runs it
- `index.html`: the page

While you work, `nomi run --web app.nomi` serves the page at
`http://localhost:8000/`. Each time you save, it rebuilds and reloads the
page. If the build fails, the error shows on the page until you fix it.
`--open` opens the page in your browser.

To use your own page, scripts or images, put them in a folder named `web/`
next to your program. Both commands include it, and a `web/index.html`
replaces the generated page. Your page runs the program with `run` from
`nomi.js`.

## Calling Nomi from a page

A page can also call functions in your program, as often as it likes. Mark
each function the page may call with `export`:

```nomi-run no-run
import std/json.{FromJson, ToJson}

struct Game {
    word: String
    guesses: List<String>
}

derive ToJson for Game
derive FromJson for Game

export fn new_game(word: String) -> Game {
    #Game{word, guesses: []}
}

export fn guess(game: Game, letter: String) -> Game {
    #{..game, guesses: [letter, ..game.guesses]}
}
```

Values go between JavaScript and Nomi as JSON, so each type they use derives
`ToJson` and `FromJson`. A program like this needs no `main`.

Build it with `nomi build game.nomi --web`, or serve it with
`nomi run --web game.nomi`. Then your page loads it and calls its functions.
A Nomi struct arrives as a plain JavaScript object, and you pass it back the
same way:

```js
import { load } from "./nomi.js";

const nomi = await load("./app.wasm");
let game = await nomi.call("new_game", "pipeline");
game = await nomi.call("guess", game, "e"); // { word: "pipeline", guesses: ["e"] }
```

If a call fails, its promise rejects with the Nomi error, and the next call
still works. The build also writes `nomi.d.ts`, so your editor can check
each `call` against the Nomi function.

Renaming an exported function, or changing its parameters, breaks the page,
and the compiler can't tell you. To keep that risk in one place, put the
exported functions in a small file of their own that calls your other code.

## WebAssembly runners

A `--web` page is best when one program drives the page. It runs in the
background, so it can wait for the user's input, or pause, without freezing
the page.

Sometimes you want something different: many small Nomi scripts on one page
or in one Node program, or a single program for a tool like wasmtime. The
rest of this chapter covers those.

### Loading programs from JavaScript

Here, the VM is downloaded once and each script is a small separate file.
Build the VM and the scripts:

```console
$ nomi build-vm --reactor          # nomi-vm-reactor.wasm and nomi-wasi.js
$ nomi build --no-vm game.nomi     # game.nomic
$ nomi build --no-vm greet.nomi    # greet.nomic
```

The example below uses two scripts. It calls functions in `game.nomic`, the
game from [Calling Nomi from a page](#calling-nomi-from-a-page), and it runs
`greet.nomic` from start to finish. Here is `greet.nomi`:

```nomi-run no-run
import std/io

fn main() {
    Ok(name) = io.read_line() else { return }
    io.print("Hello, ${String.trim(name)}!")
}
```

The page or Node program loads the VM once, then loads each script into it:

```js
import { loadRuntime } from "./nomi-wasi.js";

const vm = await loadRuntime("./nomi-vm-reactor.wasm");

const game = await vm.load("./game.nomic");
console.log(game.exports);
// ["new_game(word: String) -> Game", "guess(game: Game, letter: String) -> Game"]
let state = await game.call("new_game", "pipeline");
state = await game.call("guess", state, "e");
console.log(state); // { word: "pipeline", guesses: ["e"] }

const { status, output } = await vm.run("./greet.nomic", { stdin: "Ada\n" });
console.log(status, output); // 0 "Hello, Ada!\n"
```

Each script gets its own copy of the VM, so scripts can't affect each other.
`run` gives back the exit status and everything the script printed;
`stdout` and `stderr` are there separately too. `nomi-wasi.d.ts` describes
the API for your editor.

Two limits: a script gets all of its input up front, and it runs on the
thread that called it. So a script that pauses, or runs for a long time,
holds up that thread. On a page, run such a script in a Worker.

If you only have one program, you can ship it as a single file instead, with
the VM inside it. Build a program that has exported functions for
WebAssembly, and `nomi-wasi.js` is written next to it. Calling `load` with
nothing loads the program inside:

```console
$ nomi build game.nomi --target wasip1/wasm -o game.wasm
```

```js
import { loadRuntime } from "./nomi-wasi.js";

const app = await loadRuntime("./game.wasm");
const game = await app.load();
console.log(await game.call("new_game", "pipeline")); // { word: "pipeline", guesses: [] }
```

### One program for a WASI runner

Tools like wasmtime run WebAssembly programs on their own, outside a
browser. To give one of them a program, build it like this:

```console
$ nomi build --target wasip1/wasm greet.nomi -o greet.wasm
```

Then `echo Ada | wasmtime greet.wasm` prints `Hello, Ada!`. The program's
`main` reads the tool's input and prints to its output, like any
command-line program. The file holds the VM, so it is about 11 MB.

### Your own WASI host

If you are writing your own host for WebAssembly, you can call a Nomi
module's functions without `nomi-wasi.js`. The
[spec's Exported Functions section](https://github.com/nomi-language/nomi/blob/main/docs/spec.md#exported-functions)
describes each function the module provides and the JSON it sends back.
