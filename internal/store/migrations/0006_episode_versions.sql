-- Second versions of episodes (#58, decision #20): a season dub kept next to the episode's
-- own file in Sonarr's season folder, grouped by Jellyfin by its SxxEyy. The record says which
-- episode it is a version of: Sonarr's episode id, and the season and episode numbers its
-- name carries. 0 for films' versions.
ALTER TABLE versions ADD COLUMN episode_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE versions ADD COLUMN season_number INTEGER NOT NULL DEFAULT 0;
ALTER TABLE versions ADD COLUMN episode_number INTEGER NOT NULL DEFAULT 0;
