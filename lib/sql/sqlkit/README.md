# sqlkit

Checked SQL for Nomi. You write SQL; sqlkit checks it against your
schema when the program is checked, and turns each query into a typed
function. Two macros do it, written against `std/macro` alone, and one
tool:

- `sqlkit.database! main` generates a row struct per table, named in the
  singular (`users` holds `User` rows), an enum per
  `CHECK (column IN (...))` column, and the migrations as data.
- ``sqlkit.query! name(params) -> Row Sql`SQL` `` generates a function that
  runs the SQL on a `Conn` and answers typed rows.
- `nomi tool sqlkit prepare` applies the migrations to a scratch SQLite
  database, asks SQLite to describe every query, and writes the snapshot
  both macros read. Checking reads only the snapshot, so `nomi check`, CI
  and the editor need no database.

The `!` marks a macro: sqlkit writes these declarations when the program is
checked. A file that uses one of them alone can import it by name instead
and write it bare: `import sqlkit.query`, then `pub query! user_by_email(...)`.

See [`../examples/shop`](../examples/shop) for a whole program.

## Setting up

```toml
# nomi.toml
[sqlkit.database.main]
migrations = "db/migrations"        # SQL files, applied in name order
snapshot = "db/main.snapshot.json"  # what prepare writes; commit it

[sqlkit.database.main.tables]
people = "Person"                   # a table's row struct, when the rule misses
```

Every key may be left out; those are the defaults (`db/<name>.snapshot.json`
for the snapshot). A module with one `sqlkit.database!` declaration may
leave out the table too, and its database has the defaults. With several
databases, each has a table that lists the directories whose queries are
its own: `queries = ["analytics"]`. SQLite is the only database so far.

A table's row struct is its name in the singular, in PascalCase: a final
`ies` becomes `y` (`categories`, `Category`), a final `ses` becomes `s`
(`addresses`, `Address`), a final `ss` stays, and any other final `s` goes
(`order_items`, `OrderItem`). `[sqlkit.database.<name>.tables]` names the
row of a table the rule gets wrong; a name there must be a type's name, and
must name a table the snapshot holds. An enum is named after its table's
row and its column: `orders.status` is `OrderStatus`, and with
`people = "Person"`, `people.status` is `PersonStatus`.

```nomi
// schema.nomi
import sqlkit

/// The shop's database.
pub sqlkit.database! main
```

Run `nomi tool sqlkit prepare` after changing a migration or a query, and
`nomi tool sqlkit prepare --check` in CI, which fails when the snapshot is
not what prepare would write. The snapshot records a checksum of the
migrations it was prepared from, so a migration added or changed since is
an error at the `sqlkit.database!` declaration, in `nomi check` and the
editor alike:

```
error: db/main.snapshot.json is out of date: the migrations changed after the last `nomi tool sqlkit prepare`
 --> schema.nomi:7:12
  |
7 | pub sqlkit.database! main
  |            ^^^^^^^^^
  = help: run `nomi tool sqlkit prepare` to describe the migrations again
```

## Queries

```nomi
import {
    schema.User
    sqlkit
}

/// The user with this email, if any.
pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`
        SELECT id, email, name, created_at
        FROM users
        WHERE email = @email
    `
```

generates this function, which `nomi expand queries.nomi` prints:

```nomi
// sqlkit.query! user_by_email (line 8), expanded by the macro query of module sqlkit
import {
    std/sql
    sqlkit
    sqlkit/runtime
}

/// The user with this email, if any.
pub fn user_by_email(conn: sql.Conn, email: String) -> Result<Maybe<User>, sql.Error> {
    statement = runtime.statement(
        ["SELECT id, email, name, created_at FROM users WHERE email = ", ""],
        [runtime.bind(email)],
    )

    runtime.optional(conn, "user_by_email", statement, fn row -> Ok(#User{
            id: try runtime.int("user_by_email", row, 0),
            email: try runtime.text("user_by_email", row, 1),
            name: try runtime.text_null("user_by_email", row, 2),
            created_at: try runtime.timestamptz("user_by_email", row, 3),
        }))
}
```

The SQL is kept apart from `email`: the statement holds the SQL around each
parameter and the values to bind. Each column is read into its field by a
reader for the column's type (`text_null` for a nullable `TEXT` column).

The body is tagged `Sql`, so editors highlight it as SQL, and sqlkit
requires the tag. A query body without it, or with another tag, is an error:

```
error: a query's body is SQL, tagged `Sql`
 --> queries.nomi:9:9
  |
9 |         SELECT id, email, name, created_at
  |         ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  = help: write `Sql` right before the opening backtick: ``Sql`SELECT ...` ``
```

`@name` is SQL's own named parameter. It marks where the parameter is
bound, and the value is never spliced into the SQL. An `@` followed by a
letter or `_` starts a parameter name (letters, digits and `_`); any other
`@` (Postgres's `@>`, `<@`, `@@`), and every `@` inside a string literal, a
quoted name or a comment, is SQL text. Every `@name` must name a parameter,
and every parameter must be used. The body is raw text, so a `${name}` in it
would not be interpolated, and would reach the database as text. It is an
error that says to write `@name`:

```
error: a query body is raw SQL, so `${...}` is not interpolated
  --> queries.nomi:11:23
   |
11 |         WHERE email = ${email}
   |                       ^^^^^^^^
   = help: write `@email`: a query binds a parameter with SQL's `@name`
```

The SQL that is sent binds by position: each `@name` is written as `?1`,
`?2`, ... in order, which is the SQL `prepare` describes and the snapshot
keeps, and the form std/sql's `Statement` prints. A parameter used twice is
bound twice.

| Result | The function answers | Zero rows | Several rows |
|---|---|---|---|
| `-> T` | `Result<T, sql.Error>` | `Err(.RowCount)` | `Err(.RowCount)` |
| `-> Maybe<T>` | `Result<Maybe<T>, sql.Error>` | `Ok(None)` | `Err(.RowCount)` |
| `-> List<T>` | `Result<List<T>, sql.Error>` | `Ok([])` | all |
| `-> Rows<T>` | `Rows<T>`, read with `Rows.fold` | | streamed |
| none | `Result<List<#{...}>, sql.Error>`, anonymous rows of the columns | `Ok([])` | all |
| `-> Changed`, the SQL returns no rows | `Result<sqlkit.Changed, sql.Error>`, the number of rows changed | | |
| `-> Unit`, the SQL returns no rows | `Result<Unit, sql.Error>`, the count ignored | | |

`T` is a struct whose fields are the columns, by name, or for a query of
one column one of std's value types (`-> Int` for `INSERT ... RETURNING
id`).

SQL that returns no rows (an `UPDATE` or `DELETE` without `RETURNING`)
writes `-> Changed` for the number of rows it changed, or `-> Unit` to
ignore it. `Changed` is sqlkit's struct `#Changed{rows: Int}`, not an `Int`,
so taking `RETURNING id` off an `INSERT ... -> Int` is an error rather than
a function that answers 1 where it answered an id:

```
error: `insert_user` returns no rows, so it cannot answer an `Int`; write `-> Changed` for the number of rows it changed, or `-> Unit`
  --> queries.nomi:15:70
   |
15 | pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
   |                                                                      ^^^
```

The snapshot says whether a statement returns rows, so leaving the result
type off one that returns none is an error at the query's name:

```
error: `set_status` returns no rows, so its result is the number of rows it changed: write `-> Changed`
  --> queries.nomi:27:19
   |
27 | pub sqlkit.query! set_status(id: Int, status: OrderStatus) Sql`
   |                   ^^^^^^^^^^
   = help: write `-> Changed` for the count, or `-> Unit` to ignore it
```

Any other row type on SQL that returns no rows is an error at the row type,
and `-> Unit` or `-> Changed` on SQL that returns rows is an error at the
result type.

A column is a `Maybe` when it may be NULL: a table column without `NOT
NULL`, a column of a table an outer join may leave out (SQLite's query plan
says which), and every expression. The column's alias says otherwise, as
sqlx's do: `count(*) AS "n!"` asserts it is never NULL, `AS "email?"` that it
may be, and `AS "n: Int"` gives an expression's type, which SQLite does not
report (an anonymous row needs one). The marker is taken out of the SQL that
is sent.

### What is checked

The generated code only checks when the query is right, and the error lands
on the SQL it is about:

```
error: Int does not implement SqlText (required by `where T: SqlText`)
  --> queries.nomi:14:12
   |
14 |     SELECT email, name FROM users
   |            ^^^^^
   = help: `email` is TEXT, so the field should be `String`
   = help: write an `impl SqlText for Int` block
```

The first help names the type the column's SQL type reads as (the table
below); the checker's own help after it is for a type of your own, such as
`type Email String`, read from the column.

- SQLite's own error for a query it cannot prepare (`no such column:
  emial`), at the token it names.
- A query the snapshot does not hold, or whose SQL changed since the last
  prepare, and migrations that changed since it, which say to run it.
- A column read into a field of the wrong type, or a NULL-able one into a
  field that is not a `Maybe`, at the column.
- A missing or extra field, at the row type.
- A result type that does not fit whether the SQL returns rows: none, or a
  row type, on SQL that returns no rows; `-> Unit` or `-> Changed` on SQL
  that returns rows.

A Nomi type is read from, or bound to, a column of a SQL type family when it
implements the family's interface and std/sql's `FromSql` and `ToSql`:

| Declared SQLite type | Family | Nomi type |
|---|---|---|
| `INTEGER`, any name holding `INT` | `SqlInt` | `Int` |
| `REAL`, `FLOAT`, `DOUBLE` | `SqlFloat` | `Float` |
| `DECIMAL`, `NUMERIC` | `SqlNumeric` | `Decimal`, at the declared scale |
| `TEXT`, names holding `CHAR`, `CLOB`, `TEXT` | `SqlText` | `String` |
| `BOOLEAN` | `SqlBool` | `Bool` |
| `BLOB` | `SqlBytes` | `Bytes` |
| `DATE`, `TIME` | `SqlDate`, `SqlTime` | `Date`, `Time` |
| `DATETIME`, `TIMESTAMP` | `SqlTimestamp` | `NaiveDateTime` |
| `TIMESTAMPTZ` | `SqlTimestamptz` | `Instant` |
| `UUID`, `JSON` | `SqlUuid`, `SqlJson` | `Uuid`, `Json` |

To read a `TEXT` column as a type of your own, implement `SqlText`,
`FromSql` and `ToSql` for it. An expression's value has whatever storage
class SQLite gave it, so a number reads as a wider number type
(`sum(total)` over `DECIMAL` reads as a `Decimal`, at its shortest scale).

### Decimals and their scale

SQLite stores a `DECIMAL(p, s)` column's values as 64-bit floats, exact to
15 digits (see the [driver's notes](../sqlite/README.md#decimals)). A
`DECIMAL(p, s)` column reads as a `Decimal` at scale `s`, so `12.50` reads
back as `12.50d`, and prepare warns about a `DECIMAL` column declared with
more than 15 digits or with no scale:

```
warning: db/migrations/20261201090000_discount.sql:1: `orders.discount` is DECIMAL with no scale, so a value reads back at its shortest scale (12.5, not 12.50): declare DECIMAL(p, s)
```

An expression has no declared type: SQLite does not say that
`sum(o.total)` is a `DECIMAL(12, 2)`. Declare it with a `CAST`, which
prepare records as the column's type:

```sql
SELECT u.email, CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
FROM users u JOIN orders o ON o.user_id = u.id
GROUP BY u.email
```

`revenue` then reads at scale 2 (`12.50d`). SQLite computes the sum with
floats, so the reader takes a float only when it is exactly a decimal of
that scale and at most 15 digits, and is an error otherwise rather than a
rounded value: a sum whose float error shows (`0.30000000000000004`), or
`avg(o.total)` with more decimals than the scale (round it in SQL with
`round(avg(o.total), 2)`). SQLite adds a sum's floats with compensated
summation, so most sums come back exact, but not all: one whose rounding
error shows is refused, never answered rounded. For sums that must succeed
whatever the rows hold, keep the amounts as INTEGER minor units.

## Rows, transactions and tests

`Rows.fold(rows, initial, f)` runs a `-> Rows<T>` query and folds its rows
one at a time; a row that does not decode stops it with the error.

Every generated function takes the `Conn` first, so one function runs on a
connection, a transaction (`sql.transaction`) or a test's.

`sqlkit.migrate(conn, schema.migrations)` brings a database up to date: it
runs each generated migration the database has not run, in order, each in
its own transaction with its record. The records are the table
`nomi_migrations` (`version`, the migration's file name; `checksum`, of its
text; `applied_at`), which the first run creates. So it builds a test's
fresh database and migrates one that lives on, run after run. A migration
whose text changed after it ran is refused before anything runs, with
`sql.Error.Database` coded `sqlkit` naming the file: write the change as a
new migration.

## The snapshot

The snapshot is JSON, written in a stable order so a change reviews as a
diff: a checksum of the migrations, each table's columns with their
declared types, nullability and the migration line that made them, and each
query by file and name with the SQL it sends, its columns (with the table
column each one reads) and SQLite's error if it had one. See
`snapshot.nomi` for the format. The shop's `user_by_email` is this entry:

```json
{
  "file": "queries.nomi",
  "name": "user_by_email",
  "sql": "SELECT id, email, name, created_at FROM users WHERE email = ?1",
  "params": 1,
  "columns": [
    {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
    {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
    {"name": "name", "type_name": "TEXT", "table": "users", "column": "name", "nullable": true},
    {"name": "created_at", "type_name": "TIMESTAMPTZ", "table": "users", "column": "created_at", "nullable": false}
  ],
  "error": null
}
```

`?1` is where the first parameter goes, numbered in the order the `@name`s
appear.

## Limits

- SQLite only. Postgres needs its driver and prepare's `EXPLAIN`-based
  nullability.
- Prepare finds a query by the macro at the start of a line, after an
  optional `pub` (`sqlkit.query!`, `query!` after `import sqlkit.query`,
  or the name an `import sqlkit.{query as q}` gives it), and its body
  between the first two backticks after it.
- A query is one statement.

## In the editor

The language server shows the errors at the SQL, as `nomi check` does.
Inside a query's body, hover on a table or column shows its type and the
migration line that made it (hover elsewhere shows the query's columns as
the snapshot describes them), go to definition opens that line, and
completion offers the tables and their columns. A query the snapshot does
not describe, a database with no snapshot, and one whose migrations
changed since it have the quick fix "Describe
the queries with `nomi tool sqlkit prepare`", which runs the tool when you
choose it; the server never runs it on its own.

A misspelled column in `user_by_email` first shows that the query is not
described:

```
error: `user_by_email` is not in db/main.snapshot.json: its SQL changed after the last `nomi tool sqlkit prepare`
 --> queries.nomi:9:9
  |
9 |         SELECT id, emial, name, created_at
  |         ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ (through line 11)
  = help: run `nomi tool sqlkit prepare` to describe the queries again
```

After prepare (`2 tables, 9 queries (1 SQLite rejects)`), SQLite's own error
points at the word:

```
error: no such column: emial
 --> queries.nomi:9:20
  |
9 |         SELECT id, emial, name, created_at
  |                    ^^^^^
  = help: reported by SQLite when `nomi tool sqlkit prepare` described `user_by_email`
```

Reading the nullable `name` into a `String` field is an error at the column
whose help says the field must be a `Maybe`, or the query must assert the
column is never NULL with `AS "name!"`.

While a query is not described (its SQL changed, or a marker names no
parameter), it still declares its function with the signature it writes,
or, with no result type, the columns the snapshot last described, so its
callers still check. Before the first `prepare` there is no snapshot, the
`sqlkit.database!` declaration declares nothing, and `nomi check` reports only the
"no snapshot" error of each declaration.
