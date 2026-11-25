# Nomi SQL packages

First-party packages for talking to SQL databases from Nomi, over the
vocabulary in the standard library's `std/sql` (`Conn`, `Statement` and
the `Sql"..."` literal, `Value`, `Row`, `sql.Error`, `sql.transaction`).

| Package | Added as | What it is |
|---|---|---|
| [`sqlite`](sqlite) | `github.com/nomi-language/nomi/lib/sql/sqlite` | The SQLite driver: `sqlite.open(path)` and `sqlite.memory()` answer a `Conn`, over SQLite compiled to WebAssembly and run by `std::wasm`. |
| [`postgres`](postgres) | `github.com/nomi-language/nomi/lib/sql/postgres` | The Postgres driver: `postgres.pool(url)` and `postgres.connect(url)` answer a `Conn`, written in Nomi over `std::net` and `std::tls`; `postgres.listen` waits for notifications, and `postgres.scratch()` makes a throwaway database for tests. |
| [`migrate`](migrate) | `github.com/nomi-language/nomi/lib/sql/migrate` | Migrations: `migrate.up(conn, dir)` applies the `.sql` files in a directory that the database has not run, once each, in name order, under a lock, recording them in `nomi_migrations`; `nomi tool migrate up`, `status` and `adopt` (a one-time move from golang-migrate). |
| [`sqlkit`](sqlkit) | `github.com/nomi-language/nomi/lib/sql/sqlkit` | Checked SQL: `nomi tool sqlkit generate`, which checks the queries of `.sql` files against the migrations and writes a row struct per table and a typed function per query, and `Rows<T>`, `migrate` (over the migrate module) and the SQL type families the generated code uses. |
| [`examples/shop`](examples/shop) | `github.com/nomi-language/nomi/lib/sql/examples/shop` | A program using both: users and orders, migrations, checked queries, a transaction, and tests. |

Each is a package with its own `nomi.toml`, a directory of
nomi's repository. They are versioned by nomi's release tags, one tag for
the whole repository: a release `nomi` adds them at its own version, and a
`nomi` built from source at the latest release.

## Using the packages

```sh
nomi add github.com/nomi-language/nomi/lib/sql/sqlite
nomi add github.com/nomi-language/nomi/lib/sql/migrate
nomi add github.com/nomi-language/nomi/lib/sql/sqlkit
```

A release `nomi` adds them with those commands; before the first release, or
with a `nomi` built from source, add each by its directory in a checkout of
the repository: `nomi add ../nomi/lib/sql/sqlite`.

The [Databases](https://nomi-lang.org/databases/) chapter of the Nomi tour
walks through both.

## Working on the packages

`sqlkit` requires `sqlite`, `postgres` and `migrate`, and `migrate`
requires both drivers (for its tool and its tests). Each names them by a
`dir` entry for the sibling directory, with no version, and
`examples/shop` names all four so. A checkout runs the working tree. A
project that adds `sqlkit` from the repository at a tag gets the siblings
at that same tag, since a fetched package's `dir` entry names its
neighbor in the same repository. So a change to `sqlite` that `sqlkit`
needs lands in the same commit, and the next release carries both.

The postgres and migrate tests start a throwaway server with `initdb` and
`postgres` from `PATH`. From the repository root:

```sh
nomi test lib/sql/sqlite
nomi test lib/sql/postgres
nomi test lib/sql/migrate
nomi test lib/sql/sqlkit
nomi test lib/sql/examples/shop
```

and `nomi tool sqlkit generate --check` in `lib/sql/examples/shop`.

The SQLite driver runs SQLite compiled to WebAssembly on std::wasm, and the
Postgres driver is Nomi over std::net and std::tls, so neither needs a C
compiler or a database client library.
