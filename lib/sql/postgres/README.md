# postgres

The Postgres driver for Nomi's `std/sql`, over
[`pgx`](https://pkg.go.dev/github.com/jackc/pgx/v5), a pure-Go Postgres
client: a program that uses it needs no C compiler and no `libpq`.
`postgres.pool(url)` returns a `Conn`, std/sql's interface, so code
written against `Conn` runs on it unchanged.

```
nomi add github.com/nomi-language/nomi/lib/sql/postgres
```

```nomi
import
    std::io
    std::sql.{self, Conn, Error, Row, Sql}
    postgres
end

fn main() -> Result<Unit, Error>
    db = postgres.pool("postgres://localhost/notes")
    defer sql.close_quietly(db)

    _ = Conn.exec(
        db,
        Sql"CREATE TABLE IF NOT EXISTS notes (id BIGSERIAL PRIMARY KEY, body TEXT NOT NULL)",
    )?

    _ = Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"hello"})")?
    rows = Conn.query(db, Sql"SELECT body FROM notes")?

    Iter.each(rows, fn row ->
        io.print(
            Row.get<String>(row, "body")
            |> Result.with_default("?")
        )
    end)

    Ok(Unit)
end
```

A release `nomi` carries the driver's Go code prebuilt, so a program that
uses it needs no Go toolchain while the module is at the release's commit,
which `nomi add` takes and `nomi update` restores. Otherwise the Go code is
built into the program's runner the first time a program that uses it
runs, with the `go` on `PATH` or the Go toolchain `nomi` downloads.

## What it does

- `postgres.pool(url)` is a pool of connections. It connects lazily, as
  Go's `sql.Open` does: an unreachable server, or a URL that does not
  parse, is `Error.Connection` from the first statement, so a program that
  must fail early calls `Conn.ping`. A statement runs on any connection of
  the pool.
- `postgres.connect(url)` is one connection, opened now. A temporary table
  lives on one connection, so code that uses one connects.
- `Conn.begin_transaction` is `BEGIN` on a connection the transaction holds until it
  commits or rolls back; on a transaction it is a savepoint, which
  `commit` releases and `rollback` rolls back to. A statement must run on
  the innermost open transaction or savepoint, and the driver refuses one
  sent through an outer transaction.
- One `Conn` may be shared by every task of a program. On a pool, a
  statement from another task while a transaction is open runs on another
  connection. On a `connect` connection, it waits for the transaction to
  end and then runs outside it, for as long as the waiting task's context
  allows (then `Error.Timeout`); inside the transaction, use the `Conn` its
  `begin` returned, since a statement through the connection would wait
  for its own transaction.
- Parameters are bound as `$1`, `$2`, ..., never spliced into the SQL.
- Every statement runs under the calling task's context. A
  `with App.context = Context.with_timeout(...)` deadline, or a cancelled
  task, cancels the statement on the server and answers `Error.Timeout`,
  and the connection stays usable.
- A constraint error names the constraint as Postgres does, with its table
  and columns. A not-null violation is named as Postgres 18 names it
  (`users_email_not_null`), which older servers do not report. A
  serialization failure or a deadlock is `Error.Serialization`; any other
  error is `Error.Database` with its SQLSTATE.
- `postgres.listen(url, channel)` is a `Listener` on a connection of its
  own: `Listener.wait` answers the next `Notification` sent on the
  channel (`NOTIFY` or `pg_notify`), waiting as long as the task's context
  allows. `Listener.close` ends a `wait` running on another task with
  `Error.Closed`.
- `Listener.receiver(listener, supervisor)` is the listener's
  notifications as a channel, `Receiver<Result<Notification, Error>>`, so
  one can be an arm of `Channel.select` beside a timer:

  ```nomi
  notes = Listener.receiver(listener, App.workers)

  Channel.select([
      Arm.receive(notes, fn note -> handle(note)),
      Arm.after(Duration.seconds(30), fn -> poll()),
  ])
  ```

  A task under the supervisor, which an app creates in `boot`, runs
  `Listener.forward`, which a program can also run in a task of its own.
  The channel closes when the task ends: after `close`, when the task is
  cancelled (as the supervisor's drain does at shutdown), or after a
  connection failure, which arrives first as a last value, `Err(e)`. So
  `None` means the listener was closed or the program is stopping, and
  `Err` means the connection failed. While the channel is full the task
  looks every 200 milliseconds at whether the listener was closed, so
  `close` ends it even when nothing reads the channel.
- `postgres.scratch()` is the URL of a fresh empty database on a
  throwaway server, for tests. The first call in a process starts the
  server with the `initdb` and `postgres` on `PATH`, in a temporary
  directory, on a free local port, with `fsync` off; it stops and its
  directory is removed when the process ends, however it ends. Each call
  makes another database on that server. Without `initdb` and `postgres`
  on `PATH`, or on Windows (it needs a POSIX shell), it returns
  `Error.Connection` saying so.
- It passes `std/sql_testing`'s conformance suite (every rule;
  `postgres_test.nomi`).
- `postgres/describe` is what `nomi tool sqlkit generate` asks a Postgres
  database. `describe.describe(url, migrations, statements)` makes a
  scratch database, `nomi_prepare_<hash>_<random>`, on the development
  server at `url`, applies the migrations, and answers the tables, each
  statement's inferred parameter types and its result columns: their
  types, the table column each reads, and whether it may be NULL. A column
  is non-null only when it reads a `NOT NULL` column that no outer join of
  the statement can leave out, which the statement's generic plan shows
  (`EXPLAIN EXECUTE` under `plan_cache_mode = force_generic_plan`, so
  Postgres 12 or later). A view's column is described through the view's
  definition, as the table column that one reads. It drops the scratch
  database before it answers, also when it is cancelled.
- In the browser (the tour's wasm build) the Go side has no pgx: a page
  cannot open a TCP connection, so every call answers `Error.Connection`
  saying Postgres cannot be reached from the browser.

## Values

| `Value` | Written as | Read back from |
|---|---|---|
| `Int` | `bigint`; past 64 bits, `numeric` (a `BIGINT` column refuses it) | `smallint`, `integer`, `bigint` |
| `Float` | `double precision` | `real`, `double precision`; also `numeric`'s `NaN` and infinities |
| `Decimal` | `numeric` | `numeric` |
| `Text` | `text` | `text`, `varchar`, `char`, and any type with no `Value` of its own (an interval, an enum, `citext`), as its text |
| `Bool` | `boolean` | `boolean` |
| `Bytes` | `bytea` | `bytea` |
| `Date`, `Time`, `Timestamp`, `Timestamptz` | `date`, `time`, `timestamp`, `timestamptz` | the same |
| `Uuid` | `uuid` | `uuid` |
| `Json` | its text, to `json` or `jsonb` | `json`, `jsonb` |
| `Array` | a one-dimensional array of its elements' type, which must all be one kind | a one-dimensional array |

A `List<T>` is an `Array` parameter, so `WHERE id = ANY(${ids})` matches a
list of ids, and an array column reads as a `List<T>`
(`Row.get<List<Int>>`). A time keeps microseconds, as Postgres stores it.

## Not built

- `copy_from` (`COPY ... FROM STDIN`). It needs the driver's own
  connection behind a `Conn`, and a transaction's `Conn` reaches a program
  as the std interface, which offers no way back to it.
- Arrays of more than one dimension, and composite values, which are
  refused with a message.
- `postgres.scratch()` on Windows.

## Tests

```
nomi test lib/sql/postgres
go test -C lib/sql/postgres ./...
```

Both run on `postgres.scratch()`'s throwaway server, so they need `initdb`
and `postgres` on `PATH`. Without them the tests fail, since a Nomi test
cannot skip itself; `TestLibModules` (`cmd/nomi`) skips this module
instead.
