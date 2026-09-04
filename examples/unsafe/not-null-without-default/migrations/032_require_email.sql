-- No DEFAULT is supplied, so Postgres cannot backfill a value for a row
-- an old writer inserts without email populated: the write is rejected
-- outright once this constraint commits.
ALTER TABLE users ALTER COLUMN email SET NOT NULL;
