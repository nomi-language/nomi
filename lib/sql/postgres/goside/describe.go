//go:build !js

package goside

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Describe makes a scratch database, nomi_prepare_<hash>, on the server at
// url, applies the migrations to it in the order given, describes each
// statement against the schema they build, and drops the database. url is
// a development server the caller may create databases on; nothing is
// written to the database it names.
//
// A statement is described by preparing it, through Postgres's extended
// protocol, which answers the type of each parameter and of each result
// column, and the table column a result column reads. Which tables an outer
// join may leave out comes from the statement's generic plan (EXPLAIN
// EXECUTE under plan_cache_mode = force_generic_plan, which Postgres 12 and
// later have), so a column of such a table is nullable.
func Describe(ctx context.Context, url string, migrations []Migration, statements []string) Description {
	d := Description{Tables: []Table{}, Statements: []Statement{}, Offset: -1}
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		d.Error = "the development server does not answer: " + err.Error()
		return d
	}
	defer admin.Close(context.WithoutCancel(ctx))

	name, err := scratchName(migrations, statements)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		d.Error = "cannot make the scratch database " + name + ": " + err.Error()
		return d
	}
	// Dropped whatever happens, on a context the caller's cancel does not
	// end, so a cancelled run leaves no database behind.
	defer admin.Exec(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")

	config, err := pgx.ParseConfig(url)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	config.Database = name
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		d.Error = "cannot connect to the scratch database " + name + ": " + err.Error()
		return d
	}
	defer conn.Close(context.WithoutCancel(ctx))

	created := map[string]string{}
	changed := map[string]string{}
	for _, m := range migrations {
		if _, err := conn.PgConn().Exec(ctx, m.Text).ReadAll(); err != nil {
			d.Error, _, d.Offset = errorAt(m.Text, err)
			d.Migration = m.Path
			return d
		}
		ts, err := describeTables(ctx, conn)
		if err != nil {
			d.Error, d.Migration = err.Error(), m.Path
			return d
		}
		for _, t := range ts {
			if _, ok := created[t.Name]; !ok {
				created[t.Name] = m.Path
			}
			for _, c := range t.Columns {
				key := t.Name + "\x00" + c.Name + "\x00" + c.TypeName
				if _, ok := changed[key]; !ok {
					changed[key] = m.Path
				}
			}
		}
	}
	ts, err := describeTables(ctx, conn)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	for _, t := range ts {
		t.Migration = created[t.Name]
		for i, c := range t.Columns {
			t.Columns[i].Migration = changed[t.Name+"\x00"+c.Name+"\x00"+c.TypeName]
		}
		d.Tables = append(d.Tables, t)
	}
	names := &typeNames{conn: conn, known: map[uint32]string{}}
	for _, s := range statements {
		d.Statements = append(d.Statements, describeStatement(ctx, conn, names, s))
	}
	return d
}

// scratchName is nomi_prepare_ and a hash of what is described, with a
// random suffix so that two runs at once on one server never share a
// database.
func scratchName(migrations []Migration, statements []string) (string, error) {
	h := sha256.New()
	for _, m := range migrations {
		fmt.Fprintf(h, "%d:%s%d:%s", len(m.Path), m.Path, len(m.Text), m.Text)
	}
	for _, s := range statements {
		fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return "nomi_prepare_" + hex.EncodeToString(h.Sum(nil))[:16] + "_" + hex.EncodeToString(suffix[:]), nil
}

// errorAt is a server error's message, and the byte of text its position
// names (-1 when it names none). Postgres gives a position as the 1-based
// index of a character, not of a byte.
func errorAt(text string, err error) (string, string, int64) {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err.Error(), "", -1
	}
	if pg.Position <= 0 {
		return pg.Message, "", -1
	}
	return pg.Message, "", byteOffset(text, int(pg.Position)-1)
}

// byteOffset is the byte where character index chars starts in text.
func byteOffset(text string, chars int) int64 {
	at := 0
	for i := 0; i < chars && at < len(text); i++ {
		_, size := utf8.DecodeRuneInString(text[at:])
		at += size
	}
	return int64(at)
}

const tablesQuery = `
SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
       EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY (i.indkey)),
       a.atthasdef OR a.attidentity <> '' OR a.attgenerated <> '',
       COALESCE((SELECT array_agg(e.enumlabel ORDER BY e.enumsortorder) FROM pg_enum e WHERE e.enumtypid = a.atttypid), '{}'),
       COALESCE((SELECT array_agg(pg_get_constraintdef(k.oid)) FROM pg_constraint k
                 WHERE k.conrelid = c.oid AND k.contype = 'c' AND k.conkey = ARRAY[a.attnum]), '{}')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE c.relkind IN ('r', 'p') AND n.nspname = current_schema()
ORDER BY c.relname, a.attnum`

// describeTables is every table of the current schema, in name order, with
// its columns in their order.
func describeTables(ctx context.Context, conn *pgx.Conn) ([]Table, error) {
	rows, err := conn.Query(ctx, tablesQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Table
	for rows.Next() {
		var table string
		var c TableColumn
		var enum, checks []string
		if err := rows.Scan(&table, &c.Name, &c.TypeName, &c.NotNull, &c.PrimaryKey, &c.HasDefault, &enum, &checks); err != nil {
			return nil, err
		}
		c.Values = enum
		if len(c.Values) == 0 {
			c.Values = checkValues(c.Name, checks)
		}
		if c.Values == nil {
			c.Values = []string{}
		}
		if len(out) == 0 || out[len(out)-1].Name != table {
			out = append(out, Table{Name: table, Columns: []TableColumn{}})
		}
		last := &out[len(out)-1]
		last.Columns = append(last.Columns, c)
	}
	return out, rows.Err()
}

// A CHECK constraint Postgres writes back from `CHECK (status IN ('a',
// 'b'))`: `CHECK ((status = ANY (ARRAY['a'::text, 'b'::text])))`, or for a
// varchar column `CHECK (((status)::text = ANY ((ARRAY['a'::character
// varying, ...])::text[])))`.
var (
	checkInPattern = regexp.MustCompile(`^CHECK \(\(+"?([A-Za-z_][A-Za-z0-9_]*)"?\)*(?:::[a-z ]+)? = ANY \(\(?ARRAY\[(.*)\]\)?(?:::[a-z ]+\[\])?\)\)\)?$`)
	literal        = regexp.MustCompile(`'((?:[^']|'')*)'::[a-z ]+`)
)

// checkValues is the values a one-column `CHECK (column IN (...))` allows,
// or nil when no constraint of column is of that form.
func checkValues(column string, checks []string) []string {
	for _, def := range checks {
		m := checkInPattern.FindStringSubmatch(def)
		if m == nil || m[1] != column {
			continue
		}
		var values []string
		for _, l := range literal.FindAllStringSubmatch(m[2], -1) {
			values = append(values, strings.ReplaceAll(l[1], "''", "'"))
		}
		if len(values) > 0 {
			return values
		}
	}
	return nil
}

// typeNames answers a type's name as format_type writes it, asking the
// server once per type and modifier.
type typeNames struct {
	conn  *pgx.Conn
	known map[uint32]string
}

func (n *typeNames) name(ctx context.Context, oid uint32, modifier int32) (string, error) {
	if modifier == -1 {
		if s, ok := n.known[oid]; ok {
			return s, nil
		}
	}
	var s string
	if err := n.conn.QueryRow(ctx, "SELECT format_type($1::oid, NULLIF($2::int, -1))", oid, modifier).Scan(&s); err != nil {
		return "", err
	}
	if modifier == -1 {
		n.known[oid] = s
	}
	return s, nil
}

// describeStatement prepares one statement and reads its parameter and
// column types, the table column each column reads, and the tables its
// outer joins may leave out.
func describeStatement(ctx context.Context, conn *pgx.Conn, names *typeNames, text string) Statement {
	s := Statement{Columns: []Column{}, Params: []string{}, Offset: -1}
	sd, err := conn.PgConn().Prepare(ctx, "", text, nil)
	if err != nil {
		s.Error, _, s.Offset = errorAt(text, err)
		return s
	}
	for _, oid := range sd.ParamOIDs {
		name, err := names.name(ctx, oid, -1)
		if err != nil {
			s.Error = err.Error()
			return s
		}
		s.Params = append(s.Params, name)
	}
	outer, planned := outerJoined(ctx, conn, text, len(sd.ParamOIDs))
	for _, f := range sd.Fields {
		typeName, err := names.name(ctx, f.DataTypeOID, f.TypeModifier)
		if err != nil {
			s.Error = err.Error()
			return s
		}
		c := Column{Name: f.Name, TypeName: typeName, Nullable: true}
		if f.TableOID != 0 && f.TableAttributeNumber > 0 {
			var notNull bool
			err := conn.QueryRow(ctx,
				"SELECT c.relname, a.attname, a.attnotnull FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid WHERE a.attrelid = $1 AND a.attnum = $2",
				f.TableOID, int16(f.TableAttributeNumber)).Scan(&c.Table, &c.Column, &notNull)
			if err != nil {
				s.Error = err.Error()
				return s
			}
			// Without a plan nothing says which tables an outer join may
			// leave out, so every table column may be NULL.
			c.Nullable = !notNull || !planned || outer[c.Table]
		}
		s.Columns = append(s.Columns, c)
	}
	return s
}

// outerJoined is the tables an outer join of the statement may leave out,
// by name, from its generic plan; false when the plan cannot be had.
//
// The generic plan is the one a prepared statement's parameters do not
// shape, so a parameter that happens to be NULL folds nothing away.
func outerJoined(ctx context.Context, conn *pgx.Conn, text string, params int) (map[string]bool, bool) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, false
	}
	// A prepared statement outlives the transaction's rollback, so it is
	// deallocated after it, for the next statement's PREPARE.
	defer func() {
		after := context.WithoutCancel(ctx)
		tx.Rollback(after)
		conn.Exec(after, "DEALLOCATE nomi_describe")
	}()
	if _, err := tx.Exec(ctx, "SET LOCAL plan_cache_mode = force_generic_plan"); err != nil {
		return nil, false
	}
	if _, err := tx.Exec(ctx, "PREPARE nomi_describe AS "+text); err != nil {
		return nil, false
	}
	args := strings.TrimSuffix(strings.Repeat("NULL, ", params), ", ")
	explain := "EXPLAIN (VERBOSE, FORMAT JSON) EXECUTE nomi_describe"
	if params > 0 {
		explain += "(" + args + ")"
	}
	var raw string
	// The extended protocol: pgx refuses the simple protocol's client-side
	// argument sanitizing on a server whose encoding is not UTF8, which a
	// development server made by a bare initdb (SQL_ASCII) is.
	if err := tx.QueryRow(ctx, explain).Scan(&raw); err != nil {
		return nil, false
	}
	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(raw), &plans); err != nil || len(plans) == 0 {
		return nil, false
	}
	root := plans[0].Plan
	out := map[string]bool{}
	var ctes []string
	var nullable func(n planNode)
	nullable = func(n planNode) {
		if n.Relation != "" {
			out[n.Relation] = true
		}
		if n.CTE != "" {
			ctes = append(ctes, n.CTE)
		}
		for _, c := range n.Plans {
			nullable(c)
		}
	}
	var walk func(n planNode)
	walk = func(n planNode) {
		switch n.JoinType {
		case "Left":
			if len(n.Plans) > 1 {
				nullable(n.Plans[1])
			}
		case "Right":
			if len(n.Plans) > 0 {
				nullable(n.Plans[0])
			}
		case "Full":
			for _, c := range n.Plans {
				nullable(c)
			}
		}
		for _, c := range n.Plans {
			walk(c)
		}
	}
	walk(root)
	// A CTE on a nullable side is planned apart, as an init plan named
	// `CTE <name>`: its tables may be left out too.
	seen := map[string]bool{}
	for len(ctes) > 0 {
		name := ctes[0]
		ctes = ctes[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		var find func(n planNode)
		find = func(n planNode) {
			if n.Subplan == "CTE "+name {
				nullable(n)
				return
			}
			for _, c := range n.Plans {
				find(c)
			}
		}
		find(root)
	}
	return out, true
}

// planNode is the part of a node of EXPLAIN's JSON plan outerJoined reads.
type planNode struct {
	JoinType string     `json:"Join Type"`
	Relation string     `json:"Relation Name"`
	CTE      string     `json:"CTE Name"`
	Subplan  string     `json:"Subplan Name"`
	Plans    []planNode `json:"Plans"`
}
