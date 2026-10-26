package goside

import "strings"

// Describe applies the migrations, in the order given, to a fresh
// in-memory database, and describes each statement against the schema
// they build. It uses SQLite's column metadata (sqlite3_column_table_name,
// sqlite3_column_origin_name, sqlite3_table_column_metadata), which both
// builds compile in (modernc.org/sqlite natively, SQLite's official
// WebAssembly build in the browser), and sqlite3_error_offset for errors.
// Each build reaches SQLite's C API its own way (describe_native.go,
// describe_js.go); what it asks is here.
func Describe(migrations []Migration, statements []string) Description {
	d := Description{Tables: []Table{}, Statements: []Statement{}, Offset: -1}
	db, err := openRaw()
	if err != nil {
		d.Error = err.Error()
		return d
	}
	defer db.close()
	execAll(db, "PRAGMA foreign_keys = ON")

	created := map[string]string{}
	changed := map[string]string{}
	for _, m := range migrations {
		if msg, offset := execAll(db, m.Text); msg != "" {
			d.Error, d.Migration, d.Offset = msg, m.Path, offset
			return d
		}
		for _, t := range tables(db) {
			if _, ok := created[t.Name]; !ok {
				created[t.Name] = m.Path
			}
			for _, c := range t.Columns {
				key := t.Name + "\x00" + c.Name + "\x00" + c.Declared
				if _, ok := changed[key]; !ok {
					changed[key] = m.Path
				}
			}
		}
	}
	for _, t := range tables(db) {
		t.Migration = created[t.Name]
		for i, c := range t.Columns {
			t.Columns[i].Migration = changed[t.Name+"\x00"+c.Name+"\x00"+c.Declared]
		}
		d.Tables = append(d.Tables, t)
	}
	for _, s := range statements {
		d.Statements = append(d.Statements, describe(db, s))
	}
	return d
}

// rawDB is a fresh in-memory database, opened with extended result codes,
// and the calls of SQLite's C API that Describe makes on it. A statement
// is the address of its sqlite3_stmt; a function that answers text
// answers "" for NULL.
type rawDB interface {
	// prepareEach prepares each statement of text in turn and calls f
	// with it and the byte offset where it starts, then finalizes it. It
	// answers the first error and its byte offset in text: SQLite's
	// message and sqlite3_error_offset for a statement that does not
	// prepare (-1 when it gives none), or f's.
	prepareEach(text string, f func(stmt uintptr, start int) (string, int)) (string, int64)
	step(stmt uintptr) int
	errmsg() string
	bindParameterCount(stmt uintptr) int
	columnCount(stmt uintptr) int
	columnText(stmt uintptr, i int) string
	columnName(stmt uintptr, i int) string
	columnDecltype(stmt uintptr, i int) string
	columnTableName(stmt uintptr, i int) string
	columnOriginName(stmt uintptr, i int) string
	// tableColumnMetadata is sqlite3_table_column_metadata's declared
	// type, NOT NULL and PRIMARY KEY of table.column, and false when
	// SQLite does not know the column.
	tableColumnMetadata(table, column string) (declared string, notNull, primaryKey, ok bool)
	close()
}

// execAll runs every statement of text.
func execAll(db rawDB, text string) (string, int64) {
	return db.prepareEach(text, func(stmt uintptr, start int) (string, int) {
		for {
			switch rc := db.step(stmt); rc {
			case sqliteRow:
				continue
			case sqliteDone:
				return "", 0
			default:
				return db.errmsg(), start
			}
		}
	})
}

// query runs one statement and answers its rows as text.
func query(db rawDB, text string) [][]string {
	var out [][]string
	db.prepareEach(text, func(stmt uintptr, _ int) (string, int) {
		n := db.columnCount(stmt)
		for db.step(stmt) == sqliteRow {
			row := make([]string, n)
			for i := range n {
				row[i] = db.columnText(stmt, i)
			}
			out = append(out, row)
		}
		return "", 0
	})
	return out
}

func quoteText(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func tables(db rawDB) []Table {
	var out []Table
	for _, row := range query(db, "SELECT name, sql FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name") {
		t := Table{Name: row[0], Sql: row[1], Columns: []TableColumn{}}
		for _, c := range query(db, "SELECT name, type, \"notnull\", pk, dflt_value IS NOT NULL FROM pragma_table_info("+quoteText(t.Name)+") ORDER BY cid") {
			t.Columns = append(t.Columns, TableColumn{
				Name:       c[0],
				Declared:   c[1],
				NotNull:    c[2] == "1",
				PrimaryKey: c[3] != "0",
				HasDefault: c[4] == "1",
			})
		}
		out = append(out, t)
	}
	return out
}

// describe prepares one statement and reads its columns and plan.
func describe(db rawDB, text string) Statement {
	s := Statement{Columns: []Column{}, Plan: []string{}, Offset: -1}
	described := false
	msg, offset := db.prepareEach(text, func(stmt uintptr, _ int) (string, int) {
		if described {
			return "a query is one statement; this one has more after it", 0
		}
		described = true
		s.Params = int64(db.bindParameterCount(stmt))
		n := db.columnCount(stmt)
		for i := range n {
			c := Column{
				Name:     db.columnName(stmt, i),
				Declared: db.columnDecltype(stmt, i),
				Table:    db.columnTableName(stmt, i),
				Origin:   db.columnOriginName(stmt, i),
			}
			if c.Table != "" && c.Origin != "" {
				c.NotNull = notNull(db, c.Table, c.Origin)
			}
			s.Columns = append(s.Columns, c)
		}
		return "", 0
	})
	if msg != "" {
		s.Error, s.Offset = msg, offset
		return s
	}
	for _, row := range query(db, "EXPLAIN QUERY PLAN "+text) {
		if len(row) == 4 {
			s.Plan = append(s.Plan, row[3])
		}
	}
	return s
}

// notNull reports whether table.column is declared NOT NULL, or is an
// INTEGER PRIMARY KEY, which is the rowid and never NULL.
func notNull(db rawDB, table, column string) bool {
	declared, notNull, primary, ok := db.tableColumnMetadata(table, column)
	if !ok {
		return false
	}
	return notNull || primary && strings.EqualFold(declared, "INTEGER")
}
