# pgjobs

A durable queue for `std/jobs`, kept in a Postgres table, over
[`lib/sql/postgres`](../../sql/postgres). `pgjobs.queue(db, url)` returns a
`Queue`, std/jobs' interface, so code written against `Queue` runs on it
unchanged. It is Nomi with no Go code of its own.

```
nomi add github.com/nomi-language/nomi/lib/jobs/postgres
nomi add github.com/nomi-language/nomi/lib/sql/postgres
```

A job is enqueued in the same transaction as the write it follows, and a
worker started in `main` runs it:

```nomi
import
    std::io
    std::jobs.{self, Failure, Job, Queue, Worker}
    std::json.{FromJson, ToJson}
    std::sql.{self, Conn, Sql}
    pgjobs
    postgres
end

pub struct App
    context: Context
    db: Conn
    queue: Queue
    worker: Worker
end

pub struct NotifyArgs
    post_id: Int
end

derive ToJson for NotifyArgs
derive FromJson for NotifyArgs

once notify_job = Job.new("notify", notify)

fn notify(args: NotifyArgs) -> Result<Unit, Failure>
    io.print("post ${args.post_id} published")
    Ok(Unit)
end

pub fn boot() -> Result<App, String>
    url = "postgres://localhost/app"
    db = postgres.pool(url)
    pgjobs.install(db) |> Result.map_err(fn e -> "${e}")?
    queue = pgjobs.queue(db, url) |> Result.map_err(fn e -> "${e}")?
    Ok(App{context: Context.root(), db:, queue:, worker: Worker.new(queue, max_running: 4)})
end

fn publish(post_id: Int) -> Result<Unit, sql.Error>
    sql.transaction(App.db, fn tx ->
        _ = Conn.exec(tx, Sql"UPDATE posts SET published = true WHERE id = ${post_id}")?
        _ = jobs.enqueue(App.queue, tx, notify_job, NotifyArgs{post_id:})?
        Ok(Unit)
    end)
end

fn main()
    Worker.start(App.worker, [notify_job])
    // ... serve requests that call publish
end
```

## What it does

- `pgjobs.queue(db, url)` returns a `Queue` in the table `nomi_jobs` of
  the database `db` points at; `url` names the same database for the
  connection each worker listens on. An enqueue writes on the `Conn` it is
  given, so the job exists only if that transaction commits.
- `schema.sql` is the tables and their indexes, for a project's migrations
  to copy, and `schema_upgrade.sql` brings a table made from an earlier
  `schema.sql` up to it: the `priority`, `unique_key` and `unique_states`
  columns, the indexes that use them and the schedules table.
  `pgjobs.install(conn)` runs both where needed, for tests and small apps.
- A project that calls `install` at boot is upgraded by it. A project that
  made its table from `schema.sql` in its own migrations never calls
  `install`, so each time it upgrades pgjobs it adds the current
  `schema_upgrade.sql` as a new migration. Every statement in it is
  `IF NOT EXISTS`, so it is safe on a table that is already current.
- `pgjobs.queue` checks the table before it answers a `Queue`, and is an
  error when the table lacks a column, the unique index or the schedules
  table that this version uses. A table made from an earlier `schema.sql`
  gives:

  ```
  database error 42703: pgjobs: the jobs table nomi_jobs is older than this
  version of pgjobs and lacks the columns priority, unique_key,
  unique_states; the index nomi_jobs_unique; the table nomi_jobs_schedules.
  Call pgjobs.install(conn) before pgjobs.queue, or add lib/jobs/postgres's
  schema_upgrade.sql to your migrations as a new migration.
  ```

  The check is a lookup of the table's columns and relations, so a
  project's boot fails with it before any worker starts, rather than on
  the first claim or enqueue. Run migrations before building the queue.
- A `table:` argument to `queue`, `install` and `stuck` names another
  table, and renames its indexes, its schedules table
  (`<table>_schedules`) and its `NOTIFY` channel with it. The name must
  be lowercase letters, digits and `_`; each returns an error on another
  name. A project that renames the table renames `nomi_jobs` in the copies
  of `schema.sql` and `schema_upgrade.sql` it migrates with.
- A worker claims due jobs with `FOR UPDATE SKIP LOCKED`, so workers in any
  number of processes never take the same job, highest `priority` first,
  then oldest.
- A unique enqueue is an `INSERT ... ON CONFLICT DO NOTHING` against a
  partial unique index over the jobs that hold a key, on the caller's
  `Conn`. A duplicate answers the holder's id with `duplicate: True`, and
  a key taken in a transaction that rolls back is free again.
- A worker started with schedules records each schedule's latest tick in
  `nomi_jobs_schedules`, in the same transaction as the tick's job. The
  job's unique key is the schedule's name and the tick, so every process
  can run the same schedules and each tick is enqueued once.
- A claimed job holds a lease its worker renews. When a process dies, any
  other worker makes its jobs available again once the leases run out, so
  no process leads. A rescued run counts as an attempt.
- An enqueue sends `NOTIFY` on the table's name, which Postgres delivers at
  commit. Each worker listens on it, so a job starts at once rather than at
  the next poll. The listener stops as soon as shutdown starts.
- Finished jobs are deleted once they are older than `retention` (a
  `queue` argument, 7 days unless given), by any worker after a claim
  finds nothing due, 1,000 rows at a time.
- `pgjobs.stuck(conn, older_than)` lists up to 100 jobs, oldest first, due
  for longer than `older_than` that no worker has claimed: their kind is
  one no running worker handles, or no worker runs their queue.
- Each failed run is kept on the row in `errors`, with its attempt and
  time.
- It passes `std/jobs/testing`'s conformance suite.

## Not built

- A rate shared across processes, and a web UI.
- A rate limit is per worker process, as `std/jobs`'s `Rate` says.

## Tests

`nomi test lib/jobs/postgres` runs on `postgres.scratch()`'s throwaway
server, so it needs `initdb` and `postgres` on `PATH`. Without them the
tests fail, since a Nomi test cannot skip itself; `TestLibModules`
(`cmd/nomi`) skips this module instead. The example in
[`../examples/notify`](../examples/notify) ports an app's notification jobs
from River and runs them on a real worker.
