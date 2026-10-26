package goside

import (
	"slices"
	"strings"
	"testing"
)

// A transaction's savepoints commit and roll back to their own depth, and
// an ended one answers Finished.
func TestConn_SavepointsAndFinished(t *testing.T) {
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer Close(c)
	if a := Exec(c, 0, 0, []string{"CREATE TABLE t (n INTEGER)"}, nil); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	insert := func(depth, token, n int64) Failure {
		return Exec(c, depth, token, []string{"INSERT INTO t VALUES (", ")"}, []Cell{{Kind: KindInt, Int: n}}).Failure
	}
	tx := Begin(c, 0, 0)
	if f := insert(tx.Depth, tx.Token, 1); f.Kind != FailNone {
		t.Fatal(f)
	}
	sp := Begin(c, tx.Depth, tx.Token)
	if tx.Depth != 1 || sp.Depth != 2 {
		t.Fatalf("depths %d %d", tx.Depth, sp.Depth)
	}
	if f := insert(sp.Depth, sp.Token, 2); f.Kind != FailNone {
		t.Fatal(f)
	}
	// The outer transaction's statement would belong to the savepoint.
	if f := insert(tx.Depth, tx.Token, 3); f.Kind != FailDatabase || !strings.Contains(f.Message, "a savepoint is open") {
		t.Errorf("a statement on the transaction under an open savepoint answered %+v", f)
	}
	if f := insert(0, 0, 3); f.Kind != FailDatabase || !strings.Contains(f.Message, "a transaction is open") {
		t.Errorf("a statement on the connection under an open transaction answered %+v", f)
	}
	if f := Rollback(c, sp.Depth, sp.Token); f.Kind != FailNone {
		t.Fatal(f)
	}
	if f := Exec(c, sp.Depth, sp.Token, []string{"SELECT 1"}, nil).Failure; f.Kind != FailFinished {
		t.Errorf("a statement on a rolled-back savepoint answered %+v", f)
	}
	if f := Commit(c, tx.Depth, tx.Token); f.Kind != FailNone {
		t.Fatal(f)
	}
	rows := Run(c, 0, 0, []string{"SELECT n FROM t"}, nil)
	if len(rows.Rows) != 1 || rows.Rows[0][0].Int != 1 {
		t.Errorf("rows %+v, %+v", rows.Rows, rows.Failure)
	}
}

// A constraint violation names the constraint as Postgres would: the
// schema's name when it gives one, else Postgres's default name for it.
func TestConn_ConstraintNames(t *testing.T) {
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer Close(c)
	for _, s := range []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			handle TEXT,
			age INTEGER CHECK (age > 0),
			score INTEGER CONSTRAINT score_small CHECK (score < 100),
			code TEXT CONSTRAINT users_code_once UNIQUE,
			nick TEXT CONSTRAINT users_nick_given NOT NULL DEFAULT 'x'
		)`,
		"CREATE UNIQUE INDEX users_handle_key ON users (handle)",
		"CREATE TABLE pairs (a INTEGER, b INTEGER, UNIQUE (a, b), CHECK (a <> b))",
		"CREATE TABLE keyed (k TEXT, CONSTRAINT keyed_named_pk PRIMARY KEY (k))",
		`CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users (id),
			helper_id INTEGER CONSTRAINT orders_helper REFERENCES users (id),
			status TEXT NOT NULL DEFAULT 'pending'
				CHECK (status IN ('pending',
					'paid'))
		)`,
		"CREATE TEMPORARY TABLE scratch (n INTEGER UNIQUE, parent INTEGER REFERENCES scratch (n))",
		"INSERT INTO users (id, email, handle, code) VALUES (1, 'a', 'a', 'x')",
		"INSERT INTO pairs VALUES (1, 2)",
		"INSERT INTO keyed VALUES ('k')",
		"INSERT INTO orders (user_id) VALUES (1)",
		"INSERT INTO scratch (n) VALUES (1)",
	} {
		if a := Exec(c, 0, 0, []string{s}, nil); a.Failure.Kind != FailNone {
			t.Fatalf("%s: %+v", s, a.Failure)
		}
	}
	cases := []struct {
		sql        string
		constraint int64
		name       string
		table      string
		column     string
	}{
		{"INSERT INTO users (email) VALUES ('a')", ConstraintUnique, "users_email_key", "users", "email"},
		{"INSERT INTO users (id, email) VALUES (1, 'b')", ConstraintUnique, "users_pkey", "users", "id"},
		{"INSERT INTO users (email, handle) VALUES ('b', 'a')", ConstraintUnique, "users_handle_key", "users", "handle"},
		{"INSERT INTO users (email, code) VALUES ('b', 'x')", ConstraintUnique, "users_code_once", "users", "code"},
		{"INSERT INTO pairs VALUES (1, 2)", ConstraintUnique, "pairs_a_b_key", "pairs", "a,b"},
		{"INSERT INTO keyed VALUES ('k')", ConstraintUnique, "keyed_named_pk", "keyed", "k"},
		{"INSERT INTO scratch (n) VALUES (1)", ConstraintUnique, "scratch_n_key", "scratch", "n"},
		{"INSERT INTO users (email) VALUES (NULL)", ConstraintNotNull, "users_email_not_null", "users", "email"},
		{"INSERT INTO users (email, nick) VALUES ('b', NULL)", ConstraintNotNull, "users_nick_given", "users", "nick"},
		{"INSERT INTO users (email, age) VALUES ('b', 0)", ConstraintCheck, "users_age_check", "users", "age"},
		{"INSERT INTO users (email, score) VALUES ('b', 100)", ConstraintCheck, "score_small", "users", "score"},
		{"INSERT INTO pairs VALUES (3, 3)", ConstraintCheck, "pairs_check", "pairs", ""},
		{"INSERT INTO orders (user_id, status) VALUES (1, 'lost')", ConstraintCheck, "orders_status_check", "orders", "status"},
		{"INSERT INTO orders (user_id) VALUES (99)", ConstraintForeignKey, "orders_user_id_fkey", "orders", "user_id"},
		{"INSERT INTO orders (user_id, helper_id) VALUES (1, 99)", ConstraintForeignKey, "orders_helper", "orders", "helper_id"},
		{"UPDATE orders SET user_id = 98", ConstraintForeignKey, "orders_user_id_fkey", "orders", "user_id"},
		{"DELETE FROM users WHERE id = 1", ConstraintForeignKey, "orders_user_id_fkey", "orders", "user_id"},
		{"INSERT INTO scratch (n, parent) VALUES (2, 7)", ConstraintForeignKey, "scratch_parent_fkey", "scratch", "parent"},
	}
	check := func(depth, token int64) {
		for _, k := range cases {
			f := Exec(c, depth, token, []string{k.sql}, nil).Failure
			if f.Kind != FailConstraint || f.Constraint != k.constraint || f.Name != k.name || f.Table != k.table || f.Column != k.column {
				t.Errorf("%s (depth %d): got %+v, want %s on %s.%s", k.sql, depth, f, k.name, k.table, k.column)
			}
		}
	}
	check(0, 0)
	// Inside a transaction too, and finding a foreign key's name leaves the
	// transaction as it was.
	tx := Begin(c, 0, 0)
	if a := Exec(c, tx.Depth, tx.Token, []string{"INSERT INTO pairs VALUES (5, 6)"}, nil); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	check(tx.Depth, tx.Token)
	if a := Exec(c, tx.Depth, tx.Token, []string{"INSERT INTO pairs VALUES (7, 8)"}, nil); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	if f := Rollback(c, tx.Depth, tx.Token); f.Kind != FailNone {
		t.Fatal(f)
	}
	for table, want := range map[string]int64{"pairs": 1, "orders": 1, "users": 1} {
		rows := Run(c, 0, 0, []string{"SELECT count(*) FROM " + table}, nil)
		if rows.Failure.Kind != FailNone || rows.Rows[0][0].Int != want {
			t.Errorf("%s after the checks: %+v", table, rows)
		}
	}
}

// A DECIMAL column's number reads as its decimal text at the column's
// scale, and one that is not exactly a decimal of that scale, or of more
// digits than a float holds, is refused.
func TestDecimalOf(t *testing.T) {
	tenth := 0.1
	cases := []struct {
		v        any
		declared string
		want     string
		refused  bool
	}{
		{12.5, "DECIMAL(12, 2)", "12.50", false},
		{int64(12), "DECIMAL(12,2)", "12.00", false},
		{0.1, "numeric(5, 2)", "0.10", false},
		{-3.05, "DECIMAL(12, 2)", "-3.05", false},
		{12.5, "DECIMAL", "12.5", false},
		{int64(7), "DECIMAL(10)", "7", false},
		{9999999999999.99, "DECIMAL(15, 2)", "9999999999999.99", false},
		{12.555, "DECIMAL(12, 2)", "", true},
		{tenth + 0.2, "DECIMAL(12, 2)", "", true}, // 0.30000000000000004
		{90071992547409.94, "DECIMAL(16, 2)", "", true},
		{int64(9007199254740992), "DECIMAL(18, 2)", "", true},
		{1e20, "NUMERIC", "", true},
	}
	for _, c := range cases {
		scale, ok := decimalScale(c.declared)
		if !ok {
			t.Fatalf("%q is not a decimal type", c.declared)
		}
		got, err := decimalOf(c.v, scale, "amount", c.declared)
		if c.refused != (err != nil) || got != c.want {
			t.Errorf("%v in %s: got %q, %v", c.v, c.declared, got, err)
		}
	}
	if _, ok := decimalScale("REAL"); ok {
		t.Errorf("REAL read as a decimal type")
	}
}

// A Decimal written into a DECIMAL(12, 2) column reads back at that scale;
// one of more than 15 digits is refused before SQLite rounds it.
func TestConn_DecimalsKeepTheirScale(t *testing.T) {
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer Close(c)
	if a := Exec(c, 0, 0, []string{"CREATE TABLE m (amount DECIMAL(12, 2))"}, nil); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	insert := []string{"INSERT INTO m VALUES (", ")"}
	if a := Exec(c, 0, 0, insert, []Cell{{Kind: KindDecimal, Text: "12.50"}}); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	a := Exec(c, 0, 0, insert, []Cell{{Kind: KindDecimal, Text: "123456789012345678.91"}})
	if a.Failure.Code != "SQLITE_MISMATCH" || !strings.HasPrefix(a.Failure.Message, "parameter ?1 is the Decimal 123456789012345678.91, of 20 digits") {
		t.Errorf("got %+v", a.Failure)
	}
	got := Run(c, 0, 0, []string{"SELECT amount, typeof(amount) FROM m"}, nil)
	if got.Failure.Kind != FailNone || len(got.Rows) != 1 || got.Rows[0][0].Kind != KindDecimal || got.Rows[0][0].Text != "12.50" || got.Rows[0][1].Text != "real" {
		t.Errorf("got %+v", got)
	}
}

// Every storage class crosses both ways, in both builds: the browser
// driver answers what modernc.org/sqlite answers, a DATE column's text
// read as a date included.
func TestConn_ValuesCrossAsTheNativeDriverAnswersThem(t *testing.T) {
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer Close(c)
	if a := Exec(c, 0, 0, []string{"CREATE TABLE v (i INTEGER, f REAL, s TEXT, b BLOB, e BLOB, n TEXT, d DATE)"}, nil); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	params := []Cell{
		{Kind: KindInt, Int: -9007199254740993},
		{Kind: KindFloat, Float: 0.1},
		{Kind: KindText, Text: "héllo"},
		{Kind: KindBytes, Bytes: []byte{0, 1, 255}},
		{Kind: KindBytes, Bytes: []byte{}},
		{Kind: KindNull},
		{Kind: KindText, Text: "2026-10-24T16:59:52"},
	}
	if a := Exec(c, 0, 0, []string{"INSERT INTO v VALUES (", ", ", ", ", ", ", ", ", ", ", ", ", ")"}, params); a.Failure.Kind != FailNone || a.Changed != 1 {
		t.Fatalf("%+v", a)
	}
	got := Run(c, 0, 0, []string{"SELECT i, f, s, b, e, n, d, typeof(e) AS te FROM v"}, nil)
	if got.Failure.Kind != FailNone || len(got.Rows) != 1 {
		t.Fatalf("%+v", got)
	}
	row := got.Rows[0]
	if row[0].Int != -9007199254740993 || row[1].Float != 0.1 || row[2].Text != "héllo" ||
		string(row[3].Bytes) != "\x00\x01\xff" || row[4].Kind != KindBytes || len(row[4].Bytes) != 0 ||
		row[5].Kind != KindNull || row[6].Text != "2026-10-24" || row[7].Text != "blob" {
		t.Errorf("row %+v", row)
	}
	if want := []string{"INTEGER", "REAL", "TEXT", "BLOB", "BLOB", "TEXT", "DATE", ""}; strings.Join(got.Types, ",") != strings.Join(want, ",") {
		t.Errorf("types %q", got.Types)
	}
}

// Describe's tests run on both builds: SQLite's official WebAssembly
// build compiles in the column metadata it reads.

var schema = []Migration{
	{Path: "1.sql", Text: "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT);\n" +
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users (id), total DECIMAL(12, 2) NOT NULL);"},
	{Path: "2.sql", Text: "ALTER TABLE orders ADD COLUMN shipped_on DATE;"},
}

// SQLite's column metadata names each result column's table column, its
// declared type and whether it is NOT NULL; an expression has none.
func TestDescribe_ColumnsComeWithTheirTableColumns(t *testing.T) {
	d := Describe(schema, []string{"SELECT o.id, u.email, u.name, count(*) AS n FROM orders o JOIN users u ON u.id = o.user_id WHERE o.id = ?1"})
	if d.Error != "" {
		t.Fatalf("describe: %s", d.Error)
	}
	s := d.Statements[0]
	want := []Column{
		{Name: "id", Declared: "INTEGER", Table: "orders", Origin: "id", NotNull: true},
		{Name: "email", Declared: "TEXT", Table: "users", Origin: "email", NotNull: true},
		{Name: "name", Declared: "TEXT", Table: "users", Origin: "name"},
		{Name: "n"},
	}
	if !slices.Equal(s.Columns, want) || s.Params != 1 {
		t.Errorf("got %+v, %d params", s.Columns, s.Params)
	}
}

// The query plan marks the table a LEFT JOIN may leave out.
func TestDescribe_PlanMarksTheLeftJoinedTable(t *testing.T) {
	d := Describe(schema, []string{"SELECT u.email, o.id FROM users u LEFT JOIN orders o ON o.user_id = u.id"})
	plan := strings.Join(d.Statements[0].Plan, "\n")
	if !strings.Contains(plan, "o ") || !strings.Contains(plan, "LEFT-JOIN") {
		t.Errorf("plan:\n%s", plan)
	}
}

// A statement SQLite rejects is its message and the byte it names; a
// migration it rejects stops the description at that migration.
func TestDescribe_Errors(t *testing.T) {
	d := Describe(schema, []string{"SELECT nope FROM users"})
	if s := d.Statements[0]; s.Error != "no such column: nope" || s.Offset != 7 {
		t.Errorf("got %q at %d", s.Error, s.Offset)
	}
	d = Describe([]Migration{{Path: "1.sql", Text: "CREATE TABLE a (n INTEGER);\nCREATE TABLE b (n INTEGER REFERENCES;"}}, nil)
	if d.Error == "" || d.Migration != "1.sql" || d.Offset < 28 {
		t.Errorf("got %+v", d)
	}
}

// Each migration that creates or changes a column is recorded with it.
func TestDescribe_ColumnsKnowTheirMigration(t *testing.T) {
	d := Describe(schema, nil)
	for _, table := range d.Tables {
		for _, c := range table.Columns {
			want := "1.sql"
			if c.Name == "shipped_on" {
				want = "2.sql"
			}
			if c.Migration != want {
				t.Errorf("%s.%s: migration %q, want %q", table.Name, c.Name, c.Migration, want)
			}
		}
	}
}
