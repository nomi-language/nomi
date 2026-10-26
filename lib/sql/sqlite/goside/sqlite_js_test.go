//go:build js

package goside

import (
	"strings"
	"syscall/js"
	"testing"
)

// These tests, and the shared ones, run under Node with SQLite's
// WebAssembly build loaded: see testdata/wasm_exec_sqlite.cjs.

// A program that opens a database before the page loaded the build is
// told so, as a connection error.
func TestOpen_BeforeTheBuildIsLoaded(t *testing.T) {
	loaded := js.Global().Get("sqlite3")
	js.Global().Set("sqlite3", js.Undefined())
	defer js.Global().Set("sqlite3", loaded)
	if _, err := Open(":memory:"); err == nil || !strings.Contains(err.Error(), "WebAssembly build is not loaded") {
		t.Errorf("Open answered %v", err)
	}
}

// A path opens a database in the build's own memory file system, which
// lasts as long as the page: the browser build has no files of the
// visitor's to open.
func TestOpen_APathIsInTheBuildsMemory(t *testing.T) {
	c, err := Open("notes.db")
	if err != nil {
		t.Fatal(err)
	}
	if a := Exec(c, 0, 0, []string{"CREATE TABLE IF NOT EXISTS t (n INTEGER); INSERT INTO t VALUES (1)"}, nil); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	Close(c)
	c, err = Open("notes.db")
	if err != nil {
		t.Fatal(err)
	}
	defer Close(c)
	got := Run(c, 0, 0, []string{"SELECT count(*) AS n FROM t"}, nil)
	if got.Failure.Kind != FailNone || len(got.Rows) != 1 || got.Rows[0][0].Int != 1 {
		t.Errorf("the database opened again holds %+v", got)
	}
}
