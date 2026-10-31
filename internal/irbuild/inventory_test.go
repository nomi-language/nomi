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
	{module: "bits"},
	{module: "bitwise"},
	{module: "bool"},
	{module: "bytes"},
	{module: "calendar"},
	{module: "channels"},
	{module: "code_points"},
	{module: "collation"},
	{module: "comparable"},
	// std/compiler's host fns are the analyzer, the VM runner and the LSP
	// hover renderer; internal/compilerhosts answers them.
	{module: "compiler", permanent: true},
	{module: "contains"},
	{module: "currency"},
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
	{module: "fs"},
	{module: "fs_host"},
	{module: "fs_testing"},
	{module: "hash"},
	{module: "hashable"},
	{module: "instant"},
	{module: "int"},
	{module: "io"},
	{module: "iter"},
	{module: "json"},
	{module: "lists"},
	{module: "locale"},
	{module: "localize"},
	{module: "literals"},
	{module: "maps"},
	{module: "matcher"},
	{module: "maybe"},
	{module: "message"},
	{module: "message_format"},
	{module: "money"},
	{module: "money_format"},
	{module: "multiply"},
	{module: "path"},
	{module: "productable"},
	{module: "plural"},
	{module: "startup"},
	{module: "prelude"},
	{module: "process"},
	{module: "random"},
	{module: "ranges"},
	{module: "regex"},
	{module: "results"},
	{module: "reverse"},
	{module: "sets"},
	{module: "sql"},
	{module: "sql_testing"},
	{module: "steppable"},
	{module: "strings"},
	{module: "structs"},
	{module: "subtract"},
	{module: "summable"},
	{module: "supervisors"},
	{module: "tasks"},
	{module: "testing"},
	{module: "timer"},
	{module: "toml"},
	{module: "type"},
	{module: "unit"},
	{module: "uuid"},
	{module: "variants"},
	{module: "vectors"},
}
