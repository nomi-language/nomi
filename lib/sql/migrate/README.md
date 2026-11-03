# migrate

Migrations for Nomi's `std/sql`: plain SQL files, each applied once, in
name order, and recorded in the table `nomi_migrations`. It works on any
`Conn`, and takes a lock on SQLite and Postgres so two processes that
start at once do not both migrate. There are no down migrations: a mistake
is fixed by a new migration.

```
nomi add github.com/nomi-language/nomi/lib/sql/migrate
```

Then `nomi tool migrate up` applies a project's migrations from the
command line ([The tool](#the-tool)), and `migrate.up` applies them from
the program:

```nomi
import {
    std/sql
    migrate
    postgres
}

fn main() -> Result<Unit, migrate.Error> {
    db = postgres.pool("postgres://localhost/app")
    defer sql.close_quietly(db)
    _ = migrate.up(db, "db/migrations")?
    // ... serve
    Ok(Unit)
}
```

## What it does

- `migrate.read(dir)` is the migrations in `dir`: the `.sql` files directly
  in it, in name order, with their text. A `.down.sql` file is not a
  migration (`migrate.is_migration_file`), so a directory golang-migrate
  keeps works as it is, and so does any naming whose order is name order
  (`20261019120000_users.sql`, `000001_users.up.sql`). A directory that
  does not exist is an error, not an empty list.
- `migrate.apply(conn, migrations)` brings the database up to date, and
  `migrate.up(conn, dir)` is `read` then `apply`. Both answer the versions
  they ran. A version is the file's name.
- `migrate.status(conn, migrations)` says where each migration stands
  (applied, pending, changed since it ran, or recorded with no file),
  without a lock and without creating anything.
- `migrate.adopt_golang_migrate(conn, migrations)` moves a database that
  golang-migrate kept, once (below).

### One run

A run is one transaction on one connection, so a pool works:

1. **The lock.** On Postgres, `pg_advisory_xact_lock` on a fixed key, held
   until the run ends; a second process waits there, then finds the first
   one's records. On SQLite, the run's first write takes the database's
   write lock; a second process waits for it as long as the driver's busy
   timeout (5 seconds), then fails with `SQLITE_BUSY`. Another engine gets
   no lock.
2. **The records.** `nomi_migrations (version TEXT PRIMARY KEY, checksum
   TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT
   CURRENT_TIMESTAMP)`, created if it is missing. The checksum is 64-bit
   FNV-1a of the file's text, as 16 hex digits.
3. **The check.** A migration whose text is not the text that ran is
   `Err(.Changed)`, before anything runs: write the change as a new
   migration.
4. **The migrations.** Each pending one runs, in list order, in a savepoint
   with its record. One that fails is `Err(.Failed)`: nothing of it is
   applied, the ones before it are committed, and the ones after it do not
   run. A file may hold several statements.

A statement Postgres refuses inside a transaction (`CREATE INDEX
CONCURRENTLY`, `ALTER TYPE ... ADD VALUE` before Postgres 12) cannot be a
migration.

## The tool

```
nomi tool migrate up [--dir db/migrations] [--url URL]
nomi tool migrate status [--dir db/migrations] [--url URL]
nomi tool migrate adopt [--dir db/migrations] [--url URL]
```

`--url` defaults to `$DATABASE_URL`: `postgres://...` or `postgresql://...`
for Postgres, `sqlite:PATH` for a SQLite file. `--dir` defaults to
`db/migrations`, which is also sqlkit's default. `status` exits with a
failure when a migration changed after it ran.

## A built program

`migrate.up` reads the directory when the program runs, so a program built
with `nomi build` needs its migrations directory where it runs: Nomi has
no way to put data files inside a built executable. A program that uses
sqlkit has its migrations in the generated schema file
(`schema.migrations`), and `sqlkit.migrate` applies them with this module,
so it needs no directory.

## Moving from golang-migrate

golang-migrate records one version in `schema_migrations`. Once, against
the database it kept:

```
nomi tool migrate adopt --dir migrations
```

records each migration whose leading number is at most that version as
applied, with the checksum of its text now, and runs nothing. After it,
`migrate up` runs only the newer ones, and `schema_migrations` can be
dropped. Adoption refuses, recording nothing, when there is no
`schema_migrations`, when golang-migrate marked its version dirty (resolve
it with `migrate force` first), when no migration has that version's
number, or when a migration has no leading number.

## Tests

`nomi test lib/sql/migrate` runs each case on SQLite in memory and on a
database on `postgres.scratch()`'s throwaway server, and runs two processes
at once against Postgres to check the lock. The Postgres cases need
`initdb` and `postgres` on `PATH`. Without them they fail, since a Nomi
test cannot skip itself; `TestLibModules` (`cmd/nomi`) skips this module
instead.
