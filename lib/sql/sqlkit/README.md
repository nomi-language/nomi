# sqlkit

Checked SQL for Nomi. You write SQL in `.sql` files; sqlkit checks each
query against your schema and writes ordinary Nomi code for it: a row
struct per table and a typed function per query. One tool does it:

```
nomi tool sqlkit generate           # write the generated files
nomi tool sqlkit generate --check   # write nothing; fail when they are stale
```

`generate` applies the migrations to a scratch in-memory SQLite database,
asks SQLite to describe every query, and writes:

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
migrations = "db/migrations"   # SQL files, applied in name order
queries = "db/queries"         # the query files, *.sql
out = "."                      # where the generated files go
schema = "schema"              # the schema file's name: schema.nomi

[sqlkit.database.main.tables]
people = "Person"              # a table's row struct, when the rule misses
```

Every key may be left out; those are the defaults. A module with no
`[sqlkit.database]` table has one database, `main`, with the defaults.
With several databases, give each its own `queries` and `out` or `schema`,
so no two write one file. The migrations and the query files are the `.sql`
files directly in their directories. SQLite is the only database so far.

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
ignore it. `Changed` is sqlkit's struct `#Changed{rows: Int}`, not an `Int`,
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

The checker then checks the generated code like any other. A column read
into a field of the wrong type, or a NULL-able one into a field that is not
a `Maybe`, is an error at the column's reader in the generated file:

```
error: Int does not implement SqlNumeric (required by `where T: SqlNumeric`)
   --> queries.nomi:142:26
    |
142 |             revenue: try runtime.numeric_scaled("revenue_since", row, 3, 2),
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

`sqlkit.migrate(conn, schema.migrations)` brings a database up to date: it
runs each generated migration the database has not run, in order, each in
its own transaction with its record. The records are the table
`nomi_migrations` (`version`, the migration's file name; `checksum`, of its
text; `applied_at`), which the first run creates. So it builds a test's
fresh database and migrates one that lives on, run after run. A migration
whose text changed after it ran is refused before anything runs, with
`sql.Error.Database` coded `sqlkit` naming the file: write the change as a
new migration.

## Limits

- SQLite only. Postgres needs its driver and an `EXPLAIN`-based
  nullability.
- A query is one statement.
- The editor reads the generated code, not the SQL: there is no hover,
  completion or error inside a `.sql` file unless a SQL language server is
  configured for it.
