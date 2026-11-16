-- M2: what the user chose and what the app answered (plan §9). Times are Unix nanoseconds
-- (UTC); NULL means "not set". Everything here goes with its job (ON DELETE CASCADE).

-- The title a job was matched to; at most one per job.
CREATE TABLE targets (
    job_id     TEXT    PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
    app        TEXT    NOT NULL,
    title_id   INTEGER NOT NULL,
    title      TEXT    NOT NULL,
    year       INTEGER NOT NULL DEFAULT 0,
    options    BLOB,
    matched_by TEXT    NOT NULL DEFAULT '',
    matched_at INTEGER
) STRICT;

-- "One active job per app and title" looks jobs up by this.
CREATE INDEX targets_title ON targets (app, title_id);

-- One file's place in the import. pos keeps the order PutRows was given.
CREATE TABLE import_rows (
    job_id   TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    id       TEXT    NOT NULL,
    file_id  INTEGER NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    pos      INTEGER NOT NULL,
    skip     INTEGER NOT NULL DEFAULT 0,
    edit     BLOB,
    proposal BLOB,
    override INTEGER NOT NULL DEFAULT 0,
    result   TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (job_id, id)
) STRICT;

CREATE INDEX import_rows_file ON import_rows (file_id);

-- The bookkeeping of one import attempt; at most one per job.
CREATE TABLE imports (
    job_id       TEXT    PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
    command_id   INTEGER NOT NULL DEFAULT 0,
    submitted_at INTEGER,
    finished_at  INTEGER
) STRICT;

-- One post-processing step's state. seq keeps the order the steps were first recorded.
CREATE TABLE post_steps (
    seq        INTEGER PRIMARY KEY,
    job_id     TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    status     TEXT    NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    detail     TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER,
    UNIQUE (job_id, name)
) STRICT;
