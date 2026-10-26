package goside

// What Describe answers, in every build: describe.nomi binds it, so the
// browser build declares the same types (describe_js.go).

// Migration is one migration file: its path and its SQL.
type Migration struct {
	Path string
	Text string
}

// TableColumn is one column of a table as the schema declares it, and the
// migration that last created or changed it.
type TableColumn struct {
	Name       string
	Declared   string
	NotNull    bool
	PrimaryKey bool
	HasDefault bool
	Migration  string
}

// Table is one table: its name, the CREATE TABLE statement SQLite keeps,
// its columns, and the migration that created it.
type Table struct {
	Name      string
	Sql       string
	Columns   []TableColumn
	Migration string
}

// Column is one result column of a statement: its name, the declared
// type of the table column it comes from ("" for an expression), and that
// table and column with its NOT NULL.
type Column struct {
	Name     string
	Declared string
	Table    string
	Origin   string
	NotNull  bool
}

// Statement is what SQLite says of one statement: its result columns and
// how many parameters it has, the detail lines of its query plan, or the
// error that stopped it from preparing and the byte offset SQLite gave
// for it (-1 when it gave none).
type Statement struct {
	Columns []Column
	Params  int64
	Plan    []string
	Error   string
	Offset  int64
}

// Description is the schema the migrations build and each statement's
// description. A migration that fails stops the description: Error and
// Offset are its error, and Migration names it.
type Description struct {
	Tables     []Table
	Statements []Statement
	Error      string
	Migration  string
	Offset     int64
}
