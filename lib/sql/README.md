# Nomi SQL modules

First-party modules for talking to SQL databases from Nomi, over the
vocabulary in the standard library's `std/sql` (`Conn`, `Statement` and
the `Sql"..."` literal, `Value`, `Row`, `sql.Error`, `sql.transaction`).

| Module | Path | What it is |
|---|---|---|
| [`sqlite`](sqlite) | `github.com/nomi-language/nomi/lib/sql/sqlite` | The SQLite driver: `sqlite.open(path)` and `sqlite.memory()` answer a `Conn`, over the pure-Go `modernc.org/sqlite`. |
| [`sqlkit`](sqlkit) | `github.com/nomi-language/nomi/lib/sql/sqlkit` | Checked SQL: `nomi tool sqlkit generate`, which checks the queries of `.sql` files against the migrations and writes a row struct per table and a typed function per query, and `Rows<T>`, `migrate` and the SQL type families the generated code uses. |
| [`examples/shop`](examples/shop) | `github.com/nomi-language/nomi/lib/sql/examples/shop` | A program using both: users and orders, migrations, checked queries, a transaction, and tests. |

Each module is its own Go module with its own `go.mod` and `nomi.toml`,
nested in the nomi repository, so the compiler's module never requires
them or what they require (`modernc.org/sqlite`). They are versioned with
nomi's commits: there are no release tags, and a project gets the newest
commit on `main`.

## Using the modules

```
nomi add github.com/nomi-language/nomi/lib/sql/sqlite
nomi add github.com/nomi-language/nomi/lib/sql/sqlkit
```

The [Databases](https://nomi-lang.org/databases/) chapter of the Nomi tour
walks through both.

## Working on the modules

`sqlkit` requires `sqlite` and `replace`s it with the sibling directory,
and `examples/shop` replaces both, so a checkout runs the working tree. A
project that adds `sqlkit` ignores the `replace` and gets `sqlite` at the
version `sqlkit`'s go.mod requires. When `sqlkit` needs a newer `sqlite`,
publish `sqlite` (push the commit to nomi's `main`) first, then run
`nomi add github.com/nomi-language/nomi/lib/sql/sqlite@<commit>` in
`sqlkit`.

nomi's own checks run all of it: `TestLibModules` (`cmd/nomi`) runs
`nomi test` on `sqlite` and `sqlkit`, `nomi tool sqlkit generate --check`
and `nomi test .` in `examples/shop`, and `go test ./...` in `sqlite`. By
hand, from the repository root:

```
nomi test lib/sql/sqlite
nomi test lib/sql/sqlkit
nomi test lib/sql/examples/shop
go test -C lib/sql/sqlite ./...
```

and `nomi tool sqlkit generate --check` in `lib/sql/examples/shop`.

The SQLite driver's Go code builds into the program's runner the first time
a program that uses it runs, with the `go` on `PATH` or the toolchain `nomi`
downloads (no cgo is needed). The tour links the driver's browser build
into its wasm (see `internal/tourgo`).
