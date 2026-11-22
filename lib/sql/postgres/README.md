# postgres

The Postgres driver for Nomi's `std/sql`, written in Nomi: it speaks
Postgres's frontend/backend protocol (version 3) over `std::net` and
`std::tls`, so a program that uses it needs no Go toolchain, no C
compiler and no `libpq`. `postgres.pool(url)` returns a `Conn`, std/sql's
interface, so code written against `Conn` runs on it unchanged.

```sh
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

A program that uses the driver has the effects of `std::net` (it dials
the server), `std::tls` (when the connection is TLS), `std::channels`
(the pool's and each connection's state), `std::random` (the SCRAM nonce)
and `std::instant` (the pool's ages and the reads' deadlines);
`postgres.scratch()` adds `std::process`. `nomi effects --write` writes
them into a project's `[effects]` tables.

## What it does

- `postgres.pool(url)` is a pool of connections. It connects lazily: an
  unreachable server, or a URL that does not parse, is `Error.Connection`
  from the first statement, so a program that must fail early calls
  `Conn.ping`. A statement runs on any connection of the pool, and waits
  for one when all `pool_max_conns` (4 unless the URL says) are busy. A
  connection is dropped once it outlives `pool_max_conn_lifetime` (1 hour)
  or sits idle past `pool_max_conn_idle_time` (30 minutes), and one idle
  for over a second answers an empty query before a statement uses it.
- `postgres.connect(url)` is one connection, opened now. A temporary table
  lives on one connection, so code that uses one connects.
- The URL is `postgres://user:password@host:port/name?key=value` or libpq's
  `key=value` words. The keys are `host`, `port`, `user`, `password`,
  `dbname`, `sslmode` (`disable`, `allow`, `prefer`, the default,
  `require`, `verify-ca`, `verify-full`), `sslrootcert`, `sslcert`,
  `sslkey`, `connect_timeout` (seconds) and the pool's three; any other key
  is a setting each connection starts with (`application_name`,
  `search_path`, `client_encoding`).
- A password is sent as the server asks: SCRAM-SHA-256, MD5, or in clear.
- `Conn.begin_transaction` is `BEGIN` on a connection the transaction
  holds until it commits or rolls back; on a transaction it is a
  savepoint, which `commit` releases and `rollback` rolls back to. A
  statement must run on the innermost open transaction or savepoint, and
  the driver refuses one sent through an outer transaction.
- One `Conn` may be shared by every task of a program. On a pool, a
  statement from another task while a transaction is open runs on another
  connection. On a `connect` connection, it waits for the transaction to
  end and then runs outside it; inside the transaction, use the `Conn` its
  `begin` returned, since a statement through the connection would wait
  for its own transaction.
- Parameters are bound as `$1`, `$2`, ..., never spliced into the SQL. A
  statement is prepared the first time a connection runs its SQL and kept,
  so it runs in one round trip after that; a statement with no parameters
  given to `exec` is sent as a simple query, which may hold several
  statements, as a migration does.
- A task that is cancelled, or whose `Context.with_timeout` deadline
  passes, while a statement runs stops within a tenth of a second. The
  statement is cancelled on the server, and the connection stays usable:
  the next statement on it reads what the server still sent for the
  stopped one first.
- A constraint error names the constraint as Postgres does, with its table
  and columns. A not-null violation is named as Postgres 18 names it
  (`users_email_not_null`), which older servers do not report. A
  serialization failure or a deadlock is `Error.Serialization`; any other
  error is `Error.Database` with its SQLSTATE.
- `postgres.listen(url, channel)` is a `Listener` on a connection of its
  own: `Listener.wait` answers the next `Notification` sent on the
  channel (`NOTIFY` or `pg_notify`). `Listener.close` ends a `wait`
  running on another task with `Error.Closed`.
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
  directory is removed within a second of the process ending, however it
  ends. Each call makes another database on that server. Without `initdb`
  and `postgres` on `PATH`, or with no POSIX shell, it returns
  `Error.Connection` saying so.
- It passes `std/sql/testing`'s conformance suite (every rule;
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
  database before it answers.
- In the browser (the tour's wasm build) std::net has no sockets, so every
  call answers `Error.Connection`.

## Values

| `Value` | Written as | Read back from |
|---|---|---|
| `Int` | its digits, as the type the server asks for; past 64 bits, a `BIGINT` refuses it and a `NUMERIC` takes it | `smallint`, `integer`, `bigint`, `oid` |
| `Float` | binary `float8` or `float4` | `real`, `double precision`; also `numeric`'s `NaN` and infinities |
| `Decimal` | its digits | `numeric` |
| `Text` | itself | `text`, `varchar`, `char`, `name`, and any type with no `Value` of its own (an interval, an enum, `inet`, `citext`), as its text |
| `Bool` | `true` or `false` | `boolean` |
| `Bytes` | binary `bytea` | `bytea` |
| `Date`, `Time`, `Timestamp`, `Timestamptz` | binary `date`, `time`, `timestamp`, `timestamptz` | the same; `infinity` and `-infinity` read as `Text` |
| `Uuid` | its text | `uuid` |
| `Json` | its text, to `json` or `jsonb` | `json`, `jsonb` |
| `Array` | an array literal of its elements, which must all be one kind | a one-dimensional array of any type above |

A `List<T>` is an `Array` parameter, so `WHERE id = ANY(${ids})` matches a
list of ids, and an array column reads as a `List<T>`
(`Row.get<List<Int>>`). A time keeps microseconds, as Postgres stores it.

## How it is built

| File | What it holds |
|---|---|
| `postgres.nomi` | The public API: `pool`, `connect`, `scratch`, `listen`, `Listener`, and `Conn` for `PgConn`. |
| `config.nomi` | Connection strings, read into a `Config`. |
| `messages.nomi` | Each protocol message as bytes, and `decode` of the server's. |
| `auth.nomi` | SCRAM-SHA-256 and MD5, as functions from the server's message to the client's. |
| `types.nomi` | Each type's binary and text form, by OID. |
| `failures.nomi` | The `sql.Error` of each SQLSTATE. |
| `cell.nomi` | State several tasks share one at a time, which a stopped task never leaves taken. |
| `wire.nomi` | One connection: opening it, sending, reading, cancelling. |
| `session.nomi` | Statements on one connection: prepared, bound and executed, or simple. |
| `pooled.nomi` | The pool, its permits and idle connections, and transactions. |
| `scratch.nomi` | The throwaway server. |
| `describe.nomi` | What sqlkit asks of a schema. |

Everything but `wire.nomi`, `session.nomi`, `pooled.nomi`, `scratch.nomi`
and `describe.nomi` is pure, tested against recorded bytes and the RFCs'
examples.

## Not built

- `copy_from` (`COPY ... FROM STDIN`). It needs the driver's own
  connection behind a `Conn`, and a transaction's `Conn` reaches a program
  as the std interface, which offers no way back to it. A `COPY ... FROM
  STDIN` sent as a statement is refused with `Error.Database` coded
  `0A000`.
- Arrays of more than one dimension, and composite values, which are
  refused with a message.
- SCRAM-SHA-256-PLUS: std::tls gives no channel-binding data.
  `sslmode=verify-ca` checks the server's host name too, as `verify-full`
  does.
- Unix-domain sockets, a URL naming several hosts, the `PG*` environment
  variables and `.pgpass`.
- `postgres.scratch()` on Windows.

## Tests

```sh
nomi test lib/sql/postgres
```

The tests run on `postgres.scratch()`'s throwaway server, so they need
`initdb` and `postgres` on `PATH`. Without them the tests fail, since a
Nomi test cannot skip itself; `TestLibModules` (`cmd/nomi`) skips this
module instead. `wire_test.nomi` answers a client's password with a server
written in the test, for each method, and `messages_test.nomi` and
`types_test.nomi` read recorded bytes.
