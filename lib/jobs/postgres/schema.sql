-- The tables lib/jobs/postgres keeps jobs in, for a project's migrations to
-- copy. `pgjobs.install(conn)` runs these statements, with IF NOT EXISTS,
-- for tests and small apps. A queue made with another `table:` name uses
-- that name for the table, its indexes, its schedules table
-- (`<table>_schedules`) and its NOTIFY channel. `schema_upgrade.sql` brings
-- a table made from an earlier version of this file up to it.

CREATE TABLE nomi_jobs (
    id            bigserial   PRIMARY KEY,
    queue         text        NOT NULL DEFAULT 'default',
    kind          text        NOT NULL,
    args          jsonb       NOT NULL,
    state         text        NOT NULL DEFAULT 'available'
                  CHECK (state IN ('available', 'running', 'completed', 'discarded')),
    attempt       integer     NOT NULL DEFAULT 0,
    max_attempts  integer     NOT NULL,
    run_at        timestamptz NOT NULL DEFAULT now(),
    locked_until  timestamptz,
    locked_by     text,
    errors        jsonb       NOT NULL DEFAULT '[]',
    inserted_at   timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    priority      integer     NOT NULL DEFAULT 0,
    unique_key    text,
    unique_states text        CHECK (unique_states IN ('live', 'kept'))
);

-- Due jobs, highest priority first, then oldest.
CREATE INDEX nomi_jobs_ready ON nomi_jobs (queue, priority DESC, run_at, id) WHERE state = 'available';
CREATE INDEX nomi_jobs_lease ON nomi_jobs (locked_until)                    WHERE state = 'running';
CREATE INDEX nomi_jobs_done  ON nomi_jobs (finished_at)                     WHERE state IN ('completed', 'discarded');

-- A job holds its unique key while it is available or running, or, when its
-- unique_states is 'kept', while the table keeps it.
CREATE UNIQUE INDEX nomi_jobs_unique ON nomi_jobs (unique_key)
    WHERE unique_key IS NOT NULL AND (unique_states = 'kept' OR state IN ('available', 'running'));

-- Each schedule's latest tick a scheduler reached.
CREATE TABLE nomi_jobs_schedules (
    name      text        PRIMARY KEY,
    last_tick timestamptz NOT NULL
);
