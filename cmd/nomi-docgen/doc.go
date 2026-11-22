// Command nomi-docgen generates the standard-library API reference from ///
// doc comments.
//
// For each stdlib module it writes <out>/<module>.md: every pub declaration with
// its signature + doc, rendered by the same renderer the LSP and the tour's
// hovers use (hoverdoc.Render), so the reference can't drift
// from the editor. Signatures use ```nomi fences (statically highlighted,
// not turned into runnable editors). Output is generated, not committed.
//
//	go run ./cmd/nomi-docgen <reference-output-dir>
//
// With -examples it writes the tour's Examples section instead, from the
// Exercism solutions (examples.go). Those pages are committed.
//
//	go run ./cmd/nomi-docgen -examples tests/exercism tour/src/content/docs/examples
//
// With -spec it writes the site's Specification section from docs/spec.md
// (spec.go). Those pages are generated, not committed.
//
//	go run ./cmd/nomi-docgen -spec docs/spec.md tour/src/content/docs/spec
//
// With -style it writes the site's Writing Nomi section from docs/style.md
// (style.go). Those pages are generated, not committed.
//
//	go run ./cmd/nomi-docgen -style docs/style.md tour/src/content/docs/writing
//
// With -tooling it writes the site's Tooling section from docs/tooling.md
// (tooling.go):
//
//	go run ./cmd/nomi-docgen -tooling docs/tooling.md tour/src/content/docs/tooling
//
// With -llms it writes the files for models (llms.go): the cheat sheet's
// page from docs/cheatsheet.md, and llms.txt, llms-full.txt and
// cheatsheet.md at the site root. All are generated, not committed.
//
//	go run ./cmd/nomi-docgen -llms . tour
//
// # Effects
//
// A function a call to which has an effect (vmhost.EffectCalls), and a
// `once` a read of which has one, has a line under its signature naming
// what an effects list must allow for it, `Has effects: std::fs`, and a
// module with any such entry says so in its overview, linking the tooling
// reference's effects lists; llms-full.txt carries both. The name is the
// one a refusal gives a program's call: the module that declares the
// function, since a call is named by the std module the program called
// (`net.dial` is `std::net` though it crosses through std::http's host
// functions). The decision is
// vmhost.Program.StdEffects over the module's lowered tests, which
// docgen loads once per module for its prompts too (stdProgram, reach.go):
// a `host fn` that is an effect crossing, and a function written in Nomi,
// or a `once` by its initializer (`io.stdout`, which obtains the stream),
// whose reach walk, started from it, reaches one without leaving std. The
// walk over-approximates as the effect check does: a function value a
// function makes counts though nothing calls it (`std::storage::testing.rules`
// returns its rules' names), and a call through an interface value
// reaches every impl (`storage.list_all` reaches the file store's).
// A generic function is walked from the instances the module's tests and
// examples make, and from its abstract body (irbuild.StdAbstractBodies,
// or the program's own for a module lowered with each program), which has
// what its body does whatever its type arguments are; an effect reached
// only through a function the call passes is the caller's, and one
// through a type parameter's interface function counts std's
// implementations, as the walk stays in std. A function every call of
// which the builder lowers to an operation of the machine (`Iter.map`,
// `Set.insert`, irbuild.StdLowersToMachine) is answered by the operation,
// which has no effect of its own. A generic function with none of these
// has no line, whether or not it has an effect: one whose abstract body
// the builder cannot yet lower with its type parameters held as their
// bounds. No list in docgen names a function with effects.
//
// The tooling reference's "Effects lists" holds a table from the same walk
// (effect_table_test.go): the public functions and `once` values with
// effects, by the name a list holds. TestEffectTableMatchesTheReference
// fails while it differs from the walk and prints the table to paste.
// TestReferenceEffectsAnswerEveryFunction fails for each public function
// the walk has no answer for, so a new one fails there rather than
// reading as pure, with what would let the walk answer it.
//
// # Stdlib reference examples
//
// Each `//!` prompt on a public entry is written as a live editor,
// `<pre data-nomi-stdlib-module><code class="language-nomi-test">`, which
// the tour's worker runs through nomiRunStdlibTest (vmhost.StdlibReference)
// when it scrolls into view. A prompt that reaches an effect class the
// browser lacks (promptClassifier, browser.go) is written instead as a
// plain `language-nomi` block carrying data-nomi-browser-lacks, with a line
// under it saying why and that `nomi test` runs it; the client only
// highlights it, as it does a ```nomi-run compile-only tour block. The
// classes are vmhost.TargetLacks("js", "wasm"): `connect`, `listen` and
// `processes`. An HTTP request (`network`) is not among them, since the
// browser build sends it through fetch.
//
// Docgen loads each module's lowered tests once (vmhost.LoadEmbeddedStdlib)
// and walks from the prompt's own case (Program.TestClassUses), the
// walk `nomi build --target js/wasm` refuses a program by. The walk counts
// what a prompt could reach, which is wider than what it does: a call
// through an interface reaches every impl, and a crossing a fake answers on
// the frame still counts. So a flagged prompt runs once, natively, and
// keeps only the classes whose calls reached the operating system, which
// internal/osreach counts. A new crossing of a class the browser lacks must
// call osreach.Note where it reaches the operating system, or its prompt
// stays live and vmhost's TestTourWasmAnswersTheReferenceEditors fails on
// it.
//
// reference.expect (TestReferenceInteractiveTestsOnTheVM) records each
// prompt the page does not run with `[not run in the browser: <classes>]`
// and still runs it natively. TestTourWasmAnswersTheReferenceEditors runs
// every prompt on the generated pages through the staged wasm under node,
// requires each live editor to run clean, and logs each prompt the page
// does not run that ran clean anyway (`net.lookup_port`, which Go's
// built-in table of service ports resolves without a resolver).
//
// An editor's edit is its test, and the module's declarations are the
// embedded ones the process has already checked and lowered. So
// vmhost.StdlibReference parses the test alone, at its lines, checks it in
// the module's scope of the shared analysis
// (frontend.Checker.CheckStdlibTest, analysis.FileAnalysis.CheckAddedTest)
// and lowers it against the module's lowering
// (irbuild.GenerateStdlibReferenceIR). The checker's walk and checks run
// over the test only, into a copy of the analysis's body part, and the
// test's scope leaves the module scope once the case has run. A shared
// module's case is lowered against its cached view; a listed module's
// case, or a shared module's case that imports one, in a program whose
// modules all link the kept lowering (irbuild's linkAllStd). An edit that
// is not one test alone is checked and lowered with the module's whole
// source (vmhost.LoadStdlibSource).
package main
