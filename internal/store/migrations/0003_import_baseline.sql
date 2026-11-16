-- What the app tracked for an import's titles before the first submission (#40): the import
-- engine's JSON, NULL when not recorded.
ALTER TABLE imports ADD COLUMN baseline BLOB;
