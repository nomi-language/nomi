-- Brings a nomi_jobs table that has no priorities, unique keys or schedules
-- up to schema.sql, for a project's migrations to copy: a project that made
-- its table from schema.sql adds this file as a new migration each time it
-- upgrades pgjobs. `pgjobs.queue` is an error naming what a table lacks
-- until then. `pgjobs.install` runs these statements too. The ready index
-- takes the place of nomi_jobs_due, which orders due jobs without their
-- priority.

ALTER TABLE nomi_jobs ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 0;
ALTER TABLE nomi_jobs ADD COLUMN IF NOT EXISTS unique_key text;
ALTER TABLE nomi_jobs ADD COLUMN IF NOT EXISTS unique_states text CHECK (unique_states IN ('live', 'kept'));

DROP INDEX IF EXISTS nomi_jobs_due;
CREATE INDEX IF NOT EXISTS nomi_jobs_ready ON nomi_jobs (queue, priority DESC, run_at, id) WHERE state = 'available';

CREATE UNIQUE INDEX IF NOT EXISTS nomi_jobs_unique ON nomi_jobs (unique_key)
    WHERE unique_key IS NOT NULL AND (unique_states = 'kept' OR state IN ('available', 'running'));

CREATE TABLE IF NOT EXISTS nomi_jobs_schedules (
    name      text        PRIMARY KEY,
    last_tick timestamptz NOT NULL
);
