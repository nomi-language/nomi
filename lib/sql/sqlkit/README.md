# sqlkit

Checked SQL for Nomi. You write SQL in `.sql` files; sqlkit checks each
query against your schema and writes ordinary Nomi code for it: a row
struct per table and a typed function per query. One tool does it:

```
nomi tool sqlkit generate           # write the generated files
nomi tool sqlkit generate --check   # write nothing; fail when they are stale
```

`generate` applies the migrations to a scratch database (an in-memory
SQLite database, or one it makes on a Postgres development server; see
[Postgres](#postgres)), asks the database to describe every query, and
writes:

- `schema.nomi`: a row struct per table, named in the singular (`users`
  holds `User` rows), an enum per `CHECK (column IN (...))` column, and the
  migrations as data, for `sqlkit.migrate`;
- one file of functions per query file: `db/queries/queries.sql` becomes
  `queries.nomi`, with a function for each query that runs its SQL on a
  `Conn` and answers typed rows;
- `schema_test.nomi`: a test that the migrations and queries are still the
  ones the files were generated from.

The generated files are ordinary, formatted Nomi that you commit. `nomi
check`, the editor, review and grep read them like any other file. Each
starts with a header that says what it was generated from and records a
SHA-256 digest of the inputs. Run `generate` after changing a migration or
a query. `generate --check` in CI fails when a generated file is not what
`generate` would write, and the generated test fails `nomi test` when a
migration or query changed since, wherever `nomi test` runs, since a test
reads relative paths under its module root.

See [`../examples/shop`](../examples/shop) for a whole program.

## Setting up

```
db/
├── migrations/
│   ├── 20261019120000_users_and_orders.sql
│   └── 20261102090000_orders_shipped_on.sql
└── queries/
    └── queries.sql
```

The defaults need no configuration. To change them:

```toml
# nomi.toml
[sqlkit.database.main]
dialect = "sqlite"             # or "postgres"
migrations = "db/migrations"   # SQL files, applied in name order
queries = "db/queries"         # the query files, *.sql
out = "."                      # where the generated files go
schema = "schema"              # the schema file's name: schema.nomi
dev_url_env = "DATABASE_URL"   # postgres: the variable naming a development server

[sqlkit.database.main.tables]
people = "Person"              # a table's row struct, when the rule misses
```

Every key may be left out; those are the defaults. A module with no
`[sqlkit.database]` table has one database, `main`, with the defaults.
With several databases, give each its own `queries` and `out` or `schema`,
so no two write one file. The migrations and the query files are the `.sql`
files directly in their directories.

A table's row struct is its name in the singular, in PascalCase: a final
`ies` becomes `y` (`categories`, `Category`), a final `ses` becomes `s`
(`addresses`, `Address`), a final `ss` stays, and any other final `s` goes
(`order_items`, `OrderItem`). `[sqlkit.database.<name>.tables]` names the
row of a table the rule gets wrong; a name there must be a type's name, and
must name a table the migrations create. An enum is named after its
table's row and its column: `orders.status` is `OrderStatus`, and with
`people = "Person"`, `people.status` is `PersonStatus`.

## Queries

A query file holds queries, each an annotation that gives its Nomi name,
parameters and result, optional doc lines, and its SQL, ending in `;`:

```sql
-- query: user_by_email(email: String) -> Maybe<User>
-- The user with this email, if any.
SELECT id, email, name, created_at
FROM users
WHERE email = @email;
```

`generate` writes this function into `queries.nomi`:

```nomi
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
```

The SQL is kept apart from `email`: the statement holds the SQL around each
parameter and the values to bind. Each column is read into its field by a
reader for the column's type (`text_null` for a nullable `TEXT` column).

The generated file imports what its signatures name: a type of the schema
(`User`, `OrderStatus`), one of std's (`Instant`, `Date`, `Uuid`, ...), or
the prelude's. Any other type comes from an `-- import:` line before the
first query, written as an entry of an import block:

```sql
-- import: reports.CustomerRevenue
```

`@name` is SQL's own named parameter. It marks where the parameter is
bound, and the value is never spliced into the SQL. An `@` followed by a
letter or `_` starts a parameter name (letters, digits and `_`); any other
`@` (Postgres's `@>`, `<@`, `@@`), and every `@` inside a string literal, a
quoted name or a comment, is SQL text. Every `@name` must name a parameter,
and every parameter must be used. The SQL that is sent binds by position:
each `@name` is written as `?1`, `?2`, ... in order, the form std/sql's
`Statement` prints. A parameter used twice is bound twice.

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
ignore it. `Changed` is sqlkit's struct `Changed{rows: Int}`, not an `Int`,
so taking `RETURNING id` off an `INSERT ... -> Int` is an error rather than
a function that answers 1 where it answered an id.

A column is a `Maybe` when it may be NULL: a table column without `NOT
NULL`, a column of a table an outer join may leave out (SQLite's query plan
says which), and every expression. The column's alias says otherwise, as
sqlx's do: `count(*) AS "n!"` asserts it is never NULL, `AS "email?"` that it
may be, and `AS "n: Int"` gives an expression's type, which SQLite does not
report (an anonymous row needs one). The marker is taken out of the SQL that
is sent.

### What is checked

`generate` stops with each problem at its file, line and column, and writes
nothing:

```
db/queries/queries.sql:3:12: no such column: emial
db/queries/queries.sql:3:12: help: reported by SQLite for `user_by_email`
error: 1 problem in the SQL; nothing was written
```

- SQLite's own error for a query it cannot prepare, at the token it names,
  and for a migration it cannot apply, at the migration's line.
- An `@name` that names no parameter, and a parameter the SQL does not use.
- A result type that does not fit whether the SQL returns rows: none, or a
  row type, on SQL that returns no rows; `-> Unit` or `-> Changed` on SQL
  that returns rows.
- A column whose name cannot be a field's, two columns of one name, and an
  expression with no type in an anonymous row.
- A type in a signature that nothing imports.

One mistake can give two errors. Here `user_by_email` says `@mail` where it
means `@email`, so `mail` names no parameter and `email` is not used:

```
db/queries/queries.sql:9:15: `mail` is not a parameter of `user_by_email`
db/queries/queries.sql:9:15: help: its parameters are `email`
db/queries/queries.sql:5:25: the parameter `email` is not used in the SQL of `user_by_email`
db/queries/queries.sql:5:25: help: write `@email` where it is bound, or remove it
error: 2 problems in the SQL; nothing was written
```

Taking `RETURNING id` off `insert_user` leaves an `INSERT` that returns no
row, so there is no id to answer:

```
db/queries/queries.sql:11:62: `insert_user` returns no rows, so it cannot answer an `Int`; write `-> Changed` for the number of rows it changed, or `-> Unit`
```

The checker then checks the generated code like any other. A column read
into a field of the wrong type, or a NULL-able one into a field that is not
a `Maybe`, is an error at the column's reader in the generated file:

```
error: Int does not implement SqlNumeric (required by `where T: SqlNumeric`)
   --> queries.nomi:142:26
    |
142 |             revenue: runtime.numeric_scaled("revenue_since", row, 3, 2)?,
    |                          ^^^^^^^
```

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
back as `12.50d`, and `generate` warns about a `DECIMAL` column declared
with more than 15 digits or with no scale:

```
warning: db/migrations/20261201090000_discount.sql:1: `orders.discount` is DECIMAL with no scale, so a value reads back at its shortest scale (12.5, not 12.50): declare DECIMAL(p, s)
```

An expression has no declared type: SQLite does not say that
`sum(o.total)` is a `DECIMAL(12, 2)`. Declare it with a `CAST`, which
`generate` reads as the column's type:

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

`sqlkit.migrate(conn, schema.migrations)` brings a database up to date
through the [migrate](../migrate) module: under a lock (on Postgres an
advisory lock, on SQLite the write lock), it runs each generated migration
the database has not run, in order, each in a savepoint with its record.
The records are the table `nomi_migrations` (`version`, the migration's
file name; `checksum`, of its text; `applied_at`), which the first run
creates. So it builds a test's fresh database and migrates one that lives
on, run after run, and two processes that start at once do not both
migrate. A migration whose text changed after it ran is refused before
anything runs, with `sql.Error.Database` coded `migrate` naming the file:
write the change as a new migration. A migration that fails answers its
own error; the ones before it stay applied.

The migrations directory is read as the migrate module reads it: the
`.sql` files directly in it, in name order, without `.down.sql` files, so
a directory golang-migrate keeps (`000001_x.up.sql`, `000001_x.down.sql`)
describes the schema its `.up.sql` files build.

### Testing with the schema

A test group's `setup` can hand each test a fresh in-memory database with
the schema, so no test sees another's rows:

```nomi
group "the shop" {
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
    }
}
```

A constraint the schema leaves unnamed gets the name Postgres would give
it, so a broken `UNIQUE` on `users.email` is a `sql.Error.Constraint` named
`users_email_key` on either database. The shop example's `shop_test.nomi`
has eleven such tests: joins, `Maybe` columns of an outer join, decimal
scale, transactions over queries and a `-> Changed` count.

### In an application

A program that serves requests opens the database once, in its `boot`, and
keeps the connection in an app field, so every function reads it as
`App.db`:

```nomi
pub struct App {
    db: Conn
    context: Context
}

pub fn boot(startup: Startup) -> Result<App, Error> {
    path = Map.get(startup.env, "SHOP_DATABASE") |> Maybe.with_default(":memory:")
    db = sqlite.open(path)?
    defer sql.close_quietly(db)
    sqlkit.migrate(db, schema.migrations)?
    Ok(App{db, context: Context.root()})
}

pub fn sign_up(email: String) -> Result<Int, Error> {
    queries.insert_user(App.db, email, None)
}
```

When the file does not open or a migration fails, `boot` returns the `Err`
and `main` does not run. The `defer` closes the database when the program
ends. A test group that writes `boot main.boot(Startup{})` gets a fresh
in-memory database and its own `App.db` for each test; a test that builds
its own database replaces the field with `with App.db = db` in its `setup`.

## Postgres

With `dialect = "postgres"`, `generate` needs a Postgres development
server, whose URL the environment variable `dev_url_env` holds
(`DATABASE_URL` by default). It makes a scratch database there,
`nomi_prepare_<hash>_<random>`, applies the migrations, describes each
query, and drops the database before it finishes, also when it fails.
Nothing is written to the database the URL names, but its user must be
allowed to create databases. `generate --check` does the same, so CI that
runs it needs a server and the variable too; with the variable unset,
`generate` stops and says to set it.

The generated code runs on a `Conn` from the
[Postgres driver](../postgres), which a project adds with `nomi add
github.com/nomi-language/nomi/lib/sql/postgres`, and the SQL it sends
binds `$1`, `$2`, .... A test gets a fresh database from the driver's
throwaway server, `postgres.connect(postgres.scratch()?)?`, and
`sqlkit.migrate` builds the schema there as it does on SQLite.

What differs from SQLite:

- Postgres reports a type for every column, expressions included, so an
  anonymous row needs no `AS "n: Int"`.
- A column is non-null only when it reads a `NOT NULL` table column that
  no outer join of the query can leave out, which the query's generic
  plan shows (Postgres 12 or later). `AS "n!"` and `AS "n?"` still say
  otherwise.
- An enum type's labels, like a `CHECK (column IN (...))`, make the
  column a generated enum.
- `T[]` reads and binds as `List<T>`: `WHERE id = ANY(@ids)` with
  `ids: List<Uuid>`.
- A type sqlkit has no family for (`interval`, `inet`) reads as text,
  and `generate` prints a note; `citext` reads as text without one.

| Postgres | Nomi type |
|---|---|
| `smallint`, `integer`, `bigint` | `Int` |
| `real`, `double precision` | `Float` |
| `numeric(p, s)` | `Decimal`, at scale `s` |
| `text`, `varchar`, `char`, `citext` | `String` |
| `boolean` | `Bool` |
| `bytea` | `Bytes` |
| `date`, `time` | `Date`, `Time` |
| `timestamp`, `timestamptz` | `NaiveDateTime`, `Instant` |
| `uuid` | `Uuid` |
| `json`, `jsonb` | `Json` |
| `T[]` | `List<T>` |

## Limits

- A query is one statement.
- Postgres parameters are not yet checked against the type Postgres infers
  for them: a parameter binds through `ToSql`, and a mismatch is
  Postgres's error when the query runs.
- The editor reads the generated code, not the SQL: there is no hover,
  completion or error inside a `.sql` file unless a SQL language server is
  configured for it.
