-- Widening: integer -> bigint never rejects a value an old writer sends.
ALTER TABLE users ALTER COLUMN user_id TYPE bigint;
