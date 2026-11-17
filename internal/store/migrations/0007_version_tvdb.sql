-- The re-check of episodes' versions (#96): the series' TVDB id, so that a Sonarr series id
-- that now answers with another show (a restored or rebuilt Sonarr, another instance) is never
-- taken for the version's series, and a series deleted and added back under a new id is found
-- again. 0 for films' versions, and for an episode's version whose series is not known yet.
ALTER TABLE versions ADD COLUMN tvdb_id INTEGER NOT NULL DEFAULT 0;
