//go:build !js

package goside

// The database/sql driver "sqlite" outside the browser: modernc.org/sqlite,
// SQLite translated to Go, so a build needs no C compiler. It has no
// js/wasm port; that build registers sqlite_js.go's driver instead.
import _ "modernc.org/sqlite"
