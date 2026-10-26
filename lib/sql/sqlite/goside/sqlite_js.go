//go:build js

package goside

// The database/sql driver "sqlite" in the browser: SQLite's official
// WebAssembly build (sqlite.org's sqlite-wasm), reached through its C-style
// API (sqlite3.capi) with syscall/js. modernc.org/sqlite has no js/wasm
// port, so this driver stands in for it, and answers what it answers: the
// same values from Next (a DATE, DATETIME or TIMESTAMP column's text parsed
// as modernc.org/sqlite parses it), the same upper-case declared types, and
// errors with the same text and extended result codes (wasmError), so
// driver.go maps them to the same Failures.
//
// The page loads the build and hands it over before the program runs:
//
//	importScripts("sqlite3.js")           // or import sqlite3InitModule from "sqlite3.mjs"
//	globalThis.sqlite3 = await sqlite3InitModule()
//
// Loading is asynchronous and a Go call from JavaScript cannot wait for it,
// so a program that opens a database before the build is loaded gets
// Err(.Connection) saying so.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"syscall/js"
	"time"
)

func init() {
	sql.Register("sqlite", wasmDriver{})
}

// notLoaded is the error of opening a database before the page loaded the
// build.
const notLoaded = "SQLite's WebAssembly build is not loaded: the page must set globalThis.sqlite3 to what sqlite3InitModule() resolves to before the program opens a database"

// Open flags, as sqlite3.h numbers them: what modernc.org/sqlite opens with.
const (
	openReadWrite = 0x00000002
	openCreate    = 0x00000004
	openURI       = 0x00000040
	openFullMutex = 0x00010000
)

// Column types, as sqlite3_column_type answers them.
const (
	typeInteger = 1
	typeFloat   = 2
	typeText    = 3
	typeBlob    = 4
	typeNull    = 5
)

// wasmAPI is the loaded build: its C API and its wasm memory helpers.
type wasmAPI struct {
	capi, wasm js.Value
	// dealloc is SQLITE_WASM_DEALLOC, the destructor that frees memory
	// the helpers allocated once SQLite is done with it.
	dealloc js.Value
}

func loadAPI() (*wasmAPI, error) {
	s := js.Global().Get("sqlite3")
	if s.Type() != js.TypeObject {
		return nil, errors.New(notLoaded)
	}
	a := &wasmAPI{capi: s.Get("capi"), wasm: s.Get("wasm")}
	if a.capi.Type() != js.TypeObject || a.wasm.Type() != js.TypeObject {
		return nil, errors.New(notLoaded)
	}
	a.dealloc = a.capi.Get("SQLITE_WASM_DEALLOC")
	return a, nil
}

func (a *wasmAPI) call(name string, args ...any) js.Value {
	return a.capi.Call(name, args...)
}

func (a *wasmAPI) int(name string, args ...any) int {
	return a.call(name, args...).Int()
}

// text is a C function's string result, "" for NULL.
func (a *wasmAPI) text(name string, args ...any) string {
	v := a.call(name, args...)
	if v.Type() != js.TypeString {
		return ""
	}
	return v.String()
}

func (a *wasmAPI) peekPtr(p int) int {
	return a.wasm.Call("peekPtr", p).Int()
}

// wasmError is an error SQLite reported, written as modernc.org/sqlite
// writes its Error (errstr below), with the extended result code.
type wasmError struct {
	msg  string
	code int
}

func (e *wasmError) Error() string { return e.msg }
func (e *wasmError) Code() int     { return e.code }

// errstr is modernc.org/sqlite's conn.errstr: the code's text, the
// connection's message when it says more, and the code.
func (c *wasmConn) errstr(rc int) error {
	str := c.a.text("sqlite3_errstr", rc)
	msg := c.a.text("sqlite3_errmsg", c.db)
	busy := ""
	if rc == sqliteBusy {
		busy = " (SQLITE_BUSY)"
	}
	if msg == str {
		return &wasmError{msg: fmt.Sprintf("%s (%v)%s", str, rc, busy), code: rc}
	}
	return &wasmError{msg: fmt.Sprintf("%s: %s (%v)%s", str, msg, rc, busy), code: rc}
}

type wasmDriver struct{}

// Open opens the database name names, as modernc.org/sqlite opens it:
// read-write, created when missing, a URI filename allowed, and extended
// result codes on.
func (wasmDriver) Open(name string) (driver.Conn, error) {
	a, err := loadAPI()
	if err != nil {
		return nil, err
	}
	// Two pointers' room, for sqlite3_open_v2's and then each
	// sqlite3_prepare_v2's out-parameters.
	scratch := a.wasm.Call("alloc", 8).Int()
	rc := a.int("sqlite3_open_v2", name, scratch, openReadWrite|openCreate|openFullMutex|openURI, nil)
	db := a.peekPtr(scratch)
	if rc != sqliteOK {
		msg := a.text("sqlite3_errstr", rc)
		if db != 0 {
			msg = a.text("sqlite3_errmsg", db)
			a.call("sqlite3_close_v2", db)
		}
		a.wasm.Call("dealloc", scratch)
		return nil, &wasmError{msg: fmt.Sprintf("%s (%v)", msg, rc), code: rc}
	}
	a.call("sqlite3_extended_result_codes", db, 1)
	return &wasmConn{a: a, db: db, scratch: scratch}, nil
}

// wasmConn is one SQLite connection in the build's memory.
type wasmConn struct {
	a       *wasmAPI
	db      int
	scratch int
}

var (
	_ driver.ExecerContext  = (*wasmConn)(nil)
	_ driver.QueryerContext = (*wasmConn)(nil)
)

func (c *wasmConn) Close() error {
	if c.db == 0 {
		return nil
	}
	rc := c.a.int("sqlite3_close_v2", c.db)
	c.a.wasm.Call("dealloc", c.scratch)
	c.db = 0
	if rc != sqliteOK {
		return &wasmError{msg: fmt.Sprintf("%s (%v)", c.a.text("sqlite3_errstr", rc), rc), code: rc}
	}
	return nil
}

// Begin is unused: driver.go begins with BEGIN and SAVEPOINT statements.
func (c *wasmConn) Begin() (driver.Tx, error) {
	return nil, errors.New("sqlite: begin a transaction with a BEGIN statement")
}

// Prepare answers a statement that runs its SQL on the connection when
// executed, as ExecContext and QueryContext run it.
func (c *wasmConn) Prepare(query string) (driver.Stmt, error) {
	return &wasmStmt{c: c, query: query}, nil
}

type wasmStmt struct {
	c     *wasmConn
	query string
}

func (s *wasmStmt) Close() error  { return nil }
func (s *wasmStmt) NumInput() int { return -1 }

func (s *wasmStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.c.ExecContext(context.Background(), s.query, named(args))
}

func (s *wasmStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.c.QueryContext(context.Background(), s.query, named(args))
}

func named(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}

// statements prepares each statement of query in turn and calls f with
// it, finalizing it after; f answers false to stop. A statement that does
// not prepare is SQLite's error. f keeps a statement it answers kept for,
// and finalizes it itself.
func (c *wasmConn) statements(query string, f func(stmt int) (more, keep bool, err error)) error {
	pair := c.a.wasm.Call("allocCString", query, true)
	base, n := pair.Index(0).Int(), pair.Index(1).Int()
	defer c.a.wasm.Call("dealloc", base)
	ppStmt, ppTail := c.scratch, c.scratch+4
	for cur, end := base, base+n; cur < end; {
		if rc := c.a.int("sqlite3_prepare_v2", c.db, cur, end-cur, ppStmt, ppTail); rc != sqliteOK {
			return c.errstr(rc)
		}
		stmt, next := c.a.peekPtr(ppStmt), c.a.peekPtr(ppTail)
		if stmt != 0 {
			more, keep, err := f(stmt)
			if !keep {
				c.a.call("sqlite3_finalize", stmt)
			}
			if err != nil || !more {
				return err
			}
		}
		if next <= cur {
			break
		}
		cur = next
	}
	return nil
}

// bind binds args to stmt's parameters, as modernc.org/sqlite binds them:
// ?NNN to the argument of ordinal NNN, a name to the argument of that
// name, and a bare ? to the argument at its place.
func (c *wasmConn) bind(stmt int, args []driver.NamedValue) error {
	n := c.a.int("sqlite3_bind_parameter_count", stmt)
	for i := 1; i <= n; i++ {
		name := c.a.text("sqlite3_bind_parameter_name", stmt, i)
		var v driver.NamedValue
		found := false
		for _, v = range args {
			if name != "" {
				if (name[0] == '?' || name[0] == '$') && name[1:] == strconv.Itoa(v.Ordinal) {
					found = true
					break
				}
				if name[1:] == v.Name {
					found = true
					break
				}
			} else if v.Ordinal == i {
				found = true
				break
			}
		}
		if !found {
			if name != "" {
				return fmt.Errorf("missing named argument %q", name[1:])
			}
			return fmt.Errorf("missing argument with index %d", i)
		}
		var rc int
		switch x := v.Value.(type) {
		case int64:
			rc = c.a.int("sqlite3_bind_int64", stmt, i, js.Global().Call("BigInt", strconv.FormatInt(x, 10)))
		case float64:
			rc = c.a.int("sqlite3_bind_double", stmt, i, x)
		case bool:
			b := 0
			if x {
				b = 1
			}
			rc = c.a.int("sqlite3_bind_int", stmt, i, b)
		case string:
			rc = c.bindText(stmt, i, x)
		case time.Time:
			rc = c.bindText(stmt, i, x.String())
		case []byte:
			if len(x) == 0 {
				rc = c.a.int("sqlite3_bind_zeroblob", stmt, i, 0)
				break
			}
			buf := js.Global().Get("Uint8Array").New(len(x))
			js.CopyBytesToJS(buf, x)
			p := c.a.wasm.Call("allocFromTypedArray", buf).Int()
			rc = c.a.int("sqlite3_bind_blob", stmt, i, p, len(x), c.a.dealloc)
		case nil:
			// The build declares sqlite3_bind_null's result undefined
			// (3.54.1), so it answers no code.
			if v := c.a.call("sqlite3_bind_null", stmt, i); v.Type() == js.TypeNumber {
				rc = v.Int()
			}
		default:
			return fmt.Errorf("sqlite: invalid driver.Value type %T", x)
		}
		if rc != sqliteOK {
			return c.errstr(rc)
		}
	}
	return nil
}

// bindText binds s as text. The build's sqlite3_bind_text wrapper fails
// on a JavaScript string (3.54.1 reads an undefined variable on that
// path), so the text is copied into the build's memory here and bound by
// pointer and length, for SQLite to free.
func (c *wasmConn) bindText(stmt, i int, s string) int {
	pair := c.a.wasm.Call("allocCString", s, true)
	return c.a.int("sqlite3_bind_text", stmt, i, pair.Index(0).Int(), pair.Index(1).Int(), c.a.dealloc)
}

// ExecContext runs every statement of query, as modernc.org/sqlite's
// exec does, and answers the changes the last one made.
func (c *wasmConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	res := wasmResult{}
	err := c.statements(query, func(stmt int) (bool, bool, error) {
		if c.a.int("sqlite3_bind_parameter_count", stmt) != 0 {
			if err := c.bind(stmt, args); err != nil {
				return false, false, err
			}
		}
		switch rc := c.a.int("sqlite3_step", stmt); rc & 0xff {
		case sqliteDone, sqliteRow:
			res = wasmResult{changed: int64(c.a.int("sqlite3_changes", c.db))}
			return true, false, nil
		default:
			return false, false, c.errstr(rc)
		}
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

type wasmResult struct{ changed int64 }

func (r wasmResult) LastInsertId() (int64, error) {
	return 0, errors.New("sqlite: LastInsertId is not supported")
}
func (r wasmResult) RowsAffected() (int64, error) { return r.changed, nil }

// QueryContext runs query's first statement and answers its rows, the
// first already stepped to, as modernc.org/sqlite's query does: a query
// is one statement.
func (c *wasmConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	var rows *wasmRows
	err := c.statements(query, func(stmt int) (bool, bool, error) {
		if c.a.int("sqlite3_bind_parameter_count", stmt) != 0 {
			if err := c.bind(stmt, args); err != nil {
				return false, false, err
			}
		}
		rc := c.a.int("sqlite3_step", stmt)
		switch rc & 0xff {
		case sqliteRow, sqliteDone:
		default:
			return false, false, c.errstr(rc)
		}
		rows = &wasmRows{c: c, stmt: stmt, empty: rc&0xff == sqliteDone}
		n := c.a.int("sqlite3_column_count", stmt)
		rows.columns = make([]string, n)
		rows.types = make([]string, n)
		for i := range n {
			rows.columns[i] = c.a.text("sqlite3_column_name", stmt, i)
			rows.types[i] = strings.ToUpper(c.a.text("sqlite3_column_decltype", stmt, i))
		}
		return false, true, nil
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return &wasmRows{c: c, empty: true}, nil
	}
	return rows, nil
}

// wasmRows is a query's rows, read one step at a time.
type wasmRows struct {
	c       *wasmConn
	stmt    int
	columns []string
	types   []string
	empty   bool
	stepped bool
}

func (r *wasmRows) Columns() []string { return r.columns }

func (r *wasmRows) ColumnTypeDatabaseTypeName(i int) string { return r.types[i] }

func (r *wasmRows) Close() error {
	if r.stmt != 0 {
		r.c.a.call("sqlite3_finalize", r.stmt)
		r.stmt = 0
	}
	return nil
}

func (r *wasmRows) Next(dest []driver.Value) error {
	if r.empty || r.stmt == 0 {
		return io.EOF
	}
	if r.stepped {
		switch rc := r.c.a.int("sqlite3_step", r.stmt); rc & 0xff {
		case sqliteRow:
		case sqliteDone:
			r.empty = true
			return io.EOF
		default:
			return r.c.errstr(rc)
		}
	}
	r.stepped = true
	a, stmt := r.c.a, r.stmt
	for i := range dest {
		switch a.int("sqlite3_column_type", stmt, i) {
		case typeInteger:
			// Read as text, which SQLite writes exactly, since a 64-bit
			// integer crosses from JavaScript only as a BigInt.
			n, err := strconv.ParseInt(a.text("sqlite3_column_text", stmt, i), 10, 64)
			if err != nil {
				return err
			}
			dest[i] = n
		case typeFloat:
			dest[i] = a.call("sqlite3_column_double", stmt, i).Float()
		case typeText:
			v := a.text("sqlite3_column_text", stmt, i)
			switch r.types[i] {
			case "DATE", "DATETIME", "TIMESTAMP":
				dest[i], _ = parseTime(v)
			default:
				dest[i] = v
			}
		case typeBlob:
			n := a.int("sqlite3_column_bytes", stmt, i)
			p := a.int("sqlite3_column_blob", stmt, i)
			if p == 0 || n == 0 {
				dest[i] = []byte(nil)
				break
			}
			b := make([]byte, n)
			js.CopyBytesToGo(b, a.wasm.Call("heap8u").Call("subarray", p, p+n))
			dest[i] = b
		default:
			dest[i] = nil
		}
	}
	return nil
}

// parseTimeFormats and parseTime are modernc.org/sqlite's: the text of a
// DATE, DATETIME or TIMESTAMP column it reads as a time.Time.
var parseTimeFormats = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
}

func parseTime(s string) (any, bool) {
	if v, ok := parseTimeString(s, strings.Index(s, "m=")); ok {
		return v, true
	}
	ts := strings.TrimSuffix(s, "Z")
	for _, f := range parseTimeFormats {
		if t, err := time.Parse(f, ts); err == nil {
			return t, true
		}
	}
	return s, false
}

func parseTimeString(s0 string, x int) (any, bool) {
	s := s0
	if x > 0 {
		s = s[:x]
	}
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", s); err == nil {
		return t, true
	}
	return s0, false
}
