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
[`github.com/nomi-language/sql`](https://github.com/nomi-language/sql). It
talks to SQLite, a database kept in one file or in memory, with no server to
run, and builds on [`std/sql`](/reference/sql/), the types every database
library shares. The second way adds `sqlkit`, from the same repository. Make
a project and add the driver, as the [Modules](/modules-and-imports/)
chapter describes:

```console
$ nomi new notes
$ cd notes
$ nomi add github.com/nomi-language/sql/sqlite
```

The driver calls Go code, as the [FFI](/ffi-and-dynamic/) chapter describes,
so a program that uses it can't run in the browser. The output under those
programs comes from running them with `nomi`. The blocks that use only
`std/sql` are live editors.

## Running SQL directly

### A first query

This `main.nomi` makes a table in a new in-memory database, adds two rows
and reads them back:

```nomi
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

```console
$ nomi run main.nomi
1: buy milk
2: call Ada
```

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
inside one:

```nomi
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
```

`sql.transaction` starts a transaction and runs the function with it as
`tx`. When the function returns `Ok`, the changes take effect. When it
returns `Err`, or traps, every change made through `tx` is undone.

A transaction is a `Conn` too, so anything that takes a connection runs
inside one. `sql.transaction` on a transaction starts a savepoint inside it,
so a failure there undoes only the inner part.

### Errors and constraints

A table can carry rules the database enforces, called **constraints**: a
`CHECK`, a `UNIQUE` column, a foreign key. Say `accounts` was made with
`balance INTEGER NOT NULL CHECK (balance >= 0)`, Ada has 50 and Bob has 0.
The first `transfer(db, "ada", "bob", 30)` returns
`Ok(Unit)`. The second returns:

```nomi
Err(
    #Constraint{
        kind: Check,
        name: "accounts_balance_check",
        table: Some("accounts"),
        column: Some("balance"),
    }
)
```

The second debit would leave Ada at -10, so the database refuses it, and
`transfer` returns a `sql.Error.Constraint` with the kind of rule, its name,
and the table and column. Bob's credit was undone with it: the balances are
Ada 20 and Bob 30. A rule the schema leaves unnamed gets the name Postgres
would give it, as `accounts_balance_check` here, so a program can tell one
rule's failure from another by its name.

### Testing

Open a database with `sqlite.memory()` in each test, so no test sees another's
rows. To test code without any database,
[`Fake`](/reference/sql_testing/) from `std/sql_testing` is a `Conn` that
runs no SQL: it records each statement, answers it with a function you give
it, and tells you which statements took effect.

## Checked SQL with sqlkit

Nothing above checks a statement before it runs. A misspelled column, a
column read as the wrong type, or a `NULL` where you expected a value, is
an `Err` at run time. sqlkit moves those mistakes to `nomi check` and your
editor. Add it beside the driver:

```console
$ nomi add github.com/nomi-language/sql/sqlkit
```

The rest of the chapter follows a small shop that keeps users and their
orders. It is
[`examples/shop`](https://github.com/nomi-language/sql/tree/main/examples/shop)
in the SQL repository, and runs from a clone:

```console
$ git clone https://github.com/nomi-language/sql
$ cd sql/examples/shop
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
`shop.nomi` holds operations built from them.

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

### Declaring the database

The shop's `schema.nomi` declares the database:

```nomi
// The shop's database: `User` and `Order` rows, the `OrderStatus` enum,
// and `migrations`, all generated from db/main.snapshot.json (see them with
// `nomi expand schema.nomi`).
import sqlkit

/// The shop's SQLite database.
pub sqlkit.database! main
```

`sqlkit.database!` is a [macro](/macros/): code in the sqlkit library that
writes Nomi code while your program is checked, marked by the `!`. It reads
the migrations in `db/migrations/` and the snapshot `db/main.snapshot.json`.
Both are at their default paths, so the shop's `nomi.toml` says nothing about
its database; the
[sqlkit README](https://github.com/nomi-language/sql/tree/main/sqlkit#readme)
lists the keys for a project that keeps them elsewhere. From them, it
writes:

- a struct for each table, named after one of its rows: the table `users`
  holds `User` rows, and `orders` holds `Order` rows;
- an enum for each column that a `CHECK (column IN (...))` limits;
- `migrations`, the migration files as a list, for `sqlkit.migrate`.

`nomi expand schema.nomi` prints that code. `User` has the fields `id: Int`,
`email: String`, `name: Maybe<String>` and `created_at: instant.Instant`,
and the enum is:

```nomi
/// The values `CHECK (status IN (...))` allows in `orders.status`.
pub enum OrderStatus {
    Pending
    Paid
    Shipped
    Cancelled
}
```

Each column's SQL type picks its field's Nomi type. `INTEGER` is `Int`,
`TEXT` is `String`, `DATE` is a calendar `Date`, `TIMESTAMPTZ` (a moment in
time) is an `Instant`, and `DECIMAL` is a `Decimal`. A nullable column is a
`Maybe`. The sqlkit README lists every SQL type it reads. SQLite keeps a
`DECIMAL` as a 64-bit float, so the driver reads `total` back at its
declared scale, `12.50d`, and refuses a value it could not keep exactly.

A migration changed since the last `prepare` is an error at the
declaration, in `nomi check` and in the editor:

```console
error: db/main.snapshot.json is out of date: the migrations changed after the last `nomi tool sqlkit prepare`
 --> schema.nomi:7:12
  |
7 | pub sqlkit.database! main
  |            ^^^^^^^^^
  = help: run `nomi tool sqlkit prepare` to describe the migrations again
```

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
count:

```nomi
/// Add a user, returning its id.
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
error:

```console
error: `insert_user` returns no rows, so it cannot answer an `Int`; write `-> Changed` for the number of rows it changed, or `-> Unit`
  --> queries.nomi:15:70
   |
15 | pub sqlkit.query! insert_user(email: String, name: Maybe<String>) -> Int Sql`
   |                                                                      ^^^
```

### Query parameters

Every `@name` must name a parameter of the declaration, so a misspelled name
is an error at the name:

```console
error: `mail` is not a parameter of `user_by_email`
  --> queries.nomi:11:23
   |
11 |         WHERE email = @mail
   |                       ^^^^^
   = help: its parameters are `email`
```

Every parameter must be used too, so the same mistake also reports that
`email` is not used. An `@` in a string such as `'ada@example.com'`, or one
not followed by a letter or `_`, is SQL, not a parameter.

A query's body writes parameters as `@name`, not `${...}`, because it is SQL
that sqlkit reads and checks, so it uses SQL's own named parameters and stays
SQL you could paste into a database console. A `${...}` inside a query's
backticks is an error. Either way, a parameter travels apart from the SQL,
as in [Parameters](#parameters).

### Calling the queries

The shop's `main.nomi` builds the schema in a fresh in-memory database, adds
a user, places an order with `insert_order` (a query like `insert_user`),
marks it paid and reads the user back:

```nomi
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
        Ok(Unit)
    })
}
```

```console
$ nomi run main.nomi
order 1 is paid (1 row changed)
user 1: ada@example.com (Ada)
```

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
[transaction](#transactions) as a direct statement does. The shop's
`shop.nomi` signs a user up with a first order, both or neither:

```nomi
pub fn sign_up_with_order(conn: Conn, email: String, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        user_id = try queries.insert_user(tx, email, None)
        queries.insert_order(tx, user_id, total)
    })
}
```

### Joins and NULL

A **join** combines matching rows of two tables, such as each order with
the user who placed it. A plain `JOIN` leaves out a user with no orders. A
`LEFT JOIN` keeps every user, and fills the order's columns with `NULL` for
a user who has none. In `order_counts`, `users u` lets the query call the
table `u`, `max` and `count` combine a user's orders into one value each,
and `GROUP BY u.id` makes one row per user:

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
`last_order: Maybe<Int>` and `orders: Int`.

A plain column needs no mark. `prepare` asks SQLite which tables a join may
leave out, so a column from the left-out side of a `LEFT JOIN` is a `Maybe`,
even when it is `NOT NULL` in its own table.

### Errors in the editor

The editor checks queries as you type, because checking needs only the
snapshot, and shows the same errors as `nomi check` at the SQL they are
about. Say you misspell `email` in `user_by_email`. The editor first says
the snapshot does not describe the new SQL, and offers a quick fix that runs
`prepare`; it never runs `prepare` on its own. Once `prepare` has run,
SQLite's own error points at the word:

```console
error: no such column: emial
 --> queries.nomi:9:20
  |
9 |         SELECT id, emial, name, created_at
  |                    ^^^^^
  = help: reported by SQLite when `nomi tool sqlkit prepare` described `user_by_email`
```

A field of the wrong type is an error at its column, and so is a nullable
column read into a field that is not a `Maybe`. Inside a query's SQL the
editor also helps you write it:

- Hovering over a table or a column shows its type and the migration line
  that created it.
- Go to definition on a table or a column opens that migration line.
- Completion offers the tables and their columns.

### Testing with the schema

A test group's `setup` runs before each test and hands it a value, as the
[Testing](/testing/#groups-and-setup) chapter explains. Here the value is a
fresh in-memory database with the shop's schema:

```nomi
group "the shop" {
    // Each test gets a fresh in-memory database with the shop's schema.
    setup {
        db = try sqlite.memory()
        defer sql.close_quietly(db)
        try sqlkit.migrate(db, schema.migrations)
        db
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
}
```

`sqlkit.migrate` builds the schema, and the `defer` closes the database when
the test ends. Each test has its own database, so no test sees another's
rows. The second `insert_user` breaks the `UNIQUE` on `email`, and the
constraint gets its Postgres name, `users_email_key`, as in
[Errors and constraints](#errors-and-constraints). `nomi test .` runs the
shop's eleven tests this way.

sqlkit checks your SQL against SQLite. A driver for another database
implements the same `Conn`, so sqlkit's functions take its connections too,
but each query must then be SQL that both databases accept.
