package mcp

// explanations says what each diagnostic code means and how to fix it.
// TestEveryDiagnosticCodeIsExplained holds it to the codes the compiler
// declares.
var explanations = map[string]string{
	"literal-breaks-rule": "A literal stands where a refinement or a validated type is expected (`refine Port Int when fn p -> p >= 1`, " +
		"`type Email String when is_email`), and the type's rule, or a rule up its chain, is false for it: `Port(0)`, or `0` passed where a " +
		"`Port` is expected. The rule runs when the program is compiled. Change the literal, or, for a value known only when the program runs, " +
		"check it with `Port.try_from(x)`, which answers an `Option<Port>`.",
	"unchecked-literal": "A literal of a refinement or a validated type could not be checked against its rule when the program was compiled, " +
		"so the program is refused rather than run with a value no rule vouched for. A message that starts `internal:` is a bug in Nomi: " +
		"the compiler lost the function that checks the rule; report it. Otherwise the rule reaches something the compiler does not " +
		"support yet; until it does, build the value with the type's `try_from`, which answers an `Option`.",
	"seeded-hash": "A refinement's rule, a typed literal's handler or a checked literal's `check` can reach a hash: `Hashable.hash`, " +
		"`String.hash` or `Bytes.hash`, or a derived `Hashable` of a String, a collection or a struct. The compiler runs these once, when it " +
		"checks the program, and a hash is keyed by a seed each run draws, so the same value can hash to another Int when the program runs " +
		"and a literal the compiler accepted could break its rule then. Compare the values themselves instead of their hashes. Building a " +
		"`Map` or `Set` and reading it back is allowed, since its answers do not depend on the seed, unless its key's type has a " +
		"hand-written `impl Hashable` whose `hash` reads a hash. So is the hash of an `Int`, a " +
		"`Float`, a `Byte` or `Unit`, which is the value's own bits.",
	"literal-zero-divisor": "An `Int`, `Decimal` or `Duration` `/` or `%`, or its named form (`Int.divide`, `Int.remainder`, " +
		"`Decimal.remainder`), divides by a value that is always zero: a literal zero (`x / 0`, `x % -0`, `d / 0d`), or a binding " +
		"or expression whose value the compiler knows is 0 (`zero = 0` then `7 % zero`). Divide by the value you meant. " +
		"`Float` division by zero gives an infinity or NaN and is not reported.",
	"unchecked-operand": "std's `/` and `%` on `Int`, `Decimal` and `Duration` take a divisor that cannot be zero (`NonZeroInt`, " +
		"`NonZeroDecimal`, `NonZeroDuration`; a `Duration` divided by an `Int` takes a `NonZeroInt`), and `<<<` and `>>>` a " +
		"`NonNegativeInt` count, so a checked program never divides by zero or shifts by a negative count. The operand here is not " +
		"shown to be one. Check it first: after `if d == 0 then return ...`, or in the `else` of `if d == 0`, `d` is a `NonZeroInt`. " +
		"Or take the refinement as the parameter's type (`fn per(total: Int, d: NonZeroInt)`), or check a computed value with " +
		"`NonZeroInt.try_from(...)`, which answers an `Option`. A non-zero literal, and a value whose range rules zero out " +
		"(`n + 1` with `n` a `NonNegativeInt`), pass as they are.",
	"refinement-impl-makes-base": "A refinement has its base's impls, except those of an interface with a function that makes a value " +
		"of its type: `Parse`, `FromSql`, `Summable`, `Productable`, `Discrete`, `Steppable`, `From`, `TryFrom`. The " +
		"base's impl makes a value of the base, which no rule of the refinement has checked, so `Parse.parse<Port>(text)` and " +
		"`Summable.empty_sum<Port>()` are refused. Make a value " +
		"of the base and check it: `Port.try_from(n)` answers an `Option<Port>`. A call whose type argument nothing pins to the " +
		"refinement is made at the base instead, so with `ports` a `List<Port>`, `Iter.sum(ports)` is an `Int`. `FromJson` is a " +
		"reader: it reads a refinement at its base and checks its rules, a value that breaks one being the read's shape error, and a " +
		"record read from texts reads a field of a refinement with its base's `Parse` and checks it the same way.",
	"effect-not-allowed": "The project's nomi.toml lists the effects its program may have (files, the network, " +
		"processes, output and input, the clock, randomness, the environment, tasks, channels and other state it makes) in two tables. " +
		"A function is pure, or it has effects; each effect is named by the std module the code called. " +
		"`[effects.self]` holds the calls written in the project's own files: `std` lists the effects they may have, by std module " +
		"(`std::fs`, as an import writes it), and `std_in_tests` adds names for the effects only the tests have. " +
		"Under `[effects.self.modules]` an entry for one " +
		"of the project's modules, keyed by the module as its own code imports it (`pricing`, `reports::daily`), lists the std " +
		"modules whose effects that module's files may have within `std`; an empty entry allows no effects, and `entries_required = true` under " +
		"`[effects.self]` makes every module the program loads have one. `[effects.deps]` holds the calls written in the " +
		"files of every package the project depends on: `std` is the most effects any dependency may have, with `std_in_tests` for the " +
		"tests, and under " +
		"`[effects.deps.packages]` a dependency's entry lists the effects its own files may have. `entries_required = true` " +
		"makes every dependency have an entry, or an `[effects.self]` in its own nomi.toml. A table the project leaves out holds " +
		"nothing. A dependency whose own nomi.toml has an `[effects.self]` is held to it too; a call over it is " +
		"`effect-not-declared`. This call has an effect the list the message names does not hold. " +
		"A call counts when anything can reach it from `main`, the boot, an exported function or a test, even if " +
		"it never runs: a branch never taken, a function passed as a value and never called, any implementation of an interface " +
		"function, any `Display` body when a value is displayed. The help's chain says why this one counts. Fix it one of three " +
		"ways: add the name to the key the help names; remove the call; or change how the program reaches it, following the chain: a call in a `Display` " +
		"body, an interface implementation or a function value nothing needs counts, so move it out of the code the program reaches. " +
		"Args are the name, the list it is over (`self`, " +
		"`self.modules`, `deps`, `deps.packages`, or `host`), the package whose files hold the call (for `self.modules` the entry's " +
		"key), and the names that list allows. A Go program that embeds Nomi can hold it to a list of its own " +
		"(`vmhost.WithEffects`), which holds the whole program; a call over it says the " +
		"host allows it, and the fix is the host's. `nomi effects` prints the effects the project's own code and each dependency " +
		"have, `--why <name>` each call that has an effect, and `--write` writes the tightest tables the program passes.",
	"effect-not-declared": "A package this project depends on has an `[effects.self]` table in its own nomi.toml: its " +
		"`std` is the list of effects the package declares for its own files, an entry under its own `[effects.self.modules]` " +
		"narrows that for one of its modules. The " +
		"project's nomi.toml has an `[effects.deps]` table, so the package is held to what it declares, as well as to the " +
		"project's `[effects.deps]` and its entry for the package under `[effects.deps.packages]`. This call, in the " +
		"package's own files, has an effect the package's own `std`, or its entry for the file's module, does not hold: " +
		"the package declares fewer effects than its code has. The fix is in that package: add the name to " +
		"`std` or to the entry under `[effects.self]` in its nomi.toml, or remove the call. Nothing in the " +
		"project's own tables can allow it. When the project's tables do not allow the call either, the help says which. " +
		"Only the package's own `std` and its " +
		"entries are read; its `std_in_tests`, `entries_required` and `[effects.deps]` are for its own " +
		"builds. The call counts by the same rules as `effect-not-allowed`: anything that can reach it, even if it never runs. " +
		"Args are the name, `self` (or `self.modules` over its entry), the package, and the names that list holds.",
	"effects-not-determined": "The project's nomi.toml has an `[effects.self]` table, which holds the project's own code, " +
		"its public functions among it, to the effects it lists. A public generic function runs at whatever types its callers " +
		"give it, so `nomi check` reads its body with each type parameter held as its bounds: a call through a " +
		"bound counts every implementation the program has, and a generic function it calls at its type parameters is " +
		"read the same way. This function's body does not lower that way, and the help names the reason. A call that " +
		"gives the function types is still checked " +
		"at those types; what it does at other types is not known, so the list cannot hold it. Fix it by giving the " +
		"function a parameter of interface type in place of the type parameter, or by dropping its `pub` when no other " +
		"package needs it. " +
		"Args are the function's name and `self`.",
	"nomi-version": "A package's nomi.toml says, as `[package]`'s `nomi = \"v0.0.2\"`, the lowest nomi release its source needs, and " +
		"the running nomi is an older release. The package is the project itself or one it depends on, named by its repository, " +
		"directory and version. Nomi makes no compatibility promise yet, so nothing else is checked: a newer package's files may " +
		"not mean to this nomi what they say. Install a newer nomi. A nomi built from source refuses no package. " +
		"Args are the package as the message names it, the release it needs and the running release.",
	"manifest": "A nomi.toml the program is built from cannot be read: the project's own, or that of a package it depends on, " +
		"has a syntax error, a table or key nomi.toml does not have, a missing or invalid `[package]` name, or another problem " +
		"the message names, with the file and its line. An import by a package's name and every dependency resolve through " +
		"these files, so nothing else is checked until the problem is fixed: any other error would follow from it.",
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
		"(`Option`, `Result`, `List`, `Iter`, ...). Remove it.",
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
	"private-declaration": "Code names a `private` declaration of another file. A private declaration is seen only by its own file: " +
		"no other file sees it, a test file beside it included, so a test of it is a `test` block in the same file. Drop `private` " +
		"so every file of the package sees it, write `pub` so every package does, or move the code into the declaring file.",
	"derive-outside-types-file": "A `derive` names a type another file declares. A derive is written in the file that declares " +
		"its type, and in no other file, a test file beside it included. Move the derive beside the type's declaration, or write an `impl` " +
		"of the interface in this file when the package's orphan rule allows it.",
	"internal-declaration": "Code names a declaration of another package that has no modifier. Such a declaration is internal: " +
		"every file of its own package sees it, and no file of another package. Write `pub` on the declaration to name it from " +
		"another package, or call a public function of that package instead. A fetched dependency's files are in the package " +
		"cache and are not yours to edit, so use what that package makes public. A std declaration is not part of std's public " +
		"surface, and no program can make it so: use what the module's reference page lists.",
	"reexport-not-public": "A `pub import` re-exports an item that is not public. A re-export puts the item on the file's public " +
		"surface, so it may not widen an internal or private declaration. Write `pub` on the declaration, or import it without `pub`.",
	"exposes-type": "A declaration's signature names a type fewer files see than see the declaration: a `pub` declaration " +
		"names an internal or private type, or an internal one a private type. A file that may name the declaration could not " +
		"name the type it takes or returns. Raise the type's visibility, lower the declaration's, or make the type `opaque` " +
		"where only its representation names the narrower type.",
	"member-more-visible-than-type": "A function or `once` of an inherent `impl` block is seen by more files than see its type: " +
		"a `pub` member of an internal or private type, or an internal member of a private type. A file that sees the member " +
		"but not the type could not name the owner it is reached through (`Money.double`). Give the member its type's level " +
		"(drop `pub`, or write `private`), or raise the type's. A `private` member is never this error.",
	"once-shadow": "A name in a pattern names a `once` in scope. A pattern name always binds a new name, " +
		"so it matches every value instead of comparing with the `once`. Bind another name and compare in a guard " +
		"(`x when x == limit ->`), or rename the binding if no comparison was meant.",
	"unreachable-code": "A statement follows one that always leaves its block (`return`, a trap, a loop that never breaks), " +
		"so it never runs. Remove it or move it before the exit.",
	"unconditional-recursion": "Every path through the function calls the function itself with its own parameters " +
		"before it can return, so a call never ends. Add a case that returns without recursing, or change the arguments.",
	"polymorphic-recursion": "A generic function calls itself, or a generic type holds itself, at a type argument built from " +
		"its own type parameter (`grow(Box{inner: x})` inside `fn grow<T>`, `inner: Option<Nested<List<T>>>` inside " +
		"`struct Nested<T>`), directly or through other declarations. Each generic function and type has an instance per type " +
		"argument, and each such instance needs one at a larger argument, without end. Recurse, or hold the type, at its own " +
		"type parameters (`Tree<T>` holding `List<Tree<T>>`) or at arguments that do not grow; a function can recurse " +
		"through a non-generic helper or an interface-typed parameter instead.",
	"recursive-render": "A `Display` or `Debug` impl renders its own receiver through the function it is defining " +
		"(`\"${value}\"` inside `to_string(value)`), which never ends. Render the fields instead.",
	"recursive-protocol": "An `Equatable` `equal`, `Comparable` `compare` or `Hashable` `hash` impl compares or hashes " +
		"its own arguments through the function it is defining, which never ends. Compare or hash the fields, or `derive` the impl.",
	"task-not-settled": "A handle bound from `Task.spawn`, or a list of handles from `Task.spawn_all`, in a `concurrent` block " +
		"is not awaited or cancelled on every path to the block's end. Pass it to `Task.await`, `Task.await_all`, " +
		"`Task.try_await_all`, `Task.outcome` or `Task.cancel`, directly, through a function argument " +
		"(`Iter.each(handles, Task.cancel)`, `Iter.each(handles, fn t -> _ = Task.outcome(t))`) or a pipe " +
		"(`handles |> Task.await_all()`), in both branches of an `if` and in every arm of a `case`, or after them. " +
		"A path that leaves the block early, by `?`, `return`, `break` or `continue`, needs nothing: the block cancels " +
		"its running tasks. Reading the handle (`Task.inspect(t)`, `Iter.count(handles)`), copying it to another name " +
		"(`u = [t]`, `_ = t`), passing it to a function the program declares, or settling it inside a lambda that " +
		"captures it (`check = fn -> Task.outcome(t)`), which may never run, does not settle it.",
	"infinite-iterator": "An `Iter` that never ends is consumed whole (`Iter.to_list`, `Iter.count`, ...), " +
		"so the program never finishes. Limit it first with `Iter.take(n)` or `Iter.take_while`.",
	"reserved-name": "A declaration reuses a name the language reserves for a built-in type (`Int`, `String`, ...). " +
		"Every use of the name still means the built-in, so other errors may follow from this one. Rename the declaration.",
	"boot-result": "An entry file's `boot` must return `Result<App, E>`: the application struct in `Ok`, " +
		"or the error that stops the program. Wrap the struct: `Ok(App{...})`.",
	"invalid-literal": "A typed literal checked at compile time does not parse as its type, and the message says why: " +
		"a backtick literal (``Regex`...` ``, ``Locale`...` ``), or a checked literal in any quotes (`Url\"...\"`), " +
		"whose error points at the character or slot its type refused. Fix the literal's text.",
	"unlisted-currency": "A `Currency.custom` call names a currency its package's `nomi.toml` does not list. " +
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
	"type-argument-count": "A use of a type writes more or fewer type arguments than the type takes. A type declared without " +
		"type parameters (a primitive, a type parameter, or a struct, enum, distinct type or interface declared without them) takes none: " +
		"write `S`, not `S<Int>`. A generic type takes as many as its declaration lists, less any with a default.",
	"interface-value-self": "A function of an interface is used on an interface value, by a call (`Comparable.compare(a, b)`) " +
		"or by an operator that goes through it (`a < b` uses `Comparable.compare`), and the function needs the implementor's " +
		"own type beyond its receiver: a second `self` argument, a `self` result, or a parameter holding `self` values. " +
		"A generic call, an ordering of Lists or `Iter.sort` at an interface type is such a use too, when the type parameter's " +
		"bound has such a function: an interface type meets no `Comparable` bound. " +
		"An interface value does not say which type it holds, and two of them may hold different types, which have no order. " +
		"Take a type parameter bounded by the interface instead, so the values are one type: `fn less<T>(a: T, b: T) -> Bool where T: Comparable`.",
	"opaque-construction": "Code outside an opaque type's defining file builds a value of the type: its call form, its literal, " +
		"a variant, or a function the compiler provides for it, `try_from` or `unchecked`, or it relies on the `TryFrom` impl " +
		"the compiler provides for it: a generic call bounded by `TryFrom` at the type, an impl whose `where` needs it, a " +
		"conversion to a `TryFrom` value, or `TryFrom.try_from` where the type is expected. An opaque type's construction is private " +
		"to its file. Call a public function that returns one instead, such as " +
		"`Int.to_codepoint` for a `Codepoint`.",
	"no-type-member": "`Type.member` names a member the type does not have: no function in its `impl` blocks, no variant and no " +
		"constant by that name. Check the spelling against the help's suggestion, or declare the function in an `impl Type` block.",
	"argument-type-mismatch": "A call passes an argument whose type its parameter does not take. The message names the argument, " +
		"the parameter's type and the argument's. Pass a value of the parameter's type, or convert the value first; the help says " +
		"how when one conversion is right for the two types.",
	"missing-field": "A struct or variant literal leaves out a field that has no default. Write the field in the literal, " +
		"or give it a default in the declaration.",
	"missing-impl-function": "An `impl Iface for T` block lacks a function the interface requires and gives no default for. " +
		"Write the function in the block with the signature the interface declares.",
	"enum-field-not-in-every-variant": "A field is read off a value typed as an enum (`s.w`), and some variant of the enum " +
		"has no such field, so a value of that variant would have nothing to read. The message names the variants that lack it. " +
		"Match the value with `case` and read the field in the arms whose variants have it, or give every variant the field.",
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
