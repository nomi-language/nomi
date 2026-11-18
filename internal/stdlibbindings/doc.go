// Package stdlibbindings is the one hand-maintained table of stdlib host
// functions: which Go function implements each std `host fn`. A std
// `host fn` is implemented either by a VM intrinsic (tasks, channels,
// collection kernels, `io.print`) or by an adapter generated from a row
// here. The table holds two lists.
//
//   - Funcs: the stdlib modules whose implementation is Go (std/calendar,
//     std/locale, std/plural, std/random and std/regex), in their FFI-shaped
//     surface, std/currency's code checks and std/money/format's money
//     formatting, plus std/strings' `capture_groups` and
//     `capture_groups_all`, behind `String.captures`, which take a `Regex`
//     handle and so cannot be RtFuncs rows, and the locale-aware formatting
//     and parsing behind `Int.format`, `Float.format`, `Decimal.format` and
//     `String.join_list` (internal/stdformat), which read the program's CLDR
//     data. The VM calls them through generated adapters
//     (internal/stdlibadapters).
//   - RtFuncs: the rt functions the VM reaches for std's scalar, text, byte,
//     JSON, Decimal and concurrency primitives. The VM calls every row
//     through a generated adapter, and internal/irbuild's IR builder retains
//     a std `host fn` call only when its key has a row here.
//
// It exposes descriptors rather than performing registration, so it imports
// no engine: the adapter generator reads them, and every machine binds the
// generated adapters on its first host crossing.
//
// # Adding a host function
//
//  1. Write the `host fn` in std/<module>.nomi. A first-party facade names
//     no Go symbol.
//  2. Add a row: the key the declaration answers to and the Go function
//     value, in Funcs for an adapter package's function or RtFuncs for a
//     function over rt values. A function that acts outside the machine in
//     a way some target cannot gets the row's Class, which `nomi build`
//     checks against the target (vmhost.TargetLacks,
//     vmhost.Program.CheckTarget). A function that computes its answer
//     from its operands alone, acts on nothing outside the machine, and
//     costs no more than its operands' size gets the row's Pure, so a
//     refinement rule, a typed literal's handler, a `check` and a
//     debugger's evaluation may call it (internal/vm package doc,
//     "Effects"); unset, every call to it is an effect. A row that is not
//     Pure says whether it is an outside call, World: Inside or Outside
//     ("Outside calls" below); TestEveryRowDecidesItsWorld fails for one
//     that says neither.
//  3. Run `go generate ./internal/stdlibadapters`.
//  4. Add recorded cases for the new row
//     (internal/stdlibadapters/testdata/*.golden).
//  5. Run `go test ./internal/hostpair ./internal/stdlibadapters
//     ./internal/hostgen/...`.
//
// A row whose Go signature does not match the declaration fails generation
// by name. A Go parameter or result for a Nomi `Int` is `rt.Int` when the
// function handles an Int of any size, and `int64` (or a narrower width)
// when it needs a machine integer. A parameter takes a fixed width only
// when its declared type admits no Int past that width: `Int64`
// (std::int), a subtype whose `when` bounds it within the range, or an
// intersection with one (`NonNegativeInt and Int64`); generation refuses
// a plain `Int` there, since the adapter could only refuse the value as
// the program runs. Such a function takes `rt.Int` and defines what a
// large value means: an error or a range trap that names it, or, where
// every value past int64 means the same as the bound, saturation
// (`rt.Int.Saturated`). A result may be any width. An adapter
// returning a Nomi struct must build it with the declared type's
// descriptor, or every impl lookup on it misses.
//
// # Outside calls
//
// An outside call acts on, or reads from, something outside the machine:
// files, the network, processes, output and input streams, the clock,
// entropy, environment variables and arguments, tasks that outlive the call
// that starts them, a nested program, and any Go binding. Each row says
// whether it is one (World); a Pure row is inside by being Pure. The VM's
// intrinsics say the same in their own table (internal/vm's vm.go, held by
// TestEveryIntrinsicDecidesItsWorld) and std/compiler's hosts in theirs
// (internal/compilerhosts). Outside is its own fact: every row with a Class
// is Outside (TestEveryClassedRowIsOutside), and a row that is not Pure may
// still be Inside.
//
// The rules the rows follow:
//
//   - Obtaining a handle on something outside is the outside call; using a
//     handle the code already holds is inside. Opening a file, dialing,
//     listening, starting a process and asking for the standard streams
//     (`io.standard_input`, `standard_output`, `standard_error`) are
//     Outside. Reading, writing, closing or waiting on a Reader, a Writer,
//     a File, a connection, a server, a request, a WebSocket or a process
//     is Inside, the time such an operation waits and the Date header a
//     response carries included: the code that obtained the handle made the
//     outside call, and code handed one acts on its authority. Most of these
//     handles also have an in-memory form (a Reader over bytes, a sandbox's
//     files, `http/host.pipe`, a fake process), which a row cannot tell
//     apart.
//   - Reading the clock is Outside when the program gets the reading or
//     waits on it: `instant.Instant.now`, `timer.sleep`,
//     `Context.with_timeout` and `deadline_remaining` (both read now),
//     `Supervisor.flush_bounded` (a bound in real time). A deadline the
//     program gives as an Instant is Inside (`Context.with_deadline`).
//     Formatting or parsing a time is Inside.
//   - Reading the system's entropy is Outside when it is the value the
//     program gets: `random.os_state` and `secure_bytes`, and generating a
//     key or a certificate (`signatures/host.generate_*`,
//     `http/host.test_certificate`). A seeded generator is Inside. A
//     cryptographic operation that draws a nonce or salt and answers a
//     result whose meaning does not depend on it is Inside (`sign`,
//     `http/host.seal`).
//   - A read of files a row does on its own is Outside without a class
//     where the target check has none for it: `http/host.tls_config` reads
//     the PEM files its roots name. `http/host.ws_connect` dials.
//   - `io.read_line` reads standard input: Outside. Output is the VM's
//     intrinsics (`io.print` and its kin, `dbg`): Outside.
//   - Embedded files (`embed/host`) are part of the program: Inside.
//
// The environment and the arguments are not a row: the machine hands them
// to the entry's boot as a Startup, and a boot that takes one is the outside
// call that reads them (internal/vm's outside.go). An exit status, a trap
// and a failed assertion end the program and are not outside calls.
//
// What a program reaches is answered by vmhost.Program.OutsideCalls: each
// outside call is named by the std module the program's or a dependency's
// own code called (`std::net` for `net.dial`, which crosses through
// http's host functions), or `go:` and the Go import path of a `go`
// binding, and charged to the module whose file holds that call.
//
// # A new Go-backed module
//
// A module whose host functions live in a new Go package also needs:
//
//   - its package in `hostPackages` (internal/irbuild/stdlib.go);
//   - `Pure` on each of its rows that acts on nothing outside the machine,
//     since a refinement rule or a typed literal's handler may only reach
//     those, and World on each of the others (step 2);
//   - a row in `stdlibInventory` (internal/irbuild/inventory_test.go).
//
// # Std types in public signatures
//
// In a module lowered once for the process, a std type that a public
// signature names (an enum, an opaque distinct, a carrier struct a host
// function returns) needs an rt Go type and a row in internal/irbuild's
// stdenum.go, opaque.go or stdstruct.go. An interface such a body takes as a
// value needs a `stdIfaceSpecs` row (internal/irbuild/stdiface.go) and an rt
// dispatch table; a program holding one needs neither (internal/irbuild
// package doc, "Instantiation and dispatch"). Without them, every call whose
// signature names the type is reported as not supported yet.
//
// The shared std modules are lowered once, in import order, the members of
// an import cycle as one unit, so a body may call into any module it
// imports, either direction of a cycle included (internal/irbuild package
// doc, "The stdlib"). In a shared module, a function's parameter
// cannot be an `Iter<T>` unless it is generic, and a generic function's
// defaults cannot be left out at a call.
//
// # State across calls
//
// A program std module whose Go half holds state across calls keeps it in a
// handle: a `host type` in its internal module nested as `host` under it
// (std/csv/host.nomi, imported inside std as `csv::host`), an rt struct over
// `any` (rt/formats.go, rt/processes.go), a row in `stdHostSpecs`
// (internal/irbuild/stdhost.go), `rtHeld` (internal/hostgen/rtshapes.go) and
// `irStreamKinds` (internal/irbuild/irsupervisor.go), and the module in
// `alsoImplements` (internal/hostgen/stdtable) when the Go package is named
// for the public module (internal/stdcsv is std/csv/host's). The `host`
// module goes in `internalStdModules` (internal/analysis/internal_std.go),
// so programs cannot import it and the reference leaves it out. An
// rt-shaped host function returns one value, so a crossing with several to
// give returns a `List<Int>` or splits into one call per value.
//
// A module written only in Nomi (no `host fn`, no `host type`) that no
// shared std module imports needs none of that: add its name to
// `programStdModules` (internal/irbuild/programstd.go) and a row in
// `stdlibInventory`, and it is lowered with each program like the program's
// own files (std/uuid is).
//
// # Why this is hand-written, and what stops it drifting
//
// Every std declaration is a `host fn` or a `host type`, so no Nomi source
// names a Go symbol (package std's doc, "Why std uses host rather than go").
// The other candidate sources for the symbol half do not work;
// internal/hostpair's symbolsource_test.go checks each:
//
//   - Pairing by signature against the adapter package is not a function:
//     about half of its FFI-shaped exports share a signature with another,
//     and nine `func(d DateTime, n int64) DateTime` adders are
//     indistinguishable.
//   - Computing the Go name from the Nomi name misses about a third of the
//     rows, and the exceptions are not typos: `random.os_state` is
//     `FFIFromOS`, `Regex.compile` drops its receiver, and every `_raw` or
//     `_state` row drops its suffix.
//
// It holds function values rather than names, for this reason. A pair of
// strings ("this Nomi name" -> "call go_regex.Compile") is a lookup decided
// by a name: a rename on the Go side gives a name that resolves to nothing,
// silently. Holding the function value removes the failure mode: a renamed
// or deleted symbol is a Go compile error in this package, and the
// signature is reflected over rather than restated. So the only
// hand-written thing is the Nomi key.
//
// # A wrong or missing key is quiet, so a test checks the keys
//
// Nothing at load time checks a std `host fn` against this table: a stdlib
// declaration with no row is simply not retained, so a program reaching it
// is BLOCKED, far from the omission and without naming the missing row. The
// function values prevent silent resolution on the Go side. The Nomi key is
// the only half that remains derivable from source, so that is where the
// check is:
// internal/hostpair.TestEveryHostDeclarationInAGoBackedModuleIsRegistered
// derives every key from `std/*.nomi` and requires this table to be a
// bijection with them, reporting a missing key, a misspelling (both halves)
// and an orphaned row.
//
// # What would make the symbol derivable, and why it is not worth it
//
// Two things would: a `go alias.Symbol` selector back in the facade, or a
// marker comment on each Go function naming its Nomi key. The first is the
// arrangement this package deliberately avoids. A `gopkg` handle in a std
// facade makes the module a discoverable co-located adapter again, and
// every program importing it then needs `go` on PATH (internal/ffirun's
// hostkeyword_toolchain_test.go). The second keeps `host fn` and the
// toolchain-free path, but the derived symbol would be a string in a
// comment: a Go rename invalidates it silently and nothing catches it,
// which is the exact failure [Binding]'s Fn field exists to remove. So the
// reason this table holds function values is also the reason the last
// route to generating it is closed.
//
// A generator that emitted the keys and left `Fn:` to a human would be
// strictly worse than the test: same errors caught, plus a build step and a
// generated file to keep in sync.
package stdlibbindings
