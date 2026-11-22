// Package effects is the one rule that names an effect and the one
// check that holds effects to lists, shared by everything that holds a
// program to them: the lists of nomi.toml's [effects.self] and
// [effects.deps] tables and an embedding host's lists in vmhost, and
// a host's list in vmrunner, which links no front end. It imports nothing
// of the compiler's, so the checker (which validates nomi.toml's lists),
// vmhost and vmrunner all call it.
//
// Effect is the word a user reads: a function is pure, or it has effects.
// An effect is a call internal/vm's reach walk reports (vm.ReachedEffects):
// a call neither pure nor held (one that acts only through a value its
// operand already holds), whether it acts on something outside the machine
// or makes state inside it. Its name is the std module the code outside
// std called, as an import writes it (`std::fs`); for a crossing no std
// module declares, a function an embedding Go program supplies, `host:`
// and the crossing's key. A `dbg` prints and is an effect the walk
// reports, but no list names it, since `nomi build` refuses a program that
// holds one: it is named `dbg`, of neither of those forms (Listed), and
// every list allows it.
//
// A list holds a call by its name (Allows). An entry of nomi.toml's lists
// is a std name (NameProblem): a `host:` name is a function only the
// program that supplies it can allow, through its own list
// (CheckHostList). A std name must be a std module a program can import
// (UnknownStdName), named in std_names_gen.go, which `go generate
// ./internal/stdlibadapters` writes from std and TestGeneratedFileIsCurrent
// holds current; a misspelled one is an error naming the nearest
// (internal/suggest), for nomi.toml's lists and for every host's alike.
//
// How a refusal reads is shared too: Message is its headline, EffectList
// how a sentence names one effect or several,
// HostAddHint and RemoveHint its help, ReachedLine the line an editor's
// hover shows on a call ("Has effects: `std::fs`."), FunctionReach the
// lines it shows on a function and its calls, UsesLine the line it adds
// there for the app fields the function uses, which no list holds, and
// vm.ReachText says how the walk reached the call. vmhost renders them as
// diagnostics at the call's source; vmrunner, which has no source, renders
// them at the file and line the image records.
package effects
