# sqlite

The SQLite driver for Nomi's `std/sql`, over
[`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), a pure-Go
build of SQLite: a program that uses it needs no C compiler, and
`nomi build --target` cross-compiles it to any platform Go's SQLite port
supports. In the browser it runs on SQLite's official WebAssembly build
instead ([In the browser](#in-the-browser)).

```nomi
import {
    std/io
    std/sql.{self, Conn, Error, Row, Sql}
    sqlite
}

fn main() -> Result<Unit, Error> {
    sql.with_connection(fn -> sqlite.open("notes.db"), fn db -> {
        _ = try Conn.exec(
            db,
            Sql"CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)",
        )

        _ = try Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"hello"})")
        rows = try Conn.query(db, Sql"SELECT body FROM notes")

        Iter.each(rows, fn row -> {
            io.print(
                Row.get<String>(row, "body")
                |> Result.with_default("?")
            )
        })

        Ok(Unit)
    })
}
```

## What it does

- `sqlite.open(path)` opens (or creates) a database file; `sqlite.memory()`
  is a fresh in-memory database that lives as long as its connection, what a
  test opens for each case. A connection is one SQLite connection, with
  foreign keys enforced. On a file database it sets `busy_timeout` to 5
  seconds: a statement that finds the file locked by another connection
  waits up to that long for it, then fails with `SQLITE_BUSY`.
- Parameters are bound as `?1`, `?2`, ..., never spliced into the SQL.
- `Conn.begin` on the connection is `BEGIN`; on a transaction it is a
  savepoint, which `commit` releases and `rollback` rolls back to. A
  statement must run on the innermost open transaction or savepoint: SQLite
  has one transaction per connection, so one sent through an outer `Conn`
  would belong to the savepoint, and the driver refuses it.
- It passes `std/sql_testing`'s conformance suite (every rule;
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
message naming the parameter or column and the fix. So a `DECIMAL(p, s)`
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

## In the browser

`modernc.org/sqlite` has no WebAssembly port, so a `js/wasm` build of the
driver (`nomi build --web`, and the examples of the Nomi tour's Databases
chapter) uses a second database/sql driver, `goside/sqlite_js.go`, over
[SQLite's official WebAssembly build](https://sqlite.org/wasm)
(`sqlite-wasm`, from sqlite.org's download page), called through its C API
with `syscall/js`. It answers the same values and the same errors: the
same `sqlite_test.nomi`, conformance suite and constraint names included,
passes on both, and `goside`'s Go tests run on both (below).

The page loads the build in the worker that runs the program before the
program opens a database, and hands it over as `globalThis.sqlite3`:

```js
importScripts("sqlite3.js"); // or: import sqlite3InitModule from "./sqlite3.mjs"
globalThis.sqlite3 = await sqlite3InitModule();
```

For a program built with `nomi build --web`, that is a setup module beside
`nomi.js`, with `sqlite3.mjs` and `sqlite3.wasm` from the zip below, named
by `run`'s (or `load`'s) `setup` option, which `nomi.js` imports and awaits
where the program runs:

```js
// setup.mjs
import sqlite3InitModule from "./sqlite3.mjs";
export default async () => {
  globalThis.sqlite3 = await sqlite3InitModule();
};
```

```js
await run("./app.wasm", { setup: "./setup.mjs" });
```

A program that opens a database before that gets `Err(.Connection)` saying
the build is not loaded. In the browser a database lives in the build's
memory: `sqlite.memory()` is one per connection as everywhere, and
`sqlite.open(path)` names one that lasts as long as the page.

`goside`'s tests run under Node on the browser build with the runner in
`goside/testdata`, given the `jswasm` folder of
[`sqlite-wasm-3540100.zip`](https://sqlite.org/2026/sqlite-wasm-3540100.zip):

```console
$ cd lib/sql/sqlite/goside
$ SQLITE_WASM_DIR=<dir>/jswasm GOOS=js GOARCH=wasm \
    go test -exec "node $PWD/testdata/wasm_exec_sqlite.cjs" .
```

## Describing statements

`sqlite/describe` is for tools: `describe.describe(migrations, statements)`
applies the migrations to a fresh in-memory database and, for each
statement, answers its result columns with the table column each one reads
and its `NOT NULL`, its parameter count and query plan, or SQLite's error
and the byte it names. It reads SQLite's column metadata
(`sqlite3_column_table_name`, `sqlite3_column_origin_name`,
`sqlite3_table_column_metadata`, `sqlite3_error_offset`), which
`database/sql` does not expose: natively through `modernc.org/sqlite`'s
`lib` package, and in the browser through the WebAssembly build's C API,
which compiles the column metadata in too. `nomi tool sqlkit generate` is
built on it, so it runs in the browser as well.
