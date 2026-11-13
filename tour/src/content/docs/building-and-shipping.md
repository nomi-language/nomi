---
title: "Building & Shipping"
description: "Build a Nomi program into an executable, a web page, or a WebAssembly module another program runs."
---

`nomi run` runs a program while you work on it. When you want to give the
program to someone else, `nomi build` packages it. This chapter covers each
way to package it, starting with the most common. The tooling reference's
[Building](/tooling/building/) page lists every flag and every file each
build writes.

## Executables

```console
$ nomi build app.nomi                        # writes ./app
$ nomi build app.nomi --target linux/amd64   # for another platform
```

`nomi build` writes one executable file. It needs nothing else installed to
run, because it holds both your program and the [Nomi VM](/#the-nomi-vm)
that runs it. `--target` builds for another platform, the way Go does.
[Executables](/tooling/building/#executables) lists what `nomi build`
refuses and where it finds the VM for each target.

You can also build the program without the VM:

```console
$ nomi build --no-vm app.nomi                # writes app.nomic
```

`app.nomic` is small, about 1 KB for hello world, because it holds only the
code your program uses. To run it, you need the VM: `nomi-vm app.nomic`, or
`nomi run app.nomic`. `nomi build-vm` writes `nomi-vm` for you.

A program that reads files it ships with, such as pages, templates or
migrations, can carry them inside itself. An `embed` declaration names
them by pattern, from the module root as imports are, and the build carries
them, so the executable needs no other file. It reads a file's bytes from
itself when the program reads that file, so a large embed costs nothing at
startup:

```nomi-run
// FILE: main.nomi
import
    std::embed.Files
    std::io
end

embed site `static`

fn main()
    io.print(Files.paths(site))
    io.print(Files.read_text(site, "static/index.html")!)
end

// FILE: static/index.html
<h1>Hello</h1>

// FILE: static/style.css
h1 { color: teal }
```
<!-- expect
[static/index.html, static/style.css]
<h1>Hello</h1>
-->

[Embedded files](/spec/typed-literals-and-keyword-declarations/#embedded-files)
has the pattern rules, and [std::embed](/reference/embed/) the `Files` API.

A `.nomi` file can also run as a script. Make its first line
`#!/usr/bin/env nomi` and make the file executable. Then `./hi.nomi a b`
runs it, the same as `nomi run hi.nomi a b`. [Scripts](/tooling/commands/#scripts-nomi-file)
says how a script finds the files it imports.

## Web pages

```console
$ nomi build app.nomi --web                  # writes a folder
```

This writes a folder you can put on any static web host. Opening its page
runs your program in the browser, the way the examples on this site run. The
folder holds:

- `app.wasm`: your program and the VM, as WebAssembly
- `nomi.js`: the JavaScript that runs it
- `nomi.d.ts`: TypeScript types for `nomi.js`
- `index.html`: the page

While you work, `nomi run --web app.nomi` serves the page at
`http://localhost:8000/`, or the next free port (`--port` picks one). Each
time you save, it rebuilds and reloads the page. If the build fails, the error shows on the page until you fix it.
`--open` opens the page in your browser.

To use your own page, scripts or images, put them in a folder named `web/`
next to your program. Both commands include it, and a `web/index.html`
replaces the generated page. Your page runs the program with `run` from
`nomi.js`.

A page has no disk, so a program's files are in memory. The page hands
them to `run`, by path, as text or bytes, and `run` returns every file the
program left when it ends:

```js
import { run } from "./nomi.js";

const { exitCode, files } = await run("./app.wasm", {
  files: { "data/prices.csv": csvText },
});
const report = new TextDecoder().decode(files["report.txt"]);
```

`nomi build --web` notes when a program uses files, since in the browser it
sees only the ones `files` gives it. A program built with `--target
js/wasm` and run under Node starts with no files, and a command-line
program that reads real files belongs on `--target wasip1/wasm`, which has
the directories its runner opens for it. Every option `run` takes, and what
the folder holds, is in [A program in the browser](/tooling/building/#a-program-in-the-browser).

A program with [Go bindings](/calling-go/) builds for the browser too.
`nomi build --web` compiles your Go code, and the Go packages it uses, into
`app.wasm`, as `nomi build` compiles them into an executable. Every one of
those packages must build for WebAssembly. Most pure Go does. A package
that only works on some operating systems is an error at its `gopkg`,
which names the package that stops the build. `modernc.org/sqlite` is one,
because `modernc.org/libc`, which it uses, has no WebAssembly version.
The same goes for `--target js/wasm` and `--target wasip1/wasm`.

## Calling Nomi from a page

A page can also call functions in your program, as often as it likes. Mark
each function the page may call with `export`:

```nomi-run no-run
import std::json.{FromJson, ToJson}

struct Game
    word: String
    guesses: List<String>
end

derive ToJson for Game
derive FromJson for Game

export fn new_game(word: String) -> Game
    Game{word:, guesses: []}
end

export fn guess(game: Game, letter: String) -> Game
    {..game, guesses: [letter, ..]}
end
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
still works. The folder's `nomi.d.ts` lets your editor check each `call`
against the Nomi function.

Renaming an exported function, or changing its parameters, breaks the page,
and the compiler can't tell you. To keep that risk in one place, put the
exported functions in a small file of their own that calls your other code.

## WebAssembly runners

A `--web` page suits one program that drives the page. For other
WebAssembly hosts, build for `wasip1/wasm`:

- `nomi build-vm --reactor` writes a VM, `nomi-vm-reactor.wasm`, with
  `nomi-wasi.js` beside it. A page or a Node program loads the VM once and
  then loads many small scripts into it, each one written by
  `nomi build --no-vm` as a `.nomic` file, and calls their exported
  functions or runs their `main`.
- `nomi build --target wasip1/wasm app.nomi` writes one module with the VM
  inside. `nomi-wasi.js` can load it, and a WASI runner such as wasmtime runs
  its `main` like any command-line program.

[Calling exported functions](/tooling/building/#calling-exported-functions)
covers `nomi-wasi.js`'s API and the functions a module provides to a host of
your own.

That ends the language tour. The Libraries chapters, starting with
[Dates & Times](/dates-and-times/), show programs built on the standard
library, and the [Standard Library](/reference/) reference covers every
module.
