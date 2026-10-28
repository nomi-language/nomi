# shop

A small shop over SQLite with checked SQL: users and orders, two
migrations, queries in a `.sql` file that `nomi tool sqlkit generate` turns
into typed functions (a lookup, a join, an outer join with an aggregate, a
revenue report, a streamed read), operations that run in transactions, and
a test group whose `setup` gives each test a fresh in-memory database with
the shop's schema.

| File | What it holds |
|---|---|
| `db/migrations/*.sql` | The schema, applied in name order. |
| `db/queries/queries.sql` | The queries, each with its Nomi name, parameters and result. |
| `schema.nomi` | Generated: the `User` and `Order` rows, the `OrderStatus` enum and `migrations`. |
| `queries.nomi` | Generated: a function per query. |
| `schema_test.nomi` | Generated: a test that the SQL has not changed since `generate` ran. |
| `shop.nomi` | Operations that run in transactions, and the `CustomerRevenue` row of the revenue report. |
| `main.nomi` | The program the tour's Databases chapter runs: it adds a user, places an order, marks it paid, reads the user back and prints each user's order count, on an in-memory database. |
| `shop_test.nomi` | The tests. |

```
nomi tool sqlkit generate --check   # the generated files match the SQL
nomi test .
nomi run main.nomi
```

An order's `total` is `DECIMAL(12, 2)`, which SQLite keeps as a 64-bit
float, exact to 15 digits; it reads back at scale 2 (`12.50d`), and the
revenue report declares its sum's type with
`CAST(sum(o.total) AS DECIMAL(12, 2))` so it does too. See the
[driver's notes on decimals](../../sqlite/README.md#decimals).

The module requires `sqlite` and `sqlkit` and replaces them with the
sibling directories, so it runs from a checkout of this repository.
