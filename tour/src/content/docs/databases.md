---
title: "Databases"
description: "Run SQL on SQLite from Nomi, then let sqlkit check your SQL against the schema and write typed functions for it."
---

A Nomi program talks to a SQL database in one of two ways, and this chapter
takes them in order:

- **Running SQL directly.** You write a statement, send it on a connection,
  and read the rows that come back. Nothing checks the SQL, or the names of
  the columns you read, until the program runs.
- **Checked SQL with sqlkit.** You write each query once, with a Nomi name
  and signature, and sqlkit checks it against your schema while the program
  is checked. Each query becomes an ordinary typed function.

Both use `sqlite`, the driver from
[`lib/sql`](https://github.com/nomi-language/nomi/tree/main/lib/sql) in the nomi repository. It
talks to SQLite, a database kept in one file or in memory, with no server to
run, and builds on [`std/sql`](/reference/sql/), the types every database
library shares. The second way adds `sqlkit`, from the same repository. Make
a project and add the driver, as the [Modules](/modules-and-imports/)
chapter describes:

```console
$ nomi new notes
$ cd notes
$ nomi add github.com/nomi-language/nomi/lib/sql/sqlite
```

The examples are live editors: they run in this page on SQLite's own
WebAssembly build, through the same driver a program on your machine uses,
so edit them and they run again. The driver calls Go code, as the
[FFI](/ffi-and-dynamic/) chapter describes; in the browser its Go side
calls SQLite's WebAssembly build, and on your machine a build of SQLite
written in Go. sqlkit's examples are a whole project each, with its
migrations and the snapshot sqlkit's `prepare` tool writes from them, and
they run `prepare` in the page as you would on your machine.

## Running SQL directly

### A first query

This program makes a table in a new in-memory database, adds two rows and
reads them back:

```nomi-run
import {
    std/io
    std/sql.{self, Conn, Error, Row, Sql}
    sqlite
}

fn main() -> Result<Unit, Error> {
    sql.with_connection(sqlite.memory, fn db -> {
        _ = try Conn.exec(db, Sql"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
        _ = try Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"buy milk"})")
        _ = try Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"call Ada"})")

        rows = try Conn.query(db, Sql"SELECT id, body FROM notes ORDER BY id")
        lines = try (rows |> Iter.map(note_line) |> Result.collect())
        Iter.each(lines, io.print)
        Ok(Unit)
    })
}

// One note as a line of text.
fn note_line(row: Row) -> Result<String, Error> {
    id = try Row.get<Int>(row, "id")
    body = try Row.get<String>(row, "body")
    Ok("${id}: ${body}")
}
```
<!-- expect
1: buy milk
2: call Ada
-->

- `sqlite.memory` opens a new, empty database in memory, and
  `sqlite.open("notes.db")` opens a file. `sql.with_connection` opens the
  database, runs the function with the connection as `db`, and closes it
  however the function ends.
- A `Conn` is a connection. `Conn.exec` runs a statement that changes data
  and returns how many rows it changed. `Conn.query` returns the rows a
  statement reads.
- `Sql"..."` writes a statement. A `${...}` slot in it is a
  [parameter](#parameters).
- `Row.get` reads one column of a row by name, as the type you ask for, and
  fails when the value is not that type. SQL marks a missing value with
  `NULL`, which reads as `None` when you ask for a `Maybe`.
- `note_line` can fail for each row, so mapping it gives a list of
  `Result`s. `Result.collect` turns them into one `Result`: the list of
  lines, or the first error.

Every database call returns a `Result`, because talking to a database can
fail, and `sql.Error` says why.

### Parameters

The `${...}` slots of `Sql"..."` never become SQL text. The SQL goes to the
database with a numbered placeholder where each value goes, and the values
travel separately. This rules out **SQL injection**, where text a user typed
changes what a query does. If someone signs up with the email
`x' OR '1'='1`, the query looks for a user with that exact email and finds
none:

```nomi-run
import std/sql.Sql

fn main() {
    email = "x' OR '1'='1"
    statement = Sql"SELECT id, name FROM users WHERE email = ${email}"

    dbg statement.parts
    dbg statement.params
    dbg "${statement}"
    Unit
}

```
<!-- expect
dbg line 7: statement.parts = ["SELECT id, name FROM users WHERE email = ", ""]
dbg line 8: statement.params = [Text("x' OR '1'='1")]
dbg line 9: "${statement}" = "SELECT id, name FROM users WHERE email = ?1"
-->

A statement holds the SQL around the slots in `parts`, and the values in
`params`. The email stays a `Text` value, quotes and all. Printing the
statement writes `?1` where the first value goes. The
[Typed Literals](/typed-literals/) chapter explains how `Sql"..."` is built.

### Building a statement from pieces

Some statements depend on what the user asked for, such as a search with
optional filters. Build one from pieces:

- `+` joins two statements, SQL and parameters together.
- `Statement.values(items)` writes a list for SQL's `IN`: `(?1, ?2)`.
- `Statement.where_all(conditions)` writes ` WHERE` with the conditions
  joined by `AND`, or nothing when there are none. `where_any` joins them
  by `OR`.
- `Statement.join(statements, separator)` puts a separator between
  statements, for anything else.

Each piece is still a `Sql"..."` literal, so a value only ever goes in a
`${...}` slot:

```nomi-run
import std/sql.{Sql, Statement}

/// How a list of orders can be sorted.
enum Sort {
    Newest
    Largest
}

/// Orders, filtered by only what the caller asked for.
fn orders_query(statuses: List<String>, min_total: Maybe<Decimal>, sort: Sort) -> Statement {
    by_status = case statuses {
        [] -> []
        _ -> [Sql"status IN " + Statement.values(statuses)]
    }

    by_total = case min_total {
        Some(t) -> [Sql"total >= ${t}"]
        None -> []
    }

    order = case sort {
        .Newest -> Sql" ORDER BY placed_at DESC"
        .Largest -> Sql" ORDER BY total DESC"
    }

    Sql"SELECT id FROM orders" + Statement.where_all(by_status + by_total) + order
}

fn main() {
    dbg "${orders_query([], None, .Newest)}"
    dbg "${orders_query(["paid", "shipped"], Some(20d), .Largest)}"
    dbg orders_query(["paid", "shipped"], Some(20d), .Largest).params
    Unit
}
```
<!-- expect
dbg line 30: "${orders_query([], None, .Newest)}" = "SELECT id FROM orders ORDER BY placed_at DESC"
dbg line 31: "${orders_query(["paid", "shipped"], Some(20d), .Largest)}" = "SELECT id FROM orders WHERE (status IN (?1, ?2)) AND (total >= ?3) ORDER BY total DESC"
dbg line 32: orders_query(["paid", "shipped"], Some(20d), .Largest).params = [Text("paid"), Text("shipped"), Decimal(20d)]
-->

A column name can't be a parameter, so the sort order is an enum, and each
case gives its own fixed `Sql"..."`. Never build SQL text from a name the
user sent.

### Transactions

A **transaction** groups statements so that either all of them take effect
or none does. Moving money between two accounts is two updates, and if the
second fails, the first must not stand. `sql.transaction` runs a function
inside one. This program moves 30 from Ada to Bob twice:

```nomi-run
import {
    std/io
    std/sql.{self, Conn, Error, Row, Sql}
    sqlite
}

/// Move `amount` from one account to another: both changes or neither.
fn transfer(db: Conn, from: String, to: String, amount: Int) -> Result<Unit, Error> {
    credit = Sql"UPDATE accounts SET balance = balance + ${amount} WHERE name = ${to}"
    debit = Sql"UPDATE accounts SET balance = balance - ${amount} WHERE name = ${from}"

    sql.transaction(db, fn tx -> {
        _ = try Conn.exec(tx, credit)
        _ = try Conn.exec(tx, debit)
        Ok(Unit)
    })
}

fn main() -> Result<Unit, Error> {
    sql.with_connection(sqlite.memory, fn db -> {
        _ = try Conn.exec(
            db,
            Sql"""
            CREATE TABLE accounts (
                name TEXT PRIMARY KEY,
                balance INTEGER NOT NULL CHECK (balance >= 0)
            )
            """,
        )

        _ = try Conn.exec(db, Sql"INSERT INTO accounts VALUES ('ada', 50), ('bob', 0)")

        dbg transfer(db, "ada", "bob", 30)
        dbg transfer(db, "ada", "bob", 30)

        rows = try Conn.query(db, Sql"SELECT name, balance FROM accounts ORDER BY name")
        lines = try (rows |> Iter.map(balance_line) |> Result.collect())
        Iter.each(lines, io.print)
        Ok(Unit)
    })
}

// One account and its balance as a line of text.
fn balance_line(row: Row) -> Result<String, Error> {
    name = try Row.get<String>(row, "name")
    balance = try Row.get<Int>(row, "balance")
    Ok("${name}: ${balance}")
}
```
<!-- expect
dbg line 33: transfer(db, "ada", "bob", 30) = Ok(Unit)
dbg line 34: transfer(db, "ada", "bob", 30) = Err(#Constraint{kind: Check, name: "accounts_balance_check", table: Some("accounts"), column: Some("balance")})
ada: 20
bob: 30
-->

`sql.transaction` starts a transaction and runs the function with it as
`tx`. When the function returns `Ok`, the changes take effect. When it
returns `Err`, or traps, every change made through `tx` is undone.

A transaction is a `Conn` too, so anything that takes a connection runs
inside one. `sql.transaction` on a transaction starts a savepoint inside it,
so a failure there undoes only the inner part.

### Errors and constraints

A table can carry rules the database enforces, called **constraints**: a
`CHECK`, a `UNIQUE` column, a foreign key. The `accounts` table above has
one, `CHECK (balance >= 0)`. Ada starts with 50 and Bob with 0, so the first
`transfer(db, "ada", "bob", 30)` returns `Ok(Unit)`. The second debit would
leave Ada at -10, so the database refuses it, and the second `transfer`
returns the `Err` in the output above: a `sql.Error.Constraint` with the
kind of rule, its name, and the table and column. Bob's credit was undone
with it, so the balances are Ada 20 and Bob 30. A rule the schema leaves
unnamed gets the name Postgres would give it, as `accounts_balance_check`
here, so a program can tell one rule's failure from another by its name.

### Testing

Open a database with `sqlite.memory()` in each test, so no test sees another's
rows. To test code without any database,
[`Fake`](/reference/sql_testing/) from `std/sql_testing` is a `Conn` that
runs no SQL: it records each statement, answers it with a function you give
it, and tells you which statements took effect. These two tests check the
same function, `add_note`, once against SQLite and once against a `Fake`:

```nomi-run
import {
    std/sql.{self, Conn, Error, Row, Sql}
    std/sql_testing.Fake
    sqlite
}

/// Add a note.
fn add_note(db: Conn, body: String) -> Result<Unit, Error> {
    _ = try Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${body})")
    Ok(Unit)
}

test "a note reads back from a fresh database" {
    db = try sqlite.memory()
    defer sql.close_quietly(db)
    _ = try Conn.exec(db, Sql"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")

    try add_note(db, "buy milk")

    rows = try Conn.query(db, Sql"SELECT body FROM notes")
    bodies = rows |> Iter.map(fn row -> Row.get<String>(row, "body")) |> Iter.to_list()
    assert bodies == [Ok("buy milk")]
}

test "a note is one insert, with the body as a parameter" {
    fake = Fake.new()

    try add_note(fake, "buy milk")

    assert Fake.log(fake) == ["INSERT INTO notes (body) VALUES (?1)"]
    assert Fake.committed(fake) == [Sql"INSERT INTO notes (body) VALUES (${"buy milk"})"]
}
```
<!-- expect
ok a note reads back from a fresh database
ok a note is one insert, with the body as a parameter
test result: ok. 2 passed, 0 failed
-->

The first test makes its own table in a new database and reads the row
back. The second never touches a database: `Fake.log` is what was sent,
and `Fake.committed` is the statements that took effect.

## Checked SQL with sqlkit

Nothing above checks a statement before it runs. A misspelled column, a
column read as the wrong type, or a `NULL` where you expected a value, is
an `Err` at run time. sqlkit moves those mistakes to `nomi check` and your
editor. Add it beside the driver:

```console
$ nomi add github.com/nomi-language/nomi/lib/sql/sqlkit
```

The rest of the chapter follows a small shop that keeps users and their
orders. It is
[`lib/sql/examples/shop`](https://github.com/nomi-language/nomi/tree/main/lib/sql/examples/shop)
in the nomi repository, and runs from a clone:

```console
$ git clone https://github.com/nomi-language/nomi
$ cd nomi/lib/sql/examples/shop
$ nomi run main.nomi
$ nomi test .
```

```tree
shop/
├── nomi.toml
├── go.mod
├── db/
│   ├── migrations/
│   │   ├── 20261019120000_users_and_orders.sql
│   │   └── 20261102090000_orders_shipped_on.sql
│   └── main.snapshot.json
├── schema.nomi
├── queries.nomi
├── shop.nomi
├── main.nomi
└── shop_test.nomi
```

The migrations describe the tables, `prepare` writes the snapshot from them,
`schema.nomi` declares the database, `queries.nomi` holds the queries, and
`shop.nomi` holds operations built from them. The examples below are these
files, in a file tree beside the editor. Each one runs sqlkit's macros,
which takes a moment, so it runs when you press Run.

### Migrations

The **schema** is the list of tables and their columns. With sqlkit you
write it as **migrations**: SQL files that each change the schema, applied
in the order of their file names. Each name starts with the date and time it
was written. The shop's first, `20261019120000_users_and_orders.sql`, creates
both tables:

```sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

The second, `20261102090000_orders_shipped_on.sql`, adds a column with
`ALTER TABLE orders ADD COLUMN shipped_on DATE;`.

A column declared `NOT NULL` always has a value. A column without it is
**nullable**: a row may hold `NULL` there, as a user's `name` may.
`PRIMARY KEY` makes `id` the number that names each row, and SQLite fills it
in. `UNIQUE` means no two users share an email. `REFERENCES users (id)`
means each order's `user_id` must be some user's `id`. The `CHECK` allows
only four values in `status`.

### The snapshot

The compiler never connects to a database, so `nomi check` and the editor
need no running database or password. Instead, sqlkit's `prepare` tool
writes what SQLite says about the schema and the queries to a file, the
**snapshot**, which is all the compiler reads:

```console
$ nomi tool sqlkit prepare
wrote db/main.snapshot.json: 2 tables, 9 queries (0 SQLite rejects)
```

`prepare` applies the migrations to an empty SQLite database in memory, and
asks SQLite to describe each query: its columns, their types, and whether
each may be `NULL`. Commit the snapshot, and run `prepare` again after you
change a migration or a query. In an automated check,
`nomi tool sqlkit prepare --check` writes nothing, and fails when the
snapshot is not what `prepare` would write.

Most examples below run `prepare` before each run, as you would after an
edit, so an edit to a migration or to a query's SQL rewrites the snapshot's
tab before the program is checked. The first line of their output is what
`prepare` printed. The snapshot's tab is read-only, since `prepare` writes
it.

### Declaring the database

The shop's `schema.nomi` declares the database with one line,
`pub sqlkit.database! main`. `sqlkit.database!` is a [macro](/macros/):
code in the sqlkit library that writes Nomi code while your program is
checked, marked by the `!`. It reads the migrations in `db/migrations/` and
the snapshot `db/main.snapshot.json`. Both are at their default paths, so
the shop's `nomi.toml` says nothing about its database; the
[sqlkit README](https://github.com/nomi-language/nomi/tree/main/lib/sql/sqlkit#readme)
lists the keys for a project that keeps them elsewhere. From them, it
writes:

- a struct for each table, named after one of its rows: the table `users`
  holds `User` rows, and `orders` holds `Order` rows;
- an enum for each column that a `CHECK (column IN (...))` limits;
- `migrations`, the migration files as a list, for `sqlkit.migrate`.

This example shows what `nomi expand schema.nomi` prints:

```nomi-run manual expand read-only:db/migrations/* read-only:db/main.snapshot.json
// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
// sqlkit.database! main (line 7), expanded by the macro database of module sqlkit
import {
    std/calendar
    std/instant
    std/sql
    sqlkit
    sqlkit/runtime
}

/// A row of the table `orders`.
pub struct Order {
    id: Int
    user_id: Int
    status: OrderStatus
    total: Decimal
    placed_at: instant.Instant
    shipped_on: Maybe<calendar.Date>
}

/// A row of the table `users`.
pub struct User {
    id: Int
    email: String
    name: Maybe<String>
    created_at: instant.Instant
}

/// The values `CHECK (status IN (...))` allows in `orders.status`.
pub enum OrderStatus {
    Pending
    Paid
    Shipped
    Cancelled
}

derive Equatable for OrderStatus

impl Display for OrderStatus {
    fn to_string(value: OrderStatus) -> String {
        case value {
            .Pending -> "pending"
            .Paid -> "paid"
            .Shipped -> "shipped"
            .Cancelled -> "cancelled"
        }
    }
}

impl sql.ToSql for OrderStatus {
    fn to_sql(value: OrderStatus) -> sql.Value {
        sql.Value.Text(Display.to_string(value))
    }
}

impl sql.FromSql for OrderStatus {
    fn from_sql(value: sql.Value) -> Result<OrderStatus, String> {
        case runtime.text_of(value) {
            Some("pending") -> Ok(OrderStatus.Pending)
            Some("paid") -> Ok(OrderStatus.Paid)
            Some("shipped") -> Ok(OrderStatus.Shipped)
            Some("cancelled") -> Ok(OrderStatus.Cancelled)
            _ -> Err("OrderStatus")
        }
    }
}

impl sqlkit.SqlText for OrderStatus

/// The migrations of the database `main`, in the order they apply.
pub once migrations: List<sqlkit.Migration> = [
    #sqlkit.Migration{
        path: "db/migrations/20261019120000_users_and_orders.sql",
        sql: "CREATE TABLE users (\n    id          INTEGER PRIMARY KEY,\n    email       TEXT NOT NULL UNIQUE,\n    name        TEXT,\n    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n\nCREATE TABLE orders (\n    id          INTEGER PRIMARY KEY,\n    user_id     INTEGER NOT NULL REFERENCES users (id),\n    status      TEXT NOT NULL DEFAULT 'pending'\n                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),\n    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.\n    total       DECIMAL(12, 2) NOT NULL,\n    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n",
    },
    #sqlkit.Migration{
        path: "db/migrations/20261102090000_orders_shipped_on.sql",
        sql: "ALTER TABLE orders ADD COLUMN shipped_on DATE;\n",
    },
]
-->

Each column's SQL type picks its field's Nomi type. `INTEGER` is `Int`,
`TEXT` is `String`, `DATE` is a calendar `Date`, `TIMESTAMPTZ` (a moment in
time) is an `Instant`, and `DECIMAL` is a `Decimal`. A nullable column is a
`Maybe`. The sqlkit README lists every SQL type it reads. SQLite keeps a
`DECIMAL` as a 64-bit float, so the driver reads `total` back at its
declared scale, `12.50d`, and refuses a value it could not keep exactly.

A migration changed since the last `prepare` is an error at the
declaration, in `nomi check` and in the editor. This example has a third
migration that `prepare` has not seen, and it does not run `prepare`:

```nomi-run manual error read-only:db/main.snapshot.json
// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: db/migrations/20261110090000_users_phone.sql
ALTER TABLE users ADD COLUMN phone TEXT;

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
schema.nomi:7:12: db/main.snapshot.json is out of date: the migrations changed after the last `nomi tool sqlkit prepare`
schema.nomi:7:12: help: run `nomi tool sqlkit prepare` to describe the migrations again
-->

### Queries

A `sqlkit.query!` declaration is SQL with a Nomi name and signature. The
first of the shop's queries in `queries.nomi`:

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

It has a name and parameters, as a function has, and a result type:
`-> Maybe<User>` is at most one row, read as a `User`. The SQL comes between
backticks, after a `Sql` tag, which also tells the editor to highlight it as
SQL. `@email` marks where the parameter goes.

The declaration becomes a function, which `nomi expand queries.nomi` prints.
Its first line is:

```nomi
pub fn user_by_email(conn: sql.Conn, email: String) -> Result<Maybe<User>, sql.Error> {
```

Every query function takes the connection as its first argument, `conn`, and
wraps the result type in a `Result`.

The result type says how many rows to expect:

- `-> T`: exactly one row. No rows, or more than one, is an error.
- `-> Maybe<T>`: at most one row. No rows is `None`.
- `-> List<T>`: every row.
- `-> Rows<T>`: every row, read one at a time with `Rows.fold`, so the rows
  are never all in memory at once. This one is not wrapped in a `Result`;
  `Rows.fold` returns one.
- No result type: a list of anonymous structs, one field per column.

`T` is a struct whose fields are the columns, matched by name. A query that
returns one column can read it as a plain type instead. SQL that returns no
rows, such as an `UPDATE`, declares `-> Changed` for the number of rows it
changed (sqlkit's struct `#Changed{rows: Int}`), or `-> Unit` to ignore the
count. Two more of the shop's queries:

```nomi
/// Add a user, answering its id.
pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
        INSERT INTO users (email, name) VALUES (@email, @name)
        RETURNING id
    `

/// Change an order's status: the number of orders changed.
pub sqlkit.query! set_status(id: Int, status: OrderStatus) -> Changed Sql`
        UPDATE orders SET status = @status WHERE id = @id
    `
```

`RETURNING id` makes the `INSERT` return the new row's id. Passing `None`
for `name` stores `NULL`. `set_status` takes an `OrderStatus`, so only the
four allowed values reach the column.

The count is a type of its own so that it can't be mistaken for a column.
Without the `RETURNING id` line, `insert_user` returns no row, and an `Int`
result would quietly become the number of rows inserted. Instead it is an
error. Here `insert_user` has lost that line, and `prepare` has asked
SQLite what the statement returns:

```nomi-run manual error tool:sqlkit/prepare read-only:db/main.snapshot.json
// FILE: queries.nomi
import {
    std/instant.Instant
    schema.{Order, OrderStatus, User}
    sqlkit
}

/// The user with this email, if any.
pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`
        SELECT id, email, name, created_at
        FROM users
        WHERE email = @email
    `

/// Add a user, answering its id.
pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
        INSERT INTO users (email, name) VALUES (@email, @name)
    `

/// Place an order for a user.
pub sqlkit.query! insert_order(user_id: Int, total: Decimal) -> Int Sql`
        INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
        RETURNING id
    `

/// Change an order's status: the number of orders changed.
pub sqlkit.query! set_status(id: Int, status: OrderStatus) -> Changed Sql`
        UPDATE orders SET status = @status WHERE id = @id
    `

/// One order.
pub sqlkit.query! order(id: Int) -> Order Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on
        FROM orders
        WHERE id = @id
    `

/// A user's orders, newest first, each with the user's email.
pub sqlkit.query! orders_for_user(user_id: Int, limit: Int) Sql`
        SELECT o.id, o.status, o.total, o.placed_at, u.email
        FROM orders o
        JOIN users u ON u.id = o.user_id
        WHERE o.user_id = @user_id
        ORDER BY o.placed_at DESC, o.id DESC
        LIMIT @limit
    `

/// Every user with the number of orders they placed, including those who
/// placed none.
pub sqlkit.query! order_counts() Sql`
        SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id
        ORDER BY u.email
    `

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub sqlkit.query! revenue_since(since: Instant) -> List<CustomerRevenue> Sql`
        SELECT u.id AS user_id, u.email,
               count(*) AS "order_count!",
               CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
        FROM users u
        JOIN orders o ON o.user_id = u.id
        WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
        GROUP BY u.id, u.email
        ORDER BY revenue DESC
    `

/// Every order, streamed.
pub sqlkit.query! all_orders() -> Rows<Order> Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id
    `

/// A customer's revenue.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
wrote db/main.snapshot.json: 2 tables, 9 queries (0 SQLite rejects)
queries.nomi:15:70: `insert_user` returns no rows, so it cannot answer an `Int`; write `-> Changed` for the number of rows it changed, or `-> Unit`
-->

Put `RETURNING id` back, or change the result to `-> Changed`, and run it
again.

### Query parameters

Every `@name` must name a parameter of the declaration, so a misspelled name
is an error at the name. sqlkit finds it in the SQL's text, with no help
from the snapshot, so this example does not run `prepare`. Here
`user_by_email` says `@mail`:

```nomi-run manual error read-only:db/migrations/* read-only:db/main.snapshot.json
// FILE: queries.nomi
import {
    std/instant.Instant
    schema.{Order, OrderStatus, User}
    sqlkit
}

/// The user with this email, if any.
pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`
        SELECT id, email, name, created_at
        FROM users
        WHERE email = @mail
    `

/// Add a user, answering its id.
pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
        INSERT INTO users (email, name) VALUES (@email, @name)
        RETURNING id
    `

/// Place an order for a user.
pub sqlkit.query! insert_order(user_id: Int, total: Decimal) -> Int Sql`
        INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
        RETURNING id
    `

/// Change an order's status: the number of orders changed.
pub sqlkit.query! set_status(id: Int, status: OrderStatus) -> Changed Sql`
        UPDATE orders SET status = @status WHERE id = @id
    `

/// One order.
pub sqlkit.query! order(id: Int) -> Order Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on
        FROM orders
        WHERE id = @id
    `

/// A user's orders, newest first, each with the user's email.
pub sqlkit.query! orders_for_user(user_id: Int, limit: Int) Sql`
        SELECT o.id, o.status, o.total, o.placed_at, u.email
        FROM orders o
        JOIN users u ON u.id = o.user_id
        WHERE o.user_id = @user_id
        ORDER BY o.placed_at DESC, o.id DESC
        LIMIT @limit
    `

/// Every user with the number of orders they placed, including those who
/// placed none.
pub sqlkit.query! order_counts() Sql`
        SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id
        ORDER BY u.email
    `

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub sqlkit.query! revenue_since(since: Instant) -> List<CustomerRevenue> Sql`
        SELECT u.id AS user_id, u.email,
               count(*) AS "order_count!",
               CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
        FROM users u
        JOIN orders o ON o.user_id = u.id
        WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
        GROUP BY u.id, u.email
        ORDER BY revenue DESC
    `

/// Every order, streamed.
pub sqlkit.query! all_orders() -> Rows<Order> Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id
    `

/// A customer's revenue.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
queries.nomi:11:23: `mail` is not a parameter of `user_by_email`
queries.nomi:11:23: help: its parameters are `email`
queries.nomi:8:33: the parameter `email` is not used in the SQL of `user_by_email`
queries.nomi:8:33: help: write `@email` where it is bound, or remove it
-->

Every parameter must be used too, so the same mistake also reports that
`email` is not used. Change `@mail` back to `@email` and both go away. An
`@` in a string such as `'ada@example.com'`, or one not followed by a
letter or `_`, is SQL, not a parameter.

A query's body writes parameters as `@name`, not `${...}`, because it is SQL
that sqlkit reads and checks, so it uses SQL's own named parameters and stays
SQL you could paste into a database console. A `${...}` inside a query's
backticks is an error. Either way, a parameter travels apart from the SQL,
as in [Parameters](#parameters).

### Calling the queries

This is the whole shop. Its `main.nomi` builds the schema in a fresh
in-memory database, adds a user, places an order with `insert_order` (a
query like `insert_user`), marks it paid and reads the user back. Then it
adds a second user and prints how many orders each one placed. Add a
column in a new migration, or a column to a query's `SELECT`, and run it
again: `prepare` rewrites the snapshot, and its tab shows the change.

```nomi-run manual tool:sqlkit/prepare read-only:db/main.snapshot.json
// FILE: main.nomi
// The shop on a fresh in-memory database: build the schema from the
// migrations, add a user, place an order, mark it paid, read the user back
// and count each user's orders with the checked queries.
import {
    std/io
    std/sql.{self, Error}
    queries
    schema
    sqlite
    sqlkit
}

fn main() -> Result<Unit, Error> {
    sql.with_connection(sqlite.memory, fn db -> {
        try sqlkit.migrate(db, schema.migrations)

        user_id = try queries.insert_user(db, "ada@example.com", Some("Ada"))
        order_id = try queries.insert_order(db, user_id, 12.50d)
        changed = try queries.set_status(db, order_id, .Paid)
        io.print("order ${order_id} is paid (${changed.rows} row changed)")

        Some(user) = try queries.user_by_email(db, "ada@example.com") else {
            None -> {
                io.print("no user with that email")
                return Ok(Unit)
            }
        }

        name = user.name |> Maybe.with_default("no name")
        io.print("user ${user.id}: ${user.email} (${name})")

        _ = try queries.insert_user(db, "grace@example.com", None)
        counts = try queries.order_counts(db)

        Iter.each(counts, fn c -> {
            last = c.last_order |> Maybe.map(fn id -> "${id}") |> Maybe.with_default("none")
            io.print("${c.email}: orders ${c.orders}, last order ${last}")
        })

        Ok(Unit)
    })
}

// FILE: queries.nomi
import {
    std/instant.Instant
    schema.{Order, OrderStatus, User}
    sqlkit
}

/// The user with this email, if any.
pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`
        SELECT id, email, name, created_at
        FROM users
        WHERE email = @email
    `

/// Add a user, answering its id.
pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
        INSERT INTO users (email, name) VALUES (@email, @name)
        RETURNING id
    `

/// Place an order for a user.
pub sqlkit.query! insert_order(user_id: Int, total: Decimal) -> Int Sql`
        INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
        RETURNING id
    `

/// Change an order's status: the number of orders changed.
pub sqlkit.query! set_status(id: Int, status: OrderStatus) -> Changed Sql`
        UPDATE orders SET status = @status WHERE id = @id
    `

/// One order.
pub sqlkit.query! order(id: Int) -> Order Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on
        FROM orders
        WHERE id = @id
    `

/// A user's orders, newest first, each with the user's email.
pub sqlkit.query! orders_for_user(user_id: Int, limit: Int) Sql`
        SELECT o.id, o.status, o.total, o.placed_at, u.email
        FROM orders o
        JOIN users u ON u.id = o.user_id
        WHERE o.user_id = @user_id
        ORDER BY o.placed_at DESC, o.id DESC
        LIMIT @limit
    `

/// Every user with the number of orders they placed, including those who
/// placed none.
pub sqlkit.query! order_counts() Sql`
        SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id
        ORDER BY u.email
    `

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub sqlkit.query! revenue_since(since: Instant) -> List<CustomerRevenue> Sql`
        SELECT u.id AS user_id, u.email,
               count(*) AS "order_count!",
               CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
        FROM users u
        JOIN orders o ON o.user_id = u.id
        WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
        GROUP BY u.id, u.email
        ORDER BY revenue DESC
    `

/// Every order, streamed.
pub sqlkit.query! all_orders() -> Rows<Order> Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id
    `

/// A customer's revenue.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: shop.nomi
// The shop's operations, over any `Conn`: a connection, a transaction, or
// a test's.
import {
    std/sql.{self, Conn, Error}
    queries
    sqlkit.Rows
}

/// Register a user and place their first order, in one transaction: if
/// the order fails, the user is not registered either.
pub fn sign_up_with_order(conn: Conn, email: String, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        user_id = try queries.insert_user(tx, email, None)
        queries.insert_order(tx, user_id, total)
    })
}

/// Place an order and mark it paid, in one transaction.
pub fn place_paid_order(conn: Conn, user_id: Int, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        order_id = try queries.insert_order(tx, user_id, total)
        _ = try queries.set_status(tx, order_id, .Paid)
        Ok(order_id)
    })
}

/// The total of every order, read one row at a time.
pub fn total_of_all_orders(conn: Conn) -> Result<Decimal, Error> {
    queries.all_orders(conn) |> Rows.fold(0d, fn sum, order -> sum + order.total)
}

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
db/main.snapshot.json is up to date: 2 tables, 9 queries (0 SQLite rejects)
order 1 is paid (1 row changed)
user 1: ada@example.com (Ada)
ada@example.com: orders 1, last order 1
grace@example.com: orders 0, last order none
-->

`sqlkit.migrate(db, schema.migrations)` runs each migration the database has
not run yet, in order, and records it in a table, `nomi_migrations`. So the
same call builds a new database from nothing and brings an existing file up
to date. If a migration's text changed after it ran, `migrate` runs nothing
and names the file: write the change as a new migration instead.

From there, each call is an ordinary function call that the compiler has
checked against the schema. The `Some(user) = ... else` line is
[binding with `else`](/pattern-matching/#binding-with-else): it handles the
`None` case on the spot. A caller that does not need a count discards it:
`_ = try queries.set_status(db, id, .Paid)`.

A query function takes any `Conn`, so it runs in a
[transaction](#transactions) as a direct statement does. In `shop.nomi`,
`sign_up_with_order` signs a user up with a first order inside
`sql.transaction`, both or neither, and `place_paid_order` places an order
and marks it paid the same way.

### Joins and NULL

A **join** combines matching rows of two tables, such as each order with
the user who placed it. A plain `JOIN` leaves out a user with no orders. A
`LEFT JOIN` keeps every user, and fills the order's columns with `NULL` for
a user who has none. The last two lines the shop prints come from
`order_counts`. In it, `users u` lets the query call the table `u`, `max`
and `count` combine a user's orders into one value each, and `GROUP BY u.id`
makes one row per user:

```nomi
/// Every user with the number of orders they placed, including those who
/// placed none.
pub sqlkit.query! order_counts() Sql`
        SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id
        ORDER BY u.email
    `
```

A column computed by an expression, such as `count(o.id)`, has no SQL type,
and sqlkit can't tell whether it is ever `NULL`. You say so in its name,
after `AS`, inside double quotes:

- `AS "n!"`: never `NULL`, so not a `Maybe`. A count is never `NULL`.
- `AS "n?"`: may be `NULL`, so a `Maybe`. `max` over a user with no orders
  is `NULL`.
- `AS "n: Int"`: the column's Nomi type. An anonymous struct needs it; a
  struct of your own gives the type with its field.

sqlkit takes the marks out before the SQL is sent, so the columns are
called `last_order` and `orders`. The query has no result type, so each row
is an anonymous struct with the fields `email: String`,
`last_order: Maybe<Int>` and `orders: Int`. Grace placed no orders, so her
`last_order` is `None`, which `main` prints as `none`.

A plain column needs no mark. `prepare` asks SQLite which tables a join may
leave out, so a column from the left-out side of a `LEFT JOIN` is a `Maybe`,
even when it is `NOT NULL` in its own table. `orders_for_user` in
`queries.nomi` is a plain `JOIN`, so its `u.email` is a `String`.

### Errors in the editor

The editor checks queries as you type, because checking needs only the
snapshot, and shows the same errors as `nomi check` at the SQL they are
about. Say you misspell `email` in `user_by_email`. The editor first says
the snapshot does not describe the new SQL, and offers a quick fix that runs
`prepare`; it never runs `prepare` on its own. Once `prepare` has run,
SQLite's own error points at the word, as in this example, which runs
`prepare` first:

```nomi-run manual error tool:sqlkit/prepare read-only:db/main.snapshot.json
// FILE: queries.nomi
import {
    std/instant.Instant
    schema.{Order, OrderStatus, User}
    sqlkit
}

/// The user with this email, if any.
pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`
        SELECT id, emial, name, created_at
        FROM users
        WHERE email = @email
    `

/// Add a user, answering its id.
pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
        INSERT INTO users (email, name) VALUES (@email, @name)
        RETURNING id
    `

/// Place an order for a user.
pub sqlkit.query! insert_order(user_id: Int, total: Decimal) -> Int Sql`
        INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
        RETURNING id
    `

/// Change an order's status: the number of orders changed.
pub sqlkit.query! set_status(id: Int, status: OrderStatus) -> Changed Sql`
        UPDATE orders SET status = @status WHERE id = @id
    `

/// One order.
pub sqlkit.query! order(id: Int) -> Order Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on
        FROM orders
        WHERE id = @id
    `

/// A user's orders, newest first, each with the user's email.
pub sqlkit.query! orders_for_user(user_id: Int, limit: Int) Sql`
        SELECT o.id, o.status, o.total, o.placed_at, u.email
        FROM orders o
        JOIN users u ON u.id = o.user_id
        WHERE o.user_id = @user_id
        ORDER BY o.placed_at DESC, o.id DESC
        LIMIT @limit
    `

/// Every user with the number of orders they placed, including those who
/// placed none.
pub sqlkit.query! order_counts() Sql`
        SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id
        ORDER BY u.email
    `

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub sqlkit.query! revenue_since(since: Instant) -> List<CustomerRevenue> Sql`
        SELECT u.id AS user_id, u.email,
               count(*) AS "order_count!",
               CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
        FROM users u
        JOIN orders o ON o.user_id = u.id
        WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
        GROUP BY u.id, u.email
        ORDER BY revenue DESC
    `

/// Every order, streamed.
pub sqlkit.query! all_orders() -> Rows<Order> Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id
    `

/// A customer's revenue.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
wrote db/main.snapshot.json: 2 tables, 9 queries (1 SQLite rejects)
queries.nomi:9:20: no such column: emial
queries.nomi:9:20: help: reported by SQLite when `nomi tool sqlkit prepare` described `user_by_email`
-->

`prepare` records SQLite's error in the snapshot, which counts it among
the queries SQLite rejects, and the compiler reports it at the query.

A field of the wrong type is an error at its column, and so is a nullable
column read into a field that is not a `Maybe`. Inside a query's SQL the
editor also helps you write it:

- Hovering over a table or a column shows its type and the migration line
  that created it.
- Go to definition on a table or a column opens that migration line.
- Completion offers the tables and their columns.

### In an application

The shop's `main` opens a database for one run. A program that serves
requests opens it once, in its [`boot`](/capabilities-and-context/), and
keeps the connection in an app field, so every function reads it as
`App.db`. This `main.nomi` takes the place of the shop's. Its `main` signs
up two users through `sign_up` and reads them back with `order_counts`, and
`main_test.nomi` tests `sign_up` under the same `boot`:

```nomi-run manual tool:sqlkit/prepare read-only:db/main.snapshot.json
// FILE: main.nomi
// The shop as an application: boot opens the database once, and every
// function reads it as `App.db`.
import {
    std/io
    std/sql.{self, Conn, Error}
    queries
    schema
    sqlite
    sqlkit
}

pub struct App {
    db: Conn
    context: Context
}

pub fn boot(startup: Startup) -> Result<App, Error> {
    path = Map.get(startup.env, "SHOP_DATABASE") |> Maybe.with_default(":memory:")
    db = try sqlite.open(path)
    defer sql.close_quietly(db)
    try sqlkit.migrate(db, schema.migrations)
    Ok(#App{db, context: Context.root()})
}

pub fn sign_up(email: String) -> Result<Int, Error> {
    queries.insert_user(App.db, email, None)
}

fn main() -> Result<Unit, Error> {
    _ = try sign_up("ada@example.com")
    _ = try sign_up("grace@example.com")
    users = try queries.order_counts(App.db)
    Iter.each(users, fn u -> io.print("${u.email}: ${u.orders} orders"))
    Ok(Unit)
}

// FILE: main_test.nomi
import {
    std/sql.{self, ConstraintKind}
    main.{self, App}
    queries
}

group "sign-up" {
    // Each test boots a fresh in-memory database.
    boot main.boot(#Startup{})

    test "a new user reads back by email" {
        id = try main.sign_up("ada@example.com")
        user = try queries.user_by_email(App.db, "ada@example.com")
        assert user |> Maybe.map(fn u -> u.id) == Some(id)
    }

    test "the same email twice breaks the unique constraint" {
        _ = try main.sign_up("ada@example.com")

        assert main.sign_up("ada@example.com") == Err(
            #sql.Error.Constraint{
                kind: ConstraintKind.Unique,
                name: "users_email_key",
                table: Some("users"),
                column: Some("email"),
            }
        )
    }
}

// FILE: queries.nomi
import {
    std/instant.Instant
    schema.{Order, OrderStatus, User}
    sqlkit
}

/// The user with this email, if any.
pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`
        SELECT id, email, name, created_at
        FROM users
        WHERE email = @email
    `

/// Add a user, answering its id.
pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
        INSERT INTO users (email, name) VALUES (@email, @name)
        RETURNING id
    `

/// Place an order for a user.
pub sqlkit.query! insert_order(user_id: Int, total: Decimal) -> Int Sql`
        INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
        RETURNING id
    `

/// Change an order's status: the number of orders changed.
pub sqlkit.query! set_status(id: Int, status: OrderStatus) -> Changed Sql`
        UPDATE orders SET status = @status WHERE id = @id
    `

/// One order.
pub sqlkit.query! order(id: Int) -> Order Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on
        FROM orders
        WHERE id = @id
    `

/// A user's orders, newest first, each with the user's email.
pub sqlkit.query! orders_for_user(user_id: Int, limit: Int) Sql`
        SELECT o.id, o.status, o.total, o.placed_at, u.email
        FROM orders o
        JOIN users u ON u.id = o.user_id
        WHERE o.user_id = @user_id
        ORDER BY o.placed_at DESC, o.id DESC
        LIMIT @limit
    `

/// Every user with the number of orders they placed, including those who
/// placed none.
pub sqlkit.query! order_counts() Sql`
        SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id
        ORDER BY u.email
    `

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub sqlkit.query! revenue_since(since: Instant) -> List<CustomerRevenue> Sql`
        SELECT u.id AS user_id, u.email,
               count(*) AS "order_count!",
               CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
        FROM users u
        JOIN orders o ON o.user_id = u.id
        WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
        GROUP BY u.id, u.email
        ORDER BY revenue DESC
    `

/// Every order, streamed.
pub sqlkit.query! all_orders() -> Rows<Order> Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id
    `

/// A customer's revenue.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: shop.nomi
// The shop's operations, over any `Conn`: a connection, a transaction, or
// a test's.
import {
    std/sql.{self, Conn, Error}
    queries
    sqlkit.Rows
}

/// Register a user and place their first order, in one transaction: if
/// the order fails, the user is not registered either.
pub fn sign_up_with_order(conn: Conn, email: String, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        user_id = try queries.insert_user(tx, email, None)
        queries.insert_order(tx, user_id, total)
    })
}

/// Place an order and mark it paid, in one transaction.
pub fn place_paid_order(conn: Conn, user_id: Int, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        order_id = try queries.insert_order(tx, user_id, total)
        _ = try queries.set_status(tx, order_id, .Paid)
        Ok(order_id)
    })
}

/// The total of every order, read one row at a time.
pub fn total_of_all_orders(conn: Conn) -> Result<Decimal, Error> {
    queries.all_orders(conn) |> Rows.fold(0d, fn sum, order -> sum + order.total)
}

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
db/main.snapshot.json is up to date: 2 tables, 9 queries (0 SQLite rejects)
ada@example.com: 0 orders
grace@example.com: 0 orders
ok sign-up / a new user reads back by email
ok sign-up / the same email twice breaks the unique constraint
test result: ok. 2 passed, 0 failed
-->

`boot` opens the database the `SHOP_DATABASE` variable names, such as
`shop.db`, and a fresh in-memory one when it is not set. The tour sets no
variables, so this run's database is in memory.

`boot` returns a `Result`, so each step that can fail is a `try`. If the file
does not open or a migration fails, `boot` returns the `Err`, `main` does
not run, and the program prints `error: ` and the error and exits 1. The
`defer` in `boot` closes the database when the program ends, after `main`
and the tasks it started have finished, or right away when a later step of
`boot` fails.

The test file's group calls the same `boot` before each test with
`boot main.boot(#Startup{})`, as in
[Testing](/testing/#booting-the-application). The `Startup` is empty, so
each test gets a fresh in-memory database and its own `App.db`. The second
test signs up the same email twice and gets the constraint error. A test
that builds its own database instead replaces the field with
`with App.db = db` in its `setup`.

### Testing with the schema

A test group's `setup` runs before each test and hands it a value, as the
[Testing](/testing/#groups-and-setup) chapter explains. In the shop's
`shop_test.nomi`, the value is a fresh in-memory database with the shop's
schema. This example runs `prepare`, then the shop's eleven tests:

```nomi-run manual tool:sqlkit/prepare read-only:db/main.snapshot.json
// FILE: shop_test.nomi
import {
    std/duration.Duration
    std/instant.Instant
    std/sql.{self, ConstraintKind}
    queries
    schema.{self, Order, OrderStatus, User}
    shop
    sqlite
    sqlkit
}

group "the shop" {
    // Each test gets a fresh in-memory database with the shop's schema.
    setup {
        db = try sqlite.memory()
        defer sql.close_quietly(db)
        try sqlkit.migrate(db, schema.migrations)
        db
    }

    test "a user reads back as its row", db {
        id = try queries.insert_user(db, "ada@example.com", Some("Ada"))

        Some(user) = try queries.user_by_email(db, "ada@example.com") else {
            None -> return Err(#sql.Error.Database{code: "", message: "no user"})
        }

        assert #User{id: read_id, email, name} = user
        assert read_id == id
        assert email == "ada@example.com"
        assert name == Some("Ada")
        assert try queries.user_by_email(db, "nobody@example.com") == None
    }

    test "a second user with the same email breaks the unique constraint", db {
        _ = try queries.insert_user(db, "ada@example.com", None)

        assert queries.insert_user(db, "ada@example.com", None) == Err(
            #sql.Error.Constraint{
                kind: ConstraintKind.Unique,
                name: "users_email_key",
                table: Some("users"),
                column: Some("email"),
            }
        )
    }

    test "an order reads back with its enum status and decimal total", db {
        user = try queries.insert_user(db, "ada@example.com", None)
        id = try shop.place_paid_order(db, user, 12.50d)
        order = try queries.order(db, id)
        assert #Order{status, total, shipped_on} = order
        assert status == OrderStatus.Paid
        // `==` on Decimal ignores the scale; the text shows it.
        assert "${total}" == "12.50"
        assert shipped_on == None

        assert queries.order(db, id + 1) == Err(
            #sql.Error.RowCount{query: "order", expected: "exactly one", found: 0}
        )
    }

    test "a failed sign-up leaves nothing behind", db {
        _ = try shop.sign_up_with_order(db, "ada@example.com", 5d)
        failed = shop.sign_up_with_order(db, "ada@example.com", 7d)
        assert Result.err?(failed)
        assert try shop.total_of_all_orders(db) == 5d
        counts = try queries.order_counts(db)

        assert counts |> Iter.map(fn c -> #(c.email, c.orders)) |> Iter.to_list() == [
            #("ada@example.com", 1),
        ]
    }

    test "a join reads every order of a user, newest first", db {
        user = try queries.insert_user(db, "ada@example.com", None)
        first = try queries.insert_order(db, user, 1d)
        second = try queries.insert_order(db, user, 2d)
        rows = try queries.orders_for_user(db, user, 10)
        assert rows |> Iter.map(fn r -> r.id) |> Iter.to_list() == [second, first]

        assert rows
            |> Iter.all?(fn r -> r.email == "ada@example.com" and r.status == OrderStatus.Pending)
    }

    test "an outer join's columns are Maybe, and an aggregate counts", db {
        ada = try queries.insert_user(db, "ada@example.com", None)
        _ = try queries.insert_user(db, "grace@example.com", None)
        _ = try queries.insert_order(db, ada, 3d)
        latest = try queries.insert_order(db, ada, 4d)
        counts = try queries.order_counts(db)

        assert counts |> Iter.map(fn c -> #(c.email, c.last_order, c.orders)) |> Iter.to_list() == [
            #("ada@example.com", Some(latest), 2),
            #("grace@example.com", None, 0),
        ]
    }

    test "revenue sums the paid orders of each customer", db {
        ada = try queries.insert_user(db, "ada@example.com", None)
        grace = try queries.insert_user(db, "grace@example.com", None)
        _ = try shop.place_paid_order(db, ada, 10d)
        _ = try shop.place_paid_order(db, ada, 2.5d)
        _ = try queries.insert_order(db, grace, 100d)
        _ = try shop.place_paid_order(db, grace, 1d)
        revenue = try queries.revenue_since(db, Instant.from_seconds(0))
        assert revenue |> Iter.map(fn r -> "${r.revenue}") |> Iter.to_list() == ["12.50", "1.00"]

        assert revenue == [
            #queries.CustomerRevenue{
                user_id: ada,
                email: "ada@example.com",
                order_count: 2,
                revenue: 12.50d,
            },
            #queries.CustomerRevenue{
                user_id: grace,
                email: "grace@example.com",
                order_count: 1,
                revenue: 1d,
            },
        ]
    }

    test "a total keeps its column's scale, and is exact to 15 digits", db {
        user = try queries.insert_user(db, "ada@example.com", None)
        whole = try queries.insert_order(db, user, 7d)
        largest = try queries.insert_order(db, user, 9_999_999_999.99d)
        assert "${(try queries.order(db, whole)).total}" == "7.00"
        assert "${(try queries.order(db, largest)).total}" == "9999999999.99"

        // 2^53 + 1 cents does not fit a 64-bit float, which is how SQLite
        // keeps a DECIMAL: the driver refuses it rather than round it.
        refused = case queries.insert_order(db, user, 90_071_992_547_409.93d) {
            Err(#sql.Error.Database{code, message: _}) -> code
            _ -> "not refused"
        }

        assert refused == "SQLITE_MISMATCH"
    }

    test "a sum of many small totals is exact", db {
        user = try queries.insert_user(db, "ada@example.com", None)

        Iter.from(0)
        |> Iter.take(1000)
        |> Iter.each(fn _ -> { _ = shop.place_paid_order(db, user, 0.10d) })

        revenue = try queries.revenue_since(db, Instant.from_seconds(0))
        assert revenue |> Iter.map(fn r -> "${r.revenue}") |> Iter.to_list() == ["100.00"]
        assert "${try shop.total_of_all_orders(db)}" == "100.00"
    }

    test "a status update answers the rows it changed", db {
        user = try queries.insert_user(db, "ada@example.com", None)
        id = try queries.insert_order(db, user, 1d)
        assert queries.set_status(db, id, .Shipped) == Ok(#sqlkit.Changed{rows: 1})
        assert queries.set_status(db, id + 1, .Shipped) == Ok(#sqlkit.Changed{rows: 0})
    }

    test "revenue since an hour ago counts an order placed now", db {
        user = try queries.insert_user(db, "ada@example.com", None)
        _ = try shop.place_paid_order(db, user, 10d)
        now = Instant.now()
        hour_ago = try queries.revenue_since(db, now - Duration.hours(1))
        hour_later = try queries.revenue_since(db, now + Duration.hours(1))
        assert hour_ago |> Iter.map(fn r -> r.order_count) |> Iter.to_list() == [1]
        assert hour_later == []
    }
}

// FILE: queries.nomi
import {
    std/instant.Instant
    schema.{Order, OrderStatus, User}
    sqlkit
}

/// The user with this email, if any.
pub sqlkit.query! user_by_email(email: String) -> Maybe<User> Sql`
        SELECT id, email, name, created_at
        FROM users
        WHERE email = @email
    `

/// Add a user, answering its id.
pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
        INSERT INTO users (email, name) VALUES (@email, @name)
        RETURNING id
    `

/// Place an order for a user.
pub sqlkit.query! insert_order(user_id: Int, total: Decimal) -> Int Sql`
        INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
        RETURNING id
    `

/// Change an order's status: the number of orders changed.
pub sqlkit.query! set_status(id: Int, status: OrderStatus) -> Changed Sql`
        UPDATE orders SET status = @status WHERE id = @id
    `

/// One order.
pub sqlkit.query! order(id: Int) -> Order Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on
        FROM orders
        WHERE id = @id
    `

/// A user's orders, newest first, each with the user's email.
pub sqlkit.query! orders_for_user(user_id: Int, limit: Int) Sql`
        SELECT o.id, o.status, o.total, o.placed_at, u.email
        FROM orders o
        JOIN users u ON u.id = o.user_id
        WHERE o.user_id = @user_id
        ORDER BY o.placed_at DESC, o.id DESC
        LIMIT @limit
    `

/// Every user with the number of orders they placed, including those who
/// placed none.
pub sqlkit.query! order_counts() Sql`
        SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id
        ORDER BY u.email
    `

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub sqlkit.query! revenue_since(since: Instant) -> List<CustomerRevenue> Sql`
        SELECT u.id AS user_id, u.email,
               count(*) AS "order_count!",
               CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
        FROM users u
        JOIN orders o ON o.user_id = u.id
        WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
        GROUP BY u.id, u.email
        ORDER BY revenue DESC
    `

/// Every order, streamed.
pub sqlkit.query! all_orders() -> Rows<Order> Sql`
        SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id
    `

/// A customer's revenue.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

// FILE: schema.nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main

// FILE: shop.nomi
// The shop's operations, over any `Conn`: a connection, a transaction, or
// a test's.
import {
    std/sql.{self, Conn, Error}
    queries
    sqlkit.Rows
}

/// Register a user and place their first order, in one transaction: if
/// the order fails, the user is not registered either.
pub fn sign_up_with_order(conn: Conn, email: String, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        user_id = try queries.insert_user(tx, email, None)
        queries.insert_order(tx, user_id, total)
    })
}

/// Place an order and mark it paid, in one transaction.
pub fn place_paid_order(conn: Conn, user_id: Int, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        order_id = try queries.insert_order(tx, user_id, total)
        _ = try queries.set_status(tx, order_id, .Paid)
        Ok(order_id)
    })
}

/// The total of every order, read one row at a time.
pub fn total_of_all_orders(conn: Conn) -> Result<Decimal, Error> {
    queries.all_orders(conn) |> Rows.fold(0d, fn sum, order -> sum + order.total)
}

// FILE: db/migrations/20261019120000_users_and_orders.sql
CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

// FILE: db/migrations/20261102090000_orders_shipped_on.sql
ALTER TABLE orders ADD COLUMN shipped_on DATE;

// FILE: db/main.snapshot.json
{
  "format": 2,
  "database": "main",
  "dialect": "sqlite",
  "migrations": "fd5b73c2f9a9f117",
  "tables": [
    {
      "name": "orders",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 8,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 9},
        {"name": "user_id", "type_name": "INTEGER", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 10},
        {"name": "status", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": true, "values": ["pending", "paid", "shipped", "cancelled"], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 11},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 14},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 15},
        {"name": "shipped_on", "type_name": "DATE", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261102090000_orders_shipped_on.sql", "line": 1}
      ]
    },
    {
      "name": "users",
      "migration": "db/migrations/20261019120000_users_and_orders.sql",
      "line": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "nullable": false, "primary_key": true, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 2},
        {"name": "email", "type_name": "TEXT", "nullable": false, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 3},
        {"name": "name", "type_name": "TEXT", "nullable": true, "primary_key": false, "has_default": false, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 4},
        {"name": "created_at", "type_name": "TIMESTAMPTZ", "nullable": false, "primary_key": false, "has_default": true, "values": [], "migration": "db/migrations/20261019120000_users_and_orders.sql", "line": 5}
      ]
    }
  ],
  "queries": [
    {
      "file": "queries.nomi",
      "name": "all_orders",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id",
      "params": 0,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_order",
      "sql": "INSERT INTO orders (user_id, total) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "insert_user",
      "sql": "INSERT INTO users (email, name) VALUES (?1, ?2) RETURNING id",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order",
      "sql": "SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ?1",
      "params": 1,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "user_id", "type_name": "INTEGER", "table": "orders", "column": "user_id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "shipped_on", "type_name": "DATE", "table": "orders", "column": "shipped_on", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "order_counts",
      "sql": "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
      "params": 0,
      "columns": [
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "last_order", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "orders", "type_name": "", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "orders_for_user",
      "sql": "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ?1 ORDER BY o.placed_at DESC, o.id DESC LIMIT ?2",
      "params": 2,
      "columns": [
        {"name": "id", "type_name": "INTEGER", "table": "orders", "column": "id", "nullable": false},
        {"name": "status", "type_name": "TEXT", "table": "orders", "column": "status", "nullable": false},
        {"name": "total", "type_name": "DECIMAL(12, 2)", "table": "orders", "column": "total", "nullable": false},
        {"name": "placed_at", "type_name": "TIMESTAMPTZ", "table": "orders", "column": "placed_at", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "revenue_since",
      "sql": "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ?1 GROUP BY u.id, u.email ORDER BY revenue DESC",
      "params": 1,
      "columns": [
        {"name": "user_id", "type_name": "INTEGER", "table": "users", "column": "id", "nullable": false},
        {"name": "email", "type_name": "TEXT", "table": "users", "column": "email", "nullable": false},
        {"name": "order_count", "type_name": "", "table": "", "column": "", "nullable": true},
        {"name": "revenue", "type_name": "DECIMAL(12, 2)", "table": "", "column": "", "nullable": true}
      ],
      "error": null
    },
    {
      "file": "queries.nomi",
      "name": "set_status",
      "sql": "UPDATE orders SET status = ?1 WHERE id = ?2",
      "params": 2,
      "columns": [],
      "error": null
    },
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
  ]
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
db/main.snapshot.json is up to date: 2 tables, 9 queries (0 SQLite rejects)
ok the shop / a user reads back as its row
ok the shop / a second user with the same email breaks the unique constraint
ok the shop / an order reads back with its enum status and decimal total
ok the shop / a failed sign-up leaves nothing behind
ok the shop / a join reads every order of a user, newest first
ok the shop / an outer join's columns are Maybe, and an aggregate counts
ok the shop / revenue sums the paid orders of each customer
ok the shop / a total keeps its column's scale, and is exact to 15 digits
ok the shop / a sum of many small totals is exact
ok the shop / a status update answers the rows it changed
ok the shop / revenue since an hour ago counts an order placed now
test result: ok. 11 passed, 0 failed
-->

`sqlkit.migrate` builds the schema, and the `defer` closes the database when
the test ends. Each test has its own database, so no test sees another's
rows. In the second test, the second `insert_user` breaks the `UNIQUE` on
`email`, and the constraint gets its Postgres name, `users_email_key`, as in
[Errors and constraints](#errors-and-constraints).

sqlkit checks your SQL against SQLite. A driver for another database
implements the same `Conn`, so sqlkit's functions take its connections too,
but each query must then be SQL that both databases accept.
