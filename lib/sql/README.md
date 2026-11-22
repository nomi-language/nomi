# Nomi SQL modules

First-party modules for talking to SQL databases from Nomi, over the
vocabulary in the standard library's `std/sql` (`Conn`, `Statement` and
the `Sql"..."` literal, `Value`, `Row`, `sql.Error`, `sql.transaction`).

| Module | Path | What it is |
|---|---|---|
| [`sqlite`](sqlite) | `github.com/nomi-language/nomi/lib/sql/sqlite` | The SQLite driver: `sqlite.open(path)` and `sqlite.memory()` answer a `Conn`, over SQLite compiled to WebAssembly and run by `std::wasm`. |
| [`postgres`](postgres) | `github.com/nomi-language/nomi/lib/sql/postgres` | The Postgres driver: `postgres.pool(url)` and `postgres.connect(url)` answer a `Conn`, written in Nomi over `std::net` and `std::tls`; `postgres.listen` waits for notifications, and `postgres.scratch()` makes a throwaway database for tests. |
| [`migrate`](migrate) | `github.com/nomi-language/nomi/lib/sql/migrate` | Migrations: `migrate.up(conn, dir)` applies the `.sql` files in a directory that the database has not run, once each, in name order, under a lock, recording them in `nomi_migrations`; `nomi tool migrate up`, `status` and `adopt` (a one-time move from golang-migrate). |
| [`sqlkit`](sqlkit) | `github.com/nomi-language/nomi/lib/sql/sqlkit` | Checked SQL: `nomi tool sqlkit generate`, which checks the queries of `.sql` files against the migrations and writes a row struct per table and a typed function per query, and `Rows<T>`, `migrate` (over the migrate module) and the SQL type families the generated code uses. |
| [`examples/shop`](examples/shop) | `github.com/nomi-language/nomi/lib/sql/examples/shop` | A program using both: users and orders, migrations, checked queries, a transaction, and tests. |

Each module is a Nomi module with its own `nomi.toml`, and the one-line
`go.mod` `nomi` writes beside it, which makes it a module of its own to
Go's module proxy and keeps its files out of the compiler module's zip.
They are versioned with nomi's commits: there are no release tags, and a
project gets the newest commit on `main`.

## Using the modules

```sh
nomi add github.com/nomi-language/nomi/lib/sql/sqlite
nomi add github.com/nomi-language/nomi/lib/sql/migrate
nomi add github.com/nomi-language/nomi/lib/sql/sqlkit
```

The [Databases](https://nomi-lang.org/databases/) chapter of the Nomi tour
walks through both.

## Working on the modules

`sqlkit` requires `sqlite` and `migrate`, `migrate` requires both drivers
(for its tool and its tests), and each names them with a version and a
`dir` entry for the sibling directory; `examples/shop` names all four so,
and a checkout runs the working tree. A project that adds `sqlkit` from
the proxy does not read those `dir` entries and gets `sqlite` at the
version `sqlkit`'s `nomi.toml` names. When `sqlkit` needs a newer
`sqlite`, publish `sqlite` (push the commit to nomi's `main`) first, then
run `nomi add github.com/nomi-language/nomi/lib/sql/sqlite@<commit>` in
`sqlkit`.

nomi's own checks run all of it: `TestLibModules` (`cmd/nomi`) runs
`nomi test` on `sqlite`, `postgres`, `migrate` and `sqlkit`, `nomi tool sqlkit
generate --check` and `nomi test .` in `examples/shop`. The postgres and migrate runs start a throwaway server
with `initdb` and `postgres` from `PATH`, and are skipped where those are
absent and on Windows. By hand, from the repository root:

```sh
nomi test lib/sql/sqlite
nomi test lib/sql/postgres
nomi test lib/sql/migrate
nomi test lib/sql/sqlkit
nomi test lib/sql/examples/shop
```

and `nomi tool sqlkit generate --check` in `lib/sql/examples/shop`.

The SQLite driver runs SQLite compiled to WebAssembly on std::wasm, and the
Postgres driver is Nomi over std::net and std::tls, so neither has Go code.
