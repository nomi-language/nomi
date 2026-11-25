// Package webtypes writes TypeScript declarations for Nomi types: nomi.d.ts,
// which `nomi build --web` puts beside nomi.js with a typed `call` per
// exported function, and the file `nomi types` writes for a frontend that
// talks to a Nomi backend over HTTP. Both map a Nomi type to the TypeScript
// type of the JSON its ToJson writes, through one Writer, and differ only in
// its Ints: nomi.d.ts writes an Int as `number | bigint` (IntsExact, what
// nomi.js hands JavaScript), `nomi types` as `number` (IntsNumber, what
// JSON.parse yields).
//
//	Int                              number | bigint (nomi.d.ts), number (nomi types)
//	Float                            number
//	String                           string
//	Bool                             boolean
//	Decimal                          string, its exact digits ("12.50")
//	Uuid                             string, lowercase 8-4-4-4-12 hex
//	Instant                          string, RFC 3339 in UTC
//	Date, Time, NaiveDateTime        string
//	List, Vector, Set<T>             T[]
//	Map<String, V>                   Record<string, V>
//	Option<T>                        T | null
//	a Presence<T> field              an optional property of T (name?: T)
//	Presence<T> outside a field      T | null
//	a tuple payload                  [A, B]
//	Unit (a result)                  void
//	a derived struct                 an interface of its fields, by their JSON keys
//	a derived enum                   a union: "Bare" | { Payload: ... }
//	a derived distinct type          its inner type
//	a generic declaration            a generic interface or type; a field of a
//	                                 type parameter is optional when a reached
//	                                 instantiation puts a Presence there
//
// A type that derives FromJson and has no ToJson (the program records no
// impl, in any file, through analysis.ImplementsToJSON) is what a frontend
// sends, so its declaration is the JSON its FromJson reads. It differs only
// in which properties are optional, a question one place answers
// (Writer.fields): a key the derived FromJson fills when it is missing, an
// Option field's (None) and a defaulted field's, is optional there, where
// ToJson always writes it. A Presence field is optional in every
// declaration, since ToJson leaves out an Absent field's key and FromJson
// reads a missing one as Absent.
//
// A generic declaration is written once for all its instantiations, so a
// field whose type is a type parameter (`value: T`) is optional when any
// instantiation the roots reach puts a Presence at that parameter, since
// ToJson leaves the key out for Absent. A parameter passed on as another
// declaration's type argument carries that on: the first pass records each
// Presence argument and each parameter-to-parameter flow (Writer.noteArgs),
// and Seal closes the one over the other. In a declaration only FromJson
// reads, such a field stays required: every instantiation's FromJson takes
// the key, and a Presence reads the null its ToJson writes for Absent.
//
// A declared type is written once, under its Nomi name, and referred to by
// that name. A type whose ToJson is written by hand, in any file of the
// program, is declared `unknown`: the declarations cannot know what its
// impl writes. So is any type with no JSON encoding at all, such as a Map
// with non-String keys.
//
// # Reading the checked program
//
// The writer reads the checked program, not the source. AddProgram indexes
// each module's type declarations with the analysis type its scope holds,
// and a field's type is the checker's, so imports, directory paths and
// aliases resolve as the checker resolved them. Declarations are keyed by
// (file, name), because an origin names a file only within one program
// (every entry's origin is `<entry>`); each Program keeps its own
// origin-to-file map. `nomi types` checks the named files as `nomi check`
// checks their project: each program of the project that loads one of
// them, so a file that reads an application field is checked under the
// entry that boots it (cmd/nomi's typesRoots). It adds every program to
// one Writer and writes the named files' types alone.
//
// # Two passes
//
// The first pass visits the roots (the named files' types the checker
// records as public or internal, not private, or the exported functions'
// signatures) and records each declaration reached, private ones included, in
// order. Seal names them: a Nomi name taken by two declarations, or a
// reserved one (Record, and nomi.d.ts's own types, by Reserve), is
// qualified by the fewest trailing path elements of each file that tell
// them apart. The second pass writes references and declarations under
// those names. A derived impl's JSON keys come from
// analysis.DerivedJSONKeys, the rule the derive synthesis uses. A std type
// maps by name through a table of what std's impls write.
package webtypes
