//go:build !js

package goside

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is a pool, or one connection. Every Session of it shares it, and
// closing any of them closes it.
type DB struct {
	mu sync.Mutex
	// pool is the pool, for a DB from Pool; conn the connection, for one
	// from Connect. Exactly one is set, unless the pool's URL did not
	// parse, when openErr says why.
	pool    *pgxpool.Pool
	conn    *pgx.Conn
	openErr error
	// turn is conn's one turn, which pgx does not share: a statement sent
	// through the connection holds it while it runs, and a transaction
	// from its begin until it ends, so a statement from another task waits
	// for an open transaction rather than run inside it.
	turn   chan struct{}
	closed bool
	// txs are the transactions open on the DB. A DB from Connect has one
	// at most, since a transaction is a property of a connection.
	txs map[*Tx]bool
}

// Tx is one transaction, on a connection it holds until it ends: a pool's
// connection acquired for it, or the DB's own.
type Tx struct {
	db      *DB
	mu      sync.Mutex
	conn    *pgx.Conn
	release func()
	// stack holds the token of the transaction (depth 1) and of each
	// savepoint inside it (depth 2 and more) while it is open.
	stack []int64
	next  int64
}

// Session is one Nomi Conn: the DB itself (tx nil), or a transaction or
// savepoint, at depth with token.
type Session struct {
	db    *DB
	tx    *Tx
	depth int64
	token int64
}

// Connect opens one connection to the database at url.
func Connect(ctx context.Context, url string) (*Session, error) {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	keepOnCancel(&config.Config)
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	return &Session{db: &DB{conn: conn, txs: map[*Tx]bool{}, turn: make(chan struct{}, 1)}}, nil
}

// Pool makes a pool of connections to the database at url. It connects
// lazily, as database/sql's Open does: an unreachable server, or a URL that
// does not parse, is the first statement's failure.
func Pool(url string) *Session {
	db := &DB{txs: map[*Tx]bool{}}
	config, err := pgxpool.ParseConfig(url)
	if err == nil {
		keepOnCancel(&config.ConnConfig.Config)
		db.pool, err = pgxpool.NewWithConfig(context.Background(), config)
	}
	db.openErr = err
	return &Session{db: db}
}

// keepOnCancel makes a statement whose context ends cancelled on the
// server, so the connection stays usable. pgx's default breaks the
// connection's socket instead, and a timed-out statement would close it.
// The socket's deadline is set only if the server has not answered the
// cancel within cancelGrace.
func keepOnCancel(config *pgconn.Config) {
	config.BuildContextWatcherHandler = func(conn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: conn, DeadlineDelay: cancelGrace}
	}
}

const cancelGrace = 5 * time.Second

// take waits for the connection's turn, or for ctx to end, which is a
// Timeout. The caller does not hold db.mu. Once it has the turn the DB may
// have closed meanwhile, which is Closed, with the turn given back.
func (db *DB) take(ctx context.Context) Failure {
	select {
	case db.turn <- struct{}{}:
	case <-ctx.Done():
		return Failure{Kind: FailTimeout, Message: ctx.Err().Error()}
	}
	db.mu.Lock()
	closed := db.closed
	db.mu.Unlock()
	if closed {
		db.give()
		return Failure{Kind: FailClosed}
	}
	return Failure{}
}

// give gives the connection's turn back.
func (db *DB) give() { <-db.turn }

// querier is what a statement runs on: a pool or a connection.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// target is what a statement on s runs on, and the function that releases
// it, or the failure that stops it. A statement must run on the innermost
// open transaction or savepoint of its connection: one sent through an
// outer level would belong to the savepoint, and roll back with it. A
// statement sent through a single connection waits for its turn (DB.turn).
func (s *Session) target(ctx context.Context) (querier, func(), Failure) {
	db := s.db
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil, nil, Failure{Kind: FailClosed}
	}
	if db.openErr != nil {
		db.mu.Unlock()
		return nil, nil, Failure{Kind: FailConnection, Message: db.openErr.Error()}
	}
	if s.tx == nil {
		if db.conn == nil {
			db.mu.Unlock()
			return db.pool, func() {}, Failure{}
		}
		db.mu.Unlock()
		if f := db.take(ctx); f.Kind != FailNone {
			return nil, nil, f
		}
		return db.conn, db.give, Failure{}
	}
	db.mu.Unlock()
	tx := s.tx
	tx.mu.Lock()
	if f := s.usable(); f.Kind != FailNone {
		tx.mu.Unlock()
		return nil, nil, f
	}
	if int64(len(tx.stack)) > s.depth {
		tx.mu.Unlock()
		return nil, nil, Failure{Kind: FailDatabase, Code: "25001",
			Message: "a savepoint is open inside this transaction; run the statement on the Conn its begin answered"}
	}
	return tx.conn, tx.mu.Unlock, Failure{}
}

// usable is the failure of using the transaction or savepoint s: Closed
// after a close, Finished once it has ended. The caller holds s.tx.mu.
func (s *Session) usable() Failure {
	s.db.mu.Lock()
	closed := s.db.closed
	s.db.mu.Unlock()
	if closed {
		return Failure{Kind: FailClosed}
	}
	tx := s.tx
	if int64(len(tx.stack)) < s.depth || tx.stack[s.depth-1] != s.token {
		return Failure{Kind: FailFinished}
	}
	return Failure{}
}

// Run runs a statement on s. parts is the SQL around each parameter; each
// parameter is bound as $1, $2, ..., never spliced into the text.
func Run(ctx context.Context, s *Session, parts []string, params []Cell) Answer {
	q, release, f := s.target(ctx)
	if f.Kind != FailNone {
		return Answer{Failure: f}
	}
	defer release()
	args, f := values(params)
	if f.Kind != FailNone {
		return Answer{Failure: f}
	}
	rows, err := q.Query(ctx, join(parts), args...)
	if err != nil {
		return Answer{Failure: failure(ctx, err)}
	}
	defer rows.Close()
	answer := Answer{Columns: columns(rows), Rows: [][]Cell{}}
	err = each(rows, func(cells []Cell) bool {
		answer.Rows = append(answer.Rows, cells)
		return true
	})
	if err != nil {
		return Answer{Failure: failure(ctx, err)}
	}
	return answer
}

// Exec runs a statement that writes and answers how many rows it changed.
func Exec(ctx context.Context, s *Session, parts []string, params []Cell) Answer {
	q, release, f := s.target(ctx)
	if f.Kind != FailNone {
		return Answer{Failure: f}
	}
	defer release()
	args, f := values(params)
	if f.Kind != FailNone {
		return Answer{Failure: f}
	}
	tag, err := q.Exec(ctx, join(parts), args...)
	if err != nil {
		return Answer{Failure: failure(ctx, err)}
	}
	return Answer{Changed: tag.RowsAffected()}
}

// EachRow runs a statement and calls f with each row in order, stopping
// when f returns false. The connection is held while it runs, so f must
// not use the same connection.
func EachRow(ctx context.Context, s *Session, parts []string, params []Cell, f func(Row) bool) Failure {
	q, release, fl := s.target(ctx)
	if fl.Kind != FailNone {
		return fl
	}
	defer release()
	args, fl := values(params)
	if fl.Kind != FailNone {
		return fl
	}
	rows, err := q.Query(ctx, join(parts), args...)
	if err != nil {
		return failure(ctx, err)
	}
	defer rows.Close()
	names := columns(rows)
	err = each(rows, func(cells []Cell) bool {
		return f(Row{Columns: names, Cells: cells})
	})
	if err != nil {
		return failure(ctx, err)
	}
	return Failure{}
}

// Begin starts a transaction on the DB, or a savepoint inside the
// transaction or savepoint s. On a failure it answers s itself beside it.
func Begin(ctx context.Context, s *Session) (*Session, Failure) {
	if s.tx == nil {
		return s.beginTransaction(ctx)
	}
	tx := s.tx
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if f := s.usable(); f.Kind != FailNone {
		return s, f
	}
	if int64(len(tx.stack)) != s.depth {
		return s, Failure{Kind: FailDatabase, Code: "25001", Message: "a savepoint is already open inside this transaction"}
	}
	next := s.depth + 1
	if _, err := tx.conn.Exec(ctx, fmt.Sprintf("SAVEPOINT sp%d", next)); err != nil {
		return s, failure(ctx, err)
	}
	tx.next++
	tx.stack = append(tx.stack, tx.next)
	return &Session{db: s.db, tx: tx, depth: next, token: tx.next}, Failure{}
}

func (s *Session) beginTransaction(ctx context.Context) (*Session, Failure) {
	db := s.db
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return s, Failure{Kind: FailClosed}
	}
	if db.openErr != nil {
		db.mu.Unlock()
		return s, Failure{Kind: FailConnection, Message: db.openErr.Error()}
	}
	tx := &Tx{db: db}
	if db.conn != nil {
		db.mu.Unlock()
		// The transaction holds the connection's turn until it ends, so a
		// statement or transaction already on it finishes first.
		if f := db.take(ctx); f.Kind != FailNone {
			return s, f
		}
		db.mu.Lock()
		db.txs[tx] = true
		db.mu.Unlock()
		tx.conn, tx.release = db.conn, db.give
	} else {
		db.mu.Unlock()
		pooled, err := db.pool.Acquire(ctx)
		if err != nil {
			return s, failure(ctx, err)
		}
		tx.conn, tx.release = pooled.Conn(), pooled.Release
		db.mu.Lock()
		if db.closed {
			db.mu.Unlock()
			pooled.Release()
			return s, Failure{Kind: FailClosed}
		}
		db.txs[tx] = true
		db.mu.Unlock()
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if _, err := tx.conn.Exec(ctx, "BEGIN"); err != nil {
		db.end(tx)
		return s, failure(ctx, err)
	}
	tx.next = 1
	tx.stack = []int64{1}
	return &Session{db: db, tx: tx, depth: 1, token: 1}, Failure{}
}

// end forgets tx and gives its connection back. The caller holds tx.mu.
func (db *DB) end(tx *Tx) {
	db.mu.Lock()
	delete(db.txs, tx)
	db.mu.Unlock()
	tx.stack = nil
	if tx.release != nil {
		tx.release()
		tx.release = nil
	}
}

// Commit commits the transaction s, or releases the savepoint s. One that
// has ended is Finished.
func Commit(ctx context.Context, s *Session) Failure {
	if s.tx == nil {
		return s.notATransaction("commit")
	}
	tx := s.tx
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if f := s.usable(); f.Kind != FailNone {
		return f
	}
	if s.depth == 1 {
		_, err := tx.conn.Exec(ctx, "COMMIT")
		s.db.end(tx)
		if err != nil {
			return failure(ctx, err)
		}
		return Failure{}
	}
	if _, err := tx.conn.Exec(ctx, fmt.Sprintf("RELEASE SAVEPOINT sp%d", s.depth)); err != nil {
		return failure(ctx, err)
	}
	tx.stack = tx.stack[:s.depth-1]
	return Failure{}
}

// Rollback rolls back the transaction s, or to the savepoint s. Rolling
// back one that has ended does nothing.
func Rollback(ctx context.Context, s *Session) Failure {
	if s.tx == nil {
		return s.notATransaction("rollback")
	}
	tx := s.tx
	tx.mu.Lock()
	defer tx.mu.Unlock()
	switch f := s.usable(); f.Kind {
	case FailFinished:
		return Failure{}
	case FailNone:
	default:
		return f
	}
	if s.depth == 1 {
		// A rollback runs even when the task's context has ended, since a
		// transaction left open would hold its connection.
		_, err := tx.conn.Exec(context.WithoutCancel(ctx), "ROLLBACK")
		s.db.end(tx)
		if err != nil {
			return failure(ctx, err)
		}
		return Failure{}
	}
	name := fmt.Sprintf("sp%d", s.depth)
	if _, err := tx.conn.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); err != nil {
		return failure(ctx, err)
	}
	if _, err := tx.conn.Exec(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return failure(ctx, err)
	}
	tx.stack = tx.stack[:s.depth-1]
	return Failure{}
}

func (s *Session) notATransaction(what string) Failure {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.closed {
		return Failure{Kind: FailClosed}
	}
	return Failure{Kind: FailDatabase, Code: "25P01", Message: what + " on a connection that is not a transaction"}
}

// Ping checks that the server answers.
func Ping(ctx context.Context, s *Session) Failure {
	q, release, f := s.pingTarget(ctx)
	if f.Kind != FailNone {
		return f
	}
	defer release()
	if _, err := q.Exec(ctx, "SELECT 1"); err != nil {
		return failure(ctx, err)
	}
	return Failure{}
}

// pingTarget is target without the rule that a statement runs on the
// innermost level: a ping changes nothing.
func (s *Session) pingTarget(ctx context.Context) (querier, func(), Failure) {
	if s.tx == nil {
		db := s.db
		db.mu.Lock()
		switch {
		case db.closed:
			db.mu.Unlock()
			return nil, nil, Failure{Kind: FailClosed}
		case db.openErr != nil:
			db.mu.Unlock()
			return nil, nil, Failure{Kind: FailConnection, Message: db.openErr.Error()}
		case db.conn == nil:
			db.mu.Unlock()
			return db.pool, func() {}, Failure{}
		case len(db.txs) > 0:
			// The connection is the transaction's: ping through it.
			var tx *Tx
			for open := range db.txs {
				tx = open
			}
			db.mu.Unlock()
			tx.mu.Lock()
			return tx.conn, tx.mu.Unlock, Failure{}
		}
		db.mu.Unlock()
		if f := db.take(ctx); f.Kind != FailNone {
			return nil, nil, f
		}
		return db.conn, db.give, Failure{}
	}
	tx := s.tx
	tx.mu.Lock()
	if f := s.usable(); f.Kind != FailNone {
		tx.mu.Unlock()
		return nil, nil, f
	}
	return tx.conn, tx.mu.Unlock, Failure{}
}

// Close closes the DB, whichever of its sessions it is called on, rolling
// back every transaction still open. Closing it again does nothing.
func Close(s *Session) Failure {
	db := s.db
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return Failure{}
	}
	db.closed = true
	open := make([]*Tx, 0, len(db.txs))
	for tx := range db.txs {
		open = append(open, tx)
	}
	db.mu.Unlock()
	ctx := context.Background()
	var first error
	for _, tx := range open {
		tx.mu.Lock()
		if _, err := tx.conn.Exec(ctx, "ROLLBACK"); err != nil && first == nil {
			first = err
		}
		db.end(tx)
		tx.mu.Unlock()
	}
	switch {
	case db.conn != nil:
		// A statement running on the connection finishes first; one
		// waiting for the turn finds the DB closed.
		db.turn <- struct{}{}
		if err := db.conn.Close(ctx); err != nil && first == nil {
			first = err
		}
		db.give()
	case db.pool != nil:
		db.pool.Close()
	}
	if first != nil {
		return Failure{Kind: FailConnection, Message: first.Error()}
	}
	return Failure{}
}

// join writes the SQL with $1, $2, ... between the parts.
func join(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("$")
			b.WriteString(strconv.Itoa(i))
		}
		b.WriteString(p)
	}
	return b.String()
}

func columns(rows pgx.Rows) []string {
	fields := rows.FieldDescriptions()
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return names
}

// each reads rows, passing each to f until it returns false.
func each(rows pgx.Rows, f func([]Cell) bool) error {
	fields := rows.FieldDescriptions()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return err
		}
		raw := rows.RawValues()
		cells := make([]Cell, len(values))
		for i, v := range values {
			c, err := cellOf(v, raw[i], fields[i])
			if err != nil {
				return err
			}
			cells[i] = c
		}
		if !f(cells) {
			break
		}
	}
	return rows.Err()
}

// failure maps the error of a call that ran under ctx to a Failure. A
// statement the context ended is a Timeout, whatever error it surfaced as.
func failure(ctx context.Context, err error) Failure {
	if ctx.Err() != nil {
		return Failure{Kind: FailTimeout, Message: ctx.Err().Error()}
	}
	var mismatch mismatchError
	if errors.As(err, &mismatch) {
		return Failure{Kind: FailDatabase, Code: "22000", Message: string(mismatch)}
	}
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		var ce *pgconn.ConnectError
		if errors.As(err, &ce) || pgconn.SafeToRetry(err) || pgconn.Timeout(err) {
			return Failure{Kind: FailConnection, Message: err.Error()}
		}
		return Failure{Kind: FailDatabase, Message: err.Error()}
	}
	f := Failure{Kind: FailDatabase, Code: pe.Code, Message: pe.Message, Name: pe.ConstraintName, Table: pe.TableName, Column: pe.ColumnName}
	switch pe.Code {
	case "23505":
		f.Kind, f.Constraint = FailConstraint, ConstraintUnique
		f.Column = keyColumns(pe.Detail)
	case "23503":
		f.Kind, f.Constraint = FailConstraint, ConstraintForeignKey
		f.Column = keyColumns(pe.Detail)
	case "23502":
		f.Kind, f.Constraint = FailConstraint, ConstraintNotNull
		if f.Name == "" && f.Table != "" && f.Column != "" {
			// Postgres before 18 reports a not-null violation by column
			// alone; std/sql names it as Postgres 18 names the constraint.
			f.Name = f.Table + "_" + f.Column + "_not_null"
		}
	case "23514":
		f.Kind, f.Constraint = FailConstraint, ConstraintCheck
	case "40001", "40P01":
		f.Kind = FailSerialization
	case "57014":
		f.Kind = FailTimeout
	default:
		if strings.HasPrefix(pe.Code, "08") || strings.HasPrefix(pe.Code, "57P") {
			f.Kind = FailConnection
		}
	}
	if f.Kind != FailConstraint {
		f.Name, f.Table, f.Column = "", "", ""
	}
	return f
}

// keyColumns reads "Key (a, b)=(1, 2) already exists." as "a,b".
func keyColumns(detail string) string {
	rest, ok := strings.CutPrefix(detail, "Key (")
	if !ok {
		return ""
	}
	list, _, ok := strings.Cut(rest, ")=")
	if !ok {
		return ""
	}
	names := strings.Split(list, ",")
	for i, n := range names {
		names[i] = strings.TrimSpace(n)
	}
	return strings.Join(names, ",")
}
