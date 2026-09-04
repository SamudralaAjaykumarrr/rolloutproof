-- Drops the legacy email column. Unsafe when scheduled during a rolling
-- deployment that still permits api:v1 replicas to serve traffic.
ALTER TABLE users DROP COLUMN email;
