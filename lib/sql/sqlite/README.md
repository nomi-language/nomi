# sqlite

The SQLite driver for Nomi's `std/sql`. SQLite itself, compiled to
WebAssembly, ships inside the package, and std::wasm runs it; the file
layer SQLite reads and writes through is Nomi over std::fs ([How it
runs](#how-it-runs)). A program that uses it needs no C compiler.

```nomi
import
    std::io
    std::sql.{self, Conn, Error, Row, Sql}
    sqlite
end

fn main() -> Result<Unit, Error>
    db = sqlite.open("notes.db")?
    defer sql.close_quietly(db)

    _ = Conn.exec(
        db,
        Sql"CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)",
    )?

    _ = Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"hello"})")?
    rows = Conn.query(db, Sql"SELECT body FROM notes")?

    Iter.each(rows, fn row ->
        io.print(
            Row.get<String>(row, "body")
            |> Result.with_default("?")
        )
    end)

    Conn.close(db)?
    Ok(Unit)
end
```

## What it does

- `sqlite.open(path)` opens (or creates) a database file; `sqlite.memory()`
  is a fresh in-memory database that lives as long as its connection, what a
  test opens for each case. `sqlite.open_in(dir, name)` opens the database
  `name` in a `Dir` the program opened (`fs.open_dir`), and reaches no file
  outside it: its journal is beside it, and its temporary files are in the
  `Dir` passed as `temp:`, or in `dir`. The `Dir`s stay the caller's to
  close. A test passes it a `Dir` from `std::fs::testing.dir`, which keeps
  the database in memory. A connection is one SQLite connection, with
  foreign keys enforced. On a file database it sets `busy_timeout` to 5
  seconds: a statement that finds the file locked by another connection
  waits up to that long for it, then fails with `SQLITE_BUSY`.
- Parameters are bound as `?1`, `?2`, ..., never spliced into the SQL.
- `Conn.begin_transaction` on the connection is `BEGIN`; on a transaction it is a
  savepoint, which `commit` releases and `rollback` rolls back to. Inside a
  transaction, a statement must run on the innermost open transaction or
  savepoint: SQLite has one transaction per connection, so one sent through
  an outer transaction would belong to the savepoint, and the driver
  refuses it.
- One `Conn` is meant to be shared by every task of a program, as an app
  keeps it in an app field for every request. Tasks take turns on the
  connection: a statement runs alone, and a transaction has the connection
  to itself from `begin` until `commit` or `rollback`. A statement or
  `begin` from another task meanwhile waits for the transaction to end,
  then runs outside it; it never fails for it, and never joins it. The wait
  lasts as long as the waiting task's context allows: past a
  `Context.with_timeout` deadline, or once the task is cancelled, it is
  `Err(.Timeout)`. So keep transactions short, and inside one use only the
  `Conn` its `begin` returned: a statement the transaction's own task sends
  through the shared `Conn` waits for that same transaction, until the
  task's deadline.
- It passes `std/sql/testing`'s conformance suite (every rule;
  `sqlite_test.nomi`).

## Values

A value is written as SQLite stores it, and read back by the column's
declared type:

| `Value` | Written as | Read back from a column declared |
|---|---|---|
| `Int`, `Float`, `Text`, `Bytes`, `Null` | the storage class of the same name | any type |
| `Bool` | `1` or `0` | `BOOLEAN`, `BOOL` |
| `Decimal` | its text, `12.50` (see below) | `DECIMAL`, `NUMERIC`, at the declared scale |
| `Date`, `Time` | ISO 8601 text | `DATE`, `TIME` |
| `Timestamp` | `2026-10-19 12:30:05` | `DATETIME`, `TIMESTAMP` |
| `Timestamptz` | `2026-10-19 12:30:05` in UTC, with a fraction only when it has one (`12:30:05.5`) | `TIMESTAMPTZ` (that form, or RFC 3339 text with an offset) |
| `Uuid`, `Json` | their text | `UUID`, `JSON` |
| `Array` | refused (`SQLITE_MISMATCH`): SQLite has no array type | never |

A column with no declared type (an expression, `count(*)`) keeps its
storage class.

SQLite has no timestamp type: it compares and sorts timestamps as text. So
an `Instant` is written in the form SQLite's own `CURRENT_TIMESTAMP` writes,
UTC with no offset, and `placed_at >= @since` and `ORDER BY placed_at`
compare a bound `Instant` with a `DEFAULT CURRENT_TIMESTAMP` value in time
order. A fraction of a second is written only when there is one, shortest,
which keeps text order and time order the same.

### Decimals

SQLite has no decimal type. A column declared `DECIMAL(12, 2)` has NUMERIC
affinity, and SQLite converts the text a `Decimal` is written as to a
number: `'12.50'` is stored as the REAL `12.5`, `'12.00'` as the INTEGER
`12`. A REAL is a 64-bit float, so text of more than 15 significant digits
is rounded as it is stored, with no error: `'123456789012345678.91'` is
stored as `123456789012345680`, and `'90071992547409.93'` (2^53 + 1 cents)
as `90071992547409.94`. Arithmetic in SQL (`sum(total)`, `total * 2`) is
floating-point arithmetic on those REALs.

A 64-bit float tells apart every decimal of at most 15 digits: the
shortest decimal form of the float a 15-digit decimal is stored as is that
decimal again. So the driver:

- reads a number from a `DECIMAL(p, s)` or `NUMERIC(p, s)` column as a
  `Decimal` at scale `s`: `12.5` reads as `12.50d`, `12` as `12.00d`. A
  column with no scale (`DECIMAL`) reads at the number's shortest scale.
- refuses to write a `Decimal` of more than 15 digits (`12.50` has 4), which
  SQLite would round.
- refuses to read a stored value with more decimals than the column's scale
  (`12.555` in a `DECIMAL(12, 2)`; SQLite does not round to the scale), or
  more than 15 digits at that scale, which a float may already have rounded.

Each refusal is `sql.Error.Database` with the code `SQLITE_MISMATCH` and a
message naming the parameter or column and the fix. An `Int` parameter past
the signed 64-bit range of an SQLite `INTEGER` is refused the same way: a
Nomi `Int` is exact at any size, and SQLite would keep such a value as a
float or as text, which reads back as another value. So a `DECIMAL(p, s)`
column with `p` at most 15 keeps every value exactly, at its scale;
`nomi tool sqlkit generate` warns about one declared wider, or with no
scale. Keep an amount that needs more digits, or that SQL adds up over many
rows and must be exact whatever the rows hold, as INTEGER minor units
(cents): SQLite's integer arithmetic is exact, and a sum that overflows is
an error. A `TEXT` column keeps any decimal's text, but SQLite compares and
sorts it as text and adds it up as floats.

An expression has no declared type, so its number reads as an `Int` or
`Float`; `sqlkit` reads one `CAST(... AS DECIMAL(p, s))` at that scale.

## Errors

A constraint SQLite reports is `sql.Error.Constraint` with its kind, and
its `name` is the one Postgres reports, so code that matches on it works on
both. SQLite itself names no constraint: it reports the columns of a
`UNIQUE`, `PRIMARY KEY` or `NOT NULL` constraint, a `CHECK`'s name or
expression, and nothing at all of a foreign key. The driver works the name
out from the schema:

| Violated | `name` | `table`, `column` |
|---|---|---|
| a constraint the schema names (`CONSTRAINT users_email_once UNIQUE`), or `CREATE UNIQUE INDEX users_email_key ON users (email)` | that name | the table and its columns |
| `UNIQUE` | `<table>_<columns>_key`: `users_email_key`, `pairs_a_b_key` | the table and its columns (`a,b`) |
| `PRIMARY KEY` | `<table>_pkey` | the table and its columns |
| `REFERENCES` | `<table>_<columns>_fkey`: `orders_user_id_fkey` | the table holding the foreign key and its columns |
| `NOT NULL` | `<table>_<column>_not_null`: `users_email_not_null` | the table and column |
| `CHECK` | `<table>_<column>_check` for one that reads one column, `<table>_check` for one that reads several | the table, and the one column it reads |

Postgres also cuts a default name to 63 bytes, which the driver does not.
For a foreign key it reruns the failed statement in a savepoint with the
foreign keys deferred, asks SQLite's `foreign_key_check` which key the
statement breaks, and rolls the savepoint back. Any other error is
`sql.Error.Database` with SQLite's result code (`SQLITE_ERROR`) and message.

A trap in the engine is `sql.Error.Engine` with the trap's text
(`an access was outside the module's memory or table`). It is a fault in
SQLite's C code that the WebAssembly sandbox held, so nothing outside the
engine was touched, but the engine has a bug, and its instance is dead.
Every later operation of the connection that reaches the engine, `ping`
and `close` included, is `sql.Error.Engine` too, and after the `close` the
connection is `sql.Error.Closed`. An engine that cannot start is
`sql.Error.Engine` as well.

## How it runs

`engine/sqlite3.wasm` is SQLite 3.54.1 compiled with `engine/shim.c` by
`engine/build.sh`, embedded in the package. Each connection is an instance
of it. A statement crosses into the instance once: its SQL and parameters
in, every row out (a statement `each_row` reads crosses once for each 64
rows). The module imports SQLite's file layer from the driver
(`internal/vfs.nomi`): files opened through a `Dir` as `std::fs`'s `File`, SQLite's own
byte-range locks over `fs.lock`, and the clock, the machine's time zone,
entropy and sleep. A
module compiles once a process, and its machine code is kept in the
artifact cache, so a later process opens SQLite in about 25 ms; the first
open on a machine compiles it, about half a second.

- **Locks.** A database's lock is SQLite's: the bytes it locks are the
  ones every SQLite locks, so another program's SQLite on the same file,
  the system's `sqlite3` among them, waits for this driver as it waits
  for itself.
- **Journal.** A connection keeps a rollback journal. WAL needs shared
  memory, which the file layer does not have: `PRAGMA journal_mode = WAL`
  answers `delete` and leaves the journal as it was, except on a
  connection that set `PRAGMA locking_mode = EXCLUSIVE`, which keeps the
  WAL index in its own memory and holds the file for itself.
- **Sync.** A sync is the one SQLite's Unix layer makes: `File.sync_data`,
  which on macOS is `fsync` and hands the data to the drive, and
  `File.sync`, which there waits for the drive's own cache, only under
  `PRAGMA fullfsync` or `checkpoint_fullfsync`. A new journal's directory
  is synced with `sync_dir`, an `fsync` of the directory as SQLite's own,
  so an autocommit on macOS takes about 0.9 ms.
- **Stopping.** A statement stops when its task does: SQLite asks the
  driver every 10,000 steps whether the task is stopping (its progress
  handler) and ends the statement when it is, and the connection goes on.
- **Local time.** `localtime` and `utc` in a date function read the zone
  the machine is set to, `DateTime.local_zone` in std::calendar, at the
  offset each moment has there, daylight saving included. Where the
  machine's zone has no name std reads (a copied `/etc/localtime`, Windows
  without `TZ`), they fail with SQLite's `local time unavailable`.
- **Page cache.** 16 MiB a connection, where SQLite's default is 2 MiB: a
  page the cache misses is a call out of the engine.
- **Effects.** A program that opens its databases with `open_in` reaches
  `std::calendar` (the machine's zone), `std::channels`, `std::instant`,
  `std::random`, `std::timer` and `std::wasm` in the driver's code, and no
  `std::fs`: the files are reached through the `Dir`, which the program's
  own `fs.open_dir` opened. Its `[dependencies]` entry can say so:

  ```toml
  [dependencies]
  sqlite = { repo = "github.com/nomi-language/nomi", subdir = "lib/sql/sqlite", version = "v0.0.1", effects = ["std::calendar", "std::channels", "std::instant", "std::random", "std::timer", "std::wasm"] }
  ```

  `sqlite.open` and `sqlite.memory` open their directories themselves, so
  a program that calls either has `std::fs` in the driver too. The
  package's own `nomi.toml` lists all seven.
- **Targets.** On amd64 and arm64 the engine runs as machine code;
  other architectures interpret it, 20 to 50 times slower. A `js/wasm`
  build runs it on the browser's or Node's own engine. `wasip1/wasm` has no std::wasm, so `nomi build --target
  wasip1/wasm` refuses a program that uses the driver.

To rebuild the engine, with zig 0.15.2 on `PATH`:

```console
$ lib/sql/sqlite/engine/build.sh                  # fetches the pinned amalgamation
$ lib/sql/sqlite/engine/build.sh <unpacked dir>   # or uses one at hand
```

It checks the amalgamation's SHA-256, prints the module's, and writes it
to `engine/sqlite3.wasm.sha256`, which a test holds the committed module
to; the same amalgamation and zig build the same bytes. The compile
options are listed at the top of the script.

## Describing statements

`sqlite/describe` is for tools: `describe.describe(migrations, statements)`
applies the migrations to a fresh in-memory database and, for each
statement, answers its result columns with the table column each one reads
and its `NOT NULL`, its parameter count and query plan, or SQLite's error
and the byte it names. It reads SQLite's column metadata
(`sqlite3_column_table_name`, `sqlite3_column_origin_name`,
`sqlite3_table_column_metadata`, `sqlite3_error_offset`), which the engine
compiles in. `nomi tool sqlkit generate` is built on it, so it runs in the
browser as well.
