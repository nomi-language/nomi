package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// `dbg` and `io.inspect` over a std type whose `impl Debug` is a `host fn`
// (std/dynamic's Dynamic) render through that host, as `Debug.inspect(x)`
// does, and match the golden record of `nomi test`.

const irDbgHostSource = `import {
  std/io
  std/json.Json
}

test "dbg and inspect of a Dynamic" {
  dyn = Json.to_dynamic(Json.String("hi"))
  dbg dyn
  io.inspect(dyn)
  x = dbg Json.to_dynamic(Json.String("four"))
  assert Debug.inspect(x) == "\"four\""
}
`

func TestIRDbgHost_DynamicRendersThroughItsHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbg_host_test.nomi")
	if err := os.WriteFile(path, []byte(irDbgHostSource), 0600); err != nil {
		t.Fatal(err)
	}
	irAssertFixtureVM(t, path)
}
