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
import {
    std::io
    std::jobs.{self, Failure, Job, Queue, Worker}
    std::json.{FromJson, ToJson}
    std::sql.{self, Conn, Sql}
    pgjobs
    postgres
}

pub struct App {
    context: Context
    db: Conn
    queue: Queue
    worker: Worker
}

pub struct NotifyArgs {
    post_id: Int
}

derive ToJson for NotifyArgs
derive FromJson for NotifyArgs

once notify_job = Job.new("notify", notify)

fn notify(args: NotifyArgs) -> Result<Unit, Failure> {
    io.print("post ${args.post_id} published")
    Ok(Unit)
}

pub fn boot() -> Result<App, String> {
    url = "postgres://localhost/app"
    db = postgres.pool(url)
    pgjobs.install(db) |> Result.map_err(fn e -> "${e}")?
    queue = pgjobs.queue(db, url)
    Ok(App{context: Context.root(), db, queue, worker: Worker.new(queue, max_running: 4)})
}

fn publish(post_id: Int) -> Result<Unit, sql.Error> {
    sql.transaction(App.db, fn tx -> {
        _ = Conn.exec(tx, Sql"UPDATE posts SET published = true WHERE id = ${post_id}")?
        _ = jobs.enqueue(App.queue, tx, notify_job, NotifyArgs{post_id})?
        Ok(Unit)
    })
}

fn main() {
    Worker.start(App.worker, [notify_job])
    // ... serve requests that call publish
}
```

## What it does

- `pgjobs.queue(db, url)` returns a `Queue` in the table `nomi_jobs` of
  the database `db` points at; `url` names the same database for the
  connection each worker listens on. An enqueue writes on the `Conn` it is
  given, so the job exists only if that transaction commits.
- `schema.sql` is the table and its indexes, for a project's migrations to
  copy. `pgjobs.install(conn)` creates them where they are missing, for
  tests and small apps. A `table:` argument to `queue`, `install` and
  `stuck` names another table, and renames its indexes and its `NOTIFY`
  channel with it. The name must be lowercase letters, digits and `_`:
  `queue` traps on another name, and `install` and `stuck` return an
  error.
- A worker claims due jobs with `FOR UPDATE SKIP LOCKED`, so workers in any
  number of processes never take the same job.
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
- It passes `std/jobs_testing`'s conformance suite.

## Not built

- Recurring and cron jobs, unique jobs, priorities, a rate shared across
  processes, and a web UI.
- A rate limit is per worker process, as `std/jobs`'s `Rate` says.

## Tests

`nomi test lib/jobs/postgres` runs on `postgres.scratch()`'s throwaway
server, so it needs `initdb` and `postgres` on `PATH`. Without them the
tests fail, since a Nomi test cannot skip itself; `TestLibModules`
(`cmd/nomi`) skips this module instead. The example in
[`../examples/notify`](../examples/notify) ports an app's notification jobs
from River and runs them on a real worker.
