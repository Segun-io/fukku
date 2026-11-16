-- Jobs, their files, unfinished uploads, state changes and the audit log (plan §9, §12).
-- Times are Unix nanoseconds (UTC); NULL means "not set". Read them in sqlite3 with
-- datetime(created_at / 1000000000, 'unixepoch').

CREATE TABLE jobs (
    seq        INTEGER PRIMARY KEY, -- insertion order: "newest first" within the same instant
    id         TEXT    NOT NULL UNIQUE,
    owner      TEXT    NOT NULL,
    state      TEXT    NOT NULL,
    resume     TEXT    NOT NULL DEFAULT '',
    attempt    INTEGER NOT NULL DEFAULT 1,
    error_code TEXT    NOT NULL DEFAULT '',
    error_text TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER                  -- NULL: never
) STRICT;

CREATE INDEX jobs_owner ON jobs (owner, seq);
CREATE INDEX jobs_state ON jobs (state, seq);
CREATE INDEX jobs_expires ON jobs (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE files (
    id        INTEGER PRIMARY KEY,
    job_id    TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    path      TEXT    NOT NULL,
    kind      TEXT    NOT NULL,
    size      INTEGER NOT NULL,
    status    TEXT    NOT NULL,
    reason    TEXT    NOT NULL DEFAULT '',
    from_path TEXT    NOT NULL DEFAULT '',
    report    BLOB,
    UNIQUE (job_id, path)
) STRICT;

CREATE TABLE uploads (
    id          TEXT    PRIMARY KEY,
    job_id      TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    owner       TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    size        INTEGER NOT NULL,
    offset_     INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    progress_at INTEGER
) STRICT;

CREATE INDEX uploads_job ON uploads (job_id);
CREATE INDEX uploads_owner ON uploads (owner);

CREATE TABLE events (
    id         INTEGER PRIMARY KEY,
    job_id     TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    at         INTEGER NOT NULL,
    from_state TEXT    NOT NULL,
    to_state   TEXT    NOT NULL,
    actor      TEXT    NOT NULL,
    note       TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX events_job ON events (job_id, id);

-- No foreign key: audit entries outlive their job.
CREATE TABLE audit (
    id     INTEGER PRIMARY KEY,
    at     INTEGER NOT NULL,
    actor  TEXT    NOT NULL,
    email  TEXT    NOT NULL DEFAULT '',
    action TEXT    NOT NULL,
    job_id TEXT    NOT NULL DEFAULT '',
    detail TEXT                         -- JSON object of strings; NULL: none
) STRICT;

CREATE INDEX audit_job ON audit (job_id, id);
