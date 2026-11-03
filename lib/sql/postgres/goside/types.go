// Package goside is the Go half of the Nomi module postgres: a pool of
// connections, or one connection, to a Postgres database over pgx, with the
// transaction and savepoint bookkeeping std/sql's Conn rules ask for, and
// the description of a schema and its statements that sqlkit's tool asks
// for (describe.go). The Nomi facade (postgres.nomi, describe.nomi) binds
// these functions and turns their plain structs into std/sql's values and
// errors.
//
// Every function that talks to the server takes the calling task's context
// first, which the Go binding fills (spec §19 *The task's context*), so a
// deadline or a cancelled task stops the statement.
//
// The browser build (goside_js.go) has no pgx: a page cannot open a TCP
// connection to a server, so every function there answers that Postgres
// cannot be reached from the browser. This file and describe_types.go are
// the values that cross in every build.
package goside

// Failure kinds. FailNone means no failure.
const (
	FailNone int64 = iota
	FailConnection
	FailConstraint
	FailDatabase
	FailFinished
	FailClosed
	FailTimeout
	FailSerialization
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

// Answer is what a statement answers: the result's columns, the rows, and
// the number of rows a statement that writes changed.
type Answer struct {
	Columns []string
	Rows    [][]Cell
	Changed int64
	Failure Failure
}

// Row is one row as each_row passes it. The facade binds it as RawRow,
// since std/sql names its own Row.
type Row struct {
	Columns []string
	Cells   []Cell
}

// Cell kinds: how a value crosses between Nomi and Go, one per std/sql
// Value variant. A calendar value crosses as its ISO text, an instant as
// Unix nanoseconds in Int, and an array as its Items.
const (
	KindNull int64 = iota
	KindInt
	KindFloat
	KindText
	KindBytes
	KindDecimal
	KindBool
	KindDate
	KindTime
	KindTimestamp
	KindTimestamptz
	KindUuid
	KindJson
	KindArray
)

// Cell is one value, tagged by Kind, since the Go FFI has no projection
// for a Nomi enum.
type Cell struct {
	Kind  int64
	Int   int64
	Float float64
	Text  string
	Bytes []byte
	Items []Item
}

// Item is one element of an array: a Cell that is not itself an array,
// since Postgres arrays of more than one dimension do not cross.
type Item struct {
	Kind  int64
	Int   int64
	Float float64
	Text  string
	Bytes []byte
}

// Notice is a notification, or the failure of waiting for one.
type Notice struct {
	Channel string
	Payload string
	Failure Failure
}
