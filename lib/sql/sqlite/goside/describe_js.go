//go:build js

package goside

import (
	"errors"
	"fmt"
)

// jsDB is Describe's database in the browser: SQLite's official
// WebAssembly build, through its C-style API (sqlite3.capi), as the
// browser driver reaches it (sqlite_js.go). The build compiles in the
// column metadata Describe reads.
type jsDB struct {
	a  *wasmAPI
	db int
	// scratch is two pointers' room, for sqlite3_open_v2's and then each
	// sqlite3_prepare_v2's out-parameters.
	scratch int
}

func openRaw() (rawDB, error) {
	a, err := loadAPI()
	if err != nil {
		return nil, err
	}
	if a.capi.Get("sqlite3_column_table_name").IsUndefined() || a.capi.Get("sqlite3_table_column_metadata").IsUndefined() {
		return nil, errors.New("this SQLite WebAssembly build leaves out the column metadata that describing a statement reads")
	}
	scratch := a.wasm.Call("alloc", 8).Int()
	rc := a.int("sqlite3_open_v2", ":memory:", scratch, openReadWrite|openCreate, nil)
	db := a.peekPtr(scratch)
	if rc != sqliteOK {
		if db != 0 {
			a.call("sqlite3_close_v2", db)
		}
		a.wasm.Call("dealloc", scratch)
		return nil, fmt.Errorf("SQLite result code %s", codeName(rc))
	}
	a.call("sqlite3_extended_result_codes", db, 1)
	return &jsDB{a: a, db: db, scratch: scratch}, nil
}

func (r *jsDB) close() {
	r.a.call("sqlite3_close_v2", r.db)
	r.a.wasm.Call("dealloc", r.scratch)
}

func (r *jsDB) errmsg() string {
	return r.a.text("sqlite3_errmsg", r.db)
}

func (r *jsDB) prepareEach(text string, f func(stmt uintptr, start int) (string, int)) (string, int64) {
	pair := r.a.wasm.Call("allocCString", text, true)
	base, n := pair.Index(0).Int(), pair.Index(1).Int()
	defer r.a.wasm.Call("dealloc", base)
	ppStmt, ppTail := r.scratch, r.scratch+4
	for cur, end := base, base+n; cur < end; {
		start := cur - base
		if rc := r.a.int("sqlite3_prepare_v2", r.db, cur, end-cur, ppStmt, ppTail); rc != sqliteOK {
			offset := int64(-1)
			if o := r.a.int("sqlite3_error_offset", r.db); o >= 0 {
				offset = int64(start) + int64(o)
			}
			return r.errmsg(), offset
		}
		stmt, next := r.a.peekPtr(ppStmt), r.a.peekPtr(ppTail)
		if stmt != 0 {
			msg, offset := f(uintptr(stmt), start)
			r.a.call("sqlite3_finalize", stmt)
			if msg != "" {
				return msg, int64(offset)
			}
		}
		if next <= cur {
			break
		}
		cur = next
	}
	return "", -1
}

func (r *jsDB) step(stmt uintptr) int {
	return r.a.int("sqlite3_step", int(stmt))
}

func (r *jsDB) bindParameterCount(stmt uintptr) int {
	return r.a.int("sqlite3_bind_parameter_count", int(stmt))
}

func (r *jsDB) columnCount(stmt uintptr) int {
	return r.a.int("sqlite3_column_count", int(stmt))
}

func (r *jsDB) columnText(stmt uintptr, i int) string {
	return r.a.text("sqlite3_column_text", int(stmt), i)
}

func (r *jsDB) columnName(stmt uintptr, i int) string {
	return r.a.text("sqlite3_column_name", int(stmt), i)
}

func (r *jsDB) columnDecltype(stmt uintptr, i int) string {
	return r.a.text("sqlite3_column_decltype", int(stmt), i)
}

func (r *jsDB) columnTableName(stmt uintptr, i int) string {
	return r.a.text("sqlite3_column_table_name", int(stmt), i)
}

func (r *jsDB) columnOriginName(stmt uintptr, i int) string {
	return r.a.text("sqlite3_column_origin_name", int(stmt), i)
}

func (r *jsDB) tableColumnMetadata(table, column string) (string, bool, bool, bool) {
	// Five out-parameters: the declared type and collation (char*), then
	// NOT NULL, PRIMARY KEY and AUTOINCREMENT (int), four bytes each.
	out := r.a.wasm.Call("alloc", 20).Int()
	defer r.a.wasm.Call("dealloc", out)
	if rc := r.a.int("sqlite3_table_column_metadata", r.db, nil, table, column, out, out+4, out+8, out+12, out+16); rc != sqliteOK {
		return "", false, false, false
	}
	declared := ""
	if p := r.a.peekPtr(out); p != 0 {
		declared = r.a.wasm.Call("cstrToJs", p).String()
	}
	notNull := r.a.wasm.Call("peek32", out+8).Int() != 0
	primary := r.a.wasm.Call("peek32", out+12).Int() != 0
	return declared, notNull, primary, true
}
