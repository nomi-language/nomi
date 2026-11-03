//go:build js

package goside

import (
	"context"
	"errors"
)

// The browser build: a page cannot open a TCP connection to a Postgres
// server, so every function answers that it cannot be reached, as a
// connection failure. postgres.nomi and describe.nomi bind the same names
// in every build, so these declare each of them over the shared values of
// types.go and describe_types.go.

const unreachable = "Postgres cannot be reached from the browser: a page cannot open a TCP connection to a server"

var errUnreachable = errors.New(unreachable)

func unreachableFailure() Failure {
	return Failure{Kind: FailConnection, Message: unreachable}
}

// Session is a connection that was never opened.
type Session struct{}

// Listener is a listener that was never opened.
type Listener struct{}

func Connect(ctx context.Context, url string) (*Session, error) { return nil, errUnreachable }

func Pool(url string) *Session { return &Session{} }

func Scratch(ctx context.Context) (string, error) { return "", errUnreachable }

func Run(ctx context.Context, s *Session, parts []string, params []Cell) Answer {
	return Answer{Columns: []string{}, Rows: [][]Cell{}, Failure: unreachableFailure()}
}

func Exec(ctx context.Context, s *Session, parts []string, params []Cell) Answer {
	return Answer{Columns: []string{}, Rows: [][]Cell{}, Failure: unreachableFailure()}
}

func EachRow(ctx context.Context, s *Session, parts []string, params []Cell, f func(Row) bool) Failure {
	return unreachableFailure()
}

func Begin(ctx context.Context, s *Session) (*Session, Failure) { return s, unreachableFailure() }

func Commit(ctx context.Context, s *Session) Failure { return unreachableFailure() }

func Rollback(ctx context.Context, s *Session) Failure { return unreachableFailure() }

func Ping(ctx context.Context, s *Session) Failure { return unreachableFailure() }

func Close(s *Session) Failure { return Failure{} }

func Listen(ctx context.Context, url, channel string) (*Listener, error) {
	return nil, errUnreachable
}

func Wait(ctx context.Context, l *Listener) Notice {
	return Notice{Failure: unreachableFailure()}
}

func Waiting(l *Listener) bool { return false }

func Received(l *Listener) int64 { return 0 }

func IsClosed(l *Listener) bool { return true }

func CloseListener(l *Listener) Failure { return Failure{} }

func Describe(ctx context.Context, url string, migrations []Migration, statements []string) Description {
	return Description{Tables: []Table{}, Statements: []Statement{}, Error: unreachable, Offset: -1}
}
