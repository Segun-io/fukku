-- Where the count of the app's refusals in a row starts for an import (#74): the number of
-- the job's events when the app last answered a whole pass of it. 0: from the start.
ALTER TABLE imports ADD COLUMN refusals_from INTEGER NOT NULL DEFAULT 0;
