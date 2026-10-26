// Package goside is the Go half of the Nomi module sqlite: a connection to
// one SQLite database over database/sql, with the transaction and savepoint
// bookkeeping std/sql's Conn rules ask for. The Nomi facade (sqlite.nomi)
// binds these functions and turns their plain structs into std/sql's values
// and errors.
//
// The database/sql driver named "sqlite" is modernc.org/sqlite, which needs
// no cgo, in every build but the browser's (sqlite_native.go). A js/wasm
// build registers its own driver under that name instead, over SQLite's
// official WebAssembly build (sqlite_js.go), so both answer the same
// bindings with the same values and errors.
package goside

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cell kinds: how a value crosses between Nomi and Go.
const (
	KindNull int64 = iota
	KindInt
	KindFloat
	KindText
	KindBytes
	// KindDecimal is a decimal's text: what a Decimal is bound as, and
	// what a column declared DECIMAL or NUMERIC reads as.
	KindDecimal
)

// Cell is one value, tagged by Kind, since the Go FFI has no projection
// for a Nomi enum.
type Cell struct {
	Kind  int64
	Int   int64
	Float float64
	Text  string
	Bytes []byte
}

// Failure kinds. FailNone means no failure.
const (
	FailNone int64 = iota
	FailConnection
	FailConstraint
	FailDatabase
	FailFinished
	FailClosed
)

// Constraint kinds, in std/sql's ConstraintKind order.
const (
	ConstraintUnique int64 = iota
	ConstraintForeignKey
	ConstraintNotNull
	ConstraintCheck
)

// Failure is an error as the facade maps it to sql.Error: a Go error
// crosses only as a String, which would lose the constraint.
type Failure struct {
	Kind       int64
	Code       string
	Message    string
	Constraint int64
	Name       string
	Table      string
	Column     string
}

// Answer is what a statement answers: the result's columns, each
// column's declared type ("" for an expression), the rows, and the number
// of rows a statement that writes changed.
type Answer struct {
	Columns []string
	Types   []string
	Rows    [][]Cell
	Changed int64
	Failure Failure
}

// Level is a transaction or savepoint begin opened: its depth (1 for a
// transaction, 2 and more for savepoints) and its token.
type Level struct {
	Depth   int64
	Token   int64
	Failure Failure
}

// Conn is one connection. Every Nomi Conn of it (the connection itself,
// a transaction, a savepoint) shares it and names its own depth and
// token; a transaction or savepoint is open while its token is on the
// stack at its depth.
type Conn struct {
	mu     sync.Mutex
	db     *sql.DB
	conn   *sql.Conn
	closed bool
	stack  []int64
	next   int64
}

var background = context.Background()

// busyTimeout is how long a statement on a file database waits for a lock
// another connection holds before it fails with SQLITE_BUSY.
const busyTimeout = 5 * time.Second

// Open opens the database at path, creating it when it does not exist;
// ":memory:" is a fresh in-memory database that lives as long as the
// connection. Foreign keys are enforced, and a file database waits up to
// busyTimeout for a lock another connection holds.
func Open(path string) (*Conn, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection: an in-memory database is per connection, and a
	// transaction is a property of one.
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(background)
	if err != nil {
		db.Close()
		return nil, err
	}
	pragmas := []string{"PRAGMA foreign_keys = ON"}
	if path != ":memory:" {
		// Another connection to the file may hold its lock for a moment:
		// wait up to busyTimeout for it rather than fail at once.
		pragmas = append(pragmas, fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds()))
	}
	for _, pragma := range pragmas {
		if _, err := conn.ExecContext(background, pragma); err != nil {
			conn.Close()
			db.Close()
			return nil, err
		}
	}
	return &Conn{db: db, conn: conn}, nil
}

// usable is the failure of using the Conn at depth with token: Closed
// after a close, Finished when that transaction or savepoint has ended.
// The caller holds c.mu.
func (c *Conn) usable(depth, token int64) Failure {
	if c.closed {
		return Failure{Kind: FailClosed}
	}
	if depth == 0 {
		return Failure{}
	}
	if int64(len(c.stack)) < depth || c.stack[depth-1] != token {
		return Failure{Kind: FailFinished}
	}
	return Failure{}
}

// runnable is usable for a statement, which must run on the innermost open
// transaction or savepoint: SQLite has one transaction per connection, so a
// statement sent through the connection or an outer transaction while a
// savepoint is open would belong to the savepoint, and roll back with it.
// The caller holds c.mu.
func (c *Conn) runnable(depth, token int64) Failure {
	if f := c.usable(depth, token); f.Kind != FailNone {
		return f
	}
	if int64(len(c.stack)) > depth {
		what := "a transaction is open on this connection; run the statement on the Conn its begin answered"
		if depth > 0 {
			what = "a savepoint is open inside this transaction; run the statement on the Conn its begin answered"
		}
		return Failure{Kind: FailDatabase, Code: "SQLITE_MISUSE", Message: what}
	}
	return Failure{}
}

// Run runs a statement on the Conn at depth with token. parts is the SQL
// around each parameter; each parameter is bound as ?1, ?2, ..., never
// spliced into the text.
func Run(c *Conn, depth, token int64, parts []string, params []Cell) Answer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.runnable(depth, token); f.Kind != FailNone {
		return Answer{Failure: f}
	}
	args, f := values(params)
	if f.Kind != FailNone {
		return Answer{Failure: f}
	}
	query := join(parts)
	rows, err := c.conn.QueryContext(background, query, args...)
	if err != nil {
		return Answer{Failure: c.statementFailure(err, query, args)}
	}
	defer rows.Close()
	answer, err := read(rows, func([]Cell) bool { return true }, true)
	if err != nil {
		rows.Close()
		return Answer{Failure: c.statementFailure(err, query, args)}
	}
	return answer
}

// Exec runs a statement that writes and answers how many rows it changed.
func Exec(c *Conn, depth, token int64, parts []string, params []Cell) Answer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.runnable(depth, token); f.Kind != FailNone {
		return Answer{Failure: f}
	}
	args, f := values(params)
	if f.Kind != FailNone {
		return Answer{Failure: f}
	}
	query := join(parts)
	res, err := c.conn.ExecContext(background, query, args...)
	if err != nil {
		return Answer{Failure: c.statementFailure(err, query, args)}
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Answer{Failure: c.failure(err)}
	}
	return Answer{Changed: n}
}

// Row is one row as each_row passes it. The facade binds it as RawRow, since
// std/sql names its own Row.
type Row struct {
	Columns []string
	Types   []string
	Cells   []Cell
}

// EachRow runs a statement and calls f with each row in order, stopping
// when f returns false. The connection is held while it runs, so f must
// not use the same connection.
func EachRow(c *Conn, depth, token int64, parts []string, params []Cell, f func(Row) bool) Failure {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fl := c.runnable(depth, token); fl.Kind != FailNone {
		return fl
	}
	args, fl := values(params)
	if fl.Kind != FailNone {
		return fl
	}
	query := join(parts)
	rows, err := c.conn.QueryContext(background, query, args...)
	if err != nil {
		return c.statementFailure(err, query, args)
	}
	defer rows.Close()
	var columns, types []string
	_, err = read(rows, func(cells []Cell) bool {
		if columns == nil {
			columns, types = names(rows)
		}
		return f(Row{Columns: columns, Types: types, Cells: cells})
	}, false)
	if err != nil {
		rows.Close()
		return c.statementFailure(err, query, args)
	}
	return Failure{}
}

// Begin starts a transaction on the connection (depth 0), or a savepoint
// inside the transaction or savepoint at depth.
func Begin(c *Conn, depth, token int64) Level {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f := c.usable(depth, token); f.Kind != FailNone {
		return Level{Failure: f}
	}
	if int64(len(c.stack)) != depth {
		what := "a transaction is already open on this connection"
		if depth > 0 {
			what = "a savepoint is already open inside this transaction"
		}
		return Level{Failure: Failure{Kind: FailDatabase, Code: "SQLITE_MISUSE", Message: what}}
	}
	next := depth + 1
	stmt := "BEGIN"
	if depth > 0 {
		stmt = fmt.Sprintf("SAVEPOINT sp%d", next)
	}
	if _, err := c.conn.ExecContext(background, stmt); err != nil {
		return Level{Failure: c.failure(err)}
	}
	c.next++
	c.stack = append(c.stack, c.next)
	return Level{Depth: next, Token: c.next}
}

// Commit commits the transaction at depth 1, or releases the savepoint at
// a deeper one. A transaction or savepoint that has ended is Finished.
func Commit(c *Conn, depth, token int64) Failure {
	c.mu.Lock()
	defer c.mu.Unlock()
	if depth == 0 {
		if c.closed {
			return Failure{Kind: FailClosed}
		}
		return Failure{Kind: FailDatabase, Code: "SQLITE_MISUSE", Message: "commit on a connection that is not a transaction"}
	}
	if f := c.usable(depth, token); f.Kind != FailNone {
		return f
	}
	stmt := "COMMIT"
	if depth > 1 {
		stmt = fmt.Sprintf("RELEASE SAVEPOINT sp%d", depth)
	}
	if _, err := c.conn.ExecContext(background, stmt); err != nil {
		return c.failure(err)
	}
	c.stack = c.stack[:depth-1]
	return Failure{}
}

// Rollback rolls back the transaction at depth 1, or to the savepoint at
// a deeper one. Rolling back one that has ended does nothing.
func Rollback(c *Conn, depth, token int64) Failure {
	c.mu.Lock()
	defer c.mu.Unlock()
	if depth == 0 {
		if c.closed {
			return Failure{Kind: FailClosed}
		}
		return Failure{Kind: FailDatabase, Code: "SQLITE_MISUSE", Message: "rollback on a connection that is not a transaction"}
	}
	f := c.usable(depth, token)
	switch f.Kind {
	case FailFinished:
		return Failure{}
	case FailNone:
	default:
		return f
	}
	if depth == 1 {
		if _, err := c.conn.ExecContext(background, "ROLLBACK"); err != nil {
			return c.failure(err)
		}
	} else {
		name := fmt.Sprintf("sp%d", depth)
		if _, err := c.conn.ExecContext(background, "ROLLBACK TO SAVEPOINT "+name); err != nil {
			return c.failure(err)
		}
		if _, err := c.conn.ExecContext(background, "RELEASE SAVEPOINT "+name); err != nil {
			return c.failure(err)
		}
	}
	c.stack = c.stack[:depth-1]
	return Failure{}
}

// Ping checks the connection.
func Ping(c *Conn, depth, token int64) Failure {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return Failure{Kind: FailClosed}
	}
	if err := c.conn.PingContext(background); err != nil {
		return c.failure(err)
	}
	return Failure{}
}

// Close closes the connection, rolling back a transaction still open.
// Closing it again does nothing.
func Close(c *Conn) Failure {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return Failure{}
	}
	c.closed = true
	var first error
	if len(c.stack) > 0 {
		_, first = c.conn.ExecContext(background, "ROLLBACK")
		c.stack = nil
	}
	if err := c.conn.Close(); err != nil && first == nil {
		first = err
	}
	if err := c.db.Close(); err != nil && first == nil {
		first = err
	}
	if first != nil {
		return Failure{Kind: FailConnection, Message: first.Error()}
	}
	return Failure{}
}

// join writes the SQL with ?1, ?2, ... between the parts.
func join(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("?")
			b.WriteString(strconv.Itoa(i))
		}
		b.WriteString(p)
	}
	return b.String()
}

// values is the parameters as database/sql binds them. A decimal is bound
// as its text, and one SQLite would round is refused (see exactDigits).
func values(params []Cell) ([]any, Failure) {
	out := make([]any, len(params))
	for i, p := range params {
		switch p.Kind {
		case KindDecimal:
			if n := decimalDigits(p.Text); n > exactDigits {
				return nil, mismatch(fmt.Sprintf(
					"parameter ?%d is the Decimal %s, of %d digits: SQLite stores a decimal as a 64-bit float, exact to %d digits, and would round it; keep the amount as INTEGER minor units (cents), or write its text as a String into a TEXT column",
					i+1, p.Text, n, exactDigits))
			}
			out[i] = p.Text
		case KindInt:
			out[i] = p.Int
		case KindFloat:
			out[i] = p.Float
		case KindText:
			out[i] = p.Text
		case KindBytes:
			if p.Bytes == nil {
				out[i] = []byte{}
			} else {
				out[i] = p.Bytes
			}
		default:
			out[i] = nil
		}
	}
	return out, Failure{}
}

func names(rows *sql.Rows) ([]string, []string) {
	columns, _ := rows.Columns()
	types := make([]string, len(columns))
	if cts, err := rows.ColumnTypes(); err == nil {
		for i, ct := range cts {
			types[i] = ct.DatabaseTypeName()
		}
	}
	return columns, types
}

// read reads rows, passing each to each until it returns false; with keep
// it also collects them into the answer.
func read(rows *sql.Rows, each func([]Cell) bool, keep bool) (Answer, error) {
	columns, types := names(rows)
	answer := Answer{Columns: columns, Types: types, Rows: [][]Cell{}}
	dest := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return answer, err
		}
		cells := make([]Cell, len(dest))
		for i, v := range dest {
			c, err := cell(v, columns[i], types[i])
			if err != nil {
				return answer, err
			}
			cells[i] = c
		}
		if keep {
			answer.Rows = append(answer.Rows, cells)
		}
		if !each(cells) {
			break
		}
	}
	return answer, rows.Err()
}

// cell is one scanned value of the column named column. modernc.org/sqlite
// turns the text of a DATE, DATETIME or TIMESTAMP column into a time.Time;
// it is written back as the text SQLite stored, so the facade reads every
// column the same way. A number in a DECIMAL or NUMERIC column is its
// decimal text at the column's scale, or an error when it is not exactly
// a decimal of that scale (see decimalOf).
func cell(v any, column, declared string) (Cell, error) {
	switch v.(type) {
	case int64, float64:
		if scale, ok := decimalScale(declared); ok {
			text, err := decimalOf(v, scale, column, declared)
			if err != nil {
				return Cell{}, err
			}
			return Cell{Kind: KindDecimal, Text: text}, nil
		}
	}
	switch v := v.(type) {
	case nil:
		return Cell{Kind: KindNull}, nil
	case int64:
		return Cell{Kind: KindInt, Int: v}, nil
	case float64:
		return Cell{Kind: KindFloat, Float: v}, nil
	case string:
		return Cell{Kind: KindText, Text: v}, nil
	case []byte:
		return Cell{Kind: KindBytes, Bytes: append([]byte{}, v...)}, nil
	case bool:
		if v {
			return Cell{Kind: KindInt, Int: 1}, nil
		}
		return Cell{Kind: KindInt}, nil
	case time.Time:
		if strings.EqualFold(declared, "DATE") {
			return Cell{Kind: KindText, Text: v.Format("2006-01-02")}, nil
		}
		return Cell{Kind: KindText, Text: v.Format("2006-01-02 15:04:05.999999999")}, nil
	default:
		return Cell{Kind: KindText, Text: fmt.Sprint(v)}, nil
	}
}

// exactDigits is how many decimal digits SQLite keeps exactly in a DECIMAL
// or NUMERIC column. It has no decimal type: such a column has NUMERIC
// affinity, which turns the text '12.50' into the REAL 12.5 and text of more
// than 15 significant digits into the nearest 64-bit float, silently
// ('123456789012345678.91' is stored as 123456789012345680). A float64
// holds every decimal of at most 15 digits so that its shortest form is
// that decimal again, so a value of DECIMAL(15, s) or less reads back
// exactly, and the driver refuses to write or read a longer one.
const exactDigits = 15

// decimalScale reports whether a declared type is DECIMAL or NUMERIC, and
// its scale: s of DECIMAL(p, s), 0 of DECIMAL(p), and -1 when it declares
// none, which reads a value at its shortest scale.
func decimalScale(declared string) (int, bool) {
	name, args, _ := strings.Cut(strings.ToUpper(strings.TrimSpace(declared)), "(")
	switch strings.TrimSpace(name) {
	case "DECIMAL", "NUMERIC":
	default:
		return 0, false
	}
	args, closed := strings.CutSuffix(strings.TrimSpace(args), ")")
	if !closed {
		return -1, true
	}
	_, s, two := strings.Cut(args, ",")
	if !two {
		return 0, true
	}
	scale, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || scale < 0 {
		return -1, true
	}
	return scale, true
}

// decimalDigits counts a decimal text's digits, from its first nonzero
// digit before the point through every digit after it: 4 for 12.50, 2 for
// 0.05.
func decimalDigits(text string) int {
	whole, frac, _ := strings.Cut(strings.TrimLeft(text, "+-"), ".")
	return len(strings.TrimLeft(whole, "0")) + len(frac)
}

// decimalOf is the decimal text of a number SQLite stored in a column
// declared DECIMAL or NUMERIC, at the column's scale (at the float's
// shortest scale for a column that declares none). A float is read by its
// shortest form, which is the decimal written when that decimal had at
// most exactDigits digits. A value with more decimals than the scale, or
// more digits than exactDigits at the scale, is not exactly the decimal
// written (or not one the column declares), and is an error naming the
// column.
func decimalOf(v any, scale int, column, declared string) (string, error) {
	var text string
	switch v := v.(type) {
	case int64:
		text = strconv.FormatInt(v, 10)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", mismatchError(fmt.Sprintf("the column `%s` is %s and holds %v, which is not a decimal", column, declared, v))
		}
		text = strconv.FormatFloat(v, 'f', -1, 64)
	}
	sign, rest := "", text
	if after, ok := strings.CutPrefix(text, "-"); ok {
		sign, rest = "-", after
	}
	whole, frac, _ := strings.Cut(rest, ".")
	if scale < 0 {
		scale = len(frac)
	}
	if len(frac) > scale {
		return "", mismatchError(fmt.Sprintf(
			"the column `%s` is %s and holds %s, which has more than %d decimals: SQLite does not round a DECIMAL column to its scale, so round the value before writing it",
			column, declared, text, scale))
	}
	if n := len(strings.TrimLeft(whole, "0")) + scale; n > exactDigits {
		return "", mismatchError(fmt.Sprintf(
			"the column `%s` is %s and holds %s, of %d digits at its scale, more than the %d SQLite keeps exactly: it stores a DECIMAL column as a 64-bit float, which rounds a longer value; declare the column DECIMAL(p, s) with p at most %d, or keep the amount as INTEGER minor units (cents)",
			column, declared, text, n, exactDigits, exactDigits))
	}
	if scale == 0 {
		return sign + whole, nil
	}
	return sign + whole + "." + frac + strings.Repeat("0", scale-len(frac)), nil
}

// mismatchError is a value the driver refuses to read: SQLITE_MISMATCH,
// SQLite's code for a value of the wrong type.
type mismatchError string

func (e mismatchError) Error() string { return string(e) }

func mismatch(message string) Failure {
	return Failure{Kind: FailDatabase, Code: "SQLITE_MISMATCH", Message: message}
}

// failure maps an error that no one statement caused to a Failure.
func (c *Conn) failure(err error) Failure {
	return c.statementFailure(err, "", nil)
}

// statementFailure maps the error of running query with args to a
// Failure. A constraint error keeps the constraint, named as Postgres names
// it (see constraints.go): SQLite's message names the columns of a UNIQUE,
// PRIMARY KEY or NOT NULL constraint and a CHECK's name or expression, and
// nothing of a foreign key, which is found from the statement. The caller
// holds c.mu.
func (c *Conn) statementFailure(err error, query string, args []any) Failure {
	var me mismatchError
	if errors.As(err, &me) {
		return mismatch(string(me))
	}
	var se reportedError
	if !errors.As(err, &se) {
		return Failure{Kind: FailDatabase, Code: "", Message: err.Error()}
	}
	code := se.Code()
	msg := message(se)
	f := Failure{Kind: FailDatabase, Code: codeName(code), Message: msg}
	switch code {
	case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
		f.Kind, f.Constraint = FailConstraint, ConstraintUnique
		table, columns := constrained(msg)
		f.Table, f.Column = table, strings.Join(columns, ",")
		f.Name = c.uniqueName(table, columns, code == sqliteConstraintPrimaryKey)
	case sqliteConstraintNotNull:
		f.Kind, f.Constraint = FailConstraint, ConstraintNotNull
		table, columns := constrained(msg)
		f.Table, f.Column = table, strings.Join(columns, ",")
		if len(columns) == 1 {
			f.Name = c.notNullName(table, columns[0])
		}
	case sqliteConstraintForeignKey:
		f.Kind, f.Constraint = FailConstraint, ConstraintForeignKey
		if v, ok := c.foreignKeyViolation(query, args); ok {
			f.Table, f.Column = v.table, strings.Join(v.columns, ",")
			f.Name = c.foreignKeyName(v)
		}
	case sqliteConstraintCheck:
		f.Kind, f.Constraint = FailConstraint, ConstraintCheck
		_, after, _ := strings.Cut(msg, "CHECK constraint failed: ")
		f.Name, f.Table, f.Column = c.checkName(strings.TrimSpace(after), target(query))
	case sqliteBusy, sqliteLocked:
		f.Kind = FailDatabase
	case sqliteCantOpen, sqliteNotADB, sqliteIOErr:
		f.Kind = FailConnection
	}
	return f
}

// reportedError is an error SQLite reported: its text, as
// modernc.org/sqlite writes it (sqlite3_errstr, then sqlite3_errmsg when it
// says more, then the code in parentheses), and its extended result code.
// modernc.org/sqlite's *Error is one, and so is the browser driver's.
type reportedError interface {
	error
	Code() int
}

// message is the error's text without the code modernc.org/sqlite
// appends.
func message(se reportedError) string {
	msg := se.Error()
	if i := strings.LastIndex(msg, " ("); i >= 0 && strings.HasSuffix(msg, ")") {
		msg = msg[:i]
	}
	// modernc.org/sqlite writes sqlite3_errstr before sqlite3_errmsg.
	for _, prefix := range []string{"SQL logic error: ", "constraint failed: "} {
		if after, ok := strings.CutPrefix(msg, prefix); ok {
			msg = after
		}
	}
	return msg
}

// constrained reads "UNIQUE constraint failed: users.email, users.name"
// as the table and its columns.
func constrained(msg string) (string, []string) {
	_, list, ok := strings.Cut(msg, "constraint failed: ")
	if !ok {
		return "", nil
	}
	var table string
	var columns []string
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		t, col, ok := strings.Cut(item, ".")
		if !ok {
			continue
		}
		table = t
		columns = append(columns, col)
	}
	return table, columns
}

// uniqueIndex is the name of the unique index of table over exactly
// columns that the schema made with CREATE UNIQUE INDEX, or "" when there
// is none (SQLite makes its own, sqlite_autoindex_..., for a UNIQUE or
// PRIMARY KEY clause).
func (c *Conn) uniqueIndex(table string, columns []string) string {
	if table == "" {
		return ""
	}
	rows, err := c.conn.QueryContext(background, "SELECT name, \"unique\" FROM pragma_index_list(?1)", table)
	if err != nil {
		return ""
	}
	var indexes []string
	for rows.Next() {
		var name string
		var unique int64
		if rows.Scan(&name, &unique) == nil && unique == 1 {
			indexes = append(indexes, name)
		}
	}
	rows.Close()
	for _, index := range indexes {
		if strings.HasPrefix(index, "sqlite_autoindex_") {
			continue
		}
		cols, err := c.conn.QueryContext(background, "SELECT name FROM pragma_index_info(?1) ORDER BY seqno", index)
		if err != nil {
			continue
		}
		var got []string
		for cols.Next() {
			var name sql.NullString
			if cols.Scan(&name) == nil {
				got = append(got, name.String)
			}
		}
		cols.Close()
		if sameColumns(got, columns) {
			return index
		}
	}
	return ""
}
