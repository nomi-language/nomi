package goside

import (
	"database/sql"
	"strings"
)

// Constraint names. SQLite does not report a constraint's name: its
// message names the columns of a UNIQUE, PRIMARY KEY or NOT NULL
// constraint, the expression (or the name) of a CHECK, and nothing of a
// foreign key. So a constraint error is named here as Postgres names the
// constraint: the schema's own name when it gives one (`CONSTRAINT name`,
// or a unique index's name), else Postgres's default name: `<table>_pkey`,
// `<table>_<columns>_key`, `<table>_<columns>_fkey`,
// `<table>_<column>_not_null`, and `<table>_<column>_check` for a CHECK
// that reads one column or `<table>_check` for one that reads several.
// Postgres also cuts a default name to 63 bytes, which this does not.

// declared is one constraint of a table as its CREATE TABLE statement
// declares it.
type declared struct {
	kind    string // "pkey", "key", "fkey", "not_null", "check"
	name    string // "" when the schema names none
	columns []string
	check   string // a CHECK's expression, as written
}

// sqlToken is one token of SQL text: a word, a quoted name (unquoted), a
// string literal, or one punctuation character. start and end are its
// bytes in the text.
type sqlToken struct {
	text       string
	word       bool
	string     bool
	start, end int
}

// tokenize splits SQL into tokens, leaving out space and comments.
func tokenize(text string) []sqlToken {
	var out []sqlToken
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				i = len(text)
			} else {
				i += end + 4
			}
		case c == '\'':
			j := i + 1
			for j < len(text) {
				if text[j] == '\'' {
					if j+1 < len(text) && text[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			end := min(j+1, len(text))
			out = append(out, sqlToken{text: text[i:end], string: true, start: i, end: end})
			i = end
		case c == '"' || c == '`' || c == '[':
			closer := c
			if c == '[' {
				closer = ']'
			}
			j := i + 1
			var b strings.Builder
			for j < len(text) {
				if text[j] == closer {
					if closer != ']' && j+1 < len(text) && text[j+1] == closer {
						b.WriteByte(closer)
						j += 2
						continue
					}
					break
				}
				b.WriteByte(text[j])
				j++
			}
			end := min(j+1, len(text))
			out = append(out, sqlToken{text: b.String(), word: true, start: i, end: end})
			i = end
		case isWordByte(c):
			j := i
			for j < len(text) && isWordByte(text[j]) {
				j++
			}
			out = append(out, sqlToken{text: text[i:j], word: true, start: i, end: j})
			i = j
		default:
			out = append(out, sqlToken{text: text[i : i+1], start: i, end: i + 1})
			i++
		}
	}
	return out
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func is(t sqlToken, word string) bool {
	return t.word && strings.EqualFold(t.text, word)
}

// closing is the index of the ")" that closes the "(" at open.
func closing(tokens []sqlToken, open int) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch {
		case tokens[i].string || tokens[i].word:
		case tokens[i].text == "(":
			depth++
		case tokens[i].text == ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(tokens) - 1
}

// nameList reads the parenthesized list of names at open: `(a, b)`.
func nameList(tokens []sqlToken, open int) []string {
	if open >= len(tokens) || tokens[open].text != "(" {
		return nil
	}
	var out []string
	end := closing(tokens, open)
	for i := open + 1; i < end; i++ {
		if tokens[i].word && (i == open+1 || tokens[i-1].text == ",") {
			out = append(out, tokens[i].text)
		}
	}
	return out
}

// constraintsOf reads the constraints a CREATE TABLE statement declares.
func constraintsOf(create string) []declared {
	tokens := tokenize(create)
	open := -1
	for i, t := range tokens {
		if t.text == "(" {
			open = i
			break
		}
	}
	if open < 0 {
		return nil
	}
	end := closing(tokens, open)
	var out []declared
	// Each definition, between top-level commas.
	start := open + 1
	depth := 0
	for i := open + 1; i <= end; i++ {
		t := tokens[i]
		if !t.word && !t.string {
			switch t.text {
			case "(":
				depth++
			case ")":
				depth--
			}
		}
		if (t.text == "," && !t.word && !t.string && depth == 0) || i == end {
			out = append(out, definition(create, tokens[start:i])...)
			start = i + 1
		}
	}
	return out
}

// definition reads one column definition's constraints, or one table
// constraint.
func definition(text string, tokens []sqlToken) []declared {
	if len(tokens) == 0 {
		return nil
	}
	column := ""
	i := 0
	first := tokens[0]
	if !(is(first, "CONSTRAINT") || is(first, "PRIMARY") || is(first, "UNIQUE") || is(first, "CHECK") || is(first, "FOREIGN")) {
		column = first.text
		i = 1
	}
	var out []declared
	name := ""
	for i < len(tokens) {
		t := tokens[i]
		switch {
		case is(t, "CONSTRAINT") && i+1 < len(tokens):
			name = tokens[i+1].text
			i += 2
			continue
		case is(t, "PRIMARY"):
			cols := []string{column}
			if column == "" {
				cols = nameList(tokens, i+2)
			}
			out = append(out, declared{kind: "pkey", name: name, columns: cols})
		case is(t, "UNIQUE"):
			cols := []string{column}
			if column == "" {
				cols = nameList(tokens, i+1)
			}
			out = append(out, declared{kind: "key", name: name, columns: cols})
		case is(t, "NOT") && i+1 < len(tokens) && is(tokens[i+1], "NULL") && column != "":
			out = append(out, declared{kind: "not_null", name: name, columns: []string{column}})
			i += 2
			name = ""
			continue
		case is(t, "REFERENCES") && column != "":
			out = append(out, declared{kind: "fkey", name: name, columns: []string{column}})
		case is(t, "FOREIGN") && column == "":
			out = append(out, declared{kind: "fkey", name: name, columns: nameList(tokens, i+2)})
		case is(t, "CHECK") && i+1 < len(tokens) && tokens[i+1].text == "(":
			end := closing(tokens, i+1)
			expr := ""
			if end > i+2 {
				expr = text[tokens[i+2].start:tokens[end-1].end]
			}
			out = append(out, declared{kind: "check", name: name, check: expr})
			name = ""
			i = end + 1
			continue
		case t.text == "(" && !t.word && !t.string:
			// A type's precision, a default's expression, a references
			// list: nothing in it declares a constraint.
			i = closing(tokens, i) + 1
			continue
		default:
			i++
			continue
		}
		name = ""
		i++
	}
	return out
}

// tableSQL is the CREATE TABLE statement of table, a temporary table's
// first, as the schema keeps it.
func (c *Conn) tableSQL(table string) string {
	for _, schema := range []string{"sqlite_temp_schema", "sqlite_schema"} {
		var text sql.NullString
		row := c.conn.QueryRowContext(background, "SELECT sql FROM "+schema+" WHERE type = 'table' AND name = ?1 COLLATE NOCASE", table)
		if row.Scan(&text) == nil && text.Valid {
			return text.String
		}
	}
	return ""
}

func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// declaredName is the name the schema gives table's constraint of kind
// over columns, or "".
func (c *Conn) declaredName(table, kind string, columns []string) string {
	for _, d := range constraintsOf(c.tableSQL(table)) {
		if d.kind == kind && d.name != "" && sameColumns(d.columns, columns) {
			return d.name
		}
	}
	return ""
}

// defaultName is Postgres's name for a constraint of kind on columns of
// table that the schema does not name.
func defaultName(table, kind string, columns []string) string {
	if kind == "pkey" {
		return table + "_pkey"
	}
	parts := append([]string{table}, columns...)
	return strings.Join(append(parts, kind), "_")
}

// uniqueName names a UNIQUE or PRIMARY KEY violation over columns of table:
// the unique index over exactly those columns when the schema made one with
// CREATE UNIQUE INDEX, the constraint's name when the schema gives one,
// else Postgres's default.
func (c *Conn) uniqueName(table string, columns []string, primary bool) string {
	if index := c.uniqueIndex(table, columns); index != "" {
		return index
	}
	kind := "key"
	if primary {
		kind = "pkey"
	}
	if name := c.declaredName(table, kind, columns); name != "" {
		return name
	}
	return defaultName(table, kind, columns)
}

// notNullName names a NOT NULL violation of table's column.
func (c *Conn) notNullName(table, column string) string {
	if name := c.declaredName(table, "not_null", []string{column}); name != "" {
		return name
	}
	return defaultName(table, "not_null", []string{column})
}

// checkName names a CHECK violation SQLite reported as reported (the
// constraint's name, or its expression when it has none), with the table
// it is on and the one column it reads (or ""). It looks in target, the
// table the statement writes, first, then in every table.
func (c *Conn) checkName(reported, target string) (name, table, column string) {
	tables := []string{}
	if target != "" {
		tables = append(tables, target)
	}
	rows, err := c.conn.QueryContext(background, "SELECT name FROM sqlite_temp_schema WHERE type = 'table' UNION ALL SELECT name FROM sqlite_schema WHERE type = 'table'")
	if err == nil {
		for rows.Next() {
			var t string
			if rows.Scan(&t) == nil {
				tables = append(tables, t)
			}
		}
		rows.Close()
	}
	want := squash(reported)
	for _, t := range tables {
		columns := c.columnNames(t)
		for _, d := range constraintsOf(c.tableSQL(t)) {
			if d.kind != "check" {
				continue
			}
			if d.name != "" && d.name != reported {
				continue
			}
			if d.name == "" && squash(d.check) != want {
				continue
			}
			read := readColumns(d.check, columns)
			one := ""
			if len(read) == 1 {
				one = read[0]
			}
			if d.name != "" {
				return d.name, t, one
			}
			if one != "" {
				return t + "_" + one + "_check", t, one
			}
			return t + "_check", t, ""
		}
	}
	return reported, "", ""
}

// squash is SQL text with its runs of space made one space.
func squash(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// columnNames are table's columns.
func (c *Conn) columnNames(table string) []string {
	rows, err := c.conn.QueryContext(background, "SELECT name FROM pragma_table_info(?1)", table)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			out = append(out, name)
		}
	}
	return out
}

// readColumns are the columns an expression reads, each once, in the order
// they appear.
func readColumns(expr string, columns []string) []string {
	var out []string
	for _, t := range tokenize(expr) {
		if !t.word {
			continue
		}
		for _, col := range columns {
			if strings.EqualFold(t.text, col) && !contains(out, col) {
				out = append(out, col)
			}
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// target is the table a statement writes: INSERT INTO t, REPLACE INTO t,
// UPDATE t, DELETE FROM t (with OR ... and a schema allowed), or "".
func target(query string) string {
	tokens := tokenize(query)
	i := 0
	if i < len(tokens) && is(tokens[i], "WITH") {
		return ""
	}
	if i >= len(tokens) {
		return ""
	}
	switch {
	case is(tokens[i], "INSERT"), is(tokens[i], "REPLACE"):
		for i < len(tokens) && !is(tokens[i], "INTO") {
			i++
		}
	case is(tokens[i], "UPDATE"):
		if i+1 < len(tokens) && is(tokens[i+1], "OR") {
			i += 2
		}
	case is(tokens[i], "DELETE"):
		i++
	default:
		return ""
	}
	i++
	if i >= len(tokens) || !tokens[i].word {
		return ""
	}
	name := tokens[i].text
	if i+2 < len(tokens) && tokens[i+1].text == "." && tokens[i+2].word {
		name = tokens[i+2].text
	}
	return name
}

// violation is a foreign key a row breaks: the child table and the
// columns of its foreign key that point at the missing row.
type violation struct {
	table   string
	columns []string
}

// foreignKeyViolation finds the foreign key a failed statement broke.
// SQLite says only that one was: the deferred violations of an open
// transaction are listed by foreign_key_check, and an immediate one is
// found by running the statement again in a savepoint with the foreign
// keys deferred, listing what it would break, and rolling it back. The
// caller holds c.mu.
func (c *Conn) foreignKeyViolation(query string, args []any) (violation, bool) {
	if v, ok := c.foreignKeyCheck(); ok {
		return v, true
	}
	if query == "" {
		return violation{}, false
	}
	var deferred int64
	if c.conn.QueryRowContext(background, "PRAGMA defer_foreign_keys").Scan(&deferred) != nil {
		return violation{}, false
	}
	if _, err := c.conn.ExecContext(background, "SAVEPOINT nomi_foreign_key"); err != nil {
		return violation{}, false
	}
	defer func() {
		c.conn.ExecContext(background, "ROLLBACK TO SAVEPOINT nomi_foreign_key")
		c.conn.ExecContext(background, "RELEASE SAVEPOINT nomi_foreign_key")
		if deferred == 0 {
			c.conn.ExecContext(background, "PRAGMA defer_foreign_keys = OFF")
		}
	}()
	if _, err := c.conn.ExecContext(background, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return violation{}, false
	}
	if _, err := c.conn.ExecContext(background, query, args...); err != nil {
		return violation{}, false
	}
	return c.foreignKeyCheck()
}

// foreignKeyCheck is the first row foreign_key_check lists, in the main
// database or the temporary one.
func (c *Conn) foreignKeyCheck() (violation, bool) {
	for _, schema := range []string{"main", "temp"} {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int64
		row := c.conn.QueryRowContext(background, "PRAGMA "+schema+".foreign_key_check")
		if row.Scan(&table, &rowid, &parent, &fkid) != nil {
			continue
		}
		rows, err := c.conn.QueryContext(background, "SELECT \"from\" FROM pragma_foreign_key_list(?1) WHERE id = ?2 ORDER BY seq", table, fkid)
		if err != nil {
			return violation{table: table}, true
		}
		var columns []string
		for rows.Next() {
			var col string
			if rows.Scan(&col) == nil {
				columns = append(columns, col)
			}
		}
		rows.Close()
		return violation{table: table, columns: columns}, true
	}
	return violation{}, false
}

// foreignKeyName names a broken foreign key: the schema's name for it, or
// Postgres's default.
func (c *Conn) foreignKeyName(v violation) string {
	if name := c.declaredName(v.table, "fkey", v.columns); name != "" {
		return name
	}
	return defaultName(v.table, "fkey", v.columns)
}
