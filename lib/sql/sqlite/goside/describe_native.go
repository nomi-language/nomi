//go:build !js

package goside

import (
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// nativeDB is Describe's database natively: SQLite's C API as
// modernc.org/sqlite translates it to Go.
type nativeDB struct {
	tls *libc.TLS
	db  uintptr
}

func openRaw() (rawDB, error) {
	tls := libc.NewTLS()
	r := &nativeDB{tls: tls}
	name, err := libc.CString(":memory:")
	if err != nil {
		tls.Close()
		return nil, err
	}
	defer libc.Xfree(tls, name)
	p := libc.Xmalloc(tls, 8)
	defer libc.Xfree(tls, p)
	flags := int32(sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_MEMORY)
	if rc := sqlite3.Xsqlite3_open_v2(tls, name, p, flags, 0); rc != sqlite3.SQLITE_OK {
		tls.Close()
		return nil, errString(rc)
	}
	r.db = *(*uintptr)(cmem(p))
	sqlite3.Xsqlite3_extended_result_codes(tls, r.db, 1)
	return r, nil
}

type rcError string

func (e rcError) Error() string { return string(e) }

func errString(rc int32) error {
	return rcError("SQLite result code " + codeName(int(rc)))
}

func (r *nativeDB) close() {
	sqlite3.Xsqlite3_close_v2(r.tls, r.db)
	r.tls.Close()
}

func (r *nativeDB) errmsg() string {
	return libc.GoString(sqlite3.Xsqlite3_errmsg(r.tls, r.db))
}

func (r *nativeDB) prepareEach(text string, f func(stmt uintptr, start int) (string, int)) (string, int64) {
	base, err := libc.CString(text)
	if err != nil {
		return err.Error(), -1
	}
	defer libc.Xfree(r.tls, base)
	pstmt := libc.Xmalloc(r.tls, 8)
	defer libc.Xfree(r.tls, pstmt)
	ptail := libc.Xmalloc(r.tls, 8)
	defer libc.Xfree(r.tls, ptail)
	cur := base
	end := base + uintptr(len(text))
	for cur < end {
		start := int(cur - base)
		rc := sqlite3.Xsqlite3_prepare_v2(r.tls, r.db, cur, -1, pstmt, ptail)
		if rc != sqlite3.SQLITE_OK {
			offset := int64(-1)
			if o := sqlite3.Xsqlite3_error_offset(r.tls, r.db); o >= 0 {
				offset = int64(start) + int64(o)
			}
			return r.errmsg(), offset
		}
		stmt := *(*uintptr)(cmem(pstmt))
		next := *(*uintptr)(cmem(ptail))
		if stmt != 0 {
			msg, offset := f(stmt, start)
			sqlite3.Xsqlite3_finalize(r.tls, stmt)
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

func (r *nativeDB) step(stmt uintptr) int {
	return int(sqlite3.Xsqlite3_step(r.tls, stmt))
}

func (r *nativeDB) bindParameterCount(stmt uintptr) int {
	return int(sqlite3.Xsqlite3_bind_parameter_count(r.tls, stmt))
}

func (r *nativeDB) columnCount(stmt uintptr) int {
	return int(sqlite3.Xsqlite3_column_count(r.tls, stmt))
}

func (r *nativeDB) columnText(stmt uintptr, i int) string {
	return libc.GoString(sqlite3.Xsqlite3_column_text(r.tls, stmt, int32(i)))
}

func (r *nativeDB) columnName(stmt uintptr, i int) string {
	return libc.GoString(sqlite3.Xsqlite3_column_name(r.tls, stmt, int32(i)))
}

func (r *nativeDB) columnDecltype(stmt uintptr, i int) string {
	return libc.GoString(sqlite3.Xsqlite3_column_decltype(r.tls, stmt, int32(i)))
}

func (r *nativeDB) columnTableName(stmt uintptr, i int) string {
	return libc.GoString(sqlite3.Xsqlite3_column_table_name(r.tls, stmt, int32(i)))
}

func (r *nativeDB) columnOriginName(stmt uintptr, i int) string {
	return libc.GoString(sqlite3.Xsqlite3_column_origin_name(r.tls, stmt, int32(i)))
}

func (r *nativeDB) tableColumnMetadata(table, column string) (string, bool, bool, bool) {
	ctable, _ := libc.CString(table)
	defer libc.Xfree(r.tls, ctable)
	ccolumn, _ := libc.CString(column)
	defer libc.Xfree(r.tls, ccolumn)
	out := libc.Xmalloc(r.tls, 8*5)
	defer libc.Xfree(r.tls, out)
	notNull := out + 16
	primary := out + 20
	rc := sqlite3.Xsqlite3_table_column_metadata(r.tls, r.db, 0, ctable, ccolumn, out, out+8, notNull, primary, out+24)
	if rc != sqlite3.SQLITE_OK {
		return "", false, false, false
	}
	declared := libc.GoString(*(*uintptr)(cmem(out)))
	return declared, *(*int32)(cmem(notNull)) != 0, *(*int32)(cmem(primary)) != 0, true
}

// cmem is the C memory libc allocated at p, which the Go heap does not
// manage, so the address stays valid as long as libc keeps it.
func cmem(p uintptr) unsafe.Pointer {
	return unsafe.Add(nil, p)
}
