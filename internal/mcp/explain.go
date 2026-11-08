package mcp

// explanations says what each diagnostic code means and how to fix it.
// TestEveryDiagnosticCodeIsExplained holds it to the codes the compiler
// declares.
var explanations = map[string]string{
	"unused-import": "An import names a module or item nothing in the file uses. " +
		"Unused imports are errors in Nomi. Remove the import, or the unused names from its `{...}` list.",
	"path-at-use": "A use names a file by its path (`db::posts.find(1)`) after the file was imported " +
		"as a file (`import db::posts`), which binds only its short name. Write the short name (`posts.find(1)`), " +
		"or import the directory (`import db`) and keep `db::posts`.",
	"embed-missing": "An `embed` pattern names a folder or path that does not exist yet, such as a frontend's " +
		"`web/dist` before its first build. `nomi run`, `check` and `test` embed it as an empty folder and warn; " +
		"`nomi build` refuses. Create it (run the frontend's build), or fix the pattern if the path is wrong.",
	"redundant-prelude-import": "An import names something the prelude already gives every file " +
		"(`Maybe`, `Result`, `List`, `Iter`, ...). Remove it.",
	"unused-binding": "A binding or parameter is never read. Remove it, or name it with a leading `_` " +
		"(`_ = f()`, `_unused`) to say the value is ignored on purpose.",
	"unreleased-resource": "A resource the function acquires (a file reader or writer, a spawned process, a connection, " +
		"or a value of a type implementing `Release`) is never released and not handed on. Write `defer` with its release " +
		"right after the binding (`defer Reader.close_quietly(r)`), or return it or store it where its owner releases it.",
	"discarded-resource": "A call acquires a resource and the value is dropped (bound to `_` or left unused), so nothing " +
		"can release it. Bind it to a name and `defer` its release, or do not acquire it.",
	"uncommitted-resource": "A writer's discard is deferred, and a path returns normally without committing it, so the " +
		"deferred discard throws the output away. Call `Writer.close(w)?` before each success return; error exits need nothing.",
	"once-shadow": "A name in a pattern names a `once` in scope. A pattern name always binds a new name, " +
		"so it matches every value instead of comparing with the `once`. Bind another name and compare in a guard " +
		"(`x when x == limit ->`), or rename the binding if no comparison was meant.",
	"unreachable-code": "A statement follows one that always leaves its block (`return`, a trap, a loop that never breaks), " +
		"so it never runs. Remove it or move it before the exit.",
	"unconditional-recursion": "Every path through the function calls the function itself with its own parameters " +
		"before it can return, so a call never ends. Add a case that returns without recursing, or change the arguments.",
	"recursive-render": "A `Display` or `Debug` impl renders its own receiver through the function it is defining " +
		"(`\"${value}\"` inside `to_string(value)`), which never ends. Render the fields instead.",
	"recursive-protocol": "An `Equatable` `equal`, `Comparable` `compare` or `Hashable` `hash` impl compares or hashes " +
		"its own arguments through the function it is defining, which never ends. Compare or hash the fields, or `derive` the impl.",
	"infinite-iterator": "An `Iter` that never ends is consumed whole (`Iter.to_list`, `Iter.count`, ...), " +
		"so the program never finishes. Limit it first with `Iter.take(n)` or `Iter.take_while`.",
	"reserved-name": "A declaration reuses a name the language reserves for a built-in type (`Int`, `String`, ...). " +
		"Every use of the name still means the built-in, so other errors may follow from this one. Rename the declaration.",
	"boot-result": "An entry file's `boot` must return `Result<App, E>`: the application struct in `Ok`, " +
		"or the error that stops the program. Wrap the struct: `Ok(App{...})`.",
	"invalid-literal": "A typed literal checked at compile time does not parse as its type, and the message says why: " +
		"a backtick literal (``Regex`...` ``, ``Locale`...` ``), or a checked literal in any quotes (`Url\"...\"`), " +
		"whose error points at the character or slot its type refused. Fix the literal's text.",
	"unlisted-currency": "A `Currency.custom` call names a currency its module's `nomi.toml` does not list. " +
		"List it, or use the currency's literal.",
	"custom-currency-call": "A `Currency.custom` call with a literal code and digits writes a currency " +
		"a ``Currency`...` `` literal already names; the literal is checked at compile time.",
}
