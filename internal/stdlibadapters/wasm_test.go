package stdlibadapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// TestRtFuncs_WasmModules holds std/wasm/host's rows to what they do
// through the generated adapters: a module compiles or is refused, `run`
// answers bytes, and an instance's calls, runs, memory reads and writes,
// size and drop act on the one instance. The memory an import is handed needs a
// Nomi function to hand it to; std/wasm's attached tests run those rows,
// and internal/stdwasm's tests run each row over the committed modules.
func TestRtFuncs_WasmModules(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	fr := rt.NewFrame(context.Background())
	call := func(key string, args ...any) any {
		t.Helper()
		v, err := table[key](fr, args)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	ok := func(key string, v any) any {
		t.Helper()
		r, tag, err := hostadapt.Variant(v, "Result")
		if err != nil || tag != "Ok" {
			t.Fatalf("%s answered %s, want Ok", key, canonText(v, false))
		}
		return hostadapt.Payload(r)
	}
	text := func(v any) string { return canonText(v, false) }
	want := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s is %s, want %s", what, got, want)
		}
	}
	fixture := func(name string) rt.Bytes {
		data, err := os.ReadFile(filepath.Join("..", "stdwasm", "testdata", name+".wasm"))
		if err != nil {
			t.Fatal(err)
		}
		return rt.Bytes(data)
	}

	want("bytes that are no module", text(call("wasm/host.compile", rt.Bytes("not wasm"))),
		`results.Result/4.Err{0=String("invalid magic number")}`)
	upper := ok("wasm/host.compile", call("wasm/host.compile", fixture("upper")))
	want("a run", text(call("wasm/host.run", upper, "upper", rt.Bytes("abc"), int64(1<<24))), `results.Result/4.Ok{0=Bytes("ABC")}`)
	want("a run of no export", text(call("wasm/host.run", upper, "lower", rt.Bytes("abc"), int64(1<<24))),
		`results.Result/4.Err{0=[String("no_export"), String("lower")]}`)

	values := ok("wasm/host.compile", call("wasm/host.compile", fixture("values")))
	none := rtList()
	inst := ok("wasm/host.instantiate", call("wasm/host.instantiate", values, none, none, none, none, none, none, int64(1<<20)))
	want("a run on an instance with no alloc", text(call("wasm/host.run_on", inst, "add_i32", rt.Bytes("x"))),
		`results.Result/4.Err{0=[String("no_export"), String("alloc")]}`)
	want("a call", text(call("wasm/host.call", inst, "add_i32", rtList(int64(0), int64(2), int64(0), int64(3)))),
		`results.Result/4.Ok{0=[Int(0), Int(5)]}`)
	want("a write", text(call("wasm/host.write", inst, int64(4), rt.Bytes("hi"))), `results.Result/4.Ok{0=Unit}`)
	want("a read", text(call("wasm/host.read", inst, int64(4), int64(2))), `results.Result/4.Ok{0=Bytes("hi")}`)
	want("a read past the end", text(call("wasm/host.read", inst, int64(65535), int64(2))),
		`results.Result/4.Err{0=[String("out_of_range"), String("65536")]}`)
	want("the size", text(call("wasm/host.size", inst)), `results.Result/4.Ok{0=Int(65536)}`)
	want("a trap", text(call("wasm/host.call", inst, "unreachable", rtList())),
		`results.Result/4.Err{0=[String("trapped"), String("unreachable")]}`)
	want("a call after it", text(call("wasm/host.call", inst, "ready", rtList())),
		`results.Result/4.Err{0=[String("dead"), String("unreachable")]}`)
	call("wasm/host.drop", inst)
	want("the size once dropped", text(call("wasm/host.size", inst)), `results.Result/4.Err{0=[String("dropped")]}`)
}
