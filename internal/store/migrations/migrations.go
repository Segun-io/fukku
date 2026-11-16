// Package migrations holds the database schema history of package store, as numbered SQL
// files embedded in the binary.
//
// # Files
//
// Each file is NNNN_name.sql (four digits, then lower-case letters, digits and '_'),
// numbered 0001, 0002, … without gaps. A released file is never edited: a change is a new
// file. The database's PRAGMA user_version is the number of the last file applied; a
// database with a higher number than the binary has is refused.
//
// # How a file is applied
//
// At start-up, store.Open applies the missing files in order, each in one transaction
// (BEGIN IMMEDIATE) that also sets user_version, on the one connection that writes. Inside
// the transaction it re-reads user_version and skips a file that is already applied, so a
// file never runs twice. A file that fails is rolled back completely and Open fails.
//
// # Foreign keys
//
// Foreign keys are on. In a transaction PRAGMA foreign_keys cannot be changed, so a file
// that rebuilds a table (CREATE the new table, copy the rows, DROP the old one, RENAME)
// would cascade: dropping a parent table deletes the rows that refer to it. Such a file
// starts with this line:
//
//	-- fukku:foreign_keys=off
//
// It is then applied as SQLite's "making other kinds of table schema changes" procedure
// prescribes: PRAGMA foreign_keys=OFF outside the transaction, BEGIN, the file, PRAGMA
// foreign_key_check (any violation rolls everything back and Open fails), set
// user_version, COMMIT, and PRAGMA foreign_keys=ON again.
package migrations

import "embed"

// FS holds the migration files.
//
//go:embed *.sql
var FS embed.FS

// ForeignKeysOff is the first line of a file that must run with foreign keys off.
const ForeignKeysOff = "-- fukku:foreign_keys=off"
