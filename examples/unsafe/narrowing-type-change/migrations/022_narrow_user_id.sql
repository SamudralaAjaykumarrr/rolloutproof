-- Narrowing: bigint -> integer can overflow-reject a value an old writer
-- still sends.
ALTER TABLE users ALTER COLUMN user_id TYPE integer;
