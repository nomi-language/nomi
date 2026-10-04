// Package stdlibadapters holds the generated host-function adapters for every
// stdlib host function: one Go function per row of internal/stdlibbindings'
// two lists that calls the row's Go function with rt values and no
// reflection. Funcs rows are the Go-backed modules (std/calendar,
// std/random, std/regex), whose adapters recover a panic into the
// marshaller's error text; RtFuncs rows are rt's primitives, whose adapters
// let a panic unwind to the engine (see stdlibbindings.Binding.PanicsPropagate).
//
// adapters_gen.go is generated from two inputs and nothing else: the binding
// table, which names each Go symbol, and the `host fn` declarations in
// std/*.nomi, which say what each operand and result is. The table stays the
// one hand-maintained list; this package adds none. TestGeneratedFileIsCurrent
// regenerates the file and fails when it differs.
//
// The VM binds this table on every machine (internal/vm/hostcall.go) and
// calls a stdlib crossing through it. The adapters are held to recorded
// outcomes: the Funcs rows by TestParity_EveryBindingReproducesItsRecordedOutcomes
// (testdata/funcs.golden), the RtFuncs rows by
// TestRtFuncs_AdaptersReproduceTheRecordedHosts (testdata/rtfuncs.golden) and
// TestRtFuncs_NewRowsReproduceTheirRecordedOutcomes (testdata/builtins.golden).
package stdlibadapters

//go:generate go run ./gen
