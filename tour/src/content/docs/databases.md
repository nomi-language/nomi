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
  or the first error, as in [Lines](/input-and-output/#lines).

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
optional filters. Build one from pieces:

- `+` joins two statements, SQL and parameters together.
- `Statement.values(items)` writes parameters for SQL's `IN`: `(?1, ?2)`.
- `Statement.list(statements)` writes statements in parentheses, separated
  by commas: `(a, b)`, such as one row of a multi-row `INSERT`.
- `Statement.all(conditions)` joins conditions by `AND`, and
  `Statement.any(conditions)` by `OR`. With several conditions, each is
  parenthesized, so an `any` can sit inside an `all`.
- `Statement.where_all(conditions)` writes `WHERE` and `all` of the
  conditions, or nothing when there are none. `where_any` does the same
  with `any`.
- `Statement.join(statements, separator)` puts a separator between
  statements, for anything else.

Each takes a list or any other `Iter`, so a pipe such as
`ids |> Iter.map(fn id -> Sql"${id}") |> Statement.join(Sql", ")` needs no
`Iter.to_list()`.

Each piece is still a `Sql"..."` literal, so a value only ever goes in a
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
fn orders_query(
    statuses: List<String>,
    min_total: Maybe<Decimal>,
    search: Maybe<String>,
    sort: Sort,
) -> Statement {
    by_status = if Iter.is_empty(statuses) {
        None
    } else {
        Some(Sql"status IN " + Statement.values(statuses))
    }

    by_total = min_total |> Maybe.map(fn t -> Sql"total >= ${t}")

    // The search text may match the customer's name or their email.
    by_search =
        search
        |> Maybe.map(fn text -> {
            pattern = "%${text}%"
            Statement.any([Sql"customer LIKE ${pattern}", Sql"email LIKE ${pattern}"])
        })

    order = case sort {
        .Newest -> Sql" ORDER BY placed_at DESC"
        .Largest -> Sql" ORDER BY total DESC"
    }

    filters = Maybe.values([by_status, by_total, by_search])
    Sql"SELECT id FROM orders" + Statement.where_all(filters) + order
}

fn main() {
    dbg "${orders_query([], None, None, .Newest)}"
    dbg "${orders_query(["paid", "shipped"], Some(20d), None, .Largest)}"
    dbg "${orders_query(["paid"], None, Some("ada"), .Newest)}"
    dbg orders_query(["paid"], None, Some("ada"), .Newest).params
    Unit
}
```
<!-- expect
dbg line 42: "${orders_query([], None, None, .Newest)}" = "SELECT id FROM orders ORDER BY placed_at DESC"
dbg line 43: "${orders_query(["paid", "shipped"], Some(20d), None, .Largest)}" = "SELECT id FROM orders WHERE (status IN (?1, ?2)) AND (total >= ?3) ORDER BY total DESC"
dbg line 44: "${orders_query(["paid"], None, Some("ada"), .Newest)}" = "SELECT id FROM orders WHERE (status IN (?1)) AND ((customer LIKE ?2) OR (email LIKE ?3)) ORDER BY placed_at DESC"
dbg line 45: orders_query(["paid"], None, Some("ada"), .Newest).params = [Text("paid"), Text("%ada%"), Text("%ada%")]
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
dbg line 34: transfer(db, "ada", "bob", 30) = Err(#Constraint{kind: Check, name: "accounts_balance_check", table: Some("accounts"), column: Some("balance")})
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
end at Ada 20 and Bob 30.

A rule the schema leaves unnamed gets the name Postgres would give it, as
`accounts_balance_check` here, so a rule has the same name on either
database. A program can tell one rule's failure from another by its name.

A transaction is a `Conn` too, so anything that takes a connection runs
inside one. `sql.transaction` on a transaction starts a savepoint inside it,
so a failure there undoes only the inner part.

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
    _ = Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${body})")?
    Ok(Unit)
}

test "a note reads back from a fresh database" {
    db = sqlite.memory()?
    defer sql.close_quietly(db)
    _ = Conn.exec(db, Sql"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")?

    add_note(db, "buy milk")?

    rows = Conn.query(db, Sql"SELECT body FROM notes")?
    bodies = rows |> Iter.map(fn row -> Row.get<String>(row, "body")) |> Iter.to_list()
    assert bodies == [Ok("buy milk")]
}

test "a note is one insert, with the body as a parameter" {
    fake = Fake.new()

    add_note(fake, "buy milk")?

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
column read as the wrong type, or a `NULL` where you expected a value is an
`Err` at run time. sqlkit finds those mistakes before the program runs. Add
it beside the driver:

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
│   └── queries/
│       └── queries.sql
├── schema.nomi
├── schema_test.nomi
├── queries.nomi
├── shop.nomi
├── main.nomi
└── shop_test.nomi
```

The migrations describe the tables, and `queries.sql` holds the queries.
sqlkit's `generate` tool writes `schema.nomi`, `queries.nomi` and
`schema_test.nomi` from them. `shop.nomi` holds operations built from the
queries. The examples below are these files, in a file tree beside the
editor. Each one runs `generate` first, as you would after an edit, and
then runs the program.

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

### Generating the code

The compiler never connects to a database, so `nomi check` and the editor
need no running database or password. Instead, sqlkit's `generate` tool
asks SQLite about the schema and the queries once, and writes what it
learns as Nomi code:

```console
$ nomi tool sqlkit generate
main: wrote schema.nomi, schema_test.nomi, queries.nomi
```

`generate` applies the migrations to an empty SQLite database in memory,
and asks SQLite to describe each query: its columns, their types, and
whether each may be `NULL`. `main` in its output is the database's name.
The migrations and queries are where the tree above shows them, and the
[sqlkit README](https://github.com/nomi-language/nomi/tree/main/lib/sql/sqlkit#readme)
covers other layouts.

The generated files are ordinary Nomi that you commit, so `nomi check`,
the editor and code review read them like any other file. Don't edit them:
run `generate` again after you change a migration or a query. From the
migrations, `generate` writes `schema.nomi`:

- a struct for each table, named after one of its rows: the table `users`
  holds `User` rows, and `orders` holds `Order` rows;
- an enum for each column that a `CHECK (column IN (...))` limits;
- `migrations`, the migration files as a list, for `sqlkit.migrate`.

The shop's begins:

```nomi
// Generated by `nomi tool sqlkit generate` from db/migrations. Do not edit:
// change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/calendar.Date
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
    shipped_on: Maybe<Date>
}

/// A row of the table `users`.
pub struct User {
    id: Int
    email: String
    name: Maybe<String>
    created_at: Instant
}
```

Each column's SQL type picks its field's Nomi type. `INTEGER` is `Int`,
`TEXT` is `String`, `DATE` is a calendar `Date`, `TIMESTAMPTZ` (a moment in
time) is an `Instant`, and `DECIMAL` is a `Decimal`. A nullable column is a
`Maybe`. The sqlkit README lists every SQL type it reads. SQLite keeps a
`DECIMAL` as a 64-bit float, so the driver reads `total` back at its
declared scale, `12.50d`, and refuses a value it could not keep exactly.

Each generated file starts with a header that names what it was made from
and records a digest of the migrations and queries. `generate` also writes
`schema_test.nomi`, a test that computes the digest again and fails when a
migration or a query changed after the last `generate`, so `nomi test`
catches a forgotten run:

```console
$ nomi test .
FAIL schema_test.nomi :: generated sql is current
```

In an automated check, `nomi tool sqlkit generate --check` writes nothing
and fails when a generated file is not what `generate` would write.

### Queries

`db/queries/queries.sql` holds the shop's queries. Each starts with an
annotation that gives it a Nomi name and signature, then its doc comment,
then its SQL, ending in `;`. The first:

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
query stays SQL you could paste into a database console. `generate` writes
this function for it into `queries.nomi`:

```nomi
/// The user with this email, if any.
pub fn user_by_email(conn: sql.Conn, email: String) -> Result<Maybe<User>, sql.Error> {
    statement = runtime.statement(
        ["SELECT id, email, name, created_at FROM users WHERE email = ", ""],
        [runtime.bind(email)],
    )

    runtime.optional(conn, "user_by_email", statement, fn row -> Ok(#User{
            id: runtime.int("user_by_email", row, 0)?,
            email: runtime.text("user_by_email", row, 1)?,
            name: runtime.text_null("user_by_email", row, 2)?,
            created_at: runtime.timestamptz("user_by_email", row, 3)?,
        }))
}
```

Every query function takes the connection as its first argument, `conn`, and
wraps the result type in a `Result`. `generate` imports what the signatures
name: a row struct or enum from `schema.nomi`, and std's types such as
`Instant`. A type of your own is named by an `-- import:` line at the top of
the file; the shop's revenue report reads rows of `shop.nomi`'s
`CustomerRevenue` with `-- import: shop.CustomerRevenue`.

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

```sql
-- query: insert_user(email: String, name: Maybe<String>) -> Int
-- Add a user, answering its id.
INSERT INTO users (email, name) VALUES (@email, @name)
RETURNING id;

-- query: set_status(id: Int, status: OrderStatus) -> Changed
-- Change an order's status: the number of orders changed.
UPDATE orders SET status = @status WHERE id = @id;
```

`RETURNING id` makes the `INSERT` return the new row's id. Passing `None`
for `name` stores `NULL`. `set_status` takes an `OrderStatus`, so only the
four allowed values reach the column.

The count is a type of its own so that it can't be mistaken for a column,
as [When generate finds a mistake](#when-generate-finds-a-mistake) shows.

### Calling the queries

This is the whole shop. Its `main.nomi` builds the schema in a fresh
in-memory database, adds a user, places an order with `insert_order` (a
query like `insert_user`), marks it paid and reads the user back. Then it
adds a second user and prints how many orders each one placed. Add a
column in a new migration, or a column to a query's `SELECT`, and run it
again: `generate` rewrites the generated files, and their tabs show the
change. The block runs the generated test too.

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

// FILE: db/queries/queries.sql
-- The shop's queries. `nomi tool sqlkit generate` writes queries.nomi from
-- them, a function for each.
-- import: shop.CustomerRevenue

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

-- query: order(id: Int) -> Order
-- One order.
SELECT id, user_id, status, total, placed_at, shipped_on
FROM orders
WHERE id = @id;

-- query: orders_for_user(user_id: Int, limit: Int)
-- A user's orders, newest first, each with the user's email.
SELECT o.id, o.status, o.total, o.placed_at, u.email
FROM orders o
JOIN users u ON u.id = o.user_id
WHERE o.user_id = @user_id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @limit;

-- query: order_counts()
-- Every user with the number of orders they placed, including those who
-- placed none.
SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id
ORDER BY u.email;

-- query: revenue_since(since: Instant) -> List<CustomerRevenue>
-- Revenue per customer from paid and shipped orders since a moment. SQLite
-- declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
-- reads back at scale 2.
SELECT u.id AS user_id, u.email,
       count(*) AS "order_count!",
       CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
FROM users u
JOIN orders o ON o.user_id = u.id
WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
GROUP BY u.id, u.email
ORDER BY revenue DESC;

-- query: all_orders() -> Rows<Order>
-- Every order, streamed.
SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id;

// FILE: queries.nomi
// Generated by `nomi tool sqlkit generate` from db/queries/queries.sql and
// db/migrations. Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/instant.Instant
    std/sql
    schema.{Order, OrderStatus, User}
    shop.CustomerRevenue
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

/// One order.
pub fn order(conn: sql.Conn, id: Int) -> Result<Order, sql.Error> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ", ""],
        [runtime.bind(id)],
    )

    runtime.one(conn, "order", statement, fn row -> Ok(#Order{
            id: runtime.int("order", row, 0)?,
            user_id: runtime.int("order", row, 1)?,
            status: runtime.text("order", row, 2)?,
            total: runtime.numeric_scaled("order", row, 3, 2)?,
            placed_at: runtime.timestamptz("order", row, 4)?,
            shipped_on: runtime.date_null("order", row, 5)?,
        }))
}

/// A user's orders, newest first, each with the user's email.
pub fn orders_for_user(conn: sql.Conn, user_id: Int, limit: Int) -> Result<List<#{
    id: Int,
    status: OrderStatus,
    total: Decimal,
    placed_at: Instant,
    email: String,
}>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ",
            " ORDER BY o.placed_at DESC, o.id DESC LIMIT ",
            "",
        ],
        [runtime.bind(user_id), runtime.bind(limit)],
    )

    runtime.list(conn, "orders_for_user", statement, fn row -> Ok(#{
            id: runtime.int<Int>("orders_for_user", row, 0)?,
            status: runtime.text<OrderStatus>("orders_for_user", row, 1)?,
            total: runtime.numeric_scaled<Decimal>("orders_for_user", row, 2, 2)?,
            placed_at: runtime.timestamptz<Instant>("orders_for_user", row, 3)?,
            email: runtime.text<String>("orders_for_user", row, 4)?,
        }))
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

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub fn revenue_since(conn: sql.Conn, since: Instant) -> Result<List<CustomerRevenue>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ",
            " GROUP BY u.id, u.email ORDER BY revenue DESC",
        ],
        [runtime.bind(since)],
    )

    runtime.list(conn, "revenue_since", statement, fn row -> Ok(#CustomerRevenue{
            user_id: runtime.int("revenue_since", row, 0)?,
            email: runtime.text("revenue_since", row, 1)?,
            order_count: runtime.value("revenue_since", row, 2)?,
            revenue: runtime.numeric_scaled("revenue_since", row, 3, 2)?,
        }))
}

/// Every order, streamed.
pub fn all_orders(conn: sql.Conn) -> sqlkit.Rows<Order> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id"],
        [],
    )

    runtime.rows(conn, "all_orders", statement, fn row -> Ok(#Order{
            id: runtime.int("all_orders", row, 0)?,
            user_id: runtime.int("all_orders", row, 1)?,
            status: runtime.text("all_orders", row, 2)?,
            total: runtime.numeric_scaled("all_orders", row, 3, 2)?,
            placed_at: runtime.timestamptz("all_orders", row, 4)?,
            shipped_on: runtime.date_null("all_orders", row, 5)?,
        }))
}

// FILE: schema.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations. Do not edit:
// change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/calendar.Date
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
    shipped_on: Maybe<Date>
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
    #sqlkit.Migration{
        path: "db/migrations/20261019120000_users_and_orders.sql",
        sql: "CREATE TABLE users (\n    id          INTEGER PRIMARY KEY,\n    email       TEXT NOT NULL UNIQUE,\n    name        TEXT,\n    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n\nCREATE TABLE orders (\n    id          INTEGER PRIMARY KEY,\n    user_id     INTEGER NOT NULL REFERENCES users (id),\n    status      TEXT NOT NULL DEFAULT 'pending'\n                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),\n    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.\n    total       DECIMAL(12, 2) NOT NULL,\n    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n",
    },
    #sqlkit.Migration{
        path: "db/migrations/20261102090000_orders_shipped_on.sql",
        sql: "ALTER TABLE orders ADD COLUMN shipped_on DATE;\n",
    },
]

// FILE: schema_test.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations and db/queries.
// Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import sqlkit/inputs

// Fails when a migration or a query changed after the last
// `nomi tool sqlkit generate`: run it again. The paths are under the
// module root, where `nomi test` resolves a test's relative paths.
test "generated sql is current" {
    assert inputs.current("db/migrations", "db/queries")
        == Ok("4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e")
}

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
        user_id = queries.insert_user(tx, email, None)?
        queries.insert_order(tx, user_id, total)
    })
}

/// Place an order and mark it paid, in one transaction.
pub fn place_paid_order(conn: Conn, user_id: Int, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        order_id = queries.insert_order(tx, user_id, total)?
        _ = queries.set_status(tx, order_id, .Paid)?
        Ok(order_id)
    })
}

/// The total of every order, read one row at a time.
pub fn total_of_all_orders(conn: Conn) -> Result<Decimal, Error> {
    queries.all_orders(conn) |> Rows.fold(0d, fn sum, order -> sum + order.total)
}

/// A customer's revenue: a row of `queries.revenue_since`.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

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
ok generated sql is current
test result: ok. 1 passed, 0 failed
-->

`sqlkit.migrate(db, schema.migrations)` runs each migration the database has
not run yet, in order, and records it in a table, `nomi_migrations`. So the
same call builds a new database from nothing and brings an existing file up
to date. If a migration's text changed after it ran, `migrate` runs nothing
and names the file: write the change as a new migration instead.

From there, each call is an ordinary function call that the compiler has
checked against the generated code. The `Some(user) = ... else` line is
[binding with `else`](/pattern-matching/#binding-with-else): it handles the
`None` case on the spot. A caller that does not need a count discards it:
`_ = queries.set_status(db, id, .Paid)?`.

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
`order_counts`, which makes one row per user with `GROUP BY u.id`:

```sql
-- query: order_counts()
-- Every user with the number of orders they placed, including those who
-- placed none.
SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id
ORDER BY u.email;
```

A column computed by an expression, such as `count(o.id)`, has no SQL type,
and sqlkit can't tell whether it is ever `NULL`. You say so in its name,
after `AS`, inside double quotes:

- `AS "n!"`: never `NULL`, so not a `Maybe`. A count is never `NULL`.
- `AS "n?"`: may be `NULL`, so a `Maybe`. `max` over a user with no orders
  is `NULL`.
- `AS "n: Int"`: the column's Nomi type. A query with no result type needs
  it for a computed column. A query that reads into a struct of yours takes
  the type from the struct's field.

sqlkit takes the marks out before the SQL is sent, so the columns are
called `last_order` and `orders`. The query has no result type, so each row
is an anonymous struct with the fields `email: String`,
`last_order: Maybe<Int>` and `orders: Int`. Grace placed no orders, so her
`last_order` is `None`, which `main` prints as `none`.

A plain column needs no mark. `generate` asks SQLite which tables a join
may leave out, so a column from the left-out side of a `LEFT JOIN` is a
`Maybe`, even when it is `NOT NULL` in its own table. `orders_for_user` in
`queries.sql` is a plain `JOIN`, so its `u.email` is a `String`.

### When generate finds a mistake

`generate` checks every query before it writes anything. It reports each
problem at its file, line and column, and when there is one it writes
nothing. Here `user_by_email` says `@mail` where it means `@email`:

```nomi-run manual error tool:sqlkit/generate
// FILE: db/queries/queries.sql
-- The shop's queries. `nomi tool sqlkit generate` writes queries.nomi from
-- them, a function for each.
-- import: shop.CustomerRevenue

-- query: user_by_email(email: String) -> Maybe<User>
-- The user with this email, if any.
SELECT id, email, name, created_at
FROM users
WHERE email = @mail;

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

-- query: order(id: Int) -> Order
-- One order.
SELECT id, user_id, status, total, placed_at, shipped_on
FROM orders
WHERE id = @id;

-- query: orders_for_user(user_id: Int, limit: Int)
-- A user's orders, newest first, each with the user's email.
SELECT o.id, o.status, o.total, o.placed_at, u.email
FROM orders o
JOIN users u ON u.id = o.user_id
WHERE o.user_id = @user_id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @limit;

-- query: order_counts()
-- Every user with the number of orders they placed, including those who
-- placed none.
SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id
ORDER BY u.email;

-- query: revenue_since(since: Instant) -> List<CustomerRevenue>
-- Revenue per customer from paid and shipped orders since a moment. SQLite
-- declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
-- reads back at scale 2.
SELECT u.id AS user_id, u.email,
       count(*) AS "order_count!",
       CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
FROM users u
JOIN orders o ON o.user_id = u.id
WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
GROUP BY u.id, u.email
ORDER BY revenue DESC;

-- query: all_orders() -> Rows<Order>
-- Every order, streamed.
SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id;

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

// FILE: queries.nomi
// Generated by `nomi tool sqlkit generate` from db/queries/queries.sql and
// db/migrations. Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/instant.Instant
    std/sql
    schema.{Order, OrderStatus, User}
    shop.CustomerRevenue
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

/// One order.
pub fn order(conn: sql.Conn, id: Int) -> Result<Order, sql.Error> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ", ""],
        [runtime.bind(id)],
    )

    runtime.one(conn, "order", statement, fn row -> Ok(#Order{
            id: runtime.int("order", row, 0)?,
            user_id: runtime.int("order", row, 1)?,
            status: runtime.text("order", row, 2)?,
            total: runtime.numeric_scaled("order", row, 3, 2)?,
            placed_at: runtime.timestamptz("order", row, 4)?,
            shipped_on: runtime.date_null("order", row, 5)?,
        }))
}

/// A user's orders, newest first, each with the user's email.
pub fn orders_for_user(conn: sql.Conn, user_id: Int, limit: Int) -> Result<List<#{
    id: Int,
    status: OrderStatus,
    total: Decimal,
    placed_at: Instant,
    email: String,
}>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ",
            " ORDER BY o.placed_at DESC, o.id DESC LIMIT ",
            "",
        ],
        [runtime.bind(user_id), runtime.bind(limit)],
    )

    runtime.list(conn, "orders_for_user", statement, fn row -> Ok(#{
            id: runtime.int<Int>("orders_for_user", row, 0)?,
            status: runtime.text<OrderStatus>("orders_for_user", row, 1)?,
            total: runtime.numeric_scaled<Decimal>("orders_for_user", row, 2, 2)?,
            placed_at: runtime.timestamptz<Instant>("orders_for_user", row, 3)?,
            email: runtime.text<String>("orders_for_user", row, 4)?,
        }))
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

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub fn revenue_since(conn: sql.Conn, since: Instant) -> Result<List<CustomerRevenue>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ",
            " GROUP BY u.id, u.email ORDER BY revenue DESC",
        ],
        [runtime.bind(since)],
    )

    runtime.list(conn, "revenue_since", statement, fn row -> Ok(#CustomerRevenue{
            user_id: runtime.int("revenue_since", row, 0)?,
            email: runtime.text("revenue_since", row, 1)?,
            order_count: runtime.value("revenue_since", row, 2)?,
            revenue: runtime.numeric_scaled("revenue_since", row, 3, 2)?,
        }))
}

/// Every order, streamed.
pub fn all_orders(conn: sql.Conn) -> sqlkit.Rows<Order> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id"],
        [],
    )

    runtime.rows(conn, "all_orders", statement, fn row -> Ok(#Order{
            id: runtime.int("all_orders", row, 0)?,
            user_id: runtime.int("all_orders", row, 1)?,
            status: runtime.text("all_orders", row, 2)?,
            total: runtime.numeric_scaled("all_orders", row, 3, 2)?,
            placed_at: runtime.timestamptz("all_orders", row, 4)?,
            shipped_on: runtime.date_null("all_orders", row, 5)?,
        }))
}

// FILE: schema.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations. Do not edit:
// change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/calendar.Date
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
    shipped_on: Maybe<Date>
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
    #sqlkit.Migration{
        path: "db/migrations/20261019120000_users_and_orders.sql",
        sql: "CREATE TABLE users (\n    id          INTEGER PRIMARY KEY,\n    email       TEXT NOT NULL UNIQUE,\n    name        TEXT,\n    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n\nCREATE TABLE orders (\n    id          INTEGER PRIMARY KEY,\n    user_id     INTEGER NOT NULL REFERENCES users (id),\n    status      TEXT NOT NULL DEFAULT 'pending'\n                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),\n    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.\n    total       DECIMAL(12, 2) NOT NULL,\n    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n",
    },
    #sqlkit.Migration{
        path: "db/migrations/20261102090000_orders_shipped_on.sql",
        sql: "ALTER TABLE orders ADD COLUMN shipped_on DATE;\n",
    },
]

// FILE: schema_test.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations and db/queries.
// Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import sqlkit/inputs

// Fails when a migration or a query changed after the last
// `nomi tool sqlkit generate`: run it again. The paths are under the
// module root, where `nomi test` resolves a test's relative paths.
test "generated sql is current" {
    assert inputs.current("db/migrations", "db/queries")
        == Ok("4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e")
}

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
        user_id = queries.insert_user(tx, email, None)?
        queries.insert_order(tx, user_id, total)
    })
}

/// Place an order and mark it paid, in one transaction.
pub fn place_paid_order(conn: Conn, user_id: Int, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        order_id = queries.insert_order(tx, user_id, total)?
        _ = queries.set_status(tx, order_id, .Paid)?
        Ok(order_id)
    })
}

/// The total of every order, read one row at a time.
pub fn total_of_all_orders(conn: Conn) -> Result<Decimal, Error> {
    queries.all_orders(conn) |> Rows.fold(0d, fn sum, order -> sum + order.total)
}

/// A customer's revenue: a row of `queries.revenue_since`.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

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

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
db/queries/queries.sql:9:15: `mail` is not a parameter of `user_by_email`
db/queries/queries.sql:9:15: help: its parameters are `email`
db/queries/queries.sql:5:25: the parameter `email` is not used in the SQL of `user_by_email`
db/queries/queries.sql:5:25: help: write `@email` where it is bound, or remove it
error: 2 problems in the SQL; nothing was written
-->

Every `@name` must name a parameter of the query, and every parameter must
be used, so this one mistake gives two errors. Change `@mail` back to
`@email` and both go away. An `@` inside a string, such as
`'ada@example.com'`, or one not followed by a letter or `_`, is SQL, not a
parameter.

With `@email` back, try these mistakes in the same editor:

- Remove the `RETURNING id` line from `insert_user`. The `INSERT` then
  returns no row, so there is no id to answer, and `generate` reports it at
  the result type:

  ```text
  db/queries/queries.sql:11:62: `insert_user` returns no rows, so it cannot answer an `Int`; write `-> Changed` for the number of rows it changed, or `-> Unit`
  ```

  This is why a count is the type `Changed` and not an `Int`: without the
  error, `insert_user` would quietly answer the number of rows inserted
  where it answered an id.
- Misspell a column, such as `emial` for `email` in the `SELECT` of
  `user_by_email`. `generate` reports SQLite's own error at the SQL it is
  about:

  ```text
  db/queries/queries.sql:7:12: no such column: emial
  db/queries/queries.sql:7:12: help: reported by SQLite for `user_by_email`
  ```

Some mistakes get past `generate`: a field of the wrong type, or a nullable
column read into a field that is not a `Maybe`. `nomi check` and the editor
report those in the generated `queries.nomi`, at the line that reads the
column. The editor reads the generated code, not the SQL, so it offers no
hover or completion inside a `.sql` file unless you set up a SQL language
server for it.

### In an application

The shop's `main` opens a database for one run. A program that serves
requests opens it once, in its [`boot`](/capabilities-and-context/), and
keeps the connection in an app field, so every function reads it as
`App.db`. This `main.nomi` takes the place of the shop's. Its `main` signs
up two users through `sign_up` and reads them back with `order_counts`, and
`main_test.nomi` tests `sign_up` under the same `boot`:

```nomi-run manual tool:sqlkit/generate
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
    db = sqlite.open(path)?
    defer sql.close_quietly(db)
    sqlkit.migrate(db, schema.migrations)?
    Ok(#App{db, context: Context.root()})
}

pub fn sign_up(email: String) -> Result<Int, Error> {
    queries.insert_user(App.db, email, None)
}

fn main() -> Result<Unit, Error> {
    _ = sign_up("ada@example.com")?
    _ = sign_up("grace@example.com")?
    users = queries.order_counts(App.db)?
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
        id = main.sign_up("ada@example.com")?
        user = queries.user_by_email(App.db, "ada@example.com")?
        assert user |> Maybe.map(fn u -> u.id) == Some(id)
    }

    test "the same email twice breaks the unique constraint" {
        _ = main.sign_up("ada@example.com")?

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

// FILE: db/queries/queries.sql
-- The shop's queries. `nomi tool sqlkit generate` writes queries.nomi from
-- them, a function for each.
-- import: shop.CustomerRevenue

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

-- query: order(id: Int) -> Order
-- One order.
SELECT id, user_id, status, total, placed_at, shipped_on
FROM orders
WHERE id = @id;

-- query: orders_for_user(user_id: Int, limit: Int)
-- A user's orders, newest first, each with the user's email.
SELECT o.id, o.status, o.total, o.placed_at, u.email
FROM orders o
JOIN users u ON u.id = o.user_id
WHERE o.user_id = @user_id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @limit;

-- query: order_counts()
-- Every user with the number of orders they placed, including those who
-- placed none.
SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id
ORDER BY u.email;

-- query: revenue_since(since: Instant) -> List<CustomerRevenue>
-- Revenue per customer from paid and shipped orders since a moment. SQLite
-- declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
-- reads back at scale 2.
SELECT u.id AS user_id, u.email,
       count(*) AS "order_count!",
       CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
FROM users u
JOIN orders o ON o.user_id = u.id
WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
GROUP BY u.id, u.email
ORDER BY revenue DESC;

-- query: all_orders() -> Rows<Order>
-- Every order, streamed.
SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id;

// FILE: queries.nomi
// Generated by `nomi tool sqlkit generate` from db/queries/queries.sql and
// db/migrations. Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/instant.Instant
    std/sql
    schema.{Order, OrderStatus, User}
    shop.CustomerRevenue
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

/// One order.
pub fn order(conn: sql.Conn, id: Int) -> Result<Order, sql.Error> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ", ""],
        [runtime.bind(id)],
    )

    runtime.one(conn, "order", statement, fn row -> Ok(#Order{
            id: runtime.int("order", row, 0)?,
            user_id: runtime.int("order", row, 1)?,
            status: runtime.text("order", row, 2)?,
            total: runtime.numeric_scaled("order", row, 3, 2)?,
            placed_at: runtime.timestamptz("order", row, 4)?,
            shipped_on: runtime.date_null("order", row, 5)?,
        }))
}

/// A user's orders, newest first, each with the user's email.
pub fn orders_for_user(conn: sql.Conn, user_id: Int, limit: Int) -> Result<List<#{
    id: Int,
    status: OrderStatus,
    total: Decimal,
    placed_at: Instant,
    email: String,
}>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ",
            " ORDER BY o.placed_at DESC, o.id DESC LIMIT ",
            "",
        ],
        [runtime.bind(user_id), runtime.bind(limit)],
    )

    runtime.list(conn, "orders_for_user", statement, fn row -> Ok(#{
            id: runtime.int<Int>("orders_for_user", row, 0)?,
            status: runtime.text<OrderStatus>("orders_for_user", row, 1)?,
            total: runtime.numeric_scaled<Decimal>("orders_for_user", row, 2, 2)?,
            placed_at: runtime.timestamptz<Instant>("orders_for_user", row, 3)?,
            email: runtime.text<String>("orders_for_user", row, 4)?,
        }))
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

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub fn revenue_since(conn: sql.Conn, since: Instant) -> Result<List<CustomerRevenue>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ",
            " GROUP BY u.id, u.email ORDER BY revenue DESC",
        ],
        [runtime.bind(since)],
    )

    runtime.list(conn, "revenue_since", statement, fn row -> Ok(#CustomerRevenue{
            user_id: runtime.int("revenue_since", row, 0)?,
            email: runtime.text("revenue_since", row, 1)?,
            order_count: runtime.value("revenue_since", row, 2)?,
            revenue: runtime.numeric_scaled("revenue_since", row, 3, 2)?,
        }))
}

/// Every order, streamed.
pub fn all_orders(conn: sql.Conn) -> sqlkit.Rows<Order> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id"],
        [],
    )

    runtime.rows(conn, "all_orders", statement, fn row -> Ok(#Order{
            id: runtime.int("all_orders", row, 0)?,
            user_id: runtime.int("all_orders", row, 1)?,
            status: runtime.text("all_orders", row, 2)?,
            total: runtime.numeric_scaled("all_orders", row, 3, 2)?,
            placed_at: runtime.timestamptz("all_orders", row, 4)?,
            shipped_on: runtime.date_null("all_orders", row, 5)?,
        }))
}

// FILE: schema.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations. Do not edit:
// change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/calendar.Date
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
    shipped_on: Maybe<Date>
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
    #sqlkit.Migration{
        path: "db/migrations/20261019120000_users_and_orders.sql",
        sql: "CREATE TABLE users (\n    id          INTEGER PRIMARY KEY,\n    email       TEXT NOT NULL UNIQUE,\n    name        TEXT,\n    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n\nCREATE TABLE orders (\n    id          INTEGER PRIMARY KEY,\n    user_id     INTEGER NOT NULL REFERENCES users (id),\n    status      TEXT NOT NULL DEFAULT 'pending'\n                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),\n    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.\n    total       DECIMAL(12, 2) NOT NULL,\n    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n",
    },
    #sqlkit.Migration{
        path: "db/migrations/20261102090000_orders_shipped_on.sql",
        sql: "ALTER TABLE orders ADD COLUMN shipped_on DATE;\n",
    },
]

// FILE: schema_test.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations and db/queries.
// Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import sqlkit/inputs

// Fails when a migration or a query changed after the last
// `nomi tool sqlkit generate`: run it again. The paths are under the
// module root, where `nomi test` resolves a test's relative paths.
test "generated sql is current" {
    assert inputs.current("db/migrations", "db/queries")
        == Ok("4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e")
}

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
        user_id = queries.insert_user(tx, email, None)?
        queries.insert_order(tx, user_id, total)
    })
}

/// Place an order and mark it paid, in one transaction.
pub fn place_paid_order(conn: Conn, user_id: Int, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        order_id = queries.insert_order(tx, user_id, total)?
        _ = queries.set_status(tx, order_id, .Paid)?
        Ok(order_id)
    })
}

/// The total of every order, read one row at a time.
pub fn total_of_all_orders(conn: Conn) -> Result<Decimal, Error> {
    queries.all_orders(conn) |> Rows.fold(0d, fn sum, order -> sum + order.total)
}

/// A customer's revenue: a row of `queries.revenue_since`.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

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

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
main: up to date
ada@example.com: 0 orders
grace@example.com: 0 orders
ok sign-up / a new user reads back by email
ok sign-up / the same email twice breaks the unique constraint
ok generated sql is current
test result: ok. 3 passed, 0 failed
-->

`boot` opens the database the `SHOP_DATABASE` variable names, such as
`shop.db`, and a fresh in-memory one when it is not set. The tour sets no
variables, so this run's database is in memory.

`boot` returns a `Result`, so each step that can fail ends in `?`. If the file
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
schema. This example runs `generate`, then the shop's eleven tests and the
generated one:

```nomi-run manual tool:sqlkit/generate
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
        db = sqlite.memory()?
        defer sql.close_quietly(db)
        sqlkit.migrate(db, schema.migrations)?
        db
    }

    test "a user reads back as its row", db {
        id = queries.insert_user(db, "ada@example.com", Some("Ada"))?

        Some(user) = queries.user_by_email(db, "ada@example.com")? else {
            None -> return Err(#sql.Error.Database{code: "", message: "no user"})
        }

        assert #User{id: read_id, email, name} = user
        assert read_id == id
        assert email == "ada@example.com"
        assert name == Some("Ada")
        assert queries.user_by_email(db, "nobody@example.com")? == None
    }

    test "a second user with the same email breaks the unique constraint", db {
        _ = queries.insert_user(db, "ada@example.com", None)?

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
        user = queries.insert_user(db, "ada@example.com", None)?
        id = shop.place_paid_order(db, user, 12.50d)?
        order = queries.order(db, id)?
        assert #Order{status, total, shipped_on} = order
        assert status == OrderStatus.Paid
        // `==` on Decimal ignores the scale; the text shows it.
        assert "${total}" == "12.50"
        assert shipped_on == None

        assert queries.order(db, id + 1)
            == Err(#sql.Error.RowCount{query: "order", expected: "exactly one", found: 0})
    }

    test "a failed sign-up leaves nothing behind", db {
        _ = shop.sign_up_with_order(db, "ada@example.com", 5d)?
        failed = shop.sign_up_with_order(db, "ada@example.com", 7d)
        assert Result.is_err(failed)
        assert shop.total_of_all_orders(db)? == 5d
        counts = queries.order_counts(db)?

        assert counts |> Iter.map(fn c -> #(c.email, c.orders)) |> Iter.to_list() == [
            #("ada@example.com", 1),
        ]
    }

    test "a join reads every order of a user, newest first", db {
        user = queries.insert_user(db, "ada@example.com", None)?
        first = queries.insert_order(db, user, 1d)?
        second = queries.insert_order(db, user, 2d)?
        rows = queries.orders_for_user(db, user, 10)?
        assert rows |> Iter.map(fn r -> r.id) |> Iter.to_list() == [second, first]

        assert rows
            |> Iter.all(fn r -> r.email == "ada@example.com" and r.status == OrderStatus.Pending)
    }

    test "an outer join's columns are Maybe, and an aggregate counts", db {
        ada = queries.insert_user(db, "ada@example.com", None)?
        _ = queries.insert_user(db, "grace@example.com", None)?
        _ = queries.insert_order(db, ada, 3d)?
        latest = queries.insert_order(db, ada, 4d)?
        counts = queries.order_counts(db)?

        assert counts |> Iter.map(fn c -> #(c.email, c.last_order, c.orders)) |> Iter.to_list() == [
            #("ada@example.com", Some(latest), 2),
            #("grace@example.com", None, 0),
        ]
    }

    test "revenue sums the paid orders of each customer", db {
        ada = queries.insert_user(db, "ada@example.com", None)?
        grace = queries.insert_user(db, "grace@example.com", None)?
        _ = shop.place_paid_order(db, ada, 10d)?
        _ = shop.place_paid_order(db, ada, 2.5d)?
        _ = queries.insert_order(db, grace, 100d)?
        _ = shop.place_paid_order(db, grace, 1d)?
        revenue = queries.revenue_since(db, Instant.from_seconds(0))?
        assert revenue |> Iter.map(fn r -> "${r.revenue}") |> Iter.to_list() == ["12.50", "1.00"]

        assert revenue == [
            #shop.CustomerRevenue{
                user_id: ada,
                email: "ada@example.com",
                order_count: 2,
                revenue: 12.50d,
            },
            #shop.CustomerRevenue{
                user_id: grace,
                email: "grace@example.com",
                order_count: 1,
                revenue: 1d,
            },
        ]
    }

    test "a total keeps its column's scale, and is exact to 15 digits", db {
        user = queries.insert_user(db, "ada@example.com", None)?
        whole = queries.insert_order(db, user, 7d)?
        largest = queries.insert_order(db, user, 9_999_999_999.99d)?
        assert "${(queries.order(db, whole)?).total}" == "7.00"
        assert "${(queries.order(db, largest)?).total}" == "9999999999.99"

        // 2^53 + 1 cents does not fit a 64-bit float, which is how SQLite
        // keeps a DECIMAL: the driver refuses it rather than round it.
        refused = case queries.insert_order(db, user, 90_071_992_547_409.93d) {
            Err(#sql.Error.Database{code, message: _}) -> code
            _ -> "not refused"
        }

        assert refused == "SQLITE_MISMATCH"
    }

    test "a sum of many small totals is exact", db {
        user = queries.insert_user(db, "ada@example.com", None)?

        Iter.from(0)
        |> Iter.take(1000)
        |> Iter.each(fn _ -> { _ = shop.place_paid_order(db, user, 0.10d) })

        revenue = queries.revenue_since(db, Instant.from_seconds(0))?
        assert revenue |> Iter.map(fn r -> "${r.revenue}") |> Iter.to_list() == ["100.00"]
        assert "${shop.total_of_all_orders(db)?}" == "100.00"
    }

    test "a status update answers the rows it changed", db {
        user = queries.insert_user(db, "ada@example.com", None)?
        id = queries.insert_order(db, user, 1d)?
        assert queries.set_status(db, id, .Shipped) == Ok(#sqlkit.Changed{rows: 1})
        assert queries.set_status(db, id + 1, .Shipped) == Ok(#sqlkit.Changed{rows: 0})
    }

    test "revenue since an hour ago counts an order placed now", db {
        user = queries.insert_user(db, "ada@example.com", None)?
        _ = shop.place_paid_order(db, user, 10d)?
        now = Instant.now()
        hour_ago = queries.revenue_since(db, now - Duration.hours(1))?
        hour_later = queries.revenue_since(db, now + Duration.hours(1))?
        assert hour_ago |> Iter.map(fn r -> r.order_count) |> Iter.to_list() == [1]
        assert hour_later == []
    }
}

// FILE: db/queries/queries.sql
-- The shop's queries. `nomi tool sqlkit generate` writes queries.nomi from
-- them, a function for each.
-- import: shop.CustomerRevenue

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

-- query: order(id: Int) -> Order
-- One order.
SELECT id, user_id, status, total, placed_at, shipped_on
FROM orders
WHERE id = @id;

-- query: orders_for_user(user_id: Int, limit: Int)
-- A user's orders, newest first, each with the user's email.
SELECT o.id, o.status, o.total, o.placed_at, u.email
FROM orders o
JOIN users u ON u.id = o.user_id
WHERE o.user_id = @user_id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @limit;

-- query: order_counts()
-- Every user with the number of orders they placed, including those who
-- placed none.
SELECT u.email, max(o.id) AS "last_order?: Int", count(o.id) AS "orders!: Int"
FROM users u
LEFT JOIN orders o ON o.user_id = u.id
GROUP BY u.id
ORDER BY u.email;

-- query: revenue_since(since: Instant) -> List<CustomerRevenue>
-- Revenue per customer from paid and shipped orders since a moment. SQLite
-- declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
-- reads back at scale 2.
SELECT u.id AS user_id, u.email,
       count(*) AS "order_count!",
       CAST(sum(o.total) AS DECIMAL(12, 2)) AS "revenue!"
FROM users u
JOIN orders o ON o.user_id = u.id
WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= @since
GROUP BY u.id, u.email
ORDER BY revenue DESC;

-- query: all_orders() -> Rows<Order>
-- Every order, streamed.
SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id;

// FILE: queries.nomi
// Generated by `nomi tool sqlkit generate` from db/queries/queries.sql and
// db/migrations. Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/instant.Instant
    std/sql
    schema.{Order, OrderStatus, User}
    shop.CustomerRevenue
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

/// One order.
pub fn order(conn: sql.Conn, id: Int) -> Result<Order, sql.Error> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders WHERE id = ", ""],
        [runtime.bind(id)],
    )

    runtime.one(conn, "order", statement, fn row -> Ok(#Order{
            id: runtime.int("order", row, 0)?,
            user_id: runtime.int("order", row, 1)?,
            status: runtime.text("order", row, 2)?,
            total: runtime.numeric_scaled("order", row, 3, 2)?,
            placed_at: runtime.timestamptz("order", row, 4)?,
            shipped_on: runtime.date_null("order", row, 5)?,
        }))
}

/// A user's orders, newest first, each with the user's email.
pub fn orders_for_user(conn: sql.Conn, user_id: Int, limit: Int) -> Result<List<#{
    id: Int,
    status: OrderStatus,
    total: Decimal,
    placed_at: Instant,
    email: String,
}>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT o.id, o.status, o.total, o.placed_at, u.email FROM orders o JOIN users u ON u.id = o.user_id WHERE o.user_id = ",
            " ORDER BY o.placed_at DESC, o.id DESC LIMIT ",
            "",
        ],
        [runtime.bind(user_id), runtime.bind(limit)],
    )

    runtime.list(conn, "orders_for_user", statement, fn row -> Ok(#{
            id: runtime.int<Int>("orders_for_user", row, 0)?,
            status: runtime.text<OrderStatus>("orders_for_user", row, 1)?,
            total: runtime.numeric_scaled<Decimal>("orders_for_user", row, 2, 2)?,
            placed_at: runtime.timestamptz<Instant>("orders_for_user", row, 3)?,
            email: runtime.text<String>("orders_for_user", row, 4)?,
        }))
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

/// Revenue per customer from paid and shipped orders since a moment. SQLite
/// declares no type for a sum; the `CAST` declares it DECIMAL(12, 2), so it
/// reads back at scale 2.
pub fn revenue_since(conn: sql.Conn, since: Instant) -> Result<List<CustomerRevenue>, sql.Error> {
    statement = runtime.statement(
        [
            "SELECT u.id AS user_id, u.email, count(*) AS \"order_count\", CAST(sum(o.total) AS DECIMAL(12, 2)) AS \"revenue\" FROM users u JOIN orders o ON o.user_id = u.id WHERE o.status IN ('paid', 'shipped') AND o.placed_at >= ",
            " GROUP BY u.id, u.email ORDER BY revenue DESC",
        ],
        [runtime.bind(since)],
    )

    runtime.list(conn, "revenue_since", statement, fn row -> Ok(#CustomerRevenue{
            user_id: runtime.int("revenue_since", row, 0)?,
            email: runtime.text("revenue_since", row, 1)?,
            order_count: runtime.value("revenue_since", row, 2)?,
            revenue: runtime.numeric_scaled("revenue_since", row, 3, 2)?,
        }))
}

/// Every order, streamed.
pub fn all_orders(conn: sql.Conn) -> sqlkit.Rows<Order> {
    statement = runtime.statement(
        ["SELECT id, user_id, status, total, placed_at, shipped_on FROM orders ORDER BY id"],
        [],
    )

    runtime.rows(conn, "all_orders", statement, fn row -> Ok(#Order{
            id: runtime.int("all_orders", row, 0)?,
            user_id: runtime.int("all_orders", row, 1)?,
            status: runtime.text("all_orders", row, 2)?,
            total: runtime.numeric_scaled("all_orders", row, 3, 2)?,
            placed_at: runtime.timestamptz("all_orders", row, 4)?,
            shipped_on: runtime.date_null("all_orders", row, 5)?,
        }))
}

// FILE: schema.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations. Do not edit:
// change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import {
    std/calendar.Date
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
    shipped_on: Maybe<Date>
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
    #sqlkit.Migration{
        path: "db/migrations/20261019120000_users_and_orders.sql",
        sql: "CREATE TABLE users (\n    id          INTEGER PRIMARY KEY,\n    email       TEXT NOT NULL UNIQUE,\n    name        TEXT,\n    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n\nCREATE TABLE orders (\n    id          INTEGER PRIMARY KEY,\n    user_id     INTEGER NOT NULL REFERENCES users (id),\n    status      TEXT NOT NULL DEFAULT 'pending'\n                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),\n    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.\n    total       DECIMAL(12, 2) NOT NULL,\n    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP\n);\n",
    },
    #sqlkit.Migration{
        path: "db/migrations/20261102090000_orders_shipped_on.sql",
        sql: "ALTER TABLE orders ADD COLUMN shipped_on DATE;\n",
    },
]

// FILE: schema_test.nomi
// Generated by `nomi tool sqlkit generate` from db/migrations and db/queries.
// Do not edit: change the SQL and generate again.
// inputs sha256: 4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e
import sqlkit/inputs

// Fails when a migration or a query changed after the last
// `nomi tool sqlkit generate`: run it again. The paths are under the
// module root, where `nomi test` resolves a test's relative paths.
test "generated sql is current" {
    assert inputs.current("db/migrations", "db/queries")
        == Ok("4e91efef46c08ed86d9b59853756ef619b71cc1d4f93e016e1a3c987221dd44e")
}

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
        user_id = queries.insert_user(tx, email, None)?
        queries.insert_order(tx, user_id, total)
    })
}

/// Place an order and mark it paid, in one transaction.
pub fn place_paid_order(conn: Conn, user_id: Int, total: Decimal) -> Result<Int, Error> {
    sql.transaction(conn, fn tx -> {
        order_id = queries.insert_order(tx, user_id, total)?
        _ = queries.set_status(tx, order_id, .Paid)?
        Ok(order_id)
    })
}

/// The total of every order, read one row at a time.
pub fn total_of_all_orders(conn: Conn) -> Result<Decimal, Error> {
    queries.all_orders(conn) |> Rows.fold(0d, fn sum, order -> sum + order.total)
}

/// A customer's revenue: a row of `queries.revenue_since`.
pub struct CustomerRevenue {
    user_id: Int
    email: String
    order_count: Int
    revenue: Decimal
}

derive Equatable for CustomerRevenue

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

// FILE: nomi.toml
[module]
name = "shop"
entry_points = ["main"]
```
<!-- expect
main: up to date
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
ok generated sql is current
test result: ok. 12 passed, 0 failed
-->

`sqlkit.migrate` builds the schema, and the `defer` closes the database when
the test ends. Each test has its own database, so no test sees another's
rows. In the second test, the second `insert_user` breaks the `UNIQUE` on
`email`, and the constraint gets its Postgres name, `users_email_key`, as in
[Transactions](#transactions).

`generate` checks the migrations and queries with SQLite. A driver for
another database implements the same `Conn`, so the generated functions take
its connections too, but a project on another database can use only SQL that
SQLite also accepts.
