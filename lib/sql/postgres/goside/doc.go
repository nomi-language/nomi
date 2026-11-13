// Package goside is the Go half of the Nomi module postgres: a pool of
// connections, or one connection, to a Postgres database over pgx, with the
// transaction and savepoint bookkeeping std/sql's Conn rules ask for, and
// the description of a schema and its statements that sqlkit's tool asks
// for (describe.go). The Nomi facade (postgres.nomi, describe.nomi) binds
// these functions and turns their plain structs into std/sql's values and
// errors.
//
// Every function that talks to the server takes the calling task's context
// first, which the Go binding fills (spec §19 *The task's context*), so a
// deadline or a cancelled task stops the statement.
//
// The browser build (goside_js.go) has no pgx: a page cannot open a TCP
// connection to a server, so every function there answers that Postgres
// cannot be reached from the browser. types.go and describe_types.go are
// the values that cross in every build.
//
// # Transactions
//
// The driver keeps the SQLite driver's transaction bookkeeping per
// transaction instead of per connection. A Session is one Nomi `Conn`: the
// pool or connection, or a transaction or savepoint at a depth with a
// token. A pool's transaction holds a connection acquired for it until it
// ends (Tx). One connection from Connect has one turn (DB.turn): a
// statement sent through it holds the turn while it runs, and a
// transaction from its begin until it ends, so a statement from another
// task waits for the transaction, under its own context, instead of
// failing or running inside it. Connections cancel a statement on the server when its context
// ends (keepOnCancel), since pgx's default breaks the socket and the
// connection with it.
//
// # The scratch server
//
// `postgres.scratch()` (Scratch, scratch.go) runs one throwaway cluster per
// process under `sh`, which stops it and removes its directory when its
// standard input, a pipe the process holds, closes at exit. The driver's
// tests and std/sql/testing's conformance suite run on it, and
// lib/jobs/postgres's tests run std/jobs/testing's suite there.
package goside
