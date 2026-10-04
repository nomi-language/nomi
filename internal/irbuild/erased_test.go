package irbuild

// An interface EXISTENTIAL is the one component kind whose rendering is not an
// identity, so no intern table in this package may be keyed on the rendering.
//
// `kind.nomi()` answers `rt.Dyn` for every existential, by design: erasure is
// what an existential IS. So two interfaces reaching one structural
// constructor in one module produce two identities behind one spelling, and a
// table keyed on that spelling would meet them as one entry.
//
// On legal programs that would mean:
//
//   - `List<Alpha>` and `List<Beta>`: composite.go's internLocal panics with a
//     Go type interned for two Nomi types, a compiler crash.
//   - `Maybe<Alpha>` and `Maybe<Beta>`: the second instantiation silently
//     adopts the first one's def, and the payload then refuses with
//     `existential of another interface (Beta.b wants Beta, got Alpha)`, a
//     reason that is false about the program.
//
// Two plain user interfaces are enough. No stdlib, no generics, no typed
// literal, and no corpus program reaches either shape — so this is a fixture's
// job and not a sweep's.
