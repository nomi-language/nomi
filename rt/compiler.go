package rt

// The data std/compiler's host functions exchange, and nothing else.
//
// # Why a compiler type lives in the runtime library
//
// rt must not import the compiler's packages —
// TestRuntimeImportsOnlyItsAllowlist is the guard. So the implementation of
// `compiler.check` cannot be here: it is the analyzer. It lives in
// nomi/stdcompiler.
//
// What must be here is the type crossing that boundary. `check` answers
// `List<Diagnostic>`, and internal/irbuild's stdstruct.go represents a stdlib
// struct as a Go type in rt so that one Nomi type has exactly one Go
// representation (TestStdStructHasExactlyONEGoRepresentation).
// A `Diagnostic` declared in nomi/stdcompiler instead would be a second
// vocabulary for the same kind identity, reachable only through the compiler's
// module — so a program that merely names the type in a signature, without
// calling anything, would drag the whole front end in.
//
// Plain int64 and string fields and no behaviour: this file adds no dependency
// to rt and nothing here can reach a front-end package.
//
// # Field types are int64, not int
//
// A Nomi `Int` is a Go `int64` everywhere in this runtime (rt/arith.go), and
// internal/irbuild projects an rt field onto `kindInt` by its Go type. An `int`
// field would produce no anchor and the type would refuse at every mention,
// which is the correct failure but a confusing one to read.

// Diagnostic is one compiler diagnostic as data — `std/compiler.Diagnostic`.
//
// 1-based line and column, exactly as the analyzer reports them, and the message
// verbatim.
type Diagnostic struct {
	Line    int64
	Col     int64
	Message string
}

// Hover is the editor hover content for one source position —
// `std/compiler.Hover`.
//
// `Markdown` is the renderer's whole answer, byte for byte what an LSP client
// receives; `Signature` is its first rendered signature line with the markdown
// fence and the surrounding whitespace removed. Both are produced by one
// function in nomi/stdcompiler.
//
// The renderer itself is nomi/internal/hoverdoc, which reads a resolved
// analysis, so the same split applies as to Diagnostic and for the same reason:
// the data is here and every line that computes it is in the compiler's module.
type Hover struct {
	Signature string
	Markdown  string
}

// Project is a whole in-memory project as data — `std/compiler.Project`.
//
// `Files` maps a module path without `.nomi` (`"main"`, `"tools/seed"`) to that
// file's source; `EntryPoint` names which of them is the entry; `Manifest` is
// the `nomi.toml` text when the project supplies one.
//
// The three field types are the reason this row exists at all, and each is a
// different projection:
//
//   - `EntryPoint` is a scalar, which every row covers.
//   - `Files` is a map, a container-typed field.
//     `rt.Map` is generic, so the field's Go type is fully applied here and the
//     builder (internal/irbuild) interns `Map<String, String>` in the process-wide table — both
//     components are package-neutral, so the kind compares equal to the one a
//     call site's own `mapKind` produces. A kind interned per-gen would be one
//     no other gen could match.
//   - `Manifest` is a `Maybe` over `rt.Toml`, a std newtype that already has an
//     `opaqueSpecs` row, and it carries a field default of `None` — so the
//     builder must supply that value at a construction site that omits it
//     rather than leaving a zero `Maybe`, whose Tag is neither Some nor None
//     and matches no arm of a `case`.
type Project struct {
	EntryPoint string
	Files      Map[string, string]
	Manifest   Maybe[Toml]
}

// RunFile is the input to `compiler.run_file` as data —
// `std/compiler.RunFile`.
//
// `EntryPoint` is a module path without `.nomi` (`"main"`, `"tools/seed"`)
// resolved under the current project root; `Env` is a simulated process
// environment visible to `os.get` while the run source's `boot()` builds its app
// env, and a missing key inherits the running process's own.
//
// Two fields, and the pair is deliberately not a `Project`. A `Project` carries
// its sources in it (`Files`) and is analyzed in memory; a `RunFile` names one
// file on disk and is evaluated. Sharing a Go type between them would put a
// `Files` map on a value that must read the real project root and an `Env` map on
// a value that must not — so the builder could construct either shape for either
// function, and the mistake would be a wrong answer rather than a compile error.
//
// `Env` carries a field default of `Map.empty()`, which is why the builder needs
// a value to supply at a construction site that omits it. Unlike `Maybe`, an
// omitted `rt.Map` zero value is a perfectly good empty map — the trie root is a
// nil pointer and the count is zero — so the default here is not load-bearing
// against a wrong answer the way `Project.Manifest`'s `None` is. It is supplied
// anyway, because "the builder fills a declared default" is the rule and an
// exception justified by a coincidence of representation is one nobody can check.
type RunFile struct {
	EntryPoint string
	Env        Map[string, string]
}
