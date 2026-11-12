package goside

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

var shopMigrations = []Migration{
	{Path: "db/1.up.sql", Text: `
CREATE TYPE kind AS ENUM ('heart', 'pray');
CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    name text,
    status varchar(10) NOT NULL CHECK (status IN ('active', 'gone'))
);
CREATE TABLE posts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id uuid REFERENCES users (id),
    title text NOT NULL,
    total numeric(12, 2) NOT NULL DEFAULT 0
);`},
	{Path: "db/2.up.sql", Text: `
CREATE TABLE reactions (post_id bigint NOT NULL REFERENCES posts (id), kind kind NOT NULL);
ALTER TABLE users ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();`},
}

func describeShop(t *testing.T, statements ...string) Description {
	t.Helper()
	d := Describe(background, fresh(t), shopMigrations, statements)
	if d.Error != "" {
		t.Fatalf("Describe: %s (migration %q, offset %d)", d.Error, d.Migration, d.Offset)
	}
	return d
}

func tableNamed(d Description, name string) Table {
	for _, t := range d.Tables {
		if t.Name == name {
			return t
		}
	}
	return Table{}
}

func columnNamed(t Table, name string) TableColumn {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return TableColumn{}
}

func TestDescribe_TablesTheirTypesAndWhereEachColumnCameFrom(t *testing.T) {
	d := describeShop(t)
	users := tableNamed(d, "users")
	if users.Migration != "db/1.up.sql" {
		t.Errorf("users made by %q", users.Migration)
	}
	id := columnNamed(users, "id")
	if id.TypeName != "uuid" || !id.NotNull || !id.PrimaryKey || !id.HasDefault {
		t.Errorf("users.id = %+v", id)
	}
	if name := columnNamed(users, "name"); name.NotNull || name.TypeName != "text" {
		t.Errorf("users.name = %+v", name)
	}
	if c := columnNamed(users, "created_at"); c.Migration != "db/2.up.sql" || c.TypeName != "timestamp with time zone" {
		t.Errorf("users.created_at = %+v", c)
	}
	if c := columnNamed(users, "status"); !slices.Equal(c.Values, []string{"active", "gone"}) || c.TypeName != "character varying(10)" {
		t.Errorf("users.status = %+v", c)
	}
	if c := columnNamed(tableNamed(d, "posts"), "total"); c.TypeName != "numeric(12,2)" || !c.HasDefault {
		t.Errorf("posts.total = %+v", c)
	}
	if c := columnNamed(tableNamed(d, "posts"), "id"); !c.HasDefault {
		t.Errorf("an identity column has a default: %+v", c)
	}
	if c := columnNamed(tableNamed(d, "reactions"), "kind"); !slices.Equal(c.Values, []string{"heart", "pray"}) || c.TypeName != "kind" {
		t.Errorf("an enum type's values: %+v", c)
	}
}

func TestDescribe_ParametersColumnsAndTheirTables(t *testing.T) {
	d := describeShop(t,
		"SELECT id, email, name FROM users WHERE id = ANY ($1) AND status = $2",
		"INSERT INTO posts (user_id, title) VALUES ($1, $2) RETURNING id, total",
		"SELECT count(*) AS n, max(title) AS last FROM posts",
	)
	s := d.Statements[0]
	// A varchar compared with a parameter makes the parameter text.
	if s.Error != "" || !slices.Equal(s.Params, []string{"uuid[]", "text"}) {
		t.Fatalf("statement 0 = %+v", s)
	}
	want := []Column{
		{Name: "id", TypeName: "uuid", Table: "users", Column: "id"},
		{Name: "email", TypeName: "text", Table: "users", Column: "email"},
		{Name: "name", TypeName: "text", Table: "users", Column: "name", Nullable: true},
	}
	if !slices.Equal(s.Columns, want) {
		t.Errorf("columns = %+v", s.Columns)
	}
	ins := d.Statements[1]
	if !slices.Equal(ins.Params, []string{"uuid", "text"}) || len(ins.Columns) != 2 ||
		ins.Columns[0] != (Column{Name: "id", TypeName: "bigint", Table: "posts", Column: "id"}) ||
		ins.Columns[1].TypeName != "numeric(12,2)" || ins.Columns[1].Nullable {
		t.Errorf("INSERT ... RETURNING = %+v", ins)
	}
	agg := d.Statements[2]
	if agg.Columns[0] != (Column{Name: "n", TypeName: "bigint", Nullable: true}) || !agg.Columns[1].Nullable {
		t.Errorf("an expression is nullable: %+v", agg.Columns)
	}
}

func TestDescribe_AColumnAnOuterJoinMayLeaveOutIsNullable(t *testing.T) {
	d := describeShop(t,
		"SELECT p.title, u.email FROM posts p LEFT JOIN users u ON u.id = p.user_id",
		"SELECT p.title, u.email FROM users u RIGHT JOIN posts p ON u.id = p.user_id WHERE p.id = $1",
		"SELECT p.title, u.email FROM posts p JOIN users u ON u.id = p.user_id",
		"SELECT p.title, u.email FROM posts p FULL JOIN users u ON u.id = p.user_id",
		"WITH active AS (SELECT * FROM users WHERE status = 'active') SELECT p.title, a.email FROM posts p LEFT JOIN active a ON a.id = p.user_id",
	)
	nullable := func(i int) []bool {
		var out []bool
		for _, c := range d.Statements[i].Columns {
			out = append(out, c.Nullable)
		}
		return out
	}
	for i, want := range [][]bool{{false, true}, {false, true}, {false, false}, {true, true}, {false, true}} {
		if got := nullable(i); !slices.Equal(got, want) {
			t.Errorf("statement %d: nullable %v, want %v", i, got, want)
		}
	}
}

// A view's columns are described through its definition: a NOT NULL table
// column reached through inner joins is non-null, one an outer join inside
// the view may leave out is nullable, an expression is nullable, and so is
// a view's column an outer join of the statement may leave out.
func TestDescribe_AViewColumnIsNullableAsItsDefinitionsColumnIs(t *testing.T) {
	views := Migration{Path: "db/3.up.sql", Text: `
CREATE VIEW post_authors AS
  SELECT p.title, u.email, u.name, p.title || '!' AS shout, u.status
  FROM posts p
  JOIN users u ON u.id = p.user_id;
CREATE VIEW post_readers AS
  SELECT p.title, u.email FROM posts p LEFT JOIN users u ON u.id = p.user_id;
CREATE VIEW post_titles AS SELECT title FROM post_authors;
CREATE MATERIALIZED VIEW post_counts AS
  SELECT u.email, count(*) AS n FROM posts p JOIN users u ON u.id = p.user_id GROUP BY u.email;`}
	d := Describe(background, fresh(t), append(slices.Clone(shopMigrations), views), []string{
		"SELECT d.title, d.email, d.name, d.shout, d.status FROM post_authors d",
		"SELECT r.title, r.email FROM post_readers r",
		"SELECT t.title FROM post_titles t",
		"SELECT r.kind, d.title FROM reactions r LEFT JOIN post_authors d ON d.title = 'x'",
		"SELECT c.email, c.n FROM post_counts c",
	})
	if d.Error != "" {
		t.Fatalf("Describe: %s (migration %q, offset %d)", d.Error, d.Migration, d.Offset)
	}
	for i, want := range [][]bool{{false, false, true, true, false}, {false, true}, {false}, {false, true}, {false, true}} {
		s := d.Statements[i]
		if s.Error != "" {
			t.Fatalf("statement %d: %s", i, s.Error)
		}
		var got []bool
		for _, c := range s.Columns {
			got = append(got, c.Nullable)
		}
		if !slices.Equal(got, want) {
			t.Errorf("statement %d: nullable %v, want %v", i, got, want)
		}
	}
	// The column a view's column reads is the table column under it, so a
	// CHECK enum reaches through the view.
	want := []Column{
		{Name: "title", TypeName: "text", Table: "posts", Column: "title"},
		{Name: "email", TypeName: "text", Table: "users", Column: "email"},
		{Name: "name", TypeName: "text", Table: "users", Column: "name", Nullable: true},
		{Name: "shout", TypeName: "text", Nullable: true},
		{Name: "status", TypeName: "character varying(10)", Table: "users", Column: "status"},
	}
	if !slices.Equal(d.Statements[0].Columns, want) {
		t.Errorf("columns = %+v", d.Statements[0].Columns)
	}
	if c := d.Statements[2].Columns[0]; c.Table != "posts" || c.Column != "title" {
		t.Errorf("a view over a view reads %q.%q", c.Table, c.Column)
	}
}

// A development server made by a bare initdb has the encoding SQL_ASCII,
// where pgx refuses a simple-protocol query, so reading the plan that way
// failed and every table column came out nullable. client_encoding in the
// URL makes the scratch connection such a one.
func TestDescribe_ReadsThePlanOnAServerThatIsNotUTF8(t *testing.T) {
	url := fresh(t) + "&client_encoding=SQL_ASCII"
	d := Describe(background, url, shopMigrations, []string{
		"SELECT p.title, u.email FROM posts p LEFT JOIN users u ON u.id = p.user_id WHERE p.id = $1",
	})
	if d.Error != "" {
		t.Fatal(d.Error)
	}
	var nullable []bool
	for _, c := range d.Statements[0].Columns {
		nullable = append(nullable, c.Nullable)
	}
	if !slices.Equal(nullable, []bool{false, true}) {
		t.Errorf("nullable %v, want [false true]: the plan was not read", nullable)
	}
}

func TestDescribe_AnErrorAndTheByteItIsAt(t *testing.T) {
	text := "SELECT émail, nope FROM users"
	d := describeShop(t, text)
	s := d.Statements[0]
	if !strings.Contains(s.Error, "émail") || s.Offset != int64(strings.Index(text, "émail")) {
		t.Errorf("error %q at %d", s.Error, s.Offset)
	}
	bad := []Migration{{Path: "m.sql", Text: "CREATE TABLE t (id int);\nCREATE TABLE u (x nonsense);"}}
	got := Describe(background, fresh(t), bad, nil)
	if got.Migration != "m.sql" || got.Offset != int64(strings.Index(bad[0].Text, "nonsense")) {
		t.Errorf("a migration that fails: %+v", got)
	}
}

func TestDescribe_DropsItsScratchDatabase(t *testing.T) {
	url := fresh(t)
	before := databases(t, url)
	if d := Describe(background, url, shopMigrations, []string{"SELECT 1"}); d.Error != "" {
		t.Fatal(d.Error)
	}
	cancelled, cancel := context.WithCancel(background)
	cancel()
	Describe(cancelled, url, shopMigrations, nil)
	if after := databases(t, url); !slices.Equal(before, after) {
		t.Errorf("databases before %v, after %v", before, after)
	}
}

func databases(t *testing.T, url string) []string {
	t.Helper()
	conn, err := pgx.Connect(background, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(background)
	rows, err := conn.Query(background, "SELECT datname FROM pg_database ORDER BY datname")
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return names
}
