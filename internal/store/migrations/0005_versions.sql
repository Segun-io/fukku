-- Second versions of films (#56, decision #20): files Fukku placed outside Radarr's root
-- folders and grouped with the film in the media server. No foreign key: a record outlives
-- the job that placed it, because the grouping must be checked and re-applied for as long as
-- the file exists. tmdb_id finds the film again when it is deleted and added back to Radarr
-- (a new movie id). Times are Unix nanoseconds (UTC); NULL means "not set".
CREATE TABLE versions (
    id                 INTEGER PRIMARY KEY,
    job_id             TEXT    NOT NULL,
    app                TEXT    NOT NULL,
    title_id           INTEGER NOT NULL,
    tmdb_id            INTEGER NOT NULL DEFAULT 0,
    title              TEXT    NOT NULL DEFAULT '',
    year               INTEGER NOT NULL DEFAULT 0,
    path               TEXT    NOT NULL UNIQUE,
    label              TEXT    NOT NULL DEFAULT '',
    status             TEXT    NOT NULL,
    detail             TEXT    NOT NULL DEFAULT '',
    primary_file_id    INTEGER NOT NULL DEFAULT 0,
    primary_path       TEXT    NOT NULL DEFAULT '',
    primary_item_id    TEXT    NOT NULL DEFAULT '',
    item_id            TEXT    NOT NULL DEFAULT '',
    created_at         INTEGER NOT NULL,
    checked_at         INTEGER,
    grouped_at         INTEGER,
    primary_changed_at INTEGER
) STRICT;

CREATE INDEX versions_job ON versions (job_id, id);
