package mcp

// explanations says what each diagnostic code means and how to fix it.
// TestEveryDiagnosticCodeIsExplained holds it to the codes the compiler
// declares.
var explanations = map[string]string{
	"literal-breaks-rule": "A literal stands where a subtype or a validated type is expected (`subtype Port Int when fn p -> p >= 1`, " +
		"`type Email String when is_email`), and the type's rule, or a rule up its chain, is false for it: `Port(0)`, or `0` passed where a " +
		"`Port` is expected. The rule runs when the program is compiled. Change the literal, or, for a value known only when the program runs, " +
		"check it with `Port.try_from(x)`, which answers a `Maybe<Port>`.",
	"unchecked-literal": "A literal of a subtype or a validated type could not be checked against its rule when the program was compiled, " +
		"so the program is refused rather than run with a value no rule vouched for. A message that starts `internal:` is a bug in Nomi: " +
		"the compiler lost the function that checks the rule; report it. Otherwise the rule reaches something the compiler does not " +
		"support yet; until it does, build the value with the type's `try_from`, which answers a `Maybe`.",
	"literal-zero-divisor": "An `Int`, `Decimal` or `Duration` `/` or `%`, or its named form (`Int.divide`, `Int.remainder`, " +
		"`Decimal.remainder`), divides by a value that is always zero: a literal zero (`x / 0`, `x % -0`, `d / 0d`), or a binding " +
		"or expression whose value the compiler knows is 0 (`zero = 0` then `7 % zero`). Divide by the value you meant. " +
		"`Float` division by zero gives an infinity or NaN and is not reported.",
	"unchecked-operand": "std's `/` and `%` on `Int`, `Decimal` and `Duration` take a divisor that cannot be zero (`NonZeroInt`, " +
		"`NonZeroDecimal`, `NonZeroDuration`; a `Duration` divided by an `Int` takes a `NonZeroInt`), and `<<<` and `>>>` a " +
		"`NonNegativeInt` count, so a checked program never divides by zero or shifts by a negative count. The operand here is not " +
		"shown to be one. Check it first: after `if d == 0 then return ...`, or in the `else` of `if d == 0`, `d` is a `NonZeroInt`. " +
		"Or take the subtype as the parameter's type (`fn per(total: Int, d: NonZeroInt)`), or check a computed value with " +
		"`NonZeroInt.try_from(...)`, which answers a `Maybe`. A non-zero literal, and a value whose range rules zero out " +
		"(`n + 1` with `n` a `NonNegativeInt`), pass as they are.",
	"capability-not-allowed": "The project's nomi.toml has a `[capabilities]` table, which lists what the program may reach outside " +
		"itself: files, the network, processes, output and input, the clock, randomness, the environment, tasks, Go bindings. `allow` " +
		"holds the whole program, `allow_in_tests` adds names for `nomi test`, and `[capabilities.modules]` holds a required module's " +
		"own files to a list of their own. Each name is a std module as an import writes it (`std::fs`) or `go:` and a Go import path. " +
		"This call reaches a name its list does not hold. A call counts when anything can reach it from `main`, the boot, an exported " +
		"function or a test, even if it never runs: a branch never taken, a function passed as a value and never called, any " +
		"implementation of an interface function, any `Display` body when a value is displayed. The help's chain says why this one " +
		"counts. Fix it one of three ways: add the name to the list the message names; remove the call; or change how the program " +
		"reaches it, following the chain: a call in a `Display` body, an interface implementation or a function value nothing needs " +
		"counts, so move it out of the code the program reaches. Args are the name, the module over its list (empty for the " +
		"program's), and the names that list allows. A Go program that embeds Nomi can hold it to a list of its own " +
		"(`vmhost.WithCapabilities`); a call over that list says the host allows it, and the fix is the host's.",
	"unused-import": "An import names a module or item nothing in the file uses. " +
		"Unused imports are errors in Nomi. Remove the import, or the unused names from its `{...}` list.",
	"path-at-use": "A use names a file by its path (`db::posts.find(1)`) after the file was imported " +
		"as a file (`import db::posts`), which binds only its short name. Write the short name (`posts.find(1)`), " +
		"or import the directory (`import db`) and keep `db::posts`.",
	"embed-missing": "An `embed` pattern names a folder or path that does not exist yet, such as a frontend's " +
		"`web/dist` before its first build. `nomi run`, `check` and `test` embed it as an empty folder and warn; " +
		"`nomi build` refuses. Create it (run the frontend's build), or fix the pattern if the path is wrong.",
	"interface-value-parameter": "Advice, not an error: a function's type parameter is used only as one parameter's type, " +
		"under one interface bound, so the parameter can take the interface as its type (`fn label(value: Named)` for " +
		"`fn label<T>(value: T) where T: Named`). `nomi check` warns and passes the file; the fix writes the interface value " +
		"and calls `Named.name(value)` for `T.name(value)`. Keep the type parameter when the caller's type matters past the call.",
	"redundant-prelude-import": "An import names something the prelude already gives every file " +
		"(`Maybe`, `Result`, `List`, `Iter`, ...). Remove it.",
	"redundant-nested-spread": "A nested struct update spells the value its field already holds as a path: " +
		"`{..ada, address: {..ada.address, city: \"NYC\"}}`. Inside an update a bare `..` stands for that value, " +
		"so write `.{..ada, address: .{.., city: \"NYC\"}}`; the fix does. A spread naming another value " +
		"(`..bob.address`) or a base that is a call is not affected.",
	"unused-binding": "A binding or parameter is never read. Remove it, or name it with a leading `_` " +
		"(`_ = f()`, `_unused`) to say the value is ignored on purpose.",
	"unreleased-resource": "A resource the function acquires (a file reader or writer, a spawned process, a connection, " +
		"or a value of a type implementing `Release`) is never released and not handed on. Write `defer` with its release " +
		"right after the binding (`defer Reader.close_quietly(r)`), or return it or store it where its owner releases it.",
	"released-value-escapes": "A `defer` releases a resource when its block ends, but the value (or a value holding it) " +
		"leaves that block: as the block's value, in a `return`, inside a struct or constructor, or captured by a lambda. " +
		"It would be used after it is closed. Defer the release where the value is finally used, or hand it on without deferring it here.",
	"leaks-on-error": "A function acquires a resource and hands it on (returns it, or stores it in what it returns), but a `?` " +
		"or an error `return` between the acquisition and the hand-on leaves without releasing it. Write `defer on error` " +
		"with its release right after the binding (`defer on error sql.close_quietly(conn)`), which runs only if the function fails, " +
		"or move an existing `defer` up to right after the binding.",
	"leaks-on-return": "A function acquires a resource with no `defer` that releases it, and hands it on along some paths " +
		"(returns it, or stores it in what it returns), but this path returns normally without handing it on or releasing it. " +
		"A `defer on error` does not run here, since the return is not a failure. Call the release on this path before the return " +
		"(`Reader.close_quietly(r)` before `return Ok(None)`). A plain `defer` would release the value on the paths that hand it on too.",
	"defer-on-error-cannot-fail": "A `defer on error` runs only when its function fails through a `?` or a `return` of `Err` or `None`, " +
		"and this function returns a type that carries no failure, so only a trap would run it. Write `defer`.",
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
	"undefined-variable": "A name is used as a value and nothing in scope binds it. Check the spelling against the help's " +
		"suggestion, bind the name before the use, or import the file that declares it. The Bool literals are `True` and `False`, " +
		"and an absent value is `None`.",
	"unknown-type": "A type name names no type in scope. Check the spelling, declare the type, or import the file or std module " +
		"that declares it; the help names a std module or a dotted type when one matches.",
	"undefined-type-or-variant": "A capitalized name is used as a value and names no type and no variant in scope. " +
		"Check the spelling, import the file or std module that declares the type, or write a variant through its enum (`Color.Red`).",
	"no-type-member": "`Type.member` names a member the type does not have: no function in its `impl` blocks, no variant and no " +
		"constant by that name. Check the spelling against the help's suggestion, or declare the function in an `impl Type` block.",
	"argument-type-mismatch": "A call passes an argument whose type its parameter does not take. The message names the argument, " +
		"the parameter's type and the argument's. Pass a value of the parameter's type, or convert the value first; the help says " +
		"how when one conversion is right for the two types.",
	"missing-field": "A struct or variant literal leaves out a field that has no default. Write the field in the literal, " +
		"or give it a default in the declaration.",
	"missing-impl-function": "An `impl Iface for T` block lacks a function the interface requires and gives no default for. " +
		"Write the function in the block with the signature the interface declares.",
	"non-exhaustive-case": "A `case` has no arm for some value of its subject. Add an arm for each value the message names, " +
		"or a `_` arm that takes the rest. A guarded arm does not count toward coverage.",
	"bare-pipe-stage": "A pipe stage is a name without parentheses (`3 |> double`). A stage is a call that the pipe fills at " +
		"its first argument, and a bare name is a function reference. Write the call: `3 |> double()`.",
	"lambda-pipe-stage": "A lambda stands where a pipe stage is expected (`x |> fn v -> v + 1`). A lambda is not a stage; " +
		"`then` applies one to the piped value: `x |> then fn v -> v + 1`.",
	"useless-return": "A bare `return` is the last statement of its body, where the body ends anyway. Remove it. " +
		"If the function is not written yet, write `todo`.",
	"missing-translation": "A `phrase` or a `noun` has no entry in the catalog of a locale the project lists. " +
		"Add the entry to that locale's catalog, as the help spells out, or list the locale in `[i18n].partial_locales`.",
	"named-collection-literal": "A list or map literal is written with std's type name in front (`List[1, 2]`, `Map{\"a\" => 1}`). " +
		"A list literal is `[1, 2]` and a map literal is `{\"a\" => 1}`; remove the name.",
	"dollar-interpolation": "A string writes `$name` where a value named `name` is in scope. That is text, not the value: " +
		"Nomi interpolates with `${name}`. It is a warning, since the text may be meant.",
}
