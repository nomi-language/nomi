package goside

import (
	"context"
	"os/exec"
	"runtime"
	"slices"
	"testing"
	"time"
)

var background = context.Background()

// fresh is the URL of a fresh scratch database, or skips the test when
// this machine cannot run the scratch server.
func fresh(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the scratch server needs a POSIX shell")
	}
	for _, tool := range []string{"initdb", "postgres"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH, so there is no scratch server", tool)
		}
	}
	url, err := Scratch(background)
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// connect opens a connection to a fresh scratch database.
func connect(t *testing.T) *Session {
	t.Helper()
	s, err := Connect(background, fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(s) })
	return s
}

func exec_(t *testing.T, s *Session, sql string, params ...Cell) {
	t.Helper()
	if f := Exec(background, s, []string{sql}, params).Failure; f.Kind != FailNone {
		t.Fatalf("%s: %+v", sql, f)
	}
}

// Every kind a Cell carries is written and read back as itself.
func TestValues_RoundTrip(t *testing.T) {
	s := connect(t)
	exec_(t, s, `CREATE TABLE v (i BIGINT, f DOUBLE PRECISION, d NUMERIC(12, 2), t TEXT, b BOOLEAN, x BYTEA,
		day DATE, at TIME, ts TIMESTAMP, tz TIMESTAMPTZ, id UUID, j JSONB, js JSON, ns BIGINT[], names TEXT[])`)
	at := time.Date(2026, 10, 19, 12, 30, 5, 250000000, time.UTC)
	params := []Cell{
		{Kind: KindInt, Int: 7},
		{Kind: KindFloat, Float: 2.5},
		{Kind: KindDecimal, Text: "12.25"},
		{Kind: KindText, Text: "it's"},
		{Kind: KindBool, Int: 1},
		{Kind: KindBytes, Bytes: []byte("hi")},
		{Kind: KindDate, Text: "2026-10-19"},
		{Kind: KindTime, Text: "12:30:05.5"},
		{Kind: KindTimestamp, Text: "2026-10-19T12:30:05.5"},
		{Kind: KindTimestamptz, Int: at.UnixNano()},
		{Kind: KindUuid, Text: "0192f4c8-6f0e-7b39-9d6a-3c1f2b5e8a10"},
		{Kind: KindJson, Text: `{"a": [1, 2]}`},
		{Kind: KindJson, Text: `{"b":1, "a":2}`},
		{Kind: KindArray, Items: []Item{{Kind: KindInt, Int: 1}, {Kind: KindNull}, {Kind: KindInt, Int: 3}}},
		{Kind: KindArray, Items: []Item{}},
	}
	parts := []string{"INSERT INTO v VALUES ("}
	for i := 1; i < len(params); i++ {
		parts = append(parts, ", ")
	}
	parts = append(parts, ")")
	if a := Exec(background, s, parts, params); a.Failure.Kind != FailNone || a.Changed != 1 {
		t.Fatalf("insert: %+v", a)
	}
	got := Run(background, s, []string{"SELECT * FROM v"}, nil)
	if got.Failure.Kind != FailNone || len(got.Rows) != 1 {
		t.Fatalf("select: %+v", got)
	}
	want := slices.Clone(params)
	// jsonb is stored normalized; json keeps its text.
	want[11] = Cell{Kind: KindJson, Text: `{"a": [1, 2]}`}
	for i, c := range got.Rows[0] {
		if !sameCell(c, want[i]) {
			t.Errorf("column %s: got %+v, want %+v", got.Columns[i], c, want[i])
		}
	}
}

func sameCell(a, b Cell) bool {
	if a.Kind != b.Kind || a.Int != b.Int || a.Float != b.Float || a.Text != b.Text || string(a.Bytes) != string(b.Bytes) || len(a.Items) != len(b.Items) {
		return false
	}
	for i := range a.Items {
		if a.Items[i].Kind != b.Items[i].Kind || a.Items[i].Int != b.Items[i].Int || a.Items[i].Text != b.Items[i].Text {
			return false
		}
	}
	return true
}

// An Array parameter matches with = ANY.
func TestArrays_Any(t *testing.T) {
	s := connect(t)
	exec_(t, s, "CREATE TABLE users (id BIGINT PRIMARY KEY, name TEXT NOT NULL)")
	exec_(t, s, "INSERT INTO users VALUES (1, 'a'), (2, 'b'), (3, 'c')")
	ids := Cell{Kind: KindArray, Items: []Item{{Kind: KindInt, Int: 1}, {Kind: KindInt, Int: 3}}}
	got := Run(background, s, []string{"SELECT name FROM users WHERE id = ANY(", ") ORDER BY id"}, []Cell{ids})
	if got.Failure.Kind != FailNone || len(got.Rows) != 2 || got.Rows[0][0].Text != "a" || got.Rows[1][0].Text != "c" {
		t.Fatalf("got %+v", got)
	}
}

// A constraint error names the constraint, its table and its column.
func TestFailure_Constraints(t *testing.T) {
	s := connect(t)
	exec_(t, s, "CREATE TABLE parents (id INTEGER PRIMARY KEY, k INTEGER UNIQUE, c INTEGER CONSTRAINT c_small CHECK (c < 10))")
	exec_(t, s, "CREATE TABLE children (n INTEGER NOT NULL, parent_id INTEGER REFERENCES parents (id))")
	exec_(t, s, "INSERT INTO parents VALUES (1, 10, 0)")
	for _, tc := range []struct {
		sql         string
		kind        int64
		name, table string
		column      string
	}{
		{"INSERT INTO parents VALUES (2, 10, 0)", ConstraintUnique, "parents_k_key", "parents", "k"},
		{"INSERT INTO parents VALUES (1, 20, 0)", ConstraintUnique, "parents_pkey", "parents", "id"},
		{"INSERT INTO parents VALUES (3, 30, 10)", ConstraintCheck, "c_small", "parents", ""},
		{"INSERT INTO children VALUES (NULL, 1)", ConstraintNotNull, "children_n_not_null", "children", "n"},
		{"INSERT INTO children VALUES (1, 99)", ConstraintForeignKey, "children_parent_id_fkey", "children", "parent_id"},
	} {
		f := Exec(background, s, []string{tc.sql}, nil).Failure
		if f.Kind != FailConstraint || f.Constraint != tc.kind || f.Name != tc.name || f.Table != tc.table || f.Column != tc.column {
			t.Errorf("%s: %+v", tc.sql, f)
		}
	}
}

// The calling task's context ends a statement, as a Timeout, and the
// connection works afterwards.
func TestRun_TheTaskContextEndsAStatement(t *testing.T) {
	s := connect(t)
	ctx, cancel := context.WithTimeout(background, 100*time.Millisecond)
	defer cancel()
	if f := Run(ctx, s, []string{"SELECT pg_sleep(10)"}, nil).Failure; f.Kind != FailTimeout {
		t.Fatalf("failure = %+v, want FailTimeout", f)
	}
	if got := Run(background, s, []string{"SELECT 1"}, nil); got.Failure.Kind != FailNone {
		t.Fatalf("after the timeout: %+v", got.Failure)
	}
}

// A pool runs statements on any connection, and a transaction on one it
// holds until the transaction ends.
func TestPool_TransactionsHoldTheirConnection(t *testing.T) {
	p := Pool(fresh(t))
	defer Close(p)
	exec_(t, p, "CREATE TABLE t (n INTEGER)")
	tx, f := Begin(background, p)
	if f.Kind != FailNone {
		t.Fatal(f)
	}
	exec_(t, tx, "INSERT INTO t VALUES (1)")
	// Outside the transaction, its write is not seen yet.
	if got := Run(background, p, []string{"SELECT count(*) FROM t"}, nil); got.Rows[0][0].Int != 0 {
		t.Fatalf("the pool saw the open transaction's write: %+v", got)
	}
	sp, f := Begin(background, tx)
	if f.Kind != FailNone {
		t.Fatal(f)
	}
	exec_(t, sp, "INSERT INTO t VALUES (2)")
	if f := Rollback(background, sp); f.Kind != FailNone {
		t.Fatal(f)
	}
	if f := Commit(background, tx); f.Kind != FailNone {
		t.Fatal(f)
	}
	if got := Run(background, p, []string{"SELECT count(*) FROM t"}, nil); got.Rows[0][0].Int != 1 {
		t.Fatalf("after commit: %+v", got)
	}
	if f := Commit(background, tx); f.Kind != FailFinished {
		t.Fatalf("a second commit: %+v", f)
	}
}

// A listener answers a notification sent after it began listening, and a
// wait with none to answer ends with its context, as a Timeout.
func TestListen_WaitsForANotification(t *testing.T) {
	url := fresh(t)
	l, err := Listen(background, url, "Orders Placed")
	if err != nil {
		t.Fatal(err)
	}
	defer CloseListener(l)
	notifier, err := Connect(background, url)
	if err != nil {
		t.Fatal(err)
	}
	defer Close(notifier)
	exec_(t, notifier, `SELECT pg_notify('Orders Placed', 'order 7')`)
	if n := Wait(background, l); n.Failure.Kind != FailNone || n.Channel != "Orders Placed" || n.Payload != "order 7" {
		t.Fatalf("wait = %+v", n)
	}
	ctx, cancel := context.WithTimeout(background, 100*time.Millisecond)
	defer cancel()
	if n := Wait(ctx, l); n.Failure.Kind != FailTimeout {
		t.Fatalf("an empty wait = %+v, want FailTimeout", n)
	}
	if f := CloseListener(l); f.Kind != FailNone {
		t.Fatal(f)
	}
	if n := Wait(background, l); n.Failure.Kind != FailClosed {
		t.Fatalf("a wait after close = %+v", n)
	}
}

// A pool whose server cannot be reached fails at its first statement, as
// a Connection failure.
func TestPool_ConnectsLazily(t *testing.T) {
	p := Pool("postgres://postgres@127.0.0.1:1/none?sslmode=disable&connect_timeout=2")
	defer Close(p)
	if f := Ping(background, p); f.Kind != FailConnection {
		t.Fatalf("ping = %+v, want FailConnection", f)
	}
	bad := Pool("not a url")
	defer Close(bad)
	if f := Ping(background, bad); f.Kind != FailConnection {
		t.Fatalf("ping = %+v, want FailConnection", f)
	}
}
