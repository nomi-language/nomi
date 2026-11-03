-- The table lib/jobs/postgres keeps jobs in, for a project's migrations to
-- copy. `pgjobs.install(conn)` runs these statements, with IF NOT EXISTS,
-- for tests and small apps. A queue made with another `table:` name uses
-- that name for the table, its indexes and its NOTIFY channel.

CREATE TABLE nomi_jobs (
    id           bigserial   PRIMARY KEY,
    queue        text        NOT NULL DEFAULT 'default',
    kind         text        NOT NULL,
    args         jsonb       NOT NULL,
    state        text        NOT NULL DEFAULT 'available'
                 CHECK (state IN ('available', 'running', 'completed', 'discarded')),
    attempt      integer     NOT NULL DEFAULT 0,
    max_attempts integer     NOT NULL,
    run_at       timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    locked_by    text,
    errors       jsonb       NOT NULL DEFAULT '[]',
    inserted_at  timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);

CREATE INDEX nomi_jobs_due   ON nomi_jobs (queue, run_at, id) WHERE state = 'available';
CREATE INDEX nomi_jobs_lease ON nomi_jobs (locked_until)      WHERE state = 'running';
CREATE INDEX nomi_jobs_done  ON nomi_jobs (finished_at)       WHERE state IN ('completed', 'discarded');
