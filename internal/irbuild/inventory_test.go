package irbuild

// The stdlib inventory: every module the bundled stdlib carries, declared here
// and checked against std.Load() in both directions by inventory_guard_test.go.
// A derived-only report cannot fail, and a written-only list rots invisibly; a
// declared table plus a derivation that must agree with it fails loudly on
// either.

// --- the stdlib half ------------------------------------------------------

// stdModuleRow is one module std.Load() carries. `permanent` marks a module
// whose host functions are the front end itself; no test reads it.
type stdModuleRow struct {
	module    string
	permanent bool
}

// stdlibInventory is every module the bundled stdlib carries: one per
// std/*.nomi file.
var stdlibInventory = []stdModuleRow{
	{module: "add"},
	{module: "assertions"},
	{module: "bool"},
	{module: "bytes"},
	{module: "calendar"},
	{module: "channels"},
	{module: "codepoints"},
	{module: "comparable"},
	// std/compiler's host fns are the analyzer, the VM runner and the LSP
	// hover renderer; internal/compilerhosts answers them.
	{module: "compiler", permanent: true},
	{module: "context"},
	{module: "debug"},
	{module: "decimal"},
	{module: "discrete"},
	{module: "display"},
	{module: "divide"},
	{module: "duration"},
	{module: "dynamic"},
	{module: "equatable"},
	{module: "float"},
	{module: "hashable"},
	{module: "instant"},
	{module: "int"},
	{module: "io"},
	{module: "iter"},
	{module: "json"},
	{module: "lists"},
	{module: "literals"},
	{module: "maps"},
	{module: "maybe"},
	{module: "multiply"},
	{module: "startup"},
	{module: "prelude"},
	{module: "random"},
	{module: "ranges"},
	{module: "regex"},
	{module: "results"},
	{module: "sets"},
	{module: "steppable"},
	{module: "strings"},
	{module: "structs"},
	{module: "subtract"},
	{module: "supervisors"},
	{module: "tasks"},
	{module: "testing"},
	{module: "timer"},
	{module: "toml"},
	{module: "type"},
	{module: "unit"},
	{module: "vectors"},
}
