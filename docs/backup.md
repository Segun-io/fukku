# Backup and restore

Fukku keeps its state in one SQLite file, `<server.dataDir>/fukku.db` (by default
`/var/lib/fukku/fukku.db`): jobs and their files, unfinished uploads, every state change and
the audit log. That file is what to back up (plan §13). The uploaded media in the intake
root is not in it, and uploads still in progress are not backed up.

## Online backup

The web process can keep running. SQLite's online backup copies a consistent snapshot even
while jobs are changing:

```sh
sqlite3 /var/lib/fukku/fukku.db ".backup '/backups/fukku.db'"
```

Replace `/backups/fukku.db` with where the copy should go (for a dated copy,
`".backup '/backups/fukku-$(date +%F).db'"`). Then check the copy; it prints `ok`:

```sh
sqlite3 /backups/fukku.db "PRAGMA integrity_check"
```

- **Do not copy the file with `cp` or a file-level backup tool while Fukku runs.** The
  database is in WAL mode: recent changes live in `fukku.db-wal` until they are folded back,
  so a plain copy can be stale or torn.
- `sqlite3` must run on the host that has the data directory (never over a network share),
  as a user that may write there (SQLite updates `fukku.db-shm` while it reads): the user the
  container runs as (uid 10001 in the published image) or root. It needs `sqlite3` 3.37 or
  newer: the tables are STRICT tables, which older versions cannot read.
- The Fukku image does not include `sqlite3`. With the reference compose file the data is the
  `fukku-data` volume: run the command on the host against the volume's directory, or in a
  one-off container that has `sqlite3` and mounts the same volume at `/var/lib/fukku`.
  The development image (`tools/dev/Dockerfile`) has it.

## Restore

1. Stop the web process (`fukku web`, or `fukku combined`).
2. In the data directory, move `fukku.db`, `fukku.db-wal` and `fukku.db-shm` aside.
   (`fukku.lock` is only the lock the running process holds; it needs no backup.)
3. Copy the backup to `fukku.db`, owned by the user Fukku runs as, mode `0600`.
4. Start Fukku. It applies any migrations the copy is missing. A copy made by a **newer**
   Fukku is refused; restore it with that version or a later one.

Jobs that moved on after the backup was taken (an import that finished, a job deleted by
retention) come back in their older state, so check the job list after a restore.
