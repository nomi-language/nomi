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
  and signature, and sqlkit's `generate` tool checks it against your schema
  and writes an ordinary typed function for it.

The examples use SQLite, a database kept in a file or in memory, with no
server to run. Make a project and add its driver, `sqlite`, as the
[Modules](/modules-and-imports/) chapter describes:

```console
$ nomi new notes
$ cd notes
$ nomi add github.com/nomi-language/nomi/lib/sql/sqlite
```

The examples run in this page on a real SQLite. Edit them and they run
again.

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
        _ = Conn.exec(db, Sql"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")?
        _ = Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"buy milk"})")?
        _ = Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"call Ada"})")?

        rows = Conn.query(db, Sql"SELECT id, body FROM notes ORDER BY id")?
        lines = rows |> Iter.map(note_line) |> Result.collect()?
        Iter.each(lines, io.print)
        Ok(Unit)
    })
}

// One note as a line of text.
fn note_line(row: Row) -> Result<String, Error> {
    id = Row.get<Int>(row, "id")?
    body = Row.get<String>(row, "body")?
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
- A `Conn` is a connection. It comes from [`std/sql`](/reference/sql/),
  which holds the types every database library shares. `Conn.exec` runs a
  statement that changes data and returns how many rows it changed.
  `Conn.query` returns the rows a statement reads.
- `Sql"..."` writes a statement. A `${...}` slot in it is a
  [parameter](#parameters).
- `Row.get` reads one column of a row by name, as the type you ask for, and
  fails when the value is not that type. SQL marks a missing value with
  `NULL`, which reads as `None` when you ask for a `Maybe`.
- `Result.collect` turns each row's `Result` into one: the list of lines,
  or the first error, as in [Lines](/files/#lines).

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

    dbg "${statement}"
    dbg statement.params
    Unit
}
```
<!-- expect
dbg line 7: "${statement}" = "SELECT id, name FROM users WHERE email = ?1"
dbg line 8: statement.params = [Text("x' OR '1'='1")]
-->

Printing the statement writes `?1` where the first value goes. `params`
holds the values, each a [`Value`](/reference/sql/#enum-value): the email is
`Text`, quotes and all, and goes to the database apart from the SQL. The
[Typed Literals](/typed-literals/) chapter explains how `Sql"..."` is built.

### Building a statement from pieces

Some statements depend on what the user asked for, such as a search with
optional filters. `+` joins two statements, SQL and parameters together,
and [`Statement`](/reference/sql/#struct-statement) has functions that
join a list of pieces: `values` for SQL's `IN`, `all` and `any` for
conditions, `where_all` for a `WHERE` that may be empty, and more. Each
piece is still a `Sql"..."` literal, so a value only ever goes in a
`${...}` slot. Here each optional filter is a `Maybe` of a condition, and
`Maybe.values` keeps the ones the caller asked for:

```nomi-run
import std/sql.{Sql, Statement}

/// How a list of orders can be sorted.
enum Sort {
    Newest
    Largest
}

/// Orders, filtered by only what the caller asked for.
fn orders_query(statuses: List<String>, min_total: Maybe<Decimal>, sort: Sort) -> Statement {
    by_status = if Iter.is_empty(statuses) {
        None
    } else {
        Some(Sql"status IN " + Statement.values(statuses))
    }

    by_total = min_total |> Maybe.map(fn t -> Sql"total >= ${t}")

    order = case sort {
        .Newest -> Sql" ORDER BY placed_at DESC"
        .Largest -> Sql" ORDER BY total DESC"
    }

    filters = Maybe.values([by_status, by_total])
    Sql"SELECT id FROM orders" + Statement.where_all(filters) + order
}

fn main() {
    dbg "${orders_query([], None, .Newest)}"
    dbg "${orders_query(["paid", "shipped"], Some(20d), .Largest)}"
    dbg orders_query(["paid", "shipped"], Some(20d), .Largest).params
    Unit
}
```
<!-- expect
dbg line 29: "${orders_query([], None, .Newest)}" = "SELECT id FROM orders ORDER BY placed_at DESC"
dbg line 30: "${orders_query(["paid", "shipped"], Some(20d), .Largest)}" = "SELECT id FROM orders WHERE (status IN (?1, ?2)) AND (total >= ?3) ORDER BY total DESC"
dbg line 31: orders_query(["paid", "shipped"], Some(20d), .Largest).params = [Text("paid"), Text("shipped"), Decimal(20d)]
-->

A column name can't be a parameter, so the sort order is an enum, and each
case gives its own fixed `Sql"..."`. Never build SQL text from a name the
user sent.

### Transactions

A **transaction** groups statements so that either all of them take effect
or none does. Moving money between two accounts is two updates, and if the
second fails, the first must not stand. `sql.transaction` runs a function
inside one. This program moves 30 from Ada to Bob twice. `transfer`
credits Bob before it debits Ada, so that a failed debit has a change to
undo:

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
        _ = Conn.exec(tx, credit)?
        _ = Conn.exec(tx, debit)?
        Ok(Unit)
    })
}

fn main() -> Result<Unit, Error> {
    sql.with_connection(sqlite.memory, fn db -> {
        _ = Conn.exec(
            db,
            Sql"""
            CREATE TABLE accounts (
                name TEXT PRIMARY KEY,
                balance INTEGER NOT NULL CHECK (balance >= 0)
            )
            """,
        )?

        _ = Conn.exec(db, Sql"INSERT INTO accounts VALUES ('ada', 50), ('bob', 0)")?

        dbg transfer(db, "ada", "bob", 30)
        dbg transfer(db, "ada", "bob", 30)

        rows = Conn.query(db, Sql"SELECT name, balance FROM accounts ORDER BY name")?
        lines = rows |> Iter.map(balance_line) |> Result.collect()?
        Iter.each(lines, io.print)
        Ok(Unit)
    })
}

// One account and its balance as a line of text.
fn balance_line(row: Row) -> Result<String, Error> {
    name = Row.get<String>(row, "name")?
    balance = Row.get<Int>(row, "balance")?
    Ok("${name}: ${balance}")
}
```
<!-- expect
dbg line 33: transfer(db, "ada", "bob", 30) = Ok(Unit)
dbg line 34: transfer(db, "ada", "bob", 30) = Err(Constraint{kind: Check, name: "accounts_balance_check", table: Some("accounts"), column: Some("balance")})
ada: 20
bob: 30
-->

`sql.transaction` starts a transaction and runs the function with it as
`tx`. When the function returns `Ok`, the changes take effect. When it
returns `Err`, or traps, every change made through `tx` is undone.

A rule the database enforces on a table is a **constraint**: a `CHECK`, a
`UNIQUE` column, a foreign key. The `accounts` table has one,
`CHECK (balance >= 0)`. Ada starts with 50, so the first transfer succeeds.
The second debit would leave her at -10, so the database refuses it, and
`transfer` returns a `sql.Error.Constraint` with the kind of rule, its name,
and the table and column. Bob's credit is undone with it, so the balances
end at Ada 20 and Bob 30. A rule the schema leaves unnamed gets the name
Postgres would give it, as `accounts_balance_check` here.

A transaction is a `Conn` too, so anything that takes a connection runs
inside one. `sql.transaction` on a transaction starts a savepoint inside it,
so a failure there undoes only the inner part.

<div class="tour-details">
<p class="tour-details-label">Testing direct SQL</p>

- Open a database with `sqlite.memory()` in each test, so no test sees
  another's rows.
- To test code with no database at all, [`Fake`](/reference/sql_testing/)
  from `std/sql_testing` is a `Conn` that runs no SQL. It records each
  statement, answers it with a function you give it, and tells you which
  statements took effect (`Fake.log`, `Fake.committed`).
- [`std/sql`](/reference/sql/) lists every `sql.Error` variant and what
  `Row.get` can read.

</div>

## Checked SQL with sqlkit

Nothing above checks a statement before it runs. A misspelled column, a
column read as the wrong type, or a `NULL` where you expected a value is an
`Err` at run time. sqlkit finds those mistakes before the program runs. Add
it beside the driver:

```console
$ nomi add github.com/nomi-language/nomi/lib/sql/sqlkit
```

The rest of the chapter builds a small shop that keeps users and their
orders. It has one migration, five queries, a `main` and a test file:

```tree
shop/
├── nomi.toml
├── go.mod
├── db/
│   ├── migrations/
│   │   └── 20261019120000_users_and_orders.sql
│   └── queries/
│       └── queries.sql
├── schema.nomi
├── schema_test.nomi
├── queries.nomi
├── main.nomi
└── shop_test.nomi
```

You write the migration, `queries.sql`, `main.nomi` and `shop_test.nomi`.
sqlkit's `generate` tool writes the other three `.nomi` files.

### Migrations

The **schema** is the list of tables and their columns. With sqlkit you
write it as **migrations**: SQL files that each change the schema, applied
in the order of their file names. Each name starts with the date and time it
was written. The shop's one migration creates both tables:

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

A column declared `NOT NULL` always has a value. A column without it is
**nullable**: a row may hold `NULL` there, as a user's `name` may.
`PRIMARY KEY` makes `id` the number that names each row, and SQLite fills it
in. `UNIQUE` means no two users share an email. `REFERENCES users (id)`
means each order's `user_id` must be some user's `id`. The `CHECK` allows
only four values in `status`.

### Queries

`db/queries/queries.sql` holds the queries. Each starts with an annotation
that gives it a Nomi name and signature, then its doc comment, then its
SQL, ending in `;`. The first:

```sql
-- query: user_by_email(email: String) -> Maybe<User>
-- The user with this email, if any.
SELECT id, email, name, created_at
FROM users
WHERE email = @email;
```

It has a name and parameters, as a function has, and a result type:
`-> Maybe<User>` is at most one row, read as a `User`. `@email` marks where
the parameter goes. It is SQL's own syntax for a named parameter, so the
query stays SQL you could paste into a database console. `-> T` expects
exactly one row, `-> List<T>` every row, `-> Changed` the number of rows an
`UPDATE` changed, and a query with no result type answers anonymous rows
of its columns.

### Generating the code

The compiler never connects to a database, so `nomi check` and the editor
need no running database or password. Instead, `generate` applies the
migrations to an empty SQLite database in memory, asks SQLite to describe
each query, and writes what it learns as Nomi code:

```console
$ nomi tool sqlkit generate
main: wrote schema.nomi, schema_test.nomi, queries.nomi
```

`main` is the database's name. `schema.nomi` holds a row struct for each
table, named after one of its rows (the table `users` holds `User` rows),
an enum for each column a `CHECK (column IN (...))` limits, and the
migrations as data:

```nomi
/// A row of the table `users`.
pub struct User {
    id: Int
    email: String
    name: Maybe<String>
    created_at: Instant
}
```

Each column's SQL type picks its field's Nomi type, and a nullable column
is a `Maybe`. `queries.nomi` holds a function for each query, which takes
the connection first and wraps its result in a `Result`:

```nomi
pub fn user_by_email(conn: sql.Conn, email: String) -> Result<Maybe<User>, sql.Error>
```

The generated files are ordinary Nomi that you commit, so `nomi check`, the
editor and code review read them like any other file. Don't edit them: run
`generate` again after you change a migration or a query. The generated
`schema_test.nomi` fails `nomi test` when you forget.

### Running the shop

Here is the whole shop. The block runs `generate` first, as you would after
an edit, then `main`, then the tests. Add a column in the migration, or a
column to a query's `SELECT`, and run it again: the generated files' tabs
show the change.

```nomi-run manual tool:sqlkit/generate
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
        sqlkit.migrate(db, schema.migrations)?

        user_id = queries.insert_user(db, "ada@example.com", Some("Ada"))?
        order_id = queries.insert_order(db, user_id, 12.50d)?
        changed = queries.set_status(db, order_id, .Paid)?
        io.print("order ${order_id} is paid (${changed.rows} row changed)")

        Some(user) = queries.user_by_email(db, "ada@example.com")? else {
            None -> {
                io.print("no user with that email")
                return Ok(Unit)
            }
        }

        name = user.name |> Maybe.with_default("no name")
        io.print("user ${user.id}: ${user.email} (${name})")

        _ = queries.insert_user(db, "grace@example.com", None)?
        counts = queries.order_counts(db)?

        Iter.each(counts, fn c -> {
            last = c.last_order |> Maybe.map(fn id -> "${id}") |> Maybe.with_default("none")
            io.print("${c.email}: orders ${c.orders}, last order ${last}")
        })

        Ok(Unit)
    })
}

// FILE: shop_test.nomi
import {
    std/sql.{self, ConstraintKind}
    queries
    schema.{self, User}
    sqlite
    sqlkit
}

group "the shop" {
    // Each test gets a fresh in-memory database with the shop's schema.
    setup {
        db = sqlite.memory()?
        defer sql.close_quietly(db)
        sqlkit.migrate(db, schema.migrations)?
        db
    }

    test "a user reads back as its row", db {
        id = queries.insert_user(db, "ada@example.com", Some("Ada"))?

        assert Some(User{id: read_id, email, name}) = queries.user_by_email(db, "ada@example.com")?
        assert read_id == id
        assert email == "ada@example.com"
        assert name == Some("Ada")
        assert queries.user_by_email(db, "nobody@example.com")? == None
    }

    test "a second user with the same email breaks the unique constraint", db {
        _ = queries.insert_user(db, "ada@example.com", None)?

        assert queries.insert_user(db, "ada@example.com", None) == Err(
            sql.Error.Constraint{
                kind: ConstraintKind.Unique,
                name: "users_email_key",
                table: Some("users"),
                column: Some("email"),
            }
        )
    }
}

// FILE: db/queries/queries.sql
-- The shop's queries. `nomi tool sqlkit generate` writes queries.nomi from
-- them, a function for each.

-- query: user_by_email(email: String) -> Maybe<User>
-- The user with this email, if any.
SELECT id, email, name, created_at
FROM users
WHERE email = @email;

-- query: insert_user(email: String, name: Maybe<String>) -> Int
-- Add a user, answering its id.
INSERT INTO users (email, name) VALUES (@email, @name)
RETURNING id;

-- query: insert_order(user_id: Int, total: Decimal) -> Int
-- Place an order for a user.
INSERT INTO orders (user_id, total) VALUES (@user_id, @total)
RETURNING id;

-- query: set_status(id: Int, status: OrderStatus) -> Changed
-- Change an order's status: the number of orders changed.
UPDATE orders SET status = @status WHERE id = @id;

-- query: order_counts()
-- Every user with the number of orders they placed, including those who
-- placed none.
SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id
ORDER BY u.email;

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

// FILE: queries.nomi
// Generated by `nomi tool sqlkit generate` from db/queries/queries.sql and
// db/migrations. Do not edit: change the SQL and generate again.
// inputs sha256: 8ed49783b9e6439b7a1dbb02ea95ad82e0e783915b82658c17129ce227b69c5d
import {
    std/sql
    schema.{OrderStatus, User}
    sqlkit
    sqlkit/runtime
}

/// The user with this email, if any.
pub fn user_by_email(conn: sql.Conn, email: String) -> Result<Maybe<User>, sql.Error> {
    statement = runtime.statement(
        ["SELECT id, email, name, created_at FROM users WHERE email = ", ""],
        [runtime.bind(email)],
    )

    runtime.optional(conn, "user_by_email", statement, fn row -> Ok(User{
            id: runtime.int("user_by_email", row, 0)?,
            email: runtime.text("user_by_email", row, 1)?,
            name: runtime.text_null("user_by_email", row, 2)?,
            created_at: runtime.timestamptz("user_by_email", row, 3)?,
        }))
}

/// Add a user, answering its id.
pub fn insert_user(conn: sql.Conn, email: String, name: Maybe<String>) -> Result<Int, sql.Error> {
    statement = runtime.statement(
        ["INSERT INTO users (email, name) VALUES (", ", ", ") RETURNING id"],
        [runtime.bind(email), runtime.bind(name)],
    )

    runtime.one(conn, "insert_user", statement, fn row -> runtime.int("insert_user", row, 0))
}

/// Place an order for a user.
pub fn insert_order(conn: sql.Conn, user_id: Int, total: Decimal) -> Result<Int, sql.Error> {
    statement = runtime.statement(
        ["INSERT INTO orders (user_id, total) VALUES (", ", ", ") RETURNING id"],
        [runtime.bind(user_id), runtime.bind(total)],
    )

    runtime.one(conn, "insert_order", statement, fn row -> runtime.int("insert_order", row, 0))
}

/// Change an order's status: the number of orders changed.
pub fn set_status(
    conn: sql.Conn,
    id: Int,
    status: OrderStatus,
) -> Result<sqlkit.Changed, sql.Error> {
    statement = runtime.statement(
        ["UPDATE orders SET status = ", " WHERE id = ", ""],
        [runtime.bind(status), runtime.bind(id)],
    )

    runtime.exec(conn, statement)
}

/// Every user with the number of orders they placed, including those who
/// placed none.
pub fn order_counts(conn: sql.Conn) -> Result<List<#{
    email: String,
    last_order: Maybe<Int>,
    orders: Int,
}>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT u.email, max(o.id) AS \"last_order\", count(o.id) AS \"orders\" FROM users u LEFT JOIN orders o ON o.user_id = u.id GROUP BY u.id ORDER BY u.email",
        ],
        [],
    )

    runtime.list(conn, "order_counts", statement, fn row -> Ok(#{
            email: runtime.text<String>("order_counts", row, 0)?,
            last_order: runtime.value_null<Int>("order_counts", row, 1)?,
            orders: runtime.value<Int>("order_counts", row, 2)?,
        }))
}

// FILE: schema.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations. Do not edit:
// change the SQL and generate again.
// inputs sha256: 8ed49783b9e6439b7a1dbb02ea95ad82e0e783915b82658c17129ce227b69c5d
import {
    std/instant.Instant
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
    placed_at: Instant
}

/// A row of the table `users`.
pub struct User {
    id: Int
    email: String
    name: Maybe<String>
    created_at: Instant
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
    sqlkit.Migration{
        path: "db/migrations/20261019120000_users_and_orders.sql",
        sql: "CREATE TABLE users (\n    id          INTEGER PRIMARY KEY,\n    email       TEXT NOT NULL UNIQUE,\n    name        TEXT,\n    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n\nCREATE TABLE orders (\n    id          INTEGER PRIMARY KEY,\n    user_id     INTEGER NOT NULL REFERENCES users (id),\n    status      TEXT NOT NULL DEFAULT 'pending'\n                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),\n    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.\n    total       DECIMAL(12, 2) NOT NULL,\n    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n",
    },
]

// FILE: schema_test.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations and db/queries.
// Do not edit: change the SQL and generate again.
// inputs sha256: 8ed49783b9e6439b7a1dbb02ea95ad82e0e783915b82658c17129ce227b69c5d
import sqlkit/inputs

// Fails when a migration or a query changed after the last
// `nomi tool sqlkit generate`: run it again. The paths are under the
// module root, where `nomi test` resolves a test's relative paths.
test "generated sql is current" {
    assert inputs.current("db/migrations", "db/queries")
        == Ok("8ed49783b9e6439b7a1dbb02ea95ad82e0e783915b82658c17129ce227b69c5d")
}

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
main: up to date
order 1 is paid (1 row changed)
user 1: ada@example.com (Ada)
ada@example.com: orders 1, last order 1
grace@example.com: orders 0, last order none
ok the shop / a user reads back as its row
ok the shop / a second user with the same email breaks the unique constraint
ok generated sql is current
test result: ok. 3 passed, 0 failed
-->

- `sqlkit.migrate(db, schema.migrations)` runs each migration the database
  has not run yet, in order, and records it. The same call builds a new
  database from nothing and brings an existing file up to date.
- Each query is an ordinary function call that the compiler has checked
  against the generated code. `set_status` takes an `OrderStatus`, so only
  the four allowed values reach the column. The `Some(user) = ... else`
  line is [binding with `else`](/pattern-matching/#binding-with-else).
- `order_counts` uses a `LEFT JOIN`, which keeps a user with no orders and
  fills the order's columns with `NULL`. A computed column has no SQL type,
  so its name says what it is: `AS "last_order?: Int"` may be `NULL` and is
  a `Maybe<Int>`, and `AS "orders!: Int"` never is. Grace placed no orders,
  so her `last_order` is `None`.
- The test group's [`setup`](/testing/#groups-and-setup) gives each test a
  fresh in-memory database with the shop's schema. The second test breaks
  the `UNIQUE` on `email`, and the constraint gets its Postgres name,
  `users_email_key`.

A query function takes any `Conn`, so it runs inside a
[transaction](#transactions) as a direct statement does.

<div class="tour-details">
<p class="tour-details-label">What sqlkit checks, and more</p>

The [sqlkit manual](https://github.com/nomi-language/nomi/tree/main/lib/sql/sqlkit#readme)
covers what the chapter leaves out:

- every result type and the rows each expects, and the `!`, `?` and `: T`
  column marks;
- what `generate` checks and the errors it reports, at the SQL's file, line
  and column, before it writes anything;
- every SQL type and the Nomi type it reads as, and how a `DECIMAL` keeps
  its scale;
- testing with the schema, and keeping the connection in an app field in a
  program that [boots](/app-fields/);
- other layouts and several databases in `nomi.toml`.

[`lib/sql/examples/shop`](https://github.com/nomi-language/nomi/tree/main/lib/sql/examples/shop)
is a larger shop with more queries, transactions over queries and eleven
tests.

</div>
