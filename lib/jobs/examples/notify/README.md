# notify

An app's notification emails, ported from River to `std/jobs` over
[`lib/jobs/postgres`](../../postgres). It is the acceptance program for
both.

- Publishing a post enqueues a batch job in the same transaction as the
  post, so a publish that rolls back enqueues nothing.
- The batch job writes one notification row per unnotified subscriber and
  enqueues that subscriber's email job in the same transaction as the row.
- Each email job skips a notification already sent, so a job that runs
  twice sends once.
- The worker runs one job at a time and starts at most four a second,
  burst two, as the app's Mailgun limiter did. Deliveries go to a channel
  in place of Mailgun.

`nomi run app.nomi` and `nomi test .` start a scratch Postgres server
(`postgres.scratch()`), which needs `initdb` and `postgres` on `PATH`.
