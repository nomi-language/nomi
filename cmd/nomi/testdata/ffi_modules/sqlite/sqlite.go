// Package sqlite is the Go half of the Nomi sqlite binding in sqlite.nomi.
// It wraps database/sql over modernc.org/sqlite, a pure-Go SQLite driver, so
// building it needs no C toolchain.
package sqlite

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Conn is an open database. Nomi code holds it as an opaque handle.
type Conn struct {
	db *sql.DB
}

// Open opens or creates the database at path and checks that it answers.
// The path ":memory:" opens a private in-memory database.
func Open(path string) (*Conn, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// Each connection to ":memory:" is a separate database, so keep one.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Conn{db: db}, nil
}

// Exec runs a statement that returns no rows. Each ? in query is bound to
// the next element of args.
func Exec(c *Conn, query string, args []string) error {
	_, err := c.db.Exec(query, bindings(args)...)
	return err
}

// QueryAll runs a query and returns every row, each column as a string.
// NULL reads as the empty string.
func QueryAll(c *Conn, query string, args []string) ([][]string, error) {
	rows, err := c.db.Query(query, bindings(args)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := [][]string{}
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = v.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Close closes the database. Closing it twice is not an error.
func Close(c *Conn) error {
	return c.db.Close()
}

func bindings(args []string) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}
