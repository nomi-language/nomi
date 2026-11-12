package goside

// What Describe answers, in every build: describe.nomi binds it, so the
// browser build declares the same types (goside_js.go).

// Migration is one migration file: its path and its SQL.
type Migration struct {
	Path string
	Text string
}

// TableColumn is one column of a table as the schema declares it: its type
// as Postgres writes it (format_type, `numeric(12,2)`), its NOT NULL, whether
// it is in the primary key and has a default, the values an enum type or a
// one-column `CHECK (column IN (...))` allows, and the migration that last
// created or changed it.
type TableColumn struct {
	Name       string
	TypeName   string
	NotNull    bool
	PrimaryKey bool
	HasDefault bool
	Values     []string
	Migration  string
}

// Table is one table of the current schema: its name, its columns and the
// migration that created it.
type Table struct {
	Name      string
	Columns   []TableColumn
	Migration string
}

// Column is one result column of a statement: its name, its type as
// Postgres writes it, the table column it reads ("" and "" for an
// expression; for a view's column, the table column the view's column
// reads), and whether it may be NULL: always for an expression, and
// for a table column unless it is NOT NULL and no outer join of the
// statement can leave its table out.
type Column struct {
	Name     string
	TypeName string
	Table    string
	Column   string
	Nullable bool
}

// Statement is what Postgres says of one statement: its result columns and
// the type it inferred for each parameter, or the error that stopped it
// from preparing and the byte offset of the statement Postgres gave for it
// (-1 when it gave none).
type Statement struct {
	Columns []Column
	Params  []string
	Error   string
	Offset  int64
}

// Description is the schema the migrations build and each statement's
// description. A migration that fails stops the description: Error and
// Offset are its error, and Migration names it. A server that cannot be
// reached, or a scratch database that cannot be made, is an Error with no
// Migration.
type Description struct {
	Tables     []Table
	Statements []Statement
	Error      string
	Migration  string
	Offset     int64
}
